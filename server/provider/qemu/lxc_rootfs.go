package qemu

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strings"

	"oneclickvirt/utils"
)

// Rootfs archives can retain the image builder's static network. Configure
// both supported guest network managers before boot; libvirt-lxc does not
// consume cloud-init and its namespace helper can fail on cgroup v2 hosts.
func qemuLXCRootfsFiles(plan qemuIPv6Plan, address4, mac4, mac6 string) map[string]string {
	interfaces := "auto lo\niface lo inet loopback\n"
	bootstrap := "#!/bin/sh\nset -eu\nfind_interface() {\n  wanted=$1\n  attempt=0\n  while [ \"$attempt\" -lt 30 ]; do\n    iface=$(ip -o link show | awk -v mac=\"$wanted\" 'index(tolower($0), tolower(mac)) { split($2,a,\"@\"); sub(/:$/,\"\",a[1]); print a[1]; exit }')\n    if [ -n \"$iface\" ]; then printf '%s\\n' \"$iface\"; return 0; fi\n    attempt=$((attempt+1)); sleep 1\n  done\n  echo 'managed LXC network interface did not appear' >&2; return 1\n}\ndisable_ra() {\n  for flag in accept_ra autoconf; do\n    path=\"/proc/sys/net/ipv6/conf/$1/$flag\"\n    if [ -e \"$path\" ]; then printf '0\\n' > \"$path\"; fi\n  done\n  ip -6 route flush dev \"$1\" proto ra\n}\n"
	files := map[string]string{}
	dns := "nameserver 1.1.1.1\nnameserver 8.8.8.8\n"
	if !plan.IPv6Only {
		interfaces += fmt.Sprintf("\nauto eth0\niface eth0 inet static\n    address %s/24\n    gateway 192.168.122.1\n", address4)
		files["/etc/systemd/network/00-oneclickvirt4.network"] = fmt.Sprintf("[Match]\nMACAddress=%s\n\n[Network]\nDHCP=no\nIPv6AcceptRA=no\nAddress=%s/24\nGateway=192.168.122.1\nDNS=1.1.1.1\nDNS=8.8.8.8\n", mac4, address4)
		bootstrap += fmt.Sprintf("iface=$(find_interface %s)\ndisable_ra \"$iface\"\nip link set dev \"$iface\" up\nip -4 address replace %s/24 dev \"$iface\"\nip -4 route replace default via 192.168.122.1 dev \"$iface\"\n", shellSingleQuote(mac4), shellSingleQuote(address4))
	}
	if plan.Routed != nil {
		iface := "eth1"
		if plan.IPv6Only {
			iface = "eth0"
			dns = "nameserver 2606:4700:4700::1111\nnameserver 2001:4860:4860::8888\n"
		}
		interfaces += fmt.Sprintf("\nauto %s\niface %s inet6 static\n    address %s\n    netmask %d\n    gateway %s\n    accept_ra 0\n", iface, iface, plan.Routed.Address, plan.Routed.Prefix, plan.Routed.Gateway)
		files["/etc/systemd/network/00-oneclickvirt6.network"] = fmt.Sprintf("[Match]\nMACAddress=%s\n\n[Network]\nDHCP=no\nIPv6AcceptRA=no\nAddress=%s\nDNS=2606:4700:4700::1111\nDNS=2001:4860:4860::8888\n\n[Route]\nDestination=::/0\nGateway=%s\nGatewayOnLink=yes\n", mac6, plan.Routed.AddressCIDR(), plan.Routed.Gateway)
		bootstrap += fmt.Sprintf("iface=$(find_interface %s)\ndisable_ra \"$iface\"\nip link set dev \"$iface\" up\n", shellSingleQuote(mac6))
		if plan.IPv6Only {
			bootstrap += "ip -4 address flush dev \"$iface\" scope global\n"
		}
		bootstrap += fmt.Sprintf("ip -6 address replace %s dev \"$iface\" nodad\nip -6 route replace default via %s dev \"$iface\" metric 100 onlink\n", shellSingleQuote(plan.Routed.AddressCIDR()), shellSingleQuote(plan.Routed.Gateway))
	}
	files["/etc/network/interfaces"] = interfaces
	files["/etc/resolv.conf"] = dns
	files["/usr/local/sbin/oneclickvirt-lxc-network"] = bootstrap
	files["/etc/systemd/system/oneclickvirt-lxc-network.service"] = `[Unit]
Description=OneClickVirt LXC network
DefaultDependencies=no
After=local-fs.target network.target networking.service
Before=network-online.target ssh.service sshd.service
Wants=network.target

[Service]
Type=oneshot
ExecStart=/usr/local/sbin/oneclickvirt-lxc-network
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
`
	return files
}

func (p *QEMUProvider) configureLXCRootfs(rootfs string, plan qemuIPv6Plan, address4, mac4, mac6 string) error {
	files := qemuLXCRootfsFiles(plan, address4, mac4, mac6)
	var command strings.Builder
	command.WriteString("set -eu\nroot=" + shellSingleQuote(rootfs) + "\n")
	// All changes are confined to a freshly extracted instance rootfs. Preserve
	// image defaults for diagnosis, without loading their stale build addresses.
	command.WriteString(`backup="$root/etc/oneclickvirt/image-network-backup"
mkdir -p "$backup" "$root/etc/systemd/system/multi-user.target.wants" "$root/usr/local/sbin" "$root/etc/ssh"
for path in etc/network/interfaces etc/network/interfaces.d etc/systemd/network; do
  if [ -e "$root/$path" ] && [ ! -e "$backup/$(basename "$path")" ]; then mv "$root/$path" "$backup/$(basename "$path")"; fi
done
mkdir -p "$root/etc/network/interfaces.d" "$root/etc/systemd/network"
# PVE rootfs images can socket-activate networkd even when it is disabled.
# It races with ifupdown and removes the default route after a duplicate
# address update. The generated ifupdown config and ordered fallback own
# these freshly created interfaces; preserve and mask the competing daemon.
for unit in systemd-networkd.service systemd-networkd.socket; do
  if [ -e "$root/etc/systemd/system/$unit" ] || [ -L "$root/etc/systemd/system/$unit" ]; then
    if [ ! -e "$backup/$unit" ] && [ ! -L "$backup/$unit" ]; then
      mv "$root/etc/systemd/system/$unit" "$backup/$unit"
    fi
  fi
  ln -sf /dev/null "$root/etc/systemd/system/$unit"
done
rm -f "$root/etc/resolv.conf"
`)
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		mode := "0644"
		if strings.Contains(path, "/sbin/") {
			mode = "0755"
		}
		fmt.Fprintf(&command, "printf '%%s' %s | base64 -d > %s; chmod %s %s\n", shellSingleQuote(base64.StdEncoding.EncodeToString([]byte(files[path]))), shellSingleQuote(rootfs+path), mode, shellSingleQuote(rootfs+path))
	}
	command.WriteString(`ln -sf ../oneclickvirt-lxc-network.service "$root/etc/systemd/system/multi-user.target.wants/oneclickvirt-lxc-network.service"
if [ -f "$root/etc/ssh/sshd_config" ]; then
  { printf 'PermitRootLogin yes\nPasswordAuthentication yes\n'; cat "$root/etc/ssh/sshd_config"; } > "$root/etc/ssh/sshd_config.oneclickvirt"
  mv "$root/etc/ssh/sshd_config.oneclickvirt" "$root/etc/ssh/sshd_config"
fi
`)
	if output, err := p.sshClient.Execute(command.String()); err != nil {
		return fmt.Errorf("配置libvirt-lxc首次启动网络和SSH失败: %s: %w", utils.TruncateString(strings.TrimSpace(output), 1000), err)
	}
	return nil
}
