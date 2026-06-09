package agent

import (
	"context"
	"strings"

	"oneclickvirt/global"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	providerService "oneclickvirt/service/provider"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// OnInstanceCreated is called after an instance is successfully created and running.
// It always tries to detect network interfaces, and also registers an agent monitor
// if agent monitoring is enabled for the provider.
func OnInstanceCreated(ctx context.Context, db *gorm.DB, instanceID uint) {
	var instance providerModel.Instance
	if err := db.WithContext(ctx).First(&instance, instanceID).Error; err != nil {
		if global.APP_LOG != nil {
			global.APP_LOG.Warn("agent lifecycle: instance not found",
				zap.Uint("instance_id", instanceID), zap.Error(err))
		}
		return
	}

	// Get the connected provider; needed for both interface detection and monitoring.
	providerInstance, provErr := providerService.GetProviderInstanceByID(instance.ProviderID)

	// Always detect and save network interfaces regardless of monitoring mode.
	if provErr == nil {
		if err := DetectAndSaveInstanceInterfaces(ctx, db, providerInstance, &instance, ""); err != nil {
			if global.APP_LOG != nil {
				global.APP_LOG.Warn("agent lifecycle: failed to detect interfaces on instance create",
					zap.Uint("instance_id", instanceID), zap.Error(err))
			}
		}
	} else {
		if global.APP_LOG != nil {
			global.APP_LOG.Warn("agent lifecycle: provider not connected for interface detection",
				zap.Uint("provider_id", instance.ProviderID), zap.Error(provErr))
		}
	}

	// 刷新控制端端口转发的 InternalHost（实例首次创建后IP已就绪）
	go refreshControllerPortHosts(db, instanceID)

	// Monitoring registration requires agent to be installed.
	config, err := GetMonitoringConfig(db.WithContext(ctx), instance.ProviderID)
	if err != nil || config.MonitoringMode != "agent" || !config.AgentInstalled {
		return
	}
	if provErr != nil {
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

	// Re-read updated instance from DB then sync interfaces from monitor.
	_ = db.WithContext(ctx).First(&instance, instanceID)
	updateInstanceInterfaces(ctx, db, &instance, monitor)
}

// OnInstanceDeleted is called when an instance is being deleted.
// It deregisters the agent monitor if one exists.
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
// Always re-detects interfaces; also updates the agent monitor if monitoring is enabled.
func OnInstanceRebuilt(ctx context.Context, db *gorm.DB, instanceID uint) {
	var instance providerModel.Instance
	if err := db.WithContext(ctx).First(&instance, instanceID).Error; err != nil {
		return
	}

	providerInstance, provErr := providerService.GetProviderInstanceByID(instance.ProviderID)

	// Always re-detect interfaces.
	if provErr == nil {
		if err := DetectAndSaveInstanceInterfaces(ctx, db, providerInstance, &instance, ""); err != nil {
			if global.APP_LOG != nil {
				global.APP_LOG.Warn("agent lifecycle: failed to detect interfaces on instance rebuild",
					zap.Uint("instance_id", instanceID), zap.Error(err))
			}
		}
	}

	// 刷新控制端端口转发的 InternalHost（实例重建后IP可能已变更）
	go refreshControllerPortHosts(db, instanceID)

	// Update the agent monitor if monitoring is configured.
	config, err := GetMonitoringConfig(db.WithContext(ctx), instance.ProviderID)
	if err != nil || config.MonitoringMode != "agent" || !config.AgentInstalled || provErr != nil {
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

	// Sync interfaces from the updated monitor record.
	monitor, err := svc.GetMonitorByInstanceID(instanceID)
	if err == nil && monitor != nil {
		_ = db.WithContext(ctx).First(&instance, instanceID)
		updateInstanceInterfaces(ctx, db, &instance, monitor)
	}
}

// OnInstanceStarted is called after an instance is started.
// Always re-detects interfaces; also registers/updates the agent monitor if enabled.
func OnInstanceStarted(ctx context.Context, db *gorm.DB, instanceID uint) {
	var instance providerModel.Instance
	if err := db.WithContext(ctx).First(&instance, instanceID).Error; err != nil {
		return
	}

	// 刷新控制端端口转发的 InternalHost（实例重启后IP可能已变更）
	go refreshControllerPortHosts(db, instanceID)

	providerInstance, provErr := providerService.GetProviderInstanceByID(instance.ProviderID)

	// Always re-detect interfaces on start (veth name may change after restart).
	if provErr == nil {
		if err := DetectAndSaveInstanceInterfaces(ctx, db, providerInstance, &instance, ""); err != nil {
			if global.APP_LOG != nil {
				global.APP_LOG.Warn("agent lifecycle: failed to detect interfaces on instance start",
					zap.Uint("instance_id", instanceID), zap.Error(err))
			}
		}
	}

	config, err := GetMonitoringConfig(db.WithContext(ctx), instance.ProviderID)
	if err != nil || config.MonitoringMode != "agent" || !config.AgentInstalled || provErr != nil {
		return
	}

	svc := NewMonitorService(ctx, db)

	existing, _ := svc.GetMonitorByInstanceID(instanceID)
	if existing != nil {
		if err := svc.UpdateMonitorInterfaces(providerInstance, &instance, config, ""); err != nil {
			if global.APP_LOG != nil {
				global.APP_LOG.Warn("agent lifecycle: failed to update monitor on instance start",
					zap.Uint("instance_id", instanceID), zap.Error(err))
			}
		}
		// Re-read updated monitor and sync interfaces.
		if updated, err := svc.GetMonitorByInstanceID(instanceID); err == nil && updated != nil {
			_ = db.WithContext(ctx).First(&instance, instanceID)
			updateInstanceInterfaces(ctx, db, &instance, updated)
		}
		return
	}

	monitor, err := svc.RegisterMonitor(providerInstance, &instance, config, "")
	if err != nil {
		if global.APP_LOG != nil {
			global.APP_LOG.Warn("agent lifecycle: failed to register monitor on instance start",
				zap.Uint("instance_id", instanceID), zap.Error(err))
		}
		return
	}

	_ = db.WithContext(ctx).First(&instance, instanceID)
	updateInstanceInterfaces(ctx, db, &instance, monitor)
}

// updateInstanceInterfaces updates the PmacctInterfaceV4/V6 fields on the instance
// from the detected agent monitor interfaces. monitor.Interfaces is a comma-separated
// list where index 0 is the IPv4 interface and index 1 (if present) is the IPv6 interface.
func updateInstanceInterfaces(ctx context.Context, db *gorm.DB, instance *providerModel.Instance, monitor *monitoringModel.AgentMonitor) {
	if monitor == nil || monitor.Interfaces == "" {
		return
	}

	parts := strings.Split(monitor.Interfaces, ",")
	updates := map[string]interface{}{}

	ifaceV4 := strings.TrimSpace(parts[0])
	if ifaceV4 != "" && instance.PmacctInterfaceV4 != ifaceV4 {
		updates["pmacct_interface_v4"] = ifaceV4
	}

	// Only set V6 from an explicit second interface entry in the monitor.
	// Do NOT fall back to V4 here: DetectAndSaveInstanceInterfaces is the
	// authoritative path for V6 and may have already stored a distinct veth.
	// Overwriting with V4 would corrupt the V6 field for IPv6-capable instances.
	if len(parts) >= 2 {
		ifaceV6 := strings.TrimSpace(parts[1])
		if instance.PmacctInterfaceV6 != ifaceV6 {
			updates["pmacct_interface_v6"] = ifaceV6
		}
	} else if instance.PmacctInterfaceV6 != "" {
		updates["pmacct_interface_v6"] = ""
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
					zap.String("v4", ifaceV4))
			}
		}
	}
}

// refreshControllerPortHosts 刷新实例控制端端口转发的 IP 型目标地址，
// 保留显式配置的主机名，并确保每个控制端端口的 TCP 监听器正在运行。
// 实例首次创建（IP 刚就绪）或重启（IP 可能变更）时调用。
func refreshControllerPortHosts(db *gorm.DB, instanceID uint) {
	var instance providerModel.Instance
	if err := db.Select("id", "provider_id", "private_ip").First(&instance, instanceID).Error; err != nil {
		return
	}
	if instance.PrivateIP == "" {
		return
	}

	// 查询该实例的所有活跃控制端端口转发
	var ports []providerModel.Port
	if err := db.Where("instance_id = ? AND mapping_type = ? AND status = ?",
		instanceID, "controller", "active").Find(&ports).Error; err != nil {
		if global.APP_LOG != nil {
			global.APP_LOG.Warn("查询控制端端口转发失败",
				zap.Uint("instance_id", instanceID), zap.Error(err))
		}
		return
	}

	for _, port := range ports {
		targetHost, shouldUpdate := ResolveControllerPortTarget(port.InternalHost, instance.PrivateIP)
		if targetHost == "" {
			continue
		}

		if shouldUpdate {
			if err := db.Model(&providerModel.Port{}).
				Where("id = ?", port.ID).
				Update("internal_host", targetHost).Error; err != nil {
				if global.APP_LOG != nil {
					global.APP_LOG.Warn("更新控制端端口转发目标地址失败",
						zap.Uint("port_id", port.ID), zap.Error(err))
				}
			} else if global.APP_LOG != nil {
				global.APP_LOG.Debug("已更新控制端端口转发目标地址",
					zap.Uint("port_id", port.ID),
					zap.String("new_host", targetHost),
					zap.String("old_host", port.InternalHost))
			}
		}

		// 确保控制端监听器正在运行
		ctrlListenerMu.RLock()
		_, running := ctrlListeners[port.ID]
		ctrlListenerMu.RUnlock()

		if !running {
			// 监听器尚未启动（新端口或服务重启后未恢复），立即启动
			if err := StartControllerPortForward(port.ID, port.ProviderID, port.HostPort, targetHost, port.GuestPort); err != nil {
				if global.APP_LOG != nil {
					global.APP_LOG.Warn("启动控制端端口转发失败",
						zap.Uint("port_id", port.ID), zap.Error(err))
				}
			} else if global.APP_LOG != nil {
				global.APP_LOG.Info("控制端端口转发已启动",
					zap.Uint("port_id", port.ID),
					zap.Int("host_port", port.HostPort),
					zap.String("target", targetHost))
			}
		} else if shouldUpdate {
			// IP 已变更且监听器仍在运行，需要用新目标地址重启监听器
			go func(p providerModel.Port, host string) {
				StopControllerPortForward(p.ID)
				if err := StartControllerPortForward(p.ID, p.ProviderID, p.HostPort, host, p.GuestPort); err != nil {
					if global.APP_LOG != nil {
						global.APP_LOG.Warn("重启控制端端口转发失败",
							zap.Uint("port_id", p.ID), zap.Error(err))
					}
				} else if global.APP_LOG != nil {
					global.APP_LOG.Info("控制端端口转发已用新IP重启",
						zap.Uint("port_id", p.ID),
						zap.String("new_target", host))
				}
			}(port, targetHost)
		}
	}
}
