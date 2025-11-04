package resources

import (
	"oneclickvirt/global"
	"oneclickvirt/model/dashboard"
	"oneclickvirt/model/provider"
	"oneclickvirt/model/user"
	"oneclickvirt/utils"
	"sync"
	"time"

	"go.uber.org/zap"
)

type DashboardService struct{}

var (
	statsCache      *utils.StatsCache
	statsCacheOnce  sync.Once
	statsCacheMutex sync.RWMutex
)

// initStatsCache 初始化统计数据缓存
func initStatsCache() {
	statsCacheOnce.Do(func() {
		statsCache = utils.NewStatsCache(func() (interface{}, error) {
			service := &DashboardService{}
			return service.fetchDashboardStats()
		})

		// 启动后台定时刷新
		go func() {
			ticker := time.NewTicker(5 * time.Minute)
			defer ticker.Stop()

			for range ticker.C {
				if _, err := statsCache.Update(); err != nil {
					global.APP_LOG.Error("定时更新统计数据缓存失败", zap.Error(err))
				} else {
					global.APP_LOG.Debug("定时更新统计数据缓存成功")
				}
			}
		}()
	})
}

// fetchDashboardStats 获取统计数据（不使用缓存）
func (s *DashboardService) fetchDashboardStats() (*dashboard.DashboardStats, error) {
	global.APP_LOG.Debug("获取Dashboard统计信息")

	regionStats, err := s.getRegionStats()
	if err != nil {
		global.APP_LOG.Error("获取地区统计失败", zap.Error(err))
		return nil, err
	}

	quotaStats, err := s.getQuotaStats()
	if err != nil {
		global.APP_LOG.Error("获取配额统计失败", zap.Error(err))
		return nil, err
	}

	userStats, err := s.getUserStats()
	if err != nil {
		global.APP_LOG.Error("获取用户统计失败", zap.Error(err))
		return nil, err
	}

	resourceUsage, err := s.getResourceUsageStats()
	if err != nil {
		global.APP_LOG.Error("获取资源使用统计失败", zap.Error(err))
		return nil, err
	}

	global.APP_LOG.Debug("Dashboard统计信息获取成功",
		zap.Int("regionCount", len(regionStats)),
		zap.Int("totalUsers", userStats.TotalUsers),
		zap.Int64("vmCount", resourceUsage.VMCount),
		zap.Int64("containerCount", resourceUsage.ContainerCount))
	return &dashboard.DashboardStats{
		RegionStats:   regionStats,
		QuotaStats:    *quotaStats,
		UserStats:     *userStats,
		ResourceUsage: *resourceUsage,
	}, nil
}

func (s *DashboardService) GetDashboardStats() (*dashboard.DashboardStats, error) {
	// 初始化缓存（仅第一次）
	initStatsCache()

	// 从缓存获取数据
	data, err := statsCache.Get()
	if err != nil {
		return nil, err
	}

	stats, ok := data.(*dashboard.DashboardStats)
	if !ok {
		// 如果类型不匹配，重新获取
		return s.fetchDashboardStats()
	}

	return stats, nil
}

func (s *DashboardService) getRegionStats() ([]dashboard.RegionStat, error) {
	var providers []provider.Provider
	if err := global.APP_DB.Find(&providers).Error; err != nil {
		return nil, err
	}

	regionMap := make(map[string]*dashboard.RegionStat)

	for _, p := range providers {
		if regionMap[p.Region] == nil {
			regionMap[p.Region] = &dashboard.RegionStat{
				Region: p.Region,
				Count:  0,
				Used:   0,
				Total:  0,
			}
		}
		regionMap[p.Region].Count++
		regionMap[p.Region].Used += p.UsedQuota
		regionMap[p.Region].Total += p.TotalQuota
	}

	var regionStats []dashboard.RegionStat
	for _, stat := range regionMap {
		regionStats = append(regionStats, *stat)
	}

	return regionStats, nil
}

func (s *DashboardService) getQuotaStats() (*dashboard.QuotaStat, error) {
	var totalQuota, usedQuota int64

	global.APP_DB.Model(&provider.Provider{}).Select("COALESCE(SUM(total_quota), 0)").Scan(&totalQuota)
	global.APP_DB.Model(&provider.Provider{}).Select("COALESCE(SUM(used_quota), 0)").Scan(&usedQuota)

	return &dashboard.QuotaStat{
		Used:      int(usedQuota),
		Available: int(totalQuota - usedQuota),
		Total:     int(totalQuota),
	}, nil
}

func (s *DashboardService) getUserStats() (*dashboard.UserStat, error) {
	var totalUsers, activeUsers, adminUsers int64

	global.APP_DB.Model(&user.User{}).Count(&totalUsers)
	global.APP_DB.Model(&user.User{}).Where("status = ?", 1).Count(&activeUsers)
	global.APP_DB.Model(&user.User{}).Where("user_type = ?", "admin").Count(&adminUsers)

	return &dashboard.UserStat{
		TotalUsers:  int(totalUsers),
		ActiveUsers: int(activeUsers),
		AdminUsers:  int(adminUsers),
	}, nil
}

func (s *DashboardService) getResourceUsageStats() (*dashboard.ResourceUsageStats, error) {
	var vmCount, containerCount int64
	var usedCPUCores, usedMemory, usedDisk int64

	// 统计虚拟机和容器数量
	global.APP_DB.Model(&provider.Instance{}).Where("instance_type = ? AND status != ? AND status != ?", "vm", "deleting", "deleted").Count(&vmCount)
	global.APP_DB.Model(&provider.Instance{}).Where("instance_type = ? AND status != ? AND status != ?", "container", "deleting", "deleted").Count(&containerCount)

	// 统计资源使用情况
	global.APP_DB.Model(&provider.Provider{}).Select("COALESCE(SUM(used_cpu_cores), 0)").Scan(&usedCPUCores)
	global.APP_DB.Model(&provider.Provider{}).Select("COALESCE(SUM(used_memory), 0)").Scan(&usedMemory)
	global.APP_DB.Model(&provider.Provider{}).Select("COALESCE(SUM(used_disk), 0)").Scan(&usedDisk)

	return &dashboard.ResourceUsageStats{
		VMCount:        vmCount,
		ContainerCount: containerCount,
		UsedCPUCores:   usedCPUCores,
		UsedMemory:     usedMemory,
		UsedDisk:       usedDisk,
	}, nil
}
