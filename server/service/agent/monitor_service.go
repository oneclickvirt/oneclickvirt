package agent

import (
	"context"
	"fmt"
	"strings"
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
	db  *gorm.DB
	ctx context.Context
}

// NewMonitorService creates a new monitor service.
func NewMonitorService(ctx context.Context, db *gorm.DB) *MonitorService {
	return &MonitorService{db: db, ctx: ctx}
}

// MonitorSyncSummary describes what a provider monitor synchronization changed.
type MonitorSyncSummary struct {
	Total     int      `json:"total"`
	Created   int      `json:"created"`
	Updated   int      `json:"updated"`
	Unchanged int      `json:"unchanged"`
	Failed    int      `json:"failed"`
	Cleaned   int      `json:"cleaned"`
	Errors    []string `json:"errors,omitempty"`
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

// getAgentClient returns an agent client for the given provider using its endpoint.
// For agent-mode providers behind NAT, the HTTP API is not directly reachable;
// the WS fallback in Client.doRequest handles connectivity via WebSocket.
func (s *MonitorService) getAgentClient(providerID uint, config *monitoringModel.MonitoringConfig) (*Client, error) {
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
	return GetClientWithMode(providerID, host, port, config.AgentToken, p.ConnectionType == "agent"), nil
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
	interfaces, err := s.detectInstanceInterfaces(providerInstance, instance, vmidHint)
	if err != nil {
		return nil, fmt.Errorf("detect interfaces for instance %s: %w", instance.Name, err)
	}
	if len(interfaces) == 0 {
		return nil, fmt.Errorf("no network interfaces found for instance %s", instance.Name)
	}

	client, err := s.getAgentClient(instance.ProviderID, config)
	if err != nil {
		return nil, err
	}

	providerKind := providerInstance.GetType()
	instanceName := instance.Name
	innerIP := instance.PrivateIP

	resp, err := client.AddMonitor(interfaces, providerKind, instanceName, innerIP)
	if err != nil {
		return nil, fmt.Errorf("agent add monitor for %s: %w", instance.Name, err)
	}

	monitor := monitoringModel.AgentMonitor{
		InstanceID:     instance.ID,
		ProviderID:     instance.ProviderID,
		UserID:         instance.UserID,
		AgentMonitorID: resp.ID,
		Interfaces:     strings.Join(resp.Interface, ","),
		ProviderKind:   providerKind,
		InstanceName:   instanceName,
		InnerIP:        innerIP,
		IsEnabled:      true,
		LastSyncAt:     time.Now(),
	}

	if err := s.db.Create(&monitor).Error; err != nil {
		_, _ = client.DeleteMonitor(resp.ID)
		return nil, fmt.Errorf("save agent monitor mapping: %w", err)
	}

	if global.APP_LOG != nil {
		global.APP_LOG.Info("registered agent monitor",
			zap.Uint("instance_id", instance.ID),
			zap.Int64("agent_monitor_id", resp.ID),
			zap.Strings("interfaces", resp.Interface))
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
	interfaces, err := s.detectInstanceInterfaces(providerInstance, instance, vmidHint)
	if err != nil {
		return false, fmt.Errorf("detect interfaces: %w", err)
	}
	if len(interfaces) == 0 {
		return false, fmt.Errorf("no interfaces found for %s", instance.Name)
	}

	providerKind := providerInstance.GetType()
	instanceName := instance.Name
	innerIP := instance.PrivateIP
	interfacesChanged := !stringsEqual(normalizeMonitorInterfaces(monitor.Interfaces), interfaces)
	metadataChanged := monitor.ProviderID != instance.ProviderID ||
		monitor.UserID != instance.UserID ||
		monitor.ProviderKind != providerKind ||
		monitor.InstanceName != instanceName ||
		monitor.InnerIP != innerIP ||
		!monitor.IsEnabled

	if !interfacesChanged && !metadataChanged {
		return false, nil
	}

	client, err := s.getAgentClient(instance.ProviderID, config)
	if err != nil {
		return false, err
	}

	resp, err := client.UpdateMonitor(monitor.AgentMonitorID, interfaces, providerKind, instanceName, innerIP)
	if err != nil {
		return false, fmt.Errorf("agent update monitor: %w", err)
	}

	now := time.Now()
	updates := map[string]interface{}{
		"provider_id":   instance.ProviderID,
		"user_id":       instance.UserID,
		"interfaces":    strings.Join(resp.Interface, ","),
		"provider_kind": providerKind,
		"instance_name": instanceName,
		"inner_ip":      innerIP,
		"is_enabled":    true,
		"last_sync_at":  now,
	}
	if err := s.db.Model(monitor).Updates(updates).Error; err != nil {
		return false, fmt.Errorf("save updated monitor mapping: %w", err)
	}

	monitor.ProviderID = instance.ProviderID
	monitor.UserID = instance.UserID
	monitor.Interfaces = strings.Join(resp.Interface, ",")
	monitor.ProviderKind = providerKind
	monitor.InstanceName = instanceName
	monitor.InnerIP = innerIP
	monitor.IsEnabled = true
	monitor.LastSyncAt = now

	if global.APP_LOG != nil {
		global.APP_LOG.Info("updated agent monitor mapping",
			zap.Uint("instance_id", instance.ID),
			zap.Int64("agent_monitor_id", monitor.AgentMonitorID),
			zap.Strings("interfaces", resp.Interface),
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
	summary := &MonitorSyncSummary{}

	var instances []providerModel.Instance
	if err := s.db.Where("provider_id = ? AND status IN ?", providerID,
		[]string{"running", "active"}).Find(&instances).Error; err != nil {
		return summary, fmt.Errorf("list instances: %w", err)
	}
	summary.Total = len(instances)

	var monitors []monitoringModel.AgentMonitor
	if err := s.db.Where("provider_id = ?", providerID).Find(&monitors).Error; err != nil {
		return summary, fmt.Errorf("list monitors: %w", err)
	}

	monitorByInstanceID := make(map[uint]*monitoringModel.AgentMonitor, len(monitors))
	for i := range monitors {
		monitorByInstanceID[monitors[i].InstanceID] = &monitors[i]
	}

	// Pre-fetch provider-side instance list once for Proxmox to map name→VMID,
	// avoiding N+1 API calls when iterating over instances.
	vmidByName := make(map[string]string)
	if providerInstance.GetType() == "proxmox" {
		pInstances, listErr := providerInstance.ListInstances(s.ctx)
		if listErr == nil {
			for _, pi := range pInstances {
				vmidByName[pi.Name] = pi.ID
			}
		} else if global.APP_LOG != nil {
			global.APP_LOG.Warn("failed to list proxmox instances for VMID lookup",
				zap.Uint("provider_id", providerID),
				zap.Error(listErr))
		}
	}

	for i := range instances {
		inst := &instances[i]
		vmid := vmidByName[inst.Name]
		if existing := monitorByInstanceID[inst.ID]; existing != nil {
			updated, err := s.updateMonitorForInstance(providerInstance, inst, config, vmid, existing)
			if err != nil {
				summary.Failed++
				summary.Errors = append(summary.Errors, fmt.Sprintf("%s: %v", inst.Name, err))
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

		if monitor, err := s.registerMonitorForInstance(providerInstance, inst, config, vmid); err != nil {
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
		for _, monitor := range stale {
			if _, err := client.DeleteMonitor(monitor.AgentMonitorID); err != nil && global.APP_LOG != nil {
				global.APP_LOG.Warn("agent delete stale monitor failed (will remove mapping anyway)",
					zap.Uint("instance_id", monitor.InstanceID),
					zap.Int64("agent_monitor_id", monitor.AgentMonitorID),
					zap.Error(err))
			}
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
