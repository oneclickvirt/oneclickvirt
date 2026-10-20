package ipv6pool

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"time"

	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	runtimeProvider "oneclickvirt/service/provider"
)

// Probe once per operation, outside provider-row locks and never per address.
func (s *Service) GetNativePoolNetwork(ctx context.Context, providerID uint) (*provider.NativeIPv6PoolNetwork, error) {
	var node providerModel.Provider
	if err := s.database().WithContext(ctx).Select("id", "type").First(&node, providerID).Error; err != nil {
		return nil, err
	}
	if !strings.EqualFold(node.Type, "proxmox") && !strings.EqualFold(node.Type, "proxmoxve") {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if s.readPoolNetwork != nil {
		return s.readPoolNetwork(ctx, providerID)
	}
	p, err := runtimeProvider.EnsureProviderConnected(ctx, providerID)
	if err != nil {
		return nil, err
	}
	capability, ok := p.(provider.NativeIPv6PoolNetworkProvider)
	if !ok {
		return nil, fmt.Errorf("PVE节点不支持IPv6地址池网络检测")
	}
	return capability.GetNativeIPv6PoolNetwork(ctx)
}

func validateNativePoolEntry(network *provider.NativeIPv6PoolNetwork, entry providerModel.ProviderIPv6Pool) error {
	if network == nil || entry.TunnelID != nil || entry.Source == SourceTunnel {
		return nil
	}
	prefix, err := netip.ParsePrefix(network.CIDR)
	if err != nil || !prefix.Addr().Is6() {
		return fmt.Errorf("检测到的实例IPv6前缀无效")
	}
	prefix = prefix.Masked()
	if entry.IsRange {
		requested, err := netip.ParsePrefix(entry.Address)
		if err == nil && requested.Bits() >= prefix.Bits() && prefix.Contains(requested.Masked().Addr()) {
			return nil
		}
	} else {
		address, err := netip.ParseAddr(entry.Address)
		if err == nil && address.Is6() && prefix.Contains(address) {
			reserved := address == prefix.Addr()
			for _, raw := range append(append([]string{}, network.Reserved...), network.Gateway) {
				candidate, err := netip.ParseAddr(raw)
				if err != nil {
					return fmt.Errorf("检测到的宿主IPv6保留地址无效")
				}
				reserved = reserved || address == candidate.WithZone("")
			}
			if !reserved {
				return nil
			}
		}
	}
	return fmt.Errorf("IPv6地址池条目 %s 不可用于实例；请使用网桥 %s 的可用前缀 %s，并排除网关 %s、宿主地址和网络地址", entry.Address, network.Bridge, network.CIDR, network.Gateway)
}

func validateNativePoolEntries(network *provider.NativeIPv6PoolNetwork, entries []providerModel.ProviderIPv6Pool) error {
	for _, entry := range entries {
		if err := validateNativePoolEntry(network, entry); err != nil {
			return err
		}
	}
	return nil
}
