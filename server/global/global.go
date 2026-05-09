package global

import (
	"context"
	"oneclickvirt/config"
	"sync"
	"sync/atomic"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Scheduler 调度器接口，避免循环导入
type Scheduler interface {
	StartScheduler()
	StopScheduler()
	TriggerTaskProcessing() // 立即触发任务处理
}

// MonitoringScheduler 监控调度器接口
type MonitoringScheduler interface {
	Start(ctx context.Context)
	Stop()
	IsRunning() bool
}

// ProviderHealthScheduler Provider健康检查调度器接口
type ProviderHealthScheduler interface {
	Start(ctx context.Context)
	Stop()
	IsRunning() bool
}

// TaskLockReleaser 任务锁释放器接口
type TaskLockReleaser interface {
	ReleaseTaskLocks(taskID uint)
}

// SSHPoolManager SSH连接池管理器接口
type SSHPoolManager interface {
	CloseAll()
}

// CaptchaStore 验证码存储接口（与base64Captcha.Store兼容）
type CaptchaStore interface {
	Set(id string, value string) error
	Get(id string, clear bool) string
	Verify(id, answer string, clear bool) bool
}

// SystemInitializationCallback 系统初始化完成后的回调函数类型
type SystemInitializationCallback func()

// InitProgressStatus 初始化进度状态
type InitProgressStatus string

const (
	InitStatusIdle       InitProgressStatus = "idle"
	InitStatusInProgress InitProgressStatus = "in_progress"
	InitStatusSuccess    InitProgressStatus = "success"
	InitStatusFailed     InitProgressStatus = "failed"
)

// InitProgressStep 单个初始化步骤
type InitProgressStep struct {
	Name    string             `json:"name"`
	Status  InitProgressStatus `json:"status"`  // idle | in_progress | success | failed
	Message string             `json:"message"` // 错误或描述信息
}

// InitProgress 系统初始化进度
type InitProgress struct {
	mu          sync.RWMutex
	Status      InitProgressStatus `json:"status"`
	CurrentStep int                `json:"current_step"`
	TotalSteps  int                `json:"total_steps"`
	Steps       []InitProgressStep `json:"steps"`
	ErrorMsg    string             `json:"error_msg"`
}

// Reset 重置进度（开始新的初始化）
func (p *InitProgress) Reset(steps []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Status = InitStatusInProgress
	p.CurrentStep = 0
	p.TotalSteps = len(steps)
	p.ErrorMsg = ""
	p.Steps = make([]InitProgressStep, len(steps))
	for i, name := range steps {
		p.Steps[i] = InitProgressStep{Name: name, Status: InitStatusIdle}
	}
}

// StartStep 标记某步骤开始
func (p *InitProgress) StartStep(index int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if index >= 0 && index < len(p.Steps) {
		p.Steps[index].Status = InitStatusInProgress
		p.CurrentStep = index
	}
}

// CompleteStep 标记某步骤完成
func (p *InitProgress) CompleteStep(index int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if index >= 0 && index < len(p.Steps) {
		p.Steps[index].Status = InitStatusSuccess
	}
}

// FailStep 标记某步骤失败
func (p *InitProgress) FailStep(index int, msg string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if index >= 0 && index < len(p.Steps) {
		p.Steps[index].Status = InitStatusFailed
		p.Steps[index].Message = msg
	}
	p.Status = InitStatusFailed
	p.ErrorMsg = msg
}

// Complete 标记整体完成
func (p *InitProgress) Complete() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Status = InitStatusSuccess
	p.CurrentStep = p.TotalSteps
}

// Abort 放弃当前初始化尝试，恢复为 idle 状态
// 用于因外部原因（如系统已初始化）拒绝初始化请求时，避免将状态置为 failed
func (p *InitProgress) Abort() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Status = InitStatusIdle
	p.Steps = nil
	p.TotalSteps = 0
	p.CurrentStep = 0
	p.ErrorMsg = ""
}

// Snapshot 返回当前进度快照（线程安全）
func (p *InitProgress) Snapshot() map[string]interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()
	steps := make([]InitProgressStep, len(p.Steps))
	copy(steps, p.Steps)
	return map[string]interface{}{
		"status":       p.Status,
		"current_step": p.CurrentStep,
		"total_steps":  p.TotalSteps,
		"steps":        steps,
		"error_msg":    p.ErrorMsg,
	}
}

// DBManagerStats 数据库管理器统计信息（用于性能监控，避免循环导入）
type DBManagerStats struct {
	Connected         bool   `json:"connected"`
	Reconnecting      bool   `json:"reconnecting"`
	HeartbeatActive   bool   `json:"heartbeat_active"`
	MaxReconnectRetry int    `json:"max_reconnect_retry"`
	ReconnectInterval string `json:"reconnect_interval"`
}

// GetAppConfig returns a consistent snapshot of the application configuration.
// Safe to call from any goroutine without additional locking.
func GetAppConfig() config.Server {
	if p := APP_CONFIG.Load(); p != nil {
		return *p
	}
	return config.Server{}
}

// SetAppConfig atomically replaces the global application configuration (copy-on-write).
func SetAppConfig(cfg config.Server) {
	APP_CONFIG.Store(&cfg)
}

var (
	APP_DB     *gorm.DB
	APP_LOG    *zap.Logger
	APP_CONFIG atomic.Pointer[config.Server] // access via GetAppConfig/SetAppConfig
	// CONFIG_MANAGER_READY 标记 ConfigManager 已从数据库完成初始化。
	// 设置后，viper 文件监听器（OnConfigChange）将跳过对 global.APP_CONFIG 的覆盖，
	// 防止启动阶段 YAML 写入触发的延迟事件在 API 保存后把旧值重新写回内存。
	CONFIG_MANAGER_READY          atomic.Bool
	APP_VP                        *viper.Viper
	APP_ENGINE                    *gin.Engine
	APP_SCHEDULER                 Scheduler                               // 任务调度器全局变量
	APP_MONITORING_SCHEDULER      MonitoringScheduler                     // 监控调度器全局变量
	APP_PROVIDER_HEALTH_SCHEDULER ProviderHealthScheduler                 // Provider健康检查调度器全局变量
	APP_TASK_LOCK_RELEASER        TaskLockReleaser                        // 任务锁释放器全局变量
	APP_SSH_POOL                  SSHPoolManager                          // SSH连接池管理器全局变量
	APP_CAPTCHA_STORE             CaptchaStore                            // 验证码存储全局变量
	APP_SYSTEM_INIT_CALLBACK      SystemInitializationCallback            // 系统初始化完成回调函数
	APP_SHUTDOWN_CONTEXT          context.Context                         // 系统关闭上下文
	APP_SHUTDOWN_CANCEL           context.CancelFunc                      // 系统关闭取消函数
	APP_JWT_SECRET                string                                  // JWT密钥（从数据库加载，重启后保持不变）
	APP_DB_MANAGER_STATS          *DBManagerStats                         // 数据库管理器统计信息（由DatabaseManager定期更新）
	APP_INIT_PROGRESS             = &InitProgress{Status: InitStatusIdle} // 系统初始化进度追踪
)
