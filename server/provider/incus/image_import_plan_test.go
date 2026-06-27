package incus

import (
	"os"
	"strings"
	"testing"
	"time"

	"oneclickvirt/utils"
)

type fakeIncusImageImportExecutor struct{}

func (fakeIncusImageImportExecutor) Execute(command string) (string, error) {
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

func (e fakeIncusImageImportExecutor) ExecuteWithTimeout(command string, timeout time.Duration) (string, error) {
	return e.Execute(command)
}

func (e fakeIncusImageImportExecutor) ExecuteWithLogging(command string, logPrefix string) (string, error) {
	return e.Execute(command)
}

func (e fakeIncusImageImportExecutor) ExecuteRaw(command string, timeout time.Duration) (string, error) {
	return e.Execute(command)
}

func (fakeIncusImageImportExecutor) ExecuteViaTempScript(scriptContent string, args []string, timeout time.Duration) (string, error) {
	return "", nil
}

func (fakeIncusImageImportExecutor) UploadContent(content, remotePath string, perm os.FileMode) error {
	return nil
}

func (fakeIncusImageImportExecutor) IsHealthy() bool  { return true }
func (fakeIncusImageImportExecutor) Reconnect() error { return nil }
func (fakeIncusImageImportExecutor) Close() error     { return nil }

func TestBuildImageImportPlanSplitVMDoesNotUseVMFlag(t *testing.T) {
	provider := &IncusProvider{sshClient: utils.NewSafeShellExecutor(fakeIncusImageImportExecutor{})}

	plan, err := provider.buildImageImportPlan("/tmp/image", "vm-alias", "vm")
	if err != nil {
		t.Fatalf("buildImageImportPlan() error = %v", err)
	}

	want := "incus image import '/tmp/image/lxd.tar.xz' '/tmp/image/disk.qcow2' --alias 'vm-alias'"
	if plan.importCmd != want {
		t.Fatalf("importCmd = %q, want %q", plan.importCmd, want)
	}
}
