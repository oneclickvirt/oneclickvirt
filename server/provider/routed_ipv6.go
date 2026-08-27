package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"oneclickvirt/utils"
)

// RoutedIPv6Config is controller-supplied host routing metadata for a static
// guest address. It is empty for native/provider-managed IPv6 pools.
type RoutedIPv6Config struct {
	Address         string
	CIDR            string
	Gateway         string
	Bridge          string
	Prefix          int
	TunnelID        uint
	TunnelInterface string
}

var routedIPv6BridgePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,14}$`)
var routedIPv6RuntimePattern = regexp.MustCompile(`^[A-Za-z0-9_./-]+$`)

const (
	routedIPv6LabelAddress         = "oneclickvirt.routed-ipv6.address"
	routedIPv6LabelCIDR            = "oneclickvirt.routed-ipv6.cidr"
	routedIPv6LabelGateway         = "oneclickvirt.routed-ipv6.gateway"
	routedIPv6LabelBridge          = "oneclickvirt.routed-ipv6.bridge"
	routedIPv6LabelTunnelID        = "oneclickvirt.routed-ipv6.tunnel-id"
	routedIPv6LabelTunnelInterface = "oneclickvirt.routed-ipv6.tunnel-interface"
)

// ResolveRoutedIPv6 validates the metadata contract once at the provider
// boundary. This prevents a routed address from falling through to an IPv6
// NAT/iptables path or being attached to an arbitrary interface.
func ResolveRoutedIPv6(config InstanceConfig) (RoutedIPv6Config, bool, error) {
	if config.Metadata == nil || strings.TrimSpace(config.Metadata["static_ipv6_cidr"]) == "" {
		return RoutedIPv6Config{}, false, nil
	}
	address, err := utils.NormalizeIPv6Address(config.Metadata["static_ipv6"])
	if err != nil {
		return RoutedIPv6Config{}, true, fmt.Errorf("隧道路由IPv6地址无效: %w", err)
	}
	gateway, err := utils.NormalizeIPv6Address(config.Metadata["static_ipv6_gateway"])
	if err != nil {
		return RoutedIPv6Config{}, true, fmt.Errorf("隧道路由IPv6网关无效: %w", err)
	}
	_, network, err := net.ParseCIDR(strings.TrimSpace(config.Metadata["static_ipv6_cidr"]))
	if err != nil || network == nil || network.IP.To4() != nil {
		return RoutedIPv6Config{}, true, fmt.Errorf("隧道路由IPv6前缀无效")
	}
	prefix, bits := network.Mask.Size()
	addressIP := net.ParseIP(address)
	gatewayIP := net.ParseIP(gateway)
	if bits != 128 || prefix < 1 || prefix > 127 || !network.Contains(addressIP) || !network.Contains(gatewayIP) {
		return RoutedIPv6Config{}, true, fmt.Errorf("隧道路由IPv6地址或网关不属于配置前缀")
	}
	if addressIP.Equal(gatewayIP) {
		return RoutedIPv6Config{}, true, fmt.Errorf("隧道路由IPv6地址不能与网关相同")
	}
	// Managed routed prefixes reserve the all-zero address except for RFC 6164
	// point-to-point /127 links, where both endpoints are usable.
	if prefix != 127 && addressIP.Equal(network.IP) {
		return RoutedIPv6Config{}, true, fmt.Errorf("隧道路由IPv6不能分配前缀网络地址")
	}
	bridge := strings.TrimSpace(config.Metadata["static_ipv6_bridge"])
	if bridge == "" {
		bridge = utils.RoutedIPv6BridgeName
	}
	if !routedIPv6BridgePattern.MatchString(bridge) {
		return RoutedIPv6Config{}, true, fmt.Errorf("隧道路由IPv6网桥名无效")
	}
	tunnelInterface := strings.TrimSpace(config.Metadata["static_ipv6_tunnel_interface"])
	if tunnelInterface != "" && (!routedIPv6BridgePattern.MatchString(tunnelInterface) || tunnelInterface == "lo") {
		return RoutedIPv6Config{}, true, fmt.Errorf("隧道路由IPv6隧道接口名无效")
	}
	tunnelID, _ := strconv.ParseUint(strings.TrimSpace(config.Metadata["static_ipv6_tunnel_id"]), 10, 32)
	return RoutedIPv6Config{Address: address, CIDR: network.String(), Gateway: gateway, Bridge: bridge, Prefix: prefix, TunnelID: uint(tunnelID), TunnelInterface: tunnelInterface}, true, nil
}

func (r RoutedIPv6Config) AddressCIDR() string {
	if r.Address == "" || r.Prefix < 0 {
		return ""
	}
	return r.Address + "/" + strconv.Itoa(r.Prefix)
}

// HostCheckCommand verifies the small set of host-side invariants shared by
// every backend that attaches a guest directly to a managed tunnel prefix.
// It is deliberately a single remote command: callers must not turn instance
// creation into a per-check SSH round trip.
func (r RoutedIPv6Config) HostCheckCommand() string {
	addressCIDR := r.Gateway + "/" + strconv.Itoa(r.Prefix)
	command := fmt.Sprintf(`set -eu
	if [ "$(uname -s 2>/dev/null || true)" != Linux ]; then
	  echo 'routed IPv6 guest networking requires a Linux node with a managed tunnel bridge; macOS/BSD NDP proxy mode cannot attach a guest interface' >&2
	  exit 1
	fi
	command -v ip >/dev/null 2>&1 || { echo 'iproute2 is unavailable' >&2; exit 1; }
	ip link show dev %s >/dev/null 2>&1 || { echo 'routed IPv6 bridge is missing' >&2; exit 1; }
ip -d link show dev %s 2>/dev/null | grep -F 'bridge' >/dev/null || { echo 'routed IPv6 parent is not a bridge' >&2; exit 1; }
ip -o -6 addr show dev %s | awk '{print $4}' | grep -Fx %s >/dev/null || { echo 'routed IPv6 bridge gateway is missing' >&2; exit 1; }
ip -6 route show %s | grep -F %s >/dev/null || { echo 'routed IPv6 bridge route is missing' >&2; exit 1; }
command -v sysctl >/dev/null 2>&1 || { echo 'sysctl is unavailable' >&2; exit 1; }
`,
		utils.ShellSingleQuote(r.Bridge),
		utils.ShellSingleQuote(r.Bridge),
		utils.ShellSingleQuote(r.Bridge), utils.ShellSingleQuote(addressCIDR),
		utils.ShellSingleQuote(r.CIDR), utils.ShellSingleQuote(" dev "+r.Bridge+" "))
	command += routedIPv6GlobalForwardingCheck()
	if r.TunnelInterface != "" {
		command += routedIPv6ForwardingCheck(r.TunnelInterface, "tunnel")
	}
	return command + routedIPv6ForwardingCheck(r.Bridge, "bridge")
}

// routedIPv6GlobalForwardingCheck covers both active guests and guest
// interfaces created later by a runtime. The tunnel lifecycle writes these
// settings persistently before a routed allocation reaches any backend.
func routedIPv6GlobalForwardingCheck() string {
	return `
[ "$(sysctl -n net.ipv6.conf.all.forwarding 2>/dev/null || echo 0)" = 1 ] || { echo 'routed IPv6 global forwarding is disabled (net.ipv6.conf.all.forwarding)' >&2; exit 1; }
[ "$(sysctl -n net.ipv6.conf.default.forwarding 2>/dev/null || echo 0)" = 1 ] || { echo 'routed IPv6 default forwarding is disabled (net.ipv6.conf.default.forwarding)' >&2; exit 1; }
`
}

func routedIPv6ForwardingCheck(interfaceName, role string) string {
	return fmt.Sprintf(`ip link show dev %s >/dev/null 2>&1 || { echo 'routed IPv6 %s is missing' >&2; exit 1; }
[ "$(sysctl -n %s)" = 1 ] || { echo 'routed IPv6 %s forwarding is disabled' >&2; exit 1; }
`, utils.ShellSingleQuote(interfaceName), role,
		utils.ShellSingleQuote("net.ipv6.conf."+interfaceName+".forwarding"), role)
}

// RoutedIPv6VethNames returns deterministic Linux interface names for one
// container/tunnel pair. The names are deliberately below IFNAMSIZ so the
// attach operation is idempotent and does not depend on a runtime-generated
// network name.
func RoutedIPv6VethNames(runtimeCLI, containerName string, tunnelID uint) (host, peer string) {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%d", runtimeCLI, containerName, tunnelID)))
	suffix := hex.EncodeToString(digest[:])[:8]
	return "oc6h" + suffix, "oc6p" + suffix
}

// RoutedIPv6VethCommand attaches a routed IPv6 address to a running
// Docker/Podman/nerdctl container without creating a macvlan. A veth peer is
// placed in the container network namespace and the host end is enslaved to
// the tunnel bridge, so the bridge-hosted gateway remains reachable.
//
// The command is intentionally one bounded shell operation. It validates the
// runtime-reported PID and every postcondition, and cleans up a stale host
// end before retrying an interrupted attach.
func (r RoutedIPv6Config) RoutedIPv6VethCommand(runtimeCLI, containerName string) (string, error) {
	runtimeCLI = strings.TrimSpace(runtimeCLI)
	containerName = strings.TrimSpace(containerName)
	if !routedIPv6RuntimePattern.MatchString(runtimeCLI) || runtimeCLI == "" {
		return "", fmt.Errorf("容器运行时命令无效")
	}
	if containerName == "" {
		return "", fmt.Errorf("容器名称为空")
	}
	if r.Address == "" || r.Gateway == "" || r.Prefix < 1 || r.Prefix > 127 || r.Bridge == "" {
		return "", fmt.Errorf("隧道路由IPv6 veth参数不完整")
	}
	hostIf, peerIf := RoutedIPv6VethNames(runtimeCLI, containerName, r.TunnelID)
	container := utils.ShellSingleQuote(containerName)
	bridge := utils.ShellSingleQuote(r.Bridge)
	addressCIDR := utils.ShellSingleQuote(r.AddressCIDR())
	gateway := utils.ShellSingleQuote(r.Gateway)
	host := utils.ShellSingleQuote(hostIf)
	peer := utils.ShellSingleQuote(peerIf)
	return fmt.Sprintf(`%s
command -v nsenter >/dev/null 2>&1 || { echo 'nsenter is unavailable' >&2; exit 1; }
pid=''
for attempt in $(seq 1 20); do
  pid=$(%s inspect %s --format '{{.State.Pid}}' 2>/dev/null | tr -d '[:space:]' || true)
  case "$pid" in
    ''|*[!0-9]*|0) sleep 0.25 ;;
    *) break ;;
  esac
done
case "$pid" in
  ''|*[!0-9]*|0) echo 'container network namespace is not ready' >&2; exit 1 ;;
esac
nsenter -t "$pid" -n true >/dev/null 2>&1 || { echo 'container network namespace is unavailable' >&2; exit 1; }
if ip link show dev %s >/dev/null 2>&1; then ip link delete dev %s 2>/dev/null || true; fi
if nsenter -t "$pid" -n ip link show dev %s >/dev/null 2>&1; then nsenter -t "$pid" -n ip link delete dev %s 2>/dev/null || true; fi
ip link add %s type veth peer name %s
cleanup() { ip link show dev %s >/dev/null 2>&1 && ip link delete dev %s 2>/dev/null || true; }
trap cleanup EXIT
ip link set dev %s master %s
ip link set dev %s up
ip link set dev %s netns "$pid"
nsenter -t "$pid" -n ip link set dev %s name oc6v6
nsenter -t "$pid" -n ip link set dev oc6v6 up
nsenter -t "$pid" -n ip -6 addr flush dev oc6v6 scope global
nsenter -t "$pid" -n ip -6 addr add %s dev oc6v6
nsenter -t "$pid" -n ip -6 route replace %s/128 dev oc6v6
nsenter -t "$pid" -n ip -6 route replace default via %s dev oc6v6
nsenter -t "$pid" -n ip -o -6 addr show dev oc6v6 | awk '{print $4}' | grep -Fx %s >/dev/null
nsenter -t "$pid" -n ip -6 route show default | grep -F %s >/dev/null
trap - EXIT
printf 'routed IPv6 veth attached: %%s/%%s\n' %s %s`,
		r.HostCheckCommand(), runtimeCLI, container,
		host, host, peer, peer,
		host, peer,
		host, host,
		host, bridge,
		host,
		peer,
		peer,
		addressCIDR,
		utils.ShellSingleQuote(r.Address), gateway,
		addressCIDR, gateway,
		hostIf, peerIf), nil
}

// RoutedIPv6ConfigFromSelection reconstructs validated tunnel metadata kept
// on a container runtime selection. Keeping these fields in the utils type
// avoids an import cycle while allowing the attach operation to share the
// same boundary validation as initial allocation.
func RoutedIPv6ConfigFromSelection(selection utils.ContainerNetworkSelection) (RoutedIPv6Config, error) {
	if !selection.RoutedVeth {
		return RoutedIPv6Config{}, fmt.Errorf("当前网络选择不是隧道路由IPv6")
	}
	metadata := map[string]string{
		"static_ipv6":                  selection.StaticIPv6,
		"static_ipv6_cidr":             selection.RoutedCIDR,
		"static_ipv6_gateway":          selection.RoutedGateway,
		"static_ipv6_bridge":           selection.RoutedBridge,
		"static_ipv6_tunnel_id":        strconv.FormatUint(uint64(selection.RoutedTunnelID), 10),
		"static_ipv6_tunnel_interface": selection.RoutedTunnelInterface,
	}
	routed, present, err := ResolveRoutedIPv6(InstanceConfig{Metadata: metadata})
	if err != nil {
		return RoutedIPv6Config{}, err
	}
	if !present {
		return RoutedIPv6Config{}, fmt.Errorf("隧道路由IPv6元数据为空")
	}
	return routed, nil
}

// RoutedIPv6VethAttachCommand builds the shared attach command from a
// controller selection and is used by Docker-like providers after create.
func RoutedIPv6VethAttachCommand(runtimeCLI, containerName string, selection utils.ContainerNetworkSelection) (string, error) {
	routed, err := RoutedIPv6ConfigFromSelection(selection)
	if err != nil {
		return "", err
	}
	return routed.RoutedIPv6VethCommand(runtimeCLI, containerName)
}

// RoutedIPv6RuntimeLabelArgs persists a validated routed allocation on a
// Docker-compatible container. The runtime retains labels through stop/start,
// allowing the provider to recreate the veth after a new network namespace is
// created without querying the controller database.
func RoutedIPv6RuntimeLabelArgs(selection utils.ContainerNetworkSelection) (string, error) {
	routed, err := RoutedIPv6ConfigFromSelection(selection)
	if err != nil {
		return "", err
	}
	labels := routed.runtimeLabels()
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	args := make([]string, 0, len(keys))
	for _, key := range keys {
		args = append(args, "--label "+utils.ShellSingleQuote(key+"="+labels[key]))
	}
	return strings.Join(args, " "), nil
}

// RoutedIPv6RuntimeLabelInspectCommand reads all controller-owned labels in
// one inspect call. Older containers lack the optional tunnel-interface
// label and continue through the bridge-only compatibility check.
func RoutedIPv6RuntimeLabelInspectCommand(runtimeCLI, containerName string) (string, error) {
	runtimeCLI = strings.TrimSpace(runtimeCLI)
	containerName = strings.TrimSpace(containerName)
	if !routedIPv6RuntimePattern.MatchString(runtimeCLI) || runtimeCLI == "" {
		return "", fmt.Errorf("容器运行时命令无效")
	}
	if containerName == "" {
		return "", fmt.Errorf("容器名称为空")
	}
	labelTemplate := func(label string) string {
		return fmt.Sprintf(`{{with index .Config.Labels %q}}{{.}}{{end}}`, label)
	}
	format := strings.Join([]string{
		labelTemplate(routedIPv6LabelAddress),
		labelTemplate(routedIPv6LabelCIDR),
		labelTemplate(routedIPv6LabelGateway),
		labelTemplate(routedIPv6LabelBridge),
		labelTemplate(routedIPv6LabelTunnelID),
		labelTemplate(routedIPv6LabelTunnelInterface),
	}, "\t")
	return fmt.Sprintf("%s inspect %s --format %s", runtimeCLI, utils.ShellSingleQuote(containerName), utils.ShellSingleQuote(format)), nil
}

// RoutedIPv6ConfigFromRuntimeLabelOutput validates a single line emitted by
// RoutedIPv6RuntimeLabelInspectCommand. Treat partial labels as an error so a
// restarted container never silently loses its promised routed address.
func RoutedIPv6ConfigFromRuntimeLabelOutput(output string) (RoutedIPv6Config, bool, error) {
	line := strings.TrimSpace(output)
	if line == "" {
		return RoutedIPv6Config{}, false, nil
	}
	parts := strings.Split(line, "\t")
	if len(parts) != 5 && len(parts) != 6 {
		return RoutedIPv6Config{}, true, fmt.Errorf("隧道路由IPv6运行时标签格式无效")
	}
	if len(parts) == 6 && strings.TrimSpace(parts[5]) == "" {
		parts = parts[:5]
	}
	hasValue := false
	hasMissingValue := false
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			hasMissingValue = true
			continue
		}
		hasValue = true
	}
	if !hasValue {
		return RoutedIPv6Config{}, false, nil
	}
	if hasMissingValue {
		return RoutedIPv6Config{}, true, fmt.Errorf("隧道路由IPv6运行时标签不完整")
	}
	metadata := map[string]string{
		"static_ipv6":           strings.TrimSpace(parts[0]),
		"static_ipv6_cidr":      strings.TrimSpace(parts[1]),
		"static_ipv6_gateway":   strings.TrimSpace(parts[2]),
		"static_ipv6_bridge":    strings.TrimSpace(parts[3]),
		"static_ipv6_tunnel_id": strings.TrimSpace(parts[4]),
	}
	if len(parts) == 6 {
		metadata["static_ipv6_tunnel_interface"] = strings.TrimSpace(parts[5])
	}
	routed, present, err := ResolveRoutedIPv6(InstanceConfig{Metadata: metadata})
	if err != nil {
		return RoutedIPv6Config{}, present, err
	}
	if !present {
		return RoutedIPv6Config{}, false, nil
	}
	return routed, true, nil
}

func (r RoutedIPv6Config) runtimeLabels() map[string]string {
	labels := map[string]string{
		routedIPv6LabelAddress:  r.Address,
		routedIPv6LabelCIDR:     r.CIDR,
		routedIPv6LabelGateway:  r.Gateway,
		routedIPv6LabelBridge:   r.Bridge,
		routedIPv6LabelTunnelID: strconv.FormatUint(uint64(r.TunnelID), 10),
	}
	if r.TunnelInterface != "" {
		labels[routedIPv6LabelTunnelInterface] = r.TunnelInterface
	}
	return labels
}

// RoutedIPv6RuntimeNetworkName is stable per tunnel and safe for Docker-like
// runtime network names. A routed network is kept separate from legacy
// ipv6_net so changing a tunnel cannot alter existing containers.
func RoutedIPv6RuntimeNetworkName(runtime string, routed RoutedIPv6Config) string {
	runtime = strings.ToLower(strings.TrimSpace(runtime))
	if runtime == "" {
		runtime = "runtime"
	}
	if routed.TunnelID > 0 {
		return fmt.Sprintf("oneclickvirt6-%s-%d", runtime, routed.TunnelID)
	}
	value := strings.NewReplacer(":", "-", "/", "-", " ", "").Replace(routed.CIDR)
	if len(value) > 32 {
		value = value[len(value)-32:]
	}
	return "oneclickvirt6-" + runtime + "-" + value
}

// RoutedIPv4RuntimeSubnet supplies an isolated private IPv4 side only when a
// dual-stack NAT runtime network is requested. It avoids reusing the existing
// provider network and is deterministic across retries.
func RoutedIPv4RuntimeSubnet(tunnelID uint) (subnet, gateway string) {
	third := byte(tunnelID % 240)
	if third < 10 {
		third += 10
	}
	return fmt.Sprintf("10.240.%d.0/24", third), fmt.Sprintf("10.240.%d.1", third)
}
