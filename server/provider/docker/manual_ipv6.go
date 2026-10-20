package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"oneclickvirt/utils"
)

const (
	dockerManualIPv6Helper = "/usr/local/bin/docker-ipv6-attach.sh"
	dockerManualIPv6Label  = "oneclickvirt.network.docker-manual-ipv6"
	dockerIPv6OnlyLabel    = "oneclickvirt.network.ipv6-no-ipv4"
)

func dockerInstallerNetworkLabels(selection utils.ContainerNetworkSelection) string {
	labels := ""
	if selection.ManualIPv6 {
		address := selection.StaticIPv6
		if address == "" {
			address = "auto"
		}
		labels += " --label " + shellSingleQuote(dockerManualIPv6Label+"="+address)
	}
	if selection.IPv6Only {
		labels += " --label " + shellSingleQuote(dockerIPv6OnlyLabel+"=true")
	}
	return labels
}

func (d *DockerProvider) attachDockerInstallerIPv6(ctx context.Context, name, address string) error {
	command := shellSingleQuote(dockerManualIPv6Helper) + " " + shellSingleQuote(name)
	if address != "" {
		canonical, err := utils.NormalizeIPv6Address(address)
		if err != nil {
			return fmt.Errorf("Docker路由IPv6地址无效: %w", err)
		}
		command += " " + shellSingleQuote(canonical)
	}
	if output, err := utils.ExecuteShellCommandContext(ctx, d.sshClient, command); err != nil {
		return fmt.Errorf("附加Docker安装器路由IPv6失败: output=%s: %w", utils.TruncateString(output, 2000), err)
	}
	return nil
}

func (d *DockerProvider) restoreDockerInstallerIPv6AfterStart(ctx context.Context, name string) error {
	if d.runtime.ProviderType != "docker" {
		return nil
	}
	command := fmt.Sprintf("%s inspect %s --format '{{json .Config.Labels}}'", d.runtime.CLI, shellSingleQuote(name))
	output, err := utils.ExecuteShellCommandContext(ctx, d.sshClient, command)
	if err != nil {
		return d.failRoutedIPv6Restore(ctx, name, "读取Docker网络标签", output, err)
	}
	labels := map[string]string{}
	if strings.TrimSpace(output) == "" || strings.TrimSpace(output) == "null" {
		return nil
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &labels); err != nil {
		return d.failRoutedIPv6Restore(ctx, name, "解析Docker网络标签", output, err)
	}
	if address := labels[dockerManualIPv6Label]; address != "" {
		if address == "auto" {
			address = ""
		}
		if err := d.attachDockerInstallerIPv6(ctx, name, address); err != nil {
			return d.failRoutedIPv6Restore(ctx, name, "恢复Docker路由IPv6", "", err)
		}
	}
	if strings.EqualFold(labels[dockerIPv6OnlyLabel], "true") {
		if err := d.removeDockerIPv4(ctx, name); err != nil {
			return d.failRoutedIPv6Restore(ctx, name, "恢复纯IPv6隔离", "", err)
		}
	}
	return nil
}

// IPv6-only is enforced in the actual namespace, even on older Docker versions
// whose bridge IPAM always assigns an IPv4 address alongside IPv6.
func (d *DockerProvider) removeDockerIPv4(ctx context.Context, name string) error {
	command := fmt.Sprintf(`set -eu
pid=$(%s inspect %s --format '{{.State.Pid}}')
case "$pid" in ''|0|*[!0-9]*) exit 1 ;; esac
nsenter -t "$pid" -n ip -4 route flush table main
nsenter -t "$pid" -n ip -4 address flush scope global
remaining=$(nsenter -t "$pid" -n ip -o -4 address show scope global)
test -z "$remaining"
remaining=$(nsenter -t "$pid" -n ip -4 route show default)
test -z "$remaining"
`, d.runtime.CLI, shellSingleQuote(name))
	if output, err := utils.ExecuteShellCommandContext(ctx, d.sshClient, command); err != nil {
		return fmt.Errorf("Docker纯IPv6实例仍有IPv4配置: output=%s: %w", utils.TruncateString(output, 1500), err)
	}
	return nil
}

// Read the installer registry once per inventory operation. Public /128s are
// attached outside IPAM, so Docker inspect only reports the internal ULA.
func (d *DockerProvider) dockerInstallerIPv6Allocations() map[string]string {
	allocations := map[string]string{}
	if d.runtime.ProviderType != "docker" {
		return allocations
	}
	output, err := d.sshClient.Execute("if test -r /usr/local/bin/docker_ipv6_allocations; then cat /usr/local/bin/docker_ipv6_allocations; fi")
	if err != nil {
		return allocations
	}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		address, err := utils.NormalizeIPv6Address(fields[1])
		if err == nil && utils.IsPublicIPv6(address) {
			allocations[fields[0]] = address
		}
	}
	return allocations
}
