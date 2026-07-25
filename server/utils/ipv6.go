package utils

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"unicode"
)

// IPv6Network describes an IPv6 address together with the prefix length that
// was reported for it. Address is always a 16-byte IPv6 value.
type IPv6Network struct {
	Address   net.IP
	PrefixLen int
}

// ContainerNetworkSelection is the resolved runtime network attachment for a
// container. StaticIPv6 is canonical and is only populated for an IPv6
// selection.
type ContainerNetworkSelection struct {
	Network    string
	StaticIPv6 string
	IPv6       bool
}

// NetworkTypeHasIPv6 reports whether the control-plane network type requires
// an IPv6-capable runtime network.
func NetworkTypeHasIPv6(networkType string) bool {
	switch strings.TrimSpace(networkType) {
	case "nat_ipv4_ipv6", "dedicated_ipv4_ipv6", "ipv6_only":
		return true
	default:
		return false
	}
}

// ResolveContainerNetwork prevents an allocated static IPv6 address from
// being silently discarded by an IPv4-only or unavailable runtime network.
func ResolveContainerNetwork(networkType, staticIPv6, ipv4Network, ipv6Network string, ipv6NetworkAvailable bool) (ContainerNetworkSelection, error) {
	hasIPv6 := NetworkTypeHasIPv6(networkType)
	staticIPv6 = strings.TrimSpace(staticIPv6)
	normalizedIPv6 := ""
	if staticIPv6 != "" {
		var err error
		normalizedIPv6, err = NormalizeIPv6Address(staticIPv6)
		if err != nil {
			return ContainerNetworkSelection{}, fmt.Errorf("静态IPv6地址无效: %w", err)
		}
		if !hasIPv6 {
			return ContainerNetworkSelection{}, fmt.Errorf("已分配静态IPv6 %s，但网络类型 %q 未启用IPv6", normalizedIPv6, strings.TrimSpace(networkType))
		}
	}

	if hasIPv6 && ipv6NetworkAvailable {
		return ContainerNetworkSelection{
			Network:    ipv6Network,
			StaticIPv6: normalizedIPv6,
			IPv6:       true,
		}, nil
	}
	if normalizedIPv6 != "" {
		return ContainerNetworkSelection{}, fmt.Errorf("已分配静态IPv6 %s，但节点IPv6网络不可用", normalizedIPv6)
	}
	return ContainerNetworkSelection{Network: ipv4Network}, nil
}

// ParseIPv6Network parses one IPv6 address or CIDR value. Bare addresses use
// defaultPrefix; callers should provide the prefix discovered from the host
// when it is available.
func ParseIPv6Network(value string, defaultPrefix int) (IPv6Network, error) {
	token := cleanIPv6Token(value)
	if token == "" {
		return IPv6Network{}, fmt.Errorf("IPv6地址为空")
	}
	if defaultPrefix < 0 || defaultPrefix > 128 {
		return IPv6Network{}, fmt.Errorf("无效的IPv6前缀长度: %d", defaultPrefix)
	}

	var (
		ip        net.IP
		prefixLen = defaultPrefix
	)
	if strings.Contains(token, "/") {
		var network *net.IPNet
		var err error
		ip, network, err = net.ParseCIDR(token)
		if err != nil || network == nil {
			return IPv6Network{}, fmt.Errorf("无效的IPv6地址或CIDR: %q", value)
		}
		prefixLen, _ = network.Mask.Size()
	} else {
		ip = net.ParseIP(token)
		if ip == nil {
			return IPv6Network{}, fmt.Errorf("无效的IPv6地址: %q", value)
		}
	}

	if ip.To16() == nil || ip.To4() != nil {
		return IPv6Network{}, fmt.Errorf("不是IPv6地址: %q", value)
	}
	return IPv6Network{
		Address:   cloneIPv6(ip),
		PrefixLen: prefixLen,
	}, nil
}

// ExtractIPv6Networks extracts IPv6 address/CIDR tokens from noisy command
// output. It deliberately ignores non-address lines, so diagnostics printed
// by a remote script cannot become part of a prefix or an interface setting.
func ExtractIPv6Networks(output string, defaultPrefix int) []IPv6Network {
	seen := make(map[string]struct{})
	result := make([]IPv6Network, 0)
	for _, field := range splitIPv6Candidates(output) {
		network, err := ParseIPv6Network(field, defaultPrefix)
		if err != nil {
			continue
		}
		key := network.Address.String() + "/" + strconv.Itoa(network.PrefixLen)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, network)
	}
	return result
}

// ExtractFirstIPv6Network returns the first valid IPv6 address/CIDR in noisy
// command output.
func ExtractFirstIPv6Network(output string, defaultPrefix int) (IPv6Network, error) {
	networks := ExtractIPv6Networks(output, defaultPrefix)
	if len(networks) == 0 {
		return IPv6Network{}, fmt.Errorf("输出中未找到有效的IPv6地址")
	}
	return networks[0], nil
}

// ParseFirstIPv6NetworkOutput parses the first valid IPv6 address/CIDR from
// command output. Remote probes can return warnings, progress text, or more
// than one line even when the command is expected to print one value. Each
// line/token is validated independently; invalid candidates are skipped and
// the original output is never passed on to a shell command.
func ParseFirstIPv6NetworkOutput(output string, defaultPrefix int) (IPv6Network, error) {
	for _, line := range commandOutputLines(output) {
		if isDiagnosticCommandLine(line) {
			continue
		}
		if network, err := ExtractFirstIPv6Network(line, defaultPrefix); err == nil {
			return network, nil
		}
	}
	return IPv6Network{}, fmt.Errorf("输出中未找到有效的IPv6地址")
}

// ParseSingleCommandToken accepts exactly one non-empty token from command
// output. Leading and trailing transport whitespace is allowed, but embedded
// whitespace, extra lines and control characters are rejected so diagnostics
// cannot be concatenated into a value later used by a shell command.
func ParseSingleCommandToken(output string) (string, error) {
	token := strings.TrimSpace(output)
	if token == "" {
		return "", fmt.Errorf("命令输出为空")
	}
	if strings.IndexFunc(token, func(char rune) bool {
		return unicode.IsSpace(char) || unicode.IsControl(char)
	}) >= 0 {
		return "", fmt.Errorf("命令输出必须是单行单值")
	}
	return token, nil
}

// ParseFirstCommandLineMatching validates command output one line at a time
// and returns the first line accepted by validate. It is for remote probes
// whose PTY or agent transport may prepend diagnostics. Persisted files and
// user-supplied values should continue to use strict parsers.
func ParseFirstCommandLineMatching(output string, validate func(string) bool) (string, error) {
	if validate == nil {
		return "", fmt.Errorf("命令输出校验器为空")
	}
	for _, line := range commandOutputLines(output) {
		token, err := ParseSingleCommandToken(line)
		if err == nil && validate(token) {
			return token, nil
		}
	}
	return "", fmt.Errorf("输出中未找到符合要求的单行值")
}

// ParseSingleIPv6NetworkOutput parses exactly one IPv6 address or CIDR from
// command output. Unlike ExtractFirstIPv6Network it intentionally rejects
// surrounding labels, punctuation and additional diagnostic lines.
func ParseSingleIPv6NetworkOutput(output string, defaultPrefix int) (IPv6Network, error) {
	token, err := ParseSingleCommandToken(output)
	if err != nil {
		return IPv6Network{}, err
	}
	if token != cleanIPv6Token(token) {
		return IPv6Network{}, fmt.Errorf("IPv6输出包含非法前后缀: %q", token)
	}
	network, err := ParseIPv6Network(token, defaultPrefix)
	if err != nil {
		return IPv6Network{}, err
	}
	return network, nil
}

// ParseSingleIPv6AddressOutput parses exactly one bare IPv6 host address.
// CIDR suffixes are rejected for callers such as gateway and NAT lookups that
// require an address rather than a network declaration.
func ParseSingleIPv6AddressOutput(output string) (string, error) {
	token, err := ParseSingleCommandToken(output)
	if err != nil {
		return "", err
	}
	if strings.Contains(token, "/") || token != cleanIPv6Token(token) {
		return "", fmt.Errorf("IPv6地址输出格式无效: %q", token)
	}
	ip := net.ParseIP(token)
	if ip == nil || ip.To16() == nil || ip.To4() != nil {
		return "", fmt.Errorf("无效的IPv6地址: %q", token)
	}
	return ip.To16().String(), nil
}

// ParseFirstIPv6AddressOutput parses the first valid host IPv6 address from
// noisy command output. Both bare addresses and address/prefix tokens are
// accepted because ip(8), guest agents, and JSON filters do not all use the
// same representation.
func ParseFirstIPv6AddressOutput(output string) (string, error) {
	for _, line := range commandOutputLines(output) {
		if isDiagnosticCommandLine(line) {
			continue
		}
		for _, candidate := range splitIPv6Candidates(line) {
			candidate = cleanIPv6Token(candidate)
			if slash := strings.IndexByte(candidate, '/'); slash >= 0 {
				if _, network, err := net.ParseCIDR(candidate); err != nil || network == nil || network.IP.To16() == nil || network.IP.To4() != nil {
					continue
				}
				candidate = candidate[:slash]
			}
			if address, err := ParseSingleIPv6AddressOutput(candidate); err == nil {
				return address, nil
			}
		}
	}
	return "", fmt.Errorf("输出中未找到有效的IPv6地址")
}

// ParseSingleIPv4AddressOutput parses exactly one bare IPv4 host address from
// command output. It rejects CIDRs and surrounding diagnostics for the same
// reason as ParseSingleIPv6AddressOutput.
func ParseSingleIPv4AddressOutput(output string) (string, error) {
	token, err := ParseSingleCommandToken(output)
	if err != nil {
		return "", err
	}
	if strings.Contains(token, "/") || strings.Trim(token, "[]") != token {
		return "", fmt.Errorf("IPv4地址输出格式无效: %q", token)
	}
	ip := net.ParseIP(token)
	if ip == nil || ip.To4() == nil {
		return "", fmt.Errorf("无效的IPv4地址: %q", token)
	}
	return ip.To4().String(), nil
}

// ParseFirstIPv4AddressOutput parses the first valid host IPv4 address from
// noisy command output. A CIDR suffix is accepted and removed after the
// complete candidate has been validated as IPv4.
func ParseFirstIPv4AddressOutput(output string) (string, error) {
	for _, line := range commandOutputLines(output) {
		if isDiagnosticCommandLine(line) {
			continue
		}
		for _, candidate := range splitIPv4Candidates(line) {
			if slash := strings.IndexByte(candidate, '/'); slash >= 0 {
				if _, _, err := net.ParseCIDR(candidate); err != nil {
					continue
				}
				candidate = candidate[:slash]
			}
			if address, err := ParseSingleIPv4AddressOutput(candidate); err == nil {
				return address, nil
			}
		}
	}
	return "", fmt.Errorf("输出中未找到有效的IPv4地址")
}

// ParseIPv6NetworkLines parses a command output where every non-empty line
// must be one IPv6 address or CIDR. One polluted line rejects the entire
// result; callers never silently skip shell diagnostics.
func ParseIPv6NetworkLines(output string, defaultPrefix int) ([]IPv6Network, error) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil, nil
	}

	normalized := strings.ReplaceAll(trimmed, "\r\n", "\n")
	if strings.Contains(normalized, "\r") {
		return nil, fmt.Errorf("IPv6列表包含非法控制字符")
	}
	lines := strings.Split(normalized, "\n")
	result := make([]IPv6Network, 0, len(lines))
	for index, line := range lines {
		if strings.TrimSpace(line) == "" {
			return nil, fmt.Errorf("IPv6列表第%d行为空", index+1)
		}
		network, err := ParseSingleIPv6NetworkOutput(line, defaultPrefix)
		if err != nil {
			return nil, fmt.Errorf("IPv6列表第%d行无效: %w", index+1, err)
		}
		result = append(result, network)
	}
	return result, nil
}

// ParseIPv6AddressLines is the bare-address counterpart of
// ParseIPv6NetworkLines. It is intended for address pool and mapping files.
func ParseIPv6AddressLines(output string) ([]string, error) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil, nil
	}

	normalized := strings.ReplaceAll(trimmed, "\r\n", "\n")
	if strings.Contains(normalized, "\r") {
		return nil, fmt.Errorf("IPv6地址列表包含非法控制字符")
	}
	lines := strings.Split(normalized, "\n")
	result := make([]string, 0, len(lines))
	for index, line := range lines {
		if strings.TrimSpace(line) == "" {
			return nil, fmt.Errorf("IPv6地址列表第%d行为空", index+1)
		}
		address, err := ParseSingleIPv6AddressOutput(line)
		if err != nil {
			return nil, fmt.Errorf("IPv6地址列表第%d行无效: %w", index+1, err)
		}
		result = append(result, address)
	}
	return result, nil
}

// ParseIPv6PrefixLengthOutput parses one decimal IPv6 prefix length while
// rejecting multiline output, signs and diagnostic text.
func ParseIPv6PrefixLengthOutput(output string) (int, error) {
	token, err := ParseSingleCommandToken(output)
	if err != nil {
		return 0, err
	}
	for _, char := range token {
		if char < '0' || char > '9' {
			return 0, fmt.Errorf("无效的IPv6前缀长度: %q", token)
		}
	}
	prefix, err := strconv.Atoi(token)
	if err != nil || prefix < 0 || prefix > 128 {
		return 0, fmt.Errorf("无效的IPv6前缀长度: %q", token)
	}
	return prefix, nil
}

// ParseFirstIPv6PrefixLengthOutput parses the first valid decimal prefix
// length from noisy command output. It is intended for remote probes only;
// files and user input should continue to use ParseIPv6PrefixLengthOutput.
func ParseFirstIPv6PrefixLengthOutput(output string) (int, error) {
	for _, candidate := range commandOutputLines(output) {
		if prefix, err := ParseIPv6PrefixLengthOutput(candidate); err == nil {
			return prefix, nil
		}
	}
	return 0, fmt.Errorf("输出中未找到有效的IPv6前缀长度")
}

// ParseNetworkInterfaceOutput validates one Linux interface name returned by
// a remote command. The conservative character set matches the interface
// names generated and supported by this project and prevents diagnostics or
// shell fragments from being treated as a device name.
func ParseNetworkInterfaceOutput(output string) (string, error) {
	name, err := ParseSingleCommandToken(output)
	if err != nil {
		return "", err
	}
	if len(name) > 15 {
		return "", fmt.Errorf("网络接口名称过长: %q", name)
	}
	if name == "." || name == ".." || name[0] == '-' {
		return "", fmt.Errorf("网络接口名称格式无效: %q", name)
	}
	for _, char := range name {
		if !((char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			char == '_' || char == '.' || char == '-') {
			return "", fmt.Errorf("网络接口名称包含非法字符: %q", name)
		}
	}
	return name, nil
}

// ParseFirstNetworkInterfaceOutput parses the first valid Linux interface
// name from noisy probe output. Strict ParseNetworkInterfaceOutput remains the
// parser for values that will be persisted or used as explicit configuration.
func ParseFirstNetworkInterfaceOutput(output string) (string, error) {
	for _, candidate := range commandOutputLines(output) {
		if isDiagnosticCommandLine(candidate) {
			continue
		}
		if name, err := ParseNetworkInterfaceOutput(candidate); err == nil {
			return name, nil
		}
	}
	return "", fmt.Errorf("输出中未找到有效的网络接口名称")
}

// ExtractIPv6Addresses returns canonical host addresses from noisy command
// output. CIDR suffixes and zone identifiers are removed without converting a
// host address to its network base. This is useful for one-shot occupancy
// snapshots such as `ip -6 addr`, neighbor tables and firewall rules.
func ExtractIPv6Addresses(output string) []string {
	seen := make(map[string]struct{})
	addresses := make([]string, 0)
	for _, token := range splitIPv6Candidates(output) {
		token = cleanIPv6Token(token)
		if slash := strings.IndexByte(token, '/'); slash >= 0 {
			token = token[:slash]
		}
		ip := net.ParseIP(token)
		if ip == nil || ip.To16() == nil || ip.To4() != nil {
			continue
		}
		canonical := ip.To16().String()
		if _, exists := seen[canonical]; exists {
			continue
		}
		seen[canonical] = struct{}{}
		addresses = append(addresses, canonical)
	}
	return addresses
}

// NormalizeIPv6Address validates one host address and strips an optional CIDR
// suffix without changing the host bits. A prefix is accepted for callers
// that receive `addr/prefix` from a provider command, but allocation stores
// the canonical address separately from its prefix.
func NormalizeIPv6Address(value string) (string, error) {
	token := cleanIPv6Token(value)
	if slash := strings.IndexByte(token, '/'); slash >= 0 {
		prefix := token[slash+1:]
		prefixLen, err := strconv.Atoi(prefix)
		if err != nil || prefixLen < 0 || prefixLen > 128 {
			return "", fmt.Errorf("无效的IPv6前缀长度")
		}
		token = token[:slash]
	}
	ip := net.ParseIP(token)
	if ip == nil || ip.To16() == nil || ip.To4() != nil {
		return "", fmt.Errorf("无效的IPv6地址")
	}
	return ip.To16().String(), nil
}

// NetworkAddress returns the canonical network address for the prefix.
func (n IPv6Network) NetworkAddress() net.IP {
	if n.Address == nil || n.PrefixLen < 0 || n.PrefixLen > 128 {
		return nil
	}
	return cloneIPv6(n.Address.Mask(net.CIDRMask(n.PrefixLen, 128)))
}

// CIDR returns the canonical network CIDR, suitable for passing to ip(8) or
// to an address allocator.
func (n IPv6Network) CIDR() string {
	network := n.NetworkAddress()
	if network == nil {
		return ""
	}
	return fmt.Sprintf("%s/%d", network.String(), n.PrefixLen)
}

// IPv6AddressWithSuffix returns an address in n's network using the lower
// host bits of suffix. It works for every prefix length from /0 through /128;
// for /128 only suffix zero is valid because there are no host bits.
func IPv6AddressWithSuffix(n IPv6Network, suffix uint64) (string, error) {
	network := n.NetworkAddress()
	if network == nil {
		return "", fmt.Errorf("无效的IPv6网络")
	}
	hostBits := 128 - n.PrefixLen
	if hostBits == 0 && suffix != 0 {
		return "", fmt.Errorf("/128网络没有可用的主机位")
	}

	result := cloneIPv6(network)
	// A uint64 suffix is intentionally used for bounded allocation. For
	// prefixes shorter than /64 the upper host bits remain zero, while for
	// prefixes /64 and longer all available low host bits are respected.
	if hostBits > 0 {
		if hostBits < 64 {
			suffix &= (uint64(1) << hostBits) - 1
		}
		hostBytes := (hostBits + 7) / 8
		for idx := 0; idx < hostBytes; idx++ {
			result[15-idx] = byte(suffix)
			suffix >>= 8
		}
		if remainder := hostBits % 8; remainder != 0 {
			// Preserve the prefix bits in the first partially-host byte.
			hostMask := byte((uint16(1) << remainder) - 1)
			firstHostByte := 16 - hostBytes
			result[firstHostByte] = (result[firstHostByte] & hostMask) | (network[firstHostByte] &^ hostMask)
		}
	}
	return result.String(), nil
}

// FirstAvailableIPv6 selects locally from a single remote occupancy snapshot.
// It never performs per-candidate I/O and stops when suffix masking begins to
// repeat on very small prefixes.
func FirstAvailableIPv6(n IPv6Network, occupied []string, start, maxAttempts uint64) (string, error) {
	used := make(map[string]struct{}, len(occupied))
	for _, raw := range occupied {
		if address, err := NormalizeIPv6Address(raw); err == nil {
			used[address] = struct{}{}
		}
	}
	tried := make(map[string]struct{})
	for attempt := uint64(0); attempt < maxAttempts; attempt++ {
		candidate, err := IPv6AddressWithSuffix(n, start+attempt)
		if err != nil {
			return "", err
		}
		if _, repeated := tried[candidate]; repeated {
			break
		}
		tried[candidate] = struct{}{}
		if _, exists := used[candidate]; !exists {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("IPv6网络中没有可用地址")
}

// ParseHexUint64 parses a single hexadecimal token (for example, output from
// od/tr used to generate an IPv6 host suffix). Multiple lines or diagnostic
// text are rejected instead of silently becoming an incorrect address.
func ParseHexUint64(value string) (uint64, error) {
	token, err := ParseSingleCommandToken(value)
	if err != nil {
		return 0, fmt.Errorf("无效的十六进制数输出: %w", err)
	}
	token = strings.TrimPrefix(strings.TrimPrefix(token, "0x"), "0X")
	if token == "" || len(token) > 16 {
		return 0, fmt.Errorf("无效的十六进制数: %q", value)
	}
	for _, char := range token {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return 0, fmt.Errorf("无效的十六进制数: %q", value)
		}
	}
	parsed, err := strconv.ParseUint(token, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("无效的十六进制数: %q", value)
	}
	return parsed, nil
}

func cleanIPv6Token(value string) string {
	token := strings.TrimSpace(value)
	token = strings.Trim(token, "[](){}<>,;\"'`")
	// Keep ':' intact: a compressed IPv6 address may legitimately end in
	// '::'. Only strip punctuation that cannot be part of an address.
	token = strings.TrimRight(token, ".,;)")
	if zoneIndex := strings.LastIndexByte(token, '%'); zoneIndex >= 0 {
		// net.ParseIP does not accept zone identifiers. They are not part of
		// the address/prefix and can safely be removed for allocation.
		if slashIndex := strings.IndexByte(token[zoneIndex:], '/'); slashIndex >= 0 {
			slashIndex += zoneIndex
			token = token[:zoneIndex] + token[slashIndex:]
		} else {
			token = token[:zoneIndex]
		}
	}
	return token
}

func splitIPv6Candidates(output string) []string {
	return strings.FieldsFunc(output, func(char rune) bool {
		isHex := (char >= '0' && char <= '9') ||
			(char >= 'a' && char <= 'f') ||
			(char >= 'A' && char <= 'F')
		return !isHex && char != ':' && char != '/' && char != '%'
	})
}

func splitIPv4Candidates(output string) []string {
	return strings.FieldsFunc(output, func(char rune) bool {
		return !((char >= '0' && char <= '9') || char == '.' || char == '/')
	})
}

func commandOutputLines(output string) []string {
	normalized := strings.ReplaceAll(output, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}

func isDiagnosticCommandLine(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	for _, prefix := range []string{
		"warning", "warn", "error", "debug", "info", "notice",
		"retry", "retrying", "attempting", "failed", "failure",
	} {
		if lower == prefix || strings.HasPrefix(lower, prefix+":") || strings.HasPrefix(lower, prefix+" ") {
			return true
		}
	}
	return false
}

func cloneIPv6(ip net.IP) net.IP {
	result := make(net.IP, net.IPv6len)
	copy(result, ip.To16())
	return result
}
