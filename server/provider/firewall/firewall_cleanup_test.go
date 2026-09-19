package firewall

import (
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"oneclickvirt/utils"
)

type cleanupExecutor struct {
	utils.ShellExecutor
	run func(string) (string, error)
}

func (e *cleanupExecutor) Execute(command string) (string, error) { return e.run(command) }
func (e *cleanupExecutor) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	return e.run(command)
}

func TestCleanupRuleQuotingIsLiteral(t *testing.T) {
	for _, value := range []string{"plain", "owner with spaces", "quote\"and'single", "back\\slash", "$(printf injected); `printf injected`", "", "-j"} {
		t.Run(value, func(t *testing.T) {
			input := "-A PREROUTING -m comment --comment " + shellQuote(value) + " -j ACCEPT"
			args, err := splitXTablesRule(input)
			if err != nil || len(args) != 8 || args[5] != value {
				t.Fatalf("decode %q = %v, %v", input, args, err)
			}
			rule := cleanupRule{tool: "iptables", table: "nat", args: args}
			// The local shell prints the arguments; no firewall command runs.
			output, err := exec.Command("sh", "-c", "iptables() { printf '%s\\n' \"$@\"; }; "+rule.deleteCommand()).CombinedOutput()
			want := strings.Join(append([]string{"-w", "5", "-t", "nat", "-D"}, args[1:]...), "\n") + "\n"
			if err != nil || string(output) != want {
				t.Fatalf("shell changed literal arguments: %q, error %v, want %q", output, err, want)
			}
		})
	}
	for _, input := range []string{"'unfinished", "\"unfinished", "trailing\\"} {
		if _, err := splitXTablesRule(input); err == nil {
			t.Fatalf("accepted malformed quoting: %q", input)
		}
	}
}

func TestCleanupNftReadAndDeleteFailuresAreNotSuccess(t *testing.T) {
	const snapshot = `{"nftables":[{"rule":{"family":"ip","table":"incus","chain":"prerouting","handle":11,"comment":"owner","expr":[{"match":{"op":"==","left":{"payload":{"protocol":"tcp","field":"dport"}},"right":22000}},{"dnat":{"addr":"192.0.2.10","port":22}}]}}]}`
	for _, scenario := range []string{"read error", "invalid JSON", "missing ruleset", "delete error", "concurrent deletion", "delete reports success but rule remains"} {
		t.Run(scenario, func(t *testing.T) {
			deleted, deletes := false, 0
			e := &cleanupExecutor{run: func(command string) (string, error) {
				switch {
				case strings.HasPrefix(command, "command -v nft"):
					return "", nil
				case strings.HasPrefix(command, "command -v iptables"):
					return "", errors.New("not installed")
				case command == "nft -j -a list ruleset":
					if scenario == "read error" {
						return "permission denied", errors.New("permission denied")
					}
					if scenario == "invalid JSON" {
						return "not JSON", nil
					}
					if scenario == "missing ruleset" {
						return `{}`, nil
					}
					if deleted {
						return `{"nftables":[]}`, nil
					}
					return snapshot, nil
				case strings.HasPrefix(command, "nft delete rule"):
					deletes++
					if scenario == "concurrent deletion" {
						deleted = true
					}
					if scenario == "delete reports success but rule remains" {
						return "", nil
					}
					return "delete failed", errors.New("delete failed")
				default:
					return "", fmt.Errorf("unexpected command %s", command)
				}
			}}
			m := NewManager(e, "incus", "")
			m.backend = BackendNft
			err := m.RemoveSingleDNATForFamily("", 22000, 22, "tcp", "owner", false)
			if (err == nil) != (scenario == "concurrent deletion") {
				t.Fatalf("error = %v", err)
			}
			if strings.Contains(scenario, "read") || scenario == "invalid JSON" || scenario == "missing ruleset" {
				if deletes != 0 {
					t.Fatal("mutated rules after invalid read")
				}
			}
		})
	}
}

func TestCleanupLegacyRulesPreserveOtherOwnersAndSharedSupport(t *testing.T) {
	nat := []string{
		`-A PREROUTING -p tcp --dport 22000 -j DNAT --to-destination 192.0.2.10:22`,
		`-A PREROUTING -p tcp --dport 22000 -j DNAT --to-destination 192.0.2.10:22`,
		`-A PREROUTING -p tcp --dport 22001 -m comment --comment "other" -j DNAT --to-destination 192.0.2.10:22`,
		`-A POSTROUTING -s 192.0.2.10/32 -p tcp --sport 22 -j MASQUERADE`,
		`-A PREROUTING -p tcp --dport 22000 -m comment --comment "other" -j DNAT --to-destination 192.0.2.99:22`,
	}
	filter := []string{
		`-A FORWARD -d 192.0.2.10/32 -p tcp --dport 22 -j ACCEPT`,
		`-A FORWARD ! -d 192.0.2.10/32 -p tcp --dport 22 -j ACCEPT`,
		`-A FORWARD -d 192.0.2.0/24 -p tcp --dport 22 -j ACCEPT`,
	}
	e := &cleanupExecutor{run: func(command string) (string, error) {
		if strings.HasPrefix(command, "command -v nft") {
			return "", errors.New("not installed")
		}
		if strings.HasPrefix(command, "command -v iptables") {
			return "", nil
		}
		if command == "iptables -w 5 -t nat -S" {
			return strings.Join(nat, "\n"), nil
		}
		if command == "iptables -w 5 -t filter -S" {
			return strings.Join(filter, "\n"), nil
		}
		for index, line := range nat {
			args, err := splitXTablesRule(line)
			if err != nil {
				t.Fatal(err)
			}
			if (cleanupRule{tool: "iptables", table: "nat", args: args}).deleteCommand() == command {
				nat = append(nat[:index], nat[index+1:]...)
				return "", nil
			}
		}
		return "", fmt.Errorf("unexpected mutation: %s", command)
	}}
	m := NewManager(e, "incus", "")
	m.backend = BackendIptables
	if err := m.RemoveSingleDNAT("192.0.2.10", 22000, 22, "tcp", "owner"); err != nil {
		t.Fatal(err)
	}
	if len(nat) != 3 || len(filter) != 3 {
		t.Fatalf("shared or unrelated rules deleted: %v %v", nat, filter)
	}
	if err := m.RemoveSingleDNAT("192.0.2.10", 22000, 22, "tcp", "owner"); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

func TestCleanupRejectsMissingOwnershipBeforeMutation(t *testing.T) {
	for _, scenario := range []string{"no owner", "wrong family", "invalid protocol", "invalid port", "ambiguous legacy"} {
		t.Run(scenario, func(t *testing.T) {
			calls := []string{}
			e := &cleanupExecutor{run: func(command string) (string, error) {
				calls = append(calls, command)
				if strings.HasPrefix(command, "command -v nft") {
					return "", errors.New("not installed")
				}
				if strings.HasPrefix(command, "command -v iptables") {
					return "", nil
				}
				if command == "iptables -w 5 -t nat -S" {
					return "-A PREROUTING -p tcp --dport 22000 -j DNAT --to-destination 192.0.2.10:22", nil
				}
				if command == "iptables -w 5 -t filter -S" {
					return "", nil
				}
				t.Fatalf("unexpected mutation: %s", command)
				return "", nil
			}}
			m := NewManager(e, "incus", "")
			m.backend = BackendIptables
			address, protocol, owner, hostPort := "", "tcp", "owner", 22000
			switch scenario {
			case "no owner":
				owner = ""
			case "wrong family":
				address = "2001:db8::10"
			case "invalid protocol":
				protocol = "tcp; false"
			case "invalid port":
				hostPort = 65536
			}
			if err := m.RemoveSingleDNATForFamily(address, hostPort, 22, protocol, owner, false); err == nil {
				t.Fatal("unsafe deletion accepted")
			}
			if scenario != "ambiguous legacy" && len(calls) != 0 {
				t.Fatalf("validation sent remote commands: %v", calls)
			}
		})
	}
}

func TestCleanupHostRequiresExactAddress(t *testing.T) {
	values := []interface{}{"192.0.2.10/32", "2001:db8::10/128", "192.0.2.0/24", "2001:db8::/64", map[string]interface{}{"prefix": map[string]interface{}{"addr": "192.0.2.0", "len": float64(24)}}}
	want := []string{"192.0.2.10", "2001:db8::10", "", "", ""}
	got := []string{}
	for _, value := range values {
		got = append(got, cleanupHost(value))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("addresses = %v", got)
	}
}

func TestLegacyPersistenceDoesNotEnableUnusedNftService(t *testing.T) {
	e := &cleanupExecutor{run: func(command string) (string, error) {
		switch {
		case command == persistenceDistributionCommand:
			return "debian\n\ncomplete\n", nil
		case strings.HasPrefix(command, "for tool in"):
			return "nft\niptables\ncomplete\n", nil
		case command == "nft -j list tables":
			return `{"nftables":[]}`, nil
		case command == "iptables-save":
			return "*nat\nCOMMIT\n", nil
		case strings.HasPrefix(command, "set -e\numask 077"):
			return "", nil
		case strings.Contains(command, "systemctl enable nftables"), strings.Contains(command, "/etc/nftables.conf"):
			t.Fatalf("saving legacy rules enabled an unrelated nft service: %s", command)
		}
		return "", nil
	}}
	m := NewManager(e, "qemu", "")
	m.backend, m.detected = BackendIptables, true
	if err := m.SaveRules(); err != nil {
		t.Fatal(err)
	}
}
