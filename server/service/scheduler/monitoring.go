package scheduler

import (
	"context"
	"time"

	"oneclickvirt/global"
	monitoringModel "oneclickvirt/model/monitoring"

	"go.uber.org/zap"
)

// VnStatServiceInterface vnStat服务接口
type VnStatServiceInterface interface {
	CollectVnStatData(ctx context.Context) error
	CleanupOldVnStatData(days int) error
}

// MonitoringSchedulerService 监控调度服务
type MonitoringSchedulerService struct {
	vnstatService VnStatServiceInterface
	stopChan      chan struct{}
	isRunning     bool
}

// NewMonitoringSchedulerService 创建监控调度服务
func NewMonitoringSchedulerService(vnstatService VnStatServiceInterface) *MonitoringSchedulerService {
	return &MonitoringSchedulerService{
		vnstatService: vnstatService,
		stopChan:      make(chan struct{}),
		isRunning:     false,
	}
}

// Start 启动监控调度器
func (s *MonitoringSchedulerService) Start(ctx context.Context) {
	if s.isRunning {
		global.APP_LOG.Warn("监控调度器已在运行中")
		return
	}

	s.isRunning = true
	global.APP_LOG.Info("启动监控调度器")

	// 启动vnStat流量数据收集任务
	go s.startVnStatCollection(ctx)

	// 启动清理任务
	go s.startCleanupTask(ctx)
}

// Stop 停止监控调度器
func (s *MonitoringSchedulerService) Stop() {
	if !s.isRunning {
		return
	}

	global.APP_LOG.Info("停止监控调度器")
	close(s.stopChan)
	s.isRunning = false
}

// IsRunning 检查调度器是否正在运行
func (s *MonitoringSchedulerService) IsRunning() bool {
	return s.isRunning
}

// startVnStatCollection 启动vnStat流量数据收集任务
func (s *MonitoringSchedulerService) startVnStatCollection(ctx context.Context) {
	// vnStat数据收集间隔（30分钟）
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()

	// 检查是否有启用的vnStat接口，如果没有则等待
	for {
		select {
		case <-s.stopChan:
			global.APP_LOG.Info("vnStat数据收集任务已停止")
			return
		case <-ticker.C:
			// 检查数据库是否已初始化
			if global.APP_DB == nil {
				global.APP_LOG.Debug("数据库未初始化，跳过vnStat数据收集")
				continue
			}

			// 检查是否有启用的接口
			var count int64
			err := global.APP_DB.Model(&monitoringModel.VnStatInterface{}).Where("is_enabled = ?", true).Count(&count).Error
			if err != nil {
				global.APP_LOG.Error("检查vnStat接口状态失败", zap.Error(err))
				continue
			}

			// 只有在有启用的接口时才开始收集数据
			if count > 0 {
				if s.vnstatService != nil {
					if err := s.vnstatService.CollectVnStatData(ctx); err != nil {
						global.APP_LOG.Error("vnStat数据收集失败", zap.Error(err))
					}
				} else {
					global.APP_LOG.Debug("vnStat服务未配置，跳过数据收集")
				}
			} else {
				global.APP_LOG.Debug("没有启用的vnStat接口，跳过数据收集")
			}
		}
	}
}

// startCleanupTask 启动清理任务
func (s *MonitoringSchedulerService) startCleanupTask(ctx context.Context) {
	// 清理任务间隔（每天执行一次）
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopChan:
			global.APP_LOG.Info("监控数据清理任务已停止")
			return
		case <-ticker.C:
			// 清理90天前的vnStat数据
			if s.vnstatService != nil {
				if err := s.vnstatService.CleanupOldVnStatData(90); err != nil {
					global.APP_LOG.Error("清理过期vnStat数据失败", zap.Error(err))
				}
			} else {
				global.APP_LOG.Debug("vnStat服务未配置，跳过数据清理")
			}
		}
	}
}
