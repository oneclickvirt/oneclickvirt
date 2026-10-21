package qemu

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"oneclickvirt/utils"
)

func TestLXCRootfsBootstrapConfiguresMatchedInterfacesAndIPv6OnlyIsolation(t *testing.T) {
	for _, mode := range []string{"nat_ipv4", "nat_ipv4_ipv6", "ipv6_only"} {
		t.Run(mode, func(t *testing.T) {
			plan := qemuIPv6Plan{}
			if mode != "nat_ipv4" {
				var err error
				plan, err = resolveQEMUIPv6Plan(routedQEMUConfig(mode))
				if err != nil {
					t.Fatal(err)
				}
			}
			files := qemuLXCRootfsFiles(plan, "192.168.122.7", "52:54:00:00:00:01", "52:54:00:00:00:02")
			root := t.TempDir()
			log := filepath.Join(root, "ip.log")
			ip := `#!/bin/sh
if [ "$*" = '-o link show' ]; then
  printf '7: ens3@if9: <UP> link/ether 52:54:00:00:00:01\n8: enp1s0@if10: <UP> link/ether 52:54:00:00:00:02\n'
else
  printf '%s\n' "$*" >> "$IP_LOG"
fi
`
			if err := os.WriteFile(filepath.Join(root, "ip"), []byte(ip), 0700); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("sh", "-c", files["/usr/local/sbin/oneclickvirt-lxc-network"])
			cmd.Env = append(os.Environ(), "PATH="+root+":"+os.Getenv("PATH"), "IP_LOG="+log)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("bootstrap failed: %v: %s", err, out)
			}
			body, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			trace := string(body)
			if mode != "ipv6_only" && strings.Contains(trace, "address flush") {
				t.Fatalf("normal startup destructively flushed configured addresses: %s", trace)
			}
			if mode == "ipv6_only" {
				if strings.Contains(trace, "192.168") || !strings.Contains(trace, "-4 address flush dev enp1s0 scope global") {
					t.Fatalf("IPv6-only guest retained IPv4: %s", trace)
				}
				if strings.Contains(files["/etc/network/interfaces"], "eth1") || strings.Contains(files["/etc/resolv.conf"], "1.1.1.1") {
					t.Fatal("IPv6-only persistent interface or DNS is wrong")
				}
			} else if !strings.Contains(trace, "-4 address replace 192.168.122.7/24 dev ens3") || !strings.Contains(trace, "default via 192.168.122.1 dev ens3") {
				t.Fatalf("assigned IPv4 was not applied: %s", trace)
			}
			if mode != "nat_ipv4" && !strings.Contains(trace, "-6 address replace 2001:db8::2/126 dev enp1s0 nodad") {
				t.Fatalf("routed IPv6 was not applied to its MAC-matched interface: %s", trace)
			}
			unit := files["/etc/systemd/system/oneclickvirt-lxc-network.service"]
			if strings.Contains(files["/etc/network/interfaces"], "172.16.") || !strings.Contains(unit, "After=local-fs.target network.target") || strings.Contains(unit, "After=network-online.target") {
				t.Fatal("stale image network or a boot dependency cycle survived")
			}
		})
	}
}

type lxcRootfsShellExecutor struct{ utils.ShellExecutor }

func (e *lxcRootfsShellExecutor) Execute(command string) (string, error) {
	output, err := exec.Command("sh", "-c", command).CombinedOutput()
	return string(output), err
}

func (e *lxcRootfsShellExecutor) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}

func TestLXCRootfsPreventsSocketActivatedNetworkManagerRaces(t *testing.T) {
	root := filepath.Join(t.TempDir(), "fresh rootfs")
	for _, directory := range []string{"etc/network/interfaces.d", "etc/systemd/network", "etc/systemd/system"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0755); err != nil {
			t.Fatal(err)
		}
	}
	previous := filepath.Join(root, "etc/systemd/system/systemd-networkd.service")
	if err := os.WriteFile(previous, []byte("image override\n"), 0644); err != nil {
		t.Fatal(err)
	}
	p := &QEMUProvider{sshClient: utils.NewSafeShellExecutor(&lxcRootfsShellExecutor{})}
	for attempt := 0; attempt < 2; attempt++ {
		if err := p.configureLXCRootfs(root, qemuIPv6Plan{}, "192.168.122.7", "52:54:00:00:00:01", ""); err != nil {
			t.Fatal(err)
		}
		for _, unit := range []string{"systemd-networkd.service", "systemd-networkd.socket"} {
			target, err := os.Readlink(filepath.Join(root, "etc/systemd/system", unit))
			if err != nil || target != "/dev/null" {
				t.Fatalf("competing %s remains enabled: %q, %v", unit, target, err)
			}
		}
	}
	backup, err := os.ReadFile(filepath.Join(root, "etc/oneclickvirt/image-network-backup/systemd-networkd.service"))
	if err != nil || string(backup) != "image override\n" {
		t.Fatalf("image override was not preserved: %q, %v", backup, err)
	}
}
