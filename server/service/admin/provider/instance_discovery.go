package provider

import (
	"context"
	"fmt"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	provider2 "oneclickvirt/service/provider"

	"go.uber.org/zap"
)

// DiscoveryResult 实例发现结果
type DiscoveryResult struct {
	ProviderID          uint                          `json:"providerId"`
	ProviderName        string                        `json:"providerName"`
	DiscoveredInstances []provider.DiscoveredInstance `json:"discoveredInstances"`
	TotalCount          int                           `json:"totalCount"`
	AlreadyManaged      int                           `json:"alreadyManaged"` // 已纳管的实例数
	NewInstances        int                           `json:"newInstances"`   // 新发现的实例数
	DiscoveredAt        time.Time                     `json:"discoveredAt"`
	Error               string                        `json:"error,omitempty"`
}

// DiscoverProviderInstances 发现指定provider上的所有实例
func (s *Service) DiscoverProviderInstances(ctx context.Context, providerID uint) (*DiscoveryResult, error) {
	global.APP_LOG.Debug("开始发现Provider实例", zap.Uint("providerId", providerID))

	// 1. 获取Provider信息
	var providerInfo providerModel.Provider
	if err := global.APP_DB.First(&providerInfo, providerID).Error; err != nil {
		return nil, fmt.Errorf("获取Provider信息失败: %w", err)
	}

	// 2. 获取Provider实例（确保连接可用，兼容agent模式刚上线场景）
	providerInstance, err := provider2.EnsureProviderConnected(ctx, providerID)
	if err != nil {
		return nil, fmt.Errorf("获取Provider实例失败: %w", err)
	}

	// 3. 调用DiscoverInstances接口
	discoveredInstances, err := providerInstance.DiscoverInstances(ctx)
	if err != nil {
		return &DiscoveryResult{
			ProviderID:   providerID,
			ProviderName: providerInfo.Name,
			DiscoveredAt: time.Now(),
			Error:        err.Error(),
		}, fmt.Errorf("发现实例失败: %w", err)
	}

	// 4. 统计已纳管和新实例
	alreadyManaged := 0
	newInstances := 0

	// 获取当前数据库中该provider的所有实例
	var existingInstances []providerModel.Instance
	if err := global.APP_DB.Where("provider_id = ?", providerID).
		Select("uuid", "name").
		Find(&existingInstances).Error; err != nil {
		global.APP_LOG.Warn("查询已有实例失败", zap.Error(err))
	}

	// 创建已有实例的映射（用UUID和名称双重匹配）
	existingUUIDs := make(map[string]bool)
	existingNames := make(map[string]bool)
	for _, inst := range existingInstances {
		existingUUIDs[inst.UUID] = true
		existingNames[inst.Name] = true
	}

	// 统计
	for _, discovered := range discoveredInstances {
		if existingUUIDs[discovered.UUID] || existingNames[discovered.Name] {
			alreadyManaged++
		} else {
			newInstances++
		}
	}

	result := &DiscoveryResult{
		ProviderID:          providerID,
		ProviderName:        providerInfo.Name,
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
		Select("uuid", "name").
		Find(&existingInstances).Error; err != nil {
		return nil, fmt.Errorf("查询已有实例失败: %w", err)
	}

	existingUUIDs := make(map[string]bool)
	existingNames := make(map[string]bool)
	for _, inst := range existingInstances {
		existingUUIDs[inst.UUID] = true
		existingNames[inst.Name] = true
	}

	// 筛选未纳管实例
	for _, discovered := range result.DiscoveredInstances {
		if !existingUUIDs[discovered.UUID] && !existingNames[discovered.Name] {
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
	global.APP_LOG.Debug("开始比较实例变化", zap.Uint("providerId", providerID))

	// 1. 发现远程实例
	discoveryResult, err := s.DiscoverProviderInstances(ctx, providerID)
	if err != nil {
		return nil, err
	}

	// 2. 获取数据库中的实例
	var dbInstances []providerModel.Instance
	if err := global.APP_DB.Where("provider_id = ?", providerID).
		Select("id", "uuid", "name", "status", "is_imported").
		Find(&dbInstances).Error; err != nil {
		return nil, fmt.Errorf("查询数据库实例失败: %w", err)
	}

	// 3. 创建映射用于比较
	remoteInstanceMap := make(map[string]*provider.DiscoveredInstance)
	for i := range discoveryResult.DiscoveredInstances {
		inst := &discoveryResult.DiscoveredInstances[i]
		remoteInstanceMap[inst.UUID] = inst
		// 也用名称作为备用键
		if inst.UUID == "" {
			remoteInstanceMap[inst.Name] = inst
		}
	}

	dbInstanceMap := make(map[string]*providerModel.Instance)
	for i := range dbInstances {
		inst := &dbInstances[i]
		dbInstanceMap[inst.UUID] = inst
		// 也用名称作为备用键
		if inst.UUID == "" {
			dbInstanceMap[inst.Name] = inst
		}
	}

	// 4. 分析变化
	var newInstances []provider.DiscoveredInstance
	var deletedInstances []providerModel.Instance
	var changedInstances []InstanceChange

	// 检测新增实例
	for uuid, remoteInst := range remoteInstanceMap {
		if _, exists := dbInstanceMap[uuid]; !exists {
			newInstances = append(newInstances, *remoteInst)
		}
	}

	// 检测删除的实例
	for uuid, dbInst := range dbInstanceMap {
		if _, exists := remoteInstanceMap[uuid]; !exists {
			deletedInstances = append(deletedInstances, *dbInst)
		}
	}

	// 检测状态变化的实例
	for uuid, remoteInst := range remoteInstanceMap {
		if dbInst, exists := dbInstanceMap[uuid]; exists {
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
	}

	report := &InstanceSyncReport{
		ProviderID:       providerID,
		ProviderName:     discoveryResult.ProviderName,
		TotalRemote:      len(discoveryResult.DiscoveredInstances),
		TotalDB:          len(dbInstances),
		NewInstances:     newInstances,
		DeletedInstances: deletedInstances,
		ChangedInstances: changedInstances,
		CheckedAt:        time.Now(),
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
	CheckedAt        time.Time                     `json:"checkedAt"`
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
		Select("id", "uuid", "name", "instance_type", "status").
		Find(&dbInstances).Error; err != nil {
		return nil, fmt.Errorf("查询数据库实例失败: %w", err)
	}

	// 5. 构建数据库实例映射（用于快速查找）
	dbUUIDs := make(map[string]bool)
	dbNames := make(map[string]bool)
	for _, inst := range dbInstances {
		if inst.UUID != "" {
			dbUUIDs[inst.UUID] = true
		}
		if inst.Name != "" {
			dbNames[inst.Name] = true
		}
	}

	// 6. 识别远程孤儿实例（存在于远程但不在数据库中）
	var orphans []RemoteOrphanInfo
	for _, remote := range discoveredInstances {
		// 通过UUID或名称匹配，如果数据库中没有则为孤儿
		if !dbUUIDs[remote.UUID] && !dbNames[remote.Name] {
			orphanID := remote.UUID
			if orphanID == "" {
				orphanID = remote.Name
			}
			orphans = append(orphans, RemoteOrphanInfo{
				ID:     orphanID,
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
