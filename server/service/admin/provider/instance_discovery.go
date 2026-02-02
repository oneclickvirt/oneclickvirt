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
	global.APP_LOG.Info("开始发现Provider实例", zap.Uint("providerId", providerID))

	// 1. 获取Provider信息
	var providerInfo providerModel.Provider
	if err := global.APP_DB.First(&providerInfo, providerID).Error; err != nil {
		return nil, fmt.Errorf("获取Provider信息失败: %w", err)
	}

	// 2. 获取Provider实例
	providerInstance, err := provider2.GetProviderInstanceByID(providerID)
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

	global.APP_LOG.Info("Provider实例发现完成",
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

	global.APP_LOG.Info("获取未纳管实例完成",
		zap.Uint("providerId", providerID),
		zap.Int("orphanedCount", len(orphanedInstances)))

	return orphanedInstances, nil
}

// CompareInstancesWithRemote 比较数据库实例与远程实例，检测变化
func (s *Service) CompareInstancesWithRemote(ctx context.Context, providerID uint) (*InstanceSyncReport, error) {
	global.APP_LOG.Info("开始比较实例变化", zap.Uint("providerId", providerID))

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

	global.APP_LOG.Info("实例变化检测完成",
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
