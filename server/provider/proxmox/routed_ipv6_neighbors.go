package proxmox

import (
	"fmt"
	"strings"

	coreprovider "oneclickvirt/provider"
	"oneclickvirt/utils"
)

func routedIPv6NeighborScriptPath(tunnelID uint) string {
	return fmt.Sprintf("/etc/oneclickvirt/ipv6-tunnels/%d-pve-neighbors.sh", tunnelID)
}

// reconcileRoutedIPv6Neighbors delegates MAC discovery to the managed node
// script. The script scans PVE config files in one bounded operation, avoiding
// controller-side per-guest queries during instance creation.
func (p *ProxmoxProvider) reconcileRoutedIPv6Neighbors(routed coreprovider.RoutedIPv6Config) error {
	if routed.TunnelID == 0 {
		// Retain the existing non-tunnel static IPv6 behaviour. Only allocations
		// carrying tunnel metadata use the managed permanent-neighbour lifecycle.
		return nil
	}
	scriptPath := routedIPv6NeighborScriptPath(routed.TunnelID)
	command := fmt.Sprintf(`set -eu
script=%s
[ -x "$script" ] || { echo "managed routed IPv6 neighbour script is missing: $script" >&2; exit 1; }
"$script" reconcile
`, utils.ShellSingleQuote(scriptPath))
	output, err := p.sshClient.Execute(command)
	if err != nil {
		return fmt.Errorf("协调PVE隧道路由IPv6邻居失败: %s: %w", utils.TruncateString(strings.TrimSpace(output), 1200), err)
	}
	return nil
}

// reconcileAllRoutedIPv6Neighbors is called after PVE deletion. It runs every
// installed tunnel script in one SSH operation so stale permanent entries are
// released without a database query or a remote round trip per tunnel/guest.
func (p *ProxmoxProvider) reconcileAllRoutedIPv6Neighbors() error {
	const command = `set -eu
for script in /etc/oneclickvirt/ipv6-tunnels/*-pve-neighbors.sh; do
  [ -x "$script" ] || continue
  "$script" reconcile
done
`
	output, err := p.sshClient.Execute(command)
	if err != nil {
		return fmt.Errorf("清理PVE隧道路由IPv6邻居失败: %s: %w", utils.TruncateString(strings.TrimSpace(output), 1200), err)
	}
	return nil
}
