package proxmox

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"

	"oneclickvirt/provider"
	"oneclickvirt/utils"
)

const natIPv4DataPlaneCacheTTL = 5 * time.Minute

var linuxInterfaceName = regexp.MustCompile(`^[[:alnum:]_.:-]{1,15}$`)

// ensureNATIPv4DataPlane runs at most once per provider cache window. It stays
// outside database work and coalesces concurrent creates into one remote repair.
func (p *ProxmoxProvider) ensureNATIPv4DataPlane(ctx context.Context, config provider.InstanceConfig) error {
	if !p.requiresNATIPv4DataPlane(config) {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !p.sshClient.HasExecutor() {
		return fmt.Errorf("PVE NAT IPv4 data-plane check failed: node executor is unavailable")
	}
	if p.natIPv4DataPlaneFresh(time.Now()) {
		return nil
	}

	_, err, _ := p.natDataPlaneGroup.Do("nat-ipv4-dataplane", func() (interface{}, error) {
		if p.natIPv4DataPlaneFresh(time.Now()) {
			return nil, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		bridge, gatewayCIDR, subnet, err := p.natIPv4DataPlaneConfig()
		if err != nil {
			return nil, fmt.Errorf("PVE NAT IPv4 data-plane check failed: %w", err)
		}
		output, err := p.sshClient.Execute(buildNATIPv4DataPlaneCommand(bridge, gatewayCIDR, subnet))
		if err != nil {
			return nil, fmt.Errorf("PVE NAT IPv4 data-plane repair failed: %s: %w", utils.TruncateString(strings.TrimSpace(output), 1200), err)
		}
		if !strings.Contains(output, "ONECLICKVIRT_PVE_NAT_READY") {
			return nil, fmt.Errorf("PVE NAT IPv4 data-plane repair did not confirm success: %s", utils.TruncateString(strings.TrimSpace(output), 1200))
		}

		p.natDataPlaneMu.Lock()
		p.natDataPlaneReady = time.Now()
		p.natDataPlaneMu.Unlock()
		return nil, nil
	})
	return err
}

func (p *ProxmoxProvider) requiresNATIPv4DataPlane(config provider.InstanceConfig) bool {
	networkType := ""
	if config.Metadata != nil {
		networkType = strings.TrimSpace(config.Metadata["network_type"])
	}
	if networkType == "" {
		networkType = strings.TrimSpace(p.config.NetworkType)
	}
	// NAT IPv4 is the historic default for nodes whose configuration predates
	// the explicit network_type field.
	return networkType == "" || networkType == "nat_ipv4" || networkType == "nat_ipv4_ipv6"
}

func (p *ProxmoxProvider) natIPv4DataPlaneFresh(now time.Time) bool {
	p.natDataPlaneMu.Lock()
	defer p.natDataPlaneMu.Unlock()
	return !p.natDataPlaneReady.IsZero() && now.Sub(p.natDataPlaneReady) < natIPv4DataPlaneCacheTTL
}

func (p *ProxmoxProvider) resetNATIPv4DataPlaneCache() {
	p.natDataPlaneMu.Lock()
	p.natDataPlaneReady = time.Time{}
	p.natDataPlaneMu.Unlock()
}

func (p *ProxmoxProvider) natIPv4DataPlaneConfig() (bridge, gatewayCIDR, subnet string, err error) {
	bridge = strings.TrimSpace(p.getBridgeName("nat"))
	if !linuxInterfaceName.MatchString(bridge) {
		return "", "", "", fmt.Errorf("invalid NAT bridge name %q", bridge)
	}

	gateway := net.ParseIP(strings.TrimSpace(p.getInternalGateway())).To4()
	if gateway == nil {
		return "", "", "", fmt.Errorf("invalid NAT IPv4 gateway %q", p.getInternalGateway())
	}
	network := append(net.IP(nil), gateway...)
	network[3] = 0
	return bridge, gateway.String() + "/24", (&net.IPNet{IP: network, Mask: net.CIDRMask(24, 32)}).String(), nil
}

func buildNATIPv4DataPlaneCommand(bridge, gatewayCIDR, subnet string) string {
	const scriptPath = "/usr/local/sbin/oneclickvirt-pve-nat-ensure"
	const unitName = "oneclickvirt-pve-nat.service"
	const unitPath = "/etc/systemd/system/oneclickvirt-pve-nat.service"

	script := fmt.Sprintf(`#!/bin/sh
# managed by OneClickVirt
set -eu
bridge=%s
gateway_cidr=%s
subnet=%s

ip link show dev "$bridge" >/dev/null 2>&1 || { echo "NAT bridge is missing: $bridge" >&2; exit 1; }
ip address replace "$gateway_cidr" dev "$bridge"
sysctl -w net.ipv4.ip_forward=1 >/dev/null
command -v nft >/dev/null 2>&1 || { echo 'nft is required for PVE NAT IPv4' >&2; exit 1; }

nft add table ip oneclickvirt_nat 2>/dev/null || true
nft 'add chain ip oneclickvirt_nat postrouting { type nat hook postrouting priority srcnat; policy accept; }' 2>/dev/null || true
nft 'add chain ip oneclickvirt_nat forward { type filter hook forward priority filter; policy accept; }' 2>/dev/null || true
nft list chain ip oneclickvirt_nat postrouting | grep -F -- "ip saddr $subnet ip daddr != $subnet masquerade" >/dev/null 2>&1 || \
  nft add rule ip oneclickvirt_nat postrouting ip saddr "$subnet" ip daddr != "$subnet" masquerade
nft list chain ip oneclickvirt_nat forward | grep -F -- "ip saddr $subnet accept" >/dev/null 2>&1 || \
  nft add rule ip oneclickvirt_nat forward ip saddr "$subnet" accept
nft list chain ip oneclickvirt_nat forward | grep -F -- "ip daddr $subnet ct state established,related accept" >/dev/null 2>&1 || \
  nft add rule ip oneclickvirt_nat forward ip daddr "$subnet" ct state established,related accept
printf 'ONECLICKVIRT_PVE_NAT_READY\n'
`, utils.ShellSingleQuote(bridge), utils.ShellSingleQuote(gatewayCIDR), utils.ShellSingleQuote(subnet))
	unit := fmt.Sprintf(`[Unit]
Description=OneClickVirt PVE NAT IPv4 data plane
After=network-online.target nftables.service
Before=pve-guests.service oneclickvirt-agent.service
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=%s
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
`, scriptPath)

	script64 := base64.StdEncoding.EncodeToString([]byte(script))
	unit64 := base64.StdEncoding.EncodeToString([]byte(unit))
	return fmt.Sprintf(`set -eu
script_path=%s
unit_name=%s
unit_path=%s
install -d -m 0755 /usr/local/sbin /etc/systemd/system
if [ -e "$script_path" ] && ! grep -Fqx '# managed by OneClickVirt' "$script_path"; then
  echo "refusing to replace unmanaged NAT script: $script_path" >&2
  exit 1
fi
if [ -e "$unit_path" ] && ! grep -Fqx 'Description=OneClickVirt PVE NAT IPv4 data plane' "$unit_path"; then
  echo "refusing to replace unmanaged NAT unit: $unit_path" >&2
  exit 1
fi
printf '%%s' %s | base64 -d > "${script_path}.tmp.$$"
printf '%%s' %s | base64 -d > "${unit_path}.tmp.$$"
chmod 0700 "${script_path}.tmp.$$"
chmod 0644 "${unit_path}.tmp.$$"
mv -f "${script_path}.tmp.$$" "$script_path"
mv -f "${unit_path}.tmp.$$" "$unit_path"
systemctl daemon-reload
systemctl enable "$unit_name" >/dev/null
if systemctl is-active --quiet "$unit_name"; then
  "$script_path"
else
  systemctl start "$unit_name"
fi
`, utils.ShellSingleQuote(scriptPath), utils.ShellSingleQuote(unitName), utils.ShellSingleQuote(unitPath), utils.ShellSingleQuote(script64), utils.ShellSingleQuote(unit64))
}
