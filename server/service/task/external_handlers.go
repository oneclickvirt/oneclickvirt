package task

import (
	"context"
	"fmt"
	"sync"

	adminModel "oneclickvirt/model/admin"
)

// ExternalTaskHandler lets higher-level services register long-running task
// executors without introducing an import cycle back into the task package.
type ExternalTaskHandler func(context.Context, *adminModel.Task) error

var externalTaskHandlers = struct {
	sync.RWMutex
	values map[string]ExternalTaskHandler
}{values: make(map[string]ExternalTaskHandler)}

func RegisterExternalTaskHandler(taskType string, handler ExternalTaskHandler) {
	if taskType == "" || handler == nil {
		panic("task: invalid external task handler registration")
	}
	externalTaskHandlers.Lock()
	defer externalTaskHandlers.Unlock()
	if _, exists := externalTaskHandlers.values[taskType]; exists {
		panic("task: duplicate external task handler: " + taskType)
	}
	externalTaskHandlers.values[taskType] = handler
}

func executeExternalTaskHandler(ctx context.Context, task *adminModel.Task) error {
	externalTaskHandlers.RLock()
	handler := externalTaskHandlers.values[task.TaskType]
	externalTaskHandlers.RUnlock()
	if handler == nil {
		return fmt.Errorf("未知的任务类型: %s", task.TaskType)
	}
	return handler(ctx, task)
}
