package ipv6tunnel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	providerModel "oneclickvirt/model/provider"
	ipv6poolService "oneclickvirt/service/ipv6pool"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var tunnelTestDBSequence atomic.Uint64

func setupTunnelTestService(t *testing.T, executor remoteExecutor) (*Service, *gorm.DB) {
	t.Helper()
	dsn := fmt.Sprintf("file:ipv6tunnel_%d?mode=memory&cache=shared", tunnelTestDBSequence.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sqlite handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.Exec("CREATE TABLE providers (id integer PRIMARY KEY)").Error; err != nil {
		t.Fatalf("create providers: %v", err)
	}
	if err := db.AutoMigrate(&providerModel.ProviderIPv6Tunnel{}, &providerModel.ProviderIPv6Pool{}); err != nil {
		t.Fatalf("migrate tunnels: %v", err)
	}
	if err := db.Exec("INSERT INTO providers (id) VALUES (1)").Error; err != nil {
		t.Fatalf("insert provider: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return &Service{db: db, execute: executor}, db
}

func validTunnelConfig() Config {
	return Config{
		Name: "HE Los Angeles", Mode: "sit", Interface: "he-ipv6",
		LocalIPv4: "192.0.2.10", RemoteIPv4: "198.51.100.1",
		LocalIPv6: "2001:db8:0:1::2/64", RemoteIPv6: "2001:db8:0:1::1",
		RoutedCIDR: "2001:db8:1234:5678::10/80", MTU: 1480, TTL: 255,
		RouteMetric: 100, DefaultRoute: true,
	}
}

func TestNormalizeConfigCanonicalizesIPv6AndDefaults(t *testing.T) {
	config := validTunnelConfig()
	config.RoutedCIDR = "2001:db8:1234:5678:abcd::10/80"
	config.MTU = 0
	config.TTL = 0
	config.RouteMetric = 0

	normalized, err := normalizeConfig(config)
	if err != nil {
		t.Fatalf("normalizeConfig() error = %v", err)
	}
	if normalized.LocalIPv6 != "2001:db8:0:1::2/64" {
		t.Fatalf("LocalIPv6 = %q", normalized.LocalIPv6)
	}
	if normalized.RoutedCIDR != "2001:db8:1234:5678:abcd::/80" {
		t.Fatalf("RoutedCIDR = %q", normalized.RoutedCIDR)
	}
	if normalized.MTU != defaultMTU || normalized.TTL != defaultTTL || normalized.RouteMetric != defaultRouteMetric {
		t.Fatalf("defaults not applied: %#v", normalized)
	}
}

func TestNormalizeConfigCanonicalizes6in4AliasesAndRejectsIPIP(t *testing.T) {
	for _, mode := range []string{"sit", "6in4", "v4tunnel"} {
		config := validTunnelConfig()
		config.Mode = mode
		normalized, err := normalizeConfig(config)
		if err != nil || normalized.Mode != "sit" {
			t.Fatalf("mode %q normalized to %#v, %v", mode, normalized, err)
		}
	}
	config := validTunnelConfig()
	config.Mode = "ipip"
	if _, err := normalizeConfig(config); err == nil || !strings.Contains(err.Error(), "IPIP") {
		t.Fatalf("IPIP unexpectedly accepted: %v", err)
	}
}

func TestNormalizeConfigRejectsCommandInjectionAndPollutedValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "newline", mutate: func(config *Config) { config.Interface = "he0\nip link delete eth0" }},
		{name: "ansi", mutate: func(config *Config) { config.Name = "HE\x1b[31m" }},
		{name: "shell metacharacter", mutate: func(config *Config) { config.Interface = "he0;reboot" }},
		{name: "IPv6 diagnostic output", mutate: func(config *Config) { config.LocalIPv6 = "inet6 2001:db8::2/64 scope global" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := validTunnelConfig()
			test.mutate(&config)
			if _, err := normalizeConfig(config); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestDetectLocalIPv4UsesRouteSourceAndAllowsNATPrivateAddress(t *testing.T) {
	var calls atomic.Int32
	service, _ := setupTunnelTestService(t, func(_ context.Context, providerID uint, command string) (string, error) {
		calls.Add(1)
		if providerID != 1 || !strings.Contains(command, "ip -4 route get '216.66.38.58'") {
			t.Fatalf("unexpected detection command: %q", command)
		}
		return "216.66.38.58 via 172.17.176.1 dev vmbr0 src 172.17.176.229 uid 0\n", nil
	})

	detection, err := service.DetectLocalIPv4(context.Background(), 1, "216.66.38.58")
	if err != nil {
		t.Fatalf("DetectLocalIPv4() error = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("remote calls = %d, want 1", calls.Load())
	}
	if detection.LocalIPv4 != "172.17.176.229" || detection.Interface != "vmbr0" || detection.RemoteIPv4 != "216.66.38.58" {
		t.Fatalf("unexpected detection: %#v", detection)
	}
}

func TestDetectLocalIPv4StripsTerminalColorsFromRouteOutput(t *testing.T) {
	service, _ := setupTunnelTestService(t, func(_ context.Context, _ uint, _ string) (string, error) {
		return "\x1b[1;32m216.66.80.30\x1b[0m via \x1b[36m172.17.176.1\x1b[0m dev \x1b[33menp1s0\x1b[0m src \x1b[1;34m172.17.176.229\x1b[0m uid 0\n", nil
	})

	detection, err := service.DetectLocalIPv4(context.Background(), 1, "216.66.80.30")
	if err != nil {
		t.Fatalf("DetectLocalIPv4() error = %v", err)
	}
	if detection.LocalIPv4 != "172.17.176.229" || detection.Interface != "enp1s0" {
		t.Fatalf("terminal colors were not stripped: %#v", detection)
	}
}

func TestDetectLocalIPv4ReturnsReadableRemoteDiagnostic(t *testing.T) {
	service, _ := setupTunnelTestService(t, func(_ context.Context, _ uint, _ string) (string, error) {
		return "RTNETLINK answers: Network is unreachable", errors.New("remote command failed")
	})
	_, err := service.DetectLocalIPv4(context.Background(), 1, "216.66.38.58")
	if err == nil || !IsRemoteCommandError(err) || !strings.Contains(err.Error(), "Network is unreachable") {
		t.Fatalf("unexpected detection error: %v", err)
	}
}

func TestDetectLocalIPv4RejectsMissingOrInvalidRouteSource(t *testing.T) {
	for name, output := range map[string]string{
		"missing src":  "216.66.38.58 via 172.17.176.1 dev vmbr0\n",
		"loopback src": "216.66.38.58 dev lo src 127.0.0.1\n",
	} {
		t.Run(name, func(t *testing.T) {
			service, _ := setupTunnelTestService(t, func(_ context.Context, _ uint, _ string) (string, error) {
				return output, nil
			})
			_, err := service.DetectLocalIPv4(context.Background(), 1, "216.66.38.58")
			if err == nil || !IsRemoteCommandError(err) {
				t.Fatalf("unexpected detection error: %v", err)
			}
		})
	}
}

func TestGeneratedTunnelCommandsPassShellSyntaxCheck(t *testing.T) {
	config, err := normalizeConfig(validTunnelConfig())
	if err != nil {
		t.Fatalf("normalizeConfig() error = %v", err)
	}
	tunnel := config.toModel(1)
	tunnel.ID = 42
	commands := map[string]string{
		"script":    renderTunnelScript(tunnel),
		"neighbors": renderPVERoutedIPv6NeighborScript(tunnel),
		"apply":     buildApplyCommand(tunnel),
		"disable":   buildDisableCommand(tunnel),
		"delete":    buildDeleteCommand([]providerModel.ProviderIPv6Tunnel{tunnel}),
		"check":     buildCheckCommand([]providerModel.ProviderIPv6Tunnel{tunnel}),
	}
	for name, command := range commands {
		t.Run(name, func(t *testing.T) {
			if output, err := exec.Command("sh", "-n", "-c", command).CombinedOutput(); err != nil {
				t.Fatalf("shell syntax error: %v\n%s", err, output)
			}
		})
	}
}

func TestRoutedTunnelScriptsEnableDynamicGuestForwardingForSupportedModes(t *testing.T) {
	for _, mode := range []string{"sit", "gre"} {
		t.Run(mode, func(t *testing.T) {
			config := validTunnelConfig()
			config.Mode = mode
			normalized, err := normalizeConfig(config)
			if err != nil {
				t.Fatalf("normalizeConfig() error = %v", err)
			}
			tunnel := normalized.toModel(1)
			tunnel.ID = 42
			script := renderTunnelScript(tunnel)
			for _, fragment := range []string{"net.ipv6.conf.all.forwarding=1", "net.ipv6.conf.default.forwarding=1", "net.ipv6.conf.$IFACE.forwarding", "net.ipv6.conf.$BRIDGE.forwarding"} {
				if !strings.Contains(script, fragment) {
					t.Fatalf("script missing %q: %s", fragment, script)
				}
			}
			if mode == "gre" && (!strings.Contains(script, "modprobe ip_gre") || !strings.Contains(script, "mode gre")) {
				t.Fatalf("GRE script is incomplete: %s", script)
			}
		})
	}
}

func TestDefaultRouteCheckAcceptsPVERouteOutputWithoutRepeatedDevice(t *testing.T) {
	config, err := normalizeConfig(validTunnelConfig())
	if err != nil {
		t.Fatalf("normalizeConfig() error = %v", err)
	}
	tunnel := config.toModel(1)
	tunnel.ID = 42
	script := renderTunnelScript(tunnel)
	start := strings.Index(script, "check_default_route() {")
	if start < 0 {
		t.Fatal("default route check is missing")
	}
	end := strings.Index(script[start:], "\n}\n")
	if end < 0 {
		t.Fatal("default route check is not complete")
	}
	check := script[start : start+end+3]

	binDir := t.TempDir()
	ipPath := filepath.Join(binDir, "ip")
	if err := os.WriteFile(ipPath, []byte(`#!/bin/sh
# ip route show with a dev filter does not repeat the selected interface.
printf '%s\n' 'default via 2001:db8:0:1::1 proto static metric 100 onlink pref medium'
`), 0o700); err != nil {
		t.Fatalf("write fake ip command: %v", err)
	}

	command := fmt.Sprintf("IFACE=he-ipv6\nREMOTE6=2001:db8:0:1::1\n%s\ncheck_default_route\n", check)
	cmd := exec.Command("sh", "-c", command)
	cmd.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("default route check rejected PVE-formatted route output: %v\n%s", err, output)
	}
}

func TestPVERoutedNeighborScriptReconcilesContainerAndVMConfigs(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is required to execute the generated PVE neighbour script")
	}
	config, err := normalizeConfig(validTunnelConfig())
	if err != nil {
		t.Fatalf("normalizeConfig() error = %v", err)
	}
	tunnel := config.toModel(1)
	tunnel.ID = 42

	root := t.TempDir()
	lxcDir := filepath.Join(root, "lxc")
	qemuDir := filepath.Join(root, "qemu-server")
	if err := os.MkdirAll(lxcDir, 0o700); err != nil {
		t.Fatalf("make lxc directory: %v", err)
	}
	if err := os.MkdirAll(qemuDir, 0o700); err != nil {
		t.Fatalf("make qemu directory: %v", err)
	}
	ctPath := filepath.Join(lxcDir, "100.conf")
	if err := os.WriteFile(ctPath, []byte(`net0: name=eth0,bridge=vmbr0,hwaddr=BC:24:11:C2:53:01,ip6=2001:db8:1234:5678::99/80
net1: name=eth1,bridge=oneclickvirt6,hwaddr=BC:24:11:C2:53:AE,ip6=2001:db8:1234:5678::2/80,gw6=2001:db8:1234:5678::1
`), 0o600); err != nil {
		t.Fatalf("write CT config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(qemuDir, "101.conf"), []byte(`net0: virtio=BC:24:11:C2:53:02,bridge=vmbr0
net1: virtio=52:54:00:12:34:56,bridge=oneclickvirt6,firewall=0
ipconfig1: ip6=2001:db8:1234:5678::3/80,gw6=2001:db8:1234:5678::1
`), 0o600); err != nil {
		t.Fatalf("write VM config: %v", err)
	}

	scriptPath := filepath.Join(root, "pve-neighbors.sh")
	if err := os.WriteFile(scriptPath, []byte(renderPVERoutedIPv6NeighborScript(tunnel)), 0o700); err != nil {
		t.Fatalf("write neighbour script: %v", err)
	}
	ipPath := filepath.Join(root, "ip")
	ipLog := filepath.Join(root, "ip.log")
	if err := os.WriteFile(ipPath, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$OCV_IP_LOG"
if [ "${1:-}" = '-6' ] && [ "${2:-}" = 'neigh' ] && [ "${3:-}" = 'show' ]; then
  printf '%s dev oneclickvirt6 lladdr 00:00:00:00:00:00 PERMANENT\n' "${5:-}"
fi
exit 0
`), 0o700); err != nil {
		t.Fatalf("write fake ip: %v", err)
	}
	statePath := filepath.Join(root, "state", "neighbors")
	env := append(os.Environ(),
		"PATH="+root+":"+os.Getenv("PATH"),
		"OCV_IP_LOG="+ipLog,
		"ONECLICKVIRT_PVE_LXC_CONFIG_DIR="+lxcDir,
		"ONECLICKVIRT_PVE_QEMU_CONFIG_DIR="+qemuDir,
		"ONECLICKVIRT_PVE_NEIGHBOR_STATE_PATH="+statePath,
	)
	run := func() {
		cmd := exec.Command("sh", scriptPath, "reconcile")
		cmd.Env = env
		if output, runErr := cmd.CombinedOutput(); runErr != nil {
			t.Fatalf("reconcile PVE neighbours: %v\n%s", runErr, output)
		}
	}
	run()

	log, err := os.ReadFile(ipLog)
	if err != nil {
		t.Fatalf("read fake ip log: %v", err)
	}
	for _, want := range []string{
		"-6 neigh replace 2001:db8:1234:5678::2 lladdr bc:24:11:c2:53:ae nud permanent dev oneclickvirt6",
		"-6 neigh replace 2001:db8:1234:5678::3 lladdr 52:54:00:12:34:56 nud permanent dev oneclickvirt6",
	} {
		if !strings.Contains(string(log), want) {
			t.Fatalf("neighbour command missing %q:\n%s", want, log)
		}
	}
	if strings.Contains(string(log), "2001:db8:1234:5678::99") {
		t.Fatalf("unmanaged bridge address was reconciled:\n%s", log)
	}
	state, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read neighbour state: %v", err)
	}
	if !strings.Contains(string(state), "2001:db8:1234:5678::2 bc:24:11:c2:53:ae") || !strings.Contains(string(state), "2001:db8:1234:5678::3 52:54:00:12:34:56") {
		t.Fatalf("unexpected neighbour state:\n%s", state)
	}

	if err := os.Remove(ctPath); err != nil {
		t.Fatalf("remove CT config: %v", err)
	}
	run()
	log, err = os.ReadFile(ipLog)
	if err != nil {
		t.Fatalf("read fake ip log after cleanup: %v", err)
	}
	if !strings.Contains(string(log), "-6 neigh del 2001:db8:1234:5678::2 dev oneclickvirt6") {
		t.Fatalf("stale CT neighbour was not removed:\n%s", log)
	}
	state, err = os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read neighbour state after cleanup: %v", err)
	}
	if strings.Contains(string(state), "2001:db8:1234:5678::2") || !strings.Contains(string(state), "2001:db8:1234:5678::3") {
		t.Fatalf("state was not reconciled after CT removal:\n%s", state)
	}
}

func TestCheckCommandVerifiesConfiguredDefaultRouteWithoutExternalLookup(t *testing.T) {
	config, err := normalizeConfig(validTunnelConfig())
	if err != nil {
		t.Fatalf("normalizeConfig() error = %v", err)
	}
	tunnel := config.toModel(1)
	tunnel.ID = 42
	check := buildCheckCommand([]providerModel.ProviderIPv6Tunnel{tunnel})
	if strings.Contains(check, "route get 2001:4860:4860::8888") {
		t.Fatalf("check command still depends on an external route lookup: %s", check)
	}
	for _, want := range []string{"ip -6 route show default dev 'he-ipv6'", "-v remote='2001:db8:0:1::1'"} {
		if !strings.Contains(check, want) {
			t.Fatalf("check command missing %q: %s", want, check)
		}
	}
	deleteCommand := buildDeleteCommand([]providerModel.ProviderIPv6Tunnel{tunnel})
	for _, want := range []string{pveNeighborScriptPath(tunnel.ID), pveNeighborStatePath(tunnel.ID)} {
		if !strings.Contains(deleteCommand, want) {
			t.Fatalf("delete command does not clean %q: %s", want, deleteCommand)
		}
	}
}

func TestRoutedPolicyRouteKeepsNativeDefaultRouteAndCleansUp(t *testing.T) {
	config, err := normalizeConfig(validTunnelConfig())
	if err != nil {
		t.Fatalf("normalizeConfig() error = %v", err)
	}
	config.DefaultRoute = false
	tunnel := config.toModel(1)
	tunnel.ID = 42

	table, priority, err := tunnelPolicyRouteParameters(tunnel.ID)
	if err != nil {
		t.Fatalf("tunnelPolicyRouteParameters() error = %v", err)
	}
	if table != 10042 || priority != 30042 {
		t.Fatalf("policy route identifiers = (%d, %d), want (10042, 30042)", table, priority)
	}

	script := renderTunnelScript(tunnel)
	for _, fragment := range []string{
		"POLICY_TABLE=10042",
		"POLICY_PRIORITY=30042",
		"ip -6 route replace table \"$POLICY_TABLE\" default via \"$REMOTE6\" dev \"$IFACE\" metric \"$ROUTE_METRIC\" onlink",
		"ip -6 rule add pref \"$POLICY_PRIORITY\" from \"$ROUTED_CIDR\" table \"$POLICY_TABLE\"",
		"ip -6 route get \"$POLICY_PROBE\" from \"$ROUTED_GATEWAY\" iif \"$BRIDGE\"",
		"check_routed_policy_route()",
		"cleanup_routed_policy_route",
	} {
		if !strings.Contains(script, fragment) {
			t.Fatalf("routed policy script missing %q:\n%s", fragment, script)
		}
	}
	if strings.Contains(script, "ip -6 route replace default via") {
		t.Fatalf("routed-prefix policy must not replace the host native default route:\n%s", script)
	}

	check := buildCheckCommand([]providerModel.ProviderIPv6Tunnel{tunnel})
	for _, fragment := range []string{
		"policy=0",
		"ip -6 rule show",
		"ip -6 route show table 10042",
		"ip -6 route get '2606:4700:4700::1111' from '2001:db8:1234:5678::1' iif 'oneclickvirt6'",
		"$policy",
	} {
		if !strings.Contains(check, fragment) {
			t.Fatalf("batched check missing %q:\n%s", fragment, check)
		}
	}
	if !strings.Contains(buildDisableCommand(tunnel), "\"$script_path\" down") {
		t.Fatal("disable command does not force managed policy-rule cleanup")
	}
	if !strings.Contains(buildDeleteCommand([]providerModel.ProviderIPv6Tunnel{tunnel}), "'"+scriptPath(tunnel.ID)+"' down") {
		t.Fatal("delete command does not force managed policy-rule cleanup")
	}
}

func TestRoutedPolicyStateFailsClosedAndKeepsLegacyCheckCompatibility(t *testing.T) {
	tunnel := validTunnelConfig().toModel(1)
	tunnel.Enabled = true
	state := checkState{
		UnitEnabled: true, UnitActive: true, LinkPresent: true, AddressOK: true,
		RouteOK: true, NetworkConfigOK: true, GatewayOK: true, RoutedOK: true,
		ForwardingOK: true,
	}
	status, message := classifyState(tunnel, state, true)
	if status != providerModel.IPv6TunnelStatusError || !strings.Contains(message, "源地址策略路由") {
		t.Fatalf("classifyState() = (%q, %q), want policy-route diagnostic", status, message)
	}

	states := parseCheckOutput("TUNNEL|42|1|1|1|1|1|1|1|1|1|0\n")
	if states[42].PolicyRouteOK {
		t.Fatalf("new check output did not retain missing policy state: %#v", states[42])
	}
	legacy := parseCheckOutput("TUNNEL|43|1|1|1|1|1|1|1|1|1\n")
	if !legacy[43].RoutedOK || !legacy[43].ForwardingOK || !legacy[43].PolicyRouteOK {
		t.Fatalf("previous 11-field check output lost compatibility: %#v", legacy[43])
	}
}

func TestTunnelPolicyRouteParametersRejectsMainTablePriorityCollision(t *testing.T) {
	tooLarge := uint(ipv6TunnelPolicyRulePriorityLimit-ipv6TunnelPolicyRulePriorityBase) + 1
	if _, _, err := tunnelPolicyRouteParameters(tooLarge); err == nil {
		t.Fatalf("tunnel ID %d unexpectedly received a priority at or after main table", tooLarge)
	}
}

func TestClassifyStateExplainsIncompleteRoutedForwarding(t *testing.T) {
	tunnel := validTunnelConfig().toModel(1)
	tunnel.Enabled = true
	state := checkState{UnitEnabled: true, UnitActive: true, LinkPresent: true, AddressOK: true, RouteOK: true, NetworkConfigOK: true, GatewayOK: true, RoutedOK: true}
	status, message := classifyState(tunnel, state, true)
	if status != providerModel.IPv6TunnelStatusError || !strings.Contains(message, "all、default") {
		t.Fatalf("classifyState() = (%q, %q), want forwarding diagnostic", status, message)
	}
}

func TestNetworkdConfigAndTunnelCommandsProtectPersistentNetworkState(t *testing.T) {
	config, err := normalizeConfig(validTunnelConfig())
	if err != nil {
		t.Fatalf("normalizeConfig() error = %v", err)
	}
	tunnel := config.toModel(1)
	tunnel.ID = 42

	networkd := renderNetworkdConfig(tunnel)
	if !strings.Contains(networkd, "Name=he-ipv6") || !strings.Contains(networkd, "Address=2001:db8:0:1::2/64") || !strings.Contains(networkd, "GatewayOnLink=yes") || !strings.Contains(networkd, "IPv6Forwarding=true") {
		t.Fatalf("networkd config is incomplete: %s", networkd)
	}
	bridgeNetworkd := renderRoutedBridgeNetworkdConfig()
	if !strings.Contains(bridgeNetworkd, "Name=oneclickvirt6") || !strings.Contains(bridgeNetworkd, "IPv6Forwarding=true") {
		t.Fatalf("routed bridge networkd config is incomplete: %s", bridgeNetworkd)
	}
	apply := buildApplyCommand(tunnel)
	if !strings.Contains(apply, networkConfigPath(tunnel.ID)) || !strings.Contains(apply, "recent diagnostics follow") {
		t.Fatalf("apply command does not persist or diagnose networkd state: %s", apply)
	}
	for _, fragment := range []string{"validation_attempts=6", "current tunnel network diagnostics", "tunnel_interface='he-ipv6'"} {
		if !strings.Contains(apply, fragment) {
			t.Fatalf("apply command is missing bounded validation diagnostic %q: %s", fragment, apply)
		}
	}
	script := renderTunnelScript(tunnel)
	if !strings.Contains(script, "metric 100 onlink") || !strings.Contains(script, "ip -6 route show default dev \"$IFACE\"") || !strings.Contains(script, "ping -6 -n -c 1 -W 5") || !strings.Contains(script, "networkctl reconfigure") {
		t.Fatalf("tunnel script does not verify on-link default route and gateway: %s", script)
	}
	if strings.Index(script, "reconfigure_networkd\n    ensure_routed_network") == -1 {
		t.Fatalf("tunnel script does not apply networkd before routed forwarding: %s", script)
	}
	for _, fragment := range []string{"IPv6 tunnel check failed:", "check_default_route()", "check_routed_network()", "check_routed_forwarding()"} {
		if !strings.Contains(script, fragment) {
			t.Fatalf("tunnel script is missing actionable status diagnostic %q: %s", fragment, script)
		}
	}
	for _, fragment := range []string{"net.ipv6.conf.%s.forwarding", "net.ipv6.conf.$IFACE.forwarding", "net.ipv6.conf.$BRIDGE.forwarding"} {
		if !strings.Contains(script, fragment) {
			t.Fatalf("tunnel script missing scoped forwarding %q: %s", fragment, script)
		}
	}
	for _, fragment := range []string{routedBridgeNetworkConfigPath(tunnel.ID), "ensure_routed_bridge_networkd_config", "reconfigure_routed_bridge_networkd"} {
		if !strings.Contains(script, fragment) {
			t.Fatalf("tunnel script missing routed bridge networkd handling %q: %s", fragment, script)
		}
	}
	if !strings.Contains(script, "dev \"$BRIDGE\" nodad") {
		t.Fatalf("tunnel script does not disable DAD for its routed bridge gateway: %s", script)
	}
	if !strings.Contains(script, "grep -F \" dev $BRIDGE \"") || strings.Contains(script, "grep -F \" dev 'oneclickvirt6' \"") {
		t.Fatalf("tunnel script does not match the routed bridge route safely: %s", script)
	}
	for _, fragment := range []string{"net.ipv6.conf.all.forwarding=1", "net.ipv6.conf.default.forwarding=1"} {
		if !strings.Contains(script, fragment) {
			t.Fatalf("tunnel script does not persist dynamic guest forwarding %q: %s", fragment, script)
		}
	}
	if strings.Contains(script, "proxy_ndp") {
		t.Fatalf("tunnel script must not enable proxy NDP: %s", script)
	}
	check := buildCheckCommand([]providerModel.ProviderIPv6Tunnel{tunnel})
	for _, fragment := range []string{"net.ipv6.conf.all.forwarding", "net.ipv6.conf.default.forwarding", "net.ipv6.conf.he-ipv6.forwarding", "net.ipv6.conf.oneclickvirt6.forwarding"} {
		if !strings.Contains(check, fragment) {
			t.Fatalf("tunnel check missing scoped forwarding %q: %s", fragment, check)
		}
	}
	if strings.Contains(check, "proxy_ndp") {
		t.Fatalf("tunnel check must not read proxy NDP settings: %s", check)
	}
	deleteCommand := buildDeleteCommand([]providerModel.ProviderIPv6Tunnel{tunnel})
	if !strings.Contains(deleteCommand, networkConfigPath(tunnel.ID)) || !strings.Contains(deleteCommand, "reload_networkd") {
		t.Fatalf("delete command leaves networkd state behind: %s", deleteCommand)
	}
}

func TestCreateEnabledTunnelUsesOneRemoteCallAndPersistsActive(t *testing.T) {
	var calls atomic.Int32
	var command string
	service, db := setupTunnelTestService(t, func(_ context.Context, providerID uint, remoteCommand string) (string, error) {
		calls.Add(1)
		if providerID != 1 {
			t.Fatalf("providerID = %d", providerID)
		}
		command = remoteCommand
		return "applied\n", nil
	})

	tunnel, err := service.Create(context.Background(), 1, CreateRequest{Config: validTunnelConfig(), Enabled: true})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("remote calls = %d, want 1", calls.Load())
	}
	if tunnel.Status != providerModel.IPv6TunnelStatusActive || !tunnel.Enabled {
		t.Fatalf("tunnel state = %#v", tunnel)
	}
	if !strings.Contains(command, "oneclickvirt-ipv6-tunnel-1.service") || strings.Contains(command, "systemctl restart networking") {
		t.Fatalf("unexpected apply command: %s", command)
	}
	if !strings.Contains(command, "refusing to replace an unmanaged network interface") {
		t.Fatal("apply command does not protect pre-existing unmanaged interfaces")
	}
	var stored providerModel.ProviderIPv6Tunnel
	if err := db.First(&stored, tunnel.ID).Error; err != nil {
		t.Fatalf("read tunnel: %v", err)
	}
	if stored.Status != providerModel.IPv6TunnelStatusActive {
		t.Fatalf("stored status = %q", stored.Status)
	}
}

func TestCreateWithoutLocalIPv4DetectsRouteSourceBeforeSingleApply(t *testing.T) {
	var commands []string
	service, db := setupTunnelTestService(t, func(_ context.Context, _ uint, command string) (string, error) {
		commands = append(commands, command)
		if strings.Contains(command, "ip -4 route get") && !strings.Contains(command, "OneClickVirt managed IPv6") {
			return "216.66.38.58 via 172.17.176.1 dev vmbr0 src 172.17.176.229 uid 0\n", nil
		}
		return "applied\n", nil
	})
	config := validTunnelConfig()
	config.LocalIPv4 = ""

	tunnel, err := service.Create(context.Background(), 1, CreateRequest{Config: config, Enabled: true})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(commands) != 2 {
		t.Fatalf("remote calls = %d, want bounded detect + apply calls", len(commands))
	}
	if tunnel.LocalIPv4 != "172.17.176.229" || !strings.Contains(commands[0], "ip -4 route get '198.51.100.1'") {
		t.Fatalf("automatic source was not used: tunnel=%#v commands=%#v", tunnel, commands)
	}
	var stored providerModel.ProviderIPv6Tunnel
	if err := db.First(&stored, tunnel.ID).Error; err != nil || stored.LocalIPv4 != "172.17.176.229" {
		t.Fatalf("detected IPv4 was not persisted: tunnel=%#v err=%v", stored, err)
	}
}

func TestCheckAllUsesOneRemoteCallForMultipleTunnels(t *testing.T) {
	var calls atomic.Int32
	service, db := setupTunnelTestService(t, func(_ context.Context, _ uint, _ string) (string, error) {
		calls.Add(1)
		return "login banner\nTUNNEL|1|1|1|1|1|1|1|1\nTUNNEL|2|0|0|0|0|1|0|0\n", nil
	})
	first := validTunnelConfig().toModel(1)
	first.Enabled = true
	first.Status = providerModel.IPv6TunnelStatusPending
	secondConfig := validTunnelConfig()
	secondConfig.Name = "Disabled"
	secondConfig.Interface = "tb-ipv6"
	second := secondConfig.toModel(1)
	second.Enabled = false
	second.Status = providerModel.IPv6TunnelStatusPending
	if err := db.Create(&first).Error; err != nil {
		t.Fatalf("create first: %v", err)
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatalf("create second: %v", err)
	}

	tunnels, err := service.CheckAll(context.Background(), 1)
	if err != nil {
		t.Fatalf("CheckAll() error = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("remote calls = %d, want 1", calls.Load())
	}
	if len(tunnels) != 2 || tunnels[0].Status != providerModel.IPv6TunnelStatusActive || tunnels[1].Status != providerModel.IPv6TunnelStatusInactive {
		t.Fatalf("states = %#v", tunnels)
	}
	var stored []providerModel.ProviderIPv6Tunnel
	if err := db.Order("id ASC").Find(&stored).Error; err != nil {
		t.Fatalf("read batched states: %v", err)
	}
	if len(stored) != 2 || stored[0].Status != providerModel.IPv6TunnelStatusActive || stored[1].Status != providerModel.IPv6TunnelStatusInactive {
		t.Fatalf("batched stored states = %#v", stored)
	}
	if stored[0].LastCheckedAt == nil || stored[1].LastCheckedAt == nil {
		t.Fatalf("batched check timestamps were not stored: %#v", stored)
	}
}

func TestActiveUpdateFailureRestoresPreviousDatabaseConfig(t *testing.T) {
	var calls atomic.Int32
	service, db := setupTunnelTestService(t, func(_ context.Context, _ uint, _ string) (string, error) {
		if calls.Add(1) == 1 {
			return "applied", nil
		}
		return "", errors.New("systemctl start failed")
	})
	created, err := service.Create(context.Background(), 1, CreateRequest{Config: validTunnelConfig(), Enabled: true})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	updated := validTunnelConfig()
	updated.Name = "Replacement"
	updated.Interface = "he-new"
	result, err := service.Update(context.Background(), 1, created.ID, updated)
	if err == nil {
		t.Fatal("expected remote apply error")
	}
	if result.Interface != "he-ipv6" || result.Name != "HE Los Angeles" {
		t.Fatalf("returned config was not restored: %#v", result)
	}
	var stored providerModel.ProviderIPv6Tunnel
	if err := db.First(&stored, created.ID).Error; err != nil {
		t.Fatalf("read tunnel: %v", err)
	}
	if stored.Interface != "he-ipv6" || stored.Status != providerModel.IPv6TunnelStatusError || !strings.Contains(stored.LastError, "systemctl start failed") {
		t.Fatalf("stored tunnel = %#v", stored)
	}
}

func TestInactiveUpdateRetiresStaleTunnelPoolWithoutRemoteCall(t *testing.T) {
	var calls atomic.Int32
	service, db := setupTunnelTestService(t, func(_ context.Context, _ uint, _ string) (string, error) {
		calls.Add(1)
		return "", errors.New("inactive tunnel update must not contact node")
	})
	tunnel := validTunnelConfig().toModel(1)
	tunnel.Enabled = false
	tunnel.Status = providerModel.IPv6TunnelStatusInactive
	if err := db.Create(&tunnel).Error; err != nil {
		t.Fatalf("create tunnel: %v", err)
	}
	parent := providerModel.ProviderIPv6Pool{
		ProviderID: 1, Address: "2001:db8:1234:5678::/80", PrefixLength: 80,
		IsRange: true, RangeNext: "2001:db8:1234:5678::2", Source: ipv6poolService.SourceTunnel,
		TunnelID: &tunnel.ID,
	}
	if err := db.Create(&parent).Error; err != nil {
		t.Fatalf("create stale tunnel pool: %v", err)
	}

	updated := validTunnelConfig()
	updated.Name = "inactive updated"
	updated.Interface = "he-updated"
	result, err := service.Update(context.Background(), 1, tunnel.ID, updated)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if calls.Load() != 0 || result.Status != providerModel.IPv6TunnelStatusInactive || result.Enabled {
		t.Fatalf("unexpected inactive update result: calls=%d tunnel=%#v", calls.Load(), result)
	}
	var activePoolRows int64
	if err := db.Model(&providerModel.ProviderIPv6Pool{}).Where("provider_id = ? AND tunnel_id = ?", 1, tunnel.ID).Count(&activePoolRows).Error; err != nil {
		t.Fatalf("count stale pool: %v", err)
	}
	if activePoolRows != 0 {
		t.Fatalf("stale tunnel pool rows = %d, want 0", activePoolRows)
	}
}

func TestDeleteFailureKeepsControllerRecord(t *testing.T) {
	service, db := setupTunnelTestService(t, func(_ context.Context, _ uint, _ string) (string, error) {
		return "", errors.New("node offline")
	})
	tunnel := validTunnelConfig().toModel(1)
	tunnel.Status = providerModel.IPv6TunnelStatusInactive
	if err := db.Create(&tunnel).Error; err != nil {
		t.Fatalf("create tunnel: %v", err)
	}
	if err := service.Delete(context.Background(), 1, tunnel.ID); err == nil {
		t.Fatal("expected delete error")
	}
	var count int64
	if err := db.Model(&providerModel.ProviderIPv6Tunnel{}).Where("id = ?", tunnel.ID).Count(&count).Error; err != nil {
		t.Fatalf("count tunnel: %v", err)
	}
	if count != 1 {
		t.Fatalf("record count = %d, want 1", count)
	}
}

func TestCleanupProviderRemoteBatchesAllTunnels(t *testing.T) {
	var calls atomic.Int32
	var command string
	service, db := setupTunnelTestService(t, func(_ context.Context, providerID uint, remoteCommand string) (string, error) {
		calls.Add(1)
		if providerID != 1 {
			t.Fatalf("providerID = %d", providerID)
		}
		command = remoteCommand
		return "deleted\n", nil
	})

	first := validTunnelConfig().toModel(1)
	secondConfig := validTunnelConfig()
	secondConfig.Name = "Backup tunnel"
	secondConfig.Interface = "he-backup"
	second := secondConfig.toModel(1)
	if err := db.Create(&first).Error; err != nil {
		t.Fatalf("create first tunnel: %v", err)
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatalf("create second tunnel: %v", err)
	}

	if err := service.CleanupProviderRemote(context.Background(), 1, false); err != nil {
		t.Fatalf("CleanupProviderRemote() error = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("remote calls = %d, want 1", calls.Load())
	}
	for _, tunnel := range []providerModel.ProviderIPv6Tunnel{first, second} {
		if !strings.Contains(command, unitName(tunnel.ID)) || !strings.Contains(command, tunnel.Interface) {
			t.Fatalf("cleanup command does not include tunnel %#v: %s", tunnel, command)
		}
	}
	var count int64
	if err := db.Model(&providerModel.ProviderIPv6Tunnel{}).Where("provider_id = ?", 1).Count(&count).Error; err != nil {
		t.Fatalf("count tunnels: %v", err)
	}
	if count != 2 {
		t.Fatalf("controller rows = %d, want 2 for caller transaction", count)
	}
}

func TestCleanupProviderRemoteForceModeSkipsHost(t *testing.T) {
	var calls atomic.Int32
	service, db := setupTunnelTestService(t, func(_ context.Context, _ uint, _ string) (string, error) {
		calls.Add(1)
		return "", errors.New("remote executor must not run")
	})
	tunnel := validTunnelConfig().toModel(1)
	if err := db.Create(&tunnel).Error; err != nil {
		t.Fatalf("create tunnel: %v", err)
	}

	if err := service.CleanupProviderRemote(context.Background(), 1, true); err != nil {
		t.Fatalf("CleanupProviderRemote(force) error = %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("remote calls = %d, want 0", calls.Load())
	}
}
