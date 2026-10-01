package task

import (
	"fmt"
	"net"
	"sort"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	domainModel "oneclickvirt/model/domain"
	providerModel "oneclickvirt/model/provider"
)

type resetDomainAddress struct {
	old string
	new string
}

func canonicalResetIP(value string) (net.IP, string) {
	value = strings.TrimSpace(value)
	if ip := net.ParseIP(value); ip != nil {
		return ip, ip.String()
	}
	if ip, _, err := net.ParseCIDR(value); err == nil {
		return ip, ip.String()
	}
	return nil, ""
}

func canonicalResetIPv6(value string) string {
	ip, canonical := canonicalResetIP(value)
	if ip == nil || ip.To4() != nil {
		return ""
	}
	return canonical
}

// migrateResetDomainTargets follows the same owned address family across a
// reset. Domain rows have already been transferred to newInstanceID in the
// replacement transaction, so this stays inside the short instance update
// transaction and never spans provider or Agent I/O.
func migrateResetDomainTargets(tx *gorm.DB, newInstanceID uint, old providerModel.Instance, newIPv4, newPublicIPv4, newIPv6, newPublicIPv6 string) error {
	if tx == nil || newInstanceID == 0 {
		return fmt.Errorf("域名目标迁移参数无效")
	}
	addresses := []resetDomainAddress{
		{old: old.PrivateIP, new: newIPv4},
		{old: old.PublicIP, new: newPublicIPv4},
		{old: old.IPv6Address, new: newIPv6},
		{old: old.PublicIPv6, new: newPublicIPv6},
	}
	replacements := make(map[string]string, len(addresses))
	for _, address := range addresses {
		oldIP, oldCanonical := canonicalResetIP(address.old)
		newIP, newCanonical := canonicalResetIP(address.new)
		if oldIP == nil || newIP == nil || (oldIP.To4() == nil) != (newIP.To4() == nil) || oldIP.Equal(newIP) {
			continue
		}
		if previous, exists := replacements[oldCanonical]; exists && previous != newCanonical {
			return fmt.Errorf("旧域名目标 %s 对应多个新地址，无法安全迁移", oldCanonical)
		}
		replacements[oldCanonical] = newCanonical
	}
	if len(replacements) == 0 {
		return nil
	}
	var domains []domainModel.Domain
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "internal_ip").
		Where("instance_id = ?", newInstanceID).Find(&domains).Error; err != nil {
		return fmt.Errorf("读取重置域名目标失败: %w", err)
	}
	// Capture row IDs before any update. This handles IPv6 spelling differences
	// and prevents an address swap A->B, B->A from migrating a row twice.
	groups := make(map[string][]uint, len(replacements))
	for _, domain := range domains {
		_, current := canonicalResetIP(domain.InternalIP)
		if replacement, ok := replacements[current]; ok {
			groups[replacement] = append(groups[replacement], domain.ID)
		}
	}
	destinations := make([]string, 0, len(groups))
	for address := range groups {
		destinations = append(destinations, address)
	}
	sort.Strings(destinations)
	for _, newAddress := range destinations {
		if err := tx.Model(&domainModel.Domain{}).
			Where("instance_id = ? AND id IN ?", newInstanceID, groups[newAddress]).
			Update("internal_ip", newAddress).Error; err != nil {
			return fmt.Errorf("迁移重置后的域名目标失败: %w", err)
		}
	}
	return nil
}
