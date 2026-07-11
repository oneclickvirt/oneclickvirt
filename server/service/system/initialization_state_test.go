package system

import (
	"errors"
	"testing"
)

func TestIsDatabaseInitializedRejectsMissingDatabase(t *testing.T) {
	initialized, err := IsDatabaseInitialized(nil)
	if initialized {
		t.Fatal("nil database unexpectedly reported initialized")
	}
	if !errors.Is(err, ErrDatabaseUnavailable) {
		t.Fatalf("error = %v, want ErrDatabaseUnavailable", err)
	}
}

func TestSystemInitializedMarkerLifecycle(t *testing.T) {
	t.Chdir(t.TempDir())
	if HasSystemInitializedMarker() {
		t.Fatal("marker unexpectedly exists")
	}
	if err := EnsureSystemInitializedMarker(); err != nil {
		t.Fatalf("ensure marker: %v", err)
	}
	if !HasSystemInitializedMarker() {
		t.Fatal("marker was not created")
	}
	if err := RemoveSystemInitializedMarker(); err != nil {
		t.Fatalf("remove marker: %v", err)
	}
	if HasSystemInitializedMarker() {
		t.Fatal("marker still exists after removal")
	}
}
