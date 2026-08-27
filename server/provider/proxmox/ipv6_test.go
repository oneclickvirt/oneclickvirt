package proxmox

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	coreprovider "oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
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

func setupProxmoxIPv6CommandTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "proxmox-ipv6.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite database: %v", err)
	}
	if err := db.AutoMigrate(&providerModel.Provider{}); err != nil {
		t.Fatalf("migrate provider table: %v", err)
	}
	if err := db.Create(&providerModel.Provider{ID: 1, DefaultInboundBandwidth: 300, DefaultOutboundBandwidth: 300}).Error; err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	oldDB, oldLog := global.APP_DB, global.APP_LOG
	global.APP_DB = db
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() {
		global.APP_DB = oldDB
		global.APP_LOG = oldLog
	})
}

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

func TestProxmoxAPICreateRequestAllowsFallbackForDefinitive4xx(t *testing.T) {
	rejected := proxmoxAPICreateRequestError(123, &proxmoxAPIResponseError{
		StatusCode: http.StatusBadRequest,
		Body:       "invalid parameter",
	})
	if proxmoxAPICreateFallbackBlocked(rejected) {
		t.Fatalf("definitive 4xx create rejection blocked SSH fallback: %v", rejected)
	}

	ambiguous := proxmoxAPICreateRequestError(123, &proxmoxAPIResponseError{
		StatusCode: http.StatusBadGateway,
		Body:       "upstream timeout",
	})
	if !proxmoxAPICreateFallbackBlocked(ambiguous) {
		t.Fatalf("5xx create response did not block SSH fallback: %v", ambiguous)
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

func TestProxmoxNATIPv6StateUsesPersistedULAAndVMID(t *testing.T) {
	natConfig, err := parseProxmoxNATIPv6StateOutput("fd42:5339:296f:1f07::/64\tfd42:5339:296f:1f07::1\n")
	if err != nil {
		t.Fatalf("parseProxmoxNATIPv6StateOutput() error = %v", err)
	}
	if got := natConfig.Network.CIDR(); got != "fd42:5339:296f:1f07::/64" {
		t.Fatalf("NAT CIDR = %q", got)
	}
	if natConfig.Gateway != "fd42:5339:296f:1f07::1" {
		t.Fatalf("NAT gateway = %q", natConfig.Gateway)
	}
	address, err := proxmoxNATIPv6ForVMID(&natConfig, 101)
	if err != nil || address != "fd42:5339:296f:1f07::65" {
		t.Fatalf("proxmoxNATIPv6ForVMID() = %q, %v", address, err)
	}
}

func TestProxmoxNATIPv6StateRejectsDocumentationAndInvalidPrefixes(t *testing.T) {
	for _, state := range []string{
		"2001:db8:1::/64\t2001:db8:1::1",
		"2605:52c0:2:14b::/64\t2605:52c0:2:14b::1",
		"fd42:5339:296f:1f07::/112\tfd42:5339:296f:1f07::1",
		"fd42:5339:296f:1f07::/64\tfd42:5339:296f:1f08::1",
	} {
		if _, err := parseProxmoxNATIPv6StateOutput(state); err == nil {
			t.Fatalf("parseProxmoxNATIPv6StateOutput(%q) unexpectedly succeeded", state)
		}
	}
}

func TestProxmoxNATIPv6BridgeDiscoveryAcceptsULAOnly(t *testing.T) {
	output := "10: vmbr1    inet6 fd42:5339:296f:1f09::1/64 scope global\n" +
		"10: vmbr1    inet6 2605:52c0:2:14b::1/64 scope global\n"
	natConfig, err := parseProxmoxNATIPv6BridgeOutput(output)
	if err != nil {
		t.Fatalf("parseProxmoxNATIPv6BridgeOutput() error = %v", err)
	}
	if natConfig.Network.CIDR() != "fd42:5339:296f:1f09::/64" || natConfig.Gateway != "fd42:5339:296f:1f09::1" {
		t.Fatalf("discovered NAT config = %#v", natConfig)
	}
}

func TestProxmoxDirectIPv6RejectsHostOnlyPrefix(t *testing.T) {
	info := &IPv6Info{
		HostIPv6Address: "2a14:7c0:1002:10f8::1",
		Network: utils.IPv6Network{
			Address:   net.ParseIP("2a14:7c0:1002:10f8::1"),
			PrefixLen: 128,
		},
	}
	if hasDirectProxmoxIPv6Info(info) {
		t.Fatal("host-only /128 unexpectedly accepted as an automatic direct-allocation prefix")
	}
}

func TestProxmoxDirectIPv6AllocationSupportsNonNibblePrefixWithoutHostCollision(t *testing.T) {
	p := NewProxmoxProvider().(*ProxmoxProvider)
	nonNibble := &IPv6Info{
		HostIPv6Address: "2a14:7c0:1002:10f8::1",
		Network: utils.IPv6Network{
			Address:   net.ParseIP("2a14:7c0:1002:10f8::1"),
			PrefixLen: 38,
		},
	}
	address, err := p.addressForVMID(nonNibble, 101)
	if err != nil || address != "2a14:7c0:1000::65" {
		t.Fatalf("non-nibble addressForVMID() = %q, %v", address, err)
	}

	hostCollision := &IPv6Info{
		HostIPv6Address: "2a14:7c0:1002::64",
		Network: utils.IPv6Network{
			Address:   net.ParseIP("2a14:7c0:1002::64"),
			PrefixLen: 118,
		},
	}
	address, err = p.addressForVMID(hostCollision, 100)
	if err != nil || address != "2a14:7c0:1002::3e8" {
		t.Fatalf("host collision addressForVMID() = %q, %v", address, err)
	}
}

func TestProxmoxDirectIPv6AutomaticAllocationRejectsNarrowPrefix(t *testing.T) {
	p := NewProxmoxProvider().(*ProxmoxProvider)
	info := &IPv6Info{
		HostIPv6Address: "2a14:7c0:1002::1",
		Network: utils.IPv6Network{
			Address:   net.ParseIP("2a14:7c0:1002::1"),
			PrefixLen: 119,
		},
	}
	if canAutoAllocateProxmoxIPv6(info) {
		t.Fatal("/119 direct prefix unexpectedly passed automatic-allocation preflight")
	}
	if _, err := p.addressForVMID(info, 100); err == nil {
		t.Fatal("/119 direct prefix unexpectedly accepted for automatic VMID allocation")
	}
}

func TestConfigureProxmoxVMNATIPv6UsesPersistedULA(t *testing.T) {
	setupProxmoxIPv6CommandTestDB(t)
	executor := &ipv6CommandExecutor{}
	p := NewProxmoxProvider().(*ProxmoxProvider)
	p.config.ID = 1
	p.sshClient.SetExecutor(executor)
	natConfig, err := parseProxmoxNATIPv6StateOutput("fd42:5339:296f:1f07::/64\tfd42:5339:296f:1f07::1")
	if err != nil {
		t.Fatal(err)
	}
	config := coreprovider.InstanceConfig{Metadata: map[string]string{
		"network_type": "ipv6_only",
		"static_ipv6":  "2605:52c0:2:14b::101",
	}}
	if err := p.configureVMIPv6(context.Background(), 101, config, "vmbr1", true, &IPv6Info{}, &natConfig, true); err != nil {
		t.Fatalf("configureVMIPv6() error = %v", err)
	}
	joined := strings.Join(executor.commands, "\n")
	if !strings.Contains(joined, "ip6='fd42:5339:296f:1f07::65/64',gw6='fd42:5339:296f:1f07::1'") || strings.Contains(joined, "2001:db8:1::") {
		t.Fatalf("VM NAT IPv6 commands did not use persisted ULA:\n%s", joined)
	}
}

func TestConfigureProxmoxContainerNATIPv6UsesPersistedULA(t *testing.T) {
	setupProxmoxIPv6CommandTestDB(t)
	executor := &ipv6CommandExecutor{}
	p := NewProxmoxProvider().(*ProxmoxProvider)
	p.config.ID = 1
	p.sshClient.SetExecutor(executor)
	p.bridgeNAT = "vmbr1"
	natConfig, err := parseProxmoxNATIPv6StateOutput("fd42:5339:296f:1f07::/64\tfd42:5339:296f:1f07::1")
	if err != nil {
		t.Fatal(err)
	}
	config := coreprovider.InstanceConfig{Metadata: map[string]string{
		"network_type": "ipv6_only",
		"static_ipv6":  "2605:52c0:2:14b::101",
	}}
	if err := p.configureContainerIPv6(context.Background(), 101, config, "vmbr1", true, &IPv6Info{}, &natConfig, true); err != nil {
		t.Fatalf("configureContainerIPv6() error = %v", err)
	}
	joined := strings.Join(executor.commands, "\n")
	if !strings.Contains(joined, "ip6='fd42:5339:296f:1f07::65/64',bridge=vmbr1,gw6='fd42:5339:296f:1f07::1'") || strings.Contains(joined, "2001:db8:1::") {
		t.Fatalf("container NAT IPv6 commands did not use persisted ULA:\n%s", joined)
	}
}

func TestGetNATMappedIPv6UsesPersistedULAAddress(t *testing.T) {
	executor := &ipv6CommandExecutor{output: func(command string) string {
		switch {
		case strings.Contains(command, pveNATIPv6SubnetFile):
			return "fd42:5339:296f:1f07::/64\tfd42:5339:296f:1f07::1\n"
		case strings.Contains(command, "grep -F --") && strings.Contains(command, "fd42:5339:296f:1f07::65"):
			return "ip6tables -t nat -A PREROUTING -d '2605:52c0:2:14b::101' -j DNAT --to-destination 'fd42:5339:296f:1f07::65'\n"
		default:
			return ""
		}
	}}
	p := NewProxmoxProvider().(*ProxmoxProvider)
	p.bridgeNAT = "vmbr1"
	p.sshClient.SetExecutor(executor)

	address, err := p.getNATMappedIPv6(context.Background(), "101")
	if err != nil || address != "2605:52c0:2:14b::101" {
		t.Fatalf("getNATMappedIPv6() = %q, %v", address, err)
	}
	joined := strings.Join(executor.commands, "\n")
	if strings.Contains(joined, "2001:db8:1::") {
		t.Fatalf("new mapping lookup unexpectedly queried the legacy prefix:\n%s", joined)
	}
}

func TestCleanupNATRulesUsesPersistedULAAddress(t *testing.T) {
	executor := &ipv6CommandExecutor{output: func(command string) string {
		switch {
		case strings.Contains(command, pveNATIPv6SubnetFile):
			return "fd42:5339:296f:1f07::/64\tfd42:5339:296f:1f07::1\n"
		case strings.Contains(command, "grep -F --") && strings.Contains(command, "fd42:5339:296f:1f07::65"):
			return "ip6tables -t nat -A PREROUTING -d '2605:52c0:2:14b::101' -j DNAT --to-destination 'fd42:5339:296f:1f07::65'\n"
		default:
			return ""
		}
	}}
	p := NewProxmoxProvider().(*ProxmoxProvider)
	p.bridgeNAT = "vmbr1"
	p.sshClient.SetExecutor(executor)

	if err := p.cleanupIPv6NATRules(context.Background(), "101"); err != nil {
		t.Fatalf("cleanupIPv6NATRules() error = %v", err)
	}
	joined := strings.Join(executor.commands, "\n")
	for _, fragment := range []string{
		"ip6tables -t nat -D PREROUTING -d '2605:52c0:2:14b::101' -j DNAT --to-destination 'fd42:5339:296f:1f07::65'",
		"ip6tables -t nat -D POSTROUTING -s 'fd42:5339:296f:1f07::65' -j SNAT --to-source '2605:52c0:2:14b::101'",
		"grep -Fv -- 'fd42:5339:296f:1f07::65'",
	} {
		if !strings.Contains(joined, fragment) {
			t.Fatalf("cleanup commands missing %q:\n%s", fragment, joined)
		}
	}
}
