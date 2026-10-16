package proxmox

import (
	"fmt"
	"strings"
	"testing"
)

func TestVMDNSUsesReachableAddressFamiliesAndPropagatesFailure(t *testing.T) {
	for _, network := range []string{"nat_ipv4", "dedicated_ipv4", "nat_ipv4_ipv6", "dedicated_ipv4_ipv6", "ipv6_only"} {
		for _, failure := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/failure=%t", network, failure), func(t *testing.T) {
				executor := &ipv6CommandExecutor{fail: func(string) error {
					if failure {
						return fmt.Errorf("DNS rejected")
					}
					return nil
				}}
				p := NewProxmoxProvider().(*ProxmoxProvider)
				p.sshClient.SetExecutor(executor)
				if err := p.configureVMDNS(108, network); (err != nil) != failure {
					t.Fatalf("DNS failure=%t: %v", failure, err)
				}
				if len(executor.commands) != 1 {
					t.Fatalf("unexpected commands: %v", executor.commands)
				}
				command := executor.commands[0]
				if strings.Contains(command, "1.1.1.1") != (network != "ipv6_only") ||
					strings.Contains(command, "2606:4700:4700::1111") != hasProxmoxIPv6(network) {
					t.Fatalf("unreachable DNS address family: %s", command)
				}
			})
		}
	}
}

func TestContainerDNSReplacesIgnoredTemplateResolverAndPropagatesErrors(t *testing.T) {
	for _, failure := range []string{"", "pct set", "pct exec"} {
		t.Run(failure, func(t *testing.T) {
			executor := &ipv6CommandExecutor{fail: func(command string) error {
				if failure != "" && strings.HasPrefix(command, failure) {
					return fmt.Errorf("resolver update rejected")
				}
				return nil
			}}
			p := NewProxmoxProvider().(*ProxmoxProvider)
			p.sshClient.SetExecutor(executor)
			if err := p.configureContainerDNS(201, "ipv6_only"); (err != nil) != (failure != "") {
				t.Fatalf("failure %q: %v", failure, err)
			}
			joined := strings.Join(executor.commands, "\n")
			if strings.Contains(joined, "8.8.8.8") || strings.Contains(joined, "1.1.1.1") {
				t.Fatalf("IPv4 resolver leaked into IPv6-only DNS: %s", joined)
			}
			if failure == "" && (!strings.Contains(joined, "> /etc/resolv.conf") || strings.Contains(joined, ">> /etc/resolv.conf")) {
				t.Fatalf("legacy resolver was not replaced: %s", joined)
			}
		})
	}
}
