package global

import (
	"context"
	"oneclickvirt/config"

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

// DBManagerStats 数据库管理器统计信息（用于性能监控，避免循环导入）
type DBManagerStats struct {
	Connected         bool   `json:"connected"`
	Reconnecting      bool   `json:"reconnecting"`
	HeartbeatActive   bool   `json:"heartbeat_active"`
	MaxReconnectRetry int    `json:"max_reconnect_retry"`
	ReconnectInterval string `json:"reconnect_interval"`
}

var (
	APP_DB                        *gorm.DB
	APP_LOG                       *zap.Logger
	APP_CONFIG                    config.Server
	APP_VP                        *viper.Viper
	APP_ENGINE                    *gin.Engine
	APP_SCHEDULER                 Scheduler                    // 任务调度器全局变量
	APP_MONITORING_SCHEDULER      MonitoringScheduler          // 监控调度器全局变量
	APP_PROVIDER_HEALTH_SCHEDULER ProviderHealthScheduler      // Provider健康检查调度器全局变量
	APP_TASK_LOCK_RELEASER        TaskLockReleaser             // 任务锁释放器全局变量
	APP_SSH_POOL                  SSHPoolManager               // SSH连接池管理器全局变量
	APP_CAPTCHA_STORE             CaptchaStore                 // 验证码存储全局变量
	APP_SYSTEM_INIT_CALLBACK      SystemInitializationCallback // 系统初始化完成回调函数
	APP_SHUTDOWN_CONTEXT          context.Context              // 系统关闭上下文
	APP_SHUTDOWN_CANCEL           context.CancelFunc           // 系统关闭取消函数
	APP_JWT_SECRET                string                       // JWT密钥（从数据库加载，重启后保持不变）
	APP_DB_MANAGER_STATS          *DBManagerStats              // 数据库管理器统计信息（由DatabaseManager定期更新）
)
