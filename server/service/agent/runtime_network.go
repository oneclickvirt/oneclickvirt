package agent

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	providerCore "oneclickvirt/provider"
	providerService "oneclickvirt/service/provider"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// DiscoveredInstanceNetwork is the deliberately small, credential-free part
// of a provider discovery result that may be persisted after a node returns.
// It is kept separate from provider.DiscoveredInstance so discovery code never
// has to pass RawData or credentials into the runtime repair path.
type DiscoveredInstanceNetwork struct {
	InstanceID  uint
	Status      string
	PrivateIP   string
	PublicIP    string
	IPv6Address string
	SSHPort     int
}

// RuntimeNetworkSyncResult describes actual database changes. Callers use
// PrivateIPChangedInstanceIDs for controller-side forwarding, and use
// NetworkChangedInstanceIDs for the coalesced Provider egress replay.
type RuntimeNetworkSyncResult struct {
	ChangedInstanceIDs          []uint
	PrivateIPChangedInstanceIDs []uint
	// NetworkChangedInstanceIDs includes private, public, and IPv6 changes.
	// Public-address changes do not require a controller tunnel rebind, but do
	// require one provider-wide egress replay for dedicated-address profiles.
	NetworkChangedInstanceIDs []uint
}

type runtimeNetworkRow struct {
	ID                uint
	PrivateIP         string
	PublicIP          string
	IPv6Address       string
	SSHHost           string
	SSHPort           int
	PmacctInterfaceV4 string
	PmacctInterfaceV6 string
	NetworkType       string
}

type runtimeNetworkChange struct {
	ID                uint
	PrivateIP         string
	PublicIP          string
	IPv6Address       string
	SSHPort           int
	PmacctInterfaceV4 string
	PmacctInterfaceV6 string
	PrivateIPChanged  bool
	NetworkChanged    bool
}

// Keep reads and CASE updates below common SQLite/MySQL bind budgets. A large
// post-reboot reconciliation is still provider-batched, never per-instance.
const (
	runtimeNetworkReadBatchSize   = 200
	runtimeNetworkUpdateBatchSize = 50
)

// SynchronizeDiscoveredInstanceNetworks applies authoritative, valid non-empty
// addresses returned by one provider discovery.  It performs one batched
// instance read, plus one batched SSH-mapping lookup only when SSH data is
// present, and at most one batched UPDATE; it never contacts a provider or
// holds a database transaction around remote work.
func SynchronizeDiscoveredInstanceNetworks(
	ctx context.Context,
	db *gorm.DB,
	providerID uint,
	snapshots []DiscoveredInstanceNetwork,
) (RuntimeNetworkSyncResult, error) {
	result := RuntimeNetworkSyncResult{
		ChangedInstanceIDs:          []uint{},
		PrivateIPChangedInstanceIDs: []uint{},
		NetworkChangedInstanceIDs:   []uint{},
	}
	if db == nil || providerID == 0 || len(snapshots) == 0 {
		return result, nil
	}

	// Only a known-running guest has a meaningful post-reboot address.  Fold
	// duplicate discovery rows by controller ID before querying the database.
	byInstanceID := make(map[uint]DiscoveredInstanceNetwork, len(snapshots))
	for _, snapshot := range snapshots {
		if snapshot.InstanceID == 0 || !runtimeNetworkStatusRunning(snapshot.Status) {
			continue
		}
		snapshot.PrivateIP = normalizeRuntimeIPv4(snapshot.PrivateIP)
		snapshot.PublicIP = normalizeRuntimeIPv4(snapshot.PublicIP)
		snapshot.IPv6Address = normalizeRuntimeIPv6(snapshot.IPv6Address)
		if snapshot.SSHPort <= 0 || snapshot.SSHPort > 65535 {
			snapshot.SSHPort = 0
		}
		if snapshot.PrivateIP == "" && snapshot.PublicIP == "" && snapshot.IPv6Address == "" && snapshot.SSHPort == 0 {
			continue
		}
		byInstanceID[snapshot.InstanceID] = snapshot
	}
	if len(byInstanceID) == 0 {
		return result, nil
	}

	ids := make([]uint, 0, len(byInstanceID))
	for id := range byInstanceID {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(ids) > runtimeNetworkReadBatchSize {
		for start := 0; start < len(ids); start += runtimeNetworkReadBatchSize {
			end := start + runtimeNetworkReadBatchSize
			if end > len(ids) {
				end = len(ids)
			}
			batchSnapshots := make([]DiscoveredInstanceNetwork, 0, end-start)
			for _, id := range ids[start:end] {
				batchSnapshots = append(batchSnapshots, byInstanceID[id])
			}
			batchResult, err := SynchronizeDiscoveredInstanceNetworks(ctx, db, providerID, batchSnapshots)
			result.ChangedInstanceIDs = append(result.ChangedInstanceIDs, batchResult.ChangedInstanceIDs...)
			result.PrivateIPChangedInstanceIDs = append(result.PrivateIPChangedInstanceIDs, batchResult.PrivateIPChangedInstanceIDs...)
			result.NetworkChangedInstanceIDs = append(result.NetworkChangedInstanceIDs, batchResult.NetworkChangedInstanceIDs...)
			if err != nil {
				return result, err
			}
		}
		return result, nil
	}

	var rows []runtimeNetworkRow
	if err := db.WithContext(ctx).Model(&providerModel.Instance{}).
		Select("id", "private_ip", "public_ip", "ipv6_address", "ssh_host", "ssh_port", "pmacct_interface_v4", "pmacct_interface_v6", "network_type").
		Where("provider_id = ? AND id IN ?", providerID, ids).
		Find(&rows).Error; err != nil {
		return result, fmt.Errorf("读取实例运行时网络信息失败: %w", err)
	}

	// A port mapping (controller tunnel or node-side NAT) is the explicit
	// access contract.  Never replace its SSH port with a value discovered from
	// a guest runtime.  This is one provider-batched lookup instead of looking
	// up a port for every discovered instance.
	hasDiscoveredSSHPort := false
	for _, snapshot := range byInstanceID {
		if snapshot.SSHPort > 0 {
			hasDiscoveredSSHPort = true
			break
		}
	}
	protectedSSHInstanceIDs := make(map[uint]struct{})
	if hasDiscoveredSSHPort {
		var mappedPorts []providerModel.Port
		if err := db.WithContext(ctx).Model(&providerModel.Port{}).
			Select("instance_id").
			Where("provider_id = ? AND instance_id IN ? AND status = ? AND (is_ssh = ? OR guest_port = ?)", providerID, ids, "active", true, 22).
			Find(&mappedPorts).Error; err != nil {
			return result, fmt.Errorf("读取实例SSH映射保护信息失败: %w", err)
		}
		for _, port := range mappedPorts {
			protectedSSHInstanceIDs[port.InstanceID] = struct{}{}
		}
	}

	changes := make([]runtimeNetworkChange, 0, len(rows))
	for _, row := range rows {
		snapshot, ok := byInstanceID[row.ID]
		if !ok {
			continue
		}
		change := runtimeNetworkChange{
			ID:                row.ID,
			PrivateIP:         row.PrivateIP,
			PublicIP:          row.PublicIP,
			IPv6Address:       row.IPv6Address,
			SSHPort:           row.SSHPort,
			PmacctInterfaceV4: row.PmacctInterfaceV4,
			PmacctInterfaceV6: row.PmacctInterfaceV6,
		}
		if snapshot.PrivateIP != "" && snapshot.PrivateIP != row.PrivateIP {
			change.PrivateIP = snapshot.PrivateIP
			change.PrivateIPChanged = true
			change.NetworkChanged = true
		}
		if snapshot.PublicIP != "" && snapshot.PublicIP != row.PublicIP {
			change.PublicIP = snapshot.PublicIP
			change.NetworkChanged = true
		}
		if snapshot.IPv6Address != "" && snapshot.IPv6Address != row.IPv6Address {
			change.IPv6Address = snapshot.IPv6Address
			change.NetworkChanged = true
			// An address change usually accompanies a recreated network device.
			change.PrivateIPChanged = true
		}
		// The ports table and an explicit SSHHost remain authoritative for mapped
		// access. A runtime discovery may only fill an unset/default port when no
		// such explicit controller/host contract exists.
		_, sshPortProtected := protectedSSHInstanceIDs[row.ID]
		if snapshot.SSHPort > 0 && !sshPortProtected && strings.TrimSpace(row.SSHHost) == "" &&
			(row.SSHPort == 0 || row.SSHPort == 22) && snapshot.SSHPort != row.SSHPort {
			change.SSHPort = snapshot.SSHPort
		}
		if change.PrivateIPChanged {
			// Force the existing bounded interface reconciler to re-detect a veth
			// after a guest/network recreation rather than silently counting the
			// old host interface forever.
			change.PmacctInterfaceV4 = ""
			change.PmacctInterfaceV6 = ""
		}
		if change.PrivateIP != row.PrivateIP || change.PublicIP != row.PublicIP ||
			change.IPv6Address != row.IPv6Address || change.SSHPort != row.SSHPort ||
			change.PmacctInterfaceV4 != row.PmacctInterfaceV4 || change.PmacctInterfaceV6 != row.PmacctInterfaceV6 {
			changes = append(changes, change)
		}
	}
	if len(changes) == 0 {
		return result, nil
	}

	for start := 0; start < len(changes); start += runtimeNetworkUpdateBatchSize {
		end := start + runtimeNetworkUpdateBatchSize
		if end > len(changes) {
			end = len(changes)
		}
		batch := changes[start:end]
		changedIDs := make([]uint, 0, len(batch))
		privateChangedIDs := make([]uint, 0, len(batch))
		networkChangedIDs := make([]uint, 0, len(batch))
		for _, change := range batch {
			changedIDs = append(changedIDs, change.ID)
			if change.PrivateIPChanged {
				privateChangedIDs = append(privateChangedIDs, change.ID)
			}
			if change.NetworkChanged {
				networkChangedIDs = append(networkChangedIDs, change.ID)
			}
		}

		updates := map[string]interface{}{
			"private_ip":          runtimeNetworkCase("private_ip", batch, func(change runtimeNetworkChange) interface{} { return change.PrivateIP }),
			"public_ip":           runtimeNetworkCase("public_ip", batch, func(change runtimeNetworkChange) interface{} { return change.PublicIP }),
			"ipv6_address":        runtimeNetworkCase("ipv6_address", batch, func(change runtimeNetworkChange) interface{} { return change.IPv6Address }),
			"ssh_port":            runtimeNetworkCase("ssh_port", batch, func(change runtimeNetworkChange) interface{} { return change.SSHPort }),
			"pmacct_interface_v4": runtimeNetworkCase("pmacct_interface_v4", batch, func(change runtimeNetworkChange) interface{} { return change.PmacctInterfaceV4 }),
			"pmacct_interface_v6": runtimeNetworkCase("pmacct_interface_v6", batch, func(change runtimeNetworkChange) interface{} { return change.PmacctInterfaceV6 }),
		}
		if err := db.WithContext(ctx).Model(&providerModel.Instance{}).
			Where("provider_id = ? AND id IN ?", providerID, changedIDs).
			Updates(updates).Error; err != nil {
			return result, fmt.Errorf("批量更新实例运行时网络信息失败: %w", err)
		}
		result.ChangedInstanceIDs = append(result.ChangedInstanceIDs, changedIDs...)
		result.PrivateIPChangedInstanceIDs = append(result.PrivateIPChangedInstanceIDs, privateChangedIDs...)
		result.NetworkChangedInstanceIDs = append(result.NetworkChangedInstanceIDs, networkChangedIDs...)
	}

	return result, nil
}

func runtimeNetworkStatusRunning(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "active", "up", "started":
		return true
	default:
		return false
	}
}

func runtimeNetworkCase(column string, changes []runtimeNetworkChange, value func(runtimeNetworkChange) interface{}) interface{} {
	var builder strings.Builder
	builder.WriteString("CASE id")
	args := make([]interface{}, 0, len(changes)*2)
	for _, change := range changes {
		builder.WriteString(" WHEN ? THEN ?")
		args = append(args, change.ID, value(change))
	}
	builder.WriteString(" ELSE ")
	builder.WriteString(column)
	builder.WriteString(" END")
	return gorm.Expr(builder.String(), args...)
}

func normalizeRuntimeIPv4(value string) string {
	addr, err := netip.ParseAddr(strings.Trim(strings.TrimSpace(value), "[]"))
	if err != nil || !addr.Is4() || addr.IsUnspecified() {
		return ""
	}
	return addr.String()
}

func normalizeRuntimeIPv6(value string) string {
	addr, err := netip.ParseAddr(strings.Trim(strings.TrimSpace(value), "[]"))
	if err != nil || !addr.Is6() || addr.IsUnspecified() {
		return ""
	}
	return addr.String()
}

var providerRuntimeRepair = struct {
	sync.Mutex
	pendingController map[uint]map[uint]struct{}
	pendingEgress     map[uint]bool
	running           map[uint]bool
}{
	pendingController: make(map[uint]map[uint]struct{}),
	pendingEgress:     make(map[uint]bool),
	running:           make(map[uint]bool),
}

// ScheduleProviderRuntimeNetworkRepair coalesces address-change work per
// provider. One provider scan can update many instances, but controller
// listener repair and Agent egress replay each happen once per coalesced
// provider batch. All remote work happens after the database write commits.
func ScheduleProviderRuntimeNetworkRepair(db *gorm.DB, providerID uint, controllerInstanceIDs, egressInstanceIDs []uint) {
	if db == nil || providerID == 0 || (len(controllerInstanceIDs) == 0 && len(egressInstanceIDs) == 0) {
		return
	}
	providerRuntimeRepair.Lock()
	pendingController := providerRuntimeRepair.pendingController[providerID]
	if pendingController == nil {
		pendingController = make(map[uint]struct{}, len(controllerInstanceIDs))
		providerRuntimeRepair.pendingController[providerID] = pendingController
	}
	for _, instanceID := range controllerInstanceIDs {
		if instanceID != 0 {
			pendingController[instanceID] = struct{}{}
		}
	}
	if len(egressInstanceIDs) > 0 {
		providerRuntimeRepair.pendingEgress[providerID] = true
	}
	if providerRuntimeRepair.running[providerID] {
		providerRuntimeRepair.Unlock()
		return
	}
	providerRuntimeRepair.running[providerID] = true
	providerRuntimeRepair.Unlock()

	go func() {
		for {
			if agentShutdownContext().Err() != nil {
				providerRuntimeRepair.Lock()
				delete(providerRuntimeRepair.pendingController, providerID)
				delete(providerRuntimeRepair.pendingEgress, providerID)
				delete(providerRuntimeRepair.running, providerID)
				providerRuntimeRepair.Unlock()
				return
			}
			providerRuntimeRepair.Lock()
			pendingController := providerRuntimeRepair.pendingController[providerID]
			delete(providerRuntimeRepair.pendingController, providerID)
			controllerIDs := make([]uint, 0, len(pendingController))
			for instanceID := range pendingController {
				controllerIDs = append(controllerIDs, instanceID)
			}
			needsEgress := providerRuntimeRepair.pendingEgress[providerID]
			delete(providerRuntimeRepair.pendingEgress, providerID)
			providerRuntimeRepair.Unlock()

			if len(controllerIDs) > 0 {
				sort.Slice(controllerIDs, func(i, j int) bool { return controllerIDs[i] < controllerIDs[j] })
				for start := 0; start < len(controllerIDs); start += controllerPortHostInstanceBatchSize {
					end := start + controllerPortHostInstanceBatchSize
					if end > len(controllerIDs) {
						end = len(controllerIDs)
					}
					refreshControllerPortHostsForInstances(db, controllerIDs[start:end])
				}
			}
			if needsEgress && agentShutdownContext().Err() == nil {
				ScheduleProviderEgressRefresh(db, providerID, true)
			}

			providerRuntimeRepair.Lock()
			if len(providerRuntimeRepair.pendingController[providerID]) == 0 && !providerRuntimeRepair.pendingEgress[providerID] {
				delete(providerRuntimeRepair.running, providerID)
				providerRuntimeRepair.Unlock()
				return
			}
			providerRuntimeRepair.Unlock()
		}
	}()
}

const (
	providerEgressRefreshDebounce = 2 * time.Second
	providerEgressRefreshCooldown = 15 * time.Second
)

var providerEgressRefresh = struct {
	sync.Mutex
	pending map[uint]bool
	running map[uint]bool
	lastRun map[uint]time.Time
}{
	pending: make(map[uint]bool),
	running: make(map[uint]bool),
	lastRun: make(map[uint]time.Time),
}

// ScheduleProviderEgressRefresh replaces per-instance lifecycle replays with
// a debounced, provider-wide state replacement. The Agent receives at most one
// replay for a burst of starts/IP changes, while a later event is held until a
// small cooldown has passed instead of producing a tight remote-call loop.
func ScheduleProviderEgressRefresh(db *gorm.DB, providerID uint, apply bool) {
	if db == nil || providerID == 0 {
		return
	}
	providerEgressRefresh.Lock()
	previous, exists := providerEgressRefresh.pending[providerID]
	providerEgressRefresh.pending[providerID] = (exists && previous) || apply
	if providerEgressRefresh.running[providerID] {
		providerEgressRefresh.Unlock()
		return
	}
	providerEgressRefresh.running[providerID] = true
	providerEgressRefresh.Unlock()

	go func() {
		for {
			providerEgressRefresh.Lock()
			lastRun := providerEgressRefresh.lastRun[providerID]
			providerEgressRefresh.Unlock()

			delay := providerEgressRefreshDebounce
			if !lastRun.IsZero() {
				if remaining := providerEgressRefreshCooldown - time.Since(lastRun); remaining > delay {
					delay = remaining
				}
			}
			if !waitForAgentShutdown(delay) {
				providerEgressRefresh.Lock()
				delete(providerEgressRefresh.pending, providerID)
				delete(providerEgressRefresh.running, providerID)
				providerEgressRefresh.Unlock()
				return
			}

			providerEgressRefresh.Lock()
			apply, pending := providerEgressRefresh.pending[providerID]
			delete(providerEgressRefresh.pending, providerID)
			providerEgressRefresh.Unlock()
			if pending {
				ctx, cancel := context.WithTimeout(agentShutdownContext(), 2*time.Minute)
				restored, err := NewInstanceEgressService(db).RestoreProviderEgress(ctx, providerID, apply)
				cancel()
				if err != nil {
					if global.APP_LOG != nil {
						global.APP_LOG.Debug("批量恢复Provider出口状态失败",
							zap.Uint("provider_id", providerID), zap.Error(err))
					}
				} else if restored > 0 && global.APP_LOG != nil {
					global.APP_LOG.Info("批量恢复Provider出口状态完成",
						zap.Uint("provider_id", providerID), zap.Int("restored", restored))
				}
			}

			providerEgressRefresh.Lock()
			providerEgressRefresh.lastRun[providerID] = time.Now()
			if _, hasPending := providerEgressRefresh.pending[providerID]; !hasPending {
				delete(providerEgressRefresh.running, providerID)
				providerEgressRefresh.Unlock()
				return
			}
			providerEgressRefresh.Unlock()
		}
	}()
}

const (
	providerStartedNetworkRefreshDebounce = 2 * time.Second
	providerStartedNetworkRefreshCooldown = 20 * time.Second
)

var providerStartedNetworkRefresh = struct {
	sync.Mutex
	pendingController map[uint]map[uint]struct{}
	pending           map[uint]bool
	running           map[uint]bool
	lastRun           map[uint]time.Time
}{
	pendingController: make(map[uint]map[uint]struct{}),
	pending:           make(map[uint]bool),
	running:           make(map[uint]bool),
	lastRun:           make(map[uint]time.Time),
}

// ScheduleStartedProviderRuntimeNetworkRefresh coalesces post-start address
// refreshes by Provider. A reboot can produce many completed start tasks; one
// DiscoverInstances pass then repairs every running guest instead of issuing a
// GetInstance call or controller-port query for each task. Instance IDs are
// optional for compatibility with callers which only need an address refresh.
func ScheduleStartedProviderRuntimeNetworkRefresh(db *gorm.DB, providerID uint, controllerInstanceIDs ...uint) {
	if db == nil || providerID == 0 {
		return
	}
	providerStartedNetworkRefresh.Lock()
	providerStartedNetworkRefresh.pending[providerID] = true
	pendingController := providerStartedNetworkRefresh.pendingController[providerID]
	if pendingController == nil {
		pendingController = make(map[uint]struct{}, len(controllerInstanceIDs))
		providerStartedNetworkRefresh.pendingController[providerID] = pendingController
	}
	for _, instanceID := range controllerInstanceIDs {
		if instanceID != 0 {
			pendingController[instanceID] = struct{}{}
		}
	}
	if providerStartedNetworkRefresh.running[providerID] {
		providerStartedNetworkRefresh.Unlock()
		return
	}
	providerStartedNetworkRefresh.running[providerID] = true
	providerStartedNetworkRefresh.Unlock()

	go func() {
		for {
			if agentShutdownContext().Err() != nil {
				providerStartedNetworkRefresh.Lock()
				delete(providerStartedNetworkRefresh.pending, providerID)
				delete(providerStartedNetworkRefresh.pendingController, providerID)
				delete(providerStartedNetworkRefresh.running, providerID)
				providerStartedNetworkRefresh.Unlock()
				return
			}
			providerStartedNetworkRefresh.Lock()
			lastRun := providerStartedNetworkRefresh.lastRun[providerID]
			providerStartedNetworkRefresh.Unlock()

			delay := providerStartedNetworkRefreshDebounce
			if !lastRun.IsZero() {
				if remaining := providerStartedNetworkRefreshCooldown - time.Since(lastRun); remaining > delay {
					delay = remaining
				}
			}
			if !waitForAgentShutdown(delay) {
				providerStartedNetworkRefresh.Lock()
				delete(providerStartedNetworkRefresh.pending, providerID)
				delete(providerStartedNetworkRefresh.pendingController, providerID)
				delete(providerStartedNetworkRefresh.running, providerID)
				providerStartedNetworkRefresh.Unlock()
				return
			}

			providerStartedNetworkRefresh.Lock()
			pending := providerStartedNetworkRefresh.pending[providerID]
			delete(providerStartedNetworkRefresh.pending, providerID)
			pendingController := providerStartedNetworkRefresh.pendingController[providerID]
			delete(providerStartedNetworkRefresh.pendingController, providerID)
			controllerIDs := make([]uint, 0, len(pendingController))
			for instanceID := range pendingController {
				controllerIDs = append(controllerIDs, instanceID)
			}
			providerStartedNetworkRefresh.Unlock()
			if pending {
				ctx, cancel := context.WithTimeout(agentShutdownContext(), 2*time.Minute)
				refreshStartedProviderRuntimeNetworks(ctx, db, providerID, controllerIDs)
				cancel()
			}

			providerStartedNetworkRefresh.Lock()
			providerStartedNetworkRefresh.lastRun[providerID] = time.Now()
			if !providerStartedNetworkRefresh.pending[providerID] {
				delete(providerStartedNetworkRefresh.running, providerID)
				providerStartedNetworkRefresh.Unlock()
				return
			}
			providerStartedNetworkRefresh.Unlock()
		}
	}()
}

const (
	// Recovery starts are intentionally gathered behind the task queue. Give the
	// guests a short boot/network window, then wait for every start task on this
	// Provider to settle before one authoritative discovery. The upper bound is
	// a safety valve for a stuck external task; it is not a recurring probe.
	providerRecoveryNetworkRefreshInitialDelay = 45 * time.Second
	providerRecoveryNetworkRefreshPollInterval = 15 * time.Second
	providerRecoveryNetworkRefreshMaxWait      = 10 * time.Minute
)

var providerRecoveryNetworkRefresh = struct {
	sync.Mutex
	pending map[uint]bool
	running map[uint]bool
}{
	pending: make(map[uint]bool),
	running: make(map[uint]bool),
}

// ScheduleProviderRecoveryRuntimeNetworkRefresh schedules one post-reboot
// address reconciliation for a Provider. Unlike ordinary individual starts,
// recovery starts are deliberately held until the Provider has no active start
// tasks, so an ordered task queue cannot cause a fresh discovery after every
// guest. Calls while a run is waiting are coalesced in memory and do no remote
// work themselves.
func ScheduleProviderRecoveryRuntimeNetworkRefresh(db *gorm.DB, providerID uint) {
	if db == nil || providerID == 0 {
		return
	}
	providerRecoveryNetworkRefresh.Lock()
	providerRecoveryNetworkRefresh.pending[providerID] = true
	if providerRecoveryNetworkRefresh.running[providerID] {
		providerRecoveryNetworkRefresh.Unlock()
		return
	}
	providerRecoveryNetworkRefresh.running[providerID] = true
	providerRecoveryNetworkRefresh.Unlock()

	go func() {
		for {
			ctx := agentShutdownContext()
			if !waitForProviderRecoveryStartQuiescenceContext(ctx, db, providerID) {
				providerRecoveryNetworkRefresh.Lock()
				delete(providerRecoveryNetworkRefresh.pending, providerID)
				delete(providerRecoveryNetworkRefresh.running, providerID)
				providerRecoveryNetworkRefresh.Unlock()
				return
			}

			providerRecoveryNetworkRefresh.Lock()
			providerRecoveryNetworkRefresh.pending[providerID] = false
			providerRecoveryNetworkRefresh.Unlock()

			ctx, cancel := context.WithTimeout(agentShutdownContext(), 2*time.Minute)
			refreshProviderRuntimeNetworks(ctx, db, providerID, nil, true)
			cancel()

			providerRecoveryNetworkRefresh.Lock()
			if !providerRecoveryNetworkRefresh.pending[providerID] {
				delete(providerRecoveryNetworkRefresh.pending, providerID)
				delete(providerRecoveryNetworkRefresh.running, providerID)
				providerRecoveryNetworkRefresh.Unlock()
				return
			}
			providerRecoveryNetworkRefresh.Unlock()
		}
	}()
}

func waitForProviderRecoveryStartQuiescence(db *gorm.DB, providerID uint) bool {
	return waitForProviderRecoveryStartQuiescenceContext(agentShutdownContext(), db, providerID)
}

func waitForProviderRecoveryStartQuiescenceContext(ctx context.Context, db *gorm.DB, providerID uint) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	initialTimer := time.NewTimer(providerRecoveryNetworkRefreshInitialDelay)
	defer initialTimer.Stop()
	select {
	case <-initialTimer.C:
	case <-ctx.Done():
		return false
	}

	deadline := time.Now().Add(providerRecoveryNetworkRefreshMaxWait)
	for {
		active, err := providerHasActiveStartTasksContext(ctx, db, providerID)
		if err != nil {
			if global.APP_LOG != nil {
				global.APP_LOG.Warn("检查Provider恢复启动任务状态失败，将执行一次有界网络同步",
					zap.Uint("provider_id", providerID), zap.Error(err))
			}
			return true
		}
		if !active {
			return true
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			if global.APP_LOG != nil {
				global.APP_LOG.Warn("Provider恢复启动任务长时间未收敛，将执行一次有界网络同步",
					zap.Uint("provider_id", providerID), zap.Duration("max_wait", providerRecoveryNetworkRefreshMaxWait))
			}
			return true
		}
		wait := providerRecoveryNetworkRefreshPollInterval
		if remaining < wait {
			wait = remaining
		}
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return false
		}
	}
}

func providerHasActiveStartTasks(db *gorm.DB, providerID uint) (bool, error) {
	return providerHasActiveStartTasksContext(agentShutdownContext(), db, providerID)
}

func providerHasActiveStartTasksContext(ctx context.Context, db *gorm.DB, providerID uint) (bool, error) {
	var count int64
	if ctx == nil {
		ctx = context.Background()
	}
	err := db.WithContext(ctx).Model(&adminModel.Task{}).
		Where("provider_id = ? AND task_type = ? AND status IN ?", providerID, "start",
			[]string{"pending", "processing", "running", "cancelling"}).
		Count(&count).Error
	return count > 0, err
}

func refreshStartedProviderRuntimeNetworks(ctx context.Context, db *gorm.DB, providerID uint, controllerInstanceIDs []uint) {
	refreshProviderRuntimeNetworks(ctx, db, providerID, controllerInstanceIDs, false)
}

func refreshProviderRuntimeNetworks(ctx context.Context, db *gorm.DB, providerID uint, controllerInstanceIDs []uint, recovery bool) {
	repairControllerPorts := func() {
		if len(controllerInstanceIDs) > 0 {
			ScheduleProviderRuntimeNetworkRepair(db, providerID, controllerInstanceIDs, nil)
		}
	}
	var instances []providerModel.Instance
	if err := db.WithContext(ctx).
		Select("id", "name", "uuid", "provider_vm_id").
		Where("provider_id = ? AND status = ?", providerID, "running").
		Find(&instances).Error; err != nil {
		if global.APP_LOG != nil {
			global.APP_LOG.Debug("批量读取已启动实例网络标识失败", zap.Uint("provider_id", providerID), zap.Error(err))
		}
		return
	}
	if len(instances) == 0 {
		return
	}

	var (
		providerInstance providerCore.Provider
		err              error
	)
	if recovery {
		providerInstance, err = providerService.GetProviderInstanceByIDForRecoveryContext(ctx, providerID)
	} else {
		providerInstance, err = providerService.EnsureProviderConnected(ctx, providerID)
	}
	if err != nil {
		if global.APP_LOG != nil {
			global.APP_LOG.Debug("获取Provider刷新启动后网络失败", zap.Uint("provider_id", providerID), zap.Error(err))
		}
		repairControllerPorts()
		return
	}
	var discovered []providerCore.DiscoveredInstance
	if recovery {
		discovered, err = providerCore.DiscoverInstancesForRecovery(ctx, providerInstance)
	} else {
		discovered, err = providerInstance.DiscoverInstances(ctx)
	}
	if err != nil {
		if global.APP_LOG != nil {
			global.APP_LOG.Debug("批量发现已启动实例网络失败", zap.Uint("provider_id", providerID), zap.Error(err))
		}
		repairControllerPorts()
		return
	}

	byProviderInstanceID := make(map[string]uint, len(instances))
	byUUID := make(map[string]uint, len(instances))
	byName := make(map[string]uint, len(instances))
	for _, instance := range instances {
		if providerInstanceID := strings.TrimSpace(instance.ProviderVMID); providerInstanceID != "" {
			byProviderInstanceID[providerInstanceID] = instance.ID
		}
		if instanceUUID := strings.TrimSpace(instance.UUID); instanceUUID != "" {
			byUUID[instanceUUID] = instance.ID
		}
		if name := strings.TrimSpace(instance.Name); name != "" {
			byName[name] = instance.ID
		}
	}

	snapshots := make([]DiscoveredInstanceNetwork, 0, len(instances))
	for _, remote := range discovered {
		instanceID, ok := runtimeNetworkInstanceIDForRemote(remote, byProviderInstanceID, byUUID, byName)
		if !ok {
			continue
		}
		snapshots = append(snapshots, DiscoveredInstanceNetwork{
			InstanceID:  instanceID,
			Status:      remote.Status,
			PrivateIP:   remote.PrivateIP,
			PublicIP:    remote.PublicIP,
			IPv6Address: remote.IPv6Address,
			SSHPort:     remote.SSHPort,
		})
	}
	syncResult, syncErr := SynchronizeDiscoveredInstanceNetworks(ctx, db, providerID, snapshots)
	controllerIDs := make([]uint, 0, len(controllerInstanceIDs)+len(syncResult.PrivateIPChangedInstanceIDs))
	controllerIDs = append(controllerIDs, controllerInstanceIDs...)
	controllerIDs = append(controllerIDs, syncResult.PrivateIPChangedInstanceIDs...)
	if len(controllerIDs) > 0 || len(syncResult.NetworkChangedInstanceIDs) > 0 {
		ScheduleProviderRuntimeNetworkRepair(db, providerID, controllerIDs, syncResult.NetworkChangedInstanceIDs)
	}
	if syncErr != nil && global.APP_LOG != nil {
		global.APP_LOG.Debug("批量写回启动后实例网络失败", zap.Uint("provider_id", providerID), zap.Error(syncErr))
	}
}

func runtimeNetworkInstanceIDForRemote(remote providerCore.DiscoveredInstance, byProviderInstanceID, byUUID, byName map[string]uint) (uint, bool) {
	if instanceID, ok := byProviderInstanceID[strings.TrimSpace(remote.ProviderInstanceID)]; ok && instanceID != 0 {
		return instanceID, true
	}
	if instanceID, ok := byUUID[strings.TrimSpace(remote.UUID)]; ok && instanceID != 0 {
		return instanceID, true
	}
	instanceID, ok := byName[strings.TrimSpace(remote.Name)]
	return instanceID, ok && instanceID != 0
}
