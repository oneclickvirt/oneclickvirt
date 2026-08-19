package ipv6tunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	ipv6poolService "oneclickvirt/service/ipv6pool"
	runtimeProvider "oneclickvirt/service/provider"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	defaultMTU         = 1480
	defaultTTL         = 255
	defaultRouteMetric = 100
	maxRemoteError     = 2000
)

var (
	interfacePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,14}$`)
	namePattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_. -]{0,63}$`)
	providerLocks    sync.Map
)

type Config struct {
	Name         string `json:"name"`
	Mode         string `json:"mode"`
	Interface    string `json:"interfaceName"`
	LocalIPv4    string `json:"localIpv4"`
	RemoteIPv4   string `json:"remoteIpv4"`
	LocalIPv6    string `json:"localIpv6"`
	RemoteIPv6   string `json:"remoteIpv6"`
	RoutedCIDR   string `json:"routedCidr"`
	MTU          int    `json:"mtu"`
	TTL          int    `json:"ttl"`
	RouteMetric  int    `json:"routeMetric"`
	DefaultRoute bool   `json:"defaultRoute"`
}

type CreateRequest struct {
	Config
	Enabled bool `json:"enabled"`
}

type remoteExecutor func(context.Context, uint, string) (string, error)

type Service struct {
	db      *gorm.DB
	execute remoteExecutor
}

type checkState struct {
	UnitEnabled     bool
	UnitActive      bool
	LinkPresent     bool
	AddressOK       bool
	RouteOK         bool
	NetworkConfigOK bool
	GatewayOK       bool
	RoutedOK        bool
	ForwardingOK    bool
	PolicyRouteOK   bool
}

func NewService() *Service {
	return &Service{db: global.APP_DB, execute: executeOnProvider}
}

func executeOnProvider(ctx context.Context, providerID uint, command string) (string, error) {
	providerInstance, err := runtimeProvider.EnsureProviderConnected(ctx, providerID)
	if err != nil {
		return "", fmt.Errorf("连接Provider失败: %w", err)
	}
	return providerInstance.ExecuteSSHCommand(ctx, command)
}

func providerMutex(providerID uint) *sync.Mutex {
	value, _ := providerLocks.LoadOrStore(providerID, &sync.Mutex{})
	return value.(*sync.Mutex)
}

func (s *Service) database() (*gorm.DB, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("数据库连接不可用")
	}
	return s.db, nil
}

func (s *Service) List(providerID uint) ([]providerModel.ProviderIPv6Tunnel, error) {
	db, err := s.database()
	if err != nil {
		return nil, err
	}
	var tunnels []providerModel.ProviderIPv6Tunnel
	if err := db.Where("provider_id = ?", providerID).Order("id ASC").Find(&tunnels).Error; err != nil {
		return nil, fmt.Errorf("读取IPv6隧道失败: %w", err)
	}
	return tunnels, nil
}

func (s *Service) Create(ctx context.Context, providerID uint, request CreateRequest) (*providerModel.ProviderIPv6Tunnel, error) {
	lock := providerMutex(providerID)
	lock.Lock()
	defer lock.Unlock()

	db, err := s.database()
	if err != nil {
		return nil, err
	}
	if err := ensureProviderExists(db, providerID); err != nil {
		return nil, err
	}
	// Validate every user-controlled field before the remote probe. The probe is
	// intentionally outside a database transaction, so a slow or unavailable
	// node can never hold database locks.
	config, err := normalizeConfigBase(request.Config)
	if err != nil {
		return nil, err
	}
	if err := ensureInterfaceAvailable(db, providerID, config.Interface, 0); err != nil {
		return nil, err
	}
	if config.LocalIPv4 == "" {
		detection, detectErr := s.detectLocalIPv4(ctx, providerID, config.RemoteIPv4)
		if detectErr != nil {
			return nil, detectErr
		}
		config.LocalIPv4 = detection.LocalIPv4
	}
	config, err = normalizeConfigLocalIPv4(config)
	if err != nil {
		return nil, err
	}
	tunnel := config.toModel(providerID)
	tunnel.Enabled = request.Enabled
	if request.Enabled {
		tunnel.Status = providerModel.IPv6TunnelStatusPending
	} else {
		tunnel.Status = providerModel.IPv6TunnelStatusInactive
	}
	if err := db.Create(&tunnel).Error; err != nil {
		return nil, fmt.Errorf("保存IPv6隧道失败: %w", err)
	}
	if !request.Enabled {
		return &tunnel, nil
	}

	if err := s.applyRemote(ctx, tunnel); err != nil {
		if stateErr := s.recordState(tunnel.ID, providerModel.IPv6TunnelStatusError, err); stateErr != nil {
			return &tunnel, fmt.Errorf("应用IPv6隧道失败且状态保存失败: %v; %w", stateErr, err)
		}
		_ = db.First(&tunnel, tunnel.ID).Error
		return &tunnel, err
	}
	if err := ipv6poolService.NewServiceWithDB(db).SyncTunnelPool(providerID, tunnel.ID, tunnel.RoutedCIDR, true); err != nil {
		rollbackErr := s.disableRemote(ctx, tunnel)
		combined := fmt.Errorf("隧道已在节点应用，但IPv6地址池同步失败: %w", err)
		if rollbackErr != nil {
			combined = fmt.Errorf("%v；回滚节点隧道失败: %w", combined, rollbackErr)
		}
		_ = s.recordState(tunnel.ID, providerModel.IPv6TunnelStatusError, combined)
		return &tunnel, combined
	}
	if err := s.recordState(tunnel.ID, providerModel.IPv6TunnelStatusActive, nil); err != nil {
		return &tunnel, fmt.Errorf("节点隧道已生效但状态保存失败: %w", err)
	}
	_ = db.First(&tunnel, tunnel.ID).Error
	return &tunnel, nil
}

func (s *Service) Update(ctx context.Context, providerID, tunnelID uint, request Config) (*providerModel.ProviderIPv6Tunnel, error) {
	lock := providerMutex(providerID)
	lock.Lock()
	defer lock.Unlock()

	db, err := s.database()
	if err != nil {
		return nil, err
	}
	var current providerModel.ProviderIPv6Tunnel
	if err := db.Where("id = ? AND provider_id = ?", tunnelID, providerID).First(&current).Error; err != nil {
		return nil, tunnelLookupError(err)
	}
	config, err := normalizeConfigBase(request)
	if err != nil {
		return nil, err
	}
	if err := ensureInterfaceAvailable(db, providerID, config.Interface, tunnelID); err != nil {
		return nil, err
	}
	if config.LocalIPv4 == "" {
		detection, detectErr := s.detectLocalIPv4(ctx, providerID, config.RemoteIPv4)
		if detectErr != nil {
			return nil, detectErr
		}
		config.LocalIPv4 = detection.LocalIPv4
	}
	config, err = normalizeConfigLocalIPv4(config)
	if err != nil {
		return nil, err
	}
	candidate := config.toModel(providerID)
	candidate.ID = current.ID
	candidate.CreatedAt = current.CreatedAt
	candidate.Enabled = current.Enabled
	candidate.Status = current.Status
	candidate.LastError = current.LastError
	candidate.LastCheckedAt = current.LastCheckedAt

	if !current.Enabled {
		candidate.Status = providerModel.IPv6TunnelStatusInactive
		candidate.LastError = ""
		if err := db.Save(&candidate).Error; err != nil {
			return nil, fmt.Errorf("更新IPv6隧道失败: %w", err)
		}
		// A previous disable can have completed on the host while a short DB
		// reconciliation failed. Updating an inactive tunnel is a natural repair
		// point: retire the old managed pool so its prefix cannot be allocated
		// until this tunnel is explicitly enabled again.
		if err := ipv6poolService.NewServiceWithDB(db).SyncTunnelPool(providerID, tunnelID, "", false); err != nil {
			candidate.Status = providerModel.IPv6TunnelStatusError
			candidate.LastError = limitError(err)
			now := time.Now()
			candidate.LastCheckedAt = &now
			if saveErr := db.Save(&candidate).Error; saveErr != nil {
				return &candidate, fmt.Errorf("更新IPv6隧道后退休旧地址池失败且状态保存失败: %v; %w", saveErr, err)
			}
			return &candidate, fmt.Errorf("更新未启用IPv6隧道后退休旧地址池失败: %w", err)
		}
		return &candidate, nil
	}

	candidate.Status = providerModel.IPv6TunnelStatusPending
	candidate.LastError = ""
	if err := db.Save(&candidate).Error; err != nil {
		return nil, fmt.Errorf("保存IPv6隧道期望配置失败: %w", err)
	}
	if err := s.applyRemote(ctx, candidate); err != nil {
		// The remote apply command restores the previous unit before returning an
		// error. Persist that same previous configuration so DB and node agree.
		current.Status = providerModel.IPv6TunnelStatusError
		current.LastError = limitError(err)
		now := time.Now()
		current.LastCheckedAt = &now
		if saveErr := db.Save(&current).Error; saveErr != nil {
			return &candidate, fmt.Errorf("应用新隧道失败且恢复数据库配置失败: %v; %w", saveErr, err)
		}
		return &current, err
	}
	if err := ipv6poolService.NewServiceWithDB(db).SyncTunnelPool(providerID, candidate.ID, candidate.RoutedCIDR, true); err != nil {
		rollbackErr := s.applyRemote(ctx, current)
		current.Status = providerModel.IPv6TunnelStatusError
		current.LastError = limitError(fmt.Errorf("新隧道已在节点应用，但IPv6地址池同步失败: %v", err))
		now := time.Now()
		current.LastCheckedAt = &now
		if saveErr := db.Save(&current).Error; saveErr != nil {
			return &current, fmt.Errorf("IPv6地址池同步失败且恢复数据库配置失败: %v; %w", saveErr, err)
		}
		if rollbackErr != nil {
			return &current, fmt.Errorf("IPv6地址池同步失败，且回滚节点隧道失败: %v; %w", rollbackErr, err)
		}
		return &current, fmt.Errorf("IPv6地址池同步失败，已恢复节点隧道: %w", err)
	}
	candidate.Status = providerModel.IPv6TunnelStatusActive
	candidate.LastError = ""
	now := time.Now()
	candidate.LastCheckedAt = &now
	if err := db.Save(&candidate).Error; err != nil {
		return &candidate, fmt.Errorf("节点配置已生效但状态保存失败: %w", err)
	}
	return &candidate, nil
}

func (s *Service) SetEnabled(ctx context.Context, providerID, tunnelID uint, enabled bool) (*providerModel.ProviderIPv6Tunnel, error) {
	lock := providerMutex(providerID)
	lock.Lock()
	defer lock.Unlock()

	db, err := s.database()
	if err != nil {
		return nil, err
	}
	var tunnel providerModel.ProviderIPv6Tunnel
	if err := db.Where("id = ? AND provider_id = ?", tunnelID, providerID).First(&tunnel).Error; err != nil {
		return nil, tunnelLookupError(err)
	}
	if enabled {
		if _, modeErr := normalizeTunnelMode(tunnel.Mode); modeErr != nil {
			_ = s.recordState(tunnel.ID, providerModel.IPv6TunnelStatusError, modeErr)
			_ = db.First(&tunnel, tunnel.ID).Error
			return &tunnel, modeErr
		}
	}
	if tunnel.Enabled == enabled && ((enabled && tunnel.Status == providerModel.IPv6TunnelStatusActive) || (!enabled && tunnel.Status == providerModel.IPv6TunnelStatusInactive)) {
		return &tunnel, nil
	}

	tunnel.Enabled = enabled
	tunnel.Status = providerModel.IPv6TunnelStatusPending
	tunnel.LastError = ""
	if err := db.Save(&tunnel).Error; err != nil {
		return nil, fmt.Errorf("保存IPv6隧道期望状态失败: %w", err)
	}
	if enabled {
		err = s.applyRemote(ctx, tunnel)
	} else {
		err = s.disableRemote(ctx, tunnel)
	}
	if err != nil {
		if stateErr := s.recordState(tunnel.ID, providerModel.IPv6TunnelStatusError, err); stateErr != nil {
			return &tunnel, fmt.Errorf("IPv6隧道操作失败且状态保存失败: %v; %w", stateErr, err)
		}
		_ = db.First(&tunnel, tunnel.ID).Error
		return &tunnel, err
	}
	if enabled {
		if poolErr := ipv6poolService.NewServiceWithDB(db).SyncTunnelPool(providerID, tunnel.ID, tunnel.RoutedCIDR, true); poolErr != nil {
			rollbackErr := s.disableRemote(ctx, tunnel)
			combined := fmt.Errorf("启用隧道成功但IPv6地址池同步失败: %w", poolErr)
			if rollbackErr != nil {
				combined = fmt.Errorf("%v；回滚节点隧道失败: %w", combined, rollbackErr)
			}
			_ = s.recordState(tunnel.ID, providerModel.IPv6TunnelStatusError, combined)
			return &tunnel, combined
		}
	} else if poolErr := ipv6poolService.NewServiceWithDB(db).SyncTunnelPool(providerID, tunnel.ID, tunnel.RoutedCIDR, false); poolErr != nil {
		// The remote tunnel is already disabled. Keep the desired state and expose
		// the pool-retirement failure instead of pretending cleanup completed.
		_ = s.recordState(tunnel.ID, providerModel.IPv6TunnelStatusError, poolErr)
		return &tunnel, fmt.Errorf("禁用隧道成功但IPv6地址池退休失败: %w", poolErr)
	}
	status := providerModel.IPv6TunnelStatusInactive
	if enabled {
		status = providerModel.IPv6TunnelStatusActive
	}
	if err := s.recordState(tunnel.ID, status, nil); err != nil {
		return &tunnel, fmt.Errorf("节点隧道状态已变更但状态保存失败: %w", err)
	}
	_ = db.First(&tunnel, tunnel.ID).Error
	return &tunnel, nil
}

// CheckAll performs one remote command for every tunnel on a Provider, then
// updates all cached states locally. Listing itself never triggers remote I/O.
func (s *Service) CheckAll(ctx context.Context, providerID uint) ([]providerModel.ProviderIPv6Tunnel, error) {
	lock := providerMutex(providerID)
	lock.Lock()
	defer lock.Unlock()

	tunnels, err := s.List(providerID)
	if err != nil || len(tunnels) == 0 {
		return tunnels, err
	}
	output, err := s.run(ctx, providerID, buildCheckCommand(tunnels))
	if err != nil {
		remoteErr := newRemoteCommandError("检查IPv6隧道状态失败", output, err)
		s.recordProviderError(providerID, remoteErr)
		return nil, remoteErr
	}
	states := parseCheckOutput(output)
	db, _ := s.database()
	now := time.Now()
	for index := range tunnels {
		state, found := states[tunnels[index].ID]
		tunnels[index].Status, tunnels[index].LastError = classifyState(tunnels[index], state, found)
		tunnels[index].LastCheckedAt = &now
	}
	if err := db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"status", "last_error", "last_checked_at",
		}),
	}).CreateInBatches(&tunnels, 500).Error; err != nil {
		return nil, fmt.Errorf("保存IPv6隧道状态失败: %w", err)
	}
	return s.List(providerID)
}

func (s *Service) Delete(ctx context.Context, providerID, tunnelID uint) error {
	lock := providerMutex(providerID)
	lock.Lock()
	defer lock.Unlock()

	db, err := s.database()
	if err != nil {
		return err
	}
	tunnel, err := prepareTunnelDelete(db, providerID, tunnelID)
	if err != nil {
		return err
	}
	if err := s.deleteRemote(ctx, []providerModel.ProviderIPv6Tunnel{tunnel}); err != nil {
		_ = s.recordState(tunnel.ID, providerModel.IPv6TunnelStatusError, err)
		return err
	}
	if err := ipv6poolService.NewServiceWithDB(db).SyncTunnelPool(providerID, tunnel.ID, tunnel.RoutedCIDR, false); err != nil {
		_ = s.recordState(tunnel.ID, providerModel.IPv6TunnelStatusError, err)
		return fmt.Errorf("节点隧道已删除但IPv6地址池清理失败: %w", err)
	}
	if err := db.Delete(&providerModel.ProviderIPv6Tunnel{}, tunnel.ID).Error; err != nil {
		return fmt.Errorf("节点隧道已删除但数据库记录清理失败: %w", err)
	}
	return nil
}

// prepareTunnelDelete closes the allocation race before host cleanup.  The
// Provider row lock is shared with IPv6 allocation, so an allocation which
// has already started finishes before the binding count is read; after the
// pool is marked pending-retire, no later allocation can select this tunnel.
// The remote operation deliberately happens after this short transaction.
func prepareTunnelDelete(db *gorm.DB, providerID, tunnelID uint) (providerModel.ProviderIPv6Tunnel, error) {
	var tunnel providerModel.ProviderIPv6Tunnel
	if db == nil {
		return tunnel, fmt.Errorf("数据库连接不可用")
	}

	err := db.Transaction(func(tx *gorm.DB) error {
		var provider providerModel.Provider
		if err := tx.Select("id").Where("id = ?", providerID).
			Clauses(clause.Locking{Strength: "UPDATE"}).First(&provider).Error; err != nil {
			return fmt.Errorf("锁定Provider隧道删除失败: %w", err)
		}
		if err := tx.Where("id = ? AND provider_id = ?", tunnelID, providerID).First(&tunnel).Error; err != nil {
			return tunnelLookupError(err)
		}

		// New materialized children carry TunnelID themselves.  The parent join
		// also covers bindings created before that metadata was persisted.
		var allocated int64
		if err := tx.Table("provider_ipv6_pools AS child").
			Joins("LEFT JOIN provider_ipv6_pools AS parent ON parent.id = child.parent_id AND parent.deleted_at IS NULL").
			Where("child.provider_id = ? AND child.is_allocated = ? AND child.is_reserved = ? AND child.deleted_at IS NULL AND (child.tunnel_id = ? OR parent.tunnel_id = ?)", providerID, true, false, tunnelID, tunnelID).
			Count(&allocated).Error; err != nil {
			return fmt.Errorf("检查隧道路由IPv6绑定失败: %w", err)
		}
		if allocated > 0 {
			return fmt.Errorf("隧道路由前缀仍有 %d 个实例地址绑定，无法删除；请先释放或迁移实例", allocated)
		}

		parentIDs := tx.Model(&providerModel.ProviderIPv6Pool{}).
			Select("id").Where("provider_id = ? AND tunnel_id = ?", providerID, tunnelID)
		if err := tx.Model(&providerModel.ProviderIPv6Pool{}).
			Where("provider_id = ? AND deleted_at IS NULL AND (tunnel_id = ? OR parent_id IN (?))", providerID, tunnelID, parentIDs).
			Update("pending_retire", true).Error; err != nil {
			return fmt.Errorf("冻结隧道IPv6地址池失败: %w", err)
		}

		now := time.Now()
		if err := tx.Model(&providerModel.ProviderIPv6Tunnel{}).Where("id = ?", tunnelID).
			Updates(map[string]interface{}{
				"enabled":         false,
				"status":          providerModel.IPv6TunnelStatusPending,
				"last_error":      "",
				"last_checked_at": now,
			}).Error; err != nil {
			return fmt.Errorf("保存IPv6隧道删除状态失败: %w", err)
		}
		tunnel.Enabled = false
		tunnel.Status = providerModel.IPv6TunnelStatusPending
		tunnel.LastError = ""
		tunnel.LastCheckedAt = &now
		return nil
	})
	return tunnel, err
}

// CleanupProviderRemote removes every managed host tunnel with a single remote
// command. Controller rows remain for the caller's short database transaction.
// Force mode is explicitly database-only and therefore skips this remote step.
func (s *Service) CleanupProviderRemote(ctx context.Context, providerID uint, force bool) error {
	lock := providerMutex(providerID)
	lock.Lock()
	defer lock.Unlock()

	tunnels, err := s.List(providerID)
	if err != nil {
		return err
	}
	if len(tunnels) == 0 {
		return nil
	}
	if !force {
		if err := s.deleteRemote(ctx, tunnels); err != nil {
			return fmt.Errorf("清理Provider IPv6隧道失败: %w", err)
		}
	}
	return nil
}

func (s *Service) applyRemote(ctx context.Context, tunnel providerModel.ProviderIPv6Tunnel) error {
	mode, err := normalizeTunnelMode(tunnel.Mode)
	if err != nil {
		return err
	}
	if strings.TrimSpace(tunnel.RoutedCIDR) != "" {
		if _, _, err := tunnelPolicyRouteParameters(tunnel.ID); err != nil {
			return fmt.Errorf("路由IPv6网段策略路由不可用: %w", err)
		}
	}
	tunnel.Mode = mode
	output, err := s.run(ctx, tunnel.ProviderID, buildApplyCommand(tunnel))
	if err != nil {
		return newRemoteCommandError("应用IPv6隧道失败", output, err)
	}
	return nil
}

func (s *Service) disableRemote(ctx context.Context, tunnel providerModel.ProviderIPv6Tunnel) error {
	output, err := s.run(ctx, tunnel.ProviderID, buildDisableCommand(tunnel))
	if err != nil {
		return newRemoteCommandError("禁用IPv6隧道失败", output, err)
	}
	return nil
}

func (s *Service) deleteRemote(ctx context.Context, tunnels []providerModel.ProviderIPv6Tunnel) error {
	if len(tunnels) == 0 {
		return nil
	}
	output, err := s.run(ctx, tunnels[0].ProviderID, buildDeleteCommand(tunnels))
	if err != nil {
		return newRemoteCommandError("删除节点IPv6隧道失败", output, err)
	}
	return nil
}

func (s *Service) run(ctx context.Context, providerID uint, command string) (string, error) {
	executor := s.execute
	if executor == nil {
		executor = executeOnProvider
	}
	return executor(ctx, providerID, command)
}

func (s *Service) recordState(id uint, status string, stateErr error) error {
	db, err := s.database()
	if err != nil {
		return err
	}
	now := time.Now()
	lastError := ""
	if stateErr != nil {
		lastError = limitError(stateErr)
	}
	if err := db.Model(&providerModel.ProviderIPv6Tunnel{}).Where("id = ?", id).
		Updates(map[string]interface{}{"status": status, "last_error": lastError, "last_checked_at": now}).Error; err != nil {
		return fmt.Errorf("保存IPv6隧道状态失败: %w", err)
	}
	return nil
}

func (s *Service) recordProviderError(providerID uint, stateErr error) {
	db, err := s.database()
	if err != nil {
		return
	}
	now := time.Now()
	_ = db.Model(&providerModel.ProviderIPv6Tunnel{}).Where("provider_id = ?", providerID).
		Updates(map[string]interface{}{"status": providerModel.IPv6TunnelStatusError, "last_error": limitError(stateErr), "last_checked_at": now}).Error
}

func normalizeConfig(input Config) (Config, error) {
	config, err := normalizeConfigBase(input)
	if err != nil {
		return Config{}, err
	}
	return normalizeConfigLocalIPv4(config)
}

// normalizeConfigBase validates the full tunnel configuration except the
// client IPv4. Create and update can fill that value from the node's route
// source after this safe local validation has completed.
func normalizeConfigBase(input Config) (Config, error) {
	config := input
	config.Name = strings.TrimSpace(config.Name)
	config.Mode = strings.ToLower(strings.TrimSpace(config.Mode))
	config.Interface = strings.TrimSpace(config.Interface)
	config.LocalIPv4 = strings.TrimSpace(config.LocalIPv4)
	config.RemoteIPv4 = strings.TrimSpace(config.RemoteIPv4)
	config.LocalIPv6 = strings.TrimSpace(config.LocalIPv6)
	config.RemoteIPv6 = strings.TrimSpace(config.RemoteIPv6)
	config.RoutedCIDR = strings.TrimSpace(config.RoutedCIDR)

	for field, value := range map[string]string{
		"名称": config.Name, "模式": config.Mode, "接口名": config.Interface,
		"本地IPv4": config.LocalIPv4, "远端IPv4": config.RemoteIPv4,
		"本地IPv6": config.LocalIPv6, "远端IPv6": config.RemoteIPv6, "路由网段": config.RoutedCIDR,
	} {
		if containsUnsafeText(value) {
			return Config{}, fmt.Errorf("%s包含控制字符", field)
		}
	}
	if !namePattern.MatchString(config.Name) {
		return Config{}, fmt.Errorf("隧道名称仅支持1至64位字母、数字、空格、点、下划线和连字符")
	}
	mode, err := normalizeTunnelMode(config.Mode)
	if err != nil {
		return Config{}, err
	}
	config.Mode = mode
	if !interfacePattern.MatchString(config.Interface) || config.Interface == "lo" {
		return Config{}, fmt.Errorf("接口名必须是1至15位Linux接口名且不能为lo")
	}
	remote4, err := normalizeIPv4(config.RemoteIPv4)
	if err != nil {
		return Config{}, fmt.Errorf("远端IPv4无效: %w", err)
	}
	config.RemoteIPv4 = remote4

	local6, err := normalizeIPv6CIDR(config.LocalIPv6)
	if err != nil {
		return Config{}, fmt.Errorf("本地IPv6无效: %w", err)
	}
	remote6, err := normalizeIPv6Address(config.RemoteIPv6)
	if err != nil {
		return Config{}, fmt.Errorf("远端IPv6无效: %w", err)
	}
	config.LocalIPv6, config.RemoteIPv6 = local6, remote6
	if config.RoutedCIDR != "" {
		routed, err := normalizeIPv6Network(config.RoutedCIDR)
		if err != nil {
			return Config{}, fmt.Errorf("路由IPv6网段无效: %w", err)
		}
		config.RoutedCIDR = routed
		if _, _, _, _, err := ipv6poolService.RoutedPrefixDetails(config.RoutedCIDR); err != nil {
			return Config{}, fmt.Errorf("路由IPv6网段不可分配: %w", err)
		}
	}
	if config.MTU == 0 {
		config.MTU = defaultMTU
	}
	if config.MTU < 1280 || config.MTU > 9000 {
		return Config{}, fmt.Errorf("MTU必须在1280到9000之间")
	}
	if config.TTL == 0 {
		config.TTL = defaultTTL
	}
	if config.TTL < 1 || config.TTL > 255 {
		return Config{}, fmt.Errorf("TTL必须在1到255之间")
	}
	if config.RouteMetric == 0 {
		config.RouteMetric = defaultRouteMetric
	}
	if config.RouteMetric < 1 || config.RouteMetric > 32766 {
		return Config{}, fmt.Errorf("路由优先级必须在1到32766之间")
	}
	return config, nil
}

func normalizeConfigLocalIPv4(config Config) (Config, error) {
	local4, err := normalizeIPv4(config.LocalIPv4)
	if err != nil {
		return Config{}, fmt.Errorf("本地IPv4无效: %w", err)
	}
	if local4 == config.RemoteIPv4 {
		return Config{}, fmt.Errorf("本地IPv4和远端IPv4不能相同")
	}
	config.LocalIPv4 = local4
	return config, nil
}

// normalizeTunnelMode accepts common 6in4 labels from tunnel providers while
// rejecting IPIP, whose IPv4-only payload cannot carry the routed IPv6 prefix.
func normalizeTunnelMode(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "sit", "6in4", "v4tunnel":
		return "sit", nil
	case "gre":
		return "gre", nil
	case "ipip":
		return "", fmt.Errorf("IPIP仅承载IPv4，不能用于IPv6隧道；请使用SIT/6in4或GRE")
	default:
		return "", fmt.Errorf("隧道模式仅支持SIT/6in4或GRE")
	}
}

func (c Config) toModel(providerID uint) providerModel.ProviderIPv6Tunnel {
	return providerModel.ProviderIPv6Tunnel{
		ProviderID: providerID, Name: c.Name, Mode: c.Mode, Interface: c.Interface,
		LocalIPv4: c.LocalIPv4, RemoteIPv4: c.RemoteIPv4,
		LocalIPv6: c.LocalIPv6, RemoteIPv6: c.RemoteIPv6, RoutedCIDR: c.RoutedCIDR,
		MTU: c.MTU, TTL: c.TTL, RouteMetric: c.RouteMetric, DefaultRoute: c.DefaultRoute,
	}
}

func normalizeIPv4(raw string) (string, error) {
	ip := net.ParseIP(raw)
	if ip == nil || ip.To4() == nil || ip.IsUnspecified() || ip.IsMulticast() {
		return "", fmt.Errorf("必须是单个可用IPv4地址")
	}
	return ip.To4().String(), nil
}

func normalizeIPv6Address(raw string) (string, error) {
	if strings.Contains(raw, "/") {
		return "", fmt.Errorf("不能包含前缀长度")
	}
	ip := net.ParseIP(raw)
	if ip == nil || ip.To4() != nil || ip.To16() == nil || ip.IsUnspecified() || ip.IsMulticast() {
		return "", fmt.Errorf("必须是单个可用IPv6地址")
	}
	return ip.String(), nil
}

func normalizeIPv6CIDR(raw string) (string, error) {
	ip, network, err := net.ParseCIDR(raw)
	if err != nil || ip == nil || network == nil || ip.To4() != nil || ip.To16() == nil {
		return "", fmt.Errorf("必须是IPv6地址/CIDR，例如2001:db8::2/64")
	}
	ones, bits := network.Mask.Size()
	if bits != 128 || ones < 1 || ones > 128 || ip.IsUnspecified() || ip.IsMulticast() {
		return "", fmt.Errorf("IPv6前缀长度必须在1到128之间")
	}
	return ip.String() + "/" + strconv.Itoa(ones), nil
}

func normalizeIPv6Network(raw string) (string, error) {
	ip, network, err := net.ParseCIDR(raw)
	if err != nil || ip == nil || network == nil || ip.To4() != nil || ip.To16() == nil {
		return "", fmt.Errorf("必须是IPv6 CIDR")
	}
	ones, bits := network.Mask.Size()
	if bits != 128 || ones < 1 || ones > 128 {
		return "", fmt.Errorf("IPv6前缀长度必须在1到128之间")
	}
	return network.String(), nil
}

func containsUnsafeText(value string) bool {
	for _, char := range value {
		if unicode.IsControl(char) || char == '\x1b' {
			return true
		}
	}
	return false
}

func ensureProviderExists(db *gorm.DB, providerID uint) error {
	var count int64
	if err := db.Model(&providerModel.Provider{}).Where("id = ?", providerID).Count(&count).Error; err != nil {
		return fmt.Errorf("检查Provider失败: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("Provider不存在")
	}
	return nil
}

func ensureInterfaceAvailable(db *gorm.DB, providerID uint, interfaceName string, excludeID uint) error {
	query := db.Model(&providerModel.ProviderIPv6Tunnel{}).
		Where("provider_id = ? AND interface = ?", providerID, interfaceName)
	if excludeID != 0 {
		query = query.Where("id <> ?", excludeID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return fmt.Errorf("检查隧道接口名失败: %w", err)
	}
	if count > 0 {
		return fmt.Errorf("接口名%s已被当前Provider的其他IPv6隧道使用", interfaceName)
	}
	return nil
}

func tunnelLookupError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("IPv6隧道不存在")
	}
	return fmt.Errorf("读取IPv6隧道失败: %w", err)
}

func parseCheckOutput(output string) map[uint]checkState {
	states := make(map[uint]checkState)
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		parts := strings.Split(strings.TrimSpace(line), "|")
		if (len(parts) != 9 && len(parts) != 11 && len(parts) != 12) || parts[0] != "TUNNEL" {
			continue
		}
		id, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil || id == 0 {
			continue
		}
		valid := true
		values := make([]bool, 10)
		for index, raw := range parts[2:] {
			if raw == "1" {
				values[index] = true
			} else if raw != "0" {
				valid = false
			}
		}
		if valid {
			state := checkState{UnitEnabled: values[0], UnitActive: values[1], LinkPresent: values[2], AddressOK: values[3], RouteOK: values[4], NetworkConfigOK: values[5], GatewayOK: values[6], RoutedOK: true, ForwardingOK: true, PolicyRouteOK: true}
			if len(parts) == 11 {
				state.RoutedOK, state.ForwardingOK = values[7], values[8]
			} else if len(parts) == 12 {
				state.RoutedOK, state.ForwardingOK, state.PolicyRouteOK = values[7], values[8], values[9]
			}
			states[uint(id)] = state
		}
	}
	return states
}

func classifyState(tunnel providerModel.ProviderIPv6Tunnel, state checkState, found bool) (string, string) {
	if !found {
		return providerModel.IPv6TunnelStatusError, "节点状态检查未返回该隧道"
	}
	if !tunnel.Enabled {
		if !state.UnitEnabled && !state.UnitActive && !state.LinkPresent {
			return providerModel.IPv6TunnelStatusInactive, ""
		}
		return providerModel.IPv6TunnelStatusError, "隧道期望禁用，但节点上仍有启用状态、活动unit或网络接口"
	}
	if _, err := normalizeTunnelMode(tunnel.Mode); err != nil {
		return providerModel.IPv6TunnelStatusError, limitError(err)
	}
	routedOK := strings.TrimSpace(tunnel.RoutedCIDR) == "" || (state.RoutedOK && state.ForwardingOK && state.PolicyRouteOK)
	if state.UnitEnabled && state.UnitActive && state.LinkPresent && state.AddressOK && state.RouteOK && state.NetworkConfigOK && state.GatewayOK && routedOK {
		return providerModel.IPv6TunnelStatusActive, ""
	}
	missing := make([]string, 0, 7)
	if !state.UnitEnabled {
		missing = append(missing, "unit未启用")
	}
	if !state.UnitActive {
		missing = append(missing, "unit未运行")
	}
	if !state.LinkPresent {
		missing = append(missing, "接口不存在")
	}
	if !state.AddressOK {
		missing = append(missing, "本地IPv6未配置")
	}
	if !state.RouteOK {
		missing = append(missing, "默认IPv6流量未选择该隧道")
	}
	if !state.NetworkConfigOK {
		missing = append(missing, "networkd持久配置缺失")
	}
	if !state.GatewayOK {
		missing = append(missing, "IPv6隧道网关不可达")
	}
	if strings.TrimSpace(tunnel.RoutedCIDR) != "" && !state.RoutedOK {
		missing = append(missing, "独立IPv6路由桥或路由缺失")
	}
	if strings.TrimSpace(tunnel.RoutedCIDR) != "" && !state.ForwardingOK {
		missing = append(missing, "IPv6 forwarding未完整开启（需要all、default、隧道接口和路由桥）")
	}
	if strings.TrimSpace(tunnel.RoutedCIDR) != "" && !state.PolicyRouteOK {
		missing = append(missing, "独立IPv6源地址策略路由缺失或未选择隧道接口")
	}
	return providerModel.IPv6TunnelStatusError, strings.Join(missing, "；")
}

func limitError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	if len(message) > maxRemoteError {
		message = message[:maxRemoteError]
	}
	return message
}
