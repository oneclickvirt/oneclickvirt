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
func (s *MonitorService) getAgentClient(providerID uint, config *monitoringModel.MonitoringConfig) (*Client, error) {
	var p providerModel.Provider
	if err := s.db.First(&p, providerID).Error; err != nil {
		return nil, fmt.Errorf("load provider %d: %w", providerID, err)
	}
	host := p.Endpoint
	if host == "" {
		return nil, fmt.Errorf("provider %d has no endpoint", providerID)
	}
	port := config.AgentPort
	if port == 0 {
		port = AgentPort
	}
	return GetClient(providerID, host, port, config.AgentToken), nil
}

// RegisterMonitor creates a monitor on the agent and saves the mapping in MySQL.
// It detects the network interfaces for the instance and calls the agent's add API.
func (s *MonitorService) RegisterMonitor(
	providerInstance provider.Provider,
	instance *providerModel.Instance,
	config *monitoringModel.MonitoringConfig,
) (*monitoringModel.AgentMonitor, error) {
	// Check if already registered
	var existing monitoringModel.AgentMonitor
	if err := s.db.Where("instance_id = ?", instance.ID).First(&existing).Error; err == nil {
		// Already registered, verify it's still valid
		return &existing, nil
	}

	// Detect network interfaces for this instance
	interfaces, err := s.detectInstanceInterfaces(providerInstance, instance)
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

	// Determine inner IP for per-IP traffic filtering
	innerIP := instance.PrivateIP
	if innerIP == "" {
		innerIP = instance.PublicIP // fallback for dedicated IP setups
	}

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
func (s *MonitorService) UpdateMonitorInterfaces(
	providerInstance provider.Provider,
	instance *providerModel.Instance,
	config *monitoringModel.MonitoringConfig,
) error {
	var monitor monitoringModel.AgentMonitor
	if err := s.db.Where("instance_id = ?", instance.ID).First(&monitor).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			// Not registered yet, register now
			_, err := s.RegisterMonitor(providerInstance, instance, config)
			return err
		}
		return fmt.Errorf("find monitor: %w", err)
	}

	// Detect current interfaces
	interfaces, err := s.detectInstanceInterfaces(providerInstance, instance)
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

// EnsureMonitorsForProvider ensures all active instances under a provider have monitors.
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

	monitored := make(map[uint]bool)
	for _, m := range monitors {
		monitored[m.InstanceID] = true
	}

	for i := range instances {
		if monitored[instances[i].ID] {
			continue
		}
		if _, err := s.RegisterMonitor(providerInstance, &instances[i], config); err != nil {
			if global.APP_LOG != nil {
				global.APP_LOG.Warn("failed to register monitor for instance",
					zap.Uint("instance_id", instances[i].ID),
					zap.String("name", instances[i].Name),
					zap.Error(err))
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

// detectInstanceInterfaces detects the network interfaces for an instance.
// Returns a list of interface names (typically 1 for IPv4, possibly 2 for IPv4+IPv6).
func (s *MonitorService) detectInstanceInterfaces(
	providerInstance provider.Provider,
	instance *providerModel.Instance,
) ([]string, error) {
	providerType := providerInstance.GetType()
	var interfaces []string

	switch providerType {
	case "docker", "podman", "containerd":
		iface, err := s.detectVethInterface(providerInstance, instance.Name, providerType)
		if err != nil {
			return nil, err
		}
		interfaces = append(interfaces, iface)

	case "lxd", "incus":
		iface, err := s.detectLxdIncusInterface(providerInstance, instance.Name, providerType)
		if err != nil {
			return nil, err
		}
		interfaces = append(interfaces, iface)

	case "proxmox":
		iface, err := s.detectProxmoxInterface(providerInstance, instance.Name, fmt.Sprintf("%d", instance.ID))
		if err != nil {
			return nil, err
		}
		interfaces = append(interfaces, iface)

	default:
		return nil, fmt.Errorf("unsupported provider type: %s", providerType)
	}

	return interfaces, nil
}

// detectVethInterface detects the veth interface for a Docker/Podman/Containerd container.
func (s *MonitorService) detectVethInterface(providerInstance provider.Provider, instanceName, providerType string) (string, error) {
	var runtimeCmd string
	switch providerType {
	case "docker":
		runtimeCmd = "docker"
	case "podman":
		runtimeCmd = "podman"
	case "containerd":
		runtimeCmd = "nerdctl"
	}

	detectCmd := fmt.Sprintf(`
CONTAINER_PID=$(%s inspect -f '{{.State.Pid}}' '%s' 2>/dev/null)
if [ -z "$CONTAINER_PID" ] || [ "$CONTAINER_PID" = "0" ]; then
    echo "ERROR: container not running"
    exit 1
fi
HOST_VETH_IFINDEX=$(nsenter -t $CONTAINER_PID -n ip link show eth0 2>/dev/null | head -n1 | sed -n 's/.*@if\([0-9]\+\).*/\1/p')
if [ -z "$HOST_VETH_IFINDEX" ]; then
    echo "ERROR: no veth ifindex"
    exit 1
fi
VETH_NAME=$(ip -o link show 2>/dev/null | awk -v idx="$HOST_VETH_IFINDEX" -F': ' '$1 == idx {print $2}' | cut -d'@' -f1)
if [ -n "$VETH_NAME" ]; then
    echo "$VETH_NAME"
    exit 0
fi
echo "ERROR: veth not found"
exit 1
`, runtimeCmd, instanceName)

	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()

	output, err := providerInstance.ExecuteSSHCommand(ctx, detectCmd)
	if err != nil {
		return "", fmt.Errorf("detect veth for %s: %w (output: %s)", instanceName, err, output)
	}

	iface := strings.TrimSpace(output)
	if strings.HasPrefix(iface, "ERROR:") || iface == "" {
		return "", fmt.Errorf("detect veth for %s: %s", instanceName, iface)
	}
	return iface, nil
}

// detectLxdIncusInterface detects the veth interface for an LXD/Incus container.
func (s *MonitorService) detectLxdIncusInterface(providerInstance provider.Provider, instanceName, providerType string) (string, error) {
	// Try provider's GetVethInterfaceName first
	if providerType == "lxd" {
		if lxdProv, ok := providerInstance.(interface {
			GetVethInterfaceName(string) (string, error)
		}); ok {
			if name, err := lxdProv.GetVethInterfaceName(instanceName); err == nil && name != "" {
				return name, nil
			}
		}
	} else if providerType == "incus" {
		if incusProv, ok := providerInstance.(interface {
			GetVethInterfaceName(context.Context, string) (string, error)
		}); ok {
			ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
			defer cancel()
			if name, err := incusProv.GetVethInterfaceName(ctx, instanceName); err == nil && name != "" {
				return name, nil
			}
		}
	}

	// Fallback: nsenter method
	cmd := "lxc"
	if providerType == "incus" {
		cmd = "incus"
	}

	detectCmd := fmt.Sprintf(`
CONTAINER_PID=$(%s info '%s' 2>/dev/null | grep -i 'PID:' | awk '{print $2}')
if [ -z "$CONTAINER_PID" ] || [ "$CONTAINER_PID" = "0" ]; then
    echo "ERROR: container not running"
    exit 1
fi
HOST_VETH_IFINDEX=$(nsenter -t $CONTAINER_PID -n ip link show eth0 2>/dev/null | head -n1 | sed -n 's/.*@if\([0-9]\+\).*/\1/p')
if [ -z "$HOST_VETH_IFINDEX" ]; then
    echo "ERROR: no veth ifindex"
    exit 1
fi
VETH_NAME=$(ip -o link show 2>/dev/null | awk -v idx="$HOST_VETH_IFINDEX" -F': ' '$1 == idx {print $2}' | cut -d'@' -f1)
if [ -n "$VETH_NAME" ]; then
    echo "$VETH_NAME"
    exit 0
fi
echo "ERROR: veth not found"
exit 1
`, cmd, instanceName)

	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()

	output, err := providerInstance.ExecuteSSHCommand(ctx, detectCmd)
	if err != nil {
		return "", fmt.Errorf("detect veth for %s: %w", instanceName, err)
	}

	iface := strings.TrimSpace(output)
	if strings.HasPrefix(iface, "ERROR:") || iface == "" {
		return "", fmt.Errorf("detect veth for %s: %s", instanceName, iface)
	}
	return iface, nil
}

// detectProxmoxInterface detects the network interface for a Proxmox instance.
func (s *MonitorService) detectProxmoxInterface(providerInstance provider.Provider, instanceName, instanceID string) (string, error) {
	detectCmd := fmt.Sprintf(`
INSTANCE_ID='%s'
# LXC: veth<ctid>i0
if ip link show veth${INSTANCE_ID}i0 >/dev/null 2>&1; then
    echo "veth${INSTANCE_ID}i0"
    exit 0
fi
# KVM: tap<vmid>i0
if ip link show tap${INSTANCE_ID}i0 >/dev/null 2>&1; then
    echo "tap${INSTANCE_ID}i0"
    exit 0
fi
# Broader search
IFACE=$(ip link | grep -oE "(veth|tap)${INSTANCE_ID}i[0-9]+" | head -n1)
if [ -n "$IFACE" ]; then
    echo "$IFACE"
    exit 0
fi
echo "ERROR: no interface for instance $INSTANCE_ID"
exit 1
`, instanceID)

	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()

	output, err := providerInstance.ExecuteSSHCommand(ctx, detectCmd)
	if err != nil {
		return "", fmt.Errorf("detect proxmox iface for %s: %w", instanceName, err)
	}

	iface := strings.TrimSpace(output)
	if strings.HasPrefix(iface, "ERROR:") || iface == "" {
		return "", fmt.Errorf("detect proxmox iface for %s: %s", instanceName, iface)
	}
	return iface, nil
}

func stringsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
