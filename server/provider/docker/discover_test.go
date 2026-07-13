package docker

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

type dockerDiscoveryExecutor struct {
	output  string
	command string
}

func (e *dockerDiscoveryExecutor) Execute(command string) (string, error) {
	e.command = command
	return e.output, nil
}
func (e *dockerDiscoveryExecutor) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}
func (e *dockerDiscoveryExecutor) ExecuteWithLogging(command, _ string) (string, error) {
	return e.Execute(command)
}
func (e *dockerDiscoveryExecutor) ExecuteRaw(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}
func (e *dockerDiscoveryExecutor) ExecuteViaTempScript(string, []string, time.Duration) (string, error) {
	return "", nil
}
func (e *dockerDiscoveryExecutor) UploadContent(string, string, os.FileMode) error { return nil }
func (e *dockerDiscoveryExecutor) IsHealthy() bool                                 { return true }
func (e *dockerDiscoveryExecutor) Reconnect() error                                { return nil }
func (e *dockerDiscoveryExecutor) Close() error                                    { return nil }

func TestDiscoverInstancesPreservesAllBindingsAndStableID(t *testing.T) {
	global.APP_LOG = zap.NewNop()
	executor := &dockerDiscoveryExecutor{output: `[{
      "Id":"abc123","Name":"/existing","State":{"Status":"running","Running":true},
      "Config":{"Image":"debian:12","Env":["OS=debian"],"Labels":{}},
      "HostConfig":{"NanoCpus":1500000000,"Memory":1073741824},
      "NetworkSettings":{"Networks":{"bridge":{"IPAddress":"172.17.0.2","MacAddress":"02:42:ac:11:00:02"}},
        "Ports":{"22/tcp":[{"HostIp":"0.0.0.0","HostPort":"2200"},{"HostIp":"127.0.0.1","HostPort":"2201"}],"80/tcp":[{"HostIp":"0.0.0.0","HostPort":"8080"}]}}
    }]`}
	p := NewDockerProvider().(*DockerProvider)
	p.connected = true
	p.sshClient.SetExecutor(executor)

	instances, err := p.DiscoverInstances(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 {
		t.Fatalf("instances = %#v", instances)
	}
	instance := instances[0]
	if instance.ProviderInstanceID != "abc123" || instance.CPU != 2 || instance.Memory != 1024 {
		t.Fatalf("unexpected identity/resources: %#v", instance)
	}
	if len(instance.PortMappings) != 3 || instance.SSHPort != 2200 {
		t.Fatalf("all port bindings were not retained: %#v", instance.PortMappings)
	}
	if strings.Contains(executor.command, "xargs -r") || !strings.Contains(executor.command, "[ -z \"$ids\" ]") {
		t.Fatalf("discovery command is not portable for empty lists: %s", executor.command)
	}
}
