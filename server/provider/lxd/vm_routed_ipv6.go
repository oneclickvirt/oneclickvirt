package lxd

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
func (l *LXDProvider) configureVMRoutedIPv6(ctx context.Context, name, address string) error {
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
After=network.target
[Service]
Type=oneshot
ExecStart=/usr/local/sbin/oneclickvirt-routed-ipv6
RemainAfterExit=yes
Restart=on-failure
RestartSec=3
[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable oneclickvirt-routed-ipv6.service
systemctl restart oneclickvirt-routed-ipv6.service
systemctl is-active --quiet oneclickvirt-routed-ipv6.service`
	command := fmt.Sprintf(`set -eu
name=%s
ready=0
for attempt in $(seq 1 36); do
  if timeout 5 lxc exec "$name" -- true >/dev/null 2>&1; then ready=1; break; fi
  sleep 2
done
test "$ready" -eq 1
mac=$(lxc config get "$name" volatile.eth1.hwaddr | tr '[:upper:]' '[:lower:]')
printf '%%s\n' "$mac" | grep -Eq '^[0-9a-f]{2}(:[0-9a-f]{2}){5}$'
timeout 60 lxc exec "$name" -- sh -ceu %s sh "$mac" %s`, shellSingleQuote(name), shellSingleQuote(guest), shellSingleQuote(ip.String()))
	output, err := utils.ExecuteShellCommandTimeoutContext(ctx, l.sshClient, command, 6*time.Minute)
	if err != nil {
		return fmt.Errorf("configure VM routed IPv6: output=%s: %w", summarizeIPv6ProbeOutput(output), err)
	}
	return nil
}
