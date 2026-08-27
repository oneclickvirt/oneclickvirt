package lxd

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

type recordingLXDIPv6Executor struct {
	commands []string
	outputs  []string
	errors   []error
}

func (e *recordingLXDIPv6Executor) Execute(command string) (string, error) {
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
	for _, fragment := range []string{"/etc/sysctl.d/99-oneclickvirt-ipv6.conf", "/proc/sys/net/ipv6/conf/", "net.ipv6.conf.eth0.proxy_ndp=1", "net.ipv6.conf.eth0.accept_ra=2", "net.ipv6.conf.all.forwarding=1"} {
		if !strings.Contains(command, fragment) {
			t.Fatalf("command missing %q: %s", fragment, command)
		}
	}
	if strings.Contains(command, "net.ipv6.conf.all.proxy_ndp") {
		t.Fatalf("command enables global NDP proxying: %s", command)
	}
}

func TestLXDNetworkDeviceIPv6ErrorIncludesProbeDetails(t *testing.T) {
	if global.APP_LOG == nil {
		global.APP_LOG = zap.NewNop()
	}
	executor := &recordingLXDIPv6Executor{outputs: []string{
		"2606:4700::1111",
		"probe diagnostic\nnot-an-ipv6",
		"probe diagnostic\nnot-an-ipv6",
	}}
	lxdProvider := &LXDProvider{sshClient: utils.NewSafeShellExecutor(executor)}

	_, err := lxdProvider.setupNetworkDeviceIPv6(context.Background(), IPv6Config{ContainerName: "guest"})
	if err == nil {
		t.Fatal("setupNetworkDeviceIPv6() succeeded without a host IPv6 CIDR")
	}
	for _, fragment := range []string{
		"未找到可分配的本机公网IPv6前缀",
		"output=probe diagnostic\\nnot-an-ipv6",
	} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("setupNetworkDeviceIPv6() error %q missing %q", err, fragment)
		}
	}
}

func TestLXDStaticIPv6SkipsMissingHostCIDRAndParsesNoisyTunnelProbe(t *testing.T) {
	if global.APP_LOG == nil {
		global.APP_LOG = zap.NewNop()
	}
	executor := &recordingLXDIPv6Executor{
		outputs: []string{
			"2606:4700::1111",
			"warning: transport diagnostic\nhe-ipv6",
			"7: he-ipv6    inet6 2606:4700::1111/64 scope global",
			"sysctl failed",
		},
		errors: []error{nil, nil, nil, errors.New("stop after sysctl")},
	}
	lxdProvider := &LXDProvider{sshClient: utils.NewSafeShellExecutor(executor)}

	_, err := lxdProvider.setupNetworkDeviceIPv6(context.Background(), IPv6Config{
		ContainerName: "guest",
		ContainerIPv6: "2606:4700::1234",
	})
	if err == nil || !strings.Contains(err.Error(), "配置IPv6 sysctl失败") {
		t.Fatalf("setupNetworkDeviceIPv6() error = %v, want post-probe sysctl failure", err)
	}
	if len(executor.commands) != 4 || !strings.Contains(executor.commands[1], "ip -6 route show default") ||
		!strings.Contains(executor.commands[2], "ip -o -6 addr show scope global") ||
		!strings.Contains(executor.commands[3], "net.ipv6.conf.he-ipv6") {
		t.Fatalf("static IPv6 did not use the paired tunnel network: %#v", executor.commands)
	}
}

func TestLXDSelectHostIPv6InterfaceNetworkPrefersDelegatedBridge(t *testing.T) {
	executor := &recordingLXDIPv6Executor{outputs: []string{
		"vmbr0",
		"2: vmbr0    inet6 2a14:7c0:1002:10f8::1/128 scope global\n" +
			"4: vmbr2    inet6 2a14:7c0:1002:10f8::1/38 scope global\n",
	}}
	lxdProvider := &LXDProvider{sshClient: utils.NewSafeShellExecutor(executor)}
	selected, err := lxdProvider.selectHostIPv6InterfaceNetwork(context.Background(), true)
	if err != nil {
		t.Fatalf("selectHostIPv6InterfaceNetwork() error = %v", err)
	}
	if selected.Interface != "vmbr2" || selected.Network.PrefixLen != 38 {
		t.Fatalf("selected = %#v, want vmbr2 delegated /38", selected)
	}
}

func TestLXDCheckIPv6RequiresLocallyBoundAddress(t *testing.T) {
	if global.APP_LOG == nil {
		global.APP_LOG = zap.NewNop()
	}
	executor := &recordingLXDIPv6Executor{outputs: []string{"fd42::1/64\n2606:4700::1111/64\n"}}
	lxdProvider := &LXDProvider{sshClient: utils.NewSafeShellExecutor(executor)}

	got, err := lxdProvider.checkIPv6(context.Background())
	if err != nil || got != "2606:4700::1111" {
		t.Fatalf("checkIPv6() = (%q, %v), want locally bound public IPv6", got, err)
	}
	if len(executor.commands) != 1 || strings.Contains(executor.commands[0], "curl") || !strings.Contains(executor.commands[0], "ip -o -6 addr show scope global") {
		t.Fatalf("checkIPv6 commands = %#v, want one local interface query", executor.commands)
	}
}

func TestLXDCheckIPv6DoesNotFallBackToExternalAddress(t *testing.T) {
	if global.APP_LOG == nil {
		global.APP_LOG = zap.NewNop()
	}
	executor := &recordingLXDIPv6Executor{outputs: []string{""}}
	lxdProvider := &LXDProvider{sshClient: utils.NewSafeShellExecutor(executor)}

	if _, err := lxdProvider.checkIPv6(context.Background()); err == nil || !strings.Contains(err.Error(), "本机绑定") {
		t.Fatalf("checkIPv6() error = %v, want missing local IPv6 error", err)
	}
	if len(executor.commands) != 1 {
		t.Fatalf("checkIPv6 commands = %#v, want no external fallback", executor.commands)
	}
}

func TestLXDRoutedIPv6ChecksManagedBridgeAndProtectsExistingEth1(t *testing.T) {
	executor := &recordingLXDIPv6Executor{
		errors:  []error{nil, nil, errors.New("existing device is unrelated")},
		outputs: []string{"", "", "existing eth1"},
	}
	lxdProvider := &LXDProvider{sshClient: utils.NewSafeShellExecutor(executor)}
	_, err := lxdProvider.setupRoutedNetworkDeviceIPv6(IPv6Config{
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
