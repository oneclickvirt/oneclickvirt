package lxd

import (
	"os"
	"strings"
	"testing"
	"time"

	"oneclickvirt/utils"
)

type fakeLXDImageImportExecutor struct{}

func (fakeLXDImageImportExecutor) Execute(command string) (string, error) {
	switch {
	case strings.Contains(command, "sha256sum"):
		return "abc123", nil
	case strings.Contains(command, "lxd.tar.xz"):
		return "/tmp/image/lxd.tar.xz", nil
	case strings.Contains(command, "disk.qcow2"):
		return "/tmp/image/disk.qcow2", nil
	case strings.Contains(command, "rootfs.squashfs"):
		return "/tmp/image/rootfs.squashfs", nil
	default:
		return "", nil
	}
}

func (e fakeLXDImageImportExecutor) ExecuteWithTimeout(command string, timeout time.Duration) (string, error) {
	return e.Execute(command)
}

func (e fakeLXDImageImportExecutor) ExecuteWithLogging(command string, logPrefix string) (string, error) {
	return e.Execute(command)
}

func (e fakeLXDImageImportExecutor) ExecuteRaw(command string, timeout time.Duration) (string, error) {
	return e.Execute(command)
}

func (fakeLXDImageImportExecutor) ExecuteViaTempScript(scriptContent string, args []string, timeout time.Duration) (string, error) {
	return "", nil
}

func (fakeLXDImageImportExecutor) UploadContent(content, remotePath string, perm os.FileMode) error {
	return nil
}

func (fakeLXDImageImportExecutor) IsHealthy() bool  { return true }
func (fakeLXDImageImportExecutor) Reconnect() error { return nil }
func (fakeLXDImageImportExecutor) Close() error     { return nil }

func TestBuildImageImportPlanSplitVMUsesVMFlag(t *testing.T) {
	provider := &LXDProvider{sshClient: utils.NewSafeShellExecutor(fakeLXDImageImportExecutor{})}

	plan, err := provider.buildImageImportPlan("/tmp/image", "vm-alias", "vm")
	if err != nil {
		t.Fatalf("buildImageImportPlan() error = %v", err)
	}

	want := "lxc image import '/tmp/image/lxd.tar.xz' '/tmp/image/disk.qcow2' --alias 'vm-alias' --vm"
	if plan.importCmd != want {
		t.Fatalf("importCmd = %q, want %q", plan.importCmd, want)
	}
}
