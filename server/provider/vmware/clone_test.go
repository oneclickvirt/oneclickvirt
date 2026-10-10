package vmware

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestClonePreservesExistingVMDirectory(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "template", "mother.vmx")
	dir := filepath.Join(root, "instances", "guest")
	for _, folder := range []string{filepath.Dir(src), dir} {
		if err := os.MkdirAll(folder, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(src, []byte("template"), 0600); err != nil {
		t.Fatal(err)
	}
	disk := filepath.Join(dir, "production.vmdk")
	if err := os.WriteFile(disk, []byte("existing data"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", vmwareCloneCommand(src, dir, filepath.Join(dir, "guest.vmx"), "guest"))
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("existing target accepted: %s", output)
	}
	if data, err := os.ReadFile(disk); err != nil || string(data) != "existing data" {
		t.Fatalf("existing disk changed: %q, %v", data, err)
	}
}

func TestCloneFallbackCopiesIntoNewDirectory(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "template", "mother.vmx")
	dir := filepath.Join(root, "instances", "guest")
	if err := os.MkdirAll(filepath.Dir(src), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("template"), 0600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "vmrun"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", vmwareCloneCommand(src, dir, filepath.Join(dir, "guest.vmx"), "guest"))
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("copy fallback: %v, %s", err, output)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "guest.vmx")); err != nil || string(data) != "template" {
		t.Fatalf("clone file: %q, %v", data, err)
	}
}
