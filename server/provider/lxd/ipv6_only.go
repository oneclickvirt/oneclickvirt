package lxd

import (
	"context"
	"fmt"
	"strings"

	"oneclickvirt/provider"
	"oneclickvirt/utils"
)

func lxdIPv6OnlyIsolationCommand(instanceName string, networkConfig NetworkConfig) string {
	name := shellSingleQuote(instanceName)
	return fmt.Sprintf(`set -eu
name=%s
type="$(lxc config device get "$name" eth1 type)"
nictype="$(lxc config device get "$name" eth1 nictype)"
if [ "$type" != nic ] || [ "$nictype" != routed ]; then
  printf 'refusing IPv6-only isolation without routed eth1: type=%%s nictype=%%s\n' "$type" "$nictype" >&2
  exit 1
fi
if local_type="$(lxc config device get "$name" eth0 type 2>/dev/null)"; then
  if [ "$local_type" != nic ] && [ "$local_type" != none ]; then
    printf 'refusing to replace unrelated local eth0 device: type=%%s\n' "$local_type" >&2
    exit 1
  fi
  lxc config device remove "$name" eth0
fi
lxc config device add "$name" eth0 none
lxc config device set "$name" eth1 limits.egress %s
lxc config device set "$name" eth1 limits.ingress %s
lxc config device set "$name" eth1 limits.max %s`, name,
		shellSingleQuote(fmt.Sprintf("%dMbit", networkConfig.OutSpeed)),
		shellSingleQuote(fmt.Sprintf("%dMbit", networkConfig.InSpeed)),
		shellSingleQuote(fmt.Sprintf("%dMbit", max(networkConfig.InSpeed, networkConfig.OutSpeed))))
}

func lxdIPv6OnlyDNSCommand(instanceName string) string {
	guestScript := `set -eu
if [ -L /etc/resolv.conf ]; then rm -f -- /etc/resolv.conf; fi
if [ -e /etc/resolv.conf ] && [ ! -f /etc/resolv.conf ]; then
  printf '%s\n' 'refusing to replace non-regular /etc/resolv.conf' >&2
  exit 1
fi
tmp="$(mktemp /etc/.resolv.conf.oneclickvirt.XXXXXX)"
trap 'rm -f -- "$tmp"' EXIT HUP INT TERM
printf '%s\n' 'nameserver 2606:4700:4700::1111' 'nameserver 2001:4860:4860::8888' > "$tmp"
chmod 0644 "$tmp"
mv -f -- "$tmp" /etc/resolv.conf
trap - EXIT HUP INT TERM`
	name := shellSingleQuote(instanceName)
	return fmt.Sprintf(`set -eu
ready=0
attempt=0
while [ "$attempt" -lt 90 ]; do
  if lxc exec %s -- true >/dev/null 2>&1; then ready=1; break; fi
  attempt=$((attempt + 1))
  sleep 2
done
[ "$ready" -eq 1 ]
lxc exec %s -- sh -ceu %s`, name, name, shellSingleQuote(guestScript))
}

func (l *LXDProvider) restoreIPv6OnlyDNS(config provider.InstanceConfig) error {
	networkType := strings.TrimSpace(l.config.NetworkType)
	if config.Metadata != nil {
		if configured := strings.TrimSpace(config.Metadata["network_type"]); configured != "" {
			networkType = configured
		}
	}
	if !strings.EqualFold(networkType, "ipv6_only") {
		return nil
	}
	if l.sshClient == nil {
		return fmt.Errorf("restore IPv6-only DNS: SSH executor is unavailable")
	}
	output, err := l.sshClient.Execute(lxdIPv6OnlyDNSCommand(config.Name))
	if err != nil {
		return fmt.Errorf("restore IPv6-only DNS: output=%s: %w", utils.TruncateString(output, 1200), err)
	}
	return nil
}

func (l *LXDProvider) enforceIPv6OnlyNetwork(ctx context.Context, instanceName string, networkConfig NetworkConfig) error {
	if networkConfig.NetworkType != "ipv6_only" {
		return nil
	}
	if l.sshClient == nil {
		return fmt.Errorf("IPv6-only isolation requires an SSH executor")
	}
	if err := l.sshStopInstance(ctx, instanceName); err != nil {
		return fmt.Errorf("stop instance for IPv6-only isolation: %w", err)
	}
	output, err := l.sshClient.Execute(lxdIPv6OnlyIsolationCommand(instanceName, networkConfig))
	if err != nil {
		startErr := l.sshStartInstance(ctx, instanceName)
		if startErr != nil {
			return fmt.Errorf("apply IPv6-only isolation: output=%s: %w; recovery start failed: %v",
				utils.TruncateString(output, 1200), err, startErr)
		}
		return fmt.Errorf("apply IPv6-only isolation: output=%s: %w", utils.TruncateString(output, 1200), err)
	}
	if err := l.sshStartInstance(ctx, instanceName); err != nil {
		return fmt.Errorf("start IPv6-only instance: %w", err)
	}
	output, err = l.sshClient.Execute(lxdIPv6OnlyDNSCommand(instanceName))
	if err != nil {
		return fmt.Errorf("configure IPv6-only guest DNS: output=%s: %w", utils.TruncateString(output, 1200), err)
	}
	return nil
}
