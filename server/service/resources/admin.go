package resources

import (
	"oneclickvirt/global"
	"oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	userModel "oneclickvirt/model/user"
	"oneclickvirt/service/auth"
)

// AdminDashboardService 管理员仪表板服务
type AdminDashboardService struct{}

// GetAdminDashboard 获取管理员仪表板数据
func (s *AdminDashboardService) GetAdminDashboard(ownerAdminID uint) (*admin.AdminDashboardResponse, error) {
	dashboard := &admin.AdminDashboardResponse{}

	var totalUsers, activeUsers, totalVMs, totalContainers, totalProviders, activeProviders int64

	// 统计用户（仅超管看到用户统计）
	if ownerAdminID == 0 {
		global.APP_DB.Model(&userModel.User{}).Count(&totalUsers)
		global.APP_DB.Model(&userModel.User{}).Where("status = ?", 1).Count(&activeUsers)
	}

	// 构建Provider过滤条件
	providerQuery := global.APP_DB.Model(&providerModel.Provider{})
	if ownerAdminID > 0 {
		providerQuery = providerQuery.Where("owner_admin_id = ?", ownerAdminID)
	}

	// 统计Provider
	providerQuery.Count(&totalProviders)
	providerActiveQuery := global.APP_DB.Model(&providerModel.Provider{}).Where("(connection_type <> ? AND status IN (?, ?)) OR (connection_type = ? AND agent_status = ?)", "agent", "active", "partial", "agent", "online")
	if ownerAdminID > 0 {
		providerActiveQuery = providerActiveQuery.Where("owner_admin_id = ?", ownerAdminID)
	}
	providerActiveQuery.Count(&activeProviders)

	// 统计实例
	instanceQuery := global.APP_DB.Model(&providerModel.Instance{})
	if ownerAdminID > 0 {
		var providerIDs []uint
		global.APP_DB.Model(&providerModel.Provider{}).Where("owner_admin_id = ?", ownerAdminID).Pluck("id", &providerIDs)
		if len(providerIDs) > 0 {
			instanceQuery = instanceQuery.Where("provider_id IN ?", providerIDs)
		} else {
			instanceQuery = instanceQuery.Where("1 = 0") // no results
		}
	}

	instanceQuery.Where("instance_type = ? AND status NOT IN (?)", "vm", []string{"deleted", "deleting", "failed"}).Count(&totalVMs)
	// Need a fresh query for containers
	instanceQuery2 := global.APP_DB.Model(&providerModel.Instance{})
	if ownerAdminID > 0 {
		var providerIDs []uint
		global.APP_DB.Model(&providerModel.Provider{}).Where("owner_admin_id = ?", ownerAdminID).Pluck("id", &providerIDs)
		if len(providerIDs) > 0 {
			instanceQuery2 = instanceQuery2.Where("provider_id IN ?", providerIDs)
		} else {
			instanceQuery2 = instanceQuery2.Where("1 = 0")
		}
	}
	instanceQuery2.Where("instance_type = ? AND status NOT IN (?)", "container", []string{"deleted", "deleting", "failed"}).Count(&totalContainers)

	// 统计运行中的实例
	var runningInstances int64
	runningQuery := global.APP_DB.Model(&providerModel.Instance{}).Where("status = ?", "running")
	if ownerAdminID > 0 {
		var providerIDs []uint
		global.APP_DB.Model(&providerModel.Provider{}).Where("owner_admin_id = ?", ownerAdminID).Pluck("id", &providerIDs)
		if len(providerIDs) > 0 {
			runningQuery = runningQuery.Where("provider_id IN ?", providerIDs)
		} else {
			runningQuery = runningQuery.Where("1 = 0")
		}
	}
	runningQuery.Count(&runningInstances)

	// 返回前端需要的字段名
	dashboard.Statistics.TotalUsers = int(totalUsers)
	dashboard.Statistics.TotalProviders = int(totalProviders) // 节点数量
	dashboard.Statistics.TotalVMs = int(totalVMs)
	dashboard.Statistics.TotalContainers = int(totalContainers)

	// 保留原有字段以兼容其他可能的用途
	dashboard.Statistics.ActiveUsers = int(activeUsers)
	dashboard.Statistics.TotalInstances = int(totalVMs + totalContainers)
	dashboard.Statistics.RunningInstances = int(runningInstances) // 使用真实的运行实例统计
	dashboard.Statistics.TotalProviders = int(totalProviders)
	dashboard.Statistics.ActiveProviders = int(activeProviders)

	// 系统监控状态
	monitoringService := &MonitoringService{}
	systemStats := monitoringService.GetSystemStats()

	dashboard.SystemStatus.CPUUsage = systemStats.CPU.Usage
	dashboard.SystemStatus.MemoryUsage = systemStats.Memory.Usage
	dashboard.SystemStatus.DiskUsage = systemStats.Disk.Usage
	dashboard.SystemStatus.Uptime = systemStats.Runtime.Uptime

	// 资源使用统计
	var resourceStats struct {
		TotalCPUCores int64
		UsedCPUCores  int64
		TotalMemory   int64
		UsedMemory    int64
		TotalDisk     int64
		UsedDisk      int64
	}
	resourceQuery := global.APP_DB.Model(&providerModel.Provider{})
	if ownerAdminID > 0 {
		resourceQuery = resourceQuery.Where("owner_admin_id = ?", ownerAdminID)
	}
	resourceQuery.
		Select("COALESCE(SUM(node_cpu_cores), 0) as total_cpu_cores, COALESCE(SUM(used_cpu_cores), 0) as used_cpu_cores, COALESCE(SUM(node_memory_total), 0) as total_memory, COALESCE(SUM(used_memory), 0) as used_memory, COALESCE(SUM(node_disk_total), 0) as total_disk, COALESCE(SUM(used_disk), 0) as used_disk").
		Scan(&resourceStats)
	dashboard.ResourceUsage.TotalCPUCores = resourceStats.TotalCPUCores
	dashboard.ResourceUsage.UsedCPUCores = resourceStats.UsedCPUCores
	dashboard.ResourceUsage.TotalMemoryMB = resourceStats.TotalMemory
	dashboard.ResourceUsage.UsedMemoryMB = resourceStats.UsedMemory
	dashboard.ResourceUsage.TotalDiskMB = resourceStats.TotalDisk
	dashboard.ResourceUsage.UsedDiskMB = resourceStats.UsedDisk

	return dashboard, nil
}

// GetInstanceTypePermissions 获取实例类型权限配置
func (s *AdminDashboardService) GetInstanceTypePermissions() map[string]interface{} {
	permissions := global.GetAppConfig().Quota.InstanceTypePermissions

	return map[string]interface{}{
		"minLevelForContainer":       permissions.MinLevelForContainer,
		"minLevelForVM":              permissions.MinLevelForVM,
		"minLevelForDeleteContainer": permissions.MinLevelForDeleteContainer,
		"minLevelForDeleteVM":        permissions.MinLevelForDeleteVM,
		"minLevelForResetContainer":  permissions.MinLevelForResetContainer,
		"minLevelForResetVM":         permissions.MinLevelForResetVM,
	}
}

// UpdateInstanceTypePermissions 更新实例类型权限配置
func (s *AdminDashboardService) UpdateInstanceTypePermissions(req admin.UpdateInstanceTypePermissionsRequest) error {
	// 使用ConfigService来保存配置
	configService := auth.ConfigService{}
	return configService.SaveInstanceTypePermissions(
		req.MinLevelForContainer,
		req.MinLevelForVM,
		req.MinLevelForDeleteContainer,
		req.MinLevelForDeleteVM,
		req.MinLevelForResetContainer,
		req.MinLevelForResetVM,
	)
}
