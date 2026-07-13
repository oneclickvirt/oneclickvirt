package vmcli

import (
	"context"
	"os"
	"testing"
	"time"
)

type vmcliDiscoveryExecutor struct{ output string }

func (e vmcliDiscoveryExecutor) Execute(string) (string, error) { return e.output, nil }
func (e vmcliDiscoveryExecutor) ExecuteWithTimeout(string, time.Duration) (string, error) {
	return e.output, nil
}
func (e vmcliDiscoveryExecutor) ExecuteWithLogging(string, string) (string, error) {
	return e.output, nil
}
func (e vmcliDiscoveryExecutor) ExecuteRaw(string, time.Duration) (string, error) {
	return e.output, nil
}
func (e vmcliDiscoveryExecutor) ExecuteViaTempScript(string, []string, time.Duration) (string, error) {
	return "", nil
}
func (e vmcliDiscoveryExecutor) UploadContent(string, string, os.FileMode) error { return nil }
func (e vmcliDiscoveryExecutor) IsHealthy() bool                                 { return true }
func (e vmcliDiscoveryExecutor) Reconnect() error                                { return nil }
func (e vmcliDiscoveryExecutor) Close() error                                    { return nil }

func TestDiscoverInstancesCarriesActionableRemoteID(t *testing.T) {
	p := &Provider{
		spec:      VirtualBoxSpec(),
		executor:  vmcliDiscoveryExecutor{output: "remote-uuid\texisting-vm\trunning\tvirtualbox\n"},
		connected: true,
	}
	instances, err := p.DiscoverInstances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 || instances[0].UUID != "remote-uuid" || instances[0].ProviderInstanceID != "remote-uuid" {
		t.Fatalf("unexpected discovery: %#v", instances)
	}
}
