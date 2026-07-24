package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func mustJSON(value interface{}) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}

func validateReplacedEgressState(response *EgressStateResponse, profileCount, bindingCount int, apply bool) error {
	if response == nil {
		return fmt.Errorf("Agent未返回出口状态替换结果")
	}
	if response.ProfileCount != profileCount || response.BindingCount != bindingCount {
		return fmt.Errorf("Agent出口状态数量不一致: profiles=%d/%d bindings=%d/%d",
			response.ProfileCount, profileCount, response.BindingCount, bindingCount)
	}
	if apply && (!response.Reconcile.Applied || len(response.Reconcile.Errors) > 0) {
		detail := "Agent未确认出口规则已应用"
		if len(response.Reconcile.Errors) > 0 {
			detail = strings.Join(response.Reconcile.Errors, "; ")
		}
		return fmt.Errorf("节点出口规则批量应用未生效: %s", detail)
	}
	return nil
}

// RestoreProviderEgress replays the complete controller-authoritative state
// after an Agent reconnect or local SQLite loss. All controller reads are
// batched, remote work happens outside database transactions, and the Agent is
// called exactly once so nftables is rebuilt only once.
func (s *InstanceEgressService) RestoreProviderEgress(ctx context.Context, providerID uint, apply bool) (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("数据库连接不可用")
	}
	if providerID == 0 {
		return 0, fmt.Errorf("ProviderID不能为空")
	}
	var bindings []monitoringModel.EgressDesiredBinding
	if err := s.db.WithContext(ctx).Where("provider_id = ?", providerID).Order("id ASC").Find(&bindings).Error; err != nil {
		return 0, err
	}
	if len(bindings) == 0 {
		return 0, nil
	}

	active := make([]monitoringModel.EgressDesiredBinding, 0, len(bindings))
	pending := make([]monitoringModel.EgressDesiredBinding, 0)
	instanceIDs := make([]uint, 0, len(bindings))
	for i := range bindings {
		if bindings[i].PendingDelete {
			pending = append(pending, bindings[i])
			continue
		}
		active = append(active, bindings[i])
		instanceIDs = append(instanceIDs, bindings[i].InstanceID)
	}

	instancesByID := make(map[uint]*providerModel.Instance, len(instanceIDs))
	monitorsByInstanceID := make(map[uint]*monitoringModel.AgentMonitor, len(instanceIDs))
	if len(instanceIDs) > 0 {
		var instances []providerModel.Instance
		if err := s.db.WithContext(ctx).
			Where("provider_id = ? AND id IN ?", providerID, instanceIDs).
			Find(&instances).Error; err != nil {
			return 0, fmt.Errorf("批量读取出口实例失败: %w", err)
		}
		for i := range instances {
			instancesByID[instances[i].ID] = &instances[i]
		}
		var monitors []monitoringModel.AgentMonitor
		if err := s.db.WithContext(ctx).
			Where("provider_id = ? AND instance_id IN ? AND is_enabled = ?", providerID, instanceIDs, true).
			Find(&monitors).Error; err != nil {
			return 0, fmt.Errorf("批量读取出口监控接口失败: %w", err)
		}
		for i := range monitors {
			monitorsByInstanceID[monitors[i].InstanceID] = &monitors[i]
		}
	}

	var desiredProfiles []monitoringModel.EgressDesiredProfile
	if err := s.db.WithContext(ctx).
		Where("provider_id = ?", providerID).
		Order("id ASC").
		Find(&desiredProfiles).Error; err != nil {
		return 0, fmt.Errorf("批量读取出口配置失败: %w", err)
	}
	desiredProfilesByID := make(map[string]*monitoringModel.EgressDesiredProfile, len(desiredProfiles))
	for i := range desiredProfiles {
		desiredProfilesByID[desiredProfiles[i].ProfileID] = &desiredProfiles[i]
	}

	node, config, err := s.loadProviderContext(ctx, providerID)
	if err != nil {
		return 0, fmt.Errorf("读取Provider Agent配置失败: %w", err)
	}
	profileRequestsByID := make(map[string]EgressProfileRequest)
	profileOrder := make([]string, 0, len(desiredProfiles))
	bindingRequests := make([]EgressBindingRequest, 0, len(active))
	errs := make([]error, 0)
	for i := range active {
		instance := instancesByID[active[i].InstanceID]
		if instance == nil {
			errs = append(errs, fmt.Errorf("实例%d不存在或不属于当前Provider", active[i].InstanceID))
			continue
		}
		desiredProfile := desiredProfilesByID[active[i].ProfileID]
		if desiredProfile == nil {
			errs = append(errs, fmt.Errorf("实例%d引用的出口配置%s不存在", instance.ID, active[i].ProfileID))
			continue
		}
		if _, exists := profileRequestsByID[active[i].ProfileID]; !exists {
			profileRequest, profileErr := materializeDesiredProfile(desiredProfile)
			if profileErr != nil {
				errs = append(errs, fmt.Errorf("出口配置%s恢复失败: %w", active[i].ProfileID, profileErr))
				continue
			}
			if profileRequest.ID != active[i].ProfileID {
				errs = append(errs, fmt.Errorf("出口配置%s的控制端标识不一致", active[i].ProfileID))
				continue
			}
			if profileErr = validateEgressProfileTransport(node, &profileRequest); profileErr != nil {
				errs = append(errs, fmt.Errorf("出口配置%s恢复失败: %w", active[i].ProfileID, profileErr))
				continue
			}
			profileRequestsByID[active[i].ProfileID] = profileRequest
			profileOrder = append(profileOrder, active[i].ProfileID)
		}

		explicitSources, sourceErr := desiredExplicitEgressSources(&active[i], instance)
		if sourceErr != nil {
			errs = append(errs, fmt.Errorf("实例%d出口源地址恢复失败: %w", instance.ID, sourceErr))
			continue
		}
		derivedSources, sourceErr := instanceEgressSources(instance, node)
		if sourceErr != nil {
			errs = append(errs, fmt.Errorf("实例%d出口源地址恢复失败: %w", instance.ID, sourceErr))
			continue
		}
		completeSources, sourceErr := mergeInstanceEgressSources(explicitSources, derivedSources, node)
		if sourceErr != nil || len(completeSources) == 0 {
			if sourceErr == nil {
				sourceErr = fmt.Errorf("实例源地址不能为空")
			}
			errs = append(errs, fmt.Errorf("实例%d出口源地址恢复失败: %w", instance.ID, sourceErr))
			continue
		}
		active[i].InstanceKey = instanceEgressKey(instance)
		active[i].SourcesJSON = string(mustJSON(completeSources))
		active[i].ExplicitSourcesJSON = string(mustJSON(explicitSources))
		active[i].UpdatedAt = time.Now()
		if derivedV4, derivedV6 := instanceEgressInterfaces(instance, monitorsByInstanceID[instance.ID]); derivedV4 != nil || derivedV6 != nil {
			if derivedV4 != nil {
				active[i].InterfaceV4 = *derivedV4
			}
			if derivedV6 != nil {
				active[i].InterfaceV6 = *derivedV6
			}
		}
		bindingRequest, bindingErr := desiredBindingRequest(&active[i])
		if bindingErr != nil {
			errs = append(errs, fmt.Errorf("实例%d出口绑定恢复失败: %w", instance.ID, bindingErr))
			continue
		}
		bindingRequests = append(bindingRequests, bindingRequest)
	}
	if err := errors.Join(errs...); err != nil {
		return 0, err
	}

	profileRequests := make([]EgressProfileRequest, 0, len(profileOrder))
	for _, profileID := range profileOrder {
		profileRequests = append(profileRequests, profileRequestsByID[profileID])
	}
	if len(active) > 0 {
		result := s.db.WithContext(ctx).Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"instance_key", "sources_json", "explicit_sources_json",
				"interface", "interface_v4", "interface_v6", "updated_at",
			}),
		}).CreateInBatches(&active, 500)
		if result.Error != nil {
			return 0, fmt.Errorf("批量更新控制端出口期望状态失败: %w", result.Error)
		}
	}

	client, err := egressClient(node, config)
	if err != nil {
		return 0, err
	}
	response, err := client.ReplaceEgressState(EgressStateRequest{
		Profiles: profileRequests,
		Bindings: bindingRequests,
		Apply:    apply,
	})
	if err != nil {
		return 0, fmt.Errorf("批量恢复节点出口状态失败: %w", err)
	}
	if err := validateReplacedEgressState(response, len(profileRequests), len(bindingRequests), apply); err != nil {
		return 0, err
	}

	if apply {
		activeProfileIDs := make([]string, 0, len(profileOrder))
		activeProfileIDs = append(activeProfileIDs, profileOrder...)
		if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if len(pending) > 0 {
				pendingIDs := make([]uint, 0, len(pending))
				for i := range pending {
					pendingIDs = append(pendingIDs, pending[i].ID)
				}
				if err := tx.Where("id IN ?", pendingIDs).
					Delete(&monitoringModel.EgressDesiredBinding{}).Error; err != nil {
					return err
				}
			}
			profiles := tx.Where("provider_id = ?", providerID)
			if len(activeProfileIDs) > 0 {
				profiles = profiles.Where("profile_id NOT IN ?", activeProfileIDs)
			}
			return profiles.Delete(&monitoringModel.EgressDesiredProfile{}).Error
		}); err != nil {
			return 0, fmt.Errorf("清理已确认的控制端出口待删除状态失败: %w", err)
		}
	}
	return len(bindings), nil
}

// CleanupProviderEgress removes all egress state owned by one provider before
// the controller deletes that provider. Remote work is deliberately performed
// outside the provider database transaction: a failed Agent cleanup leaves the
// authoritative desired rows available for a later retry or manual recovery.
func (s *InstanceEgressService) CleanupProviderEgress(ctx context.Context, providerID uint) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("数据库连接不可用")
	}
	if providerID == 0 {
		return fmt.Errorf("ProviderID不能为空")
	}

	var desiredBindings []monitoringModel.EgressDesiredBinding
	if err := s.db.WithContext(ctx).
		Where("provider_id = ?", providerID).
		Order("id ASC").
		Find(&desiredBindings).Error; err != nil {
		return fmt.Errorf("读取Provider出口绑定失败: %w", err)
	}
	var desiredProfiles []monitoringModel.EgressDesiredProfile
	if err := s.db.WithContext(ctx).
		Where("provider_id = ?", providerID).
		Order("id ASC").
		Find(&desiredProfiles).Error; err != nil {
		return fmt.Errorf("读取Provider出口配置失败: %w", err)
	}
	if len(desiredBindings) == 0 && len(desiredProfiles) == 0 {
		return nil
	}

	node, config, err := s.loadProviderContext(ctx, providerID)
	if err != nil {
		return fmt.Errorf("读取Provider Agent配置失败: %w", err)
	}
	client, err := egressClient(node, config)
	if err != nil {
		return err
	}

	response, err := client.ReplaceEgressState(EgressStateRequest{
		Profiles: []EgressProfileRequest{},
		Bindings: []EgressBindingRequest{},
		Apply:    true,
	})
	if err != nil {
		return fmt.Errorf("批量清理节点出口状态失败: %w", err)
	}
	if err := validateReplacedEgressState(response, 0, 0, true); err != nil {
		return fmt.Errorf("节点出口状态清理未生效: %w", err)
	}
	return nil
}
