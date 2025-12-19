package resources

import (
	"fmt"
	"oneclickvirt/utils"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	resourceModel "oneclickvirt/model/resource"
	userModel "oneclickvirt/model/user"
	"oneclickvirt/service/cache"
	trafficService "oneclickvirt/service/traffic"

	"go.uber.org/zap"
)

// UserDashboardService 处理用户仪表板相关功能
type UserDashboardService struct{}

// GetUserDashboard 获取用户仪表板数据（带缓存）
func (s *UserDashboardService) GetUserDashboard(userID uint) (*userModel.UserDashboardResponse, error) {
	cacheService := cache.GetUserCacheService()
	cacheKey := cache.MakeUserDashboardKey(userID)

	// 尝试从缓存获取
	if cachedData, ok := cacheService.Get(cacheKey); ok {
		if dashboard, ok := cachedData.(*userModel.UserDashboardResponse); ok {
			return dashboard, nil
		}
	}

	// 缓存未命中，查询数据库
	dashboard, err := s.fetchUserDashboard(userID)
	if err != nil {
		return nil, err
	}

	// 缓存结果
	cacheService.Set(cacheKey, dashboard, cache.TTLUserDashboard)
	return dashboard, nil
}

// fetchUserDashboard 从数据库获取用户仪表板数据
func (s *UserDashboardService) fetchUserDashboard(userID uint) (*userModel.UserDashboardResponse, error) {
	var user userModel.User
	if err := global.APP_DB.First(&user, userID).Error; err != nil {
		return nil, err
	}

	// 使用单次查询统计所有实例相关数据
	type InstanceStats struct {
		TotalInstances   int64
		RunningInstances int64
		StoppedInstances int64
		Containers       int64
		VMs              int64
	}

	var stats InstanceStats
	// 使用子查询一次性获取所有统计数据（排除deleted、deleting、failed状态）
	err := global.APP_DB.Raw(`
		SELECT 
			COUNT(*) as total_instances,
			SUM(CASE WHEN status = 'running' THEN 1 ELSE 0 END) as running_instances,
			SUM(CASE WHEN status = 'stopped' THEN 1 ELSE 0 END) as stopped_instances,
			SUM(CASE WHEN instance_type = 'container' AND status NOT IN ('deleting', 'deleted', 'failed') THEN 1 ELSE 0 END) as containers,
			SUM(CASE WHEN instance_type = 'vm' AND status NOT IN ('deleting', 'deleted', 'failed') THEN 1 ELSE 0 END) as vms
		FROM instances
		WHERE user_id = ? AND status NOT IN ('deleting', 'deleted', 'failed')
	`, userID).Scan(&stats).Error

	if err != nil {
		return nil, fmt.Errorf("统计用户实例失败: %v", err)
	}

	var recentInstances []providerModel.Instance
	global.APP_DB.Where("user_id = ? AND status NOT IN (?)", userID, []string{"deleting", "deleted", "failed"}).Order("created_at DESC").Limit(5).Find(&recentInstances)

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

	// 统计当前预留的资源只查询未过期的预留
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

	dashboard.Instances.Total = int(stats.TotalInstances)
	dashboard.Instances.Running = int(stats.RunningInstances)
	dashboard.Instances.Stopped = int(stats.StoppedInstances)
	dashboard.Instances.Containers = int(stats.Containers)
	dashboard.Instances.VMs = int(stats.VMs)

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

	// 使用单个聚合查询统计当前使用的资源
	type ResourceStats struct {
		UsedInstances  int64
		ContainerCount int64
		VMCount        int64
		UsedCPU        int64
		UsedMemory     int64
		UsedDisk       int64
		UsedBandwidth  int64
	}

	var resourceStats ResourceStats
	err := global.APP_DB.Raw(`
		SELECT 
			COUNT(*) as used_instances,
			SUM(CASE WHEN instance_type = 'container' THEN 1 ELSE 0 END) as container_count,
			SUM(CASE WHEN instance_type = 'vm' THEN 1 ELSE 0 END) as vm_count,
			COALESCE(SUM(cpu), 0) as used_cpu,
			COALESCE(SUM(memory), 0) as used_memory,
			COALESCE(SUM(disk), 0) as used_disk,
			COALESCE(SUM(bandwidth), 0) as used_bandwidth
		FROM instances
		WHERE user_id = ? AND status NOT IN ('deleting', 'deleted', 'failed')
	`, userID).Scan(&resourceStats).Error

	if err != nil {
		return nil, fmt.Errorf("统计用户资源使用失败: %v", err)
	}

	// 统计预留资源
	type ReservationStats struct {
		ReservationCount  int64
		ReservedCPU       int64
		ReservedMemory    int64
		ReservedDisk      int64
		ReservedBandwidth int64
	}

	var reservationStats ReservationStats
	err = global.APP_DB.Raw(`
		SELECT 
			COUNT(*) as reservation_count,
			COALESCE(SUM(cpu), 0) as reserved_cpu,
			COALESCE(SUM(memory), 0) as reserved_memory,
			COALESCE(SUM(disk), 0) as reserved_disk,
			COALESCE(SUM(bandwidth), 0) as reserved_bandwidth
		FROM resource_reservations
		WHERE user_id = ? AND expires_at > ?
	`, userID, time.Now()).Scan(&reservationStats).Error

	if err != nil {
		return nil, fmt.Errorf("统计用户预留资源失败: %v", err)
	}

	// 计算总使用量（实例 + 预留）
	usedInstances := int(resourceStats.UsedInstances) + int(reservationStats.ReservationCount)
	usedCPU := int(resourceStats.UsedCPU) + int(reservationStats.ReservedCPU)
	usedMemory := int(resourceStats.UsedMemory) + int(reservationStats.ReservedMemory)
	usedDisk := int(resourceStats.UsedDisk) + int(reservationStats.ReservedDisk)
	usedBandwidth := int(resourceStats.UsedBandwidth) + int(reservationStats.ReservedBandwidth)
	containerCount := int(resourceStats.ContainerCount)
	vmCount := int(resourceStats.VMCount)

	// 查询当月流量使用情况（从pmacct_traffic_records实时聚合）
	trafficQueryService := trafficService.NewQueryService()
	year, month, _ := time.Now().Date()
	monthlyTrafficStats, err := trafficQueryService.GetUserMonthlyTraffic(user.ID, year, int(month))

	var usedTrafficMB int64
	if err != nil {
		global.APP_LOG.Warn("获取用户流量数据失败，使用默认值",
			zap.Uint("userID", user.ID),
			zap.Error(err))
		usedTrafficMB = 0
	} else {
		usedTrafficMB = int64(monthlyTrafficStats.ActualUsageMB)
	}

	response := &userModel.UserLimitsResponse{
		Level:          user.Level,
		MaxInstances:   levelLimits.MaxInstances,
		UsedInstances:  usedInstances,
		ContainerCount: containerCount,
		VMCount:        vmCount,
		MaxCpu:         maxResources.CPU,
		UsedCpu:        usedCPU,
		MaxMemory:      int(maxResources.Memory),
		UsedMemory:     usedMemory,
		MaxDisk:        int(maxResources.Disk),
		UsedDisk:       usedDisk,
		MaxBandwidth:   maxResources.Bandwidth,
		UsedBandwidth:  usedBandwidth,
		MaxTraffic:     levelLimits.MaxTraffic, // 使用等级配置的流量限制
		UsedTraffic:    usedTrafficMB,          // 使用实时查询的流量数据
	}

	return response, nil
}

// extractIPFromEndpoint 从endpoint中提取纯IP地址（使用全局函数）
func (s *UserDashboardService) extractIPFromEndpoint(endpoint string) string {
	return utils.ExtractIPFromEndpoint(endpoint)
}
