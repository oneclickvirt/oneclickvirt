package docker

import (
	"fmt"
	"strings"

	"oneclickvirt/provider"
	"oneclickvirt/utils"
)

func (d *DockerProvider) routedNetworkSelection(config provider.InstanceConfig, networkType string) (utils.ContainerNetworkSelection, bool, error) {
	routed, present, err := provider.ResolveRoutedIPv6(config)
	if err != nil || !present {
		return utils.ContainerNetworkSelection{}, present, err
	}
	if !utils.NetworkTypeHasIPv6(networkType) {
		return utils.ContainerNetworkSelection{}, true, fmt.Errorf("已分配隧道路由IPv6，但网络类型 %q 未启用IPv6", strings.TrimSpace(networkType))
	}
	if output, execErr := d.sshClient.Execute(d.routedIPv6HostCheckCommand(routed)); execErr != nil {
		return utils.ContainerNetworkSelection{}, true, fmt.Errorf("隧道路由IPv6宿主机前置检查失败: output=%s: %w", utils.TruncateString(strings.TrimSpace(output), 2000), execErr)
	}
	selection := utils.ContainerNetworkSelection{
		Network:               d.runtime.IPv4Network,
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

func (d *DockerProvider) routedIPv6HostCheckCommand(routed provider.RoutedIPv6Config) string {
	command := routed.HostCheckCommand()
	if d.runtime.ProviderType != "orbstack" {
		return command
	}
	// OrbStack exposes a Docker-compatible client on macOS, but the runtime
	// network namespace lives in its Linux VM. A macOS-side Docker CLI cannot
	// attach the host-managed veth or advertise the routed public address.
	return `if [ "$(uname -s 2>/dev/null || true)" = Darwin ]; then
  echo 'OrbStack on macOS cannot provide host-routed public IPv6 through the Docker CLI; use a Linux node or connect directly to a Linux VM that owns the routed prefix' >&2
  exit 1
fi
` + command
}

// restoreRoutedIPv6AfterStart recreates the host-managed veth after a runtime
// stop/start. Docker can replace a container network namespace during restart,
// so this must not rely on the attachment made during initial creation.
func (d *DockerProvider) restoreRoutedIPv6AfterStart(name string) error {
	inspectCommand, err := provider.RoutedIPv6RuntimeLabelInspectCommand(d.runtime.CLI, name)
	if err != nil {
		return err
	}
	output, err := d.sshClient.Execute(inspectCommand)
	if err != nil {
		return d.failRoutedIPv6Restore(name, "读取", output, err)
	}
	routed, present, err := provider.RoutedIPv6ConfigFromRuntimeLabelOutput(output)
	if err != nil {
		return d.failRoutedIPv6Restore(name, "解析", output, err)
	}
	if !present {
		return nil
	}
	attachCommand, err := routed.RoutedIPv6VethCommand(d.runtime.CLI, name)
	if err != nil {
		return d.failRoutedIPv6Restore(name, "构造", "", err)
	}
	output, err = d.sshClient.Execute(attachCommand)
	if err != nil {
		return d.failRoutedIPv6Restore(name, "附加", output, err)
	}
	return nil
}

func (d *DockerProvider) failRoutedIPv6Restore(name, phase, output string, err error) error {
	_, _ = d.sshClient.Execute(fmt.Sprintf("%s stop %s 2>/dev/null || true", d.runtime.CLI, shellSingleQuote(name)))
	return fmt.Errorf("%s隧道路由IPv6失败，已停止实例以避免IPv6配置缺失: output=%s: %w", phase, utils.TruncateString(strings.TrimSpace(output), 4000), err)
}
