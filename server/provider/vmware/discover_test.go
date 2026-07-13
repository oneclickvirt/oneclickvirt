package vmware

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

type vmwareDiscoveryExecutor struct{}

func (vmwareDiscoveryExecutor) output(command string) string {
	if strings.Contains(command, "vmrun list") {
		return "Total running VMs: 1\n/var/lib/oneclickvirt/vmware/existing/existing.vmx\n"
	}
	if strings.Contains(command, "find") {
		return "/var/lib/oneclickvirt/vmware/existing/existing.vmx\n"
	}
	return ""
}
func (e vmwareDiscoveryExecutor) Execute(command string) (string, error) {
	return e.output(command), nil
}
func (e vmwareDiscoveryExecutor) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	return e.output(command), nil
}
func (e vmwareDiscoveryExecutor) ExecuteWithLogging(command, _ string) (string, error) {
	return e.output(command), nil
}
func (e vmwareDiscoveryExecutor) ExecuteRaw(command string, _ time.Duration) (string, error) {
	return e.output(command), nil
}
func (e vmwareDiscoveryExecutor) ExecuteViaTempScript(string, []string, time.Duration) (string, error) {
	return "", nil
}
func (e vmwareDiscoveryExecutor) UploadContent(string, string, os.FileMode) error { return nil }
func (e vmwareDiscoveryExecutor) IsHealthy() bool                                 { return true }
func (e vmwareDiscoveryExecutor) Reconnect() error                                { return nil }
func (e vmwareDiscoveryExecutor) Close() error                                    { return nil }

func TestDiscoverInstancesCarriesVMXPathAsRemoteID(t *testing.T) {
	p := &VMwareProvider{executor: vmwareDiscoveryExecutor{}, connected: true}
	instances, err := p.DiscoverInstances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := "/var/lib/oneclickvirt/vmware/existing/existing.vmx"
	if len(instances) != 1 || instances[0].ProviderInstanceID != want || instances[0].Status != "running" {
		t.Fatalf("unexpected discovery: %#v", instances)
	}
}
