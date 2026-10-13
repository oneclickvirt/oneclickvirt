package proxmox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	coreprovider "oneclickvirt/provider"
)

func directIPv6Executor(addresses, state string) *ipv6CommandExecutor {
	return &ipv6CommandExecutor{
		fail: func(command string) error {
			if strings.HasPrefix(command, "[ -s") || strings.HasPrefix(command, "[ -f") {
				return errors.New("no legacy IPv6 state")
			}
			if strings.Contains(command, "systemctl is-active") {
				return errors.New("ndpresponder is not installed")
			}
			return nil
		},
		output: func(command string) string {
			switch {
			case strings.Contains(command, "pve_direct_ipv6_prefix"):
				return state
			case strings.Contains(command, "ip -j -6 addr show"):
				return "\x1b[32m" + addresses + "\x1b[0m"
			case strings.Contains(command, "ip -j -6 route show"):
				return `[{"dst":"default","dev":"vmbr0","gateway":"fe80::1"}]`
			default:
				return ""
			}
		},
	}
}

func TestProxmoxDirectPrefixUsesGuestBridgeInsteadOfHostUplink(t *testing.T) {
	setupProxmoxIPv6CommandTestDB(t)
	for _, prefix := range []int{38, 56, 64, 80, 118, 120} {
		t.Run(fmt.Sprintf("prefix_%d", prefix), func(t *testing.T) {
			addresses := fmt.Sprintf(`[{"ifname":"vmbr0","addr_info":[{"family":"inet6","local":"2a01:4f8:c014:1a63::2","prefixlen":128,"scope":"global"}]},{"ifname":"vmbr9","addr_info":[{"family":"inet6","local":"2a14:7c0:1002:10f8::1","prefixlen":%d,"scope":"global"}]}]`, prefix)
			state := fmt.Sprintf("2a14:7c0:1002:10f8::1/%d\t2a14:7c0:1002:10f8::1\trouted\tfe80::1\tvmbr9\tbridge\n", prefix)
			executor := directIPv6Executor(addresses, state)
			p := NewProxmoxProvider().(*ProxmoxProvider)
			p.bridgeDedicatedV6 = "vmbr2"
			p.sshClient.SetExecutor(executor)
			info, err := p.getIPv6Info(context.Background())
			if err != nil || info.Network.PrefixLen != prefix || info.DirectBridge != "vmbr9" || info.HostIPv6Address != "2a14:7c0:1002:10f8::1" {
				t.Fatalf("IPv6 information = %#v, %v", info, err)
			}
			mode, err := p.resolveProxmoxIPv6ModeForCreate(context.Background())
			if err != nil || mode.BridgeName != "vmbr9" {
				t.Fatalf("routed mode = %#v, %v", mode, err)
			}
			for _, command := range executor.commands {
				if strings.Contains(command, "systemctl is-active") || strings.Contains(command, "ip -6 addr add") || strings.Contains(command, "ip -6 addr del") {
					t.Fatalf("routed-prefix detection tried to mutate addresses or require NDP: %s", command)
				}
			}
			// /120 is valid for an explicitly selected address even though it
			// cannot accommodate the platform's complete automatic VMID range.
			if prefix == 120 {
				config := coreprovider.InstanceConfig{Metadata: map[string]string{"network_type": "ipv6_only", "static_ipv6": "2a14:7c0:1002:10f8::65"}}
				if _, err := p.resolveProxmoxIPv6ModeForConfig(context.Background(), config); err != nil {
					t.Fatalf("explicit /120 address rejected: %v", err)
				}
			}
		})
	}
}

func TestProxmoxLegacyDirectBridgeRetainsNDPRequirement(t *testing.T) {
	p := NewProxmoxProvider().(*ProxmoxProvider)
	p.bridgeDedicatedV6 = "vmbr2"
	executor := directIPv6Executor(`[{"ifname":"vmbr0","addr_info":[{"family":"inet6","local":"2a01:4f8:c014:1a63::2","prefixlen":64,"scope":"global"}]},{"ifname":"vmbr2","addr_info":[{"family":"inet6","local":"2a14:7c0:1002:10f8::1","prefixlen":38,"scope":"global"}]}]`, "")
	p.sshClient.SetExecutor(executor)
	info, err := p.getIPv6Info(context.Background())
	if err != nil || info.DirectBridge != "vmbr2" || info.Network.PrefixLen != 38 {
		t.Fatalf("legacy bridge information = %#v, %v", info, err)
	}
	if _, err := p.resolveProxmoxIPv6ModeForCreate(context.Background()); err == nil || !strings.Contains(err.Error(), "ndpresponder") {
		t.Fatalf("legacy NDP requirement disappeared: %v", err)
	}
}

func TestProxmoxDirectPrefixRejectsStaleStateAndHostOnlyDiscovery(t *testing.T) {
	addresses := `[{"ifname":"vmbr0","addr_info":[{"family":"inet6","local":"2a01:4f8:c014:1a63::2","prefixlen":64,"scope":"global"}]},{"ifname":"vmbr2","addr_info":[{"family":"inet6","local":"2a14:7c0:1002:10f8::1","prefixlen":64,"scope":"global"}]}]`
	p := NewProxmoxProvider().(*ProxmoxProvider)
	p.bridgeDedicatedV6 = "vmbr2"
	p.sshClient.SetExecutor(directIPv6Executor(addresses, "2a14:7c0:1002:10f8::/80\t2a14:7c0:1002:10f8::1\trouted\tfe80::1\tvmbr2\tbridge"))
	if _, err := p.getIPv6Info(context.Background()); err == nil {
		t.Fatal("stale saved prefix was accepted against a different live bridge prefix")
	}
	p.sshClient.SetExecutor(directIPv6Executor(`[ {"ifname":"vmbr0","addr_info":[{"family":"inet6","local":"2a01:4f8:c014:1a63::2","prefixlen":64,"scope":"global"}]}]`, ""))
	if _, err := p.resolveProxmoxIPv6ModeForCreate(context.Background()); err == nil {
		t.Fatal("uplink SLAAC/static prefix was allocated without a public guest bridge")
	}
}

func TestProxmoxDirectStateParserRejectsPollutedAndInvalidValues(t *testing.T) {
	valid := "2a14:7c0:1002:2000::/64\t2a14:7c0:1002:2000::1\trouted\tfe80::1\tvmbr9\tbridge"
	for _, output := range []string{valid, "\x1b[32m" + valid + "\x1b[0m\n"} {
		if state, err := parseProxmoxDirectIPv6State(output); err != nil || state.Bridge != "vmbr9" {
			t.Fatalf("valid state rejected: %#v, %v", state, err)
		}
	}
	legacy, err := parseProxmoxDirectIPv6State("2a14:7c0:1002:2000::/64\t2a14:7c0:1002:2000::1\t\t\t\t\n")
	if err != nil || legacy.Bridge != "vmbr2" || legacy.Mode != "ndp" || legacy.Transport != "bridge" {
		t.Fatalf("legacy state rejected: %#v, %v", legacy, err)
	}
	if _, err := parseProxmoxDirectIPv6State(strings.Replace(valid, "fe80::1", "fe80::1%eth0", 1)); err != nil {
		t.Fatalf("scoped link-local upstream gateway rejected: %v", err)
	}
	for _, output := range []string{
		"IPv6-Adresse ungültig", "préfixe non valide", "前缀无效",
		strings.Replace(valid, "/64", "/128", 1),
		strings.Replace(valid, "routed", "auto", 1),
		strings.Replace(valid, "vmbr9", "vmbr9; reboot", 1),
		strings.Replace(valid, "fe80::1", "127.0.0.1", 1),
		strings.Replace(valid, "bridge", "unknown", 1),
	} {
		if _, err := parseProxmoxDirectIPv6State(output); err == nil {
			t.Fatalf("invalid state accepted: %q", output)
		}
	}
}

func TestProxmoxDirectGuestAddressMustMatchBridgeAndAvoidGateway(t *testing.T) {
	for _, address := range []string{"2a14:7c0:1002:3000::2", "2a14:7c0:1002:2000::1"} {
		if _, _, err := proxmoxIPv6AddressSettings(coreprovider.InstanceConfig{}, address, "2a14:7c0:1002:2000::1", 64); err == nil {
			t.Fatalf("out-of-prefix or gateway-conflicting address accepted: %s", address)
		}
	}
}

func TestProxmoxDirectStateDoesNotOverrideThirdPartyBridge(t *testing.T) {
	p := NewProxmoxProvider().(*ProxmoxProvider)
	p.config.NodeInstallType = "third_party"
	p.bridgeDedicatedV6 = "vmbr7"
	executor := directIPv6Executor(`[{"ifname":"vmbr7","addr_info":[{"family":"inet6","local":"2a14:7c0:1002:7000::1","prefixlen":64,"scope":"global"}]},{"ifname":"vmbr9","addr_info":[{"family":"inet6","local":"2a14:7c0:1002:9000::1","prefixlen":64,"scope":"global"}]}]`, "2a14:7c0:1002:9000::/64\t2a14:7c0:1002:9000::1\trouted\tfe80::1\tvmbr9\tbridge")
	p.sshClient.SetExecutor(executor)
	info, err := p.getIPv6Info(context.Background())
	if err != nil || info.DirectBridge != "vmbr7" || info.HostIPv6Address != "2a14:7c0:1002:7000::1" || info.DirectMode != "ndp" {
		t.Fatalf("third-party bridge was overridden: %#v, %v", info, err)
	}
}

func TestProxmoxHETransportKeepsNDPRequirement(t *testing.T) {
	p := NewProxmoxProvider().(*ProxmoxProvider)
	p.bridgeDedicatedV6 = "vmbr2"
	executor := directIPv6Executor(`[{"ifname":"vmbr2","addr_info":[{"family":"inet6","local":"2001:470:1234:100::1","prefixlen":64,"scope":"global"}]}]`, "2001:470:1234:100::/64\t2001:470:1234:100::1\trouted\t2001:470:1234::1\tvmbr2\ttunnel")
	p.sshClient.SetExecutor(executor)
	if _, err := p.resolveProxmoxIPv6ModeForCreate(context.Background()); err == nil || !strings.Contains(err.Error(), "ndpresponder") {
		t.Fatalf("HE/tunnel responder requirement changed: %v", err)
	}
}

func TestProxmoxAppendedIPv6PoolIgnoresUnusedDirectState(t *testing.T) {
	p := NewProxmoxProvider().(*ProxmoxProvider)
	executor := directIPv6Executor(`[{"ifname":"vmbr0","addr_info":[{"family":"inet6","local":"2a01:4f8:c014:1a63::2","prefixlen":128,"scope":"global"}]}]`, "invalid unused direct state")
	executor.fail = func(command string) error {
		if strings.HasPrefix(command, "[ -f") {
			return errors.New("no legacy state")
		}
		return nil
	}
	p.sshClient.SetExecutor(executor)
	info, err := p.getIPv6Info(context.Background())
	if err != nil || !info.HasAppendedAddresses {
		t.Fatalf("unused state disrupted an appended pool: %#v, %v", info, err)
	}
	for _, command := range executor.commands {
		if strings.Contains(command, "pve_direct_ipv6_prefix") {
			t.Fatal("unrelated direct state was read for an appended pool")
		}
	}
}
