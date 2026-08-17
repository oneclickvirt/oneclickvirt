package ipv6tunnel

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strings"

	providerModel "oneclickvirt/model/provider"
	ipv6poolService "oneclickvirt/service/ipv6pool"
	"oneclickvirt/utils"
)

func unitName(id uint) string {
	return fmt.Sprintf("oneclickvirt-ipv6-tunnel-%d.service", id)
}

func scriptPath(id uint) string {
	return fmt.Sprintf("/etc/oneclickvirt/ipv6-tunnels/%d.sh", id)
}

const (
	// Agent egress rules reserve priorities 10000-29999. Keep routed-prefix
	// tunnel rules immediately before the kernel's main-table rule (32766), so
	// source traffic is selected before a native RA/default route can win.
	ipv6TunnelPolicyRouteTableBase    uint64 = 10000
	ipv6TunnelPolicyRulePriorityBase  uint64 = 30000
	ipv6TunnelPolicyRulePriorityLimit uint64 = 32765
	routedPolicyProbeIPv6                    = "2606:4700:4700::1111"
)

// tunnelPolicyRouteParameters reserves a table outside Agent egress tables and
// a source-rule priority before the kernel main table. Failing clearly is safer
// than allowing a route rule to silently collide with the main-table priority.
func tunnelPolicyRouteParameters(tunnelID uint) (uint64, uint64, error) {
	if tunnelID == 0 {
		return 0, 0, fmt.Errorf("IPv6隧道尚未保存，无法分配策略路由")
	}
	id := uint64(tunnelID)
	if id > ipv6TunnelPolicyRulePriorityLimit-ipv6TunnelPolicyRulePriorityBase {
		return 0, 0, fmt.Errorf("IPv6隧道ID %d 超出可用策略路由优先级范围", tunnelID)
	}
	return ipv6TunnelPolicyRouteTableBase + id, ipv6TunnelPolicyRulePriorityBase + id, nil
}

func buildApplyCommand(tunnel providerModel.ProviderIPv6Tunnel) string {
	unit := unitName(tunnel.ID)
	script := scriptPath(tunnel.ID)
	neighborScript := pveNeighborScriptPath(tunnel.ID)
	unitPath := "/etc/systemd/system/" + unit
	unitContent := fmt.Sprintf(`[Unit]
Description=OneClickVirt managed IPv6 tunnel %d
After=network-online.target systemd-networkd.service pve-cluster.service
Before=pve-guests.service
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=%s up
ExecStop=%s down
TimeoutStartSec=45
TimeoutStopSec=30

[Install]
WantedBy=multi-user.target
`, tunnel.ID, script, script)
	scriptContent := renderTunnelScript(tunnel)
	neighborContent := renderPVERoutedIPv6NeighborScript(tunnel)
	networkContent := renderNetworkdConfig(tunnel)
	unit64 := base64.StdEncoding.EncodeToString([]byte(unitContent))
	script64 := base64.StdEncoding.EncodeToString([]byte(scriptContent))
	neighbor64 := base64.StdEncoding.EncodeToString([]byte(neighborContent))
	network64 := base64.StdEncoding.EncodeToString([]byte(networkContent))

	return fmt.Sprintf(`set -eu
if [ "$(id -u)" -ne 0 ]; then echo 'root privileges are required' >&2; exit 1; fi
if ! command -v systemctl >/dev/null 2>&1; then echo 'systemd is required for persistent IPv6 tunnels' >&2; exit 1; fi
install_pkg() {
  package="$1"
  if command -v apt-get >/dev/null 2>&1; then DEBIAN_FRONTEND=noninteractive apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y "$package"
  elif command -v dnf >/dev/null 2>&1; then dnf install -y "$package"
  elif command -v yum >/dev/null 2>&1; then yum install -y "$package"
  elif command -v apk >/dev/null 2>&1; then apk add --no-cache "$package"
  else echo "cannot install required package: $package" >&2; return 1; fi
}
install_iproute() {
  if command -v dnf >/dev/null 2>&1; then dnf install -y iproute
  elif command -v yum >/dev/null 2>&1; then yum install -y iproute
  else install_pkg iproute2; fi
}
command -v ip >/dev/null 2>&1 || install_iproute
command -v base64 >/dev/null 2>&1 || install_pkg coreutils
mkdir -p /etc/oneclickvirt/ipv6-tunnels
mkdir -p /etc/systemd/network
unit=%s
unit_path=%s
script_path=%s
neighbor_path=%s
tunnel_interface=%s
network_path=%s
%s
if ip link show dev %s >/dev/null 2>&1 && [ ! -f "$unit_path" ] && [ ! -f "$script_path" ] && [ ! -f "$neighbor_path" ] && [ ! -f "$network_path" ]; then
  echo 'refusing to replace an unmanaged network interface' >&2
  exit 1
fi
unit_backup="${unit_path}.oneclickvirt-backup.$$"
script_backup="${script_path}.oneclickvirt-backup.$$"
neighbor_backup="${neighbor_path}.oneclickvirt-backup.$$"
network_backup="${network_path}.oneclickvirt-backup.$$"
had_unit=0; had_script=0; had_neighbor=0; had_network=0; was_active=0
[ -f "$unit_path" ] && { cp -p "$unit_path" "$unit_backup"; had_unit=1; }
[ -f "$script_path" ] && { cp -p "$script_path" "$script_backup"; had_script=1; }
[ -f "$neighbor_path" ] && { cp -p "$neighbor_path" "$neighbor_backup"; had_neighbor=1; }
[ -f "$network_path" ] && { cp -p "$network_path" "$network_backup"; had_network=1; }
systemctl is-active --quiet "$unit" && was_active=1 || true
rollback() {
  rc=$?
  trap - EXIT
  systemctl stop "$unit" >/dev/null 2>&1 || true
  if [ "$had_unit" -eq 1 ]; then mv -f "$unit_backup" "$unit_path"; else rm -f "$unit_path" "$unit_backup"; fi
  if [ "$had_script" -eq 1 ]; then mv -f "$script_backup" "$script_path"; else rm -f "$script_path" "$script_backup"; fi
  if [ "$had_neighbor" -eq 1 ]; then mv -f "$neighbor_backup" "$neighbor_path"; else rm -f "$neighbor_path" "$neighbor_backup"; fi
  if [ "$had_network" -eq 1 ]; then mv -f "$network_backup" "$network_path"; else rm -f "$network_path" "$network_backup"; fi
  reload_networkd >/dev/null 2>&1 || true
  systemctl daemon-reload >/dev/null 2>&1 || true
  if [ "$was_active" -eq 1 ]; then systemctl enable --now "$unit" >/dev/null 2>&1 || true; else systemctl disable "$unit" >/dev/null 2>&1 || true; fi
  exit "$rc"
}
trap rollback EXIT
systemctl stop "$unit" >/dev/null 2>&1 || true
printf '%%s' %s | base64 -d > "${unit_path}.tmp.$$"
printf '%%s' %s | base64 -d > "${script_path}.tmp.$$"
printf '%%s' %s | base64 -d > "${neighbor_path}.tmp.$$"
printf '%%s' %s | base64 -d > "${network_path}.tmp.$$"
chmod 0644 "${unit_path}.tmp.$$"
chmod 0700 "${script_path}.tmp.$$"
chmod 0700 "${neighbor_path}.tmp.$$"
chmod 0644 "${network_path}.tmp.$$"
mv -f "${unit_path}.tmp.$$" "$unit_path"
mv -f "${script_path}.tmp.$$" "$script_path"
mv -f "${neighbor_path}.tmp.$$" "$neighbor_path"
mv -f "${network_path}.tmp.$$" "$network_path"
reload_networkd
systemctl daemon-reload
systemctl enable "$unit" >/dev/null
if ! systemctl start "$unit"; then
  echo "IPv6 tunnel unit $unit failed; recent diagnostics follow" >&2
  systemctl status "$unit" --no-pager -l >&2 || true
  command -v journalctl >/dev/null 2>&1 && journalctl -u "$unit" -n 40 --no-pager >&2 || true
  exit 1
fi
validation_attempt=1
validation_attempts=6
validation_rc=1
validation_output=''
while [ "$validation_attempt" -le "$validation_attempts" ]; do
  if validation_output=$("$script_path" status 2>&1); then
    validation_rc=0
    break
  else
    validation_rc=$?
  fi
  [ "$validation_attempt" -ge "$validation_attempts" ] && break
  sleep 1
  validation_attempt=$((validation_attempt + 1))
done
if [ "$validation_rc" -ne 0 ]; then
  echo "IPv6 tunnel validation failed after $validation_attempt attempt(s) starting $unit" >&2
  [ -z "$validation_output" ] || printf '%%s\n' "$validation_output" >&2
  echo 'current tunnel network diagnostics:' >&2
  ip -d link show dev "$tunnel_interface" >&2 || true
  ip -o -6 addr show dev "$tunnel_interface" >&2 || true
  ip -6 route show dev "$tunnel_interface" >&2 || true
  ip -d link show dev oneclickvirt6 >&2 || true
  ip -o -6 addr show dev oneclickvirt6 >&2 || true
  ip -6 route show dev oneclickvirt6 >&2 || true
  command -v sysctl >/dev/null 2>&1 && sysctl -n net.ipv6.conf.all.forwarding >&2 || true
  command -v sysctl >/dev/null 2>&1 && sysctl -n net.ipv6.conf.default.forwarding >&2 || true
  command -v sysctl >/dev/null 2>&1 && sysctl -n "net.ipv6.conf.$tunnel_interface.forwarding" >&2 || true
  command -v sysctl >/dev/null 2>&1 && sysctl -n net.ipv6.conf.oneclickvirt6.forwarding >&2 || true
  exit 1
fi
rm -f "$unit_backup" "$script_backup" "$neighbor_backup" "$network_backup"
trap - EXIT
printf 'applied\n'
`, utils.ShellSingleQuote(unit), utils.ShellSingleQuote(unitPath), utils.ShellSingleQuote(script), utils.ShellSingleQuote(neighborScript), utils.ShellSingleQuote(tunnel.Interface), utils.ShellSingleQuote(networkConfigPath(tunnel.ID)), reloadNetworkdCommand, utils.ShellSingleQuote(tunnel.Interface),
		utils.ShellSingleQuote(unit64), utils.ShellSingleQuote(script64), utils.ShellSingleQuote(neighbor64), utils.ShellSingleQuote(network64))
}

func renderTunnelScript(tunnel providerModel.ProviderIPv6Tunnel) string {
	module := "sit"
	if tunnel.Mode == "gre" {
		module = "ip_gre"
	}
	defaultUp := ""
	defaultDown := ""
	defaultRouteCheck := "check_default_route() { return 0; }\n"
	if tunnel.DefaultRoute {
		defaultUp = fmt.Sprintf("ip -6 route replace default via %s dev \"$IFACE\" metric %d onlink\n", utils.ShellSingleQuote(tunnel.RemoteIPv6), tunnel.RouteMetric)
		defaultDown = fmt.Sprintf("ip -6 route del default via %s dev \"$IFACE\" metric %d >/dev/null 2>&1 || true\n", utils.ShellSingleQuote(tunnel.RemoteIPv6), tunnel.RouteMetric)
		defaultRouteCheck = `check_default_route() {
  # Do not use a lookup to an external destination here. During networkd
  # reconciliation, route selection can lag behind the installed route even
  # though the configured tunnel default is already present. Verify the
  # tunnel-specific route directly instead of asking the kernel to select a
  # route for an unrelated external destination.
  ip -6 route show default dev "$IFACE" 2>/dev/null | awk -v remote="$REMOTE6" '
    $1 != "default" { next }
    {
      for (i = 1; i <= NF; i++) {
        if ($i == "via" && i < NF && $(i + 1) == remote) {
          found = 1
          exit
        }
      }
    }
    END { exit(found ? 0 : 1) }
  '
}
`
	}
	routedCIDR, routedGateway, _, routedPrefix := "", "", "", 0
	if strings.TrimSpace(tunnel.RoutedCIDR) != "" {
		if canonical, gateway, _, prefix, err := ipv6poolService.RoutedPrefixDetails(tunnel.RoutedCIDR); err == nil {
			routedCIDR, routedGateway, routedPrefix = canonical, gateway, prefix
		}
	}
	routedSetup := "ensure_routed_network() { :; }\nensure_routed_policy_route() { :; }\ncleanup_routed_policy_route() { :; }\nreconcile_pve_neighbors() { :; }\ncleanup_pve_neighbors() { :; }\n"
	routedCleanup := ""
	routedStatus := "check_routed_network() { return 0; }\ncheck_routed_forwarding() { return 0; }\ncheck_routed_policy_route() { return 0; }\n"
	if routedCIDR != "" {
		policyTable, policyPriority, policyErr := tunnelPolicyRouteParameters(tunnel.ID)
		if policyErr != nil {
			return fmt.Sprintf("#!/bin/sh\necho %s >&2\nexit 1\n", utils.ShellSingleQuote(policyErr.Error()))
		}
		bridge := utils.RoutedIPv6BridgeName
		sysctlPath := fmt.Sprintf("/etc/sysctl.d/99-oneclickvirt-ipv6-tunnel-%d.conf", tunnel.ID)
		bridgeNetworkPath := routedBridgeNetworkConfigPath(tunnel.ID)
		bridgeNetworkContent := renderRoutedBridgeNetworkdConfig()
		routedSetup = fmt.Sprintf(`
BRIDGE=%s
ROUTED_CIDR=%s
ROUTED_GATEWAY=%s
ROUTED_PREFIX=%d
POLICY_TABLE=%d
POLICY_PRIORITY=%d
POLICY_PROBE=%s
SYSCTL_PATH=%s
BRIDGE_NETWORK_PATH=%s
BRIDGE_NETWORK_CONTENT=%s
PVE_NEIGHBOR_SCRIPT=%s
ensure_routed_bridge_networkd_config() {
  mkdir -p /etc/systemd/network
  printf '%%s' "$BRIDGE_NETWORK_CONTENT" > "${BRIDGE_NETWORK_PATH}.tmp.$$"
  chmod 0644 "${BRIDGE_NETWORK_PATH}.tmp.$$"
  mv -f "${BRIDGE_NETWORK_PATH}.tmp.$$" "$BRIDGE_NETWORK_PATH"
  if command -v networkctl >/dev/null 2>&1 && systemctl is-active --quiet systemd-networkd; then
    networkctl reload
  fi
}
reconfigure_routed_bridge_networkd() {
  if command -v networkctl >/dev/null 2>&1 && systemctl is-active --quiet systemd-networkd; then
    networkctl reconfigure "$BRIDGE" || true
  fi
}
reconcile_pve_neighbors() {
  [ -x "$PVE_NEIGHBOR_SCRIPT" ] || return 0
  "$PVE_NEIGHBOR_SCRIPT" reconcile
}
cleanup_pve_neighbors() {
  [ -x "$PVE_NEIGHBOR_SCRIPT" ] || return 0
  "$PVE_NEIGHBOR_SCRIPT" cleanup || true
}
ensure_routed_policy_route() {
  # Keep a routed guest prefix on its tunnel without replacing the host's
  # native IPv6 default route. The policy rule is installed only after its
  # dedicated table has a usable on-link default.
  while ip -6 rule del pref "$POLICY_PRIORITY" from "$ROUTED_CIDR" table "$POLICY_TABLE" >/dev/null 2>&1; do :; done
  ip -6 route replace table "$POLICY_TABLE" default via "$REMOTE6" dev "$IFACE" metric "$ROUTE_METRIC" onlink
  ip -6 rule add pref "$POLICY_PRIORITY" from "$ROUTED_CIDR" table "$POLICY_TABLE"
}
cleanup_routed_policy_route() {
  # Remove the source selector before its table route so disabling a tunnel
  # never leaves guest traffic pinned to a disappeared device.
  while ip -6 rule del pref "$POLICY_PRIORITY" from "$ROUTED_CIDR" table "$POLICY_TABLE" >/dev/null 2>&1; do :; done
  ip -6 route del table "$POLICY_TABLE" default via "$REMOTE6" dev "$IFACE" >/dev/null 2>&1 || true
}
ensure_routed_network() {
  ensure_routed_bridge_networkd_config
  if ! ip link show dev "$BRIDGE" >/dev/null 2>&1; then
    ip link add name "$BRIDGE" type bridge
  fi
  ip link set dev "$BRIDGE" up
  reconfigure_routed_bridge_networkd
  # The routed prefix belongs exclusively to this host. Avoid a false DAD
  # conflict from an unmanaged catch-all networkd profile removing its gateway.
  ip -6 addr replace "$ROUTED_GATEWAY/$ROUTED_PREFIX" dev "$BRIDGE" nodad
  ip -6 route replace "$ROUTED_CIDR" dev "$BRIDGE"
  # all enables forwarding for guest interfaces that already exist; default
  # makes veth/TAP interfaces created after this tunnel inherit forwarding.
  # Keep tunnel and bridge entries explicit because networkd can reset them.
  printf 'net.ipv6.conf.all.forwarding=1\\nnet.ipv6.conf.default.forwarding=1\\nnet.ipv6.conf.%%s.forwarding=1\\nnet.ipv6.conf.%%s.forwarding=1\\n' "$IFACE" "$BRIDGE" > "$SYSCTL_PATH"
  if command -v sysctl >/dev/null 2>&1; then
    sysctl -p "$SYSCTL_PATH" >/dev/null
  fi
  if command -v ip6tables >/dev/null 2>&1; then
    ip6tables -C FORWARD -i "$IFACE" -o "$BRIDGE" -j ACCEPT >/dev/null 2>&1 || ip6tables -I FORWARD -i "$IFACE" -o "$BRIDGE" -j ACCEPT
    ip6tables -C FORWARD -i "$BRIDGE" -o "$IFACE" -j ACCEPT >/dev/null 2>&1 || ip6tables -I FORWARD -i "$BRIDGE" -o "$IFACE" -j ACCEPT
  fi
  ensure_routed_policy_route
  reconcile_pve_neighbors
}
		`, utils.ShellSingleQuote(bridge), utils.ShellSingleQuote(routedCIDR), utils.ShellSingleQuote(routedGateway), routedPrefix,
			policyTable, policyPriority, utils.ShellSingleQuote(routedPolicyProbeIPv6), utils.ShellSingleQuote(sysctlPath), utils.ShellSingleQuote(bridgeNetworkPath), utils.ShellSingleQuote(bridgeNetworkContent), utils.ShellSingleQuote(pveNeighborScriptPath(tunnel.ID)))
		routedCleanup = `
  cleanup_routed_policy_route
  cleanup_pve_neighbors
  if command -v ip6tables >/dev/null 2>&1; then
    while ip6tables -D FORWARD -i "$IFACE" -o "$BRIDGE" -j ACCEPT >/dev/null 2>&1; do :; done
    while ip6tables -D FORWARD -i "$BRIDGE" -o "$IFACE" -j ACCEPT >/dev/null 2>&1; do :; done
  fi
  ip -6 route del "$ROUTED_CIDR" dev "$BRIDGE" >/dev/null 2>&1 || true
  ip -6 addr del "$ROUTED_GATEWAY/$ROUTED_PREFIX" dev "$BRIDGE" >/dev/null 2>&1 || true
  rm -f -- "$SYSCTL_PATH" "$BRIDGE_NETWORK_PATH"
  if command -v networkctl >/dev/null 2>&1 && systemctl is-active --quiet systemd-networkd; then
    networkctl reload
  fi
`
		routedStatus = fmt.Sprintf(`check_routed_network() {
  ip link show dev %s >/dev/null 2>&1 || return 1
  ip -o -6 addr show dev %s | awk '{print $4}' | grep -Fx %s >/dev/null 2>&1 || return 1
  route_line=" $(ip -6 route show %s 2>/dev/null || true) "
  printf '%%s\n' "$route_line" | grep -F " dev $BRIDGE " >/dev/null 2>&1 || return 1
}
check_routed_forwarding() {
  command -v sysctl >/dev/null 2>&1 || return 1
  [ "$(sysctl -n net.ipv6.conf.all.forwarding 2>/dev/null || echo 0)" = 1 ] || return 1
  [ "$(sysctl -n net.ipv6.conf.default.forwarding 2>/dev/null || echo 0)" = 1 ] || return 1
  [ "$(sysctl -n "net.ipv6.conf.$IFACE.forwarding" 2>/dev/null || echo 0)" = 1 ] || return 1
  [ "$(sysctl -n "net.ipv6.conf.$BRIDGE.forwarding" 2>/dev/null || echo 0)" = 1 ] || return 1
}
check_routed_policy_route() {
  ip -6 rule show 2>/dev/null | awk -v priority="$POLICY_PRIORITY:" -v cidr="$ROUTED_CIDR" -v table="$POLICY_TABLE" '
    $1 != priority { next }
    {
      source = 0
      selected_table = 0
      for (i = 1; i <= NF; i++) {
        if ($i == "from" && i < NF && $(i + 1) == cidr) source = 1
        if (($i == "lookup" || $i == "table") && i < NF && $(i + 1) == table) selected_table = 1
      }
      if (source && selected_table) { found = 1; exit }
    }
    END { exit(found ? 0 : 1) }
  ' || return 1
  ip -6 route show table "$POLICY_TABLE" 2>/dev/null | awk -v remote="$REMOTE6" -v iface="$IFACE" '
    $1 != "default" { next }
    {
      peer = 0
      device = 0
      for (i = 1; i <= NF; i++) {
        if ($i == "via" && i < NF && $(i + 1) == remote) peer = 1
        if ($i == "dev" && i < NF && $(i + 1) == iface) device = 1
      }
      if (peer && device) { found = 1; exit }
    }
    END { exit(found ? 0 : 1) }
  ' || return 1
  route_line=" $(ip -6 route get "$POLICY_PROBE" from "$ROUTED_GATEWAY" iif "$BRIDGE" 2>/dev/null || true) "
  printf '%%s\n' "$route_line" | grep -F " dev $IFACE " >/dev/null 2>&1
}
`,
			utils.ShellSingleQuote(bridge), utils.ShellSingleQuote(bridge), utils.ShellSingleQuote(routedGateway+fmt.Sprintf("/%d", routedPrefix)), utils.ShellSingleQuote(routedCIDR))
	}
	return fmt.Sprintf(`#!/bin/sh
	set -eu
	IFACE=%s
	REMOTE6=%s
	ROUTE_METRIC=%d
	reconfigure_networkd() {
  if command -v networkctl >/dev/null 2>&1 && systemctl is-active --quiet systemd-networkd; then
    networkctl reconfigure "$IFACE"
  fi
}
%s
	case "${1:-}" in
	  up)
	    command -v modprobe >/dev/null 2>&1 && modprobe %s >/dev/null 2>&1 || true
	    cleanup_routed_policy_route
	    ip link show dev "$IFACE" >/dev/null 2>&1 && ip link delete "$IFACE" || true
    ip tunnel add "$IFACE" mode %s remote %s local %s ttl %d
    ip link set dev "$IFACE" mtu %d up
    ip -6 addr replace %s dev "$IFACE"
    ip -6 route replace %s/128 dev "$IFACE" metric %d
%s    # networkd may reset interface-local forwarding while applying its route state.
    # Configure it first, then apply the routed bridge and scoped forwarding.
    reconfigure_networkd
    ensure_routed_network
    ;;
  down)
%s%s    ip link show dev "$IFACE" >/dev/null 2>&1 && ip link delete "$IFACE" || true
    ;;
  status)
    status_failed=0
    check_status() {
      label=$1
      shift
      if "$@"; then return 0; fi
      printf 'IPv6 tunnel check failed: %%s\n' "$label" >&2
      status_failed=1
      return 0
    }
    check_link() { ip link show dev "$IFACE" >/dev/null 2>&1; }
    check_address() { ip -o -6 addr show dev "$IFACE" | awk '{print $4}' | grep -Fx %s >/dev/null 2>&1; }
    check_peer_route() {
      peer_route=" $(ip -6 route get "$REMOTE6" 2>/dev/null || true) "
      printf '%%s\n' "$peer_route" | grep -F " dev $IFACE " >/dev/null 2>&1
    }
    check_ping() { command -v ping >/dev/null 2>&1; }
    check_gateway() {
      check_link && check_address && check_ping || return 0
      ping -6 -n -c 1 -W 5 "$REMOTE6" >/dev/null 2>&1
    }
    check_status "tunnel interface $IFACE is missing" check_link
    check_status "local IPv6 address %s is missing" check_address
    check_status "peer route does not use $IFACE" check_peer_route
    %s
	    check_status "default IPv6 route does not use $IFACE" check_default_route
	    %s
	    check_status "routed IPv6 bridge address or route is missing" check_routed_network
	    check_status "IPv6 forwarding is disabled; all, default, tunnel, and routed bridge forwarding must be enabled" check_routed_forwarding
	    check_status "routed IPv6 source policy route does not select tunnel interface" check_routed_policy_route
	    check_status "ping is unavailable for tunnel gateway validation" check_ping
    check_status "IPv6 tunnel gateway $REMOTE6 is unreachable" check_gateway
    [ "$status_failed" -eq 0 ] || { echo 'IPv6 tunnel status validation failed' >&2; exit 1; }
    ;;
  *)
    echo 'usage: tunnel-script {up|down|status}' >&2
    exit 2
    ;;
esac
		`, utils.ShellSingleQuote(tunnel.Interface), utils.ShellSingleQuote(tunnel.RemoteIPv6), tunnel.RouteMetric, routedSetup, module, tunnel.Mode,
		utils.ShellSingleQuote(tunnel.RemoteIPv4), utils.ShellSingleQuote(tunnel.LocalIPv4), tunnel.TTL,
		tunnel.MTU, utils.ShellSingleQuote(tunnel.LocalIPv6), utils.ShellSingleQuote(tunnel.RemoteIPv6), tunnel.RouteMetric,
		defaultUp, routedCleanup, defaultDown, utils.ShellSingleQuote(tunnel.LocalIPv6), utils.ShellSingleQuote(tunnel.LocalIPv6), defaultRouteCheck, routedStatus)
}

func buildDisableCommand(tunnel providerModel.ProviderIPv6Tunnel) string {
	unit := unitName(tunnel.ID)
	return fmt.Sprintf(`set -eu
if [ "$(id -u)" -ne 0 ]; then echo 'root privileges are required' >&2; exit 1; fi
command -v systemctl >/dev/null 2>&1 || { echo 'systemd is unavailable' >&2; exit 1; }
command -v ip >/dev/null 2>&1 || { echo 'iproute2 is unavailable' >&2; exit 1; }
unit=%s
unit_path=%s
script_path=%s
network_path=%s
%s
if ip link show dev %s >/dev/null 2>&1 && [ ! -f "$unit_path" ] && [ ! -f "$script_path" ] && [ ! -f "$network_path" ] && ! systemctl cat "$unit" >/dev/null 2>&1; then
  echo 'refusing to delete an unmanaged network interface' >&2
  exit 1
	fi
	systemctl disable --now "$unit" >/dev/null 2>&1 || true
	# ExecStop normally runs the managed script. Invoke it once more when it is
	# still present so a failed/stale systemd transition cannot leave its source
	# policy rule behind.
	[ ! -x "$script_path" ] || "$script_path" down || true
	ip link show dev %s >/dev/null 2>&1 && ip link delete %s || true
rm -f -- "$network_path"
reload_networkd
if systemctl is-active --quiet "$unit" || ip link show dev %s >/dev/null 2>&1 || [ -e "$network_path" ]; then
  echo 'tunnel remained active after disable' >&2
  exit 1
fi
printf 'disabled\n'
	`, utils.ShellSingleQuote(unit), utils.ShellSingleQuote("/etc/systemd/system/"+unit), utils.ShellSingleQuote(scriptPath(tunnel.ID)), utils.ShellSingleQuote(networkConfigPath(tunnel.ID)), reloadNetworkdCommand,
		utils.ShellSingleQuote(tunnel.Interface), utils.ShellSingleQuote(tunnel.Interface), utils.ShellSingleQuote(tunnel.Interface), utils.ShellSingleQuote(tunnel.Interface))
}

func buildDeleteCommand(tunnels []providerModel.ProviderIPv6Tunnel) string {
	ordered := append([]providerModel.ProviderIPv6Tunnel(nil), tunnels...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	var builder strings.Builder
	builder.WriteString("set -eu\nif [ \"$(id -u)\" -ne 0 ]; then echo 'root privileges are required' >&2; exit 1; fi\ncommand -v systemctl >/dev/null 2>&1 || { echo 'systemd is unavailable' >&2; exit 1; }\ncommand -v ip >/dev/null 2>&1 || { echo 'iproute2 is unavailable' >&2; exit 1; }\n")
	// Validate ownership for every interface before deleting any of them. This
	// prevents a stale DB row from deleting a physical interface that later
	// reused the same name.
	for _, tunnel := range ordered {
		fmt.Fprintf(&builder, "if ip link show dev %s >/dev/null 2>&1 && [ ! -f %s ] && [ ! -f %s ] && [ ! -f %s ] && [ ! -f %s ] && ! systemctl cat %s >/dev/null 2>&1; then echo %s >&2; exit 1; fi\n",
			utils.ShellSingleQuote(tunnel.Interface), utils.ShellSingleQuote("/etc/systemd/system/"+unitName(tunnel.ID)),
			utils.ShellSingleQuote(scriptPath(tunnel.ID)), utils.ShellSingleQuote(pveNeighborScriptPath(tunnel.ID)), utils.ShellSingleQuote(networkConfigPath(tunnel.ID)), utils.ShellSingleQuote(unitName(tunnel.ID)),
			utils.ShellSingleQuote(fmt.Sprintf("refusing to delete unmanaged interface %s", tunnel.Interface)))
	}
	for _, tunnel := range ordered {
		fmt.Fprintf(&builder, "systemctl disable --now %s >/dev/null 2>&1 || true\n", utils.ShellSingleQuote(unitName(tunnel.ID)))
		fmt.Fprintf(&builder, "[ ! -x %s ] || %s down || true\n", utils.ShellSingleQuote(scriptPath(tunnel.ID)), utils.ShellSingleQuote(scriptPath(tunnel.ID)))
		fmt.Fprintf(&builder, "ip link show dev %s >/dev/null 2>&1 && ip link delete %s || true\n", utils.ShellSingleQuote(tunnel.Interface), utils.ShellSingleQuote(tunnel.Interface))
		fmt.Fprintf(&builder, "rm -f -- %s %s %s %s %s\n", utils.ShellSingleQuote("/etc/systemd/system/"+unitName(tunnel.ID)), utils.ShellSingleQuote(scriptPath(tunnel.ID)), utils.ShellSingleQuote(pveNeighborScriptPath(tunnel.ID)), utils.ShellSingleQuote(pveNeighborStatePath(tunnel.ID)), utils.ShellSingleQuote(networkConfigPath(tunnel.ID)))
	}
	builder.WriteString(reloadNetworkdCommand)
	builder.WriteString("reload_networkd\n")
	builder.WriteString("systemctl daemon-reload\n")
	for _, tunnel := range ordered {
		fmt.Fprintf(&builder, "if systemctl is-active --quiet %s || ip link show dev %s >/dev/null 2>&1 || [ -e %s ] || [ -e %s ] || [ -e %s ] || [ -e %s ] || [ -e %s ]; then echo %s >&2; exit 1; fi\n",
			utils.ShellSingleQuote(unitName(tunnel.ID)), utils.ShellSingleQuote(tunnel.Interface),
			utils.ShellSingleQuote("/etc/systemd/system/"+unitName(tunnel.ID)), utils.ShellSingleQuote(scriptPath(tunnel.ID)), utils.ShellSingleQuote(pveNeighborScriptPath(tunnel.ID)), utils.ShellSingleQuote(pveNeighborStatePath(tunnel.ID)), utils.ShellSingleQuote(networkConfigPath(tunnel.ID)),
			utils.ShellSingleQuote(fmt.Sprintf("IPv6 tunnel %d cleanup is incomplete", tunnel.ID)))
	}
	builder.WriteString("printf 'deleted\\n'\n")
	return builder.String()
}

func buildCheckCommand(tunnels []providerModel.ProviderIPv6Tunnel) string {
	ordered := append([]providerModel.ProviderIPv6Tunnel(nil), tunnels...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	var builder strings.Builder
	builder.WriteString("set -u\ncommand -v systemctl >/dev/null 2>&1 || { echo 'systemd is unavailable' >&2; exit 1; }\ncommand -v ip >/dev/null 2>&1 || { echo 'iproute2 is unavailable' >&2; exit 1; }\n")
	for _, tunnel := range ordered {
		unit := utils.ShellSingleQuote(unitName(tunnel.ID))
		iface := utils.ShellSingleQuote(tunnel.Interface)
		address := utils.ShellSingleQuote(tunnel.LocalIPv6)
		fmt.Fprintf(&builder, "enabled=0; active=0; link=0; address=0; route=1; network=0; gateway=0; routed=1; forwarding=1; policy=1\n")
		fmt.Fprintf(&builder, "systemctl is-enabled --quiet %s >/dev/null 2>&1 && enabled=1 || true\n", unit)
		fmt.Fprintf(&builder, "systemctl is-active --quiet %s >/dev/null 2>&1 && active=1 || true\n", unit)
		fmt.Fprintf(&builder, "ip link show dev %s >/dev/null 2>&1 && link=1 || true\n", iface)
		fmt.Fprintf(&builder, "if [ \"$link\" -eq 1 ]; then ip -o -6 addr show dev %s | awk '{print $4}' | grep -Fx %s >/dev/null 2>&1 && address=1 || true; fi\n", iface, address)
		fmt.Fprintf(&builder, "[ -s %s ] && network=1 || true\n", utils.ShellSingleQuote(networkConfigPath(tunnel.ID)))
		if tunnel.DefaultRoute {
			fmt.Fprintf(&builder, `route=0; if ip -6 route show default dev %s 2>/dev/null | awk -v remote=%s '
  $1 != "default" { next }
  {
    for (i = 1; i <= NF; i++) {
      if ($i == "via" && i < NF && $(i + 1) == remote) {
        found = 1
        exit
      }
    }
  }
  END { exit(found ? 0 : 1) }
'; then route=1; fi
`, iface, utils.ShellSingleQuote(tunnel.RemoteIPv6))
		}
		fmt.Fprintf(&builder, "if [ \"$link\" -eq 1 ] && [ \"$address\" -eq 1 ] && command -v ping >/dev/null 2>&1; then ping -6 -n -c 1 -W 5 %s >/dev/null 2>&1 && gateway=1 || true; fi\n", utils.ShellSingleQuote(tunnel.RemoteIPv6))
		if strings.TrimSpace(tunnel.RoutedCIDR) != "" {
			if cidr, gateway, _, prefix, err := ipv6poolService.RoutedPrefixDetails(tunnel.RoutedCIDR); err == nil {
				bridge := utils.ShellSingleQuote(utils.RoutedIPv6BridgeName)
				fmt.Fprintf(&builder, "routed=0; forwarding=0; if ip link show dev %s >/dev/null 2>&1 && ip -o -6 addr show dev %s | awk '{print $4}' | grep -Fx %s >/dev/null 2>&1; then route_line=\" $(ip -6 route show %s 2>/dev/null || true) \"; printf '%%s\\n' \"$route_line\" | grep -F %s >/dev/null 2>&1 && routed=1 || true; fi\n", bridge, bridge, utils.ShellSingleQuote(gateway+fmt.Sprintf("/%d", prefix)), utils.ShellSingleQuote(cidr), utils.ShellSingleQuote(" dev "+utils.RoutedIPv6BridgeName+" "))
				fmt.Fprintf(&builder, "if command -v sysctl >/dev/null 2>&1 && [ \"$(sysctl -n net.ipv6.conf.all.forwarding 2>/dev/null || echo 0)\" = 1 ] && [ \"$(sysctl -n net.ipv6.conf.default.forwarding 2>/dev/null || echo 0)\" = 1 ] && [ \"$(sysctl -n %s 2>/dev/null || echo 0)\" = 1 ] && [ \"$(sysctl -n %s 2>/dev/null || echo 0)\" = 1 ]; then forwarding=1; fi\n", utils.ShellSingleQuote("net.ipv6.conf."+tunnel.Interface+".forwarding"), utils.ShellSingleQuote("net.ipv6.conf."+utils.RoutedIPv6BridgeName+".forwarding"))
				if policyTable, policyPriority, policyErr := tunnelPolicyRouteParameters(tunnel.ID); policyErr == nil {
					fmt.Fprintf(&builder, `policy=0
if [ "$link" -eq 1 ]; then
  if ip -6 rule show 2>/dev/null | awk -v priority=%s -v cidr=%s -v table=%s '
    $1 != priority { next }
    {
      source = 0
      selected_table = 0
      for (i = 1; i <= NF; i++) {
        if ($i == "from" && i < NF && $(i + 1) == cidr) source = 1
        if (($i == "lookup" || $i == "table") && i < NF && $(i + 1) == table) selected_table = 1
      }
      if (source && selected_table) { found = 1; exit }
    }
    END { exit(found ? 0 : 1) }
  ' && ip -6 route show table %d 2>/dev/null | awk -v remote=%s -v iface=%s '
    $1 != "default" { next }
    {
      peer = 0
      device = 0
      for (i = 1; i <= NF; i++) {
        if ($i == "via" && i < NF && $(i + 1) == remote) peer = 1
        if ($i == "dev" && i < NF && $(i + 1) == iface) device = 1
      }
      if (peer && device) { found = 1; exit }
    }
    END { exit(found ? 0 : 1) }
  '; then
    policy_route=" $(ip -6 route get %s from %s iif %s 2>/dev/null || true) "
    printf '%%s\n' "$policy_route" | grep -F %s >/dev/null 2>&1 && policy=1 || true
  fi
fi
`, utils.ShellSingleQuote(fmt.Sprintf("%d:", policyPriority)), utils.ShellSingleQuote(cidr), utils.ShellSingleQuote(fmt.Sprintf("%d", policyTable)), policyTable, utils.ShellSingleQuote(tunnel.RemoteIPv6), iface, utils.ShellSingleQuote(routedPolicyProbeIPv6), utils.ShellSingleQuote(gateway), bridge, utils.ShellSingleQuote(" dev "+tunnel.Interface+" "))
				} else {
					builder.WriteString("policy=0\n")
				}
			}
		}
		fmt.Fprintf(&builder, "printf 'TUNNEL|%d|%%s|%%s|%%s|%%s|%%s|%%s|%%s|%%s|%%s|%%s\\n' \"$enabled\" \"$active\" \"$link\" \"$address\" \"$route\" \"$network\" \"$gateway\" \"$routed\" \"$forwarding\" \"$policy\"\n", tunnel.ID)
	}
	return builder.String()
}
