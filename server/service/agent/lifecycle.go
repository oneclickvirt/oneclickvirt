package agent

import (
	"context"
	"fmt"

	"oneclickvirt/global"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	providerService "oneclickvirt/service/provider"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// OnInstanceCreated is called after an instance is successfully created and running.
// It registers an agent monitor if agent monitoring is enabled for the provider.
func OnInstanceCreated(ctx context.Context, db *gorm.DB, instanceID uint) {
	var instance providerModel.Instance
	if err := db.WithContext(ctx).First(&instance, instanceID).Error; err != nil {
		if global.APP_LOG != nil {
			global.APP_LOG.Warn("agent lifecycle: instance not found for monitoring",
				zap.Uint("instance_id", instanceID), zap.Error(err))
		}
		return
	}

	config, err := GetMonitoringConfig(db.WithContext(ctx), instance.ProviderID)
	if err != nil || config.MonitoringMode != "agent" || !config.AgentInstalled {
		return
	}

	providerInstance, err := providerService.GetProviderInstanceByID(instance.ProviderID)
	if err != nil {
		if global.APP_LOG != nil {
			global.APP_LOG.Warn("agent lifecycle: provider not connected",
				zap.Uint("provider_id", instance.ProviderID), zap.Error(err))
		}
		return
	}

	svc := NewMonitorService(ctx, db)
	monitor, err := svc.RegisterMonitor(providerInstance, &instance, config, "")
	if err != nil {
		if global.APP_LOG != nil {
			global.APP_LOG.Warn("agent lifecycle: failed to register monitor on instance create",
				zap.Uint("instance_id", instanceID), zap.Error(err))
		}
		return
	}

	// Also update the instance's PmacctInterfaceV4/V6 fields from detected interfaces
	updateInstanceInterfaces(ctx, db, &instance, monitor)
}

// OnInstanceDeleted is called when an instance is being deleted.
// It deregisters the agent monitor but keeps the DB record (soft approach - caller can hard-delete).
func OnInstanceDeleted(ctx context.Context, db *gorm.DB, instanceID uint) {
	var instance providerModel.Instance
	if err := db.WithContext(ctx).Unscoped().First(&instance, instanceID).Error; err != nil {
		return
	}

	config, err := GetMonitoringConfig(db.WithContext(ctx), instance.ProviderID)
	if err != nil || config.MonitoringMode != "agent" || !config.AgentInstalled {
		return
	}

	svc := NewMonitorService(ctx, db)
	if err := svc.DeregisterMonitor(instanceID, config); err != nil {
		if global.APP_LOG != nil {
			global.APP_LOG.Warn("agent lifecycle: failed to deregister monitor on instance delete",
				zap.Uint("instance_id", instanceID), zap.Error(err))
		}
	}
}

// OnInstanceRebuilt is called after an instance is rebuilt (new container, new interfaces).
// It updates the monitor interfaces on the agent.
func OnInstanceRebuilt(ctx context.Context, db *gorm.DB, instanceID uint) {
	var instance providerModel.Instance
	if err := db.WithContext(ctx).First(&instance, instanceID).Error; err != nil {
		return
	}

	config, err := GetMonitoringConfig(db.WithContext(ctx), instance.ProviderID)
	if err != nil || config.MonitoringMode != "agent" || !config.AgentInstalled {
		return
	}

	providerInstance, err := providerService.GetProviderInstanceByID(instance.ProviderID)
	if err != nil {
		return
	}

	svc := NewMonitorService(ctx, db)
	if err := svc.UpdateMonitorInterfaces(providerInstance, &instance, config, ""); err != nil {
		if global.APP_LOG != nil {
			global.APP_LOG.Warn("agent lifecycle: failed to update monitor on instance rebuild",
				zap.Uint("instance_id", instanceID), zap.Error(err))
		}
		return
	}

	// Update instance interface fields
	monitor, err := svc.GetMonitorByInstanceID(instanceID)
	if err == nil && monitor != nil {
		updateInstanceInterfaces(ctx, db, &instance, monitor)
	}
}

// OnInstanceStarted is called after an instance is started.
// Same as OnInstanceCreated but also handles re-detection of interfaces.
func OnInstanceStarted(ctx context.Context, db *gorm.DB, instanceID uint) {
	var instance providerModel.Instance
	if err := db.WithContext(ctx).First(&instance, instanceID).Error; err != nil {
		return
	}

	config, err := GetMonitoringConfig(db.WithContext(ctx), instance.ProviderID)
	if err != nil || config.MonitoringMode != "agent" || !config.AgentInstalled {
		return
	}

	providerInstance, err := providerService.GetProviderInstanceByID(instance.ProviderID)
	if err != nil {
		return
	}

	svc := NewMonitorService(ctx, db)

	// Check if monitor already exists
	existing, _ := svc.GetMonitorByInstanceID(instanceID)
	if existing != nil {
		// Update interfaces (may have changed after restart)
		if err := svc.UpdateMonitorInterfaces(providerInstance, &instance, config, ""); err != nil {
			if global.APP_LOG != nil {
				global.APP_LOG.Warn("agent lifecycle: failed to update monitor on instance start",
					zap.Uint("instance_id", instanceID), zap.Error(err))
			}
		}
		// Re-read monitor to get updated interfaces
		updateInstanceInterfaces(ctx, db, &instance, existing)
		return
	}

	// Register new monitor
	monitor, err := svc.RegisterMonitor(providerInstance, &instance, config, "")
	if err != nil {
		if global.APP_LOG != nil {
			global.APP_LOG.Warn("agent lifecycle: failed to register monitor on instance start",
				zap.Uint("instance_id", instanceID), zap.Error(err))
		}
		return
	}

	updateInstanceInterfaces(ctx, db, &instance, monitor)
}

// updateInstanceInterfaces updates the PmacctInterfaceV4/V6 fields on the instance
// from the detected agent monitor interfaces. This ensures the DB always reflects
// the current network interfaces regardless of monitoring mode.
func updateInstanceInterfaces(ctx context.Context, db *gorm.DB, instance *providerModel.Instance, monitor *monitoringModel.AgentMonitor) {
	if monitor == nil || monitor.Interfaces == "" {
		return
	}

	updates := map[string]interface{}{}
	interfaces := monitor.Interfaces

	// The first interface is typically IPv4's veth, set it as PmacctInterfaceV4
	if instance.PmacctInterfaceV4 != interfaces {
		updates["pmacct_interface_v4"] = interfaces
	}

	if len(updates) > 0 {
		if err := db.WithContext(ctx).Model(instance).Updates(updates).Error; err != nil {
			if global.APP_LOG != nil {
				global.APP_LOG.Warn("failed to update instance interfaces",
					zap.Uint("instance_id", instance.ID),
					zap.Error(err))
			}
		} else {
			if global.APP_LOG != nil {
				global.APP_LOG.Debug("updated instance network interfaces",
					zap.Uint("instance_id", instance.ID),
					zap.String("interfaces", fmt.Sprintf("%v", updates)))
			}
		}
	}
}
