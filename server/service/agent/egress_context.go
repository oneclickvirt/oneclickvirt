package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"

	"gorm.io/gorm"
)

func instanceEgressKey(instance *providerModel.Instance) string {
	if value := strings.TrimSpace(instance.UUID); value != "" {
		return value
	}
	return strconv.FormatUint(uint64(instance.ID), 10)
}

func (s *InstanceEgressService) loadContext(ctx context.Context, instanceID uint) (*providerModel.Instance, *providerModel.Provider, *monitoringModel.MonitoringConfig, error) {
	if s == nil || s.db == nil {
		return nil, nil, nil, fmt.Errorf("数据库连接不可用")
	}
	var instance providerModel.Instance
	if err := s.db.WithContext(ctx).First(&instance, instanceID).Error; err != nil {
		return nil, nil, nil, err
	}
	node, config, err := s.loadProviderContext(ctx, instance.ProviderID)
	if err != nil {
		return nil, nil, nil, err
	}
	return &instance, node, config, nil
}

func (s *InstanceEgressService) loadProviderContext(ctx context.Context, providerID uint) (*providerModel.Provider, *monitoringModel.MonitoringConfig, error) {
	if s == nil || s.db == nil {
		return nil, nil, fmt.Errorf("数据库连接不可用")
	}
	var node providerModel.Provider
	if err := s.db.WithContext(ctx).First(&node, providerID).Error; err != nil {
		return nil, nil, err
	}
	config, err := GetMonitoringConfig(s.db.WithContext(ctx), node.ID)
	if err != nil {
		return nil, nil, err
	}
	return &node, config, nil
}

func egressClient(node *providerModel.Provider, config *monitoringModel.MonitoringConfig) (*Client, error) {
	if !config.AgentInstalled && !node.IsReverseAgent() {
		return nil, fmt.Errorf("节点尚未安装Agent")
	}
	if strings.TrimSpace(config.AgentToken) == "" {
		return nil, fmt.Errorf("节点Agent令牌未配置")
	}
	host := ResolveAgentHost(node.Endpoint, node.AgentRemoteIP)
	if host == "" && node.IsReverseAgent() {
		host = "127.0.0.1"
	}
	if host == "" {
		return nil, fmt.Errorf("节点没有可用的Agent地址")
	}
	port := config.AgentPort
	if port == 0 {
		port = AgentPort
	}
	return GetClientWithMode(node.ID, host, port, config.AgentToken, node.IsReverseAgent()), nil
}

func validateEgressProfileTransport(node *providerModel.Provider, profile *EgressProfileRequest) error {
	if node == nil || profile == nil {
		return fmt.Errorf("出口配置传输参数不完整")
	}
	if node.IsReverseAgent() || profile.TunnelType != "wireguard" {
		return nil
	}
	if managedWireGuard(profile.WireGuard) {
		return fmt.Errorf("托管WireGuard密钥仅允许通过反向Agent传输，当前SSH/HTTP节点不支持安全下发")
	}
	return nil
}

func (s *InstanceEgressService) rejectPersistedManagedWireGuard(ctx context.Context, node *providerModel.Provider, req *InstanceEgressBindRequest) error {
	if node == nil || req == nil || node.IsReverseAgent() || req.Profile.TunnelType != "wireguard" {
		return nil
	}
	if managedWireGuard(req.Profile.WireGuard) {
		return validateEgressProfileTransport(node, &req.Profile)
	}
	var existing monitoringModel.EgressDesiredProfile
	err := s.db.WithContext(ctx).Where("provider_id = ? AND profile_id = ?", node.ID, req.Profile.ID).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var previous EgressProfileRequest
	if json.Unmarshal([]byte(existing.ConfigJSON), &previous) == nil && managedWireGuard(previous.WireGuard) {
		return validateEgressProfileTransport(node, &previous)
	}
	return nil
}

func splitEgressInterfaces(raw string) []string {
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		result = append(result, part)
	}
	return result
}

func (s *InstanceEgressService) loadTraffic(ctx context.Context, instanceID, providerID uint) InstanceEgressTraffic {
	var monitor monitoringModel.AgentMonitor
	if err := s.db.WithContext(ctx).
		Where("instance_id = ? AND provider_id = ? AND is_enabled = ?", instanceID, providerID, true).
		First(&monitor).Error; err != nil {
		return InstanceEgressTraffic{}
	}
	lastSync := monitor.LastSyncAt
	var lastSyncPtr *time.Time
	if !lastSync.IsZero() {
		lastSyncPtr = &lastSync
	}
	return InstanceEgressTraffic{
		Monitored:  true,
		Source:     "instance-monitor",
		Interfaces: splitEgressInterfaces(monitor.Interfaces),
		BytesIn:    monitor.LastTrafficBytesIn,
		BytesOut:   monitor.LastTrafficBytesOut,
		LastSyncAt: lastSyncPtr,
	}
}

func endpointAddress(value string) (netip.Addr, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return netip.Addr{}, false
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Hostname() != "" {
		value = parsed.Hostname()
	} else if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	} else {
		value = strings.Trim(value, "[]")
	}
	address, err := netip.ParseAddr(value)
	if err != nil || address.Zone() != "" {
		return netip.Addr{}, false
	}
	return address.Unmap(), true
}

func providerEgressHostAddresses(node *providerModel.Provider) map[netip.Addr]struct{} {
	result := make(map[netip.Addr]struct{}, 3)
	if node == nil {
		return result
	}
	for _, candidate := range []string{node.Endpoint, node.PortIP, node.AgentRemoteIP} {
		if address, ok := endpointAddress(candidate); ok {
			result[address] = struct{}{}
		}
	}
	return result
}

func normalizeInstanceEgressAddress(candidate string, family int) (netip.Addr, error) {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return netip.Addr{}, nil
	}
	var address netip.Addr
	var err error
	if strings.Contains(candidate, "/") {
		var prefix netip.Prefix
		prefix, err = netip.ParsePrefix(candidate)
		address = prefix.Addr()
	} else {
		address, err = netip.ParseAddr(candidate)
	}
	address = address.Unmap()
	if err != nil || !address.IsValid() || address.Zone() != "" || address.IsUnspecified() || address.IsMulticast() ||
		(family == 4 && !address.Is4()) || (family == 6 && !address.Is6()) {
		return netip.Addr{}, fmt.Errorf("实例数据库包含无效的IPv%d出口源地址", family)
	}
	return address, nil
}

func firstSafeInstanceEgressAddress(candidates []string, family int, hostAddresses map[netip.Addr]struct{}) (string, error) {
	for _, candidate := range candidates {
		address, err := normalizeInstanceEgressAddress(candidate, family)
		if err != nil {
			return "", err
		}
		if !address.IsValid() {
			continue
		}
		if _, isHostAddress := hostAddresses[address]; isHostAddress {
			continue
		}
		return address.String(), nil
	}
	return "", nil
}

func instanceEgressNetworkType(instance *providerModel.Instance, node *providerModel.Provider) string {
	networkType := strings.ToLower(strings.TrimSpace(instance.NetworkType))
	if networkType == "" && node != nil {
		networkType = strings.ToLower(strings.TrimSpace(node.NetworkType))
	}
	return networkType
}

// instanceEgressSources returns only addresses visible as packet sources on
// the node. NAT modes must never use PublicIP/PublicIPv6 because those fields
// can contain the node's shared address after SNAT. Each family contributes at
// most one identity to prevent one instance binding from capturing host flows.
func instanceEgressSources(instance *providerModel.Instance, node *providerModel.Provider) ([]string, error) {
	if instance == nil {
		return nil, fmt.Errorf("实例不能为空")
	}
	hostAddresses := providerEgressHostAddresses(node)
	networkType := instanceEgressNetworkType(instance, node)
	var ipv4Candidates, ipv6Candidates []string
	switch networkType {
	case "nat_ipv4":
		ipv4Candidates = []string{instance.PrivateIP}
	case "nat_ipv4_ipv6":
		ipv4Candidates = []string{instance.PrivateIP}
		ipv6Candidates = []string{instance.IPv6Address}
	case "dedicated_ipv4":
		ipv4Candidates = []string{instance.PublicIP, instance.PrivateIP}
	case "dedicated_ipv4_ipv6":
		ipv4Candidates = []string{instance.PublicIP, instance.PrivateIP}
		ipv6Candidates = []string{instance.PublicIPv6, instance.IPv6Address}
	case "ipv6_only":
		ipv6Candidates = []string{instance.PublicIPv6, instance.IPv6Address}
	case "no_port_mapping":
		ipv4Candidates = []string{instance.PrivateIP}
		ipv6Candidates = []string{instance.IPv6Address}
	default:
		return nil, fmt.Errorf("无法为网络类型%q安全推导实例出口源地址", networkType)
	}
	result := make([]string, 0, 2)
	for _, selection := range []struct {
		candidates []string
		family     int
	}{{ipv4Candidates, 4}, {ipv6Candidates, 6}} {
		address, err := firstSafeInstanceEgressAddress(selection.candidates, selection.family, hostAddresses)
		if err != nil {
			return nil, err
		}
		if address != "" {
			result = append(result, address)
		}
	}
	return result, nil
}

func rejectHostEgressSources(sources []string, node *providerModel.Provider) error {
	hostAddresses := providerEgressHostAddresses(node)
	for _, source := range sources {
		prefix, err := netip.ParsePrefix(source)
		if err != nil {
			return fmt.Errorf("实例源地址格式无效")
		}
		for hostAddress := range hostAddresses {
			if prefix.Addr().BitLen() == hostAddress.BitLen() && prefix.Contains(hostAddress) {
				return fmt.Errorf("实例源地址不能包含节点管理或公网地址%s", hostAddress)
			}
		}
	}
	return nil
}

func mergeInstanceEgressSources(explicitSources, derivedSources []string, node *providerModel.Provider) ([]string, error) {
	combined := make([]string, 0, len(explicitSources)+len(derivedSources))
	combined = append(combined, explicitSources...)
	combined = append(combined, derivedSources...)
	normalized, err := normalizeBindingSources(combined)
	if err != nil {
		return nil, err
	}
	if err := rejectHostEgressSources(normalized, node); err != nil {
		return nil, err
	}
	return normalized, nil
}

func desiredExplicitEgressSources(desired *monitoringModel.EgressDesiredBinding, instance *providerModel.Instance) ([]string, error) {
	if desired == nil {
		return nil, fmt.Errorf("控制端出口绑定不存在")
	}
	if strings.TrimSpace(desired.ExplicitSourcesJSON) != "" {
		var explicit []string
		if err := json.Unmarshal([]byte(desired.ExplicitSourcesJSON), &explicit); err != nil {
			return nil, fmt.Errorf("控制端出口显式源地址损坏")
		}
		return normalizeBindingSources(explicit)
	}

	// Upgrade compatibility: old rows mixed explicit and automatically-derived
	// sources. Remove exact instance inventory identities, then re-derive them
	// under the current network-mode safety policy.
	var previous []string
	if err := json.Unmarshal([]byte(desired.SourcesJSON), &previous); err != nil {
		return nil, fmt.Errorf("控制端出口绑定源地址损坏")
	}
	knownAutomatic := make(map[netip.Addr]struct{}, 4)
	for _, candidate := range []string{instance.PrivateIP, instance.PublicIP, instance.IPv6Address, instance.PublicIPv6} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		var address netip.Addr
		if strings.Contains(candidate, "/") {
			if prefix, err := netip.ParsePrefix(candidate); err == nil {
				address = prefix.Addr().Unmap()
			}
		} else if parsed, err := netip.ParseAddr(candidate); err == nil {
			address = parsed.Unmap()
		}
		if address.IsValid() {
			knownAutomatic[address] = struct{}{}
		}
	}
	normalized, err := normalizeBindingSources(previous)
	if err != nil {
		return nil, err
	}
	explicit := make([]string, 0, len(normalized))
	for _, source := range normalized {
		prefix, _ := netip.ParsePrefix(source)
		if prefix.Bits() == prefix.Addr().BitLen() {
			if _, automatic := knownAutomatic[prefix.Addr().Unmap()]; automatic {
				continue
			}
		}
		explicit = append(explicit, source)
	}
	return explicit, nil
}

func instanceEgressInterfaces(instance *providerModel.Instance, monitor *monitoringModel.AgentMonitor) (*string, *string) {
	var v4, v6 *string
	if candidate := strings.TrimSpace(instance.PmacctInterfaceV4); candidate != "" {
		v4 = &candidate
	}
	if candidate := strings.TrimSpace(instance.PmacctInterfaceV6); candidate != "" {
		v6 = &candidate
	}
	if monitor != nil {
		interfaces := splitEgressInterfaces(monitor.Interfaces)
		if v4 == nil && len(interfaces) > 0 {
			candidate := interfaces[0]
			v4 = &candidate
		}
		if v6 == nil && len(interfaces) > 1 {
			candidate := interfaces[1]
			v6 = &candidate
		}
	}
	return v4, v6
}

func deriveEgressCapabilities(instance *providerModel.Instance, node *providerModel.Provider) (bool, string, []string) {
	nativeSupported := false
	recommendedMode := "native"
	reasons := make([]string, 0, 3)
	providerType := strings.ToLower(strings.TrimSpace(node.Type))
	// Native mode is intentionally an explicit allowlist. Unknown adapters,
	// externally managed CNIs, and passthrough links must report a qualified
	// gateway/CNI mode instead of claiming transparent host coverage.
	switch providerType {
	case "docker", "podman", "containerd", "lxd", "incus", "proxmox", "proxmoxve", "qemu", "libvirt":
		nativeSupported = true
	default:
		if providerType == "kubevirt" {
			recommendedMode = "cni"
		} else {
			recommendedMode = "gateway"
		}
		reasons = append(reasons, fmt.Sprintf("Provider类型%s不在已验证的宿主netfilter白名单中", providerType))
	}
	networkType := strings.ToLower(strings.TrimSpace(instance.NetworkType))
	if networkType == "host" || networkType == "host_network" || networkType == "hostnetwork" {
		nativeSupported = false
		recommendedMode = "gateway"
		reasons = append(reasons, "host network实例可能绕过宿主netfilter，不能静默声明native支持")
	}
	// Some adapters expose rootless/host-network hints in Config. Only parse
	// boolean hints; configuration values are never interpolated into commands.
	var hints map[string]interface{}
	if json.Unmarshal([]byte(node.Config), &hints) == nil {
		for _, key := range []string{"rootless", "hostNetwork", "host_network", "externalCNI", "external_cni", "macvlan", "macvtap", "sriov", "passthrough", "passthroughNIC", "passthrough_nic"} {
			if value, ok := hints[key].(bool); ok && value {
				nativeSupported = false
				if strings.Contains(strings.ToLower(key), "cni") || strings.Contains(strings.ToLower(key), "sriov") {
					recommendedMode = "cni"
				} else {
					recommendedMode = "gateway"
				}
				reasons = append(reasons, fmt.Sprintf("节点网络提示%s会绕过宿主Agent的可验证数据面", key))
				break
			}
		}
		for _, key := range []string{"networkMode", "network_mode", "cniMode", "cni_mode"} {
			if value, ok := hints[key].(string); ok {
				mode := strings.ToLower(strings.TrimSpace(value))
				if mode == "host" || mode == "external" || mode == "macvlan" || mode == "macvtap" || mode == "sriov" || mode == "passthrough" {
					nativeSupported = false
					recommendedMode = "gateway"
					reasons = append(reasons, fmt.Sprintf("节点网络模式%s不保证宿主netfilter覆盖", mode))
				}
			}
		}
	}
	if nativeSupported && strings.TrimSpace(instance.PmacctInterfaceV4) == "" && strings.TrimSpace(instance.PmacctInterfaceV6) == "" {
		nativeSupported = false
		recommendedMode = "gateway"
		reasons = append(reasons, "未探测到实例的宿主可见veth/TAP入口接口")
	}
	if len(reasons) > 0 && recommendedMode == "native" {
		recommendedMode = "gateway"
	}
	return nativeSupported, recommendedMode, reasons
}

func applyBindingTraffic(status *InstanceEgressStatus) {
	if status.Binding == nil {
		return
	}
	status.Traffic.Monitored = true
	status.Traffic.Source = "egress-binding"
	status.Traffic.BytesIn = status.Binding.TrafficBytesIn
	status.Traffic.BytesOut = status.Binding.TrafficBytesOut
	status.Traffic.DroppedBytes = status.Binding.TrafficBytesDropped
	status.Traffic.UpdatedAt = status.Binding.UpdatedAt
	status.FailClosedEnforced = status.Binding.FailClosedEnforced
	interfaces := make([]string, 0, 2)
	for _, candidate := range []*string{status.Binding.InterfaceV4, status.Binding.InterfaceV6, status.Binding.Interface} {
		if candidate == nil || strings.TrimSpace(*candidate) == "" {
			continue
		}
		value := strings.TrimSpace(*candidate)
		if !slices.Contains(interfaces, value) {
			interfaces = append(interfaces, value)
		}
	}
	if len(interfaces) > 0 {
		status.Traffic.Interfaces = interfaces
	}
}

func enrichEffectiveEgress(status *InstanceEgressStatus) {
	profileID := status.ConfiguredProfileID
	if status.Binding != nil {
		profileID = status.Binding.ProfileID
	}
	for i := range status.Profiles {
		profile := &status.Profiles[i]
		if profile.ID != profileID {
			continue
		}
		status.ConfiguredProfileID = profile.ID
		if profile.PublicIPv4 != nil {
			status.ExpectedIPv4 = *profile.PublicIPv4
		}
		if profile.PublicIPv6 != nil {
			status.ExpectedIPv6 = *profile.PublicIPv6
		}
		status.FailClosedRequired = profile.FailClosed
		status.EffectiveVerified = status.Binding != nil &&
			status.Binding.State == "applied" && profile.Status == "applied" &&
			status.Binding.FailClosedEnforced != nil && *status.Binding.FailClosedEnforced
		if status.EffectiveVerified {
			status.EffectiveIPv4 = status.ExpectedIPv4
			status.EffectiveIPv6 = status.ExpectedIPv6
		}
		return
	}
}
