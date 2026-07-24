package agent

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

func cleanEgressValue(value, field string, max int, required bool) (string, error) {
	value = strings.TrimSpace(value)
	if required && value == "" {
		return "", fmt.Errorf("%s不能为空", field)
	}
	if len(value) > max || strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return "", fmt.Errorf("%s格式无效", field)
	}
	return value, nil
}

func validateEgressIdentifier(value, field string, max int, required bool) (string, error) {
	value, err := cleanEgressValue(value, field, max, required)
	if err != nil || value == "" {
		return value, err
	}
	if !egressIdentifierPattern.MatchString(value) {
		return "", fmt.Errorf("%s只能包含字母、数字、点、下划线、冒号和短横线", field)
	}
	return value, nil
}

func validateIPOrCIDR(value, field string, required bool) (string, error) {
	value, err := cleanEgressValue(value, field, 128, required)
	if err != nil || value == "" {
		return value, err
	}
	if strings.Contains(value, "/") {
		prefix, parseErr := netip.ParsePrefix(value)
		if parseErr != nil || prefix.Addr().IsUnspecified() || prefix.Addr().IsMulticast() {
			return "", fmt.Errorf("%s必须是有效的IP地址或CIDR", field)
		}
		return prefix.Masked().String(), nil
	}
	address, parseErr := netip.ParseAddr(value)
	if parseErr != nil || address.IsUnspecified() || address.IsMulticast() {
		return "", fmt.Errorf("%s必须是有效的IP地址或CIDR", field)
	}
	return address.String(), nil
}

func validateHostIP(value *string, field string, family int) error {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}
	raw, err := cleanEgressValue(*value, field, 128, true)
	if err != nil {
		return err
	}
	var address netip.Addr
	if strings.Contains(raw, "/") {
		prefix, parseErr := netip.ParsePrefix(raw)
		if parseErr != nil || prefix.Bits() != prefix.Addr().BitLen() {
			return fmt.Errorf("%s必须是单个IP地址", field)
		}
		address = prefix.Addr()
	} else {
		address, err = netip.ParseAddr(raw)
		if err != nil {
			return fmt.Errorf("%s必须是单个IP地址", field)
		}
	}
	if address.IsUnspecified() || address.IsMulticast() || (family == 4 && !address.Is4()) || (family == 6 && !address.Is6()) {
		return fmt.Errorf("%s地址族或格式无效", field)
	}
	*value = address.String()
	return nil
}

func validateWireGuardKey(value, field string, required bool) (string, error) {
	value, err := cleanEgressValue(value, field, 128, required)
	if err != nil || value == "" {
		return value, err
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return "", fmt.Errorf("%s必须是32字节的标准Base64 WireGuard密钥", field)
	}
	return value, nil
}

func validateWireGuardEndpoint(value string) (string, error) {
	value, err := cleanEgressValue(value, "WireGuard对端地址", 320, false)
	if err != nil || value == "" {
		return value, err
	}
	host, port, err := net.SplitHostPort(value)
	if err != nil || host == "" {
		return "", fmt.Errorf("WireGuard对端地址必须包含有效端口")
	}
	portNumber, err := strconv.ParseUint(port, 10, 16)
	if err != nil || portNumber == 0 {
		return "", fmt.Errorf("WireGuard对端端口无效")
	}
	if strings.IndexFunc(host, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && !strings.ContainsRune(".-:%", r)
	}) >= 0 {
		return "", fmt.Errorf("WireGuard对端主机名无效")
	}
	return value, nil
}

func validateWireGuardNetworks(values []string, field string, allowDefault, preserveHost bool) ([]string, error) {
	if len(values) > 64 {
		return nil, fmt.Errorf("%s最多允许64项", field)
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value, err := cleanEgressValue(value, field, 160, true)
		if err != nil || !strings.Contains(value, "/") {
			return nil, fmt.Errorf("%s必须是带前缀长度的有效网段", field)
		}
		prefix, err := netip.ParsePrefix(value)
		if err != nil || prefix.Addr().IsMulticast() || (prefix.Addr().IsUnspecified() && !(allowDefault && prefix.Bits() == 0)) {
			return nil, fmt.Errorf("%s包含无效网段", field)
		}
		normalized := prefix.Masked().String()
		if preserveHost {
			normalized = prefix.String()
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	return result, nil
}

func validateWireGuardRequest(req *EgressWireGuardRequest) error {
	if req == nil {
		return nil
	}
	var err error
	managed := true
	if req.Managed != nil {
		managed = *req.Managed
	} else {
		req.Managed = &managed
	}
	if req.PrivateKey, err = validateWireGuardKey(req.PrivateKey, "WireGuard私钥", false); err != nil {
		return err
	}
	if req.PeerPublicKey, err = validateWireGuardKey(req.PeerPublicKey, "WireGuard对端公钥", managed); err != nil {
		return err
	}
	if req.PresharedKey, err = validateWireGuardKey(req.PresharedKey, "WireGuard预共享密钥", false); err != nil {
		return err
	}
	if req.Endpoint, err = validateWireGuardEndpoint(req.Endpoint); err != nil {
		return err
	}
	if !managed && (req.PrivateKey != "" || req.PresharedKey != "") {
		return fmt.Errorf("非托管WireGuard配置不能提交私钥或预共享密钥")
	}
	req.Addresses, err = validateWireGuardNetworks(req.Addresses, "WireGuard本地地址", false, true)
	if err != nil {
		return err
	}
	if managed && len(req.Addresses) == 0 {
		return fmt.Errorf("托管WireGuard必须至少配置一个本地隧道地址")
	}
	if managed && len(req.AllowedIPs) == 0 {
		req.AllowedIPs = []string{"0.0.0.0/0", "::/0"}
	}
	req.AllowedIPs, err = validateWireGuardNetworks(req.AllowedIPs, "WireGuard允许网段", true, false)
	if err != nil {
		return err
	}
	if req.MTU != nil && (*req.MTU < 576 || *req.MTU > 9000) {
		return fmt.Errorf("WireGuard MTU必须在576-9000之间")
	}
	return nil
}

func normalizeBindingSources(values []string) ([]string, error) {
	if len(values) > 65 { // one legacy source may duplicate the complete list
		return nil, fmt.Errorf("实例源地址最多允许64项")
	}
	prefixes := make([]netip.Prefix, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		var prefix netip.Prefix
		var err error
		if strings.Contains(value, "/") {
			prefix, err = netip.ParsePrefix(value)
			prefix = prefix.Masked()
		} else {
			var address netip.Addr
			address, err = netip.ParseAddr(value)
			if err == nil {
				prefix = netip.PrefixFrom(address, address.BitLen())
			}
		}
		if err != nil || !prefix.IsValid() || prefix.Addr().IsUnspecified() || prefix.Addr().IsMulticast() {
			return nil, fmt.Errorf("实例源地址必须是有效的IPv4/IPv6地址或CIDR")
		}
		normalized := prefix.String()
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		prefixes = append(prefixes, prefix)
	}
	if len(prefixes) > 64 {
		return nil, fmt.Errorf("实例源地址最多允许64项")
	}
	for left := range prefixes {
		for right := left + 1; right < len(prefixes); right++ {
			if prefixes[left].Addr().BitLen() == prefixes[right].Addr().BitLen() &&
				(prefixes[left].Contains(prefixes[right].Addr()) || prefixes[right].Contains(prefixes[left].Addr())) {
				return nil, fmt.Errorf("同一绑定内的实例源地址不能重叠")
			}
		}
	}
	result := make([]string, 0, len(prefixes))
	for _, prefix := range prefixes {
		result = append(result, prefix.String())
	}
	return result, nil
}

// ValidateInstanceEgressBindRequest rejects multiline command output and
// shell-like identifiers before any value reaches the Agent API.
func ValidateInstanceEgressBindRequest(req *InstanceEgressBindRequest) error {
	if req == nil {
		return fmt.Errorf("请求不能为空")
	}
	var err error
	req.Profile.ID, err = validateEgressIdentifier(req.Profile.ID, "出口配置ID", 128, true)
	if err != nil {
		return err
	}
	req.Profile.Mode, err = cleanEgressValue(req.Profile.Mode, "出口模式", 16, true)
	if err != nil {
		return err
	}
	req.Profile.Mode = strings.ToLower(req.Profile.Mode)
	if req.Profile.Mode != "native" && req.Profile.Mode != "gateway" && req.Profile.Mode != "cni" {
		return fmt.Errorf("出口模式仅支持native、gateway或cni")
	}
	req.Profile.TunnelType, err = cleanEgressValue(req.Profile.TunnelType, "隧道类型", 16, false)
	if err != nil {
		return err
	}
	if req.Profile.TunnelType == "" {
		req.Profile.TunnelType = "wireguard"
	}
	req.Profile.TunnelType = strings.ToLower(req.Profile.TunnelType)
	if req.Profile.TunnelType != "wireguard" && req.Profile.TunnelType != "ipsec" && req.Profile.TunnelType != "gateway" {
		return fmt.Errorf("隧道类型仅支持wireguard、ipsec或gateway")
	}
	req.Profile.TunnelInterface, err = validateEgressIdentifier(req.Profile.TunnelInterface, "隧道接口", 15, true)
	if err != nil {
		return err
	}
	if !egressInterfacePattern.MatchString(req.Profile.TunnelInterface) {
		return fmt.Errorf("隧道接口只能包含字母、数字、点、下划线和短横线")
	}
	if req.Profile.Enabled == nil {
		enabled := true
		req.Profile.Enabled = &enabled
	}
	if req.Profile.FailClosed == nil {
		failClosed := true
		req.Profile.FailClosed = &failClosed
	}
	if !*req.Profile.FailClosed {
		return fmt.Errorf("透明出口必须启用fail-closed，禁止回落到宿主默认出口")
	}
	if *req.Profile.Enabled && (req.Profile.RouteTable == 0 || req.Profile.RouteTable > maxEgressRouteTable || (req.Profile.RouteTable >= 253 && req.Profile.RouteTable <= 255)) {
		return fmt.Errorf("启用的出口配置路由表必须在1-%d且不能使用253-255", maxEgressRouteTable)
	}
	if *req.Profile.Enabled && (req.Profile.Mark == 0 || req.Profile.Mark > maxEgressMark) {
		return fmt.Errorf("启用的出口配置fwmark必须在1-0x%06x", maxEgressMark)
	}
	if req.Profile.Gateway != nil && strings.TrimSpace(*req.Profile.Gateway) != "" {
		gateway := strings.TrimSpace(*req.Profile.Gateway)
		if strings.Contains(gateway, "/") || net.ParseIP(gateway) == nil {
			return fmt.Errorf("网关必须是有效的IP地址")
		}
		*req.Profile.Gateway = net.ParseIP(gateway).String()
	}
	if err := validateHostIP(req.Profile.PublicIPv4, "预期公网IPv4", 4); err != nil {
		return err
	}
	if err := validateHostIP(req.Profile.PublicIPv6, "预期公网IPv6", 6); err != nil {
		return err
	}
	if req.Profile.WireGuard != nil && req.Profile.TunnelType != "wireguard" {
		return fmt.Errorf("WireGuard配置要求tunnel_type=wireguard")
	}
	if err := validateWireGuardRequest(req.Profile.WireGuard); err != nil {
		return err
	}
	allSources := append([]string(nil), req.Sources...)
	if strings.TrimSpace(req.Source) != "" {
		allSources = append(allSources, req.Source)
	}
	req.Sources, err = normalizeBindingSources(allSources)
	if err != nil {
		return err
	}
	if len(req.Sources) == 0 {
		return fmt.Errorf("实例源地址不能为空")
	}
	req.Source = req.Sources[0]
	for _, source := range req.Sources {
		prefix, _ := netip.ParsePrefix(source)
		if prefix.Addr().Is4() && req.Profile.PublicIPv4 == nil {
			return fmt.Errorf("IPv4透明出口必须配置预期公网IPv4以执行严格出口验证")
		}
		if prefix.Addr().Is6() && req.Profile.PublicIPv6 == nil {
			return fmt.Errorf("IPv6透明出口必须配置预期公网IPv6以执行严格出口验证")
		}
	}
	for _, candidate := range []struct {
		value **string
		label string
	}{
		{&req.Interface, "实例接口"},
		{&req.InterfaceV4, "实例IPv4接口"},
		{&req.InterfaceV6, "实例IPv6接口"},
	} {
		if *candidate.value == nil || strings.TrimSpace(**candidate.value) == "" {
			continue
		}
		iface, ifaceErr := validateEgressIdentifier(**candidate.value, candidate.label, 15, false)
		if ifaceErr != nil {
			return ifaceErr
		}
		if !egressInterfacePattern.MatchString(iface) {
			return fmt.Errorf("%s只能包含字母、数字、点、下划线和短横线", candidate.label)
		}
		**candidate.value = iface
	}
	if req.InterfaceV4 == nil && req.Interface != nil {
		req.InterfaceV4 = req.Interface
	}
	if req.InterfaceV6 == nil && req.Interface != nil {
		req.InterfaceV6 = req.Interface
	}
	if req.Enabled == nil {
		enabled := true
		req.Enabled = &enabled
	}
	if *req.Enabled && req.Profile.Mode == "native" {
		for _, source := range req.Sources {
			prefix, _ := netip.ParsePrefix(source)
			if prefix.Addr().Is4() && (req.InterfaceV4 == nil || strings.TrimSpace(*req.InterfaceV4) == "") {
				return fmt.Errorf("native出口的IPv4源地址要求可验证的宿主入口接口")
			}
			if prefix.Addr().Is6() && (req.InterfaceV6 == nil || strings.TrimSpace(*req.InterfaceV6) == "") {
				return fmt.Errorf("native出口的IPv6源地址要求可验证的宿主入口接口")
			}
		}
	}
	return nil
}
