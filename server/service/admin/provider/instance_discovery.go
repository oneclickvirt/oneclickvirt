package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	agentService "oneclickvirt/service/agent"
	provider2 "oneclickvirt/service/provider"

	"go.uber.org/zap"
)

// DiscoveryResult 实例发现结果
type DiscoveryResult struct {
	ProviderID          uint                          `json:"providerId"`
	ProviderName        string                        `json:"providerName"`
	ProviderType        string                        `json:"providerType"`
	DiscoveredInstances []provider.DiscoveredInstance `json:"discoveredInstances"`
	TotalCount          int                           `json:"totalCount"`
	AlreadyManaged      int                           `json:"alreadyManaged"` // 已纳管的实例数
	NewInstances        int                           `json:"newInstances"`   // 新发现的实例数
	DiscoveredAt        time.Time                     `json:"discoveredAt"`
	Error               string                        `json:"error,omitempty"`
}

// DiscoverProviderInstances 发现指定provider上的所有实例
func (s *Service) DiscoverProviderInstances(ctx context.Context, providerID uint) (*DiscoveryResult, error) {
	return s.discoverProviderInstances(ctx, providerID, false)
}

// discoverProviderInstances is shared by ordinary sync and the narrow
// post-outage recovery path. allowAutoRecoveryFrozen is never exposed to API
// callers: it exists solely so a scheduler-claimed, health-auto-frozen SSH/API
// node can be probed once without making a manually frozen node operable.
func (s *Service) discoverProviderInstances(ctx context.Context, providerID uint, allowAutoRecoveryFrozen bool) (*DiscoveryResult, error) {
	global.APP_LOG.Debug("开始发现Provider实例", zap.Uint("providerId", providerID))

	// 1. 获取Provider信息
	var providerInfo providerModel.Provider
	if err := global.APP_DB.First(&providerInfo, providerID).Error; err != nil {
		return nil, fmt.Errorf("获取Provider信息失败: %w", err)
	}

	// 2. 获取Provider实例（确保连接可用，兼容agent模式刚上线场景）
	if allowAutoRecoveryFrozen && providerInfo.IsFrozen && !isHealthAutoRecoveryFrozenReason(providerInfo.FrozenReason) {
		return nil, fmt.Errorf("Provider已被手工冻结，拒绝恢复探测")
	}
	var (
		providerInstance provider.Provider
		err              error
	)
	if allowAutoRecoveryFrozen {
		providerInstance, err = provider2.EnsureProviderConnectedForRecovery(ctx, providerID)
	} else {
		providerInstance, err = provider2.EnsureProviderConnected(ctx, providerID)
	}
	if err != nil {
		return nil, fmt.Errorf("获取Provider实例失败: %w", err)
	}

	// 3. 调用DiscoverInstances接口
	var discoveredInstances []provider.DiscoveredInstance
	if allowAutoRecoveryFrozen {
		// Recovery only needs identity, runtime state, and current addresses. Use
		// a provider's bounded recovery capability when available; importing
		// metadata/firewall data belongs to the explicit administrator sync path.
		discoveredInstances, err = provider.DiscoverInstancesForRecovery(ctx, providerInstance)
	} else {
		discoveredInstances, err = providerInstance.DiscoverInstances(ctx)
	}
	if err != nil {
		return &DiscoveryResult{
			ProviderID:   providerID,
			ProviderName: providerInfo.Name,
			ProviderType: providerInfo.Type,
			DiscoveredAt: time.Now(),
			Error:        err.Error(),
		}, fmt.Errorf("发现实例失败: %w", err)
	}
	// Credentials and port allocations left by the supported shell projects are
	// collected before a normal discovery result is returned or an import starts.
	// Recovery already has the authoritative runtime list it needs (identity,
	// status, and addresses), so deliberately skip optional metadata/firewall
	// enrichment there. That keeps one reboot recovery to one provider discovery
	// call rather than adding follow-up host commands for every recovery pass.
	if !allowAutoRecoveryFrozen {
		discoveredInstances = s.enrichDiscoveredInstanceMetadata(ctx, providerInfo.Type, providerInstance, discoveredInstances)
		discoveredInstances = enrichDiscoveredFirewallMappings(ctx, providerInstance, discoveredInstances)
	}
	discoveredInstances = normalizeDiscoveredInstances(providerID, providerInfo.Type, discoveredInstances)

	// 4. 统计已纳管和新实例
	alreadyManaged := 0
	newInstances := 0

	// 获取当前数据库中该provider的所有实例
	var existingInstances []providerModel.Instance
	if err := global.APP_DB.Where("provider_id = ?", providerID).
		Select("id", "uuid", "name", "provider_vm_id").
		Find(&existingInstances).Error; err != nil {
		global.APP_LOG.Warn("查询已有实例失败", zap.Error(err))
	}

	// 为旧版导入记录回填Proxmox VMID/CTID。首次仍可按UUID/名称兼容匹配，
	// 回填后guest即使改名，后续发现也会继续识别为同一实例。
	matches := matchDiscoveredAndDBInstances(providerInfo.Type, discoveredInstances, existingInstances)
	if backfills := providerInstanceIDBackfills(providerInfo.Type, discoveredInstances, existingInstances, matches); len(backfills) > 0 {
		// A recovery discovery may contain hundreds of guests. Keep the legacy
		// identity upgrade set-based and bounded instead of issuing one UPDATE per
		// matched instance (which would turn a single provider probe into N+1 DB
		// writes).
		if err := batchBackfillProviderInstanceIDs(global.APP_DB, backfills); err != nil {
			global.APP_LOG.Warn("批量回填实例ProviderVMID失败", zap.Error(err))
		}
	}

	// 统计
	for remoteIndex := range discoveredInstances {
		if _, managed := matches.RemoteToDB[remoteIndex]; managed {
			alreadyManaged++
		} else {
			newInstances++
		}
	}

	result := &DiscoveryResult{
		ProviderID:          providerID,
		ProviderName:        providerInfo.Name,
		ProviderType:        providerInfo.Type,
		DiscoveredInstances: discoveredInstances,
		TotalCount:          len(discoveredInstances),
		AlreadyManaged:      alreadyManaged,
		NewInstances:        newInstances,
		DiscoveredAt:        time.Now(),
	}

	global.APP_LOG.Debug("Provider实例发现完成",
		zap.Uint("providerId", providerID),
		zap.String("provider", providerInfo.Name),
		zap.Int("total", result.TotalCount),
		zap.Int("alreadyManaged", result.AlreadyManaged),
		zap.Int("newInstances", result.NewInstances))

	return result, nil
}

func isHealthAutoRecoveryFrozenReason(reason string) bool {
	reason = strings.TrimSpace(reason)
	return strings.HasPrefix(reason, "健康检查连续失败") || strings.HasPrefix(reason, "Agent 反向连接连续断开")
}

// GetOrphanedInstances 获取未纳管的实例列表（仅返回新发现的实例）
func (s *Service) GetOrphanedInstances(ctx context.Context, providerID uint) ([]provider.DiscoveredInstance, error) {
	// 先执行发现
	result, err := s.DiscoverProviderInstances(ctx, providerID)
	if err != nil {
		return nil, err
	}

	// 过滤出未纳管的实例
	var orphanedInstances []provider.DiscoveredInstance

	// 获取当前数据库中该provider的所有实例
	var existingInstances []providerModel.Instance
	if err := global.APP_DB.Where("provider_id = ?", providerID).
		Select("uuid", "name", "provider_vm_id").
		Find(&existingInstances).Error; err != nil {
		return nil, fmt.Errorf("查询已有实例失败: %w", err)
	}

	// 筛选未纳管实例
	for _, discovered := range result.DiscoveredInstances {
		if !hasMatchingDBInstance(result.ProviderType, discovered, existingInstances) {
			orphanedInstances = append(orphanedInstances, discovered)
		}
	}

	global.APP_LOG.Debug("获取未纳管实例完成",
		zap.Uint("providerId", providerID),
		zap.Int("orphanedCount", len(orphanedInstances)))

	return orphanedInstances, nil
}

// CompareInstancesWithRemote 比较数据库实例与远程实例，检测变化
func (s *Service) CompareInstancesWithRemote(ctx context.Context, providerID uint) (*InstanceSyncReport, error) {
	return s.compareInstancesWithRemote(ctx, providerID, false)
}

// CompareInstancesWithRemoteForRecovery performs the same single discovery
// pass as normal sync, but may connect a Provider which was automatically
// frozen after prolonged health-check failure. The scheduler must claim and
// revalidate that condition before calling this method.
func (s *Service) CompareInstancesWithRemoteForRecovery(ctx context.Context, providerID uint) (*InstanceSyncReport, error) {
	return s.compareInstancesWithRemote(ctx, providerID, true)
}

func (s *Service) compareInstancesWithRemote(ctx context.Context, providerID uint, allowAutoRecoveryFrozen bool) (*InstanceSyncReport, error) {
	global.APP_LOG.Debug("开始比较实例变化", zap.Uint("providerId", providerID))

	// 1. 发现远程实例
	discoveryResult, err := s.discoverProviderInstances(ctx, providerID, allowAutoRecoveryFrozen)
	if err != nil {
		return nil, err
	}

	// 2. 获取数据库中的实例
	var dbInstances []providerModel.Instance
	if err := global.APP_DB.Where("provider_id = ?", providerID).
		Select("id", "uuid", "name", "provider_id", "status", "desired_state", "is_imported", "provider_vm_id", "user_id", "instance_type", "is_frozen", "expires_at", "traffic_limited", "traffic_stopped", "expiry_stopped").
		Find(&dbInstances).Error; err != nil {
		return nil, fmt.Errorf("查询数据库实例失败: %w", err)
	}

	// 3. 以Provider远端身份优先建立一对一匹配。Proxmox使用VMID/CTID，
	// 因此guest改名不会被误判为一新一删两个实例。
	matches := matchDiscoveredAndDBInstances(discoveryResult.ProviderType, discoveryResult.DiscoveredInstances, dbInstances)

	// 4. 分析变化
	var newInstances []provider.DiscoveredInstance
	var deletedInstances []providerModel.Instance
	var changedInstances []InstanceChange
	matchedInstances := make([]MatchedInstanceSnapshot, 0, len(matches.RemoteToDB))
	networkSnapshots := make([]agentService.DiscoveredInstanceNetwork, 0, len(matches.RemoteToDB))

	// 检测新增实例
	for remoteIndex := range discoveryResult.DiscoveredInstances {
		if _, exists := matches.RemoteToDB[remoteIndex]; !exists {
			newInstances = append(newInstances, discoveryResult.DiscoveredInstances[remoteIndex])
		}
	}

	// 检测删除的实例
	for dbIndex := range dbInstances {
		if _, exists := matches.DBToRemote[dbIndex]; !exists {
			deletedInstances = append(deletedInstances, dbInstances[dbIndex])
		}
	}

	// 检测状态变化的实例
	for remoteIndex, dbIndex := range matches.RemoteToDB {
		remoteInst := discoveryResult.DiscoveredInstances[remoteIndex]
		dbInst := dbInstances[dbIndex]
		matchedInstances = append(matchedInstances, MatchedInstanceSnapshot{
			InstanceID:      dbInst.ID,
			UserID:          dbInst.UserID,
			InstanceType:    dbInst.InstanceType,
			DatabaseStatus:  dbInst.Status,
			DesiredState:    dbInst.DesiredState,
			IsImported:      dbInst.IsImported,
			IsFrozen:        dbInst.IsFrozen,
			ExpiresAt:       dbInst.ExpiresAt,
			TrafficLimited:  dbInst.TrafficLimited,
			TrafficStopped:  dbInst.TrafficStopped,
			ExpiryStopped:   dbInst.ExpiryStopped,
			RemoteStatus:    remoteInst.Status,
			PrivateIP:       remoteInst.PrivateIP,
			PublicIP:        remoteInst.PublicIP,
			IPv6Address:     remoteInst.IPv6Address,
			SSHPort:         remoteInst.SSHPort,
			RuntimeIdentity: cloneRecoveryIdentity(remoteInst.RuntimeIdentity),
		})
		networkSnapshots = append(networkSnapshots, agentService.DiscoveredInstanceNetwork{
			InstanceID:  dbInst.ID,
			Status:      remoteInst.Status,
			PrivateIP:   remoteInst.PrivateIP,
			PublicIP:    remoteInst.PublicIP,
			IPv6Address: remoteInst.IPv6Address,
			SSHPort:     remoteInst.SSHPort,
		})
		if dbInst.Status != remoteInst.Status {
			changedInstances = append(changedInstances, InstanceChange{
				InstanceID: dbInst.ID,
				UUID:       dbInst.UUID,
				Name:       dbInst.Name,
				OldStatus:  dbInst.Status,
				NewStatus:  remoteInst.Status,
			})
		}
	}

	report := &InstanceSyncReport{
		ProviderID:       providerID,
		ProviderName:     discoveryResult.ProviderName,
		TotalRemote:      len(discoveryResult.DiscoveredInstances),
		TotalDB:          len(dbInstances),
		NewInstances:     newInstances,
		DeletedInstances: deletedInstances,
		ChangedInstances: changedInstances,
		MatchedInstances: matchedInstances,
		CheckedAt:        time.Now(),
	}

	// Discovery already obtained every remote address in one provider call.  Use
	// that result to repair runtime-only mappings; do not issue a GetInstance
	// call per matched guest.  The helper performs a batched DB update and
	// coalesces controller-forward/egress repair once per provider.
	if syncResult, syncErr := agentService.SynchronizeDiscoveredInstanceNetworks(ctx, global.APP_DB, providerID, networkSnapshots); syncErr != nil {
		// A bounded multi-batch update may have committed earlier address changes
		// before a later batch fails. Repair those committed mappings instead of
		// leaving the controller forwarding state stale until a future scan.
		if len(syncResult.PrivateIPChangedInstanceIDs) > 0 || len(syncResult.NetworkChangedInstanceIDs) > 0 {
			agentService.ScheduleProviderRuntimeNetworkRepair(global.APP_DB, providerID, syncResult.PrivateIPChangedInstanceIDs, syncResult.NetworkChangedInstanceIDs)
		}
		global.APP_LOG.Warn("同步发现实例的运行时网络信息失败",
			zap.Uint("providerId", providerID), zap.Error(syncErr))
	} else if len(syncResult.PrivateIPChangedInstanceIDs) > 0 || len(syncResult.NetworkChangedInstanceIDs) > 0 {
		agentService.ScheduleProviderRuntimeNetworkRepair(global.APP_DB, providerID, syncResult.PrivateIPChangedInstanceIDs, syncResult.NetworkChangedInstanceIDs)
	}

	global.APP_LOG.Debug("实例变化检测完成",
		zap.Uint("providerId", providerID),
		zap.Int("newCount", len(newInstances)),
		zap.Int("deletedCount", len(deletedInstances)),
		zap.Int("changedCount", len(changedInstances)))

	return report, nil
}

// InstanceSyncReport 实例同步报告
type InstanceSyncReport struct {
	ProviderID       uint                          `json:"providerId"`
	ProviderName     string                        `json:"providerName"`
	TotalRemote      int                           `json:"totalRemote"`      // 远程总实例数
	TotalDB          int                           `json:"totalDB"`          // 数据库总实例数
	NewInstances     []provider.DiscoveredInstance `json:"newInstances"`     // 新增实例
	DeletedInstances []providerModel.Instance      `json:"deletedInstances"` // 已删除实例
	ChangedInstances []InstanceChange              `json:"changedInstances"` // 状态变化实例
	// MatchedInstances is internal scheduler input.  It contains only safe,
	// credential-free fields and is deliberately excluded from the public
	// discovery response to preserve its existing API shape.
	MatchedInstances []MatchedInstanceSnapshot `json:"-"`
	CheckedAt        time.Time                 `json:"checkedAt"`
}

// MatchedInstanceSnapshot couples a managed instance with the minimal remote
// facts from the same discovery response.  It intentionally excludes provider
// RawData, passwords, keys, and any other discovery-only credentials.
type MatchedInstanceSnapshot struct {
	InstanceID      uint
	UserID          uint
	InstanceType    string
	DatabaseStatus  string
	DesiredState    string
	IsImported      bool
	IsFrozen        bool
	ExpiresAt       *time.Time
	TrafficLimited  bool
	TrafficStopped  bool
	ExpiryStopped   bool
	RemoteStatus    string
	PrivateIP       string
	PublicIP        string
	IPv6Address     string
	SSHPort         int
	RuntimeIdentity *provider.RecoveryInstanceIdentity
}

func cloneRecoveryIdentity(identity *provider.RecoveryInstanceIdentity) *provider.RecoveryInstanceIdentity {
	if identity == nil {
		return nil
	}
	copy := *identity
	copy.Node = strings.TrimSpace(copy.Node)
	copy.ID = strings.TrimSpace(copy.ID)
	copy.Type = strings.ToLower(strings.TrimSpace(copy.Type))
	return &copy
}

// InstanceChange 实例变化记录
type InstanceChange struct {
	InstanceID uint   `json:"instanceId"`
	UUID       string `json:"uuid"`
	Name       string `json:"name"`
	OldStatus  string `json:"oldStatus"`
	NewStatus  string `json:"newStatus"`
}

// RemoteOrphanInfo 远程孤儿实例信息
type RemoteOrphanInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	UUID    string `json:"uuid"`
	Type    string `json:"type"`            // container / vm
	Status  string `json:"status"`          // 远程实例状态
	Deleted bool   `json:"deleted"`         // 是否成功删除
	Error   string `json:"error,omitempty"` // 删除失败原因
}

// CleanupOrphanResult 孤儿清理结果
type CleanupOrphanResult struct {
	TotalOrphans int                `json:"totalOrphans"` // 发现的孤儿实例总数
	DeletedCount int                `json:"deletedCount"` // 成功删除数量
	FailedCount  int                `json:"failedCount"`  // 删除失败数量
	Orphans      []RemoteOrphanInfo `json:"orphans"`      // 所有孤儿实例详情
}

// CleanupOrphanInstances 强制单向同步：删除远程服务器上不存在于主控数据库的实例
// 主控数据库为权威来源，远程多余的实例视为"孤儿"，直接删除
func (s *Service) CleanupOrphanInstances(ctx context.Context, providerID uint) (*CleanupOrphanResult, error) {
	global.APP_LOG.Info("开始强制单向同步：清理远程孤儿实例", zap.Uint("providerId", providerID))

	// 1. 获取Provider信息
	var providerInfo providerModel.Provider
	if err := global.APP_DB.First(&providerInfo, providerID).Error; err != nil {
		return nil, fmt.Errorf("获取Provider信息失败: %w", err)
	}

	// 2. 获取Provider实例连接
	providerInstance, err := provider2.EnsureProviderConnected(ctx, providerID)
	if err != nil {
		return nil, fmt.Errorf("连接Provider失败: %w", err)
	}

	// 3. 发现远程实例
	discoveredInstances, err := providerInstance.DiscoverInstances(ctx)
	if err != nil {
		return nil, fmt.Errorf("发现远程实例失败: %w", err)
	}

	// 4. 获取数据库中该Provider的所有实例
	var dbInstances []providerModel.Instance
	if err := global.APP_DB.Where("provider_id = ?", providerID).
		Select("id", "uuid", "name", "instance_type", "status", "provider_vm_id").
		Find(&dbInstances).Error; err != nil {
		return nil, fmt.Errorf("查询数据库实例失败: %w", err)
	}

	// 5. 识别远程孤儿实例（存在于远程但不在数据库中）
	var orphans []RemoteOrphanInfo
	for _, remote := range discoveredInstances {
		if !hasMatchingDBInstance(providerInfo.Type, remote, dbInstances) {
			orphans = append(orphans, RemoteOrphanInfo{
				ID:     discoveredInstanceDeleteID(providerInfo.Type, remote),
				Name:   remote.Name,
				UUID:   remote.UUID,
				Type:   remote.InstanceType,
				Status: remote.Status,
			})
		}
	}

	if len(orphans) == 0 {
		global.APP_LOG.Info("未发现远程孤儿实例，无需清理",
			zap.Uint("providerId", providerID))
		return &CleanupOrphanResult{
			TotalOrphans: 0,
			DeletedCount: 0,
			FailedCount:  0,
			Orphans:      []RemoteOrphanInfo{},
		}, nil
	}

	global.APP_LOG.Info("发现远程孤儿实例，开始清理",
		zap.Uint("providerId", providerID),
		zap.Int("orphanCount", len(orphans)))

	// 7. 逐个删除远程孤儿实例
	deletedCount := 0
	failedCount := 0
	for i := range orphans {
		remoteID := orphans[i].ID
		if remoteID == "" {
			remoteID = orphans[i].Name
		}
		global.APP_LOG.Debug("删除远程孤儿实例",
			zap.Uint("providerId", providerID),
			zap.String("remoteID", remoteID),
			zap.String("name", orphans[i].Name))

		if err := providerInstance.DeleteInstance(ctx, remoteID); err != nil {
			orphans[i].Error = err.Error()
			orphans[i].Deleted = false
			failedCount++
			global.APP_LOG.Warn("删除远程孤儿实例失败",
				zap.Uint("providerId", providerID),
				zap.String("remoteID", remoteID),
				zap.Error(err))
		} else {
			orphans[i].Deleted = true
			deletedCount++
			global.APP_LOG.Info("成功删除远程孤儿实例",
				zap.Uint("providerId", providerID),
				zap.String("remoteID", remoteID))
		}
	}

	result := &CleanupOrphanResult{
		TotalOrphans: len(orphans),
		DeletedCount: deletedCount,
		FailedCount:  failedCount,
		Orphans:      orphans,
	}

	global.APP_LOG.Info("远程孤儿实例清理完成",
		zap.Uint("providerId", providerID),
		zap.Int("totalOrphans", result.TotalOrphans),
		zap.Int("deleted", result.DeletedCount),
		zap.Int("failed", result.FailedCount))

	return result, nil
}
