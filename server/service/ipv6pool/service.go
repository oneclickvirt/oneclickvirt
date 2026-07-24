package ipv6pool

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"net"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	runtimeProvider "oneclickvirt/service/provider"
	"oneclickvirt/utils"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	SourceManual     = "manual"
	SourceNodeFile   = "node_file"
	SourceRangeChild = "range_child"

	maxIPv6PoolTokens = 5000
	// Occupancy is queried once per bounded window. Fully occupied windows commit
	// cursor progress before the next short transaction, so one allocation call
	// can automatically skip any number of overlapping discrete addresses while
	// keeping every database transaction short.
	rangeScanWindowSize = 1024
)

type nodeFileReader func(context.Context, uint, string) (string, error)

type Service struct {
	readNodeFile nodeFileReader
}

type SyncResult struct {
	Path               string    `json:"path"`
	Added              []string  `json:"added"`
	Removed            []string  `json:"removed"`
	PreservedAllocated []string  `json:"preservedAllocated"`
	InvalidLines       []string  `json:"invalidLines"`
	ParsedCount        int       `json:"parsedCount"`
	RemoteReadCount    int       `json:"remoteReadCount"`
	Total              int64     `json:"total"`
	Allocated          int64     `json:"allocated"`
	Available          int64     `json:"available"`
	SyncedAt           time.Time `json:"syncedAt"`
	Stats              PoolStats `json:"-"`
}

// PoolStats separates database rows from address capacity. Available is a
// saturated int64 compatibility value; AvailableExact preserves the full
// decimal capacity of large IPv6 prefixes without overflow.
type PoolStats struct {
	Entries            int64  `json:"entries"`
	Materialized       int64  `json:"materialized"`
	Ranges             int64  `json:"ranges"`
	OpenRanges         int64  `json:"openRanges"`
	PendingRetire      int64  `json:"pendingRetire"`
	Allocated          int64  `json:"allocated"`
	Reusable           int64  `json:"reusable"`
	Available          int64  `json:"available"`
	AvailableExact     string `json:"availableExact"`
	AvailableSaturated bool   `json:"availableSaturated"`
}

func NewService() *Service {
	return &Service{readNodeFile: readProviderNodeFile}
}

// SupportsStaticIPv6 reports providers whose current create path can consume
// a controller-selected address. QEMU's default libvirt NAT and KubeVirt's pod
// CNI cannot safely accept an arbitrary routed address without additional
// network/gateway data, so they are intentionally excluded instead of silently
// discarding the allocation.
func SupportsStaticIPv6(providerType string) bool {
	switch strings.ToLower(strings.TrimSpace(providerType)) {
	case "lxd", "incus", "proxmox", "proxmoxve", "docker", "podman", "containerd", "orbstack":
		return true
	default:
		return false
	}
}

func (s *Service) HasConfiguredPool(providerID uint) (bool, error) {
	var count int64
	err := global.APP_DB.Model(&providerModel.ProviderIPv6Pool{}).
		Where("provider_id = ? AND parent_id IS NULL AND pending_retire = ? AND deleted_at IS NULL", providerID, false).
		Count(&count).Error
	return count > 0, err
}

func (s *Service) GetIPv6Pool(providerID uint, page, pageSize int) ([]providerModel.ProviderIPv6Pool, int64, error) {
	var entries []providerModel.ProviderIPv6Pool
	var total int64
	query := global.APP_DB.Model(&providerModel.ProviderIPv6Pool{}).
		Where("provider_id = ? AND deleted_at IS NULL", providerID)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 100
	}
	if err := query.Order("id ASC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&entries).Error; err != nil {
		return nil, 0, err
	}
	return entries, total, nil
}

// SetIPv6Pool appends controller-managed addresses without expanding CIDRs.
func (s *Service) SetIPv6Pool(providerID uint, text string) (added, invalid []string, err error) {
	parsed, invalid, err := parseIPv6PoolText(providerID, text, SourceManual)
	if err != nil {
		return nil, invalid, err
	}

	err = global.APP_DB.Transaction(func(tx *gorm.DB) error {
		var lockedProvider providerModel.Provider
		if err := tx.Select("id").Where("id = ?", providerID).
			Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedProvider).Error; err != nil {
			return fmt.Errorf("锁定Provider IPv6地址池失败: %w", err)
		}
		var existing []providerModel.ProviderIPv6Pool
		if err := tx.Unscoped().Where("provider_id = ? AND parent_id IS NULL", providerID).Find(&existing).Error; err != nil {
			return err
		}
		byAddress := make(map[string]providerModel.ProviderIPv6Pool, len(existing))
		for _, entry := range existing {
			byAddress[entry.Address] = entry
		}

		toCreate := make([]providerModel.ProviderIPv6Pool, 0, len(parsed))
		toRestore := make([]uint, 0)
		toRestoreRanges := make([]uint, 0)
		toClaimManual := make([]uint, 0)
		for _, entry := range parsed {
			old, found := byAddress[entry.Address]
			if !found {
				toCreate = append(toCreate, entry)
				added = append(added, entry.Address)
				continue
			}
			if old.DeletedAt != nil && !old.IsAllocated {
				toRestore = append(toRestore, old.ID)
				if old.IsRange {
					toRestoreRanges = append(toRestoreRanges, old.ID)
				}
				added = append(added, entry.Address)
			} else if old.DeletedAt == nil && (old.Source != SourceManual || old.PendingRetire) {
				// An explicit controller entry takes ownership from node-file sync so
				// a later file removal cannot unexpectedly delete it.
				toClaimManual = append(toClaimManual, old.ID)
				if old.IsRange && old.PendingRetire {
					toRestoreRanges = append(toRestoreRanges, old.ID)
				}
			}
		}
		if len(toRestore) > 0 {
			if err := tx.Unscoped().Model(&providerModel.ProviderIPv6Pool{}).
				Where("id IN ?", toRestore).
				Updates(map[string]interface{}{"deleted_at": nil, "source": SourceManual, "pending_retire": false}).Error; err != nil {
				return fmt.Errorf("恢复IPv6地址池条目失败: %w", err)
			}
		}
		if len(toRestoreRanges) > 0 {
			if err := tx.Unscoped().Model(&providerModel.ProviderIPv6Pool{}).
				Where("parent_id IN ? AND is_allocated = ?", toRestoreRanges, false).
				Updates(map[string]interface{}{"deleted_at": nil, "pending_retire": false}).Error; err != nil {
				return fmt.Errorf("恢复IPv6地址范围已释放子项失败: %w", err)
			}
		}
		if len(toClaimManual) > 0 {
			if err := tx.Model(&providerModel.ProviderIPv6Pool{}).Where("id IN ?", toClaimManual).
				Updates(map[string]interface{}{"source": SourceManual, "pending_retire": false}).Error; err != nil {
				return fmt.Errorf("更新主控IPv6地址池来源失败: %w", err)
			}
		}
		if len(toCreate) > 0 {
			if err := tx.CreateInBatches(&toCreate, 200).Error; err != nil {
				return fmt.Errorf("批量写入IPv6地址池失败: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, invalid, err
	}
	sort.Strings(added)
	return added, invalid, nil
}

// SyncProviderFile reads the configured node file exactly once. Remote I/O is
// completed before the short reconciliation transaction begins.
func (s *Service) SyncProviderFile(ctx context.Context, providerID uint, pathOverride ...string) (SyncResult, error) {
	result := SyncResult{}
	var dbProvider providerModel.Provider
	if err := global.APP_DB.Select("id", "ipv6_address_file_path").First(&dbProvider, providerID).Error; err != nil {
		return result, fmt.Errorf("读取Provider IPv6文件配置失败: %w", err)
	}
	rawPath := dbProvider.IPv6AddressFilePath
	if len(pathOverride) > 0 {
		rawPath = pathOverride[0]
	}
	filePath, err := ValidateNodeFilePath(rawPath)
	if err != nil {
		s.recordSyncError(providerID, err)
		return result, err
	}
	result.Path = filePath
	if len(pathOverride) > 0 && filePath != strings.TrimSpace(dbProvider.IPv6AddressFilePath) {
		if err := global.APP_DB.Model(&providerModel.Provider{}).Where("id = ?", providerID).
			Update("ipv6_address_file_path", filePath).Error; err != nil {
			return result, fmt.Errorf("保存节点IPv6地址文件路径失败: %w", err)
		}
	}

	reader := s.readNodeFile
	if reader == nil {
		reader = readProviderNodeFile
	}
	result.RemoteReadCount++
	output, err := reader(ctx, providerID, filePath)
	if err != nil {
		err = fmt.Errorf("读取节点IPv6地址文件失败: %w", err)
		s.recordSyncError(providerID, err)
		return result, err
	}

	parsed, invalid, err := parseIPv6PoolTextWithOptions(providerID, output, SourceNodeFile, true)
	result.InvalidLines = invalid
	result.ParsedCount = len(parsed)
	if err != nil {
		s.recordSyncError(providerID, err)
		return result, err
	}
	result.SyncedAt = time.Now()
	if err := s.reconcileNodeFile(providerID, parsed, &result); err != nil {
		s.recordSyncError(providerID, err)
		return result, err
	}
	stats, statsErr := s.GetPoolStatsDetail(providerID)
	if statsErr != nil {
		err = fmt.Errorf("读取IPv6地址池统计失败: %w", statsErr)
		s.recordSyncError(providerID, err)
		return result, err
	}
	result.Stats = stats
	result.Total, result.Allocated, result.Available = stats.Entries, stats.Allocated, stats.Available
	sort.Strings(result.Added)
	sort.Strings(result.Removed)
	sort.Strings(result.PreservedAllocated)
	return result, nil
}

func readProviderNodeFile(ctx context.Context, providerID uint, filePath string) (string, error) {
	providerInstance, err := runtimeProvider.EnsureProviderConnected(ctx, providerID)
	if err != nil {
		return "", fmt.Errorf("连接Provider以同步IPv6文件失败: %w", err)
	}
	quotedPath := utils.ShellSingleQuote(filePath)
	command := fmt.Sprintf("if [ ! -f %s ]; then echo 'IPv6 address file does not exist' >&2; exit 2; fi; cat -- %s", quotedPath, quotedPath)
	return providerInstance.ExecuteSSHCommand(ctx, command)
}

type nodeFileRemovalAction uint8

const (
	nodeFileDelete nodeFileRemovalAction = iota
	nodeFileRetire
)

func classifyNodeFileRemoval(entry providerModel.ProviderIPv6Pool, hasAllocatedChildren bool) nodeFileRemovalAction {
	if entry.IsAllocated || (entry.IsRange && hasAllocatedChildren) {
		return nodeFileRetire
	}
	return nodeFileDelete
}

func (s *Service) reconcileNodeFile(providerID uint, desired []providerModel.ProviderIPv6Pool, result *SyncResult) error {
	desiredByAddress := make(map[string]providerModel.ProviderIPv6Pool, len(desired))
	for _, entry := range desired {
		desiredByAddress[entry.Address] = entry
	}

	return global.APP_DB.Transaction(func(tx *gorm.DB) error {
		var lockedProvider providerModel.Provider
		if err := tx.Select("id").Where("id = ?", providerID).
			Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedProvider).Error; err != nil {
			return fmt.Errorf("锁定Provider IPv6文件同步失败: %w", err)
		}
		var existing []providerModel.ProviderIPv6Pool
		if err := tx.Unscoped().Where("provider_id = ? AND parent_id IS NULL", providerID).Find(&existing).Error; err != nil {
			return fmt.Errorf("读取IPv6地址池失败: %w", err)
		}
		byAddress := make(map[string]providerModel.ProviderIPv6Pool, len(existing))
		allocatedChildren := make(map[uint]bool)
		for _, entry := range existing {
			byAddress[entry.Address] = entry
		}
		var allocatedParentIDs []uint
		if err := tx.Model(&providerModel.ProviderIPv6Pool{}).Distinct("parent_id").
			Where("provider_id = ? AND parent_id IS NOT NULL AND is_allocated = ? AND deleted_at IS NULL", providerID, true).
			Pluck("parent_id", &allocatedParentIDs).Error; err != nil {
			return fmt.Errorf("读取已分配IPv6范围子项失败: %w", err)
		}
		for _, parentID := range allocatedParentIDs {
			allocatedChildren[parentID] = true
		}

		toCreate := make([]providerModel.ProviderIPv6Pool, 0)
		toRestore := make([]uint, 0)
		toRestoreRanges := make([]uint, 0)
		toReactivate := make([]uint, 0)
		for _, entry := range desired {
			old, found := byAddress[entry.Address]
			if !found {
				toCreate = append(toCreate, entry)
				result.Added = append(result.Added, entry.Address)
				continue
			}
			if old.DeletedAt != nil && !old.IsAllocated {
				toRestore = append(toRestore, old.ID)
				if old.IsRange {
					toRestoreRanges = append(toRestoreRanges, old.ID)
				}
				result.Added = append(result.Added, entry.Address)
			} else if old.DeletedAt == nil && old.PendingRetire {
				toReactivate = append(toReactivate, old.ID)
				if old.IsRange {
					toRestoreRanges = append(toRestoreRanges, old.ID)
				}
				result.Added = append(result.Added, entry.Address)
			}
		}

		toDelete := make([]uint, 0)
		rangesToDelete := make([]uint, 0)
		toRetire := make([]uint, 0)
		rangesToRetire := make([]uint, 0)
		for _, entry := range existing {
			if entry.DeletedAt != nil || entry.ParentID != nil || entry.Source != SourceNodeFile {
				continue
			}
			if _, keep := desiredByAddress[entry.Address]; keep {
				continue
			}
			if classifyNodeFileRemoval(entry, allocatedChildren[entry.ID]) == nodeFileRetire {
				toRetire = append(toRetire, entry.ID)
				if entry.IsRange {
					rangesToRetire = append(rangesToRetire, entry.ID)
				}
				result.PreservedAllocated = append(result.PreservedAllocated, entry.Address)
				continue
			}
			toDelete = append(toDelete, entry.ID)
			if entry.IsRange {
				rangesToDelete = append(rangesToDelete, entry.ID)
			}
			result.Removed = append(result.Removed, entry.Address)
		}

		if len(toRestore) > 0 {
			if err := tx.Unscoped().Model(&providerModel.ProviderIPv6Pool{}).Where("id IN ?", toRestore).
				Updates(map[string]interface{}{"deleted_at": nil, "source": SourceNodeFile, "pending_retire": false}).Error; err != nil {
				return fmt.Errorf("恢复节点IPv6条目失败: %w", err)
			}
		}
		if len(toReactivate) > 0 {
			if err := tx.Model(&providerModel.ProviderIPv6Pool{}).Where("id IN ?", toReactivate).
				Updates(map[string]interface{}{"source": SourceNodeFile, "pending_retire": false}).Error; err != nil {
				return fmt.Errorf("取消节点IPv6条目退休状态失败: %w", err)
			}
		}
		if len(toRestoreRanges) > 0 {
			if err := tx.Unscoped().Model(&providerModel.ProviderIPv6Pool{}).
				Where("parent_id IN ? AND is_allocated = ?", toRestoreRanges, false).
				Updates(map[string]interface{}{"deleted_at": nil, "pending_retire": false}).Error; err != nil {
				return fmt.Errorf("恢复节点IPv6范围已释放子项失败: %w", err)
			}
		}
		if len(toCreate) > 0 {
			if err := tx.CreateInBatches(&toCreate, 200).Error; err != nil {
				return fmt.Errorf("批量写入节点IPv6条目失败: %w", err)
			}
		}
		if len(toRetire) > 0 {
			if err := tx.Model(&providerModel.ProviderIPv6Pool{}).Where("id IN ?", toRetire).
				Update("pending_retire", true).Error; err != nil {
				return fmt.Errorf("标记节点IPv6条目待退休失败: %w", err)
			}
		}
		if len(rangesToRetire) > 0 {
			if err := tx.Where("parent_id IN ? AND is_allocated = ?", rangesToRetire, false).
				Delete(&providerModel.ProviderIPv6Pool{}).Error; err != nil {
				return fmt.Errorf("清理待退休IPv6范围的未分配子项失败: %w", err)
			}
		}
		if len(rangesToDelete) > 0 {
			if err := tx.Where("parent_id IN ? AND is_allocated = ?", rangesToDelete, false).
				Delete(&providerModel.ProviderIPv6Pool{}).Error; err != nil {
				return fmt.Errorf("移除已失效节点IPv6范围子项失败: %w", err)
			}
		}
		if len(toDelete) > 0 {
			if err := tx.Where("id IN ?", toDelete).Delete(&providerModel.ProviderIPv6Pool{}).Error; err != nil {
				return fmt.Errorf("移除已失效节点IPv6条目失败: %w", err)
			}
		}
		return tx.Model(&providerModel.Provider{}).Where("id = ?", providerID).Updates(map[string]interface{}{
			"ipv6_address_file_synced_at":  result.SyncedAt,
			"ipv6_address_file_sync_error": "",
		}).Error
	})
}

func (s *Service) recordSyncError(providerID uint, syncErr error) {
	message := syncErr.Error()
	if len(message) > 1000 {
		message = message[:1000]
	}
	_ = global.APP_DB.Model(&providerModel.Provider{}).Where("id = ?", providerID).
		Update("ipv6_address_file_sync_error", message).Error
}

func ValidateNodeFilePath(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", fmt.Errorf("未配置节点IPv6地址文件路径")
	}
	if len(value) > 512 || !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return "", fmt.Errorf("节点IPv6地址文件路径必须是规范的绝对路径")
	}
	for _, char := range value {
		if char == 0 || unicode.IsControl(char) {
			return "", fmt.Errorf("节点IPv6地址文件路径包含非法控制字符")
		}
	}
	return value, nil
}

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

func (s *Service) ClearUnallocated(providerID uint) (int64, error) {
	var deleted int64
	err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
		var lockedProvider providerModel.Provider
		if err := tx.Select("id").Where("id = ?", providerID).
			Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedProvider).Error; err != nil {
			return fmt.Errorf("锁定Provider IPv6地址池清理失败: %w", err)
		}
		result := tx.Where("provider_id = ? AND is_allocated = ? AND deleted_at IS NULL AND is_range = ?", providerID, false, false).
			Delete(&providerModel.ProviderIPv6Pool{})
		if result.Error != nil {
			return result.Error
		}
		deleted += result.RowsAffected
		result = tx.Where("provider_id = ? AND is_range = ? AND deleted_at IS NULL AND id NOT IN (?)", providerID, true,
			tx.Model(&providerModel.ProviderIPv6Pool{}).Select("parent_id").
				Where("provider_id = ? AND parent_id IS NOT NULL AND is_allocated = ? AND deleted_at IS NULL", providerID, true)).
			Delete(&providerModel.ProviderIPv6Pool{})
		if result.Error != nil {
			return result.Error
		}
		deleted += result.RowsAffected
		return nil
	})
	return deleted, err
}

func (s *Service) DeleteAddress(providerID, entryID uint) error {
	return global.APP_DB.Transaction(func(tx *gorm.DB) error {
		var lockedProvider providerModel.Provider
		if err := tx.Select("id").Where("id = ?", providerID).
			Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedProvider).Error; err != nil {
			return fmt.Errorf("锁定Provider IPv6地址删除失败: %w", err)
		}
		var entry providerModel.ProviderIPv6Pool
		if err := tx.Where("id = ? AND provider_id = ? AND deleted_at IS NULL", entryID, providerID).First(&entry).Error; err != nil {
			return fmt.Errorf("地址不存在")
		}
		if entry.IsAllocated {
			return fmt.Errorf("地址已分配，无法删除")
		}
		if entry.IsRange {
			var allocatedChildren int64
			if err := tx.Model(&providerModel.ProviderIPv6Pool{}).
				Where("parent_id = ? AND is_allocated = ? AND deleted_at IS NULL", entry.ID, true).
				Count(&allocatedChildren).Error; err != nil {
				return err
			}
			if allocatedChildren > 0 {
				return fmt.Errorf("地址范围仍有已分配地址，无法删除")
			}
			if err := tx.Where("parent_id = ? AND deleted_at IS NULL", entry.ID).
				Delete(&providerModel.ProviderIPv6Pool{}).Error; err != nil {
				return fmt.Errorf("删除地址范围已释放子项失败: %w", err)
			}
		}
		return tx.Delete(&entry).Error
	})
}

// AllocateIPv6Address performs only bounded, short database work. It returns
// an existing binding for idempotent task retries and reuses released children
// before advancing a range cursor.
func (s *Service) AllocateIPv6Address(providerID, instanceID uint) (string, error) {
	for {
		var allocated string
		advancedCursor := false
		exhausted := false
		err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
			// This short provider-row lock serializes allocation with pool sync and
			// makes same-instance retries deterministic without holding any remote I/O.
			var lockedProvider providerModel.Provider
			if err := tx.Select("id").Where("id = ?", providerID).
				Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedProvider).Error; err != nil {
				return fmt.Errorf("锁定Provider IPv6分配失败: %w", err)
			}

			var existing providerModel.ProviderIPv6Pool
			existingResult := tx.Where("provider_id = ? AND instance_id = ? AND is_allocated = ? AND deleted_at IS NULL", providerID, instanceID, true).
				Order("id ASC").First(&existing)
			if existingResult.Error == nil {
				allocated = existing.Address
				return nil
			}
			if !errors.Is(existingResult.Error, gorm.ErrRecordNotFound) {
				return fmt.Errorf("查询实例IPv6地址失败: %w", existingResult.Error)
			}

			// Released discrete rows and materialized range children are reused
			// before a range cursor advances.
			var entry providerModel.ProviderIPv6Pool
			query := tx.Where("provider_id = ? AND is_allocated = ? AND is_range = ? AND pending_retire = ? AND deleted_at IS NULL", providerID, false, false, false).
				Order("id ASC").First(&entry)
			if query.Error == nil {
				update := tx.Model(&providerModel.ProviderIPv6Pool{}).
					Where("id = ? AND is_allocated = ? AND pending_retire = ?", entry.ID, false, false).
					Updates(map[string]interface{}{"is_allocated": true, "instance_id": instanceID})
				if update.Error != nil {
					return fmt.Errorf("分配IPv6地址失败: %w", update.Error)
				}
				if update.RowsAffected != 1 {
					return fmt.Errorf("IPv6地址并发分配冲突")
				}
				allocated = entry.Address
				return nil
			}
			if !errors.Is(query.Error, gorm.ErrRecordNotFound) {
				return fmt.Errorf("查询可用IPv6地址失败: %w", query.Error)
			}

			var source providerModel.ProviderIPv6Pool
			rangeResult := tx.Where("provider_id = ? AND is_range = ? AND pending_retire = ? AND range_next <> '' AND deleted_at IS NULL", providerID, true, false).
				Order("id ASC").Clauses(clause.Locking{Strength: "UPDATE"}).First(&source)
			if errors.Is(rangeResult.Error, gorm.ErrRecordNotFound) {
				exhausted = true
				return nil
			}
			if rangeResult.Error != nil {
				return fmt.Errorf("查询IPv6地址范围失败: %w", rangeResult.Error)
			}

			candidates, nextWindow, err := ipv6RangeCandidateWindow(source, rangeScanWindowSize)
			if err != nil {
				return err
			}
			var occupied []string
			if err := tx.Unscoped().Model(&providerModel.ProviderIPv6Pool{}).
				Where("provider_id = ? AND address IN ?", providerID, candidates).
				Pluck("address", &occupied).Error; err != nil {
				return fmt.Errorf("批量读取IPv6地址占用状态失败: %w", err)
			}
			used := make(map[string]struct{}, len(occupied))
			for _, address := range occupied {
				used[address] = struct{}{}
			}

			candidate := ""
			candidateIndex := -1
			for index, address := range candidates {
				if _, exists := used[address]; !exists {
					candidate = address
					candidateIndex = index
					break
				}
			}

			newCursor := nextWindow
			if candidateIndex >= 0 && candidateIndex+1 < len(candidates) {
				newCursor = candidates[candidateIndex+1]
			}
			if candidate != "" {
				child := providerModel.ProviderIPv6Pool{
					ProviderID: providerID, Address: candidate, PrefixLength: 128,
					ParentID: &source.ID, IsAllocated: true, InstanceID: &instanceID,
					Source: SourceRangeChild,
				}
				createResult := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&child)
				if createResult.Error != nil {
					return fmt.Errorf("写入已分配IPv6地址失败: %w", createResult.Error)
				}
				if createResult.RowsAffected == 1 {
					allocated = candidate
				} else {
					// A defensive fallback for a concurrent unique-key winner. The
					// provider lock normally makes this path unreachable.
					var concurrent providerModel.ProviderIPv6Pool
					if err := tx.Where("provider_id = ? AND instance_id = ? AND is_allocated = ? AND deleted_at IS NULL", providerID, instanceID, true).
						First(&concurrent).Error; err == nil {
						allocated = concurrent.Address
					} else if !errors.Is(err, gorm.ErrRecordNotFound) {
						return fmt.Errorf("查询并发IPv6分配结果失败: %w", err)
					} else {
						return fmt.Errorf("IPv6地址并发分配冲突")
					}
				}
			}

			if err := tx.Model(&providerModel.ProviderIPv6Pool{}).Where("id = ?", source.ID).
				Update("range_next", newCursor).Error; err != nil {
				return fmt.Errorf("更新IPv6地址范围游标失败: %w", err)
			}
			advancedCursor = true
			return nil
		})
		if err != nil {
			return "", err
		}
		if allocated != "" {
			return allocated, nil
		}
		if exhausted {
			return "", fmt.Errorf("IPv6地址池已耗尽，没有可用地址")
		}
		if !advancedCursor {
			return "", fmt.Errorf("IPv6地址池无法前进到下一个候选地址")
		}
	}
}

// TransferIPv6BindingWithDB moves an existing allocation to a replacement
// instance without making the address available to another allocator between
// reset/delete and create. The provider-row lock serializes the move with pool
// sync, allocation and release, while the caller's transaction keeps the new
// instance record and the binding change atomic.
func (s *Service) TransferIPv6BindingWithDB(tx *gorm.DB, providerID, oldInstanceID, newInstanceID uint) (string, error) {
	if tx == nil {
		return "", fmt.Errorf("IPv6地址迁移缺少数据库会话")
	}
	if providerID == 0 || oldInstanceID == 0 || newInstanceID == 0 {
		return "", fmt.Errorf("IPv6地址迁移参数无效")
	}

	var lockedProvider providerModel.Provider
	if err := tx.Select("id").Where("id = ?", providerID).
		Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedProvider).Error; err != nil {
		return "", fmt.Errorf("锁定Provider IPv6地址迁移失败: %w", err)
	}

	var target providerModel.ProviderIPv6Pool
	targetResult := tx.Where("instance_id = ? AND is_allocated = ? AND deleted_at IS NULL", newInstanceID, true).
		Clauses(clause.Locking{Strength: "UPDATE"}).First(&target)
	if targetResult.Error == nil {
		if target.ProviderID != providerID {
			return "", fmt.Errorf("新实例已绑定其他Provider的IPv6地址")
		}
		var source providerModel.ProviderIPv6Pool
		sourceResult := tx.Where("provider_id = ? AND instance_id = ? AND is_allocated = ? AND deleted_at IS NULL", providerID, oldInstanceID, true).
			Clauses(clause.Locking{Strength: "UPDATE"}).First(&source)
		if sourceResult.Error == nil && source.ID != target.ID {
			return "", fmt.Errorf("新旧实例同时存在IPv6地址绑定，拒绝不明确的迁移")
		}
		if sourceResult.Error != nil && !errors.Is(sourceResult.Error, gorm.ErrRecordNotFound) {
			return "", fmt.Errorf("查询旧实例IPv6地址绑定失败: %w", sourceResult.Error)
		}
		return target.Address, nil
	}
	if !errors.Is(targetResult.Error, gorm.ErrRecordNotFound) {
		return "", fmt.Errorf("查询新实例IPv6地址绑定失败: %w", targetResult.Error)
	}

	var source providerModel.ProviderIPv6Pool
	sourceResult := tx.Where("provider_id = ? AND instance_id = ? AND is_allocated = ? AND deleted_at IS NULL", providerID, oldInstanceID, true).
		Clauses(clause.Locking{Strength: "UPDATE"}).First(&source)
	if errors.Is(sourceResult.Error, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if sourceResult.Error != nil {
		return "", fmt.Errorf("查询旧实例IPv6地址绑定失败: %w", sourceResult.Error)
	}

	result := tx.Model(&providerModel.ProviderIPv6Pool{}).
		Where("id = ? AND instance_id = ? AND is_allocated = ?", source.ID, oldInstanceID, true).
		Update("instance_id", newInstanceID)
	if result.Error != nil {
		return "", fmt.Errorf("迁移实例IPv6地址绑定失败: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return "", fmt.Errorf("IPv6地址绑定在迁移期间发生并发变化")
	}
	return source.Address, nil
}

func ipv6RangeCandidateWindow(source providerModel.ProviderIPv6Pool, limit int) ([]string, string, error) {
	if limit <= 0 {
		return nil, "", fmt.Errorf("IPv6范围扫描窗口必须大于0")
	}
	candidate := net.ParseIP(strings.TrimSpace(source.RangeNext)).To16()
	_, network, err := net.ParseCIDR(source.Address)
	if candidate == nil || err != nil || network == nil || !network.Contains(candidate) {
		return nil, "", fmt.Errorf("IPv6地址范围游标无效: %s", source.Address)
	}
	candidates := make([]string, 0, limit)
	for len(candidates) < limit && network.Contains(candidate) {
		candidates = append(candidates, candidate.String())
		next, ok := incrementIPv6(candidate)
		if !ok || !network.Contains(next) {
			return candidates, "", nil
		}
		candidate = next
	}
	return candidates, candidate.String(), nil
}

func (s *Service) ReleaseIPv6(instanceID uint) error {
	return global.APP_DB.Transaction(func(tx *gorm.DB) error {
		return s.ReleaseIPv6WithDB(tx, instanceID)
	})
}

// ReleaseIPv6WithDB centralizes normal release and pending-retire cleanup. The
// caller may pass an existing transaction so failure rollback paths do not
// bypass node-file retirement semantics.
func (s *Service) ReleaseIPv6WithDB(tx *gorm.DB, instanceID uint) error {
	if tx == nil {
		return fmt.Errorf("IPv6地址释放缺少数据库会话")
	}

	var providerIDs []uint
	if err := tx.Model(&providerModel.ProviderIPv6Pool{}).
		Where("instance_id = ? AND is_allocated = ? AND deleted_at IS NULL", instanceID, true).
		Distinct("provider_id").Pluck("provider_id", &providerIDs).Error; err != nil {
		return fmt.Errorf("查询IPv6地址绑定失败: %w", err)
	}
	if len(providerIDs) == 0 {
		return nil
	}
	sort.Slice(providerIDs, func(left, right int) bool { return providerIDs[left] < providerIDs[right] })
	var lockedProviders []providerModel.Provider
	if err := tx.Select("id").Where("id IN ?", providerIDs).Order("id ASC").
		Clauses(clause.Locking{Strength: "UPDATE"}).Find(&lockedProviders).Error; err != nil {
		return fmt.Errorf("锁定Provider IPv6地址释放失败: %w", err)
	}

	var bindings []providerModel.ProviderIPv6Pool
	if err := tx.Where("instance_id = ? AND is_allocated = ? AND deleted_at IS NULL", instanceID, true).
		Order("id ASC").Clauses(clause.Locking{Strength: "UPDATE"}).Find(&bindings).Error; err != nil {
		return fmt.Errorf("锁定IPv6地址绑定失败: %w", err)
	}
	for _, binding := range bindings {
		purge := binding.PendingRetire
		var parent providerModel.ProviderIPv6Pool
		if binding.ParentID != nil {
			parentResult := tx.Unscoped().Clauses(clause.Locking{Strength: "UPDATE"}).First(&parent, *binding.ParentID)
			if errors.Is(parentResult.Error, gorm.ErrRecordNotFound) {
				purge = true
			} else if parentResult.Error != nil {
				return fmt.Errorf("查询IPv6范围父项失败: %w", parentResult.Error)
			} else if parent.PendingRetire || parent.DeletedAt != nil {
				purge = true
			}
		}

		if !purge {
			if err := tx.Model(&providerModel.ProviderIPv6Pool{}).
				Where("id = ? AND is_allocated = ?", binding.ID, true).
				Updates(map[string]interface{}{"is_allocated": false, "instance_id": nil}).Error; err != nil {
				return fmt.Errorf("释放IPv6地址失败: %w", err)
			}
			continue
		}

		if err := tx.Unscoped().Delete(&providerModel.ProviderIPv6Pool{}, binding.ID).Error; err != nil {
			return fmt.Errorf("清理已退休IPv6地址失败: %w", err)
		}
		if binding.ParentID == nil || parent.ID == 0 {
			continue
		}
		var remainingAllocated int64
		if err := tx.Model(&providerModel.ProviderIPv6Pool{}).
			Where("parent_id = ? AND is_allocated = ? AND deleted_at IS NULL", parent.ID, true).
			Count(&remainingAllocated).Error; err != nil {
			return fmt.Errorf("检查待退休IPv6范围剩余绑定失败: %w", err)
		}
		if parent.PendingRetire && remainingAllocated == 0 {
			if err := tx.Unscoped().Where("parent_id = ?", parent.ID).
				Delete(&providerModel.ProviderIPv6Pool{}).Error; err != nil {
				return fmt.Errorf("清理已退休IPv6范围子项失败: %w", err)
			}
			if err := tx.Unscoped().Delete(&providerModel.ProviderIPv6Pool{}, parent.ID).Error; err != nil {
				return fmt.Errorf("清理已退休IPv6范围失败: %w", err)
			}
		}
	}
	return nil
}

func (s *Service) GetAllocatedAddress(instanceID uint) (string, error) {
	var entry providerModel.ProviderIPv6Pool
	err := global.APP_DB.Where("instance_id = ? AND is_allocated = ? AND deleted_at IS NULL", instanceID, true).
		Order("id ASC").First(&entry).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	return entry.Address, err
}

func (s *Service) GetPoolStats(providerID uint) (total, allocated, available int64) {
	stats, err := s.GetPoolStatsDetail(providerID)
	if err != nil {
		return 0, 0, 0
	}
	return stats.Entries, stats.Allocated, stats.Available
}

func (s *Service) GetPoolStatsDetail(providerID uint) (PoolStats, error) {
	var aggregate struct {
		Entries       int64
		Materialized  int64
		Ranges        int64
		OpenRanges    int64
		PendingRetire int64
		Allocated     int64
		Reusable      int64
	}
	if err := global.APP_DB.Model(&providerModel.ProviderIPv6Pool{}).
		Select(`COUNT(*) AS entries,
COALESCE(SUM(CASE WHEN is_range = 0 THEN 1 ELSE 0 END), 0) AS materialized,
COALESCE(SUM(CASE WHEN is_range = 1 THEN 1 ELSE 0 END), 0) AS ranges,
COALESCE(SUM(CASE WHEN is_range = 1 AND pending_retire = 0 AND range_next <> '' THEN 1 ELSE 0 END), 0) AS open_ranges,
COALESCE(SUM(CASE WHEN pending_retire = 1 THEN 1 ELSE 0 END), 0) AS pending_retire,
COALESCE(SUM(CASE WHEN is_range = 0 AND is_allocated = 1 THEN 1 ELSE 0 END), 0) AS allocated,
COALESCE(SUM(CASE WHEN is_range = 0 AND is_allocated = 0 AND pending_retire = 0 THEN 1 ELSE 0 END), 0) AS reusable`).
		Where("provider_id = ? AND deleted_at IS NULL", providerID).Scan(&aggregate).Error; err != nil {
		return PoolStats{}, err
	}

	var openRanges []providerModel.ProviderIPv6Pool
	if err := global.APP_DB.Select("address", "range_next").
		Where("provider_id = ? AND is_range = ? AND pending_retire = ? AND range_next <> '' AND deleted_at IS NULL", providerID, true, false).
		Find(&openRanges).Error; err != nil {
		return PoolStats{}, err
	}
	available := big.NewInt(aggregate.Reusable)
	for _, entry := range openRanges {
		remaining, err := remainingIPv6RangeCapacity(entry.Address, entry.RangeNext)
		if err != nil {
			return PoolStats{}, err
		}
		available.Add(available, remaining)
	}

	const maxInt64 = int64(^uint64(0) >> 1)
	numericAvailable := maxInt64
	saturated := !available.IsInt64()
	if !saturated {
		numericAvailable = available.Int64()
	}
	return PoolStats{
		Entries: aggregate.Entries, Materialized: aggregate.Materialized,
		Ranges: aggregate.Ranges, OpenRanges: aggregate.OpenRanges,
		PendingRetire: aggregate.PendingRetire, Allocated: aggregate.Allocated, Reusable: aggregate.Reusable,
		Available: numericAvailable, AvailableExact: available.String(),
		AvailableSaturated: saturated,
	}, nil
}

func remainingIPv6RangeCapacity(cidr, nextValue string) (*big.Int, error) {
	next := net.ParseIP(strings.TrimSpace(nextValue)).To16()
	_, network, err := net.ParseCIDR(strings.TrimSpace(cidr))
	if err != nil || network == nil || next == nil || !network.Contains(next) {
		return nil, fmt.Errorf("无效的IPv6范围游标: %s", cidr)
	}
	ones, bits := network.Mask.Size()
	if bits != 128 || ones < 0 || ones > 128 {
		return nil, fmt.Errorf("无效的IPv6范围前缀: %s", cidr)
	}
	base := new(big.Int).SetBytes(network.IP.To16())
	last := new(big.Int).Lsh(big.NewInt(1), uint(128-ones))
	last.Sub(last, big.NewInt(1)).Add(last, base)
	remaining := new(big.Int).Sub(last, new(big.Int).SetBytes(next))
	return remaining.Add(remaining, big.NewInt(1)), nil
}
