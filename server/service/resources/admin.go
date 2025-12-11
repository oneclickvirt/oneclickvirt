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
func (s *AdminDashboardService) GetAdminDashboard() (*admin.AdminDashboardResponse, error) {
	dashboard := &admin.AdminDashboardResponse{}

	var totalUsers, activeUsers, totalVMs, totalContainers, totalProviders, activeProviders int64

	// 统计用户
	global.APP_DB.Model(&userModel.User{}).Count(&totalUsers)
	global.APP_DB.Model(&userModel.User{}).Where("status = ?", 1).Count(&activeUsers)

	// 统计虚拟机和容器（排除deleted、deleting、failed状态）
	global.APP_DB.Model(&providerModel.Instance{}).Where("instance_type = ? AND status NOT IN (?)", "vm", []string{"deleted", "deleting", "failed"}).Count(&totalVMs)
	global.APP_DB.Model(&providerModel.Instance{}).Where("instance_type = ? AND status NOT IN (?)", "container", []string{"deleted", "deleting", "failed"}).Count(&totalContainers)

	// 统计服务器 (Provider表)
	global.APP_DB.Model(&providerModel.Provider{}).Count(&totalProviders)
	// 统计活跃Provider（包括 active 和 partial 状态，因为它们都可以被用户使用）
	global.APP_DB.Model(&providerModel.Provider{}).Where("status = ? OR status = ?", "active", "partial").Count(&activeProviders)

	// 统计运行中的实例
	var runningInstances int64
	global.APP_DB.Model(&providerModel.Instance{}).Where("status = ? AND soft_deleted = ?", "running", false).Count(&runningInstances)

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

	return dashboard, nil
}

// GetInstanceTypePermissions 获取实例类型权限配置
func (s *AdminDashboardService) GetInstanceTypePermissions() map[string]interface{} {
	permissions := global.APP_CONFIG.Quota.InstanceTypePermissions

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
