package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"oneclickvirt/global"
	"oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	agentService "oneclickvirt/service/agent"
	"oneclickvirt/service/database"
	"oneclickvirt/utils"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

func init() {
	// 注册 Agent 连接回调：当 Agent 模式节点上线后，自动执行延迟的实例发现与导入。
	agentService.OnAgentConnected = func(providerID uint) {
		svc := &Service{}
		// 从 DB 读取发现参数（已在 triggerPendingDiscovery 中清除了 pending_discovery 标记）
		var p providerModel.Provider
		if err := global.APP_DB.Select("discovery_owner_user_id, discovery_auto_import, discovery_auto_adjust").
			Where("id = ?", providerID).First(&p).Error; err != nil {
			global.APP_LOG.Warn("OnAgentConnected: 查询 Provider 发现参数失败",
				zap.Uint("providerID", providerID), zap.Error(err))
			return
		}
		ownerUserID := p.DiscoveryOwnerUserID
		if ownerUserID == 0 {
			ownerUserID = 1 // 默认管理员
		}
		svc.discoverAndImportInstances(providerID, p.DiscoveryAutoImport, p.DiscoveryAutoAdjust, ownerUserID)
	}
}

// maskAuthMethod 掩码认证方法，用于日志输出，避免暴露敏感信息
func maskAuthMethod(password, sshKey string) string {
	if password != "" {
		return "password:***"
	}
	if sshKey != "" {
		return "sshKey:***"
	}
	return "none"
}

func loadAdminGroupForProvider(ownerAdminID uint) (string, string) {
	var setting providerModel.AdminGroupSetting
	if err := global.APP_DB.Select("group_name, group_description").
		Where("owner_admin_id = ?", ownerAdminID).
		First(&setting).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			global.APP_LOG.Warn("读取管理员分组设置失败，将使用默认分组",
				zap.Uint("ownerAdminID", ownerAdminID),
				zap.Error(err))
		}
		return "测试", ""
	}
	if setting.GroupName == "" {
		setting.GroupName = "测试"
	}
	return setting.GroupName, setting.GroupDescription
}

// CreateProvider 创建Provider
func (s *Service) CreateProvider(req admin.CreateProviderRequest, ownerAdminID uint) (*providerModel.Provider, error) {
	global.APP_LOG.Info("开始创建Provider",
		zap.String("name", utils.TruncateString(req.Name, 32)),
		zap.String("type", req.Type),
		zap.String("endpoint", utils.TruncateString(req.Endpoint, 64)),
		zap.String("auth", maskAuthMethod(req.Password, req.SSHKey)))

	// 1. 检查Provider名称是否已存在
	var existingNameCount int64
	if err := global.APP_DB.Model(&providerModel.Provider{}).Where("name = ?", req.Name).Count(&existingNameCount).Error; err != nil {
		global.APP_LOG.Error("检查Provider名称失败", zap.Error(err))
		return nil, fmt.Errorf("检查Provider名称失败: %v", err)
	}
	if existingNameCount > 0 {
		global.APP_LOG.Warn("Provider创建失败：名称已存在",
			zap.String("name", utils.TruncateString(req.Name, 32)))
		return nil, fmt.Errorf("Provider名称 '%s' 已存在，请使用其他名称", req.Name)
	}

	normalizedEndpoint, normalizedSSHPort := normalizeProviderEndpointAndPort(req.Endpoint, req.SSHPort)

	// 2. 检查SSH地址和端口组合是否已存在（防止配置相同节点）
	if normalizedEndpoint != "" {
		var existingEndpointCount int64
		if err := global.APP_DB.Model(&providerModel.Provider{}).
			Where("endpoint = ? AND ssh_port = ?", normalizedEndpoint, normalizedSSHPort).
			Count(&existingEndpointCount).Error; err != nil {
			global.APP_LOG.Error("检查Provider SSH地址失败", zap.Error(err))
			return nil, fmt.Errorf("检查Provider SSH地址失败: %v", err)
		}
		if existingEndpointCount > 0 {
			global.APP_LOG.Warn("Provider创建失败：SSH地址和端口组合已存在",
				zap.String("endpoint", utils.TruncateString(normalizedEndpoint, 64)),
				zap.Int("sshPort", normalizedSSHPort))
			return nil, fmt.Errorf("SSH地址 '%s:%d' 已被其他Provider使用，请检查是否重复配置", normalizedEndpoint, normalizedSSHPort)
		}
	}

	// 解析过期时间
	var expiresAt *time.Time
	if req.ExpiresAt != "" {
		// 尝试解析多种时间格式
		var t time.Time
		var err error

		// 首先尝试ISO 8601格式（前端默认格式）
		t, err = time.Parse(time.RFC3339, req.ExpiresAt)
		if err != nil {
			// 尝试标准日期时间格式
			t, err = time.Parse("2006-01-02 15:04:05", req.ExpiresAt)
			if err != nil {
				// 尝试日期格式
				t, err = time.Parse("2006-01-02", req.ExpiresAt)
				if err != nil {
					global.APP_LOG.Warn("Provider创建失败：过期时间格式错误",
						zap.String("name", utils.TruncateString(req.Name, 32)),
						zap.String("expiresAt", utils.TruncateString(req.ExpiresAt, 32)))
					return nil, fmt.Errorf("过期时间格式错误，请使用 'YYYY-MM-DD HH:MM:SS' 或 'YYYY-MM-DD' 格式")
				}
			}
		}
		expiresAt = &t
	} else {
		// 默认31天后过期
		defaultExpiry := time.Now().AddDate(0, 0, 31)
		expiresAt = &defaultExpiry
	}

	// 验证：SSH直连模式下必须提供密码或SSH密钥其中一种；agent模式无需SSH凭据
	if req.ConnectionType != "agent" && req.Password == "" && req.SSHKey == "" {
		global.APP_LOG.Warn("Provider创建失败：未提供SSH认证方式",
			zap.String("name", utils.TruncateString(req.Name, 32)))
		return nil, fmt.Errorf("SSH直连模式必须提供SSH密码或SSH密钥其中一种认证方式")
	}

	groupName, groupDescription := loadAdminGroupForProvider(ownerAdminID)

	providerType := utils.NormalizeProviderType(req.Type)
	containerOptionsSupported := utils.IsLXDIncusProvider(providerType)
	containerPrivileged := false
	containerAllowNesting := false
	containerEnableLXCFS := false
	containerCPUAllowance := ""
	containerMemorySwap := false
	containerMaxProcesses := 0
	containerDiskIOLimit := ""
	if containerOptionsSupported {
		containerPrivileged = req.ContainerPrivileged
		containerAllowNesting = req.ContainerAllowNesting
		containerEnableLXCFS = req.ContainerEnableLXCFS
		containerCPUAllowance = req.ContainerCPUAllowance
		containerMemorySwap = req.ContainerMemorySwap
		containerMaxProcesses = req.ContainerMaxProcesses
		containerDiskIOLimit = req.ContainerDiskIOLimit
	}
	gpuEnabled, gpuDeviceIDs, err := normalizeProviderGPUConfig(providerType, req.GpuEnabled, req.GpuDeviceIds)
	if err != nil {
		return nil, err
	}
	containerEnabled, vmEnabled := normalizeProviderInstanceTypeCapabilities(providerType, req.ContainerEnabled, req.VirtualMachineEnabled)
	trafficAction, trafficSpeedKbps := normalizeTrafficOverLimitPolicy(req.TrafficOverLimitAction, req.TrafficSpeedLimitKbps)
	expiryAction, expiryExtendDays := normalizeInstanceExpiryPolicy(req.InstanceExpiryAction, req.InstanceExpiryExtendDays)
	trafficQuotaVisible := true
	if req.TrafficQuotaVisible != nil {
		trafficQuotaVisible = *req.TrafficQuotaVisible
	}

	provider := providerModel.Provider{
		Name:                  req.Name,
		Type:                  providerType,
		Endpoint:              normalizedEndpoint,
		PortIP:                req.PortIP,
		SSHPort:               normalizedSSHPort,
		Username:              req.Username,
		Password:              req.Password,
		SSHKey:                req.SSHKey,
		Token:                 req.Token,
		Config:                req.Config,
		Region:                req.Region,
		Country:               req.Country,
		CountryCode:           req.CountryCode,
		City:                  req.City,
		Architecture:          req.Architecture,
		ContainerEnabled:      containerEnabled,
		VirtualMachineEnabled: vmEnabled,
		TotalQuota:            req.TotalQuota,
		AllowClaim:            req.AllowClaim,
		RedeemCodeOnly:        req.RedeemCodeOnly,
		Status:                "active",
		ExpiresAt:             expiresAt,
		IsFrozen:              false,
		MaxContainerInstances: req.MaxContainerInstances,
		MaxVMInstances:        req.MaxVMInstances,
		AllowConcurrentTasks:  req.AllowConcurrentTasks,
		MaxConcurrentTasks:    req.MaxConcurrentTasks,
		TaskPollInterval:      req.TaskPollInterval,
		EnableTaskPolling:     req.EnableTaskPolling,
		// 存储配置（所有Provider类型通用）
		StoragePool: req.StoragePool,
		// StoragePoolPath 将在健康检查时自动检测并填充
		// 操作执行配置
		ExecutionRule: req.ExecutionRule,
		// Proxmox 网桥配置
		NodeInstallType:   req.NodeInstallType,
		BridgeNAT:         req.BridgeNAT,
		BridgeDedicatedV4: req.BridgeDedicatedV4,
		BridgeDedicatedV6: req.BridgeDedicatedV6,
		NATSubnet:         req.NATSubnet,
		// 端口映射配置
		DefaultPortCount: req.DefaultPortCount,
		PortRangeStart:   req.PortRangeStart,
		PortRangeEnd:     req.PortRangeEnd,
		NetworkType:      req.NetworkType,
		// 带宽配置
		DefaultInboundBandwidth:  req.DefaultInboundBandwidth,
		DefaultOutboundBandwidth: req.DefaultOutboundBandwidth,
		MaxInboundBandwidth:      req.MaxInboundBandwidth,
		MaxOutboundBandwidth:     req.MaxOutboundBandwidth,
		// 流量管理
		MaxTraffic:               req.MaxTraffic,
		TrafficCountMode:         req.TrafficCountMode,
		TrafficMultiplier:        req.TrafficMultiplier,
		TrafficSyncMethod:        req.TrafficSyncMethod,
		EnableTrafficControl:     req.EnableTrafficControl,
		EnableResourceMonitoring: req.EnableResourceMonitoring,
		TrafficOverLimitAction:   trafficAction,
		TrafficSpeedLimitKbps:    trafficSpeedKbps,
		TrafficQuotaVisible:      trafficQuotaVisible,
		InstanceExpiryAction:     expiryAction,
		InstanceExpiryExtendDays: expiryExtendDays,
		// 端口映射方式
		IPv4PortMappingMethod: req.IPv4PortMappingMethod,
		IPv6PortMappingMethod: req.IPv6PortMappingMethod,
		// SSH连接配置
		SSHConnectTimeout: req.SSHConnectTimeout,
		SSHExecuteTimeout: req.SSHExecuteTimeout,
		// 容器资源限制配置
		ContainerLimitCPU:    req.ContainerLimitCpu,
		ContainerLimitMemory: req.ContainerLimitMemory,
		ContainerLimitDisk:   req.ContainerLimitDisk,
		// 虚拟机资源限制配置
		VMLimitCPU:    req.VMLimitCpu,
		VMLimitMemory: req.VMLimitMemory,
		VMLimitDisk:   req.VMLimitDisk,
		// 容器特殊配置选项（仅 LXD/Incus 容器）
		ContainerPrivileged:   containerPrivileged,
		ContainerAllowNesting: containerAllowNesting,
		ContainerEnableLXCFS:  containerEnableLXCFS,
		ContainerCPUAllowance: containerCPUAllowance,
		ContainerMemorySwap:   containerMemorySwap,
		ContainerMaxProcesses: containerMaxProcesses,
		ContainerDiskIOLimit:  containerDiskIOLimit,
		// GPU直通配置
		GpuEnabled:   gpuEnabled,
		GpuDeviceIds: gpuDeviceIDs,
		// 内网穿透连接模式（默认 ssh）
		ConnectionType: func() string {
			if req.ConnectionType == "agent" {
				return "agent"
			}
			return "ssh"
		}(),
		// 普通管理员归属
		OwnerAdminID:     ownerAdminID,
		GroupName:        groupName,
		GroupDescription: groupDescription,
	}

	// Agent 模式默认值：已部署 agent，开箱即用
	if provider.ConnectionType == "agent" {
		provider.EnableTrafficControl = true
		provider.EnableResourceMonitoring = true
		provider.TrafficSyncMethod = "agent"
		// Agent 模式不保存 SSH endpoint；端口映射能力由 portIP 明确控制。
		if req.PortIP == "" || req.NetworkType == "" {
			provider.NetworkType = "no_port_mapping"
		}
	}

	// 节点级别等级限制配置
	if len(req.LevelLimits) > 0 {
		// 转换前端发送的 camelCase 为存储的 kebab-case
		convertedLimits := make(map[int]map[string]interface{})
		for level, limits := range req.LevelLimits {
			convertedLimit := make(map[string]interface{})
			for key, value := range limits {
				// 转换 camelCase 键为 kebab-case
				switch key {
				case "maxInstances":
					convertedLimit["max-instances"] = value
				case "maxResources":
					convertedLimit["max-resources"] = value
				case "maxTraffic":
					convertedLimit["max-traffic"] = value
				default:
					// 保留其他键不变（已经是正确格式或未知字段）
					convertedLimit[key] = value
				}
			}
			convertedLimits[level] = convertedLimit
		}
		// 将转换后的 map[int]map[string]interface{} 序列化为 JSON 字符串
		levelLimitsJSON, err := json.Marshal(convertedLimits)
		if err != nil {
			global.APP_LOG.Error("序列化节点等级限制配置失败",
				zap.String("providerName", req.Name),
				zap.Error(err))
			return nil, fmt.Errorf("节点等级限制配置格式错误: %v", err)
		}
		provider.LevelLimits = string(levelLimitsJSON)
	} else {
		// 如果没有提供等级限制，设置默认等级1的限制
		defaultLevelLimits := map[int]map[string]interface{}{
			1: {
				"max-instances": 1,
				"max-resources": map[string]interface{}{
					"cpu":       1,
					"memory":    350,
					"disk":      1025,
					"bandwidth": 100,
				},
				"max-traffic": 102400,
			},
		}
		levelLimitsJSON, err := json.Marshal(defaultLevelLimits)
		if err != nil {
			global.APP_LOG.Error("序列化默认节点等级限制配置失败",
				zap.String("providerName", req.Name),
				zap.Error(err))
			return nil, fmt.Errorf("节点等级限制配置格式错误: %v", err)
		}
		provider.LevelLimits = string(levelLimitsJSON)
		global.APP_LOG.Debug("使用默认节点等级限制配置",
			zap.String("providerName", req.Name))
	}

	// 设置默认值
	// 并发控制默认值：默认不允许并发，最大并发数为1
	if !provider.AllowConcurrentTasks && provider.MaxConcurrentTasks <= 0 {
		provider.MaxConcurrentTasks = 1
	}
	if provider.MaxConcurrentTasks <= 0 {
		provider.MaxConcurrentTasks = 1
	}
	if provider.TaskPollInterval <= 0 {
		provider.TaskPollInterval = 60
	}
	// 操作执行配置默认值
	if provider.ExecutionRule == "" {
		provider.ExecutionRule = "auto"
	}
	// 端口映射默认值
	if provider.DefaultPortCount <= 0 {
		provider.DefaultPortCount = 10
	}
	if provider.PortRangeStart <= 0 {
		provider.PortRangeStart = 10000
	}
	if provider.PortRangeEnd <= 0 {
		provider.PortRangeEnd = 65535
	}
	if provider.NetworkType == "" {
		provider.NetworkType = "nat_ipv4"
	}
	// 带宽配置默认值
	if provider.DefaultInboundBandwidth <= 0 {
		provider.DefaultInboundBandwidth = 300
	}
	if provider.DefaultOutboundBandwidth <= 0 {
		provider.DefaultOutboundBandwidth = 300
	}
	if provider.MaxInboundBandwidth <= 0 {
		provider.MaxInboundBandwidth = 1000
	}
	if provider.MaxOutboundBandwidth <= 0 {
		provider.MaxOutboundBandwidth = 1000
	}
	// 流量限制默认值：1TB
	if provider.MaxTraffic <= 0 {
		provider.MaxTraffic = 1048576 // 1TB = 1048576MB
	}
	// 流量统计控制默认值：不启用
	// EnableTrafficControl字段由数据库默认值处理（default:false），这里不需要手动设置
	// 流量统计模式默认值
	if provider.TrafficCountMode == "" {
		provider.TrafficCountMode = "both" // 默认双向统计
	}
	// 流量计费倍率默认值
	if provider.TrafficMultiplier == 0 {
		provider.TrafficMultiplier = 1.0 // 默认1.0倍
	}
	// 流量采集间隔验证：最大不超过5分钟（300秒），因为数据聚合精度为5分钟
	if req.TrafficCollectInterval > 300 {
		return nil, fmt.Errorf("流量采集间隔不能超过300秒（5分钟），当前值: %d秒", req.TrafficCollectInterval)
	}
	// 端口映射方式默认值
	// Docker/Podman/Containerd/Orbstack 类型固定使用 native
	if provider.Type == "docker" || provider.Type == "podman" || provider.Type == "containerd" || provider.Type == "orbstack" {
		provider.IPv4PortMappingMethod = "native"
		provider.IPv6PortMappingMethod = "native"
	} else {
		if provider.IPv4PortMappingMethod == "" {
			provider.IPv4PortMappingMethod = "device_proxy" // 默认device_proxy
		}
		if provider.IPv6PortMappingMethod == "" {
			provider.IPv6PortMappingMethod = "device_proxy" // 默认device_proxy
		}
	}
	// SSH超时默认值
	if provider.SSHConnectTimeout <= 0 {
		provider.SSHConnectTimeout = 30 // 默认30秒连接超时
	}
	if provider.SSHExecuteTimeout <= 0 {
		provider.SSHExecuteTimeout = 300 // 默认300秒执行超时
	}
	// 容器特殊配置默认值（仅 LXD/Incus 容器）
	if (provider.Type == "lxd" || provider.Type == "incus") && provider.ContainerCPUAllowance == "" {
		provider.ContainerCPUAllowance = "100%" // 默认100% CPU使用率
	}
	provider.NextAvailablePort = provider.PortRangeStart

	// 初始化流量重置时间为下个月的1号
	now := time.Now()
	nextReset := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location())
	provider.TrafficResetAt = &nextReset

	dbService := database.GetDatabaseService()
	if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Create(&provider).Error
	}); err != nil {
		global.APP_LOG.Error("Provider创建失败",
			zap.String("name", utils.TruncateString(req.Name, 32)),
			zap.Error(err))
		return nil, err
	}

	global.APP_LOG.Info("Provider创建成功",
		zap.String("name", utils.TruncateString(req.Name, 32)),
		zap.String("type", req.Type),
		zap.String("endpoint", utils.TruncateString(req.Endpoint, 64)))

	// 如果启用了实例发现模式，则在创建成功后执行实例发现和导入
	if req.DiscoverMode {
		// 解析导入实例的所有者用户ID
		var ownerUserID uint = 1 // 默认为管理员（ID=1）
		if req.ImportedInstanceOwner != nil && *req.ImportedInstanceOwner != "" {
			// 根据用户名查询用户ID
			var user struct {
				ID uint
			}
			if err := global.APP_DB.Table("users").Select("id").Where("username = ?", *req.ImportedInstanceOwner).First(&user).Error; err != nil {
				global.APP_LOG.Warn("导入实例所有者用户不存在，使用默认管理员",
					zap.String("username", *req.ImportedInstanceOwner),
					zap.Error(err))
				ownerUserID = 1
			} else {
				ownerUserID = user.ID
			}
		}

		if provider.ConnectionType == "agent" {
			// Agent 模式：Agent 尚未连接，延迟到 Agent 上线后再执行发现与导入。
			// 将发现参数写入 Provider 记录，由 AgentHub.Register 在 Agent 连接后触发。
			if err := global.APP_DB.Model(&provider).Updates(map[string]interface{}{
				"pending_discovery":       true,
				"discovery_owner_user_id": ownerUserID,
				"discovery_auto_import":   req.AutoImport,
				"discovery_auto_adjust":   req.AutoAdjustQuota,
			}).Error; err != nil {
				global.APP_LOG.Warn("设置Agent模式延迟发现标记失败",
					zap.Uint("providerId", provider.ID),
					zap.Error(err))
			} else {
				global.APP_LOG.Info("Agent模式Provider创建成功，实例发现将在Agent连接后自动执行",
					zap.String("provider", req.Name),
					zap.Uint("providerId", provider.ID),
					zap.Uint("ownerUserID", ownerUserID))
			}
		} else {
			// SSH 模式：立即可连接，直接异步执行发现和导入
			global.APP_LOG.Info("Provider创建成功，开始发现实例",
				zap.String("provider", req.Name),
				zap.Uint("providerId", provider.ID),
				zap.Bool("autoImport", req.AutoImport),
				zap.Uint("ownerUserID", ownerUserID))

			go s.discoverAndImportInstances(provider.ID, req.AutoImport, req.AutoAdjustQuota, ownerUserID)
		}
	}

	return &provider, nil
}

// discoverAndImportInstances 发现并导入实例（异步执行）
func (s *Service) discoverAndImportInstances(providerID uint, autoImport, autoAdjustQuota bool, adminUserID uint) {
	ctx := context.Background()

	// 等待一小段时间确保Provider连接已建立
	time.Sleep(5 * time.Second)

	// 执行实例发现（agent模式下连接刚建立时增加重试）
	var (
		discoveryResult *DiscoveryResult
		err             error
	)
	for attempt := 1; attempt <= 3; attempt++ {
		discoveryResult, err = s.DiscoverProviderInstances(ctx, providerID)
		if err == nil {
			break
		}
		global.APP_LOG.Warn("Provider实例发现失败，准备重试",
			zap.Uint("providerId", providerID),
			zap.Int("attempt", attempt),
			zap.Error(err))
		if attempt < 3 {
			time.Sleep(3 * time.Second)
		}
	}
	if err != nil {
		global.APP_LOG.Error("Provider实例发现失败",
			zap.Uint("providerId", providerID),
			zap.Error(err))
		return
	}

	global.APP_LOG.Info("Provider实例发现完成",
		zap.Uint("providerId", providerID),
		zap.Int("totalInstances", discoveryResult.TotalCount),
		zap.Int("newInstances", discoveryResult.NewInstances),
		zap.Int("alreadyManaged", discoveryResult.AlreadyManaged))

	// 如果启用了自动导入且有新实例，执行导入
	if autoImport && discoveryResult.NewInstances > 0 {
		importOptions := ImportOptions{
			ProviderID:      providerID,
			InstanceUUIDs:   nil, // 导入所有新实例
			AdminUserID:     adminUserID,
			AutoAdjustQuota: autoAdjustQuota,
			MarkConflicts:   true,
		}

		importResult, err := s.ImportDiscoveredInstances(ctx, importOptions)
		if err != nil {
			global.APP_LOG.Error("Provider实例导入失败",
				zap.Uint("providerId", providerID),
				zap.Error(err))
			return
		}

		global.APP_LOG.Info("Provider实例导入完成",
			zap.Uint("providerId", providerID),
			zap.Int("attempted", importResult.TotalAttempted),
			zap.Int("success", importResult.SuccessCount),
			zap.Int("failed", importResult.FailedCount),
			zap.Int("portConflicts", importResult.PortConflicts))
	}
}
