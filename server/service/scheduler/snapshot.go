package scheduler

import (
	"context"
	"sync"
	"time"

	"oneclickvirt/global"
	snapshotSvc "oneclickvirt/service/snapshot"

	"go.uber.org/zap"
)

// SnapshotSchedulerService executes due snapshot schedules.
type SnapshotSchedulerService struct {
	service   *snapshotSvc.Service
	stopChan  chan struct{}
	isRunning bool
	runMu     sync.Mutex
	mu        sync.RWMutex
}

func NewSnapshotSchedulerService() *SnapshotSchedulerService {
	return &SnapshotSchedulerService{
		service:  &snapshotSvc.Service{},
		stopChan: make(chan struct{}),
	}
}

func (s *SnapshotSchedulerService) Start(ctx context.Context) {
	s.mu.Lock()
	if s.isRunning {
		s.mu.Unlock()
		return
	}
	s.stopChan = make(chan struct{})
	s.isRunning = true
	s.mu.Unlock()
	global.APP_LOG.Info("启动计划快照调度器")
	go s.loop(ctx)
}

func (s *SnapshotSchedulerService) Stop() {
	s.mu.Lock()
	if !s.isRunning {
		s.mu.Unlock()
		return
	}
	s.isRunning = false
	close(s.stopChan)
	s.mu.Unlock()
	global.APP_LOG.Info("计划快照调度器已停止")
}

func (s *SnapshotSchedulerService) loop(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			global.APP_LOG.Error("计划快照调度器panic", zap.Any("panic", r), zap.Stack("stack"))
		}
	}()
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopChan:
			return
		case <-ticker.C:
			if global.APP_DB == nil {
				continue
			}
			if !s.runMu.TryLock() {
				global.APP_LOG.Debug("计划快照调度仍在运行中，跳过本轮触发")
				continue
			}
			func() {
				defer s.runMu.Unlock()
				s.service.RunDueSchedules(ctx)
			}()
		}
	}
}
