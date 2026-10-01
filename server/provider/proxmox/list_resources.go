package proxmox

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"oneclickvirt/provider"
)

// listInstancesFromResources uses PVE's machine-readable resource records for
// both API and SSH listing. Human-readable pct/qm tables may contain warnings,
// ANSI colors, or translated headings, none of which identify a guest safely.
func (p *ProxmoxProvider) listInstancesFromResources(ctx context.Context, resources []proxmoxDiscoveredResource) ([]provider.Instance, error) {
	listed, err := p.convertDiscoveredResources(resources)
	if err != nil {
		return nil, err
	}
	instances := make([]provider.Instance, 0, len(listed))
	node := p.nodeName()
	for _, item := range listed {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if item.RuntimeIdentity != nil && node != "" && item.RuntimeIdentity.Node != "" && item.RuntimeIdentity.Node != node {
			continue
		}
		instance := provider.Instance{
			ID:     item.ProviderInstanceID,
			Name:   item.Name,
			Status: item.Status,
			Type:   item.InstanceType,
			CPU:    strconv.Itoa(item.CPU),
			Memory: fmt.Sprintf("%d MB", item.Memory),
			Disk:   fmt.Sprintf("%d MB", item.Disk),
		}
		if p.sshClient.HasExecutor() {
			if ip, err := p.getInstanceIPAddress(ctx, instance.ID, instance.Type); err == nil {
				instance.IP = strings.TrimSpace(ip)
				instance.PrivateIP = instance.IP
			}
			if ipv6, err := p.getInstanceIPv6ByVMID(ctx, instance.ID, instance.Type); err == nil {
				instance.IPv6Address = strings.TrimSpace(ipv6)
			}
		}
		instances = append(instances, instance)
	}
	return instances, nil
}
