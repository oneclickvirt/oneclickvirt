package qemu

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

type qemuDiscoveryExecutor struct{}

func (qemuDiscoveryExecutor) output(command string) string {
	switch {
	case strings.Contains(command, "qemu:///system") && strings.Contains(command, "list --all --name"):
		return "existing-vm\n"
	case strings.Contains(command, " dominfo "):
		return "Id: -\nName: existing-vm\nUUID: 11111111-2222-3333-4444-555555555555\nOS Type: hvm\nState: shut off\nCPU(s): 2\nMax memory: 1048576 KiB\n"
	case strings.Contains(command, "domblkinfo"):
		return "10737418240\n"
	default:
		return ""
	}
}
func (e qemuDiscoveryExecutor) Execute(command string) (string, error) { return e.output(command), nil }
func (e qemuDiscoveryExecutor) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	return e.output(command), nil
}
func (e qemuDiscoveryExecutor) ExecuteWithLogging(command, _ string) (string, error) {
	return e.output(command), nil
}
func (e qemuDiscoveryExecutor) ExecuteRaw(command string, _ time.Duration) (string, error) {
	return e.output(command), nil
}
func (e qemuDiscoveryExecutor) ExecuteViaTempScript(string, []string, time.Duration) (string, error) {
	return "", nil
}
func (e qemuDiscoveryExecutor) UploadContent(string, string, os.FileMode) error { return nil }
func (e qemuDiscoveryExecutor) IsHealthy() bool                                 { return true }
func (e qemuDiscoveryExecutor) Reconnect() error                                { return nil }
func (e qemuDiscoveryExecutor) Close() error                                    { return nil }

func TestDiscoverDisklessOrStoppedVMKeepsLibvirtUUID(t *testing.T) {
	global.APP_LOG = zap.NewNop()
	p := NewQEMUProvider().(*QEMUProvider)
	p.connected = true
	p.sshClient.SetExecutor(qemuDiscoveryExecutor{})
	instances, err := p.DiscoverInstances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantID := "11111111-2222-3333-4444-555555555555"
	if len(instances) != 1 || instances[0].ProviderInstanceID != wantID || instances[0].Status != "stopped" {
		t.Fatalf("unexpected discovery: %#v", instances)
	}
	if instances[0].OSType != "" {
		t.Fatalf("libvirt machine type must not be reported as guest OS: %q", instances[0].OSType)
	}
}
