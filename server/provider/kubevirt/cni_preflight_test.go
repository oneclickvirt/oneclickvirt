package kubevirt

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCNIPluginPreflightAcceptsExecutableSymlinksAndRejectsIncompleteInstall(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "k3s CNI's plugins")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "cni-binary")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, plugin := range []string{"bridge", "static"} {
		if err := os.Symlink(binary, filepath.Join(directory, plugin)); err != nil {
			t.Fatal(err)
		}
	}
	run := func() ([]byte, error) {
		return exec.Command("sh", "-c", "set -eu\n"+kubeVirtCNIPluginPreflightCommand(filepath.Join(root, "missing"), directory)).CombinedOutput()
	}
	if output, err := run(); err != nil {
		t.Fatalf("valid K3s-style symlinks rejected: %v: %s", err, output)
	}
	if err := os.Remove(filepath.Join(directory, "static")); err != nil {
		t.Fatal(err)
	}
	if output, err := run(); err == nil || !strings.Contains(string(output), "static CNI plugin is unavailable") {
		t.Fatal("missing static IPAM must fail before importing images")
	}
	if err := os.Mkdir(filepath.Join(directory, "static"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := run(); err == nil {
		t.Fatal("a searchable directory is not an executable CNI plugin")
	}
}

func TestCNIPluginPreflightIncludesK3sDataDirectories(t *testing.T) {
	command := kubeVirtCNIPluginPreflightCommand()
	for _, directory := range []string{"/var/lib/rancher/k3s/data/cni", "/var/lib/rancher/k3s/data/current/bin"} {
		if !strings.Contains(command, directory) {
			t.Fatalf("K3s plugin directory missing: %s", directory)
		}
	}
}
