package podman

import (
	"context"
	"fmt"
	"strings"

	"oneclickvirt/provider"
	"oneclickvirt/utils"
)

func (p *PodmanProvider) routedNetworkSelection(config provider.InstanceConfig, networkType string) (utils.ContainerNetworkSelection, bool, error) {
	routed, present, err := provider.ResolveRoutedIPv6(config)
	if err != nil || !present {
		return utils.ContainerNetworkSelection{}, present, err
	}
	if !utils.NetworkTypeHasIPv6(networkType) {
		return utils.ContainerNetworkSelection{}, true, fmt.Errorf("已分配隧道路由IPv6，但网络类型 %q 未启用IPv6", strings.TrimSpace(networkType))
	}
	if output, execErr := p.sshClient.Execute(routed.HostCheckCommand()); execErr != nil {
		return utils.ContainerNetworkSelection{}, true, fmt.Errorf("隧道路由IPv6宿主机前置检查失败: output=%s: %w", utils.TruncateString(strings.TrimSpace(output), 2000), execErr)
	}
	selection := utils.ContainerNetworkSelection{
		Network:               ipv4Network,
		StaticIPv6:            routed.Address,
		IPv6Network:           routed.CIDR,
		RoutedCIDR:            routed.CIDR,
		RoutedGateway:         routed.Gateway,
		RoutedBridge:          routed.Bridge,
		RoutedTunnelID:        routed.TunnelID,
		RoutedTunnelInterface: routed.TunnelInterface,
		IPv6:                  true,
		RoutedVeth:            true,
	}
	if strings.EqualFold(strings.TrimSpace(networkType), "ipv6_only") {
		selection.Network = "none"
	}
	return selection, true, nil
}

// restoreRoutedIPv6AfterStart recreates the host-managed veth after Podman
// starts a fresh network namespace for a routed IPv6 container.
func (p *PodmanProvider) restoreRoutedIPv6AfterStart(name string, contexts ...context.Context) error {
	ctx := context.Background()
	if len(contexts) > 0 && contexts[0] != nil {
		ctx = contexts[0]
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	inspectCommand, err := provider.RoutedIPv6RuntimeLabelInspectCommand(cliName, name)
	if err != nil {
		return err
	}
	output, err := utils.ExecuteShellCommandContext(ctx, p.sshClient, inspectCommand)
	if err != nil {
		return p.failRoutedIPv6Restore(ctx, name, "读取", output, err)
	}
	routed, present, err := provider.RoutedIPv6ConfigFromRuntimeLabelOutput(output)
	if err != nil {
		return p.failRoutedIPv6Restore(ctx, name, "解析", output, err)
	}
	if !present {
		return nil
	}
	attachCommand, err := routed.RoutedIPv6VethCommand(cliName, name)
	if err != nil {
		return p.failRoutedIPv6Restore(ctx, name, "构造", "", err)
	}
	output, err = utils.ExecuteShellCommandContext(ctx, p.sshClient, attachCommand)
	if err != nil {
		return p.failRoutedIPv6Restore(ctx, name, "附加", output, err)
	}
	return nil
}

func (p *PodmanProvider) restoreIPv6AfterStart(name string, contexts ...context.Context) error {
	ctx := context.Background()
	if len(contexts) > 0 && contexts[0] != nil {
		ctx = contexts[0]
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := utils.ExecuteShellCommandContext(ctx, p.sshClient, fmt.Sprintf("test -x %s && test -f %s && awk -v name=%s '$1 == name {found=1} END {exit found ? 0 : 1}' %s", shellSingleQuote(ipv6ManualHelper), shellSingleQuote(ipv6AllocationFile), shellSingleQuote(name), shellSingleQuote(ipv6AllocationFile))); err == nil {
		var output string
		var attachErr error
		output, attachErr = utils.ExecuteShellCommandContext(ctx, p.sshClient, fmt.Sprintf("%s %s", shellSingleQuote(ipv6ManualHelper), shellSingleQuote(name)))
		if attachErr != nil {
			return fmt.Errorf("恢复手工路由IPv6失败: output=%s: %w", utils.TruncateString(strings.TrimSpace(output), 2000), attachErr)
		}
	}
	return p.restoreRoutedIPv6AfterStart(name, ctx)
}

func (p *PodmanProvider) failRoutedIPv6Restore(ctx context.Context, name, phase, output string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	_, _ = utils.ExecuteShellCommandContext(ctx, p.sshClient, fmt.Sprintf("%s stop %s 2>/dev/null || true", cliName, shellSingleQuote(name)))
	return fmt.Errorf("%s隧道路由IPv6失败，已尝试停止实例以避免IPv6配置缺失: output=%s: %w", phase, utils.TruncateString(strings.TrimSpace(output), 4000), err)
}
