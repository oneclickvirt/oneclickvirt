package resources

import (
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	resourceModel "oneclickvirt/model/resource"
	userModel "oneclickvirt/model/user"
)

// UserDashboardService 处理用户仪表板相关功能
type UserDashboardService struct{}

// GetUserDashboard 获取用户仪表板数据
func (s *UserDashboardService) GetUserDashboard(userID uint) (*userModel.UserDashboardResponse, error) {
	var user userModel.User
	if err := global.APP_DB.First(&user, userID).Error; err != nil {
		return nil, err
	}

	// 统计当前实例（不包含预留资源，只统计实际实例）
	var totalInstances, runningInstances, stoppedInstances, containers, vms int64
	// 只统计非删除状态的实例
	global.APP_DB.Model(&providerModel.Instance{}).Where("user_id = ? AND status != ? AND status != ?", userID, "deleting", "deleted").Count(&totalInstances)
	global.APP_DB.Model(&providerModel.Instance{}).Where("user_id = ? AND status = ?", userID, "running").Count(&runningInstances)
	global.APP_DB.Model(&providerModel.Instance{}).Where("user_id = ? AND status = ?", userID, "stopped").Count(&stoppedInstances)
	global.APP_DB.Model(&providerModel.Instance{}).Where("user_id = ? AND instance_type = ? AND status != ? AND status != ?", userID, "container", "deleting", "deleted").Count(&containers)
	global.APP_DB.Model(&providerModel.Instance{}).Where("user_id = ? AND instance_type = ? AND status != ? AND status != ?", userID, "vm", "deleting", "deleted").Count(&vms)

	var recentInstances []providerModel.Instance
	global.APP_DB.Where("user_id = ? AND status != ? AND status != ?", userID, "deleting", "deleted").Order("created_at DESC").Limit(5).Find(&recentInstances)

	// 处理最近实例的IP地址显示（移除端口号）
	for i := range recentInstances {
		recentInstances[i].PublicIP = s.extractIPFromEndpoint(recentInstances[i].PublicIP)
	}

	// 获取用户等级限制
	levelLimits, exists := global.APP_CONFIG.Quota.LevelLimits[user.Level]
	if !exists {
		return nil, fmt.Errorf("用户等级 %d 没有配置资源限制", user.Level)
	}

	// 统计当前实例使用的资源
	var currentInstances []providerModel.Instance
	if err := global.APP_DB.Where("user_id = ? AND status NOT IN (?)", userID, []string{"deleting", "deleted"}).Find(&currentInstances).Error; err != nil {
		return nil, fmt.Errorf("查询用户实例失败: %v", err)
	}

	// 统计当前预留的资源（新机制：只查询未过期的预留）
	var activeReservations []resourceModel.ResourceReservation
	if err := global.APP_DB.Where("user_id = ? AND expires_at > ?", userID, time.Now()).Find(&activeReservations).Error; err != nil {
		return nil, fmt.Errorf("查询用户预留资源失败: %v", err)
	}

	// 计算总使用资源（实例 + 预留）
	totalCPU := 0
	totalMemory := int64(0)
	totalDisk := int64(0)
	totalBandwidth := 0
	instanceCountWithReservations := len(currentInstances)

	for _, instance := range currentInstances {
		totalCPU += instance.CPU
		totalMemory += instance.Memory
		totalDisk += instance.Disk
		totalBandwidth += instance.Bandwidth
	}

	for _, reservation := range activeReservations {
		totalCPU += reservation.CPU
		totalMemory += reservation.Memory
		totalDisk += reservation.Disk
		totalBandwidth += reservation.Bandwidth
		instanceCountWithReservations++ // 预留也算作实例数量
	}

	// 获取最大允许资源
	quotaService := NewQuotaService()
	maxResources := quotaService.GetLevelMaxResources(levelLimits)

	dashboard := &userModel.UserDashboardResponse{
		User:            user,
		UsedQuota:       totalCPU + int(totalMemory/1024) + int(totalDisk/1024), // 简化的配额计算
		TotalQuota:      user.TotalQuota,
		RecentInstances: recentInstances,
	}

	dashboard.Instances.Total = int(totalInstances)
	dashboard.Instances.Running = int(runningInstances)
	dashboard.Instances.Stopped = int(stoppedInstances)
	dashboard.Instances.Containers = int(containers)
	dashboard.Instances.VMs = int(vms)

	// 详细的资源使用信息（包含预留资源）
	dashboard.ResourceUsage = &userModel.ResourceUsageInfo{
		CPU:              totalCPU,                      // 包含预留的CPU
		Memory:           totalMemory,                   // 包含预留的内存
		Disk:             totalDisk,                     // 包含预留的磁盘
		MaxInstances:     levelLimits.MaxInstances,      // 最大实例数
		CurrentInstances: instanceCountWithReservations, // 包含预留的实例数量
		MaxCPU:           maxResources.CPU,
		MaxMemory:        maxResources.Memory,
		MaxDisk:          maxResources.Disk,
	}

	return dashboard, nil
}

// GetUserLimits 获取用户资源限制
func (s *UserDashboardService) GetUserLimits(userID uint) (*userModel.UserLimitsResponse, error) {
	var user userModel.User
	if err := global.APP_DB.First(&user, userID).Error; err != nil {
		return nil, err
	}

	// 获取等级限制
	levelLimits, exists := global.APP_CONFIG.Quota.LevelLimits[user.Level]
	if !exists {
		return nil, fmt.Errorf("用户等级 %d 没有配置资源限制", user.Level)
	}

	// 获取配额服务来计算最大资源
	quotaService := NewQuotaService()
	maxResources := quotaService.GetLevelMaxResources(levelLimits)

	// 统计当前使用的资源
	var currentInstances []providerModel.Instance
	if err := global.APP_DB.Where("user_id = ? AND status NOT IN (?)", userID, []string{"deleting", "deleted"}).Find(&currentInstances).Error; err != nil {
		return nil, fmt.Errorf("查询用户实例失败: %v", err)
	}

	var activeReservations []resourceModel.ResourceReservation
	if err := global.APP_DB.Where("user_id = ? AND expires_at > ?", userID, time.Now()).Find(&activeReservations).Error; err != nil {
		return nil, fmt.Errorf("查询用户预留资源失败: %v", err)
	}

	// 计算当前使用量
	var usedCPU, usedMemory, usedDisk, usedBandwidth int
	usedInstances := len(currentInstances)

	for _, instance := range currentInstances {
		usedCPU += instance.CPU
		usedMemory += int(instance.Memory)
		usedDisk += int(instance.Disk)
		usedBandwidth += instance.Bandwidth
	}

	for _, reservation := range activeReservations {
		usedCPU += reservation.CPU
		usedMemory += int(reservation.Memory)
		usedDisk += int(reservation.Disk)
		usedBandwidth += reservation.Bandwidth
		usedInstances++
	}

	response := &userModel.UserLimitsResponse{
		Level:         user.Level,
		MaxInstances:  levelLimits.MaxInstances,
		UsedInstances: usedInstances,
		MaxCpu:        maxResources.CPU,
		UsedCpu:       usedCPU,
		MaxMemory:     int(maxResources.Memory),
		UsedMemory:    usedMemory,
		MaxDisk:       int(maxResources.Disk),
		UsedDisk:      usedDisk,
		MaxBandwidth:  maxResources.Bandwidth,
		UsedBandwidth: usedBandwidth,
		MaxTraffic:    levelLimits.MaxTraffic, // 使用等级配置的流量限制
		UsedTraffic:   user.UsedTraffic,       // 使用用户本身的已使用流量
	}

	return response, nil
}

// extractIPFromEndpoint 从endpoint中提取纯IP地址（移除端口号）
func (s *UserDashboardService) extractIPFromEndpoint(endpoint string) string {
	if endpoint == "" {
		return ""
	}

	// 移除协议前缀
	if strings.Contains(endpoint, "://") {
		parts := strings.Split(endpoint, "://")
		if len(parts) > 1 {
			endpoint = parts[1]
		}
	}

	// 处理IPv6地址
	if strings.HasPrefix(endpoint, "[") {
		closeBracket := strings.Index(endpoint, "]")
		if closeBracket > 0 {
			return endpoint[1:closeBracket]
		}
	}

	// 处理IPv4地址
	colonIndex := strings.LastIndex(endpoint, ":")
	if colonIndex > 0 {
		// 检查是否是IPv6地址（多个冒号）
		if strings.Count(endpoint, ":") > 1 {
			return endpoint // IPv6地址不处理
		}
		// IPv4地址，移除端口
		return endpoint[:colonIndex]
	}

	return endpoint
}
