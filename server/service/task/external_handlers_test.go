package task

import (
	"context"
	"strings"
	"testing"

	adminModel "oneclickvirt/model/admin"
)

func TestExternalTaskHandlerDispatch(t *testing.T) {
	const taskType = "test-external-dispatch"
	called := false
	RegisterExternalTaskHandler(taskType, func(_ context.Context, task *adminModel.Task) error {
		called = task.TaskType == taskType
		return nil
	})
	if err := executeExternalTaskHandler(context.Background(), &adminModel.Task{TaskType: taskType}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("registered external task handler was not called")
	}
}

func TestExternalTaskHandlerRejectsUnknownType(t *testing.T) {
	err := executeExternalTaskHandler(context.Background(), &adminModel.Task{TaskType: "missing-external-handler"})
	if err == nil || !strings.Contains(err.Error(), "未知的任务类型") {
		t.Fatalf("unexpected error: %v", err)
	}
}
