package utils

import (
	"encoding/json"
	"fmt"
	"strings"
)

// IPv6ForwardingInterfaces keeps router advertisements enabled on both the
// allocation interface and every IPv6 default-route interface. Linux ignores
// RA on an interface whose accept_ra remains 1 after forwarding is enabled.
func IPv6ForwardingInterfaces(routeOutput, allocationInterface string) ([]string, error) {
	allocationInterface, err := ParseNetworkInterfaceOutput(allocationInterface)
	if err != nil {
		return nil, fmt.Errorf("无效的IPv6分配接口: %w", err)
	}
	var routes []struct {
		Destination string `json:"dst"`
		Device      string `json:"dev"`
		NextHops    []struct {
			Device string `json:"dev"`
		} `json:"nexthops"`
		Multipath []struct {
			Device string `json:"dev"`
		} `json:"multipath"`
	}
	routeOutput = StripTerminalCSI(strings.TrimSpace(routeOutput))
	if err := json.Unmarshal([]byte(routeOutput), &routes); err != nil || routes == nil {
		return nil, fmt.Errorf("无法解析宿主机IPv6默认路由JSON")
	}
	result := []string{allocationInterface}
	seen := map[string]bool{allocationInterface: true}
	add := func(raw string) error {
		device, err := ParseNetworkInterfaceOutput(raw)
		if err != nil {
			return fmt.Errorf("IPv6默认路由接口无效: %w", err)
		}
		if !seen[device] {
			result = append(result, device)
			seen[device] = true
		}
		return nil
	}
	for _, route := range routes {
		// `ip -j route show default` normally includes dst=default, but
		// older iproute2 releases may omit the field for the default route.
		if route.Destination != "" && route.Destination != "default" {
			continue
		}
		if route.Device == "" && len(route.NextHops) == 0 && len(route.Multipath) == 0 {
			return nil, fmt.Errorf("IPv6默认路由缺少接口，无法保护宿主机RA")
		}
		if route.Device != "" {
			if err := add(route.Device); err != nil {
				return nil, err
			}
		}
		for _, hop := range route.NextHops {
			if err := add(hop.Device); err != nil {
				return nil, err
			}
		}
		for _, hop := range route.Multipath {
			if err := add(hop.Device); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

// IPv6ForwardingSysctlCommand applies accept_ra=2 before enabling forwarding
// and persists the same order in one installer-owned sysctl file.
func IPv6ForwardingSysctlCommand(interfaces []string) (string, error) {
	if len(interfaces) == 0 {
		return "", fmt.Errorf("IPv6转发缺少接口")
	}
	validated := make([]string, 0, len(interfaces))
	seen := make(map[string]bool)
	for _, raw := range interfaces {
		device, err := ParseNetworkInterfaceOutput(raw)
		if err != nil {
			return "", fmt.Errorf("无效的IPv6网络接口: %w", err)
		}
		if !seen[device] {
			validated = append(validated, ShellSingleQuote(device))
			seen[device] = true
		}
	}
	return fmt.Sprintf(`set -eu
conf=/etc/sysctl.d/99-oneclickvirt-ipv6.conf
mkdir -p /etc/sysctl.d
tmp="${conf}.tmp.$$"
{
  for iface in %s; do
    if [ -e "/proc/sys/net/ipv6/conf/$iface/accept_ra" ]; then
      printf 'net.ipv6.conf.%%s.accept_ra=2\n' "$iface"
    fi
  done
  printf 'net.ipv6.conf.all.forwarding=1\n'
  printf 'net.ipv6.conf.default.forwarding=1\n'
  printf 'net.ipv6.conf.all.proxy_ndp=1\n'
  printf 'net.ipv6.conf.default.proxy_ndp=1\n'
  for iface in %s; do
    if [ -e "/proc/sys/net/ipv6/conf/$iface/proxy_ndp" ]; then
      printf 'net.ipv6.conf.%%s.proxy_ndp=1\n' "$iface"
    fi
  done
} > "$tmp"
chmod 0644 "$tmp"
mv -f "$tmp" "$conf"
for iface in %s; do
  if [ -e "/proc/sys/net/ipv6/conf/$iface/accept_ra" ]; then
    sysctl -w "net.ipv6.conf.$iface.accept_ra=2" >/dev/null
  fi
done
sysctl -w net.ipv6.conf.all.forwarding=1 >/dev/null
sysctl -w net.ipv6.conf.default.forwarding=1 >/dev/null
sysctl -w net.ipv6.conf.all.proxy_ndp=1 >/dev/null
sysctl -w net.ipv6.conf.default.proxy_ndp=1 >/dev/null
for iface in %s; do
  if [ -e "/proc/sys/net/ipv6/conf/$iface/proxy_ndp" ]; then
    sysctl -w "net.ipv6.conf.$iface.proxy_ndp=1" >/dev/null
  fi
done
# networkd restores its configured proxy-NDP value when it configures a link,
# after systemd-sysctl has already applied the interface-specific setting.
# Persist the setting in the selected network file's drop-in as well.
if command -v networkctl >/dev/null 2>&1 && command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet systemd-networkd.service; then
  for iface in %s; do
    network_file=$(LC_ALL=C SYSTEMD_COLORS=0 networkctl status --no-pager "$iface" 2>/dev/null | sed -n 's/^[[:space:]]*Network File: //p')
    network_name=${network_file##*/}
    case "$network_name" in
      ''|*[!a-zA-Z0-9_.:-]*) continue ;;
      *.network) ;;
      *) continue ;;
    esac
    directory="/etc/systemd/network/$network_name.d"
    mkdir -p "$directory"
    target="$directory/99-oneclickvirt-ipv6.conf"
    [ ! -L "$target" ]
    printf '[Network]\nIPv6ProxyNDP=yes\n' > "$target.tmp.$$"
    chmod 0644 "$target.tmp.$$"
    mv -f "$target.tmp.$$" "$target"
  done
  networkctl reload
fi
# Runtime autostart must wait until the final uplink settings are restored.
if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
  unit=oneclickvirt-ipv6-routing.service
  mkdir -p /etc/systemd/system
  sysctl_bin=$(command -v sysctl)
  cat > "/etc/systemd/system/$unit.tmp.$$" <<EOF
[Unit]
Description=OneClickVirt IPv6 forwarding and proxy NDP readiness
After=network-online.target
Wants=network-online.target
Before=incus.service incus-daemon.service lxd.service snap.lxd.daemon.service snap.incus.daemon.service

[Service]
Type=oneshot
ExecStart=$sysctl_bin -p /etc/sysctl.d/99-oneclickvirt-ipv6.conf
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
EOF
  chmod 0644 "/etc/systemd/system/$unit.tmp.$$"
  mv -f "/etc/systemd/system/$unit.tmp.$$" "/etc/systemd/system/$unit"
  for daemon in incus.service incus-daemon.service lxd.service snap.lxd.daemon.service snap.incus.daemon.service; do
    if [ "$(systemctl show "$daemon" -p LoadState --value 2>/dev/null)" = loaded ]; then
      directory="/etc/systemd/system/$daemon.d"
      mkdir -p "$directory"
      printf '[Unit]\nRequires=%%s\nAfter=%%s\n' "$unit" "$unit" > "$directory/99-oneclickvirt-ipv6.conf"
    fi
  done
  systemctl daemon-reload
  systemctl enable "$unit" >/dev/null
  sysctl -p "$conf" >/dev/null
  systemctl start "$unit"
fi`, strings.Join(validated, " "), strings.Join(validated, " "), strings.Join(validated, " "), strings.Join(validated, " "), strings.Join(validated, " ")), nil
}
