package incus

import (
	"strings"
	"testing"

	"oneclickvirt/global"
	"oneclickvirt/provider"

	"go.uber.org/zap"
)

func TestIncusExecReadyTimeout(t *testing.T) {
	if got := incusExecReadyTimeout("vm"); got != 1800 {
		t.Fatalf("vm timeout = %d, want 1800", got)
	}
	if got := incusExecReadyTimeout("container"); got != 30 {
		t.Fatalf("container timeout = %d, want 30", got)
	}
}

func TestIncusStoragePoolArg(t *testing.T) {
	if got := incusStoragePoolArg(""); got != "default" {
		t.Fatalf("empty pool storage arg = %q", got)
	}
	if got := incusStoragePoolArg("local"); got != "local" {
		t.Fatalf("custom pool storage arg = %q", got)
	}
}

func TestIncusBuildCreateCommandIncludesStoragePool(t *testing.T) {
	global.APP_LOG = zap.NewNop()

	incusProvider := &IncusProvider{}

	cmd, err := incusProvider.buildCreateCommand(provider.InstanceConfig{
		Name:         "ct1",
		Image:        "oneclickvirt_debian",
		InstanceType: "container",
	})
	if err != nil {
		t.Fatalf("buildCreateCommand returned error: %v", err)
	}
	if !strings.Contains(cmd, "-s 'default'") {
		t.Fatalf("container create command missing storage pool: %s", cmd)
	}

	incusProvider.config.StoragePool = "local"
	vmCmd, err := incusProvider.buildCreateCommand(provider.InstanceConfig{
		Name:         "vm1",
		Image:        "oneclickvirt_debian_vm",
		InstanceType: "vm",
	})
	if err != nil {
		t.Fatalf("buildCreateCommand returned error: %v", err)
	}
	if !strings.Contains(vmCmd, "--vm") || !strings.Contains(vmCmd, "-s 'local'") {
		t.Fatalf("vm create command missing vm/storage options: %s", vmCmd)
	}
}
