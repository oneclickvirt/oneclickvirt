package admin

import (
	"time"

	"gorm.io/gorm"
)

// TrafficMonitorTask 流量监控操作任务
type TrafficMonitorTask struct {
	ID           uint           `gorm:"primarykey" json:"id"`
	CreatedAt    time.Time      `json:"createdAt"`
	UpdatedAt    time.Time      `json:"updatedAt"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
	ProviderID   uint           `gorm:"not null;index" json:"providerId"`                          // Provider ID
	TaskType     string         `gorm:"type:varchar(50);not null" json:"taskType"`                 // 任务类型: enable_all, disable_all, detect_all
	Status       string         `gorm:"type:varchar(20);not null;default:'pending'" json:"status"` // 状态: pending, running, completed, failed
	Progress     int            `gorm:"default:0" json:"progress"`                                 // 进度 0-100
	Message      string         `gorm:"type:text" json:"message"`                                  // 当前状态消息
	StartedAt    *time.Time     `json:"startedAt,omitempty"`                                       // 任务开始时间
	CompletedAt  *time.Time     `json:"completedAt,omitempty"`                                     // 任务完成时间
	TotalCount   int            `gorm:"default:0" json:"totalCount"`                               // 总实例数
	SuccessCount int            `gorm:"default:0" json:"successCount"`                             // 成功数量
	FailedCount  int            `gorm:"default:0" json:"failedCount"`                              // 失败数量
	Output       string         `gorm:"type:longtext" json:"output"`                               // 详细输出日志
	ErrorMsg     string         `gorm:"type:text" json:"errorMsg,omitempty"`                       // 错误信息
}

// TableName 指定表名
func (TrafficMonitorTask) TableName() string {
	return "traffic_monitor_tasks"
}

// TrafficMonitorTaskListRequest 任务列表查询请求
type TrafficMonitorTaskListRequest struct {
	Page       int    `form:"page" binding:"omitempty,min=1"`
	PageSize   int    `form:"pageSize" binding:"omitempty,min=1,max=100"`
	ProviderID uint   `form:"providerId"`
	TaskType   string `form:"taskType"`
	Status     string `form:"status"`
}

// TrafficMonitorOperationRequest 流量监控操作请求
type TrafficMonitorOperationRequest struct {
	ProviderID uint   `json:"providerId" binding:"required"`
	Operation  string `json:"operation" binding:"required,oneof=enable disable detect"` // enable: 批量启用, disable: 批量删除, detect: 批量检测
}
