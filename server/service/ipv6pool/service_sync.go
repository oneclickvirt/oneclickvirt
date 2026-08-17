package ipv6pool

import (
	"context"
	"fmt"
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
	db           *gorm.DB
}

// database returns the connection owned by this service. Keeping the fallback
// to the process-global handle preserves the existing constructor contract,
// while embedded workflows and tests can use an isolated database safely.
func (s *Service) database() *gorm.DB {
	if s != nil && s.db != nil {
		return s.db
	}
	return global.APP_DB
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
	Reserved           int64  `json:"reserved"`
	Allocated          int64  `json:"allocated"`
	Reusable           int64  `json:"reusable"`
	Available          int64  `json:"available"`
	AvailableExact     string `json:"availableExact"`
	AvailableSaturated bool   `json:"availableSaturated"`
}

func NewService() *Service {
	return &Service{readNodeFile: readProviderNodeFile, db: global.APP_DB}
}

// NewServiceWithDB is used by embedded workflows and tests that keep their own
// short-lived database connection instead of the process-global controller DB.
func NewServiceWithDB(db *gorm.DB) *Service {
	return &Service{readNodeFile: readProviderNodeFile, db: db}
}

// SupportsStaticIPv6 reports providers whose current create path can consume
// a controller-selected address. VM backends that need a routed bridge are
// included here so tunnel-backed pools can be allocated before their own
// capability preflight rejects an incompatible host.
func SupportsStaticIPv6(providerType string) bool {
	switch strings.ToLower(strings.TrimSpace(providerType)) {
	case "lxd", "incus", "proxmox", "proxmoxve", "docker", "podman", "containerd", "orbstack", "qemu", "kubevirt", "vmware", "virtualbox", "multipass", "vagrant":
		return true
	default:
		return false
	}
}

// RequiresRoutedStaticIPv6 identifies providers whose guest network cannot
// safely consume a bare controller pool address. Callers should require a
// non-empty allocation CIDR for these providers and release an incompatible
// allocation before making any remote mutation.
func RequiresRoutedStaticIPv6(providerType string) bool {
	switch strings.ToLower(strings.TrimSpace(providerType)) {
	case "qemu", "kubevirt", "vmware", "virtualbox", "multipass", "vagrant":
		return true
	default:
		return false
	}
}

func (s *Service) HasConfiguredPool(providerID uint) (bool, error) {
	var count int64
	db := s.database()
	if db == nil {
		return false, fmt.Errorf("数据库连接不可用")
	}
	err := db.Model(&providerModel.ProviderIPv6Pool{}).
		Where("provider_id = ? AND parent_id IS NULL AND pending_retire = ? AND deleted_at IS NULL", providerID, false).
		Count(&count).Error
	return count > 0, err
}

func (s *Service) GetIPv6Pool(providerID uint, page, pageSize int) ([]providerModel.ProviderIPv6Pool, int64, error) {
	var entries []providerModel.ProviderIPv6Pool
	var total int64
	db := s.database()
	if db == nil {
		return nil, 0, fmt.Errorf("数据库连接不可用")
	}
	query := db.Model(&providerModel.ProviderIPv6Pool{}).
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

	db := s.database()
	if db == nil {
		return nil, invalid, fmt.Errorf("数据库连接不可用")
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		var lockedProvider providerModel.Provider
		if err := tx.Select("id", "type").Where("id = ?", providerID).
			Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedProvider).Error; err != nil {
			return fmt.Errorf("锁定Provider IPv6地址池失败: %w", err)
		}
		if RequiresRoutedStaticIPv6(lockedProvider.Type) {
			return fmt.Errorf("Provider类型 %s 仅支持由IPv6隧道自动管理的路由前缀，不能手工配置静态IPv6地址池", lockedProvider.Type)
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
			if old.TunnelID != nil || old.Source == SourceTunnel {
				return fmt.Errorf("地址 %s 由IPv6隧道自动管理，不能通过手工地址池覆盖", entry.Address)
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
	db := s.database()
	if db == nil {
		return result, fmt.Errorf("数据库连接不可用")
	}
	if err := db.Select("id", "type", "ipv6_address_file_path").First(&dbProvider, providerID).Error; err != nil {
		return result, fmt.Errorf("读取Provider IPv6文件配置失败: %w", err)
	}
	if RequiresRoutedStaticIPv6(dbProvider.Type) {
		return result, fmt.Errorf("Provider类型 %s 仅支持由IPv6隧道自动管理的路由前缀，不能同步节点静态IPv6地址文件", dbProvider.Type)
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
		if err := db.Model(&providerModel.Provider{}).Where("id = ?", providerID).
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

	db := s.database()
	if db == nil {
		return fmt.Errorf("数据库连接不可用")
	}
	return db.Transaction(func(tx *gorm.DB) error {
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
	db := s.database()
	if db == nil {
		return
	}
	_ = db.Model(&providerModel.Provider{}).Where("id = ?", providerID).
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
