package lxd

import (
	"context"
	"fmt"
	"strings"

	"oneclickvirt/provider"
	"oneclickvirt/utils"
)

// preflightIPv6Network validates the host capabilities before an instance is
// created.  IPv6 device/firewall configuration happens after init, so waiting
// until that stage would create an instance only to roll it back when the host
// has no delegated prefix (a common /128-only VPS configuration).
func (l *LXDProvider) preflightIPv6Network(ctx context.Context, config provider.InstanceConfig, networkConfig NetworkConfig) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	hasIPv6 := networkConfig.NetworkType == "nat_ipv4_ipv6" || networkConfig.NetworkType == "dedicated_ipv4_ipv6" || networkConfig.NetworkType == "ipv6_only"
	if !hasIPv6 {
		return nil
	}
	routed, present, err := provider.ResolveRoutedIPv6(config)
	if err != nil {
		return fmt.Errorf("IPv6路由配置无效: %w", err)
	}
	if present {
		if output, checkErr := l.sshClient.Execute(routed.HostCheckCommand()); checkErr != nil {
			return fmt.Errorf("隧道路由IPv6网桥未就绪: output=%s: %w", summarizeIPv6ProbeOutput(output), checkErr)
		}
		return nil
	}
	// Native IPv6 requires more than a locally assigned address: without a
	// usable default route the container may be created successfully but can
	// never reach the public network. Repair only RA/DHCPv6-managed routes and
	// verify the real source address through ipv6.ip.sb; never guess a gateway.
	if err := utils.EnsureIPv6HostEgress(l.sshClient, utils.IPv6EgressOptions{PersistRA: true}); err != nil {
		return fmt.Errorf("宿主机IPv6公网出口不可用: %w", err)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if _, err := l.checkIPv6(ctx); err != nil {
		return fmt.Errorf("宿主机IPv6环境不可用: %w", err)
	}
	requested := ""
	if config.Metadata != nil {
		requested = strings.TrimSpace(config.Metadata["static_ipv6"])
	}
	if _, err := l.selectHostIPv6InterfaceNetwork(ctx, utils.HostIPv6PrefixMustBeAssignable(networkConfig.NetworkType, requested)); err != nil {
		return fmt.Errorf("宿主机IPv6前缀不可用于实例分配: %w", err)
	}
	return nil
}
