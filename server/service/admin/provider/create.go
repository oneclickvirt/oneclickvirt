package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"oneclickvirt/global"
	"oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/service/database"
	"oneclickvirt/utils"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// CreateProvider 创建Provider
func (s *Service) CreateProvider(req admin.CreateProviderRequest) error {
	global.APP_LOG.Debug("开始创建Provider",
		zap.String("name", utils.TruncateString(req.Name, 32)),
		zap.String("type", req.Type),
		zap.String("endpoint", utils.TruncateString(req.Endpoint, 64)))

	// 1. 检查Provider名称是否已存在
	var existingNameCount int64
	if err := global.APP_DB.Model(&providerModel.Provider{}).Where("name = ?", req.Name).Count(&existingNameCount).Error; err != nil {
		global.APP_LOG.Error("检查Provider名称失败", zap.Error(err))
		return fmt.Errorf("检查Provider名称失败: %v", err)
	}
	if existingNameCount > 0 {
		global.APP_LOG.Warn("Provider创建失败：名称已存在",
			zap.String("name", utils.TruncateString(req.Name, 32)))
		return fmt.Errorf("Provider名称 '%s' 已存在，请使用其他名称", req.Name)
	}

	// 2. 检查SSH地址和端口组合是否已存在（防止配置相同节点）
	if req.Endpoint != "" {
		sshPort := req.SSHPort
		if sshPort == 0 {
			sshPort = 22 // 默认SSH端口
		}
		var existingEndpointCount int64
		if err := global.APP_DB.Model(&providerModel.Provider{}).
			Where("endpoint = ? AND ssh_port = ?", req.Endpoint, sshPort).
			Count(&existingEndpointCount).Error; err != nil {
			global.APP_LOG.Error("检查Provider SSH地址失败", zap.Error(err))
			return fmt.Errorf("检查Provider SSH地址失败: %v", err)
		}
		if existingEndpointCount > 0 {
			global.APP_LOG.Warn("Provider创建失败：SSH地址和端口组合已存在",
				zap.String("endpoint", utils.TruncateString(req.Endpoint, 64)),
				zap.Int("sshPort", sshPort))
			return fmt.Errorf("SSH地址 '%s:%d' 已被其他Provider使用，请检查是否重复配置", req.Endpoint, sshPort)
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
					return fmt.Errorf("过期时间格式错误，请使用 'YYYY-MM-DD HH:MM:SS' 或 'YYYY-MM-DD' 格式")
				}
			}
		}
		expiresAt = &t
	} else {
		// 默认31天后过期
		defaultExpiry := time.Now().AddDate(0, 0, 31)
		expiresAt = &defaultExpiry
	}

	// 验证：必须提供密码或SSH密钥其中一种
	if req.Password == "" && req.SSHKey == "" {
		global.APP_LOG.Warn("Provider创建失败：未提供SSH认证方式",
			zap.String("name", utils.TruncateString(req.Name, 32)))
		return fmt.Errorf("必须提供SSH密码或SSH密钥其中一种认证方式")
	}

	provider := providerModel.Provider{
		Name:                  req.Name,
		Type:                  req.Type,
		Endpoint:              req.Endpoint,
		PortIP:                req.PortIP,
		SSHPort:               req.SSHPort,
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
		ContainerEnabled:      req.ContainerEnabled,
		VirtualMachineEnabled: req.VirtualMachineEnabled,
		TotalQuota:            req.TotalQuota,
		AllowClaim:            req.AllowClaim,
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
		MaxTraffic:        req.MaxTraffic,
		TrafficCountMode:  req.TrafficCountMode,
		TrafficMultiplier: req.TrafficMultiplier,
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
		ContainerPrivileged:   req.ContainerPrivileged,
		ContainerAllowNesting: req.ContainerAllowNesting,
		ContainerEnableLXCFS:  req.ContainerEnableLXCFS,
		ContainerCPUAllowance: req.ContainerCPUAllowance,
		ContainerMemorySwap:   req.ContainerMemorySwap,
		ContainerMaxProcesses: req.ContainerMaxProcesses,
		ContainerDiskIOLimit:  req.ContainerDiskIOLimit,
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
			return fmt.Errorf("节点等级限制配置格式错误: %v", err)
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
			return fmt.Errorf("节点等级限制配置格式错误: %v", err)
		}
		provider.LevelLimits = string(levelLimitsJSON)
		global.APP_LOG.Info("使用默认节点等级限制配置",
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
		return fmt.Errorf("流量采集间隔不能超过300秒（5分钟），当前值: %d秒", req.TrafficCollectInterval)
	}
	// 端口映射方式默认值
	// Docker 类型固定使用 native
	if provider.Type == "docker" {
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
	if provider.ContainerCPUAllowance == "" {
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
		return err
	}

	global.APP_LOG.Info("Provider创建成功",
		zap.String("name", utils.TruncateString(req.Name, 32)),
		zap.String("type", req.Type),
		zap.String("endpoint", utils.TruncateString(req.Endpoint, 64)))
	return nil
}
