package incus

import (
	"context"
	"fmt"
	"net"
	"oneclickvirt/utils"
	"time"
)

// A routed NIC is configured automatically for containers, but VM guests need
// their own persistent address and route. Match its MAC rather than assuming
// the guest uses the host-side device name eth1.
func (i *IncusProvider) configureVMRoutedIPv6(ctx context.Context, name, address string) error {
	ip := net.ParseIP(address)
	if ip == nil || ip.To4() != nil {
		return fmt.Errorf("invalid VM routed IPv6 address")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	guest := `set -eu
mac="$1"
address="$2"
command -v systemctl >/dev/null
for path in /usr/local/sbin/oneclickvirt-routed-ipv6 /etc/systemd/system/oneclickvirt-routed-ipv6.service; do
  test ! -L "$path"
  test ! -e "$path" || test -f "$path"
done
script=$(mktemp /usr/local/sbin/.ocv-routed-ipv6.XXXXXX)
trap 'rm -f "$script"' EXIT
printf '%s\n' '#!/bin/sh' 'set -eu' "TARGET_MAC='$mac'" "TARGET_ADDRESS='$address'" > "$script"
cat >> "$script" <<'GUEST'
iface=''
for path in /sys/class/net/*; do
  test -r "$path/address" || continue
  mac=$(tr '[:upper:]' '[:lower:]' < "$path/address")
  if test "$mac" = "$TARGET_MAC"; then iface=${path##*/}; break; fi
done
test -n "$iface"
ip link set dev "$iface" up
ip -6 addr replace "$TARGET_ADDRESS/128" dev "$iface"
ip -6 route replace default via fe80::1 dev "$iface" metric 50
GUEST
chmod 0755 "$script"
mv -f "$script" /usr/local/sbin/oneclickvirt-routed-ipv6
cat > /etc/systemd/system/oneclickvirt-routed-ipv6.service <<'UNIT'
[Unit]
Description=OneClickVirt routed IPv6 guest address
DefaultDependencies=no
After=local-fs.target network.target
Before=network-online.target shutdown.target
Conflicts=shutdown.target
[Service]
Type=oneshot
ExecStart=/usr/local/sbin/oneclickvirt-routed-ipv6
RemainAfterExit=yes
Restart=on-failure
RestartSec=3
[Install]
WantedBy=multi-user.target
UNIT
# A manually configured NIC is unmanaged to networkd. In an IPv6-only VM
# with the original IPv4 NIC removed, wait-online otherwise waits until its
# timeout and delays SSH. Give the routed NIC its own persistent MAC match.
if command -v networkctl >/dev/null 2>&1 && command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet systemd-networkd.service; then
  mkdir -p /etc/systemd/network
  network_file="/etc/systemd/network/00-oneclickvirt-routed-$(printf '%s' "$mac" | tr -d ':').network"
  test ! -L "$network_file"
  cat > "$network_file.tmp.$$" <<NETWORK
[Match]
MACAddress=$mac
[Link]
RequiredForOnline=yes
[Network]
DHCP=no
IPv6AcceptRA=no
LinkLocalAddressing=ipv6
Address=$address/128
[Route]
Destination=::/0
Gateway=fe80::1
GatewayOnLink=yes
Metric=50
NETWORK
  chmod 0644 "$network_file.tmp.$$"
  mv -f "$network_file.tmp.$$" "$network_file"
  # Minimal cloud images may omit the system D-Bus socket. The current
  # address is applied below; networkd reads this file on the next boot.
  networkctl reload >/dev/null 2>&1 || true
fi
systemctl daemon-reload
systemctl enable oneclickvirt-routed-ipv6.service
systemctl restart oneclickvirt-routed-ipv6.service
systemctl is-active --quiet oneclickvirt-routed-ipv6.service`
	command := fmt.Sprintf(`set -eu
name=%s
ready=0
for attempt in $(seq 1 36); do
  if timeout -k 2 5 incus exec "$name" -- true </dev/null >/dev/null 2>&1; then ready=1; break; fi
  sleep 2
done
test "$ready" -eq 1
mac=$(incus config get "$name" volatile.eth1.hwaddr | tr '[:upper:]' '[:lower:]')
printf '%%s\n' "$mac" | grep -Eq '^[0-9a-f]{2}(:[0-9a-f]{2}){5}$'
timeout -k 5 60 incus exec "$name" -- sh -ceu %s sh "$mac" %s </dev/null`, shellSingleQuote(name), shellSingleQuote(guest), shellSingleQuote(ip.String()))
	output, err := utils.ExecuteShellCommandTimeoutContext(ctx, i.sshClient, command, 6*time.Minute)
	if err != nil {
		return fmt.Errorf("configure VM routed IPv6: output=%s: %w", summarizeIPv6ProbeOutput(output), err)
	}
	return nil
}
