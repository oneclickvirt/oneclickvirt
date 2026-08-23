package update

import (
	"testing"
)

func TestSafeArchivePath(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "./../escape", "dir/../../escape", ""} {
		if _, err := safeArchivePath(name); err == nil {
			t.Fatalf("unsafe archive path accepted: %q", name)
		}
	}
	if got, err := safeArchivePath("dist/assets/app.js"); err != nil || got == "" {
		t.Fatalf("safe archive path rejected: %q, %v", got, err)
	}
}
