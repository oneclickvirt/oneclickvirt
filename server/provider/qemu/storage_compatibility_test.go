package qemu

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestVMStoragePreservesStandaloneLogAndRepairsEmptyLegacyDirectory(t *testing.T) {
	for _, kind := range []string{"file", "empty-directory", "nonempty-directory"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			log := filepath.Join(dir, "vmlog")
			if kind == "file" {
				if err := os.WriteFile(log, []byte("existing VM record\n"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(log, 0700); err != nil {
					t.Fatal(err)
				}
				if kind == "nonempty-directory" {
					if err := os.WriteFile(filepath.Join(log, "keep"), []byte("data"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			err := exec.Command("bash", "-c", qemuPrepareVMStorageCommand(filepath.Join(dir, "images"), log)).Run()
			if kind == "nonempty-directory" {
				if err == nil {
					t.Fatal("nonempty legacy directory must be preserved")
				}
				if _, err := os.Stat(filepath.Join(log, "keep")); err != nil {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "file" && string(content) != "existing VM record\n" {
				t.Fatal("existing log changed")
			}
		})
	}
}
