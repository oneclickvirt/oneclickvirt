package provider

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDedicatedQEMUResolvesStaticLXCAndVMReservationsByDomainMAC(t *testing.T) {
	start := strings.Index(dedicatedIPv4Helper, "resolve_guest() {")
	if start < 0 {
		t.Fatal("resolver missing")
	}
	end := strings.Index(dedicatedIPv4Helper[start:], "remove_rule() {")
	if end < 0 {
		t.Fatal("resolver boundary missing")
	}
	for _, test := range []struct {
		kind, mac string
		want      bool
	}{
		{"lxc", "52:54:00:01:02:03", true},
		{"vm", "52:54:00:01:02:03", true},
		{"lxc", "52:54:00:04:05:06", false},
	} {
		t.Run(test.kind+"-"+test.mac, func(t *testing.T) {
			root := t.TempDir()
			stub := `#!/bin/sh
case "$*" in
  '-c lxc:/// dominfo guest') [ "$FIXTURE_KIND" = lxc ] ;;
  *' domstate guest') printf 'running\n' ;;
  *' domifaddr guest '*) printf ' Name MAC address Protocol Address\n' ;;
  *' dumpxml guest') printf '<domain><name>guest</name><devices><interface type="network"><mac address="52:54:00:01:02:03"/><source network="default"/></interface></devices></domain>\n' ;;
  '-c qemu:///system net-dumpxml default') printf '<network><ip><dhcp><host name="guest" mac="%s" ip="192.168.122.7"/></dhcp></ip></network>\n' "$FIXTURE_MAC" ;;
  *) exit 1 ;;
esac
`
			if err := os.WriteFile(filepath.Join(root, "virsh"), []byte(stub), 0700); err != nil {
				t.Fatal(err)
			}
			command := "set -euo pipefail\nruntime=qemu\ninstance=guest\n" + dedicatedIPv4Helper[start:start+end] + "\nresolve_guest\n"
			cmd := exec.Command("bash", "-c", command)
			cmd.Env = append(os.Environ(), "PATH="+root+":"+os.Getenv("PATH"), "FIXTURE_KIND="+test.kind, "FIXTURE_MAC="+test.mac)
			out, err := cmd.CombinedOutput()
			if test.want {
				if err != nil || strings.TrimSpace(string(out)) != "192.168.122.7" {
					t.Fatalf("static guest resolution failed: %v: %s", err, out)
				}
			} else if err == nil || len(out) != 0 {
				t.Fatalf("another guest's reservation was accepted: %v: %s", err, out)
			}
		})
	}
}
