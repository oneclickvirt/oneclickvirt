package qemu

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

func TestQEMUVMLogRemovalKeepsEmptyAndExactNames(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses GNU file tools on provider nodes")
	}
	log := filepath.Join(t.TempDir(), "vmlog")
	run := func(command string) {
		t.Helper()
		if output, err := exec.Command("sh", "-c", command).CombinedOutput(); err != nil {
			t.Fatalf("vmlog command: %v: %s", err, output)
		}
	}
	run(qemuRemoveVMLogCommand(log, "vm.7"))
	run(qemuAppendVMLogCommand(log, "vm.7 2001"))
	run(qemuAppendVMLogCommand(log, "vm17 2002"))
	run(qemuRemoveVMLogCommand(log, "vm.7"))
	remaining, err := os.ReadFile(log)
	if err != nil || string(remaining) != "vm17 2002\n" {
		t.Fatalf("unexpected remaining entries: %q, %v", remaining, err)
	}
	run(qemuRemoveVMLogCommand(log, "vm17"))
	remaining, err = os.ReadFile(log)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("last entry was not removed: %q, %v", remaining, err)
	}
}
