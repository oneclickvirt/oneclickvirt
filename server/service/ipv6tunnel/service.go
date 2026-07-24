package ipv6tunnel

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
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
	UnitEnabled bool
	UnitActive  bool
	LinkPresent bool
	AddressOK   bool
	RouteOK     bool
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
	config, err := normalizeConfig(request.Config)
	if err != nil {
		return nil, err
	}
	if err := ensureProviderExists(db, providerID); err != nil {
		return nil, err
	}
	if err := ensureInterfaceAvailable(db, providerID, config.Interface, 0); err != nil {
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
	config, err := normalizeConfig(request)
	if err != nil {
		return nil, err
	}
	if err := ensureInterfaceAvailable(db, providerID, config.Interface, tunnelID); err != nil {
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
		s.recordProviderError(providerID, err)
		return nil, fmt.Errorf("检查IPv6隧道状态失败: %w", err)
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
	var tunnel providerModel.ProviderIPv6Tunnel
	if err := db.Where("id = ? AND provider_id = ?", tunnelID, providerID).First(&tunnel).Error; err != nil {
		return tunnelLookupError(err)
	}
	if err := s.deleteRemote(ctx, []providerModel.ProviderIPv6Tunnel{tunnel}); err != nil {
		_ = s.recordState(tunnel.ID, providerModel.IPv6TunnelStatusError, err)
		return err
	}
	if err := db.Delete(&providerModel.ProviderIPv6Tunnel{}, tunnel.ID).Error; err != nil {
		return fmt.Errorf("节点隧道已删除但数据库记录清理失败: %w", err)
	}
	return nil
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
	_, err := s.run(ctx, tunnel.ProviderID, buildApplyCommand(tunnel))
	if err != nil {
		return fmt.Errorf("应用IPv6隧道失败: %w", err)
	}
	return nil
}

func (s *Service) disableRemote(ctx context.Context, tunnel providerModel.ProviderIPv6Tunnel) error {
	_, err := s.run(ctx, tunnel.ProviderID, buildDisableCommand(tunnel))
	if err != nil {
		return fmt.Errorf("禁用IPv6隧道失败: %w", err)
	}
	return nil
}

func (s *Service) deleteRemote(ctx context.Context, tunnels []providerModel.ProviderIPv6Tunnel) error {
	if len(tunnels) == 0 {
		return nil
	}
	_, err := s.run(ctx, tunnels[0].ProviderID, buildDeleteCommand(tunnels))
	if err != nil {
		return fmt.Errorf("删除节点IPv6隧道失败: %w", err)
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
	if config.Mode != "sit" && config.Mode != "gre" {
		return Config{}, fmt.Errorf("隧道模式仅支持sit或gre")
	}
	if !interfacePattern.MatchString(config.Interface) || config.Interface == "lo" {
		return Config{}, fmt.Errorf("接口名必须是1至15位Linux接口名且不能为lo")
	}
	local4, err := normalizeIPv4(config.LocalIPv4)
	if err != nil {
		return Config{}, fmt.Errorf("本地IPv4无效: %w", err)
	}
	remote4, err := normalizeIPv4(config.RemoteIPv4)
	if err != nil {
		return Config{}, fmt.Errorf("远端IPv4无效: %w", err)
	}
	if local4 == remote4 {
		return Config{}, fmt.Errorf("本地IPv4和远端IPv4不能相同")
	}
	config.LocalIPv4, config.RemoteIPv4 = local4, remote4

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

func unitName(id uint) string {
	return fmt.Sprintf("oneclickvirt-ipv6-tunnel-%d.service", id)
}

func scriptPath(id uint) string {
	return fmt.Sprintf("/etc/oneclickvirt/ipv6-tunnels/%d.sh", id)
}

func buildApplyCommand(tunnel providerModel.ProviderIPv6Tunnel) string {
	unit := unitName(tunnel.ID)
	script := scriptPath(tunnel.ID)
	unitPath := "/etc/systemd/system/" + unit
	unitContent := fmt.Sprintf(`[Unit]
Description=OneClickVirt managed IPv6 tunnel %d
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=%s up
ExecStop=%s down
TimeoutStartSec=45
TimeoutStopSec=30

[Install]
WantedBy=multi-user.target
`, tunnel.ID, script, script)
	scriptContent := renderTunnelScript(tunnel)
	unit64 := base64.StdEncoding.EncodeToString([]byte(unitContent))
	script64 := base64.StdEncoding.EncodeToString([]byte(scriptContent))

	return fmt.Sprintf(`set -eu
if [ "$(id -u)" -ne 0 ]; then echo 'root privileges are required' >&2; exit 1; fi
if ! command -v systemctl >/dev/null 2>&1; then echo 'systemd is required for persistent IPv6 tunnels' >&2; exit 1; fi
install_pkg() {
  package="$1"
  if command -v apt-get >/dev/null 2>&1; then DEBIAN_FRONTEND=noninteractive apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y "$package"
  elif command -v dnf >/dev/null 2>&1; then dnf install -y "$package"
  elif command -v yum >/dev/null 2>&1; then yum install -y "$package"
  elif command -v apk >/dev/null 2>&1; then apk add --no-cache "$package"
  else echo "cannot install required package: $package" >&2; return 1; fi
}
install_iproute() {
  if command -v dnf >/dev/null 2>&1; then dnf install -y iproute
  elif command -v yum >/dev/null 2>&1; then yum install -y iproute
  else install_pkg iproute2; fi
}
command -v ip >/dev/null 2>&1 || install_iproute
command -v base64 >/dev/null 2>&1 || install_pkg coreutils
mkdir -p /etc/oneclickvirt/ipv6-tunnels
unit=%s
unit_path=%s
script_path=%s
if ip link show dev %s >/dev/null 2>&1 && [ ! -f "$unit_path" ] && [ ! -f "$script_path" ]; then
  echo 'refusing to replace an unmanaged network interface' >&2
  exit 1
fi
unit_backup="${unit_path}.oneclickvirt-backup.$$"
script_backup="${script_path}.oneclickvirt-backup.$$"
had_unit=0; had_script=0; was_active=0
[ -f "$unit_path" ] && { cp -p "$unit_path" "$unit_backup"; had_unit=1; }
[ -f "$script_path" ] && { cp -p "$script_path" "$script_backup"; had_script=1; }
systemctl is-active --quiet "$unit" && was_active=1 || true
rollback() {
  rc=$?
  trap - EXIT
  systemctl stop "$unit" >/dev/null 2>&1 || true
  if [ "$had_unit" -eq 1 ]; then mv -f "$unit_backup" "$unit_path"; else rm -f "$unit_path" "$unit_backup"; fi
  if [ "$had_script" -eq 1 ]; then mv -f "$script_backup" "$script_path"; else rm -f "$script_path" "$script_backup"; fi
  systemctl daemon-reload >/dev/null 2>&1 || true
  if [ "$was_active" -eq 1 ]; then systemctl enable --now "$unit" >/dev/null 2>&1 || true; else systemctl disable "$unit" >/dev/null 2>&1 || true; fi
  exit "$rc"
}
trap rollback EXIT
systemctl stop "$unit" >/dev/null 2>&1 || true
printf '%%s' %s | base64 -d > "${unit_path}.tmp.$$"
printf '%%s' %s | base64 -d > "${script_path}.tmp.$$"
chmod 0644 "${unit_path}.tmp.$$"
chmod 0700 "${script_path}.tmp.$$"
mv -f "${unit_path}.tmp.$$" "$unit_path"
mv -f "${script_path}.tmp.$$" "$script_path"
systemctl daemon-reload
systemctl enable "$unit" >/dev/null
systemctl start "$unit"
"$script_path" status
rm -f "$unit_backup" "$script_backup"
trap - EXIT
printf 'applied\n'
`, utils.ShellSingleQuote(unit), utils.ShellSingleQuote(unitPath), utils.ShellSingleQuote(script), utils.ShellSingleQuote(tunnel.Interface),
		utils.ShellSingleQuote(unit64), utils.ShellSingleQuote(script64))
}

func renderTunnelScript(tunnel providerModel.ProviderIPv6Tunnel) string {
	module := "sit"
	if tunnel.Mode == "gre" {
		module = "ip_gre"
	}
	defaultUp := ""
	defaultDown := ""
	routeStatus := ":"
	if tunnel.DefaultRoute {
		defaultUp = fmt.Sprintf("ip -6 route replace default via %s dev \"$IFACE\" metric %d\n", utils.ShellSingleQuote(tunnel.RemoteIPv6), tunnel.RouteMetric)
		defaultDown = fmt.Sprintf("ip -6 route del default via %s dev \"$IFACE\" metric %d >/dev/null 2>&1 || true\n", utils.ShellSingleQuote(tunnel.RemoteIPv6), tunnel.RouteMetric)
		routeStatus = "route_line=\" $(ip -6 route get 2001:4860:4860::8888 2>/dev/null) \"\nprintf '%s\\n' \"$route_line\" | grep -F \" dev $IFACE \" >/dev/null"
	}
	return fmt.Sprintf(`#!/bin/sh
set -eu
IFACE=%s
case "${1:-}" in
  up)
    command -v modprobe >/dev/null 2>&1 && modprobe %s >/dev/null 2>&1 || true
    ip link show dev "$IFACE" >/dev/null 2>&1 && ip link delete "$IFACE" || true
    ip tunnel add "$IFACE" mode %s remote %s local %s ttl %d
    ip link set dev "$IFACE" mtu %d up
    ip -6 addr replace %s dev "$IFACE"
    ip -6 route replace %s/128 dev "$IFACE" metric %d
%s    ;;
  down)
%s    ip link show dev "$IFACE" >/dev/null 2>&1 && ip link delete "$IFACE" || true
    ;;
  status)
    ip link show dev "$IFACE" >/dev/null
    ip -o -6 addr show dev "$IFACE" | awk '{print $4}' | grep -Fx %s >/dev/null
    %s
    ;;
  *)
    echo 'usage: tunnel-script {up|down|status}' >&2
    exit 2
    ;;
esac
`, utils.ShellSingleQuote(tunnel.Interface), module, tunnel.Mode,
		utils.ShellSingleQuote(tunnel.RemoteIPv4), utils.ShellSingleQuote(tunnel.LocalIPv4), tunnel.TTL,
		tunnel.MTU, utils.ShellSingleQuote(tunnel.LocalIPv6), utils.ShellSingleQuote(tunnel.RemoteIPv6), tunnel.RouteMetric,
		defaultUp, defaultDown, utils.ShellSingleQuote(tunnel.LocalIPv6), routeStatus)
}

func buildDisableCommand(tunnel providerModel.ProviderIPv6Tunnel) string {
	unit := unitName(tunnel.ID)
	return fmt.Sprintf(`set -eu
if [ "$(id -u)" -ne 0 ]; then echo 'root privileges are required' >&2; exit 1; fi
command -v systemctl >/dev/null 2>&1 || { echo 'systemd is unavailable' >&2; exit 1; }
command -v ip >/dev/null 2>&1 || { echo 'iproute2 is unavailable' >&2; exit 1; }
unit=%s
unit_path=%s
script_path=%s
if ip link show dev %s >/dev/null 2>&1 && [ ! -f "$unit_path" ] && [ ! -f "$script_path" ] && ! systemctl cat "$unit" >/dev/null 2>&1; then
  echo 'refusing to delete an unmanaged network interface' >&2
  exit 1
fi
systemctl disable --now "$unit" >/dev/null 2>&1 || true
ip link show dev %s >/dev/null 2>&1 && ip link delete %s || true
if systemctl is-active --quiet "$unit" || ip link show dev %s >/dev/null 2>&1; then
  echo 'tunnel remained active after disable' >&2
  exit 1
fi
printf 'disabled\n'
`, utils.ShellSingleQuote(unit), utils.ShellSingleQuote("/etc/systemd/system/"+unit), utils.ShellSingleQuote(scriptPath(tunnel.ID)),
		utils.ShellSingleQuote(tunnel.Interface), utils.ShellSingleQuote(tunnel.Interface), utils.ShellSingleQuote(tunnel.Interface), utils.ShellSingleQuote(tunnel.Interface))
}

func buildDeleteCommand(tunnels []providerModel.ProviderIPv6Tunnel) string {
	ordered := append([]providerModel.ProviderIPv6Tunnel(nil), tunnels...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	var builder strings.Builder
	builder.WriteString("set -eu\nif [ \"$(id -u)\" -ne 0 ]; then echo 'root privileges are required' >&2; exit 1; fi\ncommand -v systemctl >/dev/null 2>&1 || { echo 'systemd is unavailable' >&2; exit 1; }\ncommand -v ip >/dev/null 2>&1 || { echo 'iproute2 is unavailable' >&2; exit 1; }\n")
	// Validate ownership for every interface before deleting any of them. This
	// prevents a stale DB row from deleting a physical interface that later
	// reused the same name.
	for _, tunnel := range ordered {
		fmt.Fprintf(&builder, "if ip link show dev %s >/dev/null 2>&1 && [ ! -f %s ] && [ ! -f %s ] && ! systemctl cat %s >/dev/null 2>&1; then echo %s >&2; exit 1; fi\n",
			utils.ShellSingleQuote(tunnel.Interface), utils.ShellSingleQuote("/etc/systemd/system/"+unitName(tunnel.ID)),
			utils.ShellSingleQuote(scriptPath(tunnel.ID)), utils.ShellSingleQuote(unitName(tunnel.ID)),
			utils.ShellSingleQuote(fmt.Sprintf("refusing to delete unmanaged interface %s", tunnel.Interface)))
	}
	for _, tunnel := range ordered {
		fmt.Fprintf(&builder, "systemctl disable --now %s >/dev/null 2>&1 || true\n", utils.ShellSingleQuote(unitName(tunnel.ID)))
		fmt.Fprintf(&builder, "ip link show dev %s >/dev/null 2>&1 && ip link delete %s || true\n", utils.ShellSingleQuote(tunnel.Interface), utils.ShellSingleQuote(tunnel.Interface))
		fmt.Fprintf(&builder, "rm -f -- %s %s\n", utils.ShellSingleQuote("/etc/systemd/system/"+unitName(tunnel.ID)), utils.ShellSingleQuote(scriptPath(tunnel.ID)))
	}
	builder.WriteString("systemctl daemon-reload\n")
	for _, tunnel := range ordered {
		fmt.Fprintf(&builder, "if systemctl is-active --quiet %s || ip link show dev %s >/dev/null 2>&1 || [ -e %s ] || [ -e %s ]; then echo %s >&2; exit 1; fi\n",
			utils.ShellSingleQuote(unitName(tunnel.ID)), utils.ShellSingleQuote(tunnel.Interface),
			utils.ShellSingleQuote("/etc/systemd/system/"+unitName(tunnel.ID)), utils.ShellSingleQuote(scriptPath(tunnel.ID)),
			utils.ShellSingleQuote(fmt.Sprintf("IPv6 tunnel %d cleanup is incomplete", tunnel.ID)))
	}
	builder.WriteString("printf 'deleted\\n'\n")
	return builder.String()
}

func buildCheckCommand(tunnels []providerModel.ProviderIPv6Tunnel) string {
	ordered := append([]providerModel.ProviderIPv6Tunnel(nil), tunnels...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	var builder strings.Builder
	builder.WriteString("set -u\ncommand -v systemctl >/dev/null 2>&1 || { echo 'systemd is unavailable' >&2; exit 1; }\ncommand -v ip >/dev/null 2>&1 || { echo 'iproute2 is unavailable' >&2; exit 1; }\n")
	for _, tunnel := range ordered {
		unit := utils.ShellSingleQuote(unitName(tunnel.ID))
		iface := utils.ShellSingleQuote(tunnel.Interface)
		address := utils.ShellSingleQuote(tunnel.LocalIPv6)
		fmt.Fprintf(&builder, "enabled=0; active=0; link=0; address=0; route=1\n")
		fmt.Fprintf(&builder, "systemctl is-enabled --quiet %s >/dev/null 2>&1 && enabled=1 || true\n", unit)
		fmt.Fprintf(&builder, "systemctl is-active --quiet %s >/dev/null 2>&1 && active=1 || true\n", unit)
		fmt.Fprintf(&builder, "ip link show dev %s >/dev/null 2>&1 && link=1 || true\n", iface)
		fmt.Fprintf(&builder, "if [ \"$link\" -eq 1 ]; then ip -o -6 addr show dev %s | awk '{print $4}' | grep -Fx %s >/dev/null 2>&1 && address=1 || true; fi\n", iface, address)
		if tunnel.DefaultRoute {
			fmt.Fprintf(&builder, "route=0; route_line=\" $(ip -6 route get 2001:4860:4860::8888 2>/dev/null || true) \"; printf '%%s\\n' \"$route_line\" | grep -F %s >/dev/null 2>&1 && route=1 || true\n", utils.ShellSingleQuote(" dev "+tunnel.Interface+" "))
		}
		fmt.Fprintf(&builder, "printf 'TUNNEL|%d|%%s|%%s|%%s|%%s|%%s\\n' \"$enabled\" \"$active\" \"$link\" \"$address\" \"$route\"\n", tunnel.ID)
	}
	return builder.String()
}

func parseCheckOutput(output string) map[uint]checkState {
	states := make(map[uint]checkState)
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		parts := strings.Split(strings.TrimSpace(line), "|")
		if len(parts) != 7 || parts[0] != "TUNNEL" {
			continue
		}
		id, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil || id == 0 {
			continue
		}
		valid := true
		values := make([]bool, 5)
		for index, raw := range parts[2:] {
			if raw == "1" {
				values[index] = true
			} else if raw != "0" {
				valid = false
			}
		}
		if valid {
			states[uint(id)] = checkState{UnitEnabled: values[0], UnitActive: values[1], LinkPresent: values[2], AddressOK: values[3], RouteOK: values[4]}
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
	if state.UnitEnabled && state.UnitActive && state.LinkPresent && state.AddressOK && state.RouteOK {
		return providerModel.IPv6TunnelStatusActive, ""
	}
	missing := make([]string, 0, 5)
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
