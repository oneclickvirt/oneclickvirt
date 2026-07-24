package ipv6pool

import (
	"fmt"
	"net"
	"strings"
	"unicode"

	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/utils"
)

func parseIPv6PoolText(providerID uint, text, source string) ([]providerModel.ProviderIPv6Pool, []string, error) {
	return parseIPv6PoolTextWithOptions(providerID, text, source, false)
}

func parseIPv6PoolTextWithOptions(providerID uint, text, source string, allowEmpty bool) ([]providerModel.ProviderIPv6Pool, []string, error) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	parsed := make([]providerModel.ProviderIPv6Pool, 0)
	invalid := make([]string, 0)
	seen := make(map[string]struct{})
	tokenCount := 0
	strictNodeFile := source == SourceNodeFile
	for _, rawLine := range strings.Split(text, "\n") {
		line := strings.TrimSpace(strings.SplitN(rawLine, "#", 2)[0])
		if line == "" {
			continue
		}
		tokens := []string{line}
		if !strictNodeFile {
			tokens = strings.FieldsFunc(line, func(r rune) bool {
				return unicode.IsSpace(r) || r == ',' || r == ';'
			})
		}
		for _, token := range tokens {
			tokenCount++
			if tokenCount > maxIPv6PoolTokens {
				return nil, invalid, fmt.Errorf("IPv6地址池输入条目超过%d", maxIPv6PoolTokens)
			}
			entry, err := parseIPv6PoolToken(providerID, token, source)
			if err != nil {
				invalid = append(invalid, fmt.Sprintf("%s (%v)", token, err))
				continue
			}
			if _, exists := seen[entry.Address]; exists {
				continue
			}
			seen[entry.Address] = struct{}{}
			parsed = append(parsed, entry)
		}
	}
	if strictNodeFile && len(invalid) > 0 {
		return nil, invalid, fmt.Errorf("节点IPv6地址文件包含%d行无效或污染内容", len(invalid))
	}
	if len(parsed) == 0 {
		if allowEmpty && len(invalid) == 0 {
			return parsed, invalid, nil
		}
		return nil, invalid, fmt.Errorf("未解析到有效的IPv6地址或CIDR")
	}
	return parsed, invalid, nil
}

func parseIPv6PoolToken(providerID uint, token, source string) (providerModel.ProviderIPv6Pool, error) {
	if source == SourceNodeFile {
		strictToken, err := utils.ParseSingleCommandToken(token)
		if err != nil {
			return providerModel.ProviderIPv6Pool{}, fmt.Errorf("节点文件条目必须是单行单值: %w", err)
		}
		token = strictToken
	} else {
		token = strings.Trim(strings.TrimSpace(token), "[](){}<>'\"`")
	}
	if strings.Contains(token, "/") {
		ip, network, err := net.ParseCIDR(token)
		if err != nil || ip == nil || network == nil || ip.To4() != nil || ip.To16() == nil {
			return providerModel.ProviderIPv6Pool{}, fmt.Errorf("无效的IPv6 CIDR")
		}
		ones, bits := network.Mask.Size()
		if bits != 128 || ones < 0 || ones > 128 {
			return providerModel.ProviderIPv6Pool{}, fmt.Errorf("无效的IPv6前缀长度")
		}
		if ones == 128 {
			return providerModel.ProviderIPv6Pool{ProviderID: providerID, Address: ip.String(), PrefixLength: 128, Source: source}, nil
		}
		// IPv6 has no broadcast address. The all-zero host value is therefore a
		// valid member of an explicit pool, including both addresses in a /127.
		rangeNext := network.IP.To16()
		return providerModel.ProviderIPv6Pool{
			ProviderID: providerID, Address: network.String(), PrefixLength: ones,
			IsRange: true, RangeNext: rangeNext.String(), Source: source,
		}, nil
	}
	ip := net.ParseIP(token)
	if ip == nil || ip.To16() == nil || ip.To4() != nil {
		return providerModel.ProviderIPv6Pool{}, fmt.Errorf("无效的IPv6地址")
	}
	return providerModel.ProviderIPv6Pool{ProviderID: providerID, Address: ip.To16().String(), PrefixLength: 128, Source: source}, nil
}

func incrementIPv6(ip net.IP) (net.IP, bool) {
	next := append(net.IP(nil), ip.To16()...)
	if len(next) != net.IPv6len {
		return nil, false
	}
	for index := len(next) - 1; index >= 0; index-- {
		next[index]++
		if next[index] != 0 {
			return next, true
		}
	}
	return nil, false
}
