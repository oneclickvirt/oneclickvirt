package incus

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

type recordingIncusIPv6Executor struct {
	commands []string
	outputs  []string
	errors   []error
}

func (e *recordingIncusIPv6Executor) Execute(command string) (string, error) {
	e.commands = append(e.commands, command)
	index := len(e.commands) - 1
	var output string
	var err error
	if index < len(e.outputs) {
		output = e.outputs[index]
	}
	if index < len(e.errors) {
		err = e.errors[index]
	}
	return output, err
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

func TestIncusNetworkDeviceIPv6ErrorIncludesProbeDetails(t *testing.T) {
	if global.APP_LOG == nil {
		global.APP_LOG = zap.NewNop()
	}
	executor := &recordingIncusIPv6Executor{outputs: []string{
		"2606:4700::1111",
		"not_found",
		"eth0",
		"probe diagnostic\nnot-an-ipv6",
	}}
	incusProvider := &IncusProvider{sshClient: utils.NewSafeShellExecutor(executor)}

	_, err := incusProvider.setupNetworkDeviceIPv6(context.Background(), IPv6Config{ContainerName: "guest"})
	if err == nil {
		t.Fatal("setupNetworkDeviceIPv6() succeeded without a host IPv6 CIDR")
	}
	for _, fragment := range []string{
		"interface=eth0",
		"command=ip -6 addr show dev 'eth0'",
		"output=probe diagnostic\\nnot-an-ipv6",
	} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("setupNetworkDeviceIPv6() error %q missing %q", err, fragment)
		}
	}
}

func TestIncusStaticIPv6SkipsMissingHostCIDRAndParsesNoisyTunnelProbe(t *testing.T) {
	if global.APP_LOG == nil {
		global.APP_LOG = zap.NewNop()
	}
	executor := &recordingIncusIPv6Executor{
		outputs: []string{
			"2606:4700::1111",
			"warning: transport diagnostic\nfound",
			"",
			"sysctl failed",
		},
		errors: []error{nil, nil, nil, errors.New("stop after sysctl")},
	}
	incusProvider := &IncusProvider{sshClient: utils.NewSafeShellExecutor(executor)}

	_, err := incusProvider.setupNetworkDeviceIPv6(context.Background(), IPv6Config{
		ContainerName: "guest",
		ContainerIPv6: "2606:4700::1234",
	})
	if err == nil || !strings.Contains(err.Error(), "配置IPv6 sysctl失败") {
		t.Fatalf("setupNetworkDeviceIPv6() error = %v, want post-probe sysctl failure", err)
	}
	if strings.Contains(err.Error(), "无法获取本地IPv6网络配置") {
		t.Fatalf("static IPv6 still required a host CIDR: %v", err)
	}
	if len(executor.commands) < 3 || !strings.Contains(executor.commands[2], "he-ipv6") {
		t.Fatalf("noisy tunnel probe did not select he-ipv6: %#v", executor.commands)
	}
}

func TestIncusRoutedIPv6ChecksManagedBridgeAndProtectsExistingEth1(t *testing.T) {
	executor := &recordingIncusIPv6Executor{
		errors:  []error{nil, nil, errors.New("existing device is unrelated")},
		outputs: []string{"", "", "existing eth1"},
	}
	incusProvider := &IncusProvider{sshClient: utils.NewSafeShellExecutor(executor)}
	_, err := incusProvider.setupRoutedNetworkDeviceIPv6(IPv6Config{
		ContainerName: "guest", ContainerIPv6: "2001:db8::2",
		RoutedCIDR: "2001:db8::/126", RoutedGateway: "2001:db8::1", RoutedBridge: "oneclickvirt6", RoutedTunnelInterface: "he-ipv6",
	})
	if err == nil || !strings.Contains(err.Error(), "existing device is unrelated") {
		t.Fatalf("setupRoutedNetworkDeviceIPv6() error = %v", err)
	}
	if len(executor.commands) != 3 {
		t.Fatalf("commands = %#v, want host check, stop, and guarded device command", executor.commands)
	}
	for _, fragment := range []string{"ip -d link show dev 'oneclickvirt6'", "routed IPv6 bridge gateway is missing", "net.ipv6.conf.he-ipv6.forwarding", "net.ipv6.conf.oneclickvirt6.forwarding"} {
		if !strings.Contains(executor.commands[0], fragment) {
			t.Fatalf("host check missing %q: %s", fragment, executor.commands[0])
		}
	}
	for _, fragment := range []string{"existing_type=", "refusing to replace existing eth1", "ipv6.gateway true", "ipv6.gateway=true"} {
		if !strings.Contains(executor.commands[2], fragment) {
			t.Fatalf("device command missing %q: %s", fragment, executor.commands[2])
		}
	}
	if strings.Contains(executor.commands[2], "eth1 nictype routed") || strings.Contains(executor.commands[2], "eth1 parent 'oneclickvirt6'") {
		t.Fatalf("device command overwrites an existing eth1: %s", executor.commands[2])
	}
}
