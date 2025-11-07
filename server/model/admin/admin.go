package admin

import (
	"time"

	providerModel "oneclickvirt/model/provider"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Task 任务模型 - 用于异步任务管理
type Task struct {
	// 基础字段
	ID        uint           `json:"id" gorm:"primarykey"`                                 // 任务主键ID
	UUID      string         `json:"uuid" gorm:"uniqueIndex;not null;size:36"`             // 任务唯一标识符
	CreatedAt time.Time      `json:"createdAt" gorm:"index:idx_status_created,priority:2"` // 任务创建时间
	UpdatedAt time.Time      `json:"updatedAt"`                                            // 任务更新时间
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`                                       // 软删除时间

	// 任务基本信息
	TaskType string `json:"taskType" gorm:"not null;size:32"`                                                                               // 任务类型：create, start, stop, restart, reset, delete, reset-password
	Status   string `json:"status" gorm:"default:pending;size:32;index:idx_status_created,priority:1;index:idx_provider_status,priority:2"` // 任务状态：pending, processing, running, completed, failed, cancelling, cancelled, timeout
	Progress int    `json:"progress" gorm:"default:0"`                                                                                      // 任务执行进度百分比（0-100）

	// 错误和状态信息
	ErrorMessage  string `json:"errorMessage" gorm:"type:text"` // 任务失败时的错误信息
	CancelReason  string `json:"cancelReason" gorm:"type:text"` // 任务取消的原因
	StatusMessage string `json:"statusMessage" gorm:"size:512"` // 当前状态的描述信息
	TaskData      string `json:"taskData" gorm:"type:text"`     // 任务执行所需的数据（JSON格式）

	// 时间信息
	StartedAt         *time.Time `json:"startedAt"`                           // 任务开始执行时间
	CompletedAt       *time.Time `json:"completedAt"`                         // 任务完成时间
	EstimatedDuration int        `json:"estimatedDuration" gorm:"default:0"`  // 预计执行时长（秒）
	TimeoutDuration   int        `json:"timeoutDuration" gorm:"default:1800"` // 任务超时时间（秒，默认30分钟）

	// 预分配的实例配置信息（用于显示和排队估算）
	PreallocatedCPU       int `json:"preallocatedCpu" gorm:"default:0"`       // 预分配的CPU核心数
	PreallocatedMemory    int `json:"preallocatedMemory" gorm:"default:0"`    // 预分配的内存(MB)
	PreallocatedDisk      int `json:"preallocatedDisk" gorm:"default:0"`      // 预分配的磁盘(MB)
	PreallocatedBandwidth int `json:"preallocatedBandwidth" gorm:"default:0"` // 预分配的带宽(Mbps)

	// 关联信息
	UserID     uint  `json:"userId" gorm:"index"`                                    // 任务所属用户ID
	ProviderID *uint `json:"providerId" gorm:"index:idx_provider_status,priority:1"` // 执行任务的Provider ID（可为空）
	InstanceID *uint `json:"instanceId"`                                             // 关联的实例ID（可选，用于实例相关任务）

	// 关联对象
	Provider *providerModel.Provider `json:"provider,omitempty" gorm:"foreignKey:ProviderID"` // 关联的Provider对象

	// 控制标志
	CanForceStop     bool `json:"canForceStop" gorm:"default:false"`    // 是否可以强制停止（仅管理员）
	IsForceStoppable bool `json:"isForceStoppable" gorm:"default:true"` // 是否允许被强制停止
}

func (t *Task) BeforeCreate(tx *gorm.DB) error {
	t.UUID = uuid.New().String()
	return nil
}

// AuditLog 审计日志模型
type AuditLog struct {
	ID         uint           `json:"id" gorm:"primarykey"`
	CreatedAt  time.Time      `json:"createdAt"`
	UpdatedAt  time.Time      `json:"updatedAt"`
	DeletedAt  gorm.DeletedAt `json:"-" gorm:"index"` // 软删除字段
	UserID     *uint          `json:"userId"`         // 改为可空，未登录用户可能没有UserID
	Username   string         `json:"username" gorm:"size:64"`
	Method     string         `json:"method" gorm:"size:16"`
	Path       string         `json:"path" gorm:"size:255"`
	StatusCode int            `json:"statusCode"`
	Latency    int64          `json:"latency"`
	ClientIP   string         `json:"clientIP" gorm:"size:64"`
	UserAgent  string         `json:"userAgent" gorm:"size:255"`
	Request    string         `json:"request" gorm:"type:text"`
	Response   string         `json:"response" gorm:"type:text"`
}

// SystemConfig 系统配置模型
type SystemConfig struct {
	ID          uint           `json:"id" gorm:"primarykey"`
	Key         string         `json:"key" gorm:"uniqueIndex;not null;size:64"`
	Value       string         `json:"value" gorm:"type:text"`
	Description string         `json:"description" gorm:"size:255"`
	Category    string         `json:"category" gorm:"size:32"`
	Type        string         `json:"type" gorm:"size:20;not null;default:string"` // 配置类型
	IsPublic    bool           `json:"isPublic" gorm:"not null;default:false"`      // 是否公开
	CreatedAt   time.Time      `json:"createdAt"`
	UpdatedAt   time.Time      `json:"updatedAt"`
	DeletedAt   gorm.DeletedAt `json:"-" gorm:"index"` // 软删除字段
}
