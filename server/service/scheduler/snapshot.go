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
	runCancel context.CancelFunc
	isRunning bool
	stopping  bool
	wg        sync.WaitGroup
	doneChan  chan struct{}
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
	if s.isRunning || s.stopping {
		s.mu.Unlock()
		return
	}
	s.stopChan = make(chan struct{})
	stopChan := s.stopChan
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.runCancel = cancel
	s.doneChan = make(chan struct{})
	s.isRunning = true
	s.wg.Add(1)
	s.mu.Unlock()
	global.APP_LOG.Info("启动计划快照调度器")
	go s.loop(runCtx, stopChan)
}

func (s *SnapshotSchedulerService) Stop() {
	s.mu.Lock()
	if !s.isRunning {
		done := s.doneChan
		s.mu.Unlock()
		if done != nil {
			select {
			case <-done:
			case <-time.After(30 * time.Second):
				global.APP_LOG.Warn("计划快照调度器后台任务仍未结束")
			}
		}
		return
	}
	s.isRunning = false
	s.stopping = true
	stopChan := s.stopChan
	cancel := s.runCancel
	done := s.doneChan
	close(stopChan)
	if cancel != nil {
		cancel()
	}
	s.mu.Unlock()

	waitDone := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(waitDone)
	}()
	select {
	case <-waitDone:
		if done != nil {
			<-done
		}
		s.finishStop(stopChan)
	case <-time.After(30 * time.Second):
		global.APP_LOG.Warn("计划快照调度器关闭超时，等待后台任务结束后再允许重启")
		go func() {
			<-waitDone
			if done != nil {
				<-done
			}
			s.finishStop(stopChan)
		}()
	}
	global.APP_LOG.Info("计划快照调度器已停止")
}

func (s *SnapshotSchedulerService) finishStop(stopChan chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopChan == stopChan {
		s.stopping = false
		s.runCancel = nil
		s.doneChan = nil
	}
}

func (s *SnapshotSchedulerService) loop(ctx context.Context, stopChan <-chan struct{}) {
	defer func() {
		if r := recover(); r != nil {
			global.APP_LOG.Error("计划快照调度器panic", zap.Any("panic", r), zap.Stack("stack"))
		}
		s.mu.Lock()
		externalExit := false
		var done chan struct{}
		if s.stopChan == stopChan {
			s.isRunning = false
			if !s.stopping {
				s.stopping = true
				externalExit = true
			}
			done = s.doneChan
		}
		s.mu.Unlock()
		if done != nil {
			close(done)
		}
		s.wg.Done()
		if externalExit {
			s.mu.Lock()
			if s.stopChan == stopChan {
				s.stopping = false
				s.runCancel = nil
				s.doneChan = nil
			}
			s.mu.Unlock()
		}
	}()
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stopChan:
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
