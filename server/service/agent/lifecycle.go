package agent

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"

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
	defer ScheduleProviderEgressRefresh(db, instance.ProviderID, true)

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
	// Remove the node-side binding before the instance row is soft-deleted so
	// stale source rules cannot survive a later instance-ID reuse.
	if strings.TrimSpace(instance.EgressProfileID) != "" {
		egressCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if _, err := NewInstanceEgressService(db).Unbind(egressCtx, instanceID, true); err != nil && global.APP_LOG != nil {
			global.APP_LOG.Warn("agent lifecycle: failed to clean instance egress binding",
				zap.Uint("instance_id", instanceID), zap.Error(err))
		}
		cancel()
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
	defer ScheduleProviderEgressRefresh(db, instance.ProviderID, true)

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
	defer ScheduleProviderEgressRefresh(db, instance.ProviderID, true)

	// A reboot can finish many start tasks together. Coalesce their post-start
	// address discovery *and* controller-port repair by Provider instead of
	// doing one remote discovery plus two port-table reads per task.
	ScheduleStartedProviderRuntimeNetworkRefresh(db, instance.ProviderID, instanceID)

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

// updateInstanceInterfaces updates the cached host interfaces from typed traffic bindings.
// Legacy comma-separated monitor data remains supported during rolling upgrades.
func updateInstanceInterfaces(ctx context.Context, db *gorm.DB, instance *providerModel.Instance, monitor *monitoringModel.AgentMonitor) {
	if monitor == nil {
		return
	}
	ifaceV4, ifaceV6 := resolveMonitorBindingInterfaces(instance, monitor)
	updates := map[string]interface{}{}

	if ifaceV4 != "" && instance.PmacctInterfaceV4 != ifaceV4 {
		updates["pmacct_interface_v4"] = ifaceV4
	} else if ifaceV4 == "" && instance.PmacctInterfaceV4 != "" && instance.NetworkType == "ipv6_only" {
		updates["pmacct_interface_v4"] = ""
	}

	if isIPv6Capable(instance.NetworkType) && instance.PmacctInterfaceV6 != ifaceV6 {
		updates["pmacct_interface_v6"] = ifaceV6
	} else if !isIPv6Capable(instance.NetworkType) && instance.PmacctInterfaceV6 != "" {
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
			instance.PmacctInterfaceV4 = ifaceV4
			instance.PmacctInterfaceV6 = ifaceV6
			if global.APP_LOG != nil {
				global.APP_LOG.Debug("updated instance network interfaces",
					zap.Uint("instance_id", instance.ID),
					zap.String("v4", ifaceV4),
					zap.String("v6", ifaceV6))
			}
		}
	}
}

func resolveMonitorBindingInterfaces(instance *providerModel.Instance, monitor *monitoringModel.AgentMonitor) (string, string) {
	bindings := unmarshalTrafficBindings(monitor.Bindings)
	parts := normalizeMonitorInterfaces(monitor.Interfaces)
	ifaceV4 := ""
	ifaceV6 := ""

	for _, binding := range bindings {
		for _, family := range binding.Families {
			switch family {
			case trafficFamilyIPv4:
				if ifaceV4 == "" {
					ifaceV4 = binding.Interface
				}
			case trafficFamilyIPv6:
				if ifaceV6 == "" {
					ifaceV6 = binding.Interface
				}
			}
		}
		for _, address := range binding.Addresses {
			ip := net.ParseIP(address)
			if ip == nil {
				continue
			}
			if ip.To4() != nil && ifaceV4 == "" {
				ifaceV4 = binding.Interface
			}
			if ip.To4() == nil && ifaceV6 == "" {
				ifaceV6 = binding.Interface
			}
		}
	}
	if ifaceV4 == "" && instance.NetworkType != "ipv6_only" && len(bindings) > 0 {
		ifaceV4 = bindings[0].Interface
	}
	if ifaceV6 == "" && isIPv6Capable(instance.NetworkType) && len(bindings) > 0 {
		if len(bindings) > 1 {
			ifaceV6 = bindings[1].Interface
		} else {
			ifaceV6 = bindings[0].Interface
		}
	}
	if len(bindings) == 0 {
		if len(parts) > 0 && instance.NetworkType != "ipv6_only" {
			ifaceV4 = parts[0]
		}
		if isIPv6Capable(instance.NetworkType) {
			if len(parts) > 1 {
				ifaceV6 = parts[1]
			} else if len(parts) > 0 {
				ifaceV6 = parts[0]
			}
		}
	}
	return ifaceV4, ifaceV6
}

// refreshControllerPortHosts 刷新实例控制端端口转发的 IP 型目标地址，
// 保留显式配置的主机名，并确保每个控制端端口的 TCP 监听器正在运行。
// 实例首次创建（IP 刚就绪）或重启（IP 可能变更）时调用。
func refreshControllerPortHosts(db *gorm.DB, instanceID uint) {
	refreshControllerPortHostsForInstances(db, []uint{instanceID})
}

type controllerPortHostChange struct {
	Port         providerModel.Port
	TargetHost   string
	ShouldUpdate bool
}

const (
	// Keep the two reads below SQLite/MySQL bind budgets when one reboot changes
	// every guest address on a large provider.
	controllerPortHostInstanceBatchSize = 200
	controllerPortHostUpdateBatchSize   = 50
	controllerPortRebindConcurrency     = 4
)

// refreshControllerPortHostsForInstances batches the database side of an IP
// rebind for many instances.  A node reboot can change every container's
// address; querying ports per instance here would turn one discovery into an
// avoidable N+1 query burst.  Per-port listener restarts remain necessary but
// occur only for targets that actually changed.
func refreshControllerPortHostsForInstances(db *gorm.DB, instanceIDs []uint) {
	if db == nil || len(instanceIDs) == 0 {
		return
	}
	// A Provider-wide Agent reconnect can be rebuilding every controller
	// listener at the same time. Serialize this smaller address-driven repair
	// with that stop/rebind path so one worker cannot rebind a listener that the
	// other worker is still tearing down.
	recoveryMu.Lock()
	defer recoveryMu.Unlock()
	uniqueIDs := make([]uint, 0, len(instanceIDs))
	seen := make(map[uint]struct{}, len(instanceIDs))
	for _, instanceID := range instanceIDs {
		if instanceID == 0 {
			continue
		}
		if _, exists := seen[instanceID]; exists {
			continue
		}
		seen[instanceID] = struct{}{}
		uniqueIDs = append(uniqueIDs, instanceID)
	}
	if len(uniqueIDs) == 0 {
		return
	}

	var instances []providerModel.Instance
	if err := db.Select("id", "private_ip").Where("id IN ?", uniqueIDs).Find(&instances).Error; err != nil {
		if global.APP_LOG != nil {
			global.APP_LOG.Warn("批量查询实例控制端端口目标失败", zap.Error(err))
		}
		return
	}
	privateIPs := make(map[uint]string, len(instances))
	for _, instance := range instances {
		if strings.TrimSpace(instance.PrivateIP) != "" {
			privateIPs[instance.ID] = instance.PrivateIP
		}
	}
	if len(privateIPs) == 0 {
		return
	}

	var ports []providerModel.Port
	if err := db.Where("instance_id IN ? AND mapping_type = ? AND status = ?", uniqueIDs, "controller", "active").Find(&ports).Error; err != nil {
		if global.APP_LOG != nil {
			global.APP_LOG.Warn("批量查询控制端端口转发失败", zap.Error(err))
		}
		return
	}

	changes := make([]controllerPortHostChange, 0, len(ports))
	for _, port := range ports {
		privateIP := privateIPs[port.InstanceID]
		if privateIP == "" {
			continue
		}
		targetHost, shouldUpdate := ResolveControllerPortTarget(port.InternalHost, privateIP)
		if targetHost == "" {
			continue
		}
		changes = append(changes, controllerPortHostChange{Port: port, TargetHost: targetHost, ShouldUpdate: shouldUpdate})
	}
	if len(changes) == 0 {
		return
	}

	updatedPorts := make([]controllerPortHostChange, 0, len(changes))
	for _, change := range changes {
		if change.ShouldUpdate {
			updatedPorts = append(updatedPorts, change)
		}
	}
	persistedUpdates := make(map[uint]struct{}, len(updatedPorts))
	for start := 0; start < len(updatedPorts); start += controllerPortHostUpdateBatchSize {
		end := start + controllerPortHostUpdateBatchSize
		if end > len(updatedPorts) {
			end = len(updatedPorts)
		}
		batch := updatedPorts[start:end]
		portIDs := make([]uint, 0, len(batch))
		var builder strings.Builder
		builder.WriteString("CASE id")
		args := make([]interface{}, 0, len(batch)*2)
		for _, change := range batch {
			portIDs = append(portIDs, change.Port.ID)
			builder.WriteString(" WHEN ? THEN ?")
			args = append(args, change.Port.ID, change.TargetHost)
		}
		builder.WriteString(" ELSE internal_host END")
		if err := db.Model(&providerModel.Port{}).Where("id IN ?", portIDs).
			Update("internal_host", gorm.Expr(builder.String(), args...)).Error; err != nil {
			if global.APP_LOG != nil {
				global.APP_LOG.Warn("批量更新控制端端口转发目标地址失败", zap.Error(err))
			}
			// Do not restart a listener with an unpersisted target. A future
			// periodic port repair will safely retry it after the DB recovers.
			continue
		}
		for _, change := range batch {
			persistedUpdates[change.Port.ID] = struct{}{}
		}
	}

	ready := make([]controllerPortHostChange, 0, len(changes))
	for _, change := range changes {
		if change.ShouldUpdate {
			if _, persisted := persistedUpdates[change.Port.ID]; !persisted {
				continue
			}
		}
		ready = append(ready, change)
	}
	if len(ready) == 0 {
		return
	}

	// Each listener must still be started/rebound independently, but limit that
	// inevitable local work. The known Port rows avoid a new database lookup per
	// listener after the two batched reads above.
	workers := controllerPortRebindConcurrency
	if workers > len(ready) {
		workers = len(ready)
	}
	jobs := make(chan controllerPortHostChange)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for change := range jobs {
				running := IsControllerPortForwardRunning(change.Port.ID)
				var err error
				if change.ShouldUpdate && running {
					err = restartControllerPortForwardWithKnownPort(change.Port, change.TargetHost)
				} else if change.ShouldUpdate || !running {
					err = startControllerPortForwardWithKnownPort(change.Port, change.TargetHost)
				}
				if err != nil {
					if global.APP_LOG != nil {
						global.APP_LOG.Warn("启动或重绑控制端端口转发失败",
							zap.Uint("port_id", change.Port.ID), zap.Error(err))
					}
					continue
				}
				if change.ShouldUpdate || !running {
					if global.APP_LOG != nil {
						global.APP_LOG.Info("控制端端口转发已确认",
							zap.Uint("port_id", change.Port.ID),
							zap.String("target", change.TargetHost),
							zap.Bool("rebound", change.ShouldUpdate))
					}
				}
			}
		}()
	}
	for _, change := range ready {
		jobs <- change
	}
	close(jobs)
	wg.Wait()
}
