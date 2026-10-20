package proxmox

import (
	"context"
	"fmt"
	"time"

	"oneclickvirt/provider"
	"oneclickvirt/utils"
)

func (p *ProxmoxProvider) GetNativeIPv6PoolNetwork(ctx context.Context) (*provider.NativeIPv6PoolNetwork, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	info, err := p.getIPv6Info(ctx)
	if err != nil {
		return nil, err
	}
	if !hasDirectProxmoxIPv6Info(info) || info.IPv6Gateway == "" {
		return nil, fmt.Errorf("PVE没有可供实例分配的公网IPv6前缀和网关，请先配置实例IPv6网桥")
	}
	addresses, err := p.sshClient.ExecuteContext(ctx, "LC_ALL=C NO_COLOR=1 ip -j -6 addr show")
	if err != nil {
		return nil, err
	}
	routes, err := p.sshClient.ExecuteContext(ctx, "LC_ALL=C NO_COLOR=1 ip -j -6 route show table all")
	if err != nil {
		return nil, err
	}
	reserved, err := utils.ParseHostIPv6Reservations(addresses, routes)
	if err != nil {
		return nil, err
	}
	reserved = append(reserved, info.HostIPv6Address, info.IPv6Gateway, info.Network.NetworkAddress().String())
	bridge := info.DirectBridge
	if bridge == "" {
		if !info.HasAppendedAddresses {
			return nil, fmt.Errorf("PVE实例IPv6网桥没有可用公网前缀，不能将宿主上联网段作为实例地址池")
		}
		bridge = p.getBridgeName("nat")
	}
	return &provider.NativeIPv6PoolNetwork{CIDR: info.Network.CIDR(), Gateway: info.IPv6Gateway, Bridge: bridge, Reserved: reserved}, nil
}
