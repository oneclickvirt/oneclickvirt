package admin

import (
	"time"

	"gorm.io/gorm"
)

// ConfigurationTask 配置任务模型
type ConfigurationTask struct {
	ID        uint           `json:"id" gorm:"primarykey"`
	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`

	// 基本信息
	ProviderID uint   `json:"providerId" gorm:"not null;index"`
	TaskType   string `json:"taskType" gorm:"not null;size:32;default:auto_configure"` // auto_configure, cert_generate, health_check
	Status     string `json:"status" gorm:"not null;size:16;default:running"`          // pending, running, completed, failed, cancelled
	Progress   int    `json:"progress" gorm:"default:0"`                               // 0-100

	// 执行信息
	StartedAt    *time.Time `json:"startedAt"`
	CompletedAt  *time.Time `json:"completedAt"`
	ExecutorID   uint       `json:"executorId"` // 执行者用户ID
	ExecutorName string     `json:"executorName" gorm:"size:64"`

	// 结果信息
	Success      bool   `json:"success" gorm:"default:false"`
	ErrorMessage string `json:"errorMessage" gorm:"type:text"`
	ResultData   string `json:"resultData" gorm:"type:text"` // JSON格式存储结果数据

	// 日志相关
	LogOutput  string `json:"logOutput" gorm:"type:longtext"` // 完整的执行日志
	LogSummary string `json:"logSummary" gorm:"type:text"`    // 日志摘要

	// 关联信息
	Provider *Provider `json:"provider,omitempty" gorm:"foreignKey:ProviderID"`
}

// Provider 简化的Provider模型，用于关联查询
type Provider struct {
	ID       uint   `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Endpoint string `json:"endpoint"`
	Status   string `json:"status"`
}

func (ConfigurationTask) TableName() string {
	return "configuration_tasks"
}

// 任务状态常量
const (
	TaskStatusPending    = "pending"
	TaskStatusRunning    = "running"
	TaskStatusCancelling = "cancelling"
	TaskStatusCompleted  = "completed"
	TaskStatusFailed     = "failed"
	TaskStatusCancelled  = "cancelled"
)

// 任务类型常量
const (
	TaskTypeAutoConfig = "auto_configure"
)

// BeforeCreate 创建前钩子
func (t *ConfigurationTask) BeforeCreate(tx *gorm.DB) error {
	now := time.Now()
	if t.Status == TaskStatusRunning {
		t.StartedAt = &now
	}
	return nil
}

// BeforeUpdate 更新前钩子
func (t *ConfigurationTask) BeforeUpdate(tx *gorm.DB) error {
	now := time.Now()

	// 如果状态变为running，设置开始时间
	if t.Status == TaskStatusRunning && t.StartedAt == nil {
		t.StartedAt = &now
	}

	// 如果状态变为完成或失败，设置完成时间
	if (t.Status == TaskStatusCompleted || t.Status == TaskStatusFailed) && t.CompletedAt == nil {
		t.CompletedAt = &now
		if t.Status == TaskStatusCompleted {
			t.Success = true
			t.Progress = 100
		}
	}

	return nil
}

// IsRunning 检查任务是否正在运行
func (t *ConfigurationTask) IsRunning() bool {
	return t.Status == TaskStatusRunning || t.Status == TaskStatusPending || t.Status == TaskStatusCancelling
}
