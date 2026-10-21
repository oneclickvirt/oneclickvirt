package ipv6tunnel

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Exercise the kernel routing decision, including routed NIC /128 routes.
// Run explicitly on a disposable Linux node with CAP_NET_ADMIN.
func TestRoutedPolicyLocalDestinationsLinux(t *testing.T) {
	if os.Getenv("ONECLICKVIRT_TEST_NETNS") != "1" {
		t.Skip("set ONECLICKVIRT_TEST_NETNS=1 on a disposable Linux node")
	}
	if _, err := exec.LookPath("ip"); err != nil {
		t.Fatal(err)
	}
	ns := fmt.Sprintf("ocv6-policy-%d", os.Getpid())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := func(args ...string) string {
		t.Helper()
		out, err := exec.CommandContext(ctx, "ip", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("ip %v: %v: %s", args, err, out)
		}
		return string(out)
	}
	command("netns", "add", ns)
	t.Cleanup(func() { _ = exec.Command("ip", "netns", "del", ns).Run() })
	tunnel := validTunnelConfig().toModel(1)
	tunnel.ID = 42
	script := renderTunnelScript(tunnel)
	functions := ""
	for _, name := range []string{"ensure_routed_policy_route", "cleanup_routed_policy_route"} {
		start := strings.Index(script, name+"() {")
		if start < 0 {
			t.Fatalf("missing %s", name)
		}
		end := strings.Index(script[start:], "\n}\n")
		if end < 0 {
			t.Fatalf("unterminated %s", name)
		}
		functions += script[start:start+end+3] + "\n"
	}
	setup := `set -eu
ip link set lo up
for dev in bridge6 tunnel6 uplink6 routed6; do
  ip link add "$dev" type dummy
  ip link set "$dev" up
done
ip -6 addr add 2001:db8:42::1/120 dev bridge6 nodad
ip -6 addr add fd00:42::2/64 dev tunnel6 nodad
ip -6 addr add fd00:43::2/64 dev uplink6 nodad
ip -6 route add default via fd00:43::1 dev uplink6 onlink
ip -6 route add 2001:db8:42::3/128 dev routed6
sysctl -qw net.ipv6.conf.all.forwarding=1
BRIDGE=bridge6
IFACE=tunnel6
REMOTE6=fd00:42::1
ROUTED_CIDR=2001:db8:42::/120
ROUTE_METRIC=100
POLICY_TABLE=10042
POLICY_PRIORITY=30042
` + functions + `
ensure_routed_policy_route
ensure_routed_policy_route
ip -6 route get 2001:db8:42::2 from 2001:db8:42::1
ip -6 route get 2001:db8:42::3 from 2001:db8:42::1
ip -6 route get 2606:4700:4700::1111 from 2001:db8:42::2 iif bridge6
ip -6 route get 2606:4700:4700::1111 from fd00:43::2
cleanup_routed_policy_route
ip -6 route get 2606:4700:4700::1111 from 2001:db8:42::1
`
	out := command("netns", "exec", ns, "sh", "-ceu", setup)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var routes []string
	for _, line := range lines {
		if strings.Contains(line, " dev ") {
			routes = append(routes, line)
		}
	}
	want := []string{"bridge6", "routed6", "tunnel6", "uplink6", "uplink6"}
	if len(routes) != len(want) {
		t.Fatalf("unexpected route results: %s", out)
	}
	for i, device := range want {
		if !strings.Contains(routes[i]+" ", " dev "+device+" ") {
			t.Fatalf("route %d must use %s: %s", i, device, routes[i])
		}
	}
}
