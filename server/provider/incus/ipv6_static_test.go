package incus

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"oneclickvirt/provider"
	"oneclickvirt/utils"
)

type recordingIncusIPv6Executor struct {
	commands []string
}

func (e *recordingIncusIPv6Executor) Execute(command string) (string, error) {
	e.commands = append(e.commands, command)
	return "", nil
}
func (e *recordingIncusIPv6Executor) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}
func (e *recordingIncusIPv6Executor) ExecuteWithLogging(command, _ string) (string, error) {
	return e.Execute(command)
}
func (e *recordingIncusIPv6Executor) ExecuteRaw(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}
func (*recordingIncusIPv6Executor) ExecuteViaTempScript(string, []string, time.Duration) (string, error) {
	return "", nil
}
func (*recordingIncusIPv6Executor) UploadContent(string, string, os.FileMode) error { return nil }
func (*recordingIncusIPv6Executor) IsHealthy() bool                                 { return true }
func (*recordingIncusIPv6Executor) Reconnect() error                                { return nil }
func (*recordingIncusIPv6Executor) Close() error                                    { return nil }

func TestIncusStaticIPv6RequiresSSHWhenAPIOnly(t *testing.T) {
	incusProvider := NewIncusProvider().(*IncusProvider)
	incusProvider.connected = true
	incusProvider.config.ExecutionRule = "api_only"
	config := provider.InstanceConfig{Metadata: map[string]string{"static_ipv6": "2001:db8::10"}}

	err := incusProvider.CreateInstance(context.Background(), config)
	if err == nil || !strings.Contains(err.Error(), "api_only") {
		t.Fatalf("CreateInstance() error = %v, want explicit api_only rejection", err)
	}
}

func TestIncusHasRequestedStaticIPv6(t *testing.T) {
	if hasRequestedStaticIPv6(provider.InstanceConfig{}) {
		t.Fatal("empty metadata reported static IPv6")
	}
	if !hasRequestedStaticIPv6(provider.InstanceConfig{Metadata: map[string]string{"static_ipv6": " 2001:db8::1 "}}) {
		t.Fatal("static IPv6 metadata was not detected")
	}
}

func TestIncusConfigureIPv6SysctlsUsesDedicatedGuardedFile(t *testing.T) {
	executor := &recordingIncusIPv6Executor{}
	incusProvider := &IncusProvider{sshClient: utils.NewSafeShellExecutor(executor)}
	if err := incusProvider.configureIPv6Sysctls("eth0"); err != nil {
		t.Fatalf("configureIPv6Sysctls() error = %v", err)
	}
	if len(executor.commands) != 1 {
		t.Fatalf("commands = %#v, want one atomic script", executor.commands)
	}
	command := executor.commands[0]
	if strings.Contains(command, "/etc/sysctl.conf") {
		t.Fatalf("command mutates /etc/sysctl.conf: %s", command)
	}
	for _, fragment := range []string{"/etc/sysctl.d/99-oneclickvirt-ipv6.conf", "/proc/sys/net/ipv6/conf/", "proxy_ndp"} {
		if !strings.Contains(command, fragment) {
			t.Fatalf("command missing %q: %s", fragment, command)
		}
	}
}
