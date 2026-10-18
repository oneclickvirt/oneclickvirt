package podman

import (
	"context"
	"fmt"
	"strings"

	"oneclickvirt/utils"
)

func (p *PodmanProvider) removeIPv4FromPodmanIPv6Attachment(name string, ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	addressFormat := fmt.Sprintf(`{{with index .NetworkSettings.Networks %q}}{{if .IPAddress}}{{.IPAddress}}/{{.IPPrefixLen}}{{end}}{{end}}`, ipv6Network)
	gatewayFormat := fmt.Sprintf(`{{with index .NetworkSettings.Networks %q}}{{.Gateway}}{{end}}`, ipv6Network)
	command := `
set -eu
container=@CONTAINER@
ipv4_cidr=$(@PODMAN@ inspect "$container" --format @IPV4_FORMAT@)
[ -n "$ipv4_cidr" ] || exit 0
case "$ipv4_cidr" in *[!0-9./]*) exit 1 ;; */*) ;; *) exit 1 ;; esac
pid=$(@PODMAN@ inspect "$container" --format '{{.State.Pid}}')
case "$pid" in ''|0|*[!0-9]*) exit 1 ;; esac
gateway=$(@PODMAN@ inspect "$container" --format @GATEWAY_FORMAT@)
addresses=$(nsenter -t "$pid" -n ip -o -4 addr show)
routes=$(nsenter -t "$pid" -n ip -4 route show default)
iface=$(printf '%s\n' "$addresses" | awk -v cidr="$ipv4_cidr" '$4 == cidr {print $2; exit}')
iface=${iface%%@*}
if [ -z "$iface" ] && [ -n "$gateway" ]; then
  iface=$(printf '%s\n' "$routes" | awk -v gateway="$gateway" '$1 == "default" && $2 == "via" && $3 == gateway {for (i = 1; i <= NF; i++) if ($i == "dev") {print $(i + 1); exit}}')
fi
if [ -z "$iface" ]; then
  if printf '%s\n' "$addresses" | awk -v cidr="$ipv4_cidr" '$4 == cidr {found=1} END {exit !found}'; then
    printf '%s\n' 'Podman IPv6 attachment IPv4 address has no discoverable interface' >&2
    exit 1
  fi
  if [ -n "$gateway" ] && printf '%s\n' "$routes" | awk -v gateway="$gateway" '$1 == "default" && $2 == "via" && $3 == gateway {found=1} END {exit !found}'; then
    printf '%s\n' 'Podman IPv6 attachment IPv4 default route has no discoverable interface' >&2
    exit 1
  fi
  exit 0
fi
while nsenter -t "$pid" -n ip -4 route show default dev "$iface" | grep -q .; do
  nsenter -t "$pid" -n ip -4 route del default dev "$iface" 2>/dev/null || break
done
nsenter -t "$pid" -n ip -4 addr del "$ipv4_cidr" dev "$iface" 2>/dev/null || true
if nsenter -t "$pid" -n ip -o -4 addr show dev "$iface" | awk -v cidr="$ipv4_cidr" '$4 == cidr {found=1} END {exit !found}'; then
  printf '%s\n' 'Podman IPv6 attachment retained its unintended IPv4 address' >&2
  exit 1
fi
if nsenter -t "$pid" -n ip -4 route show default dev "$iface" | grep -q .; then
  printf '%s\n' 'Podman IPv6 attachment retained its unintended IPv4 default route' >&2
  exit 1
fi
`
	command = strings.NewReplacer(
		"@CONTAINER@", shellSingleQuote(name),
		"@PODMAN@", cliName,
		"@IPV4_FORMAT@", shellSingleQuote(addressFormat),
		"@GATEWAY_FORMAT@", shellSingleQuote(gatewayFormat),
	).Replace(command)
	output, err := utils.ExecuteShellCommandContext(ctx, p.sshClient, command)
	if err != nil {
		return fmt.Errorf("container=%s output=%s: %w", name, utils.TruncateString(strings.TrimSpace(output), 1500), err)
	}
	return nil
}

func (p *PodmanProvider) restoreIPv4IsolationAfterStart(name string, ctx context.Context) error {
	format := fmt.Sprintf(`{{index .Config.Labels %q}}{{println}}{{range $network, $config := .NetworkSettings.Networks}}{{$network}}{{println}}{{end}}`, ipv6NoIPv4Label)
	command := fmt.Sprintf("%s inspect %s --format %s", cliName, shellSingleQuote(name), shellSingleQuote(format))
	output, err := utils.ExecuteShellCommandContext(ctx, p.sshClient, command)
	if err != nil {
		return fmt.Errorf("读取Podman IPv4隔离状态失败: %w", err)
	}
	fields := strings.Fields(output)
	if len(fields) == 0 || !strings.EqualFold(fields[0], "true") {
		return nil
	}
	for _, network := range fields[1:] {
		if network == ipv6Network {
			return p.removeIPv4FromPodmanIPv6Attachment(name, ctx)
		}
	}
	return fmt.Errorf("container %s requires IPv4 isolation but is no longer attached to %s", name, ipv6Network)
}
