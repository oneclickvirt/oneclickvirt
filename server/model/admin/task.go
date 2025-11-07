package admin

import (
	"time"
)

// AdminTaskListRequest 管理员任务列表请求
type AdminTaskListRequest struct {
	Page         int    `json:"page" form:"page"`
	PageSize     int    `json:"pageSize" form:"pageSize"`
	ProviderID   uint   `json:"providerId" form:"providerId"`
	Username     string `json:"username" form:"username"` // 用户名搜索
	TaskType     string `json:"taskType" form:"taskType"`
	Status       string `json:"status" form:"status"`
	InstanceType string `json:"instanceType" form:"instanceType"` // container or vm
}

// AdminTaskResponse 管理员任务响应
type AdminTaskResponse struct {
	ID               uint       `json:"id"`
	UUID             string     `json:"uuid"`
	TaskType         string     `json:"taskType"`
	Status           string     `json:"status"`
	Progress         int        `json:"progress"`
	ErrorMessage     string     `json:"errorMessage"`
	CancelReason     string     `json:"cancelReason"` // 取消原因
	CreatedAt        time.Time  `json:"createdAt"`
	StartedAt        *time.Time `json:"startedAt"`
	CompletedAt      *time.Time `json:"completedAt"`
	TimeoutDuration  int        `json:"timeoutDuration"`
	StatusMessage    string     `json:"statusMessage"`
	UserID           uint       `json:"userId"`
	UserName         string     `json:"userName"`
	ProviderID       *uint      `json:"providerId"`
	ProviderName     string     `json:"providerName"`
	InstanceID       *uint      `json:"instanceId"`
	InstanceName     string     `json:"instanceName"`
	InstanceType     string     `json:"instanceType"`
	CanForceStop     bool       `json:"canForceStop"`
	IsForceStoppable bool       `json:"isForceStoppable"`
	RemainingTime    int        `json:"remainingTime"` // 剩余时间（秒）
	// 预分配的实例配置信息
	PreallocatedCPU       int `json:"preallocatedCpu"`       // 预分配的CPU核心数
	PreallocatedMemory    int `json:"preallocatedMemory"`    // 预分配的内存(MB)
	PreallocatedDisk      int `json:"preallocatedDisk"`      // 预分配的磁盘(MB)
	PreallocatedBandwidth int `json:"preallocatedBandwidth"` // 预分配的带宽(Mbps)
}

// AdminTaskListResponse 管理员任务列表响应
type AdminTaskListResponse struct {
	List     []AdminTaskResponse `json:"list"`
	Total    int64               `json:"total"`
	Page     int                 `json:"page"`
	PageSize int                 `json:"pageSize"`
}

// ForceStopTaskRequest 强制停止任务请求
type ForceStopTaskRequest struct {
	TaskID uint   `json:"taskId" binding:"required"`
	Reason string `json:"reason"` // 强制停止原因
}

// TaskStatsResponse 任务统计响应
type TaskStatsResponse struct {
	TotalTasks     int64 `json:"totalTasks"`
	PendingTasks   int64 `json:"pendingTasks"`
	RunningTasks   int64 `json:"runningTasks"`
	CompletedTasks int64 `json:"completedTasks"`
	FailedTasks    int64 `json:"failedTasks"`
	TimeoutTasks   int64 `json:"timeoutTasks"`
}
