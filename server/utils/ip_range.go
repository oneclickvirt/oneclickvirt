package utils

import (
	"fmt"
	"net/netip"
	"strings"
)

// MaxIPRangeAddresses bounds explicit range expansion. CIDRs keep their existing
// pool-specific behavior, including lazy allocation for large IPv6 prefixes.
const MaxIPRangeAddresses = 4096

// ParseIPRange parses an inclusive start-end range of a single address family.
// On any invalid or oversized range it returns no addresses.
func ParseIPRange(text string, ipv6 bool) ([]string, error) {
	if strings.Count(text, "-") != 1 {
		return nil, fmt.Errorf("地址范围格式应为起始IP - 结束IP")
	}
	parts := strings.SplitN(text, "-", 2)
	start, startErr := netip.ParseAddr(strings.TrimSpace(parts[0]))
	end, endErr := netip.ParseAddr(strings.TrimSpace(parts[1]))
	if startErr != nil || endErr != nil || start.Zone() != "" || end.Zone() != "" {
		return nil, fmt.Errorf("地址范围端点必须是有效的IP地址")
	}
	if start.Is4() != !ipv6 || end.Is4() != !ipv6 || start.Is4In6() || end.Is4In6() {
		return nil, fmt.Errorf("地址范围端点必须使用相同的IPv4或IPv6类型")
	}
	if start.Compare(end) > 0 {
		return nil, fmt.Errorf("起始IP不能大于结束IP")
	}
	addresses := make([]string, 0)
	for addr := start; ; addr = addr.Next() {
		if len(addresses) == MaxIPRangeAddresses {
			return nil, fmt.Errorf("单个地址范围最多支持%d个地址", MaxIPRangeAddresses)
		}
		addresses = append(addresses, addr.String())
		if addr == end {
			return addresses, nil
		}
	}
}
