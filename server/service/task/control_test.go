package task

import (
	"testing"

	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
)

func TestCancelledOperationRestoreAlsoRestoresDesiredState(t *testing.T) {
	tests := []struct {
		name         string
		task         adminModel.Task
		status       string
		desiredState string
	}{
		{name: "user start", task: adminModel.Task{TaskType: "start"}, status: "stopped", desiredState: providerModel.InstanceDesiredStateStopped},
		{name: "recovery start", task: adminModel.Task{TaskType: "start", TaskData: `{"recovery":true}`}, status: "stopped", desiredState: providerModel.InstanceDesiredStateRunning},
		{name: "traffic recovery start", task: adminModel.Task{TaskType: "start", TaskData: `{"desiredState":"running"}`}, status: "stopped", desiredState: providerModel.InstanceDesiredStateRunning},
		{name: "legacy traffic recovery start", task: adminModel.Task{TaskType: "start", StatusMessage: "流量限制已解除，自动恢复因流量策略停机的实例"}, status: "stopped", desiredState: providerModel.InstanceDesiredStateRunning},
		{name: "legacy check-in recovery start", task: adminModel.Task{TaskType: "start", StatusMessage: "签到续期后自动启动实例"}, status: "stopped", desiredState: providerModel.InstanceDesiredStateRunning},
		{name: "stop", task: adminModel.Task{TaskType: "stop"}, status: "running", desiredState: providerModel.InstanceDesiredStateRunning},
		{name: "restart", task: adminModel.Task{TaskType: "restart"}, status: "running", desiredState: providerModel.InstanceDesiredStateRunning},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, desiredState, ok := cancelledOperationRestore(test.task)
			if !ok || status != test.status || desiredState != test.desiredState {
				t.Fatalf("cancelledOperationRestore(%q) = (%q, %q, %v), want (%q, %q, true)",
					test.name, status, desiredState, ok, test.status, test.desiredState)
			}
			if got := cancelledOperationTransitionStatus(test.task.TaskType); got == "" {
				t.Fatalf("cancelledOperationTransitionStatus(%q) returned empty status", test.name)
			}
		})
	}
	if _, _, ok := cancelledOperationRestore(adminModel.Task{TaskType: "delete"}); ok {
		t.Fatal("delete should not restore an operation lifecycle state")
	}
}
