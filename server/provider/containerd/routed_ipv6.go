package containerd

import (
	"fmt"
	"strings"

	"oneclickvirt/provider"
	"oneclickvirt/utils"
)

// routedNetworkSelection prepares an isolated nerdctl/CNI network for a
// controller-assigned IPv6 address. The network name is stable per tunnel so
// retries are idempotent and never mutate the legacy shared network.
func (c *ContainerdProvider) routedNetworkSelection(config provider.InstanceConfig, networkType string) (utils.ContainerNetworkSelection, bool, error) {
	routed, present, err := provider.ResolveRoutedIPv6(config)
	if err != nil || !present {
		return utils.ContainerNetworkSelection{}, present, err
	}
	if !utils.NetworkTypeHasIPv6(networkType) {
		return utils.ContainerNetworkSelection{}, true, fmt.Errorf("已分配隧道路由IPv6，但网络类型 %q 未启用IPv6", strings.TrimSpace(networkType))
	}
	if output, execErr := c.sshClient.Execute(routed.HostCheckCommand()); execErr != nil {
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

// restoreRoutedIPv6AfterStart recreates the host-managed veth after nerdctl
// starts a fresh network namespace for a routed IPv6 container.
func (c *ContainerdProvider) restoreRoutedIPv6AfterStart(name string) error {
	inspectCommand, err := provider.RoutedIPv6RuntimeLabelInspectCommand(cliName, name)
	if err != nil {
		return err
	}
	output, err := c.sshClient.Execute(inspectCommand)
	if err != nil {
		return c.failRoutedIPv6Restore(name, "读取", output, err)
	}
	routed, present, err := provider.RoutedIPv6ConfigFromRuntimeLabelOutput(output)
	if err != nil {
		return c.failRoutedIPv6Restore(name, "解析", output, err)
	}
	if !present {
		return nil
	}
	attachCommand, err := routed.RoutedIPv6VethCommand(cliName, name)
	if err != nil {
		return c.failRoutedIPv6Restore(name, "构造", "", err)
	}
	output, err = c.sshClient.Execute(attachCommand)
	if err != nil {
		return c.failRoutedIPv6Restore(name, "附加", output, err)
	}
	return nil
}

func (c *ContainerdProvider) failRoutedIPv6Restore(name, phase, output string, err error) error {
	_, _ = c.sshClient.Execute(fmt.Sprintf("%s stop %s 2>/dev/null || true", cliName, shellSingleQuote(name)))
	return fmt.Errorf("%s隧道路由IPv6失败，已停止实例以避免IPv6配置缺失: output=%s: %w", phase, utils.TruncateString(strings.TrimSpace(output), 4000), err)
}
