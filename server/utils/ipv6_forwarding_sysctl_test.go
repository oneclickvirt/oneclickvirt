package utils

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIPv6ForwardingInterfacesProtectsDistinctDefaultRouteUplinks(t *testing.T) {
	routes := "\x1b[33m" + `[{"dst":"default","dev":"vmbr0"},{"dst":"default","nexthops":[{"dev":"eth0"},{"dev":"vmbr0"}]},{"dst":"default","multipath":[{"dev":"eth1"},{"dev":"vmbr0"}]}]` + "\x1b[0m"
	interfaces, err := IPv6ForwardingInterfaces(routes, "vmbr2")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"vmbr2", "vmbr0", "eth0", "eth1"}
	if strings.Join(interfaces, ",") != strings.Join(want, ",") {
		t.Fatalf("interfaces = %v, want %v", interfaces, want)
	}
	for _, invalid := range []string{"Adresse IPv6 ungültig", "adresse IPv6 invalide", "IPv6 地址无效", `[{"dst":"default"}]`, `[{"dst":"default","dev":"eth0;reboot"}]`} {
		if _, err := IPv6ForwardingInterfaces(invalid, "vmbr2"); err == nil {
			t.Fatalf("accepted unsafe default route %q", invalid)
		}
	}
}

func TestIPv6ForwardingInterfacesAcceptsOmittedDefaultRouteDestination(t *testing.T) {
	interfaces, err := IPv6ForwardingInterfaces(`[{"dev":"eth0"}]`, "vmbr0")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(interfaces, ",") != "vmbr0,eth0" {
		t.Fatalf("interfaces = %v, want vmbr0,eth0", interfaces)
	}
}

func TestIPv6ForwardingSysctlCommandSetsRouterAdvertisementsFirst(t *testing.T) {
	command, err := IPv6ForwardingSysctlCommand([]string{"vmbr2", "vmbr0"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Index(command, "accept_ra=2") > strings.Index(command, "all.forwarding=1") {
		t.Fatal("router advertisements were configured after forwarding")
	}
	for _, fragment := range []string{"for iface in 'vmbr2' 'vmbr0'", "/etc/sysctl.d/99-oneclickvirt-ipv6.conf", "net.ipv6.conf.all.proxy_ndp=1"} {
		if !strings.Contains(command, fragment) {
			t.Fatalf("command missing %q", fragment)
		}
	}
	path := filepath.Join(t.TempDir(), "sysctl.sh")
	if err := os.WriteFile(path, []byte(command), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("sh", "-n", path).CombinedOutput(); err != nil {
		t.Fatalf("invalid shell: %v: %s", err, output)
	}
	if _, err := IPv6ForwardingSysctlCommand([]string{"eth0;reboot"}); err == nil {
		t.Fatal("accepted unsafe interface name")
	}
}
