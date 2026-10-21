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

func TestIPv6ForwardingPersistsNetworkdAndOrdersRuntimeAutostart(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"bin", "run/systemd/system", "proc/sys/net/ipv6/conf/eth0"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"accept_ra", "proxy_ndp"} {
		if err := os.WriteFile(filepath.Join(root, "proc/sys/net/ipv6/conf/eth0", name), []byte("0\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	stubs := map[string]string{
		"networkctl": `case "$1" in
status) echo '    Network File: /run/systemd/network/10-netplan-eth0.network' ;;
reload) echo 0 > "$TEST_ROOT/proxy-state" ;;
*) exit 1 ;;
esac`,
		"sysctl": `case "$1" in
-w) case "$2" in net.ipv6.conf.eth0.proxy_ndp=1) echo 1 > "$TEST_ROOT/proxy-state" ;; esac ;;
-p) grep -qx 'net.ipv6.conf.eth0.proxy_ndp=1' "$2" || exit 1; echo 1 > "$TEST_ROOT/proxy-state" ;;
*) exit 1 ;;
esac`,
		"systemctl": `case "$1" in
is-active|daemon-reload|enable) exit 0 ;;
show) echo loaded ;;
start) sysctl -p "$TEST_ROOT/etc/sysctl.d/99-oneclickvirt-ipv6.conf" ;;
*) exit 1 ;;
esac`,
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(root, "bin", name), []byte("#!/bin/sh\nset -eu\n"+body+"\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	command, err := IPv6ForwardingSysctlCommand([]string{"eth0"})
	if err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"/etc/", "/proc/", "/run/"} {
		command = strings.ReplaceAll(command, prefix, root+prefix)
	}
	cmd := exec.Command("sh", "-c", command)
	cmd.Env = append(os.Environ(), "PATH="+filepath.Join(root, "bin")+":"+os.Getenv("PATH"), "TEST_ROOT="+root)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rendered setup failed: %v: %s", err, output)
	}
	if strings.Contains(command, "systemctl restart") {
		t.Fatal("updating network readiness must not restart dependent runtime daemons")
	}
	checks := map[string][]string{
		"etc/systemd/network/10-netplan-eth0.network.d/99-oneclickvirt-ipv6.conf": {"[Network]\nIPv6ProxyNDP=yes\n"},
		"etc/systemd/system/oneclickvirt-ipv6-routing.service":                    {"After=network-online.target", "Before=incus.service", "ExecStart="},
		"etc/systemd/system/snap.lxd.daemon.service.d/99-oneclickvirt-ipv6.conf":  {"Requires=oneclickvirt-ipv6-routing.service", "After=oneclickvirt-ipv6-routing.service"},
		"etc/systemd/system/incus.service.d/99-oneclickvirt-ipv6.conf":            {"Requires=oneclickvirt-ipv6-routing.service"},
		"etc/sysctl.d/99-oneclickvirt-ipv6.conf":                                  {"net.ipv6.conf.default.proxy_ndp=1\n"},
		"proxy-state":                                                             {"1\n"},
	}
	for name, fragments := range checks {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, fragment := range fragments {
			if !strings.Contains(string(data), fragment) {
				t.Errorf("%s missing %q", name, fragment)
			}
		}
	}
}
