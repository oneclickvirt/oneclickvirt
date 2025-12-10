package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"oneclickvirt/global"
	"oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"

	traffic_monitor "oneclickvirt/service/admin/traffic_monitor"
	"oneclickvirt/service/database"
	provider2 "oneclickvirt/service/provider"
	"oneclickvirt/utils"
	"strings"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Service 管理员Provider管理服务
type Service struct{}

// NewService 创建提供商管理服务
func NewService() *Service {
	return &Service{}
}

// GetProviderList 获取Provider列表
func (s *Service) GetProviderList(req admin.ProviderListRequest) ([]admin.ProviderManageResponse, int64, error) {
	global.APP_LOG.Debug("获取Provider列表",
		zap.String("name", utils.TruncateString(req.Name, 32)),
		zap.String("type", req.Type),
		zap.String("status", req.Status),
		zap.Int("page", req.Page),
		zap.Int("pageSize", req.PageSize))

	var providers []providerModel.Provider
	var total int64

	query := global.APP_DB.Model(&providerModel.Provider{})

	if req.Name != "" {
		query = query.Where("name LIKE ?", "%"+req.Name+"%")
	}
	if req.Type != "" {
		query = query.Where("type = ?", req.Type)
	}
	if req.Status != "" {
		query = query.Where("status = ?", req.Status)
	}

	if err := query.Count(&total).Error; err != nil {
		global.APP_LOG.Error("查询Provider总数失败", zap.Error(err))
		return nil, 0, err
	}

	offset := (req.Page - 1) * req.PageSize
	if err := query.Offset(offset).Limit(req.PageSize).Find(&providers).Error; err != nil {
		global.APP_LOG.Error("查询Provider列表失败", zap.Error(err))
		return nil, 0, err
	}

	// 批量查询统计数据
	var providerIDs []uint
	for _, provider := range providers {
		providerIDs = append(providerIDs, provider.ID)
	}

	// 批量统计实例数量（总数、容器、虚拟机）
	type InstanceCountResult struct {
		ProviderID     uint
		TotalCount     int64
		ContainerCount int64
		VMCount        int64
	}
	var instanceCounts []InstanceCountResult
	if len(providerIDs) > 0 {
		global.APP_DB.Model(&providerModel.Instance{}).
			Select(`provider_id,
				COUNT(*) as total_count,
				SUM(CASE WHEN instance_type = 'container' THEN 1 ELSE 0 END) as container_count,
				SUM(CASE WHEN instance_type = 'vm' THEN 1 ELSE 0 END) as vm_count`).
			Where("provider_id IN ?", providerIDs).
			Group("provider_id").
			Scan(&instanceCounts)
	}

	// 批量统计运行中的任务数量
	type TaskCountResult struct {
		ProviderID        uint
		RunningTasksCount int64
	}
	var taskCounts []TaskCountResult
	if len(providerIDs) > 0 {
		global.APP_DB.Model(&admin.Task{}).
			Select("provider_id, COUNT(*) as running_tasks_count").
			Where("provider_id IN ? AND status = ?", providerIDs, "running").
			Group("provider_id").
			Scan(&taskCounts)
	}

	// 批量查询Provider本月流量使用情况
	// 直接使用provider_traffic_histories聚合表，避免复杂的实时计算
	type TrafficUsageResult struct {
		ProviderID  uint
		UsedTraffic float64
	}
	var trafficUsages []TrafficUsageResult
	if len(providerIDs) > 0 {
		now := time.Now()
		year, month := now.Year(), int(now.Month())

		// 使用聚合表查询，性能大幅提升
		// day=0,hour=0 表示月度汇总数据
		global.APP_DB.Raw(`
			SELECT 
				pth.provider_id,
				CASE 
					WHEN p.traffic_count_mode = 'out' THEN pth.traffic_out * COALESCE(p.traffic_multiplier, 1.0)
					WHEN p.traffic_count_mode = 'in' THEN pth.traffic_in * COALESCE(p.traffic_multiplier, 1.0)
					ELSE pth.total_used * COALESCE(p.traffic_multiplier, 1.0)
				END as used_traffic
			FROM provider_traffic_histories pth
			INNER JOIN providers p ON pth.provider_id = p.id
			WHERE pth.provider_id IN ?
			  AND pth.year = ?
			  AND pth.month = ?
			  AND pth.day = 0
			  AND pth.hour = 0
			  AND p.enable_traffic_control = true
			  AND pth.deleted_at IS NULL
		`, providerIDs, year, month).Scan(&trafficUsages)
	}

	// 构建映射表
	instanceCountMap := make(map[uint]InstanceCountResult)
	for _, count := range instanceCounts {
		instanceCountMap[count.ProviderID] = count
	}

	taskCountMap := make(map[uint]int64)
	for _, count := range taskCounts {
		taskCountMap[count.ProviderID] = count.RunningTasksCount
	}

	trafficUsageMap := make(map[uint]int64)
	for _, usage := range trafficUsages {
		trafficUsageMap[usage.ProviderID] = int64(usage.UsedTraffic)
	}

	var providerResponses []admin.ProviderManageResponse
	for _, provider := range providers {
		// 从映射表中获取统计数据
		instanceCount := instanceCountMap[provider.ID]
		runningTasksCount := taskCountMap[provider.ID]
		usedTraffic := trafficUsageMap[provider.ID]

		// Docker 类型固定使用 native 端口映射方式
		if provider.Type == "docker" {
			provider.IPv4PortMappingMethod = "native"
			provider.IPv6PortMappingMethod = "native"
		}

		// 计算已分配资源（基于实例配置和limit配置）
		// UsedCPUCores, UsedMemory, UsedDisk 已经在数据库中按照limit配置计算好了
		// 这些值在创建/删除实例时由 AllocateResourcesInTx / ReleaseResourcesInTx 维护
		allocatedCPU := provider.UsedCPUCores
		allocatedMemory := provider.UsedMemory
		allocatedDisk := provider.UsedDisk

		providerResponse := admin.ProviderManageResponse{
			Provider:          provider,
			InstanceCount:     int(instanceCount.TotalCount),
			HealthStatus:      "healthy",
			RunningTasksCount: int(runningTasksCount),
			// 包含资源信息
			NodeCPUCores:     provider.NodeCPUCores,
			NodeMemoryTotal:  provider.NodeMemoryTotal,
			NodeDiskTotal:    provider.NodeDiskTotal,
			ResourceSynced:   provider.ResourceSynced,
			ResourceSyncedAt: provider.ResourceSyncedAt,
			// 认证方式标识
			AuthMethod: provider.GetAuthMethod(),
			// 资源占用情况（已分配/总量）
			AllocatedCPUCores: allocatedCPU,
			AllocatedMemory:   allocatedMemory,
			AllocatedDisk:     allocatedDisk,
			// 实例数量统计
			CurrentContainerCount: int(instanceCount.ContainerCount),
			CurrentVMCount:        int(instanceCount.VMCount),
			// 流量使用情况
			UsedTraffic: usedTraffic,
		}
		providerResponses = append(providerResponses, providerResponse)
	}

	global.APP_LOG.Debug("Provider列表查询成功",
		zap.Int64("total", total),
		zap.Int("count", len(providerResponses)))
	return providerResponses, total, nil
}

// UpdateProvider 更新Provider
func (s *Service) UpdateProvider(req admin.UpdateProviderRequest) error {
	global.APP_LOG.Debug("开始更新Provider", zap.Uint("providerID", req.ID))

	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, req.ID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			global.APP_LOG.Warn("Provider更新失败：Provider不存在", zap.Uint("providerID", req.ID))
		} else {
			global.APP_LOG.Error("查询Provider失败", zap.Uint("providerID", req.ID), zap.Error(err))
		}
		return err
	}

	// 1. 检查Provider名称是否与其他Provider重复（排除当前Provider）
	if req.Name != provider.Name {
		var existingNameCount int64
		if err := global.APP_DB.Model(&providerModel.Provider{}).
			Where("name = ? AND id != ?", req.Name, req.ID).
			Count(&existingNameCount).Error; err != nil {
			global.APP_LOG.Error("检查Provider名称失败", zap.Error(err))
			return fmt.Errorf("检查Provider名称失败: %v", err)
		}
		if existingNameCount > 0 {
			global.APP_LOG.Warn("Provider更新失败：名称已存在",
				zap.Uint("providerID", req.ID),
				zap.String("name", utils.TruncateString(req.Name, 32)))
			return fmt.Errorf("Provider名称 '%s' 已被其他Provider使用，请使用其他名称", req.Name)
		}
	}

	// 2. 检查SSH地址和端口组合是否与其他Provider重复（排除当前Provider）
	if req.Endpoint != "" {
		sshPort := req.SSHPort
		if sshPort == 0 {
			sshPort = 22 // 默认SSH端口
		}
		// 只有当SSH地址或端口发生变化时才检查
		if req.Endpoint != provider.Endpoint || sshPort != provider.SSHPort {
			var existingEndpointCount int64
			if err := global.APP_DB.Model(&providerModel.Provider{}).
				Where("endpoint = ? AND ssh_port = ? AND id != ?", req.Endpoint, sshPort, req.ID).
				Count(&existingEndpointCount).Error; err != nil {
				global.APP_LOG.Error("检查Provider SSH地址失败", zap.Error(err))
				return fmt.Errorf("检查Provider SSH地址失败: %v", err)
			}
			if existingEndpointCount > 0 {
				global.APP_LOG.Warn("Provider更新失败：SSH地址和端口组合已存在",
					zap.Uint("providerID", req.ID),
					zap.String("endpoint", utils.TruncateString(req.Endpoint, 64)),
					zap.Int("sshPort", sshPort))
				return fmt.Errorf("SSH地址 '%s:%d' 已被其他Provider使用，请检查是否重复配置", req.Endpoint, sshPort)
			}
		}
	}

	// 解析过期时间
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
					return fmt.Errorf("过期时间格式错误，请使用 'YYYY-MM-DD HH:MM:SS' 或 'YYYY-MM-DD' 格式")
				}
			}
		}
		provider.ExpiresAt = &t
	} else {
		// 如果没有指定过期时间，设置为31天后
		defaultExpiry := time.Now().AddDate(0, 0, 31)
		provider.ExpiresAt = &defaultExpiry
	}

	provider.Name = req.Name
	provider.Type = req.Type
	provider.Endpoint = req.Endpoint
	provider.PortIP = req.PortIP
	provider.SSHPort = req.SSHPort
	provider.Username = req.Username

	// 密码和SSH密钥的更新逻辑（使用指针以区分"未提供"和"空值"）：
	// - nil: 不修改（前端未提供该字段，保持原值）
	// - 指向空字符串: 清空该字段（切换到另一种认证方式）
	// - 指向非空字符串: 更新为新值

	// 临时保存更新后的值，用于验证
	newPassword := provider.Password
	newSSHKey := provider.SSHKey

	// 是否修改了密码
	passwordChanged := false
	if req.Password != nil {
		newPassword = *req.Password
		passwordChanged = true
		global.APP_LOG.Debug("更新Provider密码",
			zap.Uint("providerID", req.ID),
			zap.Bool("isEmpty", *req.Password == ""))
	}

	// 是否修改了SSH密钥
	sshKeyChanged := false
	if req.SSHKey != nil {
		newSSHKey = *req.SSHKey
		sshKeyChanged = true
		global.APP_LOG.Debug("更新Provider SSH密钥",
			zap.Uint("providerID", req.ID),
			zap.Bool("isEmpty", *req.SSHKey == ""))
	}

	// 验证：更新后必须至少保留一种认证方式
	// 只有在实际修改了认证字段时才进行验证
	if (passwordChanged || sshKeyChanged) && newPassword == "" && newSSHKey == "" {
		global.APP_LOG.Warn("Provider更新失败：尝试清空所有认证方式",
			zap.Uint("providerID", req.ID))
		return fmt.Errorf("必须保留至少一种SSH认证方式（密码或密钥）")
	}

	// 应用更新（只有在字段被修改时才更新）
	if passwordChanged {
		provider.Password = newPassword
	}
	if sshKeyChanged {
		provider.SSHKey = newSSHKey
	}
	provider.Token = req.Token
	provider.Config = req.Config
	provider.Region = req.Region
	provider.Country = req.Country
	provider.CountryCode = req.CountryCode
	provider.City = req.City
	provider.Architecture = req.Architecture
	provider.ContainerEnabled = req.ContainerEnabled
	provider.VirtualMachineEnabled = req.VirtualMachineEnabled
	provider.TotalQuota = req.TotalQuota
	provider.AllowClaim = req.AllowClaim
	provider.Status = req.Status
	provider.MaxContainerInstances = req.MaxContainerInstances
	provider.MaxVMInstances = req.MaxVMInstances
	provider.AllowConcurrentTasks = req.AllowConcurrentTasks
	provider.MaxConcurrentTasks = req.MaxConcurrentTasks
	provider.TaskPollInterval = req.TaskPollInterval
	provider.EnableTaskPolling = req.EnableTaskPolling
	// 存储配置（所有Provider类型通用）
	provider.StoragePool = req.StoragePool
	// 操作执行配置更新
	if req.ExecutionRule != "" {
		provider.ExecutionRule = req.ExecutionRule
	}
	// 端口映射配置更新
	if req.DefaultPortCount > 0 {
		provider.DefaultPortCount = req.DefaultPortCount
	}
	if req.PortRangeStart > 0 {
		provider.PortRangeStart = req.PortRangeStart
	}
	if req.PortRangeEnd > 0 {
		provider.PortRangeEnd = req.PortRangeEnd
	}
	if req.NetworkType != "" {
		provider.NetworkType = req.NetworkType
	}
	// 带宽配置更新
	if req.DefaultInboundBandwidth > 0 {
		provider.DefaultInboundBandwidth = req.DefaultInboundBandwidth
	}
	if req.DefaultOutboundBandwidth > 0 {
		provider.DefaultOutboundBandwidth = req.DefaultOutboundBandwidth
	}
	if req.MaxInboundBandwidth > 0 {
		provider.MaxInboundBandwidth = req.MaxInboundBandwidth
	}
	if req.MaxOutboundBandwidth > 0 {
		provider.MaxOutboundBandwidth = req.MaxOutboundBandwidth
	}
	// 流量控制开关更新
	oldEnableTrafficControl := provider.EnableTrafficControl
	provider.EnableTrafficControl = req.EnableTrafficControl

	// 检测流量统计开关是否发生变化
	trafficControlChanged := oldEnableTrafficControl != req.EnableTrafficControl

	// 流量限制更新
	if req.MaxTraffic > 0 {
		provider.MaxTraffic = req.MaxTraffic
	}
	// 流量统计模式更新
	if req.TrafficCountMode != "" {
		provider.TrafficCountMode = req.TrafficCountMode
	}
	// 流量统计性能模式更新
	if req.TrafficStatsMode != "" {
		oldMode := provider.TrafficStatsMode
		provider.TrafficStatsMode = req.TrafficStatsMode

		// 如果切换到非自定义模式，强制应用预设配置
		if req.TrafficStatsMode != providerModel.TrafficStatsModeCustom {
			global.APP_LOG.Info("应用流量统计预设配置",
				zap.Uint("providerID", req.ID),
				zap.String("oldMode", oldMode),
				zap.String("newMode", req.TrafficStatsMode))
			provider.ApplyTrafficStatsPreset()
		}
	}
	// 流量统计详细配置更新（仅在自定义模式下使用）
	if req.TrafficCollectInterval > 0 {
		// 验证采集间隔最大不超过5分钟（300秒）
		if req.TrafficCollectInterval > 300 {
			return fmt.Errorf("流量采集间隔不能超过300秒（5分钟），当前值: %d秒", req.TrafficCollectInterval)
		}
		provider.TrafficCollectInterval = req.TrafficCollectInterval
	}
	if req.TrafficCollectBatchSize > 0 {
		provider.TrafficCollectBatchSize = req.TrafficCollectBatchSize
	}
	if req.TrafficLimitCheckInterval > 0 {
		provider.TrafficLimitCheckInterval = req.TrafficLimitCheckInterval
	}
	if req.TrafficLimitCheckBatchSize > 0 {
		provider.TrafficLimitCheckBatchSize = req.TrafficLimitCheckBatchSize
	}
	if req.TrafficAutoResetInterval > 0 {
		provider.TrafficAutoResetInterval = req.TrafficAutoResetInterval
	}
	if req.TrafficAutoResetBatchSize > 0 {
		provider.TrafficAutoResetBatchSize = req.TrafficAutoResetBatchSize
	}
	// 流量计费倍率更新
	if req.TrafficMultiplier > 0 {
		oldValue := provider.TrafficMultiplier
		provider.TrafficMultiplier = req.TrafficMultiplier
		global.APP_LOG.Debug("更新流量计费倍率",
			zap.Uint("providerID", req.ID),
			zap.Float64("oldValue", oldValue),
			zap.Float64("newValue", req.TrafficMultiplier))
	}
	// 端口映射方式更新
	// Docker 类型固定使用 native，忽略前端传入的值
	if provider.Type == "docker" {
		provider.IPv4PortMappingMethod = "native"
		provider.IPv6PortMappingMethod = "native"
	} else {
		if req.IPv4PortMappingMethod != "" {
			provider.IPv4PortMappingMethod = req.IPv4PortMappingMethod
		}
		if req.IPv6PortMappingMethod != "" {
			provider.IPv6PortMappingMethod = req.IPv6PortMappingMethod
		}
	}
	// SSH超时配置更新
	if req.SSHConnectTimeout > 0 {
		provider.SSHConnectTimeout = req.SSHConnectTimeout
	}
	if req.SSHExecuteTimeout > 0 {
		provider.SSHExecuteTimeout = req.SSHExecuteTimeout
	}
	// 容器资源限制配置更新
	provider.ContainerLimitCPU = req.ContainerLimitCpu
	provider.ContainerLimitMemory = req.ContainerLimitMemory
	provider.ContainerLimitDisk = req.ContainerLimitDisk
	// 虚拟机资源限制配置更新
	provider.VMLimitCPU = req.VMLimitCpu
	provider.VMLimitMemory = req.VMLimitMemory
	provider.VMLimitDisk = req.VMLimitDisk
	// 容器特殊配置选项更新（仅 LXD/Incus 容器）
	provider.ContainerPrivileged = req.ContainerPrivileged
	provider.ContainerAllowNesting = req.ContainerAllowNesting
	provider.ContainerEnableLXCFS = req.ContainerEnableLXCFS
	if req.ContainerCPUAllowance != "" {
		provider.ContainerCPUAllowance = req.ContainerCPUAllowance
	}
	provider.ContainerMemorySwap = req.ContainerMemorySwap
	provider.ContainerMaxProcesses = req.ContainerMaxProcesses
	provider.ContainerDiskIOLimit = req.ContainerDiskIOLimit

	// 节点级别等级限制配置更新
	if req.LevelLimits != nil {
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
				zap.Uint("providerID", req.ID),
				zap.Error(err))
			return fmt.Errorf("节点等级限制配置格式错误: %v", err)
		}
		provider.LevelLimits = string(levelLimitsJSON)
	}

	// 设置默认值
	// 并发控制默认值：确保一致性
	if !provider.AllowConcurrentTasks && provider.MaxConcurrentTasks <= 0 {
		provider.MaxConcurrentTasks = 1
	}
	if provider.MaxConcurrentTasks <= 0 {
		provider.MaxConcurrentTasks = 1
	}
	if provider.TaskPollInterval <= 0 {
		provider.TaskPollInterval = 60
	}

	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		// 保存Provider更新
		if err := tx.Save(&provider).Error; err != nil {
			return err
		}

		// 同步更新该Provider下所有实例的到期时间
		if provider.ExpiresAt != nil {
			if err := tx.Model(&providerModel.Instance{}).
				Where("provider_id = ? AND status NOT IN (?)", provider.ID, []string{"deleting", "deleted"}).
				Update("expired_at", *provider.ExpiresAt).Error; err != nil {
				global.APP_LOG.Error("同步实例到期时间失败",
					zap.Uint("providerID", provider.ID),
					zap.Time("newExpiresAt", *provider.ExpiresAt),
					zap.Error(err))
				return fmt.Errorf("同步实例到期时间失败: %v", err)
			}
			global.APP_LOG.Info("已同步实例到期时间",
				zap.Uint("providerID", provider.ID),
				zap.Time("newExpiresAt", *provider.ExpiresAt))
		}

		// 如果流量统计开关发生变化，触发后台任务处理监控配置
		if trafficControlChanged {
			go s.handleTrafficControlToggle(provider.ID, req.EnableTrafficControl)
		}

		return nil
	})
}

// handleTrafficControlToggle 处理流量统计开关切换（后台任务）
// 当Provider的EnableTrafficControl从false->true或true->false时调用
func (s *Service) handleTrafficControlToggle(providerID uint, enabled bool) {
	defer func() {
		if r := recover(); r != nil {
			global.APP_LOG.Error("处理流量统计开关切换时发生panic",
				zap.Uint("providerID", providerID),
				zap.Bool("enabled", enabled),
				zap.Any("panic", r))
		}
	}()

	global.APP_LOG.Info("开始处理Provider流量统计开关切换",
		zap.Uint("providerID", providerID),
		zap.Bool("enabled", enabled))

	// 获取Provider信息（预加载，避免循环中重复查询）
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, providerID).Error; err != nil {
		global.APP_LOG.Error("查询Provider失败",
			zap.Uint("providerID", providerID),
			zap.Error(err))
		return
	}

	// 获取该Provider下所有活跃实例（预加载所有字段）
	var instances []providerModel.Instance
	if err := global.APP_DB.Where("provider_id = ? AND status NOT IN (?)",
		providerID, []string{"deleted", "deleting"}).Find(&instances).Error; err != nil {
		global.APP_LOG.Error("查询Provider实例失败",
			zap.Uint("providerID", providerID),
			zap.Error(err))
		return
	}

	if len(instances) == 0 {
		global.APP_LOG.Info("Provider没有活跃实例，无需处理",
			zap.Uint("providerID", providerID))
		return
	}

	// 使用统一的流量监控管理器
	trafficMonitorManager := traffic_monitor.GetManager()

	// 创建带超时的context
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	if enabled {
		// 启用流量统计：为所有运行中实例初始化监控
		global.APP_LOG.Info("启用流量统计，开始为实例初始化监控",
			zap.Uint("providerID", providerID),
			zap.Int("instanceCount", len(instances)))

		successCount := 0
		failCount := 0
		skippedCount := 0

		for _, instance := range instances {
			// 只为运行中的实例初始化监控
			if instance.Status != "running" {
				global.APP_LOG.Debug("跳过非运行状态实例",
					zap.Uint("instanceID", instance.ID),
					zap.String("status", instance.Status))
				skippedCount++
				continue
			}

			// 使用统一的流量监控管理器
			if err := trafficMonitorManager.AttachMonitor(ctx, instance.ID); err != nil {
				global.APP_LOG.Warn("初始化实例监控失败",
					zap.Uint("instanceID", instance.ID),
					zap.String("instanceName", instance.Name),
					zap.Error(err))
				failCount++
			} else {
				global.APP_LOG.Info("实例监控初始化成功",
					zap.Uint("instanceID", instance.ID),
					zap.String("instanceName", instance.Name))
				successCount++
			}
		}

		global.APP_LOG.Info("Provider流量统计启用处理完成",
			zap.Uint("providerID", providerID),
			zap.Int("成功", successCount),
			zap.Int("失败", failCount),
			zap.Int("跳过", skippedCount))

	} else {
		// 禁用流量统计：清理所有实例的监控
		global.APP_LOG.Info("禁用流量统计，开始清理实例监控",
			zap.Uint("providerID", providerID),
			zap.Int("instanceCount", len(instances)))

		successCount := 0
		failCount := 0

		for _, instance := range instances {
			// 使用统一的流量监控管理器清理监控
			if err := trafficMonitorManager.DetachMonitor(ctx, instance.ID); err != nil {
				global.APP_LOG.Warn("清理实例监控失败",
					zap.Uint("instanceID", instance.ID),
					zap.String("instanceName", instance.Name),
					zap.Error(err))
				failCount++
			} else {
				global.APP_LOG.Info("实例监控清理成功",
					zap.Uint("instanceID", instance.ID),
					zap.String("instanceName", instance.Name))
				successCount++
			}
		}

		global.APP_LOG.Info("Provider流量统计禁用处理完成",
			zap.Uint("providerID", providerID),
			zap.Int("成功", successCount),
			zap.Int("失败", failCount))
	}
} // FreezeProvider 冻结Provider
func (s *Service) FreezeProvider(req admin.FreezeProviderRequest) error {
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, req.ID).Error; err != nil {
		return fmt.Errorf("Provider不存在")
	}

	provider.IsFrozen = true
	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Save(&provider).Error
	})
}

// UnfreezeProvider 解冻Provider
func (s *Service) UnfreezeProvider(req admin.UnfreezeProviderRequest) error {
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, req.ID).Error; err != nil {
		return fmt.Errorf("Provider不存在")
	}

	// 解析新的过期时间
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
					return fmt.Errorf("过期时间格式错误，请使用 'YYYY-MM-DD HH:MM:SS' 或 'YYYY-MM-DD' 格式")
				}
			}
		}
		// 检查新的过期时间必须是未来时间
		if t.Before(time.Now()) {
			return fmt.Errorf("过期时间必须是未来时间")
		}
		provider.ExpiresAt = &t
	} else {
		// 如果没有指定新的过期时间，设置为31天后
		defaultExpiry := time.Now().AddDate(0, 0, 31)
		provider.ExpiresAt = &defaultExpiry
	}

	provider.IsFrozen = false
	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		// 保存Provider更新
		if err := tx.Save(&provider).Error; err != nil {
			return err
		}

		// 同步更新该Provider下所有实例的到期时间
		if provider.ExpiresAt != nil {
			if err := tx.Model(&providerModel.Instance{}).
				Where("provider_id = ? AND status NOT IN (?)", provider.ID, []string{"deleting", "deleted"}).
				Update("expired_at", *provider.ExpiresAt).Error; err != nil {
				global.APP_LOG.Error("同步实例到期时间失败",
					zap.Uint("providerID", provider.ID),
					zap.Time("newExpiresAt", *provider.ExpiresAt),
					zap.Error(err))
				return fmt.Errorf("同步实例到期时间失败: %v", err)
			}
			global.APP_LOG.Info("已同步实例到期时间",
				zap.Uint("providerID", provider.ID),
				zap.Time("newExpiresAt", *provider.ExpiresAt))
		}

		return nil
	})
}

// AutoConfigureProviderWithStream 带实时输出的自动配置Provider
func (s *Service) AutoConfigureProviderWithStream(providerID uint, outputChan chan<- string) error {
	return s.AutoConfigureProviderWithStreamContext(context.Background(), providerID, outputChan)
}

// AutoConfigureProviderWithStreamContext 带实时输出和context控制的自动配置Provider
func (s *Service) AutoConfigureProviderWithStreamContext(ctx context.Context, providerID uint, outputChan chan<- string) error {
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, providerID).Error; err != nil {
		outputChan <- fmt.Sprintf("错误: Provider不存在 (ID: %d)", providerID)
		return fmt.Errorf("Provider不存在")
	}

	// 检查context是否已取消
	select {
	case <-ctx.Done():
		outputChan <- "操作已取消"
		return ctx.Err()
	default:
	}

	// 支持LXD、Incus和Proxmox
	if provider.Type != "lxd" && provider.Type != "incus" && provider.Type != "proxmox" {
		outputChan <- fmt.Sprintf("错误: 不支持的Provider类型: %s (只支持LXD、Incus和Proxmox)", provider.Type)
		return fmt.Errorf("只支持为LXD、Incus和Proxmox生成配置")
	}

	outputChan <- fmt.Sprintf("=== 开始自动配置 %s Provider: %s ===", strings.ToUpper(provider.Type), provider.Name)
	outputChan <- fmt.Sprintf("Provider地址: %s", provider.Endpoint)
	outputChan <- fmt.Sprintf("SSH用户: %s", provider.Username)

	certService := &provider2.CertService{}

	// 执行自动配置（传递context以便取消）
	err := certService.AutoConfigureProviderWithStreamContext(ctx, &provider, outputChan)
	if err != nil {
		if ctx.Err() != nil {
			outputChan <- "操作已取消"
			return ctx.Err()
		}
		outputChan <- fmt.Sprintf("自动配置失败: %s", err.Error())
		return fmt.Errorf("自动配置失败: %w", err)
	}

	// 根据类型返回不同的成功消息
	var message string
	switch provider.Type {
	case "proxmox":
		message = "Proxmox VE API 自动配置成功，认证配置已保存到数据库和文件"
	case "lxd":
		message = "LXD 自动配置成功，证书已安装并保存到数据库和文件"
	case "incus":
		message = "Incus 自动配置成功，证书已安装并保存到数据库和文件"
	}

	outputChan <- fmt.Sprintf("✅ %s", message)
	outputChan <- "✅ 自动配置流程完成，配置信息已统一管理"

	return nil
}

// GetProviderStatus 获取Provider状态详情
func (s *Service) GetProviderStatus(providerID uint) (*admin.ProviderStatusResponse, error) {
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, providerID).Error; err != nil {
		return nil, fmt.Errorf("Provider不存在")
	}

	response := &admin.ProviderStatusResponse{
		ID:              provider.ID,
		UUID:            provider.UUID,
		Name:            provider.Name,
		Type:            provider.Type,
		Status:          provider.Status,
		APIStatus:       provider.APIStatus,
		SSHStatus:       provider.SSHStatus,
		LastAPICheck:    provider.LastAPICheck,
		LastSSHCheck:    provider.LastSSHCheck,
		CertPath:        provider.CertPath,
		KeyPath:         provider.KeyPath,
		CertFingerprint: provider.CertFingerprint,
		// 资源信息
		NodeCPUCores:     provider.NodeCPUCores,
		NodeMemoryTotal:  provider.NodeMemoryTotal,
		NodeDiskTotal:    provider.NodeDiskTotal,
		ResourceSynced:   provider.ResourceSynced,
		ResourceSyncedAt: provider.ResourceSyncedAt,
	}

	return response, nil
}
