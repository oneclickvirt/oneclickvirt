package monitoring

import (
	"time"

	"gorm.io/gorm"
)

// PerformanceMetric 性能指标历史记录
type PerformanceMetric struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	// 时间戳
	Timestamp time.Time `gorm:"index:idx_timestamp;not null" json:"timestamp"`

	// 系统基础指标
	GoroutineCount int `json:"goroutine_count"`
	CPUCount       int `json:"cpu_count"`

	// 内存指标 (MB)
	MemoryAlloc      uint64 `json:"memory_alloc"`       // 当前分配的内存
	MemoryTotalAlloc uint64 `json:"memory_total_alloc"` // 累计分配的内存
	MemorySys        uint64 `json:"memory_sys"`         // 从系统获取的内存
	MemoryHeapAlloc  uint64 `json:"memory_heap_alloc"`  // 堆上分配的内存
	MemoryHeapSys    uint64 `json:"memory_heap_sys"`    // 堆从系统获取的内存
	MemoryStackInuse uint64 `json:"memory_stack_inuse"` // 栈使用的内存

	// GC 指标
	GCCount      uint32 `json:"gc_count"`       // GC次数
	GCPauseTotal uint64 `json:"gc_pause_total"` // GC总暂停时间(ns)
	GCPauseAvg   uint64 `json:"gc_pause_avg"`   // GC平均暂停时间(ns)
	GCLastPause  uint64 `json:"gc_last_pause"`  // 上次GC暂停时间(ns)
	NextGC       uint64 `json:"next_gc"`        // 下次GC触发阈值

	// 数据库连接池状态
	DBMaxOpenConnections int   `json:"db_max_open_connections"` // 最大连接数
	DBOpenConnections    int   `json:"db_open_connections"`     // 当前打开的连接数
	DBInUse              int   `json:"db_in_use"`               // 正在使用的连接数
	DBIdle               int   `json:"db_idle"`                 // 空闲连接数
	DBWaitCount          int64 `json:"db_wait_count"`           // 等待连接的总次数
	DBWaitDuration       int64 `json:"db_wait_duration"`        // 等待连接的总时间(ns)
	DBMaxIdleClosed      int64 `json:"db_max_idle_closed"`      // 因超过最大空闲数而关闭的连接数
	DBMaxLifetimeClosed  int64 `json:"db_max_lifetime_closed"`  // 因超过最大生命周期而关闭的连接数

	// SSH连接池状态
	SSHTotalConnections     int     `json:"ssh_total_connections"`     // SSH总连接数
	SSHHealthyConnections   int     `json:"ssh_healthy_connections"`   // SSH健康连接数
	SSHUnhealthyConnections int     `json:"ssh_unhealthy_connections"` // SSH不健康连接数
	SSHIdleConnections      int     `json:"ssh_idle_connections"`      // SSH空闲连接数
	SSHActiveConnections    int     `json:"ssh_active_connections"`    // SSH活跃连接数
	SSHMaxConnections       int     `json:"ssh_max_connections"`       // SSH最大连接数限制
	SSHUtilization          float64 `json:"ssh_utilization"`           // SSH连接池利用率(%)
	SSHOldestConnectionAge  int64   `json:"ssh_oldest_connection_age"` // 最老连接年龄(秒)
	SSHNewestConnectionAge  int64   `json:"ssh_newest_connection_age"` // 最新连接年龄(秒)
	SSHAvgConnectionAge     int64   `json:"ssh_avg_connection_age"`    // 平均连接年龄(秒)

	// 任务系统状态
	TaskRunningContexts int `json:"task_running_contexts"` // 运行中的任务上下文数量
	TaskProviderPools   int `json:"task_provider_pools"`   // Provider工作池数量
	TaskTotalQueueSize  int `json:"task_total_queue_size"` // 总队列大小
}

// TableName 指定表名
func (PerformanceMetric) TableName() string {
	return "performance_metrics"
}
