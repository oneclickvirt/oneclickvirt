package lxd

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"oneclickvirt/provider"
	"oneclickvirt/utils"
)

type recordingLXDIPv6Executor struct {
	commands []string
}

func (e *recordingLXDIPv6Executor) Execute(command string) (string, error) {
	e.commands = append(e.commands, command)
	return "", nil
}
func (e *recordingLXDIPv6Executor) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}
func (e *recordingLXDIPv6Executor) ExecuteWithLogging(command, _ string) (string, error) {
	return e.Execute(command)
}
func (e *recordingLXDIPv6Executor) ExecuteRaw(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}
func (*recordingLXDIPv6Executor) ExecuteViaTempScript(string, []string, time.Duration) (string, error) {
	return "", nil
}
func (*recordingLXDIPv6Executor) UploadContent(string, string, os.FileMode) error { return nil }
func (*recordingLXDIPv6Executor) IsHealthy() bool                                 { return true }
func (*recordingLXDIPv6Executor) Reconnect() error                                { return nil }
func (*recordingLXDIPv6Executor) Close() error                                    { return nil }

func TestLXDStaticIPv6RequiresSSHWhenAPIOnly(t *testing.T) {
	lxdProvider := NewLXDProvider().(*LXDProvider)
	lxdProvider.connected = true
	lxdProvider.config.ExecutionRule = "api_only"
	config := provider.InstanceConfig{Metadata: map[string]string{"static_ipv6": "2001:db8::10"}}

	err := lxdProvider.CreateInstance(context.Background(), config)
	if err == nil || !strings.Contains(err.Error(), "api_only") {
		t.Fatalf("CreateInstance() error = %v, want explicit api_only rejection", err)
	}
}

func TestLXDHasRequestedStaticIPv6(t *testing.T) {
	if hasRequestedStaticIPv6(provider.InstanceConfig{}) {
		t.Fatal("empty metadata reported static IPv6")
	}
	if !hasRequestedStaticIPv6(provider.InstanceConfig{Metadata: map[string]string{"static_ipv6": " 2001:db8::1 "}}) {
		t.Fatal("static IPv6 metadata was not detected")
	}
}

func TestLXDConfigureIPv6SysctlsUsesDedicatedGuardedFile(t *testing.T) {
	executor := &recordingLXDIPv6Executor{}
	lxdProvider := &LXDProvider{sshClient: utils.NewSafeShellExecutor(executor)}
	if err := lxdProvider.configureIPv6Sysctls("eth0"); err != nil {
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
