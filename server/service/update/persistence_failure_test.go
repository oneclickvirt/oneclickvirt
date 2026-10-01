package update

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBeginOperationRejectsUnwritableStateBeforeScheduling(t *testing.T) {
	cfg := updateStateTestConfig(t)
	statePath := filepath.Join(cfg.InstallRoot, ".oneclickvirt-update", "state.json")
	if err := os.MkdirAll(statePath, 0750); err != nil {
		t.Fatal(err)
	}
	svc := &Service{state: OperationState{Status: OperationIdle}, now: time.Now}
	if _, created, err := svc.beginOperation("restart", "", "", "retry-key", "fingerprint"); err == nil || created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if svc.Operation().Status != OperationIdle {
		t.Fatal("failed submission locked the updater")
	}
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	if _, created, err := svc.beginOperation("restart", "", "", "retry-key", "fingerprint"); err != nil || !created {
		t.Fatalf("retry created=%v err=%v", created, err)
	}
}

func TestUpdatePersistenceFailureCannotBeOverwrittenByStaleDisk(t *testing.T) {
	cfg := updateStateTestConfig(t)
	svc := &Service{state: OperationState{Status: OperationIdle}, now: time.Now}
	first, _, err := svc.beginOperation("update", "v20260930-010101", "", "key", "fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(cfg.InstallRoot, ".oneclickvirt-update", "state.json")
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(statePath, 0750); err != nil {
		t.Fatal(err)
	}
	if err := svc.updateOperation(first.ID, OperationApplying, "apply", nil); err == nil {
		t.Fatal("expected persistence error")
	}
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	if err := writeOperationState(cfg, first); err != nil {
		t.Fatal(err)
	}
	if got := svc.Operation(); got.Status != OperationFailed || got.Error == "" {
		t.Fatalf("stale disk hid failure: %+v", got)
	}
}

func TestUpdateStateRejectsLateWorkerAndOldRevision(t *testing.T) {
	cfg := updateStateTestConfig(t)
	old := OperationState{ID: "old", Status: OperationApplying, StartedAt: time.Now().Add(-time.Minute), Revision: 2}
	if err := writeOperationState(cfg, old); err != nil {
		t.Fatal(err)
	}
	newer := old
	newer.ID, newer.StartedAt, newer.Revision = "new", time.Now(), 1
	if err := writeOperationState(cfg, newer); err != nil {
		t.Fatal(err)
	}
	old.Status, old.Revision = OperationSucceeded, 3
	if err := writeOperationState(cfg, old); err == nil {
		t.Fatal("late worker overwrote new operation")
	}
	completed := newer
	completed.Status, completed.Revision = OperationSucceeded, 2
	if err := writeOperationState(cfg, completed); err != nil {
		t.Fatal(err)
	}
	if err := writeOperationState(cfg, newer); err == nil {
		t.Fatal("stale writer regressed completed operation")
	}
	if shouldAdoptPersistedOperation(completed, newer) {
		t.Fatal("stale reader regressed completed operation")
	}
}
