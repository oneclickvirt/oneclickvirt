package scheduler

import (
	"context"
	"sync"
	"time"

	"oneclickvirt/global"
	agentSvc "oneclickvirt/service/agent"

	"go.uber.org/zap"
)

// ControllerPortHealthSchedulerService 控制端端口转发健康检查调度服务。
// 定期检查所有控制端端口转发的监听器是否在运行，自动修复已失效的监听器。
// 解决主控重启或 Agent 重连后端口转发丢失的问题。
type ControllerPortHealthSchedulerService struct {
	stopChan  chan struct{}
	mu        sync.RWMutex
	isRunning bool
}

// NewControllerPortHealthSchedulerService 创建控制端端口转发健康检查调度服务。
func NewControllerPortHealthSchedulerService() *ControllerPortHealthSchedulerService {
	return &ControllerPortHealthSchedulerService{
		stopChan: make(chan struct{}),
	}
}

// Start 启动控制端端口转发健康检查调度器。
// 每 2 分钟检查一次所有活跃的 controller 类型端口映射，
// 自动修复监听器未运行的情况。
func (s *ControllerPortHealthSchedulerService) Start(ctx context.Context) {
	s.mu.Lock()
	if s.isRunning {
		s.mu.Unlock()
		global.APP_LOG.Warn("控制端端口转发健康检查调度器已在运行中")
		return
	}
	s.stopChan = make(chan struct{})
	s.isRunning = true
	s.mu.Unlock()

	global.APP_LOG.Info("启动控制端端口转发健康检查调度器")

	go s.run(ctx)
}

// Stop 停止控制端端口转发健康检查调度器。
func (s *ControllerPortHealthSchedulerService) Stop() {
	s.mu.Lock()
	if !s.isRunning {
		s.mu.Unlock()
		return
	}
	s.isRunning = false
	s.mu.Unlock()

	global.APP_LOG.Info("停止控制端端口转发健康检查调度器")
	close(s.stopChan)
}

// IsRunning 检查调度器是否正在运行。
func (s *ControllerPortHealthSchedulerService) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isRunning
}

// run 运行定期检查循环。
func (s *ControllerPortHealthSchedulerService) run(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			global.APP_LOG.Error("控制端端口转发健康检查 goroutine panic",
				zap.Any("panic", r),
				zap.Stack("stack"))
		}
		global.APP_LOG.Info("控制端端口转发健康检查任务已停止")
	}()

	// 启动后等待 90 秒，给 Agent 连接和启动恢复留出时间窗口
	initialDelay := 90 * time.Second
	global.APP_LOG.Info("控制端端口转发健康检查将在 90 秒后开始",
		zap.Duration("delay", initialDelay))

	select {
	case <-ctx.Done():
		return
	case <-s.stopChan:
		return
	case <-time.After(initialDelay):
	}

	// 检查间隔：2 分钟
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopChan:
			return
		case <-ticker.C:
			s.runHealthCheck()
		}
	}
}

// runHealthCheck 执行一次健康检查。
func (s *ControllerPortHealthSchedulerService) runHealthCheck() {
	total, repaired := agentSvc.CheckAndRepairControllerPortForwards()

	if repaired > 0 {
		global.APP_LOG.Info("控制端端口转发健康检查完成",
			zap.Int("total", total),
			zap.Int("repaired", repaired))
	} else if total > 0 {
		global.APP_LOG.Debug("控制端端口转发健康检查通过",
			zap.Int("total", total))
	}
}

// TriggerImmediateCheck 立即触发一次健康检查（可用于 Agent 重连后）。
func (s *ControllerPortHealthSchedulerService) TriggerImmediateCheck() {
	go s.runHealthCheck()
}
