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
