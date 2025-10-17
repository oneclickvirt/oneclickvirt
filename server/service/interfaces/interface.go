package interfaces

import adminModel "oneclickvirt/model/admin"

// TaskServiceInterface 任务服务接口，用于避免循环依赖
type TaskServiceInterface interface {
	CreateTask(userID uint, providerID *uint, instanceID *uint, taskType string, taskData string, timeoutDuration int) (*adminModel.Task, error)

	// 状态管理器访问方法
	GetStateManager() TaskStateManagerInterface
}
