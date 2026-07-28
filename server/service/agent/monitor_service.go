package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"oneclickvirt/global"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// MonitorService manages the mapping between instances and agent monitors.
type MonitorService struct {
	db       *gorm.DB
	ctx      context.Context
	clientMu sync.Mutex
	clients  map[uint]*Client
}

// NewMonitorService creates a new monitor service.
func NewMonitorService(ctx context.Context, db *gorm.DB) *MonitorService {
	return &MonitorService{db: db, ctx: ctx, clients: make(map[uint]*Client)}
}

// MonitorSyncSummary describes what a provider monitor synchronization changed.
type MonitorSyncSummary struct {
	Total     int      `json:"total"`
	Created   int      `json:"created"`
	Updated   int      `json:"updated"`
	Unchanged int      `json:"unchanged"`
	Deferred  int      `json:"deferred"`
	Failed    int      `json:"failed"`
	Cleaned   int      `json:"cleaned"`
	Errors    []string `json:"errors,omitempty"`
}

type monitorHealthUpdate struct {
	ID     uint
	Status string
	Error  string
}

func (s *MonitorService) updateMonitorHealthBatch(updates []monitorHealthUpdate, checkedAt time.Time) error {
	const batchSize = 200
	for start := 0; start < len(updates); start += batchSize {
		end := start + batchSize
		if end > len(updates) {
			end = len(updates)
		}
		batch := updates[start:end]
		ids := make([]uint, 0, len(batch))
		statusArgs := make([]interface{}, 0, len(batch)*2)
		errorArgs := make([]interface{}, 0, len(batch)*2)
		var statusCase strings.Builder
		var errorCase strings.Builder
		statusCase.WriteString("CASE id")
		errorCase.WriteString("CASE id")
		for _, update := range batch {
			ids = append(ids, update.ID)
			statusCase.WriteString(" WHEN ? THEN ?")
			statusArgs = append(statusArgs, update.ID, update.Status)
			errorCase.WriteString(" WHEN ? THEN ?")
			errorArgs = append(errorArgs, update.ID, update.Error)
		}
		statusCase.WriteString(" ELSE health_status END")
		errorCase.WriteString(" ELSE health_error END")
		if err := s.db.Model(&monitoringModel.AgentMonitor{}).
			Where("id IN ?", ids).
			Updates(map[string]interface{}{
				"health_status":  gorm.Expr(statusCase.String(), statusArgs...),
				"health_error":   gorm.Expr(errorCase.String(), errorArgs...),
				"last_health_at": checkedAt,
			}).Error; err != nil {
			return fmt.Errorf("batch update monitor health: %w", err)
		}
	}
	return nil
}

func normalizeMonitorInterfaces(raw string) []string {
	parts := strings.Split(raw, ",")
	interfaces := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			interfaces = append(interfaces, part)
		}
	}
	return interfaces
}

func cachedInterfacesForInstance(instance *providerModel.Instance) []string {
	interfaces := make([]string, 0, 2)
	if v4 := strings.TrimSpace(instance.PmacctInterfaceV4); v4 != "" {
		interfaces = append(interfaces, v4)
	}
	if isIPv6Capable(instance.NetworkType) {
		if v6 := strings.TrimSpace(instance.PmacctInterfaceV6); v6 != "" {
			duplicate := false
			for _, iface := range interfaces {
				if iface == v6 {
					duplicate = true
					break
				}
			}
			if !duplicate {
				interfaces = append(interfaces, v6)
			}
		}
	}
	return interfaces
}

func cachedBindingsForInstance(instance *providerModel.Instance) []TrafficBinding {
	return buildTrafficBindings(instance, &InstanceInterfaces{
		V4: strings.TrimSpace(instance.PmacctInterfaceV4),
		V6: strings.TrimSpace(instance.PmacctInterfaceV6),
	})
}

func monitorMatchesInstanceCache(
	monitor *monitoringModel.AgentMonitor,
	instance *providerModel.Instance,
	providerKind string,
	cachedBindings []TrafficBinding,
) bool {
	if len(cachedBindings) == 0 {
		return false
	}
	return monitor.ProviderID == instance.ProviderID &&
		monitor.UserID == instance.UserID &&
		monitor.ProviderKind == providerKind &&
		monitor.InstanceName == instance.Name &&
		monitor.InnerIP == bindingsLegacyInnerIP(cachedBindings) &&
		monitor.IsEnabled &&
		stringsEqual(normalizeMonitorInterfaces(monitor.Interfaces), bindingsInterfaces(cachedBindings)) &&
		trafficBindingsEqual(unmarshalTrafficBindings(monitor.Bindings), cachedBindings)
}

// getAgentClient returns an agent client for the given provider using its endpoint.
// For agent-mode providers behind NAT, the HTTP API is not directly reachable;
// the WS fallback in Client.doRequest handles connectivity via WebSocket.
func (s *MonitorService) getAgentClient(providerID uint, config *monitoringModel.MonitoringConfig) (*Client, error) {
	s.clientMu.Lock()
	defer s.clientMu.Unlock()
	if client := s.clients[providerID]; client != nil {
		return client, nil
	}
	var p providerModel.Provider
	if err := s.db.First(&p, providerID).Error; err != nil {
		return nil, fmt.Errorf("load provider %d: %w", providerID, err)
	}
	host := ResolveAgentHost(p.Endpoint, p.AgentRemoteIP)
	if host == "" {
		if p.ConnectionType == "agent" {
			host = "127.0.0.1" // loopback fallback; calls are routed through WS fallback
		} else {
			return nil, fmt.Errorf("provider %d has no endpoint", providerID)
		}
	}
	port := config.AgentPort
	if port == 0 {
		port = AgentPort
	}
	client := GetClientWithMode(providerID, host, port, config.AgentToken, p.ConnectionType == "agent")
	s.clients[providerID] = client
	return client, nil
}

// RegisterMonitor creates a monitor on the agent and saves the mapping in MySQL.
// It detects the network interfaces for the instance and calls the agent's add API.
// vmidHint is the provider-side instance ID (e.g. Proxmox VMID); pass "" to auto-detect via GetInstance.
func (s *MonitorService) RegisterMonitor(
	providerInstance provider.Provider,
	instance *providerModel.Instance,
	config *monitoringModel.MonitoringConfig,
	vmidHint string,
) (*monitoringModel.AgentMonitor, error) {
	// Check if already registered. If the agent-side record still exists, refresh
	// local metadata only; otherwise delete the stale mapping and recreate it.
	var existing monitoringModel.AgentMonitor
	if err := s.db.Where("instance_id = ?", instance.ID).First(&existing).Error; err == nil {
		client, clientErr := s.getAgentClient(instance.ProviderID, config)
		if clientErr != nil {
			return &existing, nil
		}
		if _, infoErr := client.GetInfo(existing.AgentMonitorID); infoErr == nil {
			updated, err := s.updateMonitorForInstance(providerInstance, instance, config, vmidHint, &existing)
			if err != nil {
				return nil, err
			}
			if updated {
				return s.GetMonitorByInstanceID(instance.ID)
			}
			return &existing, nil
		} else if global.APP_LOG != nil {
			global.APP_LOG.Warn("agent monitor mapping is stale, will recreate",
				zap.Uint("instance_id", instance.ID),
				zap.Int64("agent_monitor_id", existing.AgentMonitorID),
				zap.Error(infoErr))
		}
		if err := s.db.Unscoped().Delete(&existing).Error; err != nil {
			return nil, fmt.Errorf("remove stale agent monitor mapping: %w", err)
		}
	} else if err != gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("find existing agent monitor: %w", err)
	}

	return s.registerMonitorForInstance(providerInstance, instance, config, vmidHint)
}

func (s *MonitorService) registerMonitorForInstance(
	providerInstance provider.Provider,
	instance *providerModel.Instance,
	config *monitoringModel.MonitoringConfig,
	vmidHint string,
) (*monitoringModel.AgentMonitor, error) {
	interfaces, err := s.detectBothInterfaces(providerInstance, instance, vmidHint)
	if err != nil {
		return nil, fmt.Errorf("detect interfaces for instance %s: %w", instance.Name, err)
	}
	bindings := buildTrafficBindings(instance, interfaces)
	if len(bindings) == 0 {
		return nil, fmt.Errorf("no network interfaces found for instance %s", instance.Name)
	}

	client, err := s.getAgentClient(instance.ProviderID, config)
	if err != nil {
		return nil, err
	}

	providerKind := providerInstance.GetType()
	instanceName := instance.Name
	innerIP := bindingsLegacyInnerIP(bindings)

	resp, err := client.AddMonitor(bindings, providerKind, instanceName)
	if err != nil {
		return nil, fmt.Errorf("agent add monitor for %s: %w", instance.Name, err)
	}

	healthStatus := "unknown"
	if resp.Healthy != nil {
		if *resp.Healthy {
			healthStatus = "healthy"
		} else {
			healthStatus = "unhealthy"
		}
	}
	now := time.Now()
	monitor := monitoringModel.AgentMonitor{
		InstanceID:     instance.ID,
		ProviderID:     instance.ProviderID,
		UserID:         instance.UserID,
		AgentMonitorID: resp.ID,
		Interfaces:     strings.Join(bindingsInterfaces(bindings), ","),
		Bindings:       marshalTrafficBindings(bindings),
		ProviderKind:   providerKind,
		InstanceName:   instanceName,
		InnerIP:        innerIP,
		IsEnabled:      true,
		LastSyncAt:     now,
		HealthStatus:   healthStatus,
		HealthError:    resp.HealthError,
		LastHealthAt:   &now,
	}

	if err := s.db.Create(&monitor).Error; err != nil {
		_, _ = client.DeleteMonitor(resp.ID)
		return nil, fmt.Errorf("save agent monitor mapping: %w", err)
	}
	updateInstanceInterfaces(s.ctx, s.db, instance, &monitor)

	if global.APP_LOG != nil {
		global.APP_LOG.Info("registered agent monitor",
			zap.Uint("instance_id", instance.ID),
			zap.Int64("agent_monitor_id", resp.ID),
			zap.Strings("interfaces", bindingsInterfaces(bindings)))
	}
	return &monitor, nil
}

// DeregisterMonitor removes the monitor from both the agent and MySQL.
func (s *MonitorService) DeregisterMonitor(instanceID uint, config *monitoringModel.MonitoringConfig) error {
	var monitors []monitoringModel.AgentMonitor
	if err := s.db.Where("instance_id = ?", instanceID).Find(&monitors).Error; err != nil {
		return fmt.Errorf("find monitors for instance %d: %w", instanceID, err)
	}
	if len(monitors) == 0 {
		return nil
	}

	providerID := monitors[0].ProviderID
	client, err := s.getAgentClient(providerID, config)
	if err == nil {
		for _, monitor := range monitors {
			if _, err := client.DeleteMonitor(monitor.AgentMonitorID); err != nil {
				if global.APP_LOG != nil {
					global.APP_LOG.Warn("agent delete monitor failed (will remove mapping anyway)",
						zap.Uint("instance_id", instanceID),
						zap.Int64("agent_monitor_id", monitor.AgentMonitorID),
						zap.Error(err))
				}
			}
		}
	} else if global.APP_LOG != nil {
		global.APP_LOG.Warn("agent client unavailable while deregistering monitor (will remove mapping anyway)",
			zap.Uint("instance_id", instanceID),
			zap.Uint("provider_id", providerID),
			zap.Error(err))
	}

	// Remove from MySQL (hard delete)
	if err := s.db.Unscoped().Where("instance_id = ?", instanceID).Delete(&monitoringModel.AgentMonitor{}).Error; err != nil {
		return fmt.Errorf("delete agent monitor mapping: %w", err)
	}

	return nil
}

// UpdateMonitorInterfaces re-detects interfaces and updates the agent monitor.
// Called when an instance is restarted/rebuilt and its veth may have changed.
// vmidHint is the provider-side instance ID (e.g. Proxmox VMID); pass "" to auto-detect via GetInstance.
func (s *MonitorService) UpdateMonitorInterfaces(
	providerInstance provider.Provider,
	instance *providerModel.Instance,
	config *monitoringModel.MonitoringConfig,
	vmidHint string,
) error {
	var monitor monitoringModel.AgentMonitor
	if err := s.db.Where("instance_id = ?", instance.ID).First(&monitor).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			// Not registered yet, register now
			_, err := s.registerMonitorForInstance(providerInstance, instance, config, vmidHint)
			return err
		}
		return fmt.Errorf("find monitor: %w", err)
	}
	_, err := s.updateMonitorForInstance(providerInstance, instance, config, vmidHint, &monitor)
	return err
}

func (s *MonitorService) updateMonitorForInstance(
	providerInstance provider.Provider,
	instance *providerModel.Instance,
	config *monitoringModel.MonitoringConfig,
	vmidHint string,
	monitor *monitoringModel.AgentMonitor,
) (bool, error) {
	interfaces, err := s.detectBothInterfaces(providerInstance, instance, vmidHint)
	if err != nil {
		return false, fmt.Errorf("detect interfaces: %w", err)
	}
	bindings := buildTrafficBindings(instance, interfaces)
	if len(bindings) == 0 {
		return false, fmt.Errorf("no interfaces found for %s", instance.Name)
	}

	providerKind := providerInstance.GetType()
	instanceName := instance.Name
	innerIP := bindingsLegacyInnerIP(bindings)
	interfaceNames := bindingsInterfaces(bindings)

	client, err := s.getAgentClient(instance.ProviderID, config)
	if err != nil {
		return false, err
	}

	resp, err := client.UpdateMonitor(monitor.AgentMonitorID, bindings, providerKind, instanceName)
	if err != nil {
		return false, fmt.Errorf("agent update monitor: %w", err)
	}

	now := time.Now()
	healthStatus := "unknown"
	if resp.Healthy != nil {
		if *resp.Healthy {
			healthStatus = "healthy"
		} else {
			healthStatus = "unhealthy"
		}
	}
	updates := map[string]interface{}{
		"provider_id":    instance.ProviderID,
		"user_id":        instance.UserID,
		"interfaces":     strings.Join(interfaceNames, ","),
		"bindings":       marshalTrafficBindings(bindings),
		"provider_kind":  providerKind,
		"instance_name":  instanceName,
		"inner_ip":       innerIP,
		"is_enabled":     true,
		"last_sync_at":   now,
		"health_status":  healthStatus,
		"health_error":   resp.HealthError,
		"last_health_at": &now,
	}
	if err := s.db.Model(monitor).Updates(updates).Error; err != nil {
		return false, fmt.Errorf("save updated monitor mapping: %w", err)
	}

	monitor.ProviderID = instance.ProviderID
	monitor.UserID = instance.UserID
	monitor.Interfaces = strings.Join(interfaceNames, ",")
	monitor.Bindings = marshalTrafficBindings(bindings)
	monitor.ProviderKind = providerKind
	monitor.InstanceName = instanceName
	monitor.InnerIP = innerIP
	monitor.IsEnabled = true
	monitor.LastSyncAt = now
	monitor.HealthStatus = healthStatus
	monitor.HealthError = resp.HealthError
	monitor.LastHealthAt = &now
	updateInstanceInterfaces(s.ctx, s.db, instance, monitor)

	if global.APP_LOG != nil {
		global.APP_LOG.Info("updated agent monitor mapping",
			zap.Uint("instance_id", instance.ID),
			zap.Int64("agent_monitor_id", monitor.AgentMonitorID),
			zap.Strings("interfaces", interfaceNames),
			zap.String("inner_ip", innerIP))
	}
	return true, nil
}

// EnsureMonitorsForProvider ensures all active instances under a provider have monitors,
// and re-detects interfaces for existing monitors to keep them up-to-date.
func (s *MonitorService) EnsureMonitorsForProvider(
	providerInstance provider.Provider,
	providerID uint,
	config *monitoringModel.MonitoringConfig,
) (*MonitorSyncSummary, error) {
	return s.ensureMonitorsForProvider(providerInstance, providerID, config, 0)
}

// ReconcileMonitorsForProvider performs a bounded background repair pass.
// A non-positive repairLimit means no limit and is reserved for user-triggered full syncs.
func (s *MonitorService) ReconcileMonitorsForProvider(
	providerInstance provider.Provider,
	providerID uint,
	config *monitoringModel.MonitoringConfig,
	repairLimit int,
) (*MonitorSyncSummary, error) {
	return s.ensureMonitorsForProvider(providerInstance, providerID, config, repairLimit)
}

func (s *MonitorService) ensureMonitorsForProvider(
	providerInstance provider.Provider,
	providerID uint,
	config *monitoringModel.MonitoringConfig,
	repairLimit int,
) (*MonitorSyncSummary, error) {
	summary := &MonitorSyncSummary{}

	var instances []providerModel.Instance
	if err := s.db.Where("provider_id = ? AND status IN ?", providerID,
		[]string{"running", "active"}).Find(&instances).Error; err != nil {
		return summary, fmt.Errorf("list instances: %w", err)
	}
	summary.Total = len(instances)
	providerKind := providerInstance.GetType()

	var monitors []monitoringModel.AgentMonitor
	if err := s.db.Where("provider_id = ?", providerID).Find(&monitors).Error; err != nil {
		return summary, fmt.Errorf("list monitors: %w", err)
	}

	monitorByInstanceID := make(map[uint]*monitoringModel.AgentMonitor, len(monitors))
	for i := range monitors {
		monitorByInstanceID[monitors[i].InstanceID] = &monitors[i]
	}

	client, err := s.getAgentClient(providerID, config)
	if err != nil {
		return summary, err
	}
	agentList, err := client.ListMonitors()
	if err != nil {
		return summary, fmt.Errorf("list agent monitors: %w", err)
	}
	agentByID := make(map[int64]ListMonitorItem, len(agentList.Monitors))
	for _, item := range agentList.Monitors {
		agentByID[item.ID] = item
	}

	checkedAt := time.Now()
	healthUpdates := make([]monitorHealthUpdate, 0, len(monitors))
	repairCount := 0
	var vmidByName map[string]string
	resolveVMID := func(inst *providerModel.Instance) string {
		if providerKind == "proxmox" && vmidByName == nil {
			vmidByName = make(map[string]string)
			pInstances, listErr := providerInstance.ListInstances(s.ctx)
			if listErr == nil {
				for _, providerInstance := range pInstances {
					vmidByName[providerInstance.Name] = providerInstance.ID
				}
			} else if global.APP_LOG != nil {
				global.APP_LOG.Warn("failed to list proxmox instances for VMID lookup",
					zap.Uint("provider_id", providerID),
					zap.Error(listErr))
			}
		}
		if vmid := vmidByName[inst.Name]; vmid != "" {
			return vmid
		}
		return strings.TrimSpace(inst.ProviderVMID)
	}
	canRepair := func() bool {
		if repairLimit > 0 && repairCount >= repairLimit {
			return false
		}
		repairCount++
		return true
	}

	for i := range instances {
		inst := &instances[i]
		if existing := monitorByInstanceID[inst.ID]; existing != nil {
			agentItem, existsOnAgent := agentByID[existing.AgentMonitorID]
			if !existsOnAgent {
				if !canRepair() {
					summary.Deferred++
					healthUpdates = append(healthUpdates, monitorHealthUpdate{ID: existing.ID, Status: "unhealthy", Error: "agent monitor missing; repair deferred by background batch limit"})
					continue
				}
				if err := s.db.Unscoped().Delete(existing).Error; err != nil {
					summary.Failed++
					summary.Errors = append(summary.Errors, fmt.Sprintf("%s: remove missing agent mapping: %v", inst.Name, err))
					continue
				}
				monitor, registerErr := s.registerMonitorForInstance(providerInstance, inst, config, resolveVMID(inst))
				if registerErr != nil {
					summary.Failed++
					summary.Errors = append(summary.Errors, fmt.Sprintf("%s: %v", inst.Name, registerErr))
					continue
				}
				monitorByInstanceID[inst.ID] = monitor
				summary.Created++
				continue
			}

			cachedBindings := cachedBindingsForInstance(inst)
			if agentItem.Healthy == nil {
				legacyInterfacesMatch := stringsEqual(agentItem.Interface, normalizeMonitorInterfaces(existing.Interfaces))
				if legacyInterfacesMatch && monitorMatchesInstanceCache(existing, inst, providerKind, cachedBindings) {
					healthUpdates = append(healthUpdates, monitorHealthUpdate{ID: existing.ID, Status: "unknown", Error: "agent version does not report binding health"})
					summary.Unchanged++
					continue
				}
			} else if *agentItem.Healthy && len(agentItem.Bindings) > 0 &&
				trafficBindingsEqual(agentItem.Bindings, cachedBindings) &&
				monitorMatchesInstanceCache(existing, inst, providerKind, cachedBindings) {
				healthUpdates = append(healthUpdates, monitorHealthUpdate{ID: existing.ID, Status: "healthy"})
				summary.Unchanged++
				continue
			}
			if !canRepair() {
				summary.Deferred++
				healthUpdates = append(healthUpdates, monitorHealthUpdate{ID: existing.ID, Status: "unhealthy", Error: "monitor drift detected; repair deferred by background batch limit"})
				continue
			}
			if global.APP_LOG != nil && (agentItem.Healthy == nil || !*agentItem.Healthy) {
				global.APP_LOG.Warn("agent monitor unhealthy; re-detecting provider interfaces",
					zap.Uint("instance_id", inst.ID),
					zap.Int64("agent_monitor_id", existing.AgentMonitorID),
					zap.Strings("missing_interfaces", agentItem.MissingInterfaces),
					zap.String("health_error", agentItem.HealthError))
			}
			updated, err := s.updateMonitorForInstance(providerInstance, inst, config, resolveVMID(inst), existing)
			if err != nil {
				summary.Failed++
				summary.Errors = append(summary.Errors, fmt.Sprintf("%s: %v", inst.Name, err))
				healthUpdates = append(healthUpdates, monitorHealthUpdate{ID: existing.ID, Status: "unhealthy", Error: err.Error()})
				if global.APP_LOG != nil {
					global.APP_LOG.Warn("failed to update monitor interfaces for instance",
						zap.Uint("instance_id", inst.ID),
						zap.String("name", inst.Name),
						zap.Error(err))
				}
				continue
			}
			if updated {
				summary.Updated++
			} else {
				summary.Unchanged++
			}
			continue
		}

		if !canRepair() {
			summary.Deferred++
			continue
		}
		if monitor, err := s.registerMonitorForInstance(providerInstance, inst, config, resolveVMID(inst)); err != nil {
			summary.Failed++
			summary.Errors = append(summary.Errors, fmt.Sprintf("%s: %v", inst.Name, err))
			if global.APP_LOG != nil {
				global.APP_LOG.Warn("failed to register monitor for instance",
					zap.Uint("instance_id", inst.ID),
					zap.String("name", inst.Name),
					zap.Error(err))
			}
		} else {
			monitorByInstanceID[inst.ID] = monitor
			summary.Created++
		}
	}
	if err := s.updateMonitorHealthBatch(healthUpdates, checkedAt); err != nil {
		summary.Errors = append(summary.Errors, err.Error())
		return summary, err
	}

	return summary, nil
}

// CleanupStaleMonitors removes monitors for instances that no longer exist or are stopped.
func (s *MonitorService) CleanupStaleMonitors(providerID uint, config *monitoringModel.MonitoringConfig) (int, error) {
	var monitors []monitoringModel.AgentMonitor
	if err := s.db.Where("provider_id = ?", providerID).Find(&monitors).Error; err != nil {
		return 0, err
	}
	if len(monitors) == 0 {
		return 0, nil
	}

	instanceIDs := make([]uint, 0, len(monitors))
	seenInstanceIDs := make(map[uint]struct{}, len(monitors))
	for _, monitor := range monitors {
		if _, exists := seenInstanceIDs[monitor.InstanceID]; exists {
			continue
		}
		seenInstanceIDs[monitor.InstanceID] = struct{}{}
		instanceIDs = append(instanceIDs, monitor.InstanceID)
	}

	type instanceState struct {
		ID     uint
		Status string
	}
	var instances []instanceState
	if err := s.db.Model(&providerModel.Instance{}).
		Select("id", "status").
		Where("id IN ?", instanceIDs).
		Find(&instances).Error; err != nil {
		return 0, fmt.Errorf("list instance states: %w", err)
	}

	statusByInstanceID := make(map[uint]string, len(instances))
	for _, instance := range instances {
		statusByInstanceID[instance.ID] = instance.Status
	}

	stale := make([]monitoringModel.AgentMonitor, 0)
	for _, monitor := range monitors {
		status, exists := statusByInstanceID[monitor.InstanceID]
		if !exists || (status != "running" && status != "active") {
			stale = append(stale, monitor)
		}
	}
	if len(stale) == 0 {
		return 0, nil
	}

	client, err := s.getAgentClient(providerID, config)
	if err == nil {
		agentMonitorIDs := make([]int64, 0, len(stale))
		for _, monitor := range stale {
			agentMonitorIDs = append(agentMonitorIDs, monitor.AgentMonitorID)
		}
		if _, err := client.BatchDeleteMonitors(agentMonitorIDs); err != nil && global.APP_LOG != nil {
			global.APP_LOG.Warn("agent batch delete stale monitors failed (will remove mappings anyway)",
				zap.Uint("provider_id", providerID),
				zap.Int("monitor_count", len(agentMonitorIDs)),
				zap.Error(err))
		}
	} else if global.APP_LOG != nil {
		global.APP_LOG.Warn("agent client unavailable while cleaning stale monitors (will remove mappings anyway)",
			zap.Uint("provider_id", providerID),
			zap.Error(err))
	}

	ids := make([]uint, 0, len(stale))
	for _, monitor := range stale {
		ids = append(ids, monitor.ID)
	}
	if err := s.db.Unscoped().Where("id IN ?", ids).Delete(&monitoringModel.AgentMonitor{}).Error; err != nil {
		return 0, fmt.Errorf("delete stale monitor mappings: %w", err)
	}
	return len(stale), nil
}

// GetMonitorByInstanceID returns the agent monitor mapping for an instance.
func (s *MonitorService) GetMonitorByInstanceID(instanceID uint) (*monitoringModel.AgentMonitor, error) {
	var monitor monitoringModel.AgentMonitor
	if err := s.db.Where("instance_id = ?", instanceID).First(&monitor).Error; err != nil {
		return nil, err
	}
	return &monitor, nil
}

// GetMonitorsByProviderID returns all monitors for a provider.
func (s *MonitorService) GetMonitorsByProviderID(providerID uint) ([]monitoringModel.AgentMonitor, error) {
	var monitors []monitoringModel.AgentMonitor
	if err := s.db.Where("provider_id = ?", providerID).Find(&monitors).Error; err != nil {
		return nil, err
	}
	return monitors, nil
}
