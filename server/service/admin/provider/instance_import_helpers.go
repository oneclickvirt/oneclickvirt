package provider

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"unicode"

	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"gorm.io/gorm"
)

func selectDiscoveredInstances(instances []provider.DiscoveredInstance, selectors []string) ([]provider.DiscoveredInstance, error) {
	selected := make([]provider.DiscoveredInstance, 0, len(selectors))
	seen := make(map[string]bool)
	for _, rawSelector := range selectors {
		selector := strings.TrimSpace(rawSelector)
		if selector == "" {
			return nil, fmt.Errorf("instanceUuids 不能包含空值")
		}
		matches := make([]provider.DiscoveredInstance, 0, 1)
		for _, instance := range instances {
			if selector == instance.UUID || selector == instance.ProviderInstanceID || selector == instance.Name {
				matches = append(matches, instance)
			}
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("未发现指定实例: %s", selector)
		}
		// 非纯净节点可能存在重名行。发现结果已经稳定排序，兼容名称选择器时
		// 确定性选择第一条，剩余重复项由批次去重逻辑记录为 skipped。
		key := matches[0].UUID
		if key == "" {
			key = matches[0].ProviderInstanceID + "\x00" + matches[0].Name
		}
		if !seen[key] {
			seen[key] = true
			selected = append(selected, matches[0])
		}
	}
	return selected, nil
}

func normalizeImportedInstanceUUID(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func canRestoreHistoricalImportedInstance(owner providerModel.Instance, providerID uint) bool {
	return owner.DeletedAt.Valid && owner.ProviderID == providerID
}

func restoreHistoricalImportedInstance(tx *gorm.DB, instance *providerModel.Instance, historicalID uint) *gorm.DB {
	return tx.Unscoped().Model(&providerModel.Instance{}).
		Where("id = ? AND deleted_at IS NOT NULL", historicalID).
		Select("*").
		Omit("id", "created_at").
		Updates(instance)
}

func rollbackImportedInstance(tx *gorm.DB, savepoint string) error {
	if err := tx.RollbackTo(savepoint).Error; err != nil {
		return fmt.Errorf("回滚失败的实例导入项失败: %w", err)
	}
	return nil
}

func isDuplicateImportResourceError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "duplicate") || strings.Contains(lower, "unique constraint") || strings.Contains(lower, "unique key")
}

func deduplicateDiscoveredInstances(instances []provider.DiscoveredInstance) ([]provider.DiscoveredInstance, []duplicateDiscoveredInstance) {
	ordered := append([]provider.DiscoveredInstance(nil), instances...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return discoveredInstanceCanonicalKey(ordered[i]) < discoveredInstanceCanonicalKey(ordered[j])
	})
	owners := make(map[string]string)
	kept := make([]provider.DiscoveredInstance, 0, len(ordered))
	duplicates := make([]duplicateDiscoveredInstance, 0)
	for _, instance := range ordered {
		keys := discoveredInstanceConflictKeys(instance)
		var reason string
		for _, key := range keys {
			if owner, exists := owners[key]; exists {
				reason = fmt.Sprintf("发现资源重复（%s），已保留实例 %s", strings.ReplaceAll(key, "\x00", ":"), owner)
				break
			}
		}
		if reason != "" {
			duplicates = append(duplicates, duplicateDiscoveredInstance{Instance: instance, Reason: reason})
			continue
		}
		owner := instance.Name
		if owner == "" {
			owner = instance.ProviderInstanceID
		}
		for _, key := range keys {
			owners[key] = owner
		}
		kept = append(kept, instance)
	}
	return kept, duplicates
}

func discoveredInstanceConflictKeys(instance provider.DiscoveredInstance) []string {
	keys := make([]string, 0, 8+len(instance.PortMappings))
	appendKey := func(kind, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			keys = append(keys, kind+"\x00"+strings.ToLower(value))
		}
	}
	appendKey("uuid", instance.UUID)
	appendKey("remote-id", instance.ProviderInstanceID)
	// A display name is not a durable remote identity. PVE and several other
	// platforms permit duplicate or otherwise awkward names; those resources
	// remain distinct when they have different native IDs and receive stable
	// controller-side aliases before persistence.
	if strings.TrimSpace(instance.UUID) == "" && strings.TrimSpace(instance.ProviderInstanceID) == "" {
		appendKey("name", instance.Name)
	}
	appendKey("private-ip", instance.PrivateIP)
	appendKey("ipv6", instance.IPv6Address)
	portSet := make(map[int]struct{})
	if validDiscoveredPort(instance.SSHPort) && instance.SSHPort != 22 {
		portSet[instance.SSHPort] = struct{}{}
	}
	for _, mapping := range instance.PortMappings {
		if validDiscoveredPort(mapping.HostPort) {
			portSet[mapping.HostPort] = struct{}{}
		}
	}
	for _, port := range instance.ExtraPorts {
		if validDiscoveredPort(port) {
			portSet[port] = struct{}{}
		}
	}
	ports := make([]int, 0, len(portSet))
	for port := range portSet {
		ports = append(ports, port)
	}
	sort.Ints(ports)
	for _, port := range ports {
		keys = append(keys, fmt.Sprintf("host-port\x00%d", port))
	}
	return keys
}

func prepareImportedInstanceNames(providerType string, instances []provider.DiscoveredInstance, existing []providerModel.Instance) {
	reserved := make(map[string]struct{}, len(existing)+len(instances))
	for _, instance := range existing {
		if name := strings.ToLower(strings.TrimSpace(instance.Name)); name != "" {
			reserved[name] = struct{}{}
		}
	}
	for index := range instances {
		// Preserve the original name for legacy matching. Managed instances are
		// skipped later and must not be renamed before that compatibility check.
		if hasMatchingDBInstance(providerType, instances[index], existing) {
			continue
		}
		base := sanitizeImportedInstanceName(instances[index])
		candidate := base
		if _, exists := reserved[strings.ToLower(candidate)]; exists {
			suffix := importedInstanceNameSuffix(instances[index])
			candidate = appendImportedNameSuffix(base, suffix)
			for ordinal := 2; ; ordinal++ {
				if _, collision := reserved[strings.ToLower(candidate)]; !collision {
					break
				}
				candidate = appendImportedNameSuffix(base, fmt.Sprintf("%s-%d", suffix, ordinal))
			}
		}
		instances[index].Name = candidate
		reserved[strings.ToLower(candidate)] = struct{}{}
	}
}

func sanitizeImportedInstanceName(instance provider.DiscoveredInstance) string {
	var builder strings.Builder
	lastSeparator := false
	for _, r := range strings.TrimSpace(instance.Name) {
		allowed := r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-')
		if allowed {
			builder.WriteRune(r)
			lastSeparator = false
			continue
		}
		if !lastSeparator && builder.Len() > 0 {
			builder.WriteByte('-')
			lastSeparator = true
		}
	}
	name := strings.Trim(builder.String(), "._-")
	if name == "" {
		kind := strings.ToLower(strings.TrimSpace(instance.InstanceType))
		if kind != "vm" {
			kind = "ct"
		}
		name = kind + "-" + importedInstanceNameSuffix(instance)
	}
	if len(name) > 128 {
		name = strings.TrimRight(name[:128], "._-")
	}
	if name == "" {
		name = "instance-" + importedInstanceNameSuffix(instance)
	}
	return name
}

func importedInstanceNameSuffix(instance provider.DiscoveredInstance) string {
	remoteID := strings.Trim(utils.SanitizeShellArg(strings.TrimSpace(instance.ProviderInstanceID)), "._-")
	if remoteID != "" && len(remoteID) <= 24 {
		return remoteID
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{
		strings.TrimSpace(instance.ProviderInstanceID),
		strings.TrimSpace(instance.UUID),
		strings.TrimSpace(instance.InstanceType),
		strings.TrimSpace(instance.Name),
	}, "\x00")))
	return fmt.Sprintf("%x", sum[:4])
}

func appendImportedNameSuffix(base, suffix string) string {
	suffix = strings.Trim(utils.SanitizeShellArg(suffix), "._-")
	if suffix == "" {
		suffix = "imported"
	}
	maxBaseLength := 128 - len(suffix) - 1
	if maxBaseLength < 1 {
		maxBaseLength = 1
	}
	if len(base) > maxBaseLength {
		base = strings.TrimRight(base[:maxBaseLength], "._-")
	}
	if base == "" {
		base = "i"
	}
	return base + "-" + suffix
}

func hasConflictingInstanceName(providerType string, discovered provider.DiscoveredInstance, existing []providerModel.Instance) bool {
	for _, instance := range existing {
		if strings.EqualFold(strings.TrimSpace(instance.Name), strings.TrimSpace(discovered.Name)) && !discoveredInstanceMatchesDB(providerType, discovered, instance) {
			return true
		}
	}
	return false
}

func conflictingExistingInstanceResource(discovered provider.DiscoveredInstance, existing []providerModel.Instance) string {
	for _, instance := range existing {
		if discovered.PrivateIP != "" && instance.PrivateIP != "" && discovered.PrivateIP == instance.PrivateIP {
			return fmt.Sprintf("私网IP与已纳管实例 %s 重复，已保留现有实例", instance.Name)
		}
		if discovered.IPv6Address != "" && instance.IPv6Address != "" && strings.EqualFold(discovered.IPv6Address, instance.IPv6Address) {
			return fmt.Sprintf("IPv6地址与已纳管实例 %s 重复，已保留现有实例", instance.Name)
		}
	}
	return ""
}

func sortedPortSet(values map[int]bool) []int {
	ports := make([]int, 0, len(values))
	for port := range values {
		ports = append(ports, port)
	}
	sort.Ints(ports)
	return ports
}
