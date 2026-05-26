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
			host = "127.0.0.1" // placeholder; actual calls go through WS fallback
		} else {
			return nil, fmt.Errorf("provider %d has no endpoint", providerID)
		}
	}
	port := config.AgentPort
	if port == 0 {
		port = AgentPort
	}
	return GetClient(providerID, host, port, config.AgentToken), nil
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
	// Check if already registered
	var existing monitoringModel.AgentMonitor
	if err := s.db.Where("instance_id = ?", instance.ID).First(&existing).Error; err == nil {
		// Already registered, verify it's still valid
		return &existing, nil
	}

	// Detect network interfaces for this instance
	interfaces, err := s.detectInstanceInterfaces(providerInstance, instance, vmidHint)
	if err != nil {
		return nil, fmt.Errorf("detect interfaces for instance %s: %w", instance.Name, err)
	}
	if len(interfaces) == 0 {
		return nil, fmt.Errorf("no network interfaces found for instance %s", instance.Name)
	}

	// Get agent client
	client, err := s.getAgentClient(instance.ProviderID, config)
	if err != nil {
		return nil, err
	}

	providerKind := providerInstance.GetType()
	instanceName := instance.Name

	// Determine inner IP for per-IP traffic filtering.
	// For containers sharing a bridge (NAT), PrivateIP is set → per-IP rules.
	// For dedicated-IP instances (PrivateIP empty), pass "" → interface-based
	// counting that excludes private ranges, which is more accurate for
	// single-tenant veth interfaces.
	innerIP := instance.PrivateIP

	// Call agent to add monitor
	resp, err := client.AddMonitor(interfaces, providerKind, instanceName, innerIP)
	if err != nil {
		return nil, fmt.Errorf("agent add monitor for %s: %w", instance.Name, err)
	}

	// Save mapping in MySQL
	monitor := monitoringModel.AgentMonitor{
		InstanceID:     instance.ID,
		ProviderID:     instance.ProviderID,
		UserID:         instance.UserID,
		AgentMonitorID: resp.ID,
		Interfaces:     strings.Join(resp.Interface, ","),
		ProviderKind:   providerKind,
		InstanceName:   instanceName,
		IsEnabled:      true,
		LastSyncAt:     time.Now(),
	}

	if err := s.db.Create(&monitor).Error; err != nil {
		// If DB save fails, try to clean up the agent-side monitor
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
	var monitor monitoringModel.AgentMonitor
	if err := s.db.Where("instance_id = ?", instanceID).First(&monitor).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil // Already deregistered
		}
		return fmt.Errorf("find monitor for instance %d: %w", instanceID, err)
	}

	// Call agent to delete monitor
	client, err := s.getAgentClient(monitor.ProviderID, config)
	if err != nil {
		return err
	}
	if _, err := client.DeleteMonitor(monitor.AgentMonitorID); err != nil {
		if global.APP_LOG != nil {
			global.APP_LOG.Warn("agent delete monitor failed (will remove mapping anyway)",
				zap.Uint("instance_id", instanceID),
				zap.Int64("agent_monitor_id", monitor.AgentMonitorID),
				zap.Error(err))
		}
	}

	// Remove from MySQL (hard delete)
	if err := s.db.Unscoped().Delete(&monitor).Error; err != nil {
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
			_, err := s.RegisterMonitor(providerInstance, instance, config, vmidHint)
			return err
		}
		return fmt.Errorf("find monitor: %w", err)
	}

	// Detect current interfaces
	interfaces, err := s.detectInstanceInterfaces(providerInstance, instance, vmidHint)
	if err != nil {
		return fmt.Errorf("detect interfaces: %w", err)
	}
	if len(interfaces) == 0 {
		return fmt.Errorf("no interfaces found for %s", instance.Name)
	}

	// Check if interfaces changed
	currentInterfaces := strings.Split(monitor.Interfaces, ",")
	if stringsEqual(currentInterfaces, interfaces) {
		return nil // No change
	}

	// Update on agent
	client, err := s.getAgentClient(instance.ProviderID, config)
	if err != nil {
		return err
	}

	// Determine inner IP for per-IP traffic filtering
	innerIP := instance.PrivateIP
	if innerIP == "" {
		innerIP = instance.PublicIP
	}

	resp, err := client.UpdateMonitor(monitor.AgentMonitorID, interfaces, providerInstance.GetType(), instance.Name, innerIP)
	if err != nil {
		return fmt.Errorf("agent update monitor: %w", err)
	}

	// Update in MySQL
	monitor.Interfaces = strings.Join(resp.Interface, ",")
	if err := s.db.Save(&monitor).Error; err != nil {
		return fmt.Errorf("save updated interfaces: %w", err)
	}

	if global.APP_LOG != nil {
		global.APP_LOG.Info("updated agent monitor interfaces",
			zap.Uint("instance_id", instance.ID),
			zap.Int64("agent_monitor_id", monitor.AgentMonitorID),
			zap.Strings("new_interfaces", resp.Interface))
	}
	return nil
}

// EnsureMonitorsForProvider ensures all active instances under a provider have monitors,
// and re-detects interfaces for existing monitors to keep them up-to-date.
func (s *MonitorService) EnsureMonitorsForProvider(
	providerInstance provider.Provider,
	providerID uint,
	config *monitoringModel.MonitoringConfig,
) error {
	// Get all running instances for this provider
	var instances []providerModel.Instance
	if err := s.db.Where("provider_id = ? AND status IN ?", providerID,
		[]string{"running", "active"}).Find(&instances).Error; err != nil {
		return fmt.Errorf("list instances: %w", err)
	}

	// Get existing monitors for this provider
	var monitors []monitoringModel.AgentMonitor
	if err := s.db.Where("provider_id = ?", providerID).Find(&monitors).Error; err != nil {
		return fmt.Errorf("list monitors: %w", err)
	}

	monitorByInstanceID := make(map[uint]*monitoringModel.AgentMonitor)
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
		existing := monitorByInstanceID[inst.ID]
		vmid := vmidByName[inst.Name]

		if existing != nil {
			// Already registered - re-detect interfaces and update on agent
			if err := s.UpdateMonitorInterfaces(providerInstance, inst, config, vmid); err != nil {
				if global.APP_LOG != nil {
					global.APP_LOG.Warn("failed to update monitor interfaces for instance",
						zap.Uint("instance_id", inst.ID),
						zap.String("name", inst.Name),
						zap.Error(err))
				}
			}
		} else {
			// Not registered - create new monitor
			if _, err := s.RegisterMonitor(providerInstance, inst, config, vmid); err != nil {
				if global.APP_LOG != nil {
					global.APP_LOG.Warn("failed to register monitor for instance",
						zap.Uint("instance_id", inst.ID),
						zap.String("name", inst.Name),
						zap.Error(err))
				}
			}
		}
	}
	return nil
}

// CleanupStaleMonitors removes monitors for instances that no longer exist or are stopped.
func (s *MonitorService) CleanupStaleMonitors(providerID uint, config *monitoringModel.MonitoringConfig) error {
	var monitors []monitoringModel.AgentMonitor
	if err := s.db.Where("provider_id = ?", providerID).Find(&monitors).Error; err != nil {
		return err
	}

	for _, monitor := range monitors {
		var instance providerModel.Instance
		err := s.db.First(&instance, monitor.InstanceID).Error
		if err == gorm.ErrRecordNotFound || (err == nil && instance.Status != "running" && instance.Status != "active") {
			if err := s.DeregisterMonitor(monitor.InstanceID, config); err != nil {
				if global.APP_LOG != nil {
					global.APP_LOG.Warn("failed to deregister stale monitor",
						zap.Uint("instance_id", monitor.InstanceID),
						zap.Error(err))
				}
			}
		}
	}
	return nil
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
