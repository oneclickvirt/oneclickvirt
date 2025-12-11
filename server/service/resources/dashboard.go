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
	cacheStopChan   chan struct{}
	cacheStopOnce   sync.Once
)

// StopDashboardCache 停止Dashboard缓存刷新任务
func StopDashboardCache() {
	cacheStopOnce.Do(func() {
		if cacheStopChan != nil {
			close(cacheStopChan)
		}
	})
}

// initStatsCache 初始化统计数据缓存
func initStatsCache() {
	statsCacheOnce.Do(func() {
		cacheStopChan = make(chan struct{})

		statsCache = utils.NewStatsCache(func() (interface{}, error) {
			service := &DashboardService{}
			return service.fetchDashboardStats()
		})

		// 启动后台定时刷新（固定5分钟，缓存本身有60秒有效期）
		go func() {
			defer func() {
				if r := recover(); r != nil {
					global.APP_LOG.Error("Dashboard统计缓存刷新goroutine panic", zap.Any("panic", r))
				}
			}()

			ticker := time.NewTicker(5 * time.Minute)
			defer ticker.Stop()

			for {
				select {
				case <-global.APP_SHUTDOWN_CONTEXT.Done():
					global.APP_LOG.Info("Dashboard统计缓存刷新任务已停止")
					return
				case <-cacheStopChan:
					global.APP_LOG.Info("Dashboard统计缓存刷新任务已手动停止")
					return
				case <-ticker.C:
					if _, err := statsCache.Update(); err != nil {
						global.APP_LOG.Error("定时更新统计数据缓存失败", zap.Error(err))
					} else {
						global.APP_LOG.Debug("定时更新统计数据缓存成功")
					}
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
	type RegionAggregation struct {
		Region     string
		Count      int64
		UsedQuota  int64
		TotalQuota int64
	}

	var results []RegionAggregation
	// 使用单次聚合查询替代遍历
	err := global.APP_DB.Model(&provider.Provider{}).
		Select("region, COUNT(*) as count, COALESCE(SUM(used_quota), 0) as used_quota, COALESCE(SUM(total_quota), 0) as total_quota").
		Group("region").
		Find(&results).Error

	if err != nil {
		return nil, err
	}

	regionStats := make([]dashboard.RegionStat, 0, len(results))
	for _, r := range results {
		regionStats = append(regionStats, dashboard.RegionStat{
			Region: r.Region,
			Count:  int(r.Count),
			Used:   int(r.UsedQuota),
			Total:  int(r.TotalQuota),
		})
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
	type ResourceAggregation struct {
		VMCount        int64
		ContainerCount int64
		UsedCPUCores   int64
		UsedMemory     int64
		UsedDisk       int64
	}

	var instanceCounts struct {
		VMCount        int64
		ContainerCount int64
	}

	// 使用条件聚合一次性获取两种实例数量（排除deleted、deleting、failed状态）
	global.APP_DB.Model(&provider.Instance{}).
		Select("SUM(CASE WHEN instance_type = 'vm' AND status NOT IN ('deleting', 'deleted', 'failed') THEN 1 ELSE 0 END) as vm_count, SUM(CASE WHEN instance_type = 'container' AND status NOT IN ('deleting', 'deleted', 'failed') THEN 1 ELSE 0 END) as container_count").
		Scan(&instanceCounts)

	var providerStats struct {
		UsedCPUCores int64
		UsedMemory   int64
		UsedDisk     int64
	}

	// 一次性获取所有provider的资源统计
	global.APP_DB.Model(&provider.Provider{}).
		Select("COALESCE(SUM(used_cpu_cores), 0) as used_cpu_cores, COALESCE(SUM(used_memory), 0) as used_memory, COALESCE(SUM(used_disk), 0) as used_disk").
		Scan(&providerStats)

	return &dashboard.ResourceUsageStats{
		VMCount:        instanceCounts.VMCount,
		ContainerCount: instanceCounts.ContainerCount,
		UsedCPUCores:   providerStats.UsedCPUCores,
		UsedMemory:     providerStats.UsedMemory,
		UsedDisk:       providerStats.UsedDisk,
	}, nil
}
