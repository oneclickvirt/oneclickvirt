package snapshot

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnassignedSnapshotQuota(t *testing.T) {
	if quota, err := maxSnapshotsForUserInTx(nil, 0); err != nil || quota != 3 {
		t.Fatalf("unassigned snapshot quota: %d, %v", quota, err)
	}
}

func TestIncusSnapshotCommandSelectsDialectBeforeMutation(t *testing.T) {
	bin := t.TempDir()
	incus := filepath.Join(bin, "incus")
	script := "#!/bin/sh\n" + `if [ "$1 $2" = 'snapshot --help' ]; then
  if [ "$INCUS_STYLE" = modern ]; then printf '  create\tCreate\n  restore\tRestore\n  delete\tDelete\n'; fi
  exit 0
fi
printf '%s\n' "$*"`
	if err := os.WriteFile(incus, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, tc := range []struct {
		action, modern, legacy string
	}{
		{"create", "snapshot create example stable", "snapshot example stable"},
		{"restore", "snapshot restore example stable", "restore example stable"},
		{"delete", "snapshot delete example stable", "delete example/stable"},
	} {
		for _, style := range []string{"modern", "legacy"} {
			t.Run(style+"/"+tc.action, func(t *testing.T) {
				t.Setenv("INCUS_STYLE", style)
				output, err := exec.Command("sh", "-c", incusSnapshotCommand(tc.action, "example", "stable")).CombinedOutput()
				expected := tc.legacy
				if style == "modern" {
					expected = tc.modern
				}
				if err != nil || strings.TrimSpace(string(output)) != expected {
					t.Fatalf("wanted %q; got %q (%v)", expected, output, err)
				}
			})
		}
	}
}
