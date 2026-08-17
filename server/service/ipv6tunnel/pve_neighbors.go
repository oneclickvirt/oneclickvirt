package ipv6tunnel

import (
	"fmt"
	"strings"

	providerModel "oneclickvirt/model/provider"
	ipv6poolService "oneclickvirt/service/ipv6pool"
	"oneclickvirt/utils"
)

// pveNeighborScriptPath is installed alongside a managed tunnel. Keeping the
// reconciliation on the node means a PVE reboot does not depend on a controller
// request to restore permanent routed-IPv6 neighbour entries.
func pveNeighborScriptPath(id uint) string {
	return fmt.Sprintf("/etc/oneclickvirt/ipv6-tunnels/%d-pve-neighbors.sh", id)
}

func pveNeighborStatePath(id uint) string {
	return fmt.Sprintf("/var/lib/oneclickvirt/ipv6-tunnels/%d-pve-neighbors.state", id)
}

// renderPVERoutedIPv6NeighborScript produces a bounded, host-local
// reconciliation command. It scans the PVE configuration files once, rather
// than issuing a controller or remote call for each guest. Entries are scoped
// to both the managed bridge and this tunnel's routed prefix.
func renderPVERoutedIPv6NeighborScript(tunnel providerModel.ProviderIPv6Tunnel) string {
	routedCIDR, _, _, _, err := ipv6poolService.RoutedPrefixDetails(tunnel.RoutedCIDR)
	if err != nil || strings.TrimSpace(routedCIDR) == "" {
		return "#!/bin/sh\n# No routed IPv6 prefix is managed by this tunnel.\nexit 0\n"
	}

	return fmt.Sprintf(`#!/bin/sh
set -eu

BRIDGE=%s
ROUTED_CIDR=%s
STATE_PATH=${ONECLICKVIRT_PVE_NEIGHBOR_STATE_PATH:-%s}
LXC_CONFIG_DIR=${ONECLICKVIRT_PVE_LXC_CONFIG_DIR:-/etc/pve/lxc}
QEMU_CONFIG_DIR=${ONECLICKVIRT_PVE_QEMU_CONFIG_DIR:-/etc/pve/qemu-server}

remove_state_entries() {
  [ -f "$STATE_PATH" ] || return 0
  while IFS=' ' read -r address mac; do
    [ -n "$address" ] || continue
    # Never evict an active dynamic neighbour. The state file contains only
    # entries installed by this script, and we remove one only while it remains
    # a permanent entry on the managed bridge.
    if ip -6 neigh show to "$address" dev "$BRIDGE" nud permanent 2>/dev/null | grep -F "$address" >/dev/null 2>&1; then
      ip -6 neigh del "$address" dev "$BRIDGE" >/dev/null 2>&1 || true
    fi
  done < "$STATE_PATH"
  rm -f -- "$STATE_PATH"
}

reconcile() {
  # Nodes that are not PVE do not have these directories. This keeps the
  # tunnel lifecycle portable while making the PVE repair entirely incremental.
  if [ ! -d "$LXC_CONFIG_DIR" ] && [ ! -d "$QEMU_CONFIG_DIR" ]; then
    return 0
  fi
  command -v python3 >/dev/null 2>&1 || { echo 'python3 is required to reconcile PVE routed IPv6 neighbours' >&2; exit 1; }
  command -v ip >/dev/null 2>&1 || { echo 'iproute2 is required to reconcile PVE routed IPv6 neighbours' >&2; exit 1; }
  ip link show dev "$BRIDGE" >/dev/null 2>&1 || { echo "routed IPv6 bridge $BRIDGE is missing" >&2; exit 1; }

  workdir=$(mktemp -d "${TMPDIR:-/tmp}/oneclickvirt-pve-neighbors.XXXXXX")
  trap 'rm -rf -- "$workdir"' EXIT HUP INT TERM
  desired="$workdir/desired"
  python3 - "$ROUTED_CIDR" "$BRIDGE" "$LXC_CONFIG_DIR" "$QEMU_CONFIG_DIR" <<'PY' > "$desired"
import glob
import ipaddress
import os
import re
import sys

cidr, bridge, lxc_dir, qemu_dir = sys.argv[1:]
network = ipaddress.IPv6Network(cidr, strict=False)
net_line = re.compile(r"^net([0-9]+):\s*(.*)$")
ipconfig_line = re.compile(r"^ipconfig([0-9]+):\s*(.*)$")
mac_keys = ("hwaddr", "virtio", "e1000", "vmxnet3", "rtl8139", "ne2k_pci")

def option(value, key):
    match = re.search(r"(?:^|,)\s*" + re.escape(key) + r"\s*=\s*([^,]+)", value)
    return match.group(1).strip().strip("'\"") if match else ""

def mac(value):
    for key in mac_keys:
        candidate = option(value, key)
        if re.fullmatch(r"[0-9A-Fa-f]{2}(?::[0-9A-Fa-f]{2}){5}", candidate):
            return candidate.lower()
    return ""

def collect(path):
    networks = {}
    ipconfigs = {}
    try:
        with open(path, encoding="utf-8", errors="replace") as handle:
            for raw_line in handle:
                line = raw_line.strip()
                match = net_line.match(line)
                if match:
                    index, value = match.groups()
                    networks[index] = (option(value, "bridge"), mac(value), option(value, "ip6"))
                    continue
                match = ipconfig_line.match(line)
                if match:
                    index, value = match.groups()
                    ipconfigs[index] = option(value, "ip6")
    except OSError:
        return []

    result = []
    for index, (configured_bridge, configured_mac, inline_ip6) in networks.items():
        if configured_bridge != bridge or not configured_mac:
            continue
        candidate = inline_ip6 or ipconfigs.get(index, "")
        if not candidate or candidate.lower() in ("auto", "dhcp"):
            continue
        try:
            address = ipaddress.IPv6Address(candidate.split("/", 1)[0])
        except ValueError:
            continue
        if address in network:
            result.append((address, configured_mac))
    return result

entries = set()
for directory in (lxc_dir, qemu_dir):
    for path in glob.glob(os.path.join(directory, "*.conf")):
        entries.update(collect(path))

for address, address_mac in sorted(entries, key=lambda entry: int(entry[0])):
    print(f"{address} {address_mac}")
PY
  sort -u "$desired" -o "$desired"

  if [ -f "$STATE_PATH" ]; then
    while IFS=' ' read -r old_address old_mac; do
      [ -n "$old_address" ] || continue
      if ! grep -Fqx -- "$old_address $old_mac" "$desired"; then
        if ip -6 neigh show to "$old_address" dev "$BRIDGE" nud permanent 2>/dev/null | grep -F "$old_address" >/dev/null 2>&1; then
          ip -6 neigh del "$old_address" dev "$BRIDGE" >/dev/null 2>&1 || true
        fi
      fi
    done < "$STATE_PATH"
  fi

  while IFS=' ' read -r address mac; do
    [ -n "$address" ] || continue
    ip -6 neigh replace "$address" lladdr "$mac" nud permanent dev "$BRIDGE"
  done < "$desired"

  state_dir=$(dirname "$STATE_PATH")
  mkdir -p "$state_dir"
  chmod 0700 "$state_dir"
  cp "$desired" "${STATE_PATH}.tmp.$$"
  chmod 0600 "${STATE_PATH}.tmp.$$"
  mv -f "${STATE_PATH}.tmp.$$" "$STATE_PATH"
}

case "${1:-reconcile}" in
  reconcile) reconcile ;;
  cleanup) remove_state_entries ;;
  *) echo 'usage: pve-neighbors {reconcile|cleanup}' >&2; exit 2 ;;
esac
`, utils.ShellSingleQuote(utils.RoutedIPv6BridgeName), utils.ShellSingleQuote(routedCIDR), utils.ShellSingleQuote(pveNeighborStatePath(tunnel.ID)))
}
