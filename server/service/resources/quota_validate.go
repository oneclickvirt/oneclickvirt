package resources

import (
	"encoding/json"
	"fmt"
	"strings"

	"oneclickvirt/config"
	"oneclickvirt/constant"
	"oneclickvirt/global"
	"oneclickvirt/model/provider"
	"oneclickvirt/model/user"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// validateInTransaction 在事务中进行配额验证（两阶段配额系统）
func (s *QuotaService) validateInTransaction(tx *gorm.DB, req ResourceRequest) (*QuotaCheckResult, error) {
	// 使用 SELECT FOR UPDATE 锁定用户记录
	var user user.User
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, req.UserID).Error; err != nil {
		if strings.Contains(err.Error(), "Lock wait timeout") || strings.Contains(err.Error(), "timeout") {
			return nil, fmt.Errorf("系统繁忙，请稍后重试")
		}
		return nil, fmt.Errorf("用户不存在: %v", err)
	}

	// 检查用户状态
	if user.Status != 1 {
		return &QuotaCheckResult{
			Allowed: false,
			Reason:  "用户账户已被禁用",
		}, nil
	}

	// 管理员跳过所有配额和等级限制
	if user.UserType == "admin" || user.UserType == "super_admin" {
		return &QuotaCheckResult{
			Allowed: true,
			Reason:  "管理员无配额限制",
		}, nil
	}

	// 获取用户等级限制
	levelLimits, exists := global.GetAppConfig().Quota.LevelLimits[user.Level]
	if !exists {
		return &QuotaCheckResult{
			Allowed: false,
			Reason:  fmt.Sprintf("用户等级 %d 没有配置资源限制", user.Level),
		}, nil
	}
	levelLimits = config.NormalizeLevelLimitInfo(user.Level, levelLimits)

	// 如果提供了 ProviderID，需要获取并合并 Provider 的等级限制
	var providerLevelLimits *config.LevelLimitInfo
	var prov *provider.Provider
	if req.ProviderID > 0 {
		var err error
		var providerModel provider.Provider
		if err := tx.First(&providerModel, req.ProviderID).Error; err != nil {
			return nil, fmt.Errorf("Provider 不存在: %v", err)
		}
		prov = &providerModel

		providerLevelLimits, err = s.getProviderLevelLimits(tx, req.ProviderID, user.Level)
		if err != nil {
			return nil, fmt.Errorf("获取 Provider 等级限制失败: %v", err)
		}

		// 如果 Provider 有等级限制配置，则取两者的最小值用于后续验证
		// 但需要考虑 Provider 的超分配设置
		if providerLevelLimits != nil {
			levelLimits = s.mergeLevelLimitsWithOvercommit(levelLimits, *providerLevelLimits, prov, req.InstanceType)
		}
	}

	// 统计当前资源使用：分别统计稳定状态和待确认状态
	currentInstances, currentResources, pendingResources, err := s.getCurrentResourceUsageWithPending(tx, req.UserID)
	if err != nil {
		return nil, fmt.Errorf("获取当前资源使用情况失败: %v", err)
	}

	// 如果提供了 ProviderID，还需要检查该用户在此节点上的实例数量
	var currentProviderInstances int
	if req.ProviderID > 0 {
		currentProviderInstances, err = s.getCurrentProviderInstanceCount(tx, req.UserID, req.ProviderID)
		if err != nil {
			return nil, fmt.Errorf("获取节点实例数量失败: %v", err)
		}
	}

	// 计算请求的资源
	requestedResources := ResourceUsage{
		CPU:       req.CPU,
		Memory:    req.Memory,
		Disk:      req.Disk,
		Bandwidth: req.Bandwidth,
	}

	// 获取最大允许资源
	maxResources := s.GetLevelMaxResources(levelLimits)

	result := &QuotaCheckResult{
		CurrentInstances:  currentInstances,
		MaxInstances:      levelLimits.MaxInstances,
		CurrentResources:  currentResources,
		PendingResources:  pendingResources,
		MaxResources:      maxResources,
		MaxQuota:          maxResources,
		RequiredResources: requestedResources,
	}

	// 1. 检查用户全局实例数量限制（包含待确认实例）
	if currentInstances >= levelLimits.MaxInstances {
		result.Allowed = false
		result.Reason = fmt.Sprintf("实例数量已达上限：当前 %d/%d", currentInstances, levelLimits.MaxInstances)
		return result, nil
	}

	// 1.5 如果有 Provider 限制，还需要检查用户在该节点的实例数量
	if req.ProviderID > 0 && providerLevelLimits != nil && providerLevelLimits.MaxInstances > 0 {
		// 这里使用的是合并前的 providerLevelLimits，因为要检查节点本身的限制
		if currentProviderInstances >= providerLevelLimits.MaxInstances {
			result.Allowed = false
			result.Reason = fmt.Sprintf("该节点实例数量已达上限：当前在此节点 %d/%d",
				currentProviderInstances, providerLevelLimits.MaxInstances)
			return result, nil
		}
	}

	// 2. 检查CPU限制（包含待确认资源，考虑超分配设置）
	shouldCheckCPU := true
	if req.ProviderID > 0 && prov != nil {
		switch req.InstanceType {
		case "container":
			shouldCheckCPU = prov.ContainerLimitCPU
		case "vm":
			shouldCheckCPU = prov.VMLimitCPU
		}
	}
	totalCPU := currentResources.CPU + pendingResources.CPU + requestedResources.CPU
	if shouldCheckCPU && totalCPU > maxResources.CPU {
		result.Allowed = false
		result.Reason = fmt.Sprintf("CPU资源不足：需要 %d，当前使用 %d（含待确认 %d），最大允许 %d",
			requestedResources.CPU, currentResources.CPU, pendingResources.CPU, maxResources.CPU)
		return result, nil
	}

	// 3. 检查内存限制（包含待确认资源，考虑超分配设置）
	shouldCheckMemory := true
	if req.ProviderID > 0 && prov != nil {
		switch req.InstanceType {
		case "container":
			shouldCheckMemory = prov.ContainerLimitMemory
		case "vm":
			shouldCheckMemory = prov.VMLimitMemory
		}
	}
	totalMemory := currentResources.Memory + pendingResources.Memory + requestedResources.Memory
	if shouldCheckMemory && totalMemory > maxResources.Memory {
		result.Allowed = false
		result.Reason = fmt.Sprintf("内存资源不足：需要 %dMB，当前使用 %dMB（含待确认 %dMB），最大允许 %dMB",
			requestedResources.Memory, currentResources.Memory, pendingResources.Memory, maxResources.Memory)
		return result, nil
	}

	// 4. 检查磁盘限制（包含待确认资源，考虑超分配设置）
	shouldCheckDisk := true
	if req.ProviderID > 0 && prov != nil {
		switch req.InstanceType {
		case "container":
			shouldCheckDisk = prov.ContainerLimitDisk
		case "vm":
			shouldCheckDisk = prov.VMLimitDisk
		}
	}
	totalDisk := currentResources.Disk + pendingResources.Disk + requestedResources.Disk
	if shouldCheckDisk && totalDisk > maxResources.Disk {
		result.Allowed = false
		result.Reason = fmt.Sprintf("磁盘资源不足：需要 %dMB，当前使用 %dMB（含待确认 %dMB），最大允许 %dMB",
			requestedResources.Disk, currentResources.Disk, pendingResources.Disk, maxResources.Disk)
		return result, nil
	}

	// 5. 检查带宽限制
	if requestedResources.Bandwidth > maxResources.Bandwidth {
		result.Allowed = false
		result.Reason = fmt.Sprintf("带宽超出等级限制：需要 %dMbps，等级 %d 最大允许 %dMbps",
			requestedResources.Bandwidth, user.Level, maxResources.Bandwidth)
		return result, nil
	}

	// 6. 检查实例类型权限
	if !s.checkInstanceTypePermission(user.Level, req.InstanceType) {
		result.Allowed = false
		result.Reason = fmt.Sprintf("等级 %d 不允许创建 %s 类型的实例", user.Level, req.InstanceType)
		return result, nil
	}

	result.Allowed = true
	result.Reason = "资源验证通过"
	return result, nil
}

// getCurrentResourceUsage 获取当前资源使用情况（仅稳定状态，用于向后兼容）
func (s *QuotaService) getCurrentResourceUsage(tx *gorm.DB, userID uint) (int, ResourceUsage, error) {
	count, resources, _, err := s.getCurrentResourceUsageWithPending(tx, userID)
	return count, resources, err
}

// getCurrentResourceUsageWithPending 获取当前资源使用情况（分别统计稳定和待确认）
func (s *QuotaService) getCurrentResourceUsageWithPending(tx *gorm.DB, userID uint) (int, ResourceUsage, ResourceUsage, error) {
	// 使用状态常量分别查询稳定状态和过渡状态的实例
	stableStatuses := constant.GetStableStatuses()
	transitionalStatuses := constant.GetTransitionalStatuses()
	type quotaUsageAggregate struct {
		Count     int64
		CPU       int
		Memory    int64
		Disk      int64
		Bandwidth int
	}

	// 稳定状态：running、stopped、error
	var stableUsage quotaUsageAggregate
	err := tx.Clauses(clause.Locking{Strength: "SHARE"}).
		Model(&provider.Instance{}).
		Select("COUNT(*) AS count, COALESCE(SUM(cpu), 0) AS cpu, COALESCE(SUM(memory), 0) AS memory, COALESCE(SUM(disk), 0) AS disk, COALESCE(SUM(bandwidth), 0) AS bandwidth").
		Where("user_id = ? AND deleted_at IS NULL AND status IN (?)", userID, stableStatuses).
		Scan(&stableUsage).Error
	if err != nil {
		return 0, ResourceUsage{}, ResourceUsage{}, err
	}

	// 待确认状态：creating、resetting
	var pendingUsage quotaUsageAggregate
	err = tx.Clauses(clause.Locking{Strength: "SHARE"}).
		Model(&provider.Instance{}).
		Select("COUNT(*) AS count, COALESCE(SUM(cpu), 0) AS cpu, COALESCE(SUM(memory), 0) AS memory, COALESCE(SUM(disk), 0) AS disk, COALESCE(SUM(bandwidth), 0) AS bandwidth").
		Where("user_id = ? AND deleted_at IS NULL AND status IN (?)", userID, transitionalStatuses).
		Scan(&pendingUsage).Error
	if err != nil {
		return 0, ResourceUsage{}, ResourceUsage{}, err
	}

	// 总实例数 = 稳定状态 + 待确认状态
	totalCount := int(stableUsage.Count + pendingUsage.Count)

	stableResources := ResourceUsage{
		CPU:       stableUsage.CPU,
		Memory:    stableUsage.Memory,
		Disk:      stableUsage.Disk,
		Bandwidth: stableUsage.Bandwidth,
	}

	pendingResources := ResourceUsage{
		CPU:       pendingUsage.CPU,
		Memory:    pendingUsage.Memory,
		Disk:      pendingUsage.Disk,
		Bandwidth: pendingUsage.Bandwidth,
	}

	return totalCount, stableResources, pendingResources, nil
}

// getCurrentProviderInstanceCount 获取用户在指定 Provider 上的实例数量（增强版）
func (s *QuotaService) getCurrentProviderInstanceCount(tx *gorm.DB, userID uint, providerID uint) (int, error) {
	var count int64

	// 使用 FOR SHARE 共享锁，防止幻读
	err := tx.Clauses(clause.Locking{Strength: "SHARE"}).
		Model(&provider.Instance{}).
		Where("user_id = ? AND provider_id = ? AND status NOT IN (?)",
			userID, providerID, []string{"deleting", "deleted", "failed", "creating", "resetting"}).
		Count(&count).Error

	if err != nil {
		return 0, err
	}

	return int(count), nil
}

// GetCurrentResourceUsageInTx 公开方法：在事务中获取当前资源使用情况
func (s *QuotaService) GetCurrentResourceUsageInTx(tx *gorm.DB, userID uint) (int, ResourceUsage, error) {
	return s.getCurrentResourceUsage(tx, userID)
}

// GetCurrentProviderInstanceCountInTx 公开方法：在事务中获取用户在指定 Provider 上的实例数量
func (s *QuotaService) GetCurrentProviderInstanceCountInTx(tx *gorm.DB, userID uint, providerID uint) (int, error) {
	return s.getCurrentProviderInstanceCount(tx, userID, providerID)
}

// GetProviderLevelLimitsInTx 公开方法：在事务中获取 Provider 的等级限制
func (s *QuotaService) GetProviderLevelLimitsInTx(tx *gorm.DB, providerID uint, userLevel int) (*config.LevelLimitInfo, error) {
	return s.getProviderLevelLimits(tx, providerID, userLevel)
}

// GetLevelMaxResources 获取等级最大资源限制
func (s *QuotaService) GetLevelMaxResources(levelLimits config.LevelLimitInfo) ResourceUsage {
	maxResources := ResourceUsage{
		CPU:       1,     // 默认值
		Memory:    512,   // 默认值 (MB)
		Disk:      10240, // 默认值 (MB) 10GB = 10240MB
		Bandwidth: 100,   // 默认值 (Mbps)
	}

	if levelLimits.MaxResources != nil {
		if cpu, ok := levelLimits.MaxResources["cpu"].(int); ok {
			maxResources.CPU = cpu
		} else if cpuFloat, ok := levelLimits.MaxResources["cpu"].(float64); ok {
			maxResources.CPU = int(cpuFloat)
		}

		if memory, ok := levelLimits.MaxResources["memory"].(int); ok {
			maxResources.Memory = int64(memory)
		} else if memoryFloat, ok := levelLimits.MaxResources["memory"].(float64); ok {
			maxResources.Memory = int64(memoryFloat)
		}

		if disk, ok := levelLimits.MaxResources["disk"].(int); ok {
			maxResources.Disk = int64(disk)
		} else if diskFloat, ok := levelLimits.MaxResources["disk"].(float64); ok {
			maxResources.Disk = int64(diskFloat)
		}

		if bandwidth, ok := levelLimits.MaxResources["bandwidth"].(int); ok {
			maxResources.Bandwidth = bandwidth
		} else if bandwidthFloat, ok := levelLimits.MaxResources["bandwidth"].(float64); ok {
			maxResources.Bandwidth = int(bandwidthFloat)
		}
	}

	return maxResources
}

// getLevelBandwidthLimit 获取等级带宽限制
func (s *QuotaService) getLevelBandwidthLimit(level int) int {
	// 默认带宽限制：每个等级+100Mbps，从100Mbps开始
	baseBandwidth := 100
	return baseBandwidth + (level-1)*100
}

// checkInstanceTypePermission 检查实例类型权限
func (s *QuotaService) checkInstanceTypePermission(level int, instanceType string) bool {
	// 从配置中获取实例类型权限设置
	permissions := global.GetAppConfig().Quota.InstanceTypePermissions

	switch instanceType {
	case "container":
		// 容器：所有等级用户都可创建
		return true
	case "vm":
		return level >= permissions.MinLevelForVM
	default:
		// 未知类型使用容器权限（所有等级可用）
		return true
	}
}

// getProviderLevelLimits 获取 Provider 的等级限制配置
func (s *QuotaService) getProviderLevelLimits(tx *gorm.DB, providerID uint, userLevel int) (*config.LevelLimitInfo, error) {
	var prov provider.Provider
	if err := tx.First(&prov, providerID).Error; err != nil {
		return nil, fmt.Errorf("Provider 不存在: %v", err)
	}

	// 如果 Provider 没有配置 LevelLimits，返回 nil
	if prov.LevelLimits == "" {
		return nil, nil
	}

	// 解析 JSON 格式的 LevelLimits
	var providerLimits map[int]config.LevelLimitInfo
	if err := json.Unmarshal([]byte(prov.LevelLimits), &providerLimits); err != nil {
		return nil, fmt.Errorf("解析 Provider 等级限制失败: %v", err)
	}

	// 获取对应用户等级的限制
	if limitInfo, exists := providerLimits[userLevel]; exists {
		limitInfo = config.NormalizeLevelLimitInfo(userLevel, limitInfo)
		return &limitInfo, nil
	}

	// 如果没有配置该等级的限制，返回 nil
	return nil, nil
}

// mergeLevelLimits 合并用户等级限制和 Provider 等级限制，取两者最小值
func (s *QuotaService) mergeLevelLimits(userLimits, providerLimits config.LevelLimitInfo) config.LevelLimitInfo {
	merged := config.LevelLimitInfo{
		MaxInstances: userLimits.MaxInstances,
		MaxResources: make(map[string]interface{}),
		MaxTraffic:   userLimits.MaxTraffic,
	}

	// 取实例数量的最小值
	if providerLimits.MaxInstances > 0 && providerLimits.MaxInstances < userLimits.MaxInstances {
		merged.MaxInstances = providerLimits.MaxInstances
	}

	// 取流量限制的最小值
	if providerLimits.MaxTraffic > 0 && providerLimits.MaxTraffic < userLimits.MaxTraffic {
		merged.MaxTraffic = providerLimits.MaxTraffic
	}

	// 合并资源限制，取每项的最小值
	resourceKeys := []string{"cpu", "memory", "disk", "bandwidth"}
	for _, key := range resourceKeys {
		userVal := s.getResourceValue(userLimits.MaxResources, key)
		providerVal := s.getResourceValue(providerLimits.MaxResources, key)

		// 如果 Provider 没有配置该资源，使用用户限制
		if providerVal == 0 {
			merged.MaxResources[key] = userVal
		} else if userVal == 0 {
			// 如果用户没有配置该资源（理论上不应该发生），使用 Provider 限制
			merged.MaxResources[key] = providerVal
		} else {
			// 取两者最小值
			if providerVal < userVal {
				merged.MaxResources[key] = providerVal
			} else {
				merged.MaxResources[key] = userVal
			}
		}
	}

	return merged
}

// mergeLevelLimitsWithOvercommit 合并用户等级限制和 Provider 等级限制，同时考虑超分配设置
// 如果 Provider 允许某资源超分配，则不应用 Provider 的该资源限制
func (s *QuotaService) mergeLevelLimitsWithOvercommit(userLimits, providerLimits config.LevelLimitInfo, prov *provider.Provider, instanceType string) config.LevelLimitInfo {
	merged := config.LevelLimitInfo{
		MaxInstances: userLimits.MaxInstances,
		MaxResources: make(map[string]interface{}),
		MaxTraffic:   userLimits.MaxTraffic,
	}

	// 取实例数量的最小值
	if providerLimits.MaxInstances > 0 && providerLimits.MaxInstances < userLimits.MaxInstances {
		merged.MaxInstances = providerLimits.MaxInstances
	}

	// 取流量限制的最小值
	if providerLimits.MaxTraffic > 0 && providerLimits.MaxTraffic < userLimits.MaxTraffic {
		merged.MaxTraffic = providerLimits.MaxTraffic
	}

	// 根据实例类型和超分配设置合并资源限制
	resourceKeys := []string{"cpu", "memory", "disk", "bandwidth"}
	for _, key := range resourceKeys {
		userVal := s.getResourceValue(userLimits.MaxResources, key)
		providerVal := s.getResourceValue(providerLimits.MaxResources, key)

		// 检查该资源是否允许超分配
		allowOvercommit := false
		if instanceType == "container" {
			switch key {
			case "cpu":
				allowOvercommit = !prov.ContainerLimitCPU
			case "memory":
				allowOvercommit = !prov.ContainerLimitMemory
			case "disk":
				allowOvercommit = !prov.ContainerLimitDisk
			}
		} else if instanceType == "vm" {
			switch key {
			case "cpu":
				allowOvercommit = !prov.VMLimitCPU
			case "memory":
				allowOvercommit = !prov.VMLimitMemory
			case "disk":
				allowOvercommit = !prov.VMLimitDisk
			}
		}

		// 如果允许超分配，只使用用户限制，忽略 Provider 限制
		if allowOvercommit {
			merged.MaxResources[key] = userVal
			global.APP_LOG.Debug(fmt.Sprintf("资源 %s 允许超分配，使用用户限制: %d", key, userVal))
		} else {
			// 否则取两者最小值
			if providerVal == 0 {
				merged.MaxResources[key] = userVal
			} else if userVal == 0 {
				merged.MaxResources[key] = providerVal
			} else {
				if providerVal < userVal {
					merged.MaxResources[key] = providerVal
				} else {
					merged.MaxResources[key] = userVal
				}
			}
		}
	}

	return merged
}

// getResourceValue 从资源 map 中获取数值
func (s *QuotaService) getResourceValue(resources map[string]interface{}, key string) int64 {
	if resources == nil {
		return 0
	}

	val, exists := resources[key]
	if !exists {
		return 0
	}

	switch v := val.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	default:
		return 0
	}
}
