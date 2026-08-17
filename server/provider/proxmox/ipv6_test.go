package proxmox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	coreprovider "oneclickvirt/provider"
)

type ipv6CommandExecutor struct {
	commands []string
	fail     func(string) error
	output   func(string) string
}

func (e *ipv6CommandExecutor) Execute(command string) (string, error) {
	e.commands = append(e.commands, command)
	output := ""
	if e.output != nil {
		output = e.output(command)
	}
	if e.fail != nil {
		return output, e.fail(command)
	}
	return output, nil
}

func (e *ipv6CommandExecutor) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}

func (e *ipv6CommandExecutor) ExecuteWithLogging(command, _ string) (string, error) {
	return e.Execute(command)
}

func (e *ipv6CommandExecutor) ExecuteRaw(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}

func (e *ipv6CommandExecutor) ExecuteViaTempScript(string, []string, time.Duration) (string, error) {
	return "", nil
}

func (e *ipv6CommandExecutor) UploadContent(string, string, os.FileMode) error { return nil }
func (e *ipv6CommandExecutor) IsHealthy() bool                                 { return true }
func (e *ipv6CommandExecutor) Reconnect() error                                { return nil }
func (e *ipv6CommandExecutor) Close() error                                    { return nil }

func TestExecuteIPv6NetworkCommandFallsBackWithoutRate(t *testing.T) {
	executor := &ipv6CommandExecutor{fail: func(command string) error {
		if strings.Contains(command, "rate=") {
			return errors.New("unsupported rate")
		}
		return nil
	}}
	provider := NewProxmoxProvider().(*ProxmoxProvider)
	provider.sshClient.SetExecutor(executor)

	err := provider.executeIPv6NetworkCommand(
		"qm set 101 --net0 virtio,bridge=vmbr2,rate=10",
		"qm set 101 --net0 virtio,bridge=vmbr2",
		"configure net0",
	)
	if err != nil {
		t.Fatalf("executeIPv6NetworkCommand() error = %v", err)
	}
	if len(executor.commands) != 2 || strings.Contains(executor.commands[1], "rate=") {
		t.Fatalf("commands = %#v, want one no-rate fallback", executor.commands)
	}
}

func TestPreflightIPv6CreateRejectsMissingRoutedBridge(t *testing.T) {
	executor := &ipv6CommandExecutor{fail: func(string) error { return errors.New("Device oneclickvirt6 does not exist") }}
	provider := NewProxmoxProvider().(*ProxmoxProvider)
	provider.sshClient.SetExecutor(executor)
	config := coreprovider.InstanceConfig{Metadata: map[string]string{
		"network_type":        "nat_ipv4_ipv6",
		"static_ipv6":         "2001:db8::2",
		"static_ipv6_cidr":    "2001:db8::/126",
		"static_ipv6_gateway": "2001:db8::1",
		"static_ipv6_bridge":  "oneclickvirt6",
	}}

	err := provider.preflightIPv6Create(context.Background(), config, "nat_ipv4_ipv6")
	if err == nil || !strings.Contains(err.Error(), "创建实例前IPv6环境检查失败") {
		t.Fatalf("preflightIPv6Create() error = %v, want routed bridge diagnostic", err)
	}
}

func TestPreflightIPv6CreateAcceptsReadyRoutedBridge(t *testing.T) {
	executor := &ipv6CommandExecutor{}
	provider := NewProxmoxProvider().(*ProxmoxProvider)
	provider.sshClient.SetExecutor(executor)
	config := coreprovider.InstanceConfig{Metadata: map[string]string{
		"network_type":        "nat_ipv4_ipv6",
		"static_ipv6":         "2001:db8::2",
		"static_ipv6_cidr":    "2001:db8::/126",
		"static_ipv6_gateway": "2001:db8::1",
		"static_ipv6_bridge":  "oneclickvirt6",
	}}

	if err := provider.preflightIPv6Create(context.Background(), config, "nat_ipv4_ipv6"); err != nil {
		t.Fatalf("preflightIPv6Create() error = %v", err)
	}
	if len(executor.commands) != 1 || !strings.Contains(executor.commands[0], "oneclickvirt6") {
		t.Fatalf("commands = %#v, want one routed host check", executor.commands)
	}
}

func TestRoutedIPv6NeighborReconciliationUsesOneManagedNodeCommand(t *testing.T) {
	executor := &ipv6CommandExecutor{}
	provider := NewProxmoxProvider().(*ProxmoxProvider)
	provider.sshClient.SetExecutor(executor)
	routed := coreprovider.RoutedIPv6Config{TunnelID: 42, Address: "2001:db8::2", Bridge: "oneclickvirt6"}

	if err := provider.reconcileRoutedIPv6Neighbors(routed); err != nil {
		t.Fatalf("reconcileRoutedIPv6Neighbors() error = %v", err)
	}
	if len(executor.commands) != 1 {
		t.Fatalf("commands = %#v, want one managed script invocation", executor.commands)
	}
	if !strings.Contains(executor.commands[0], "/etc/oneclickvirt/ipv6-tunnels/42-pve-neighbors.sh") || !strings.Contains(executor.commands[0], "reconcile") {
		t.Fatalf("unexpected neighbour reconcile command: %s", executor.commands[0])
	}

	if err := provider.reconcileAllRoutedIPv6Neighbors(); err != nil {
		t.Fatalf("reconcileAllRoutedIPv6Neighbors() error = %v", err)
	}
	if len(executor.commands) != 2 || !strings.Contains(executor.commands[1], "*-pve-neighbors.sh") {
		t.Fatalf("all-neighbour reconciliation was not a single batched command: %#v", executor.commands)
	}
}

func TestRoutedIPv6NeighborReconciliationLeavesNonTunnelIPv6Untouched(t *testing.T) {
	executor := &ipv6CommandExecutor{}
	provider := NewProxmoxProvider().(*ProxmoxProvider)
	provider.sshClient.SetExecutor(executor)

	if err := provider.reconcileRoutedIPv6Neighbors(coreprovider.RoutedIPv6Config{Address: "2001:db8::2", Bridge: "vmbr2"}); err != nil {
		t.Fatalf("reconcileRoutedIPv6Neighbors() error = %v", err)
	}
	if len(executor.commands) != 0 {
		t.Fatalf("non-tunnel static IPv6 unexpectedly invoked neighbour reconciliation: %#v", executor.commands)
	}
}

func TestPreflightIPv6CreateRejectsStaticAddressOnIPv4OnlyNetwork(t *testing.T) {
	executor := &ipv6CommandExecutor{}
	provider := NewProxmoxProvider().(*ProxmoxProvider)
	provider.sshClient.SetExecutor(executor)
	config := coreprovider.InstanceConfig{Metadata: map[string]string{"static_ipv6": "2001:db8::2"}}

	err := provider.preflightIPv6Create(context.Background(), config, "nat_ipv4")
	if err == nil || !strings.Contains(err.Error(), "未启用IPv6") {
		t.Fatalf("preflightIPv6Create() error = %v, want IPv4-only rejection", err)
	}
	if len(executor.commands) != 0 {
		t.Fatalf("commands = %#v, want no remote checks", executor.commands)
	}
}

func TestProxmoxAPICreateMutationBlocksSSHFallback(t *testing.T) {
	mutationErr := fmt.Errorf("wrapped create failure: %w", proxmoxAPICreateMutationError(123, errors.New("request timed out")))
	if !proxmoxAPICreateMayHaveMutated(mutationErr) {
		t.Fatalf("mutation error was not detected: %v", mutationErr)
	}
	if !proxmoxAPICreateFallbackBlocked(mutationErr) {
		t.Fatalf("unsafe API create error did not block SSH fallback: %v", mutationErr)
	}
	if proxmoxAPICreateFallbackBlocked(errors.New("image download failed before POST")) {
		t.Fatal("pre-create API error unexpectedly blocked SSH fallback")
	}
}

func TestExecuteIPv6NetworkCommandReturnsFallbackFailure(t *testing.T) {
	executor := &ipv6CommandExecutor{
		fail:   func(string) error { return errors.New("command failed") },
		output: func(string) string { return "PVE rejected this command" },
	}
	provider := NewProxmoxProvider().(*ProxmoxProvider)
	provider.sshClient.SetExecutor(executor)

	err := provider.executeIPv6NetworkCommand("primary", "fallback", "configure net0")
	if err == nil || len(executor.commands) != 2 {
		t.Fatalf("error = %v, commands = %#v", err, executor.commands)
	}
	if !strings.Contains(err.Error(), "PVE rejected this command") || !strings.Contains(err.Error(), "fallback") {
		t.Fatalf("error = %v, want remote output and fallback command", err)
	}
}

func TestEnsureVMIPv6InterfaceReusesInitialDualStackNIC(t *testing.T) {
	executor := &ipv6CommandExecutor{output: func(command string) string {
		if command == "qm config 101" {
			return "net0: virtio=02:00:00:00:00:01,bridge=vmbr0\nnet1: virtio=02:00:00:00:00:02,bridge=oneclickvirt6,firewall=0\n"
		}
		return ""
	}}
	provider := NewProxmoxProvider().(*ProxmoxProvider)
	provider.sshClient.SetExecutor(executor)

	if err := provider.ensureVMIPv6Interface(101, "oneclickvirt6"); err != nil {
		t.Fatalf("ensureVMIPv6Interface() error = %v", err)
	}
	if len(executor.commands) != 1 || executor.commands[0] != "qm config 101" {
		t.Fatalf("commands = %#v, want only one config read", executor.commands)
	}
}

func TestEnsureVMIPv6InterfaceCreatesMissingNIC(t *testing.T) {
	executor := &ipv6CommandExecutor{}
	provider := NewProxmoxProvider().(*ProxmoxProvider)
	provider.sshClient.SetExecutor(executor)

	if err := provider.ensureVMIPv6Interface(101, "oneclickvirt6"); err != nil {
		t.Fatalf("ensureVMIPv6Interface() error = %v", err)
	}
	if len(executor.commands) != 2 || executor.commands[0] != "qm config 101" || !strings.Contains(executor.commands[1], "--net1 virtio,bridge=oneclickvirt6") {
		t.Fatalf("commands = %#v, want config check then net1 creation", executor.commands)
	}
}

func TestProxmoxVMUsesIPv6SecondNICOnlyForDualStack(t *testing.T) {
	for networkType, want := range map[string]bool{
		"nat_ipv4":            false,
		"nat_ipv4_ipv6":       true,
		"dedicated_ipv4_ipv6": true,
		"ipv6_only":           false,
	} {
		if got := proxmoxVMUsesIPv6SecondNIC(networkType); got != want {
			t.Fatalf("proxmoxVMUsesIPv6SecondNIC(%q) = %t, want %t", networkType, got, want)
		}
	}
}

func TestSetupNATMappingUsesIdempotentRulesAndPersistence(t *testing.T) {
	executor := &ipv6CommandExecutor{}
	provider := NewProxmoxProvider().(*ProxmoxProvider)
	provider.sshClient.SetExecutor(executor)

	if err := provider.setupNATMapping(context.Background(), "2001:db8:1::101/64", "2001:db8:2::101/128"); err != nil {
		t.Fatalf("setupNATMapping() error = %v", err)
	}
	joined := strings.Join(executor.commands, "\n")
	for _, fragment := range []string{
		"ip6tables -t nat -C PREROUTING",
		"|| ip6tables -t nat -A PREROUTING",
		"ip6tables -t nat -C POSTROUTING",
		"|| ip6tables -t nat -A POSTROUTING",
	} {
		if !strings.Contains(joined, fragment) {
			t.Fatalf("commands missing %q:\n%s", fragment, joined)
		}
	}
	persistCount := 0
	for _, command := range executor.commands {
		if strings.Contains(command, "grep -Fqx --") {
			persistCount++
		}
	}
	if persistCount != 2 {
		t.Fatalf("persistence command count = %d, want 2", persistCount)
	}
}

func TestSetupNATMappingPropagatesRuleFailure(t *testing.T) {
	executor := &ipv6CommandExecutor{fail: func(command string) error {
		if strings.Contains(command, "-C PREROUTING") {
			return errors.New("ip6tables unavailable")
		}
		return nil
	}}
	provider := NewProxmoxProvider().(*ProxmoxProvider)
	provider.sshClient.SetExecutor(executor)

	err := provider.setupNATMapping(context.Background(), "2001:db8:1::101", "2001:db8:2::101")
	if err == nil || !strings.Contains(err.Error(), "DNAT") {
		t.Fatalf("setupNATMapping() error = %v, want DNAT failure", err)
	}
	for _, command := range executor.commands {
		if strings.Contains(command, "POSTROUTING") || strings.Contains(command, "grep -Fqx") {
			t.Fatalf("continued after DNAT failure: %#v", executor.commands)
		}
	}
}

func TestSetupNATMappingRejectsInvalidAddressBeforeRemoteCommands(t *testing.T) {
	executor := &ipv6CommandExecutor{}
	provider := NewProxmoxProvider().(*ProxmoxProvider)
	provider.sshClient.SetExecutor(executor)

	if err := provider.setupNATMapping(context.Background(), "not-an-ip", "2001:db8::1"); err == nil {
		t.Fatal("expected invalid internal IPv6 to fail")
	}
	if len(executor.commands) != 0 {
		t.Fatalf("commands = %#v, want no remote commands", executor.commands)
	}
}
