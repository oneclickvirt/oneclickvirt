package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	providerService "oneclickvirt/service/provider"

	"gorm.io/gorm"
)

func (s *InstanceEgressService) GetStatus(ctx context.Context, instanceID uint) (*InstanceEgressStatus, error) {
	instance, node, config, err := s.loadContext(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	status := &InstanceEgressStatus{
		InstanceID:          instance.ID,
		InstanceKey:         instanceEgressKey(instance),
		ProviderID:          node.ID,
		ProviderType:        node.Type,
		AgentInstalled:      config.AgentInstalled || node.IsReverseAgent(),
		ConfiguredProfileID: instance.EgressProfileID,
		Profiles:            []EgressProfile{},
		Traffic:             s.loadTraffic(ctx, instance.ID, node.ID),
	}
	if len(status.Traffic.Interfaces) > 0 && strings.TrimSpace(instance.PmacctInterfaceV4) == "" && strings.TrimSpace(instance.PmacctInterfaceV6) == "" {
		instance.PmacctInterfaceV4 = status.Traffic.Interfaces[0]
	}
	status.NativeSupported, status.RecommendedMode, status.UnsupportedReasons = deriveEgressCapabilities(instance, node)

	var controllerProfile *EgressProfile
	var desiredBinding monitoringModel.EgressDesiredBinding
	bindingErr := s.db.WithContext(ctx).Where("instance_id = ? AND pending_delete = ?", instance.ID, false).First(&desiredBinding).Error
	if bindingErr != nil && !errors.Is(bindingErr, gorm.ErrRecordNotFound) {
		return nil, bindingErr
	}
	if bindingErr == nil {
		binding, bindingErr := desiredBindingResponse(&desiredBinding)
		if bindingErr != nil {
			return nil, bindingErr
		}
		status.Binding = binding
		var desiredProfile monitoringModel.EgressDesiredProfile
		if err := s.db.WithContext(ctx).
			Where("provider_id = ? AND profile_id = ?", node.ID, desiredBinding.ProfileID).
			First(&desiredProfile).Error; err != nil {
			return nil, err
		}
		controllerProfile, err = desiredProfileResponse(&desiredProfile)
		if err != nil {
			return nil, err
		}
		status.Profiles = append(status.Profiles, *controllerProfile)
	}
	enrichEffectiveEgress(status)

	client, err := egressClient(node, config)
	if err != nil {
		status.AgentError = err.Error()
		return status, nil
	}
	capabilities, err := client.GetEgressCapabilities()
	if err != nil {
		status.AgentError = err.Error()
		return status, nil
	}
	status.AgentConnected = true
	status.Capabilities = capabilities
	profiles, err := client.ListEgressProfiles()
	if err != nil {
		status.AgentError = err.Error()
		return status, nil
	}
	status.Profiles = profiles.Profiles
	if controllerProfile != nil {
		found := false
		for i := range status.Profiles {
			if status.Profiles[i].ID == controllerProfile.ID {
				found = true
				break
			}
		}
		if !found {
			status.Profiles = append(status.Profiles, *controllerProfile)
		}
	}
	bindings, err := client.ListEgressBindings()
	if err != nil {
		status.AgentError = err.Error()
		return status, nil
	}
	for i := range bindings.Bindings {
		if bindings.Bindings[i].InstanceID == status.InstanceKey {
			binding := bindings.Bindings[i]
			status.Binding = &binding
			break
		}
	}
	applyBindingTraffic(status)
	enrichEffectiveEgress(status)
	return status, nil
}

func (s *InstanceEgressService) Bind(ctx context.Context, instanceID uint, req InstanceEgressBindRequest) (*InstanceEgressBindResult, error) {
	instance, node, config, err := s.loadContext(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	// Resolve defaults from controller-side instance/monitor metadata once. A
	// caller may still provide explicit values for unusual bridge layouts.
	var monitor monitoringModel.AgentMonitor
	if monitorErr := s.db.WithContext(ctx).
		Where("instance_id = ? AND provider_id = ? AND is_enabled = ?", instance.ID, node.ID, true).
		First(&monitor).Error; monitorErr != nil {
		monitor = monitoringModel.AgentMonitor{}
	}
	if req.InterfaceV4 == nil && req.InterfaceV6 == nil && req.Interface == nil {
		req.InterfaceV4, req.InterfaceV6 = instanceEgressInterfaces(instance, &monitor)
		if req.InterfaceV4 == nil || (isIPv6Capable(instance.NetworkType) && req.InterfaceV6 == nil) {
			// One bounded discovery pass is allowed when neither the instance
			// cache nor the local monitor knows the host attachment.
			if providerInstance, providerErr := providerService.GetProviderInstanceByID(node.ID); providerErr == nil {
				detectCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
				_ = DetectAndSaveInstanceInterfaces(detectCtx, s.db, providerInstance, instance, "")
				cancel()
				_ = s.db.WithContext(ctx).First(instance, instance.ID).Error
				req.InterfaceV4, req.InterfaceV6 = instanceEgressInterfaces(instance, &monitor)
			}
		}
	}
	if req.Interface != nil && strings.TrimSpace(*req.Interface) != "" && req.InterfaceV4 == nil && req.InterfaceV6 == nil {
		req.InterfaceV4 = req.Interface
		req.InterfaceV6 = req.Interface
	}
	if req.InterfaceV4 != nil && strings.TrimSpace(*req.InterfaceV4) != "" && strings.TrimSpace(instance.PmacctInterfaceV4) == "" {
		instance.PmacctInterfaceV4 = strings.TrimSpace(*req.InterfaceV4)
	}
	if req.InterfaceV6 != nil && strings.TrimSpace(*req.InterfaceV6) != "" && strings.TrimSpace(instance.PmacctInterfaceV6) == "" {
		instance.PmacctInterfaceV6 = strings.TrimSpace(*req.InterfaceV6)
	}
	explicitValues := append([]string(nil), req.Sources...)
	if strings.TrimSpace(req.Source) != "" {
		explicitValues = append(explicitValues, req.Source)
	}
	explicitSources, err := normalizeBindingSources(explicitValues)
	if err != nil {
		return nil, err
	}
	derivedSources, err := instanceEgressSources(instance, node)
	if err != nil {
		return nil, err
	}
	completeSources, err := mergeInstanceEgressSources(explicitSources, derivedSources, node)
	if err != nil {
		return nil, err
	}
	req.Source = ""
	req.Sources = completeSources
	req.explicitSources = explicitSources
	if err := ValidateInstanceEgressBindRequest(&req); err != nil {
		return nil, err
	}
	if supported, recommended, reasons := deriveEgressCapabilities(instance, node); req.Profile.Mode == "native" && !supported {
		detail := strings.Join(reasons, "; ")
		return nil, fmt.Errorf("当前实例不支持native出口模式，建议使用%s: %s", recommended, detail)
	}
	if err := s.rejectPersistedManagedWireGuard(ctx, node, &req); err != nil {
		return nil, err
	}
	desiredProfile, desiredBinding, err := s.persistDesiredState(ctx, instance, node, &req)
	if err != nil {
		return nil, err
	}
	profileRequest, err := materializeDesiredProfile(desiredProfile)
	if err != nil {
		return nil, err
	}
	if err := validateEgressProfileTransport(node, &profileRequest); err != nil {
		return nil, err
	}
	profileFallback, err := desiredProfileResponse(desiredProfile)
	if err != nil {
		return nil, err
	}
	bindingRequest, err := desiredBindingRequest(desiredBinding)
	if err != nil {
		return nil, err
	}
	bindingFallback, err := desiredBindingResponse(desiredBinding)
	if err != nil {
		return nil, err
	}
	result := &InstanceEgressBindResult{Profile: profileFallback, Binding: bindingFallback}
	client, err := egressClient(node, config)
	if err != nil {
		result.ReconcileError = err.Error()
		return result, nil
	}
	profile, err := client.PutEgressProfile(profileRequest)
	if err != nil {
		result.ReconcileError = fmt.Sprintf("保存节点出口配置失败: %v", err)
		return result, nil
	}
	result.Profile = profile
	bindingRequest.ProfileID = profile.ID
	binding, err := client.PutEgressBinding(bindingRequest)
	if err != nil {
		result.ReconcileError = fmt.Sprintf("绑定实例出口失败: %v", err)
		return result, nil
	}
	result.Binding = binding
	reconcile, reconcileErr := client.ReconcileEgress(egressApplyRequested(req.Apply))
	if reconcileErr != nil {
		result.ReconcileError = reconcileErr.Error()
		return result, nil
	}
	result.Reconcile = reconcile
	return result, nil
}

func (s *InstanceEgressService) Unbind(ctx context.Context, instanceID uint, apply bool) (*InstanceEgressUnbindResult, error) {
	instance, node, config, err := s.loadContext(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	var desired monitoringModel.EgressDesiredBinding
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		bindingErr := tx.Where("instance_id = ?", instance.ID).First(&desired).Error
		if bindingErr != nil && !errors.Is(bindingErr, gorm.ErrRecordNotFound) {
			return bindingErr
		}
		if bindingErr == nil {
			desired.PendingDelete = true
			desired.Enabled = false
			if err := tx.Save(&desired).Error; err != nil {
				return err
			}
		}
		return tx.Model(&providerModel.Instance{}).Where("id = ?", instance.ID).
			Update("egress_profile_id", "").Error
	})
	if err != nil {
		return nil, fmt.Errorf("记录实例出口清理状态失败: %w", err)
	}
	result := &InstanceEgressUnbindResult{}
	client, err := egressClient(node, config)
	if err != nil {
		result.ReconcileError = err.Error()
		return result, nil
	}
	if err := client.DeleteEgressBinding(instanceEgressKey(instance)); err != nil {
		result.ReconcileError = fmt.Sprintf("解除实例出口绑定失败: %v", err)
		return result, nil
	}
	reconcile, reconcileErr := client.ReconcileEgress(apply)
	if reconcileErr != nil {
		result.ReconcileError = reconcileErr.Error()
		return result, nil
	}
	result.Reconcile = reconcile
	if desired.ID != 0 {
		if err := s.finalizeBindingDeletion(ctx, client, &desired); err != nil {
			result.ReconcileError = err.Error()
		}
	}
	return result, nil
}

func (s *InstanceEgressService) finalizeBindingDeletion(ctx context.Context, client *Client, desired *monitoringModel.EgressDesiredBinding) error {
	var references int64
	if err := s.db.WithContext(ctx).Model(&monitoringModel.EgressDesiredBinding{}).
		Where("provider_id = ? AND profile_id = ? AND pending_delete = ? AND id <> ?", desired.ProviderID, desired.ProfileID, false, desired.ID).
		Count(&references).Error; err != nil {
		return err
	}
	if references == 0 {
		if err := client.DeleteEgressProfile(desired.ProfileID); err != nil {
			return fmt.Errorf("节点出口配置垃圾回收失败: %w", err)
		}
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&monitoringModel.EgressDesiredBinding{}, desired.ID).Error; err != nil {
			return fmt.Errorf("清除控制端出口绑定失败: %w", err)
		}
		if references == 0 {
			return tx.Where("provider_id = ? AND profile_id = ?", desired.ProviderID, desired.ProfileID).
				Delete(&monitoringModel.EgressDesiredProfile{}).Error
		}
		return nil
	})
}

func (s *InstanceEgressService) replayPendingBindingDeletion(ctx context.Context, desired *monitoringModel.EgressDesiredBinding, apply bool) (*EgressReconcileResponse, error) {
	if desired == nil || !desired.PendingDelete {
		return nil, fmt.Errorf("出口绑定不是待清理状态")
	}
	node, config, err := s.loadProviderContext(ctx, desired.ProviderID)
	if err != nil {
		return nil, err
	}
	client, err := egressClient(node, config)
	if err != nil {
		return nil, err
	}
	if err := client.DeleteEgressBinding(desired.InstanceKey); err != nil {
		return nil, err
	}
	reconcile, err := client.ReconcileEgress(apply)
	if err != nil {
		return nil, err
	}
	if err := s.finalizeBindingDeletion(ctx, client, desired); err != nil {
		return reconcile, err
	}
	return reconcile, nil
}

func (s *InstanceEgressService) Reconcile(ctx context.Context, instanceID uint, apply bool) (*InstanceEgressReconcileResult, error) {
	reconcile, err := s.refreshBinding(ctx, instanceID, apply)
	if err != nil {
		return nil, err
	}
	if reconcile == nil {
		_, node, config, loadErr := s.loadContext(ctx, instanceID)
		if loadErr != nil {
			return nil, loadErr
		}
		client, clientErr := egressClient(node, config)
		if clientErr != nil {
			return nil, clientErr
		}
		reconcile, err = client.ReconcileEgress(apply)
		if err != nil {
			return nil, err
		}
	}
	return &InstanceEgressReconcileResult{Reconcile: reconcile}, nil
}

// RefreshBinding re-derives the source/interface after a lifecycle event and
// then reconciles the desired route. It performs bounded Agent calls and no
// remote work while holding a database transaction.
func (s *InstanceEgressService) RefreshBinding(ctx context.Context, instanceID uint, apply bool) error {
	_, err := s.refreshBinding(ctx, instanceID, apply)
	return err
}

func (s *InstanceEgressService) refreshBinding(ctx context.Context, instanceID uint, apply bool) (*EgressReconcileResponse, error) {
	var desiredBinding monitoringModel.EgressDesiredBinding
	desiredErr := s.db.WithContext(ctx).Where("instance_id = ?", instanceID).First(&desiredBinding).Error
	if desiredErr == nil && desiredBinding.PendingDelete {
		return s.replayPendingBindingDeletion(ctx, &desiredBinding, apply)
	}
	instance, node, config, err := s.loadContext(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	if desiredErr != nil {
		if errors.Is(desiredErr, gorm.ErrRecordNotFound) && strings.TrimSpace(instance.EgressProfileID) == "" {
			return nil, nil
		}
		if errors.Is(desiredErr, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("实例缺少可恢复的控制端出口期望状态")
		}
		return nil, desiredErr
	}
	client, err := egressClient(node, config)
	if err != nil {
		return nil, err
	}
	var desiredProfile monitoringModel.EgressDesiredProfile
	if err := s.db.WithContext(ctx).
		Where("provider_id = ? AND profile_id = ?", node.ID, desiredBinding.ProfileID).
		First(&desiredProfile).Error; err != nil {
		return nil, err
	}
	profileRequest, err := materializeDesiredProfile(&desiredProfile)
	if err != nil {
		return nil, err
	}
	if err := validateEgressProfileTransport(node, &profileRequest); err != nil {
		return nil, err
	}
	var monitor monitoringModel.AgentMonitor
	_ = s.db.WithContext(ctx).
		Where("instance_id = ? AND provider_id = ? AND is_enabled = ?", instance.ID, node.ID, true).
		First(&monitor).Error
	explicitSources, err := desiredExplicitEgressSources(&desiredBinding, instance)
	if err != nil {
		return nil, err
	}
	derivedSources, err := instanceEgressSources(instance, node)
	if err != nil {
		return nil, err
	}
	completeSources, err := mergeInstanceEgressSources(explicitSources, derivedSources, node)
	if err != nil {
		return nil, err
	}
	if len(completeSources) == 0 {
		return nil, fmt.Errorf("实例源地址不能为空")
	}
	desiredBinding.SourcesJSON = string(mustJSON(completeSources))
	desiredBinding.ExplicitSourcesJSON = string(mustJSON(explicitSources))
	if derivedV4, derivedV6 := instanceEgressInterfaces(instance, &monitor); derivedV4 != nil || derivedV6 != nil {
		if derivedV4 != nil {
			desiredBinding.InterfaceV4 = *derivedV4
		}
		if derivedV6 != nil {
			desiredBinding.InterfaceV6 = *derivedV6
		}
	}
	if err := s.db.WithContext(ctx).Model(&monitoringModel.EgressDesiredBinding{}).
		Where("id = ?", desiredBinding.ID).
		Updates(map[string]interface{}{
			"sources_json":          desiredBinding.SourcesJSON,
			"explicit_sources_json": desiredBinding.ExplicitSourcesJSON,
			"interface":             desiredBinding.Interface,
			"interface_v4":          desiredBinding.InterfaceV4,
			"interface_v6":          desiredBinding.InterfaceV6,
		}).Error; err != nil {
		return nil, err
	}
	bindingRequest, err := desiredBindingRequest(&desiredBinding)
	if err != nil {
		return nil, err
	}
	if _, err := client.PutEgressProfile(profileRequest); err != nil {
		return nil, fmt.Errorf("恢复节点出口配置失败: %w", err)
	}
	if _, err := client.PutEgressBinding(bindingRequest); err != nil {
		return nil, fmt.Errorf("恢复节点出口绑定失败: %w", err)
	}
	return client.ReconcileEgress(apply)
}
