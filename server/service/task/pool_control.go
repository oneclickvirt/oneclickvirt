package task

import (
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/service/taskgate"

	"gorm.io/gorm"
)

func (s *TaskService) IsTaskPoolEnabled() bool {
	return taskgate.IsEnabled()
}

func (s *TaskService) EnsureTaskPoolAccepting() error {
	return taskgate.EnsureAccepting()
}

func (s *TaskService) EnsureTaskPoolAcceptingInTx(tx *gorm.DB) error {
	return taskgate.EnsureAcceptingInTx(tx)
}

func (s *TaskService) GetTaskPoolStatus() (*adminModel.TaskPoolStatusResponse, error) {
	return taskgate.GetStatus()
}

func (s *TaskService) SetTaskPoolEnabled(enabled bool, message string) (*adminModel.TaskPoolStatusResponse, error) {
	return taskgate.SetEnabled(enabled, message)
}
