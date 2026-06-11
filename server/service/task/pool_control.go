package task

import (
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/service/taskgate"
)

func (s *TaskService) IsTaskPoolEnabled() bool {
	return taskgate.IsEnabled()
}

func (s *TaskService) EnsureTaskPoolAccepting() error {
	return taskgate.EnsureAccepting()
}

func (s *TaskService) GetTaskPoolStatus() (*adminModel.TaskPoolStatusResponse, error) {
	return taskgate.GetStatus()
}

func (s *TaskService) SetTaskPoolEnabled(enabled bool, message string) (*adminModel.TaskPoolStatusResponse, error) {
	return taskgate.SetEnabled(enabled, message)
}
