package qemu

import (
	"context"
	"gopkg.in/yaml.v3"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloudInitPasswordSurvivesYAMLAndShell(t *testing.T) {
	for _, password := range []string{"a: b'\"$`\\!#", "Passw0rd!", "a';false;#", "a\nb\rc"} {
		var data struct {
			RunCmd []any `yaml:"runcmd"`
		}
		if err := yaml.Unmarshal([]byte(qemuCloudInitUserData("vm", password)), &data); err != nil {
			t.Fatal(err)
		}
		command, ok := data.RunCmd[1].(string)
		if !ok {
			t.Fatalf("password command parsed as %T", data.RunCmd[1])
		}
		dir := t.TempDir()
		output := filepath.Join(dir, "stdin")
		if err := os.WriteFile(filepath.Join(dir, "chpasswd"), []byte("#!/bin/sh\ncat > \"$AUDIT_STDIN\"\n"), 0700); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("sh", "-c", command)
		cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "AUDIT_STDIN="+output)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		got, err := os.ReadFile(output)
		if err != nil {
			t.Fatal(err)
		}
		want := "root:" + strings.NewReplacer("\r", "", "\n", "").Replace(password) + "\n"
		if string(got) != want {
			t.Fatalf("password changed in YAML/shell roundtrip")
		}
	}
}

func TestImageCapacityRejectsTruncatedBackingDisk(t *testing.T) {
	for _, tc := range []struct {
		info string
		size int
		ok   bool
	}{
		{`{"virtual-size":3221225472}`, 1, false},
		{`{"virtual-size":3221225472}`, 3, true},
		{`{"virtual-size":3221225472}`, 8, true},
		{`{}`, 8, false}, {`not-json`, 8, false},
	} {
		if err := qemuValidateImageCapacity(tc.info, tc.size); (err == nil) != tc.ok {
			t.Fatalf("info=%s size=%d: %v", tc.info, tc.size, err)
		}
	}
}

func TestNATCloudInitMatchesMACWithoutDependingOnInterfaceName(t *testing.T) {
	var data struct {
		Ethernets map[string]struct {
			Match struct {
				MAC string `yaml:"macaddress"`
			}
			DHCP4 bool `yaml:"dhcp4"`
			DHCP6 bool `yaml:"dhcp6"`
		}
	}
	if err := yaml.Unmarshal([]byte(qemuCloudInitNetworkData(qemuIPv6Plan{}, "52:54:00:00:00:01", "")), &data); err != nil {
		t.Fatal(err)
	}
	n, ok := data.Ethernets["oneclickvirt0"]
	if !ok || n.Match.MAC != "52:54:00:00:00:01" || !n.DHCP4 || n.DHCP6 {
		t.Fatalf("incorrect NAT-only network: %+v", data)
	}
}

func TestPasswordRejectsExtraChpasswdRecords(t *testing.T) {
	p := &QEMUProvider{}
	for _, password := range []string{"a\nb", "a\rb", "a\x00b"} {
		if err := p.sshSetPassword(context.Background(), "guest", password); err == nil {
			t.Fatal("invalid password accepted")
		}
	}
}

func TestIPv6OnlyCloudInitIncludesReachableDNS(t *testing.T) {
	plan, err := resolveQEMUIPv6Plan(routedQEMUConfig("ipv6_only"))
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		Ethernets map[string]struct {
			DNS   struct{ Addresses []string } `yaml:"nameservers"`
			DHCP4 bool                         `yaml:"dhcp4"`
		}
	}
	if err := yaml.Unmarshal([]byte(qemuCloudInitNetworkData(plan, "", "52:54:00:00:00:02")), &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Ethernets) != 1 || data.Ethernets["oneclickvirt6"].DHCP4 {
		t.Fatal("IPv4 leaked into IPv6-only config")
	}
	dns := data.Ethernets["oneclickvirt6"].DNS.Addresses
	if len(dns) < 2 {
		t.Fatal("IPv6-only guest has no DNS fallback")
	}
	for _, address := range dns {
		if !strings.Contains(address, ":") {
			t.Fatal("IPv4 DNS cannot serve IPv6-only guest")
		}
	}
}
