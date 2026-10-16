package proxmox

import (
	"fmt"
	"strings"
)

func proxmoxGuestResolvers(networkType string) string {
	resolvers := "1.1.1.1 8.8.8.8"
	if networkType == "ipv6_only" {
		return "2606:4700:4700::1111 2001:4860:4860::8888"
	}
	if hasProxmoxIPv6(networkType) {
		resolvers += " 2606:4700:4700::1111 2001:4860:4860::8888"
	}
	return resolvers
}

// Cloud-init does not replace IPv4 DNS automatically when net0 becomes
// IPv6-only. Select reachable resolvers before the guest's first boot, also
// for API-created and rebuilt guests. A rejected DNS configuration is fatal.
func (p *ProxmoxProvider) configureVMDNS(vmid int, networkType string) error {
	resolvers := proxmoxGuestResolvers(networkType)
	if _, err := p.sshClient.Execute(fmt.Sprintf("qm set %d --nameserver %s", vmid, shellSingleQuote(resolvers))); err != nil {
		return fmt.Errorf("配置虚拟机DNS失败: %w", err)
	}
	return nil
}

func (p *ProxmoxProvider) configureContainerDNS(vmid int, networkType string) error {
	resolvers := proxmoxGuestResolvers(networkType)
	if _, err := p.sshClient.Execute(fmt.Sprintf("pct set %d --nameserver %s", vmid, shellSingleQuote(resolvers))); err != nil {
		return fmt.Errorf("配置容器DNS失败: %w", err)
	}
	// Legacy templates can carry /etc/.pve-ignore.resolv.conf. Merely setting
	// the PVE property leaves their old IPv4-only resolver unchanged. Update
	// the newly created guest too, without weakening its isolation or changing
	// the host resolver. PVE retains the same settings for subsequent boots.
	var lines []string
	for _, resolver := range strings.Fields(resolvers) {
		lines = append(lines, "nameserver "+resolver)
	}
	script := "printf '%s\\n' " + shellSingleQuote(strings.Join(lines, "\n")) + " > /etc/resolv.conf"
	if _, err := p.sshClient.Execute(fmt.Sprintf("pct exec %d -- sh -c %s", vmid, shellSingleQuote(script))); err != nil {
		return fmt.Errorf("更新容器内部DNS失败: %w", err)
	}
	return nil
}
