package update

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func updateStateTestConfig(t *testing.T) runtimeConfig {
	t.Helper()
	root := t.TempDir()
	t.Setenv("ONECLICKVIRT_INSTALL_ROOT", root)
	t.Setenv("ONECLICKVIRT_SERVER_BIN", filepath.Join(root, "server", "oneclickvirt-server"))
	t.Setenv("ONECLICKVIRT_UPDATE_FLAVOR", FlavorAllInOne)
	t.Setenv("ONECLICKVIRT_UPDATE_MODE", ModeUnknown)
	return loadRuntimeConfig()
}

func TestOperationRefreshesStateWrittenByDetachedWorker(t *testing.T) {
	cfg := updateStateTestConfig(t)
	now := time.Date(2026, time.August, 22, 0, 0, 0, 0, time.UTC)
	active := OperationState{ID: "ocv-active", Action: "update", Status: OperationApplying, StartedAt: now.Add(-time.Minute)}
	if err := writeOperationState(cfg, active); err != nil {
		t.Fatal(err)
	}
	service := &Service{state: OperationState{Status: OperationIdle}, now: func() time.Time { return now }}
	if got := service.Operation(); got.Status != OperationApplying || got.ID != active.ID {
		t.Fatalf("active worker state after controller restart = %#v", got)
	}

	finished := now.Add(time.Minute)
	active.Status = OperationSucceeded
	active.FinishedAt = &finished
	if err := writeOperationState(cfg, active); err != nil {
		t.Fatal(err)
	}
	if got := service.Operation(); got.Status != OperationSucceeded || got.ID != active.ID {
		t.Fatalf("completed worker state was not refreshed = %#v", got)
	}
}

func TestOperationExpiresStaleWorkerState(t *testing.T) {
	cfg := updateStateTestConfig(t)
	now := time.Date(2026, time.August, 22, 0, 0, 0, 0, time.UTC)
	stale := OperationState{ID: "ocv-stale", Action: "update", Status: OperationApplying, StartedAt: now.Add(-maxOperationAge - time.Second)}
	if err := writeOperationState(cfg, stale); err != nil {
		t.Fatal(err)
	}
	service := &Service{state: OperationState{Status: OperationIdle}, now: func() time.Time { return now }}
	if got := service.Operation(); got.Status != OperationFailed || got.Error == "" || got.FinishedAt == nil {
		t.Fatalf("stale worker state = %#v", got)
	}
}

func TestSelectReleaseWillNotDowngradeImplicitLatest(t *testing.T) {
	releases := []githubRelease{{TagName: "v1.0.0"}}
	if _, ok := selectRelease(releases, "", false, "v1.1.0"); ok {
		t.Fatal("implicit latest selection accepted a downgrade")
	}
	if release, ok := selectRelease(releases, "", false, "v0.9.0"); !ok || release.TagName != "v1.0.0" {
		t.Fatalf("newer implicit latest selection = %#v, %t", release, ok)
	}
}

func TestBeginOperationReusesSameIdempotencyKeyWithoutStartingAgain(t *testing.T) {
	updateStateTestConfig(t)
	now := time.Date(2026, time.August, 22, 0, 0, 0, 0, time.UTC)
	service := &Service{state: OperationState{Status: OperationIdle}, now: func() time.Time { return now }}

	fingerprint := operationFingerprint("restart", "", "")
	first, created, err := service.beginOperation("restart", "", "", "system-retry-key", fingerprint)
	if err != nil || !created {
		t.Fatalf("first operation = %#v created=%t err=%v", first, created, err)
	}
	second, created, err := service.beginOperation("restart", "", "", "system-retry-key", fingerprint)
	if err != nil || created || second.ID != first.ID {
		t.Fatalf("same-key retry = %#v created=%t err=%v", second, created, err)
	}
	if _, _, err := service.beginOperation("restart", "", "", "different-key", fingerprint); err == nil {
		t.Fatal("different idempotency key was allowed while operation was active")
	}
}

func TestUpdateOperationByMessageUsesOperationIdentityAndRevision(t *testing.T) {
	updateStateTestConfig(t)
	now := time.Date(2026, time.August, 22, 0, 0, 0, 0, time.UTC)
	service := &Service{state: OperationState{
		ID:        "ocv-message",
		Action:    "update",
		Status:    OperationScheduled,
		Revision:  3,
		StartedAt: now,
	}, now: func() time.Time { return now }}
	if err := writeOperationState(loadRuntimeConfig(), service.state); err != nil {
		t.Fatal(err)
	}

	if err := service.updateOperationByMessage("ocv-message", "正在停止服务并原子切换文件"); err != nil {
		t.Fatalf("update operation message: %v", err)
	}
	if service.state.Status != OperationApplying || service.state.Revision != 4 || service.state.Message == "" {
		t.Fatalf("message transition = %#v", service.state)
	}

	before := cloneOperation(service.state)
	if err := service.updateOperationByMessage("ocv-stale", "late worker"); err == nil {
		t.Fatal("stale worker was allowed to update operation")
	}
	if got := service.Operation(); got.ID != before.ID || got.Revision != before.Revision || got.Message != before.Message {
		t.Fatalf("stale worker changed operation = %#v, before %#v", got, before)
	}
}

func TestUpdateOperationTargetAdvancesRevisionAndRejectsTerminalState(t *testing.T) {
	updateStateTestConfig(t)
	now := time.Date(2026, time.August, 22, 0, 0, 0, 0, time.UTC)
	service := &Service{state: OperationState{
		ID:        "ocv-target",
		Action:    "update",
		Status:    OperationStaging,
		Revision:  1,
		StartedAt: now,
	}, now: func() time.Time { return now }}
	if err := writeOperationState(loadRuntimeConfig(), service.state); err != nil {
		t.Fatal(err)
	}
	if err := service.updateOperationTarget("ocv-target", "v20260822-120000"); err != nil {
		t.Fatalf("update operation target: %v", err)
	}
	if service.state.Target != "v20260822-120000" || service.state.Revision != 2 {
		t.Fatalf("target transition = %#v", service.state)
	}
	service.state.Status = OperationSucceeded
	if err := service.updateOperationTarget("ocv-target", "v20260822-130000"); err == nil {
		t.Fatal("terminal operation accepted a target update")
	}
}

func TestReadInstalledVersionIgnoresUnknownMarker(t *testing.T) {
	cfg := updateStateTestConfig(t)
	if err := os.WriteFile(currentVersionFile(cfg), []byte("unknown\n"), 0640); err != nil {
		t.Fatal(err)
	}
	if got := readInstalledVersion(cfg); got != currentVersion() {
		t.Fatalf("unknown marker version = %q, want %q", got, currentVersion())
	}
	if err := os.WriteFile(currentVersionFile(cfg), []byte("v20260822-120000\n"), 0640); err != nil {
		t.Fatal(err)
	}
	if got := readInstalledVersion(cfg); got != "v20260822-120000" {
		t.Fatalf("recorded version = %q", got)
	}
}
