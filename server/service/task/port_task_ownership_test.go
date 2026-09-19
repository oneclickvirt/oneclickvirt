package task

import (
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	"testing"
	"time"
)

func TestResetPortReservationRejectsOldQueuedTasks(t *testing.T) {
	now := time.Now()
	port := providerModel.Port{ID: 7, InstanceID: 11, ProviderID: 3, Status: "active", UpdatedAt: now}
	task := &adminModel.Task{CreatedAt: now.Add(-time.Minute)}
	if err := validatePortTaskOwnership(task, port, 10, 3); err == nil {
		t.Fatal("old guest task can mutate replacement reservation")
	}
	if err := validatePortTaskOwnership(task, port, 11, 4); err == nil {
		t.Fatal("wrong provider accepted")
	}
	if err := validatePortTaskOwnership(task, port, 0, 0); err == nil {
		t.Fatal("ambiguous legacy task accepted after migration")
	}
	if err := validatePortTaskOwnership(task, port, 11, 3); err != nil {
		t.Fatal(err)
	}
	port.Status = "restoring"
	if err := validatePortTaskOwnership(task, port, 11, 3); err == nil {
		t.Fatal("unfinished restore is mutable")
	}
}
