package instance

import (
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/service/task"
)

// Service 处理用户实例相关功能
type Service struct{}

// NewService 创建实例服务
func NewService() *Service {
	return &Service{}
}

// getTaskService 获取外部服务的辅助函数
func getTaskService() interface {
	CreateTask(userID uint, providerID *uint, instanceID *uint, taskType string, taskData string, timeout int) (*adminModel.Task, error)
} {
	// 返回真实的 TaskService
	return task.GetTaskService()
}
