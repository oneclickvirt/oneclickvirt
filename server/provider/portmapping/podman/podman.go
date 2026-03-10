package podman

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/model/provider"
	"oneclickvirt/provider/portmapping"
	providerService "oneclickvirt/service/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

const (
	podmanCLI     = "podman"
	podmanNetwork = "podman-net"
	podmanSubnet  = "172.20.0.0/16"
)

// PodmanPortMapping Podman端口映射实现（独立实现，不依赖docker portmapping包）
type PodmanPortMapping struct {
	*portmapping.BaseProvider
}

// SupportsDynamicMapping Podman不支持动态端口映射
func (p *PodmanPortMapping) SupportsDynamicMapping() bool {
	return false
}

// CreatePortMapping 创建Podman端口映射
func (p *PodmanPortMapping) CreatePortMapping(ctx context.Context, req *portmapping.PortMappingRequest) (*portmapping.PortMappingResult, error) {
	global.APP_LOG.Info("Creating Podman port mapping",
		zap.String("instanceId", req.InstanceID),
		zap.Int("hostPort", req.HostPort),
		zap.Int("guestPort", req.GuestPort),
		zap.String("protocol", req.Protocol))

	if err := p.validateRequest(req); err != nil {
		return nil, fmt.Errorf("invalid request: %v", err)
	}

	instance, err := p.getInstance(req.InstanceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get instance: %v", err)
	}

	providerInfo, err := p.getProvider(req.ProviderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider: %v", err)
	}

	hostPort := req.HostPort
	if hostPort == 0 {
		hostPort, err = p.BaseProvider.AllocatePort(ctx, req.ProviderID, 0)
		if err != nil {
			return nil, fmt.Errorf("failed to allocate port: %v", err)
		}
	}

	if err := p.createPortMapping(ctx, instance, hostPort, req.GuestPort, req.Protocol, providerInfo); err != nil {
		return nil, fmt.Errorf("failed to create podman port mapping: %v", err)
	}

	isSSH := req.GuestPort == 22
	if req.IsSSH != nil {
		isSSH = *req.IsSSH
	}

	result := &portmapping.PortMappingResult{
		InstanceID:    req.InstanceID,
		ProviderID:    req.ProviderID,
		Protocol:      strings.ToLower(req.Protocol),
		HostPort:      hostPort,
		GuestPort:     req.GuestPort,
		HostIP:        providerInfo.Endpoint,
		PublicIP:      p.getPublicIP(providerInfo),
		IPv6Address:   req.IPv6Address,
		Status:        "active",
		Description:   req.Description,
		MappingMethod: "podman-native",
		IsSSH:         isSSH,
		IsAutomatic:   req.HostPort == 0,
	}

	portModel := p.BaseProvider.ToDBModel(result)
	if err := global.APP_DB.Create(portModel).Error; err != nil {
		global.APP_LOG.Error("Failed to save port mapping to database", zap.Error(err))
		p.cleanupPortMapping(ctx, instance, hostPort, req.GuestPort, req.Protocol)
		return nil, fmt.Errorf("failed to save port mapping: %v", err)
	}

	result.ID = portModel.ID
	result.CreatedAt = portModel.CreatedAt.Format("2006-01-02T15:04:05Z07:00")
	result.UpdatedAt = portModel.UpdatedAt.Format("2006-01-02T15:04:05Z07:00")

	global.APP_LOG.Info("Podman port mapping created successfully",
		zap.Uint("id", result.ID),
		zap.Int("hostPort", hostPort),
		zap.Int("guestPort", req.GuestPort))

	return result, nil
}

// DeletePortMapping 删除Podman端口映射
func (p *PodmanPortMapping) DeletePortMapping(ctx context.Context, req *portmapping.DeletePortMappingRequest) error {
	global.APP_LOG.Info("Deleting Podman port mapping",
		zap.Uint("id", req.ID),
		zap.String("instanceId", req.InstanceID))

	var portModel provider.Port
	if err := global.APP_DB.First(&portModel, req.ID).Error; err != nil {
		return fmt.Errorf("port mapping not found: %v", err)
	}

	instance, err := p.getInstance(req.InstanceID)
	if err != nil {
		return fmt.Errorf("failed to get instance: %v", err)
	}

	if err := p.removePortMapping(ctx, instance, portModel.HostPort, portModel.GuestPort, portModel.Protocol); err != nil {
		if !req.ForceDelete {
			return fmt.Errorf("failed to remove podman port mapping: %v", err)
		}
		global.APP_LOG.Warn("Failed to remove podman port mapping, but force delete is enabled", zap.Error(err))
	}

	if err := global.APP_DB.Delete(&portModel).Error; err != nil {
		return fmt.Errorf("failed to delete port mapping from database: %v", err)
	}

	global.APP_LOG.Info("Podman port mapping deleted successfully", zap.Uint("id", req.ID))
	return nil
}

// UpdatePortMapping Podman不支持动态端口映射更新
func (p *PodmanPortMapping) UpdatePortMapping(ctx context.Context, req *portmapping.UpdatePortMappingRequest) (*portmapping.PortMappingResult, error) {
	global.APP_LOG.Warn("Podman does not support dynamic port mapping updates", zap.Uint("id", req.ID))

	var portModel provider.Port
	if err := global.APP_DB.First(&portModel, req.ID).Error; err != nil {
		return nil, fmt.Errorf("port mapping not found: %v", err)
	}

	if req.HostPort != portModel.HostPort || req.GuestPort != portModel.GuestPort || req.Protocol != portModel.Protocol {
		return nil, fmt.Errorf("Podman containers do not support dynamic port mapping updates. Port mappings are fixed at container creation time. To change port mappings, you need to recreate the container with new port settings")
	}

	updates := map[string]interface{}{
		"description": req.Description,
		"status":      req.Status,
	}

	if err := global.APP_DB.Model(&portModel).Updates(updates).Error; err != nil {
		return nil, fmt.Errorf("failed to update port mapping: %v", err)
	}

	if err := global.APP_DB.First(&portModel, req.ID).Error; err != nil {
		return nil, fmt.Errorf("failed to get updated port mapping: %v", err)
	}

	providerInfo, err := p.getProvider(portModel.ProviderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider: %v", err)
	}

	result := p.BaseProvider.FromDBModel(&portModel)
	result.HostIP = providerInfo.Endpoint
	result.PublicIP = p.getPublicIP(providerInfo)
	result.MappingMethod = "podman-native"

	global.APP_LOG.Info("Podman port mapping metadata updated successfully", zap.Uint("id", req.ID))
	return result, nil
}

// ListPortMappings 列出Podman端口映射
func (p *PodmanPortMapping) ListPortMappings(ctx context.Context, instanceID string) ([]*portmapping.PortMappingResult, error) {
	var ports []provider.Port
	if err := global.APP_DB.Where("instance_id = ?", instanceID).Find(&ports).Error; err != nil {
		return nil, fmt.Errorf("failed to list port mappings: %v", err)
	}

	var results []*portmapping.PortMappingResult
	for _, port := range ports {
		result := p.BaseProvider.FromDBModel(&port)
		result.MappingMethod = "podman-native"

		if providerInfo, err := p.getProvider(port.ProviderID); err == nil {
			result.HostIP = providerInfo.Endpoint
			result.PublicIP = p.getPublicIP(providerInfo)
		}

		results = append(results, result)
	}

	return results, nil
}

func (p *PodmanPortMapping) validateRequest(req *portmapping.PortMappingRequest) error {
	if req.InstanceID == "" {
		return fmt.Errorf("instance ID is required")
	}
	if req.GuestPort <= 0 || req.GuestPort > 65535 {
		return fmt.Errorf("invalid guest port: %d", req.GuestPort)
	}
	if req.HostPort < 0 || req.HostPort > 65535 {
		return fmt.Errorf("invalid host port: %d", req.HostPort)
	}
	if req.Protocol == "" {
		req.Protocol = "tcp"
	}
	return portmapping.ValidateProtocol(req.Protocol)
}

func (p *PodmanPortMapping) getInstance(instanceID string) (*provider.Instance, error) {
	var instance provider.Instance
	id, err := strconv.ParseUint(instanceID, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid instance ID: %s", instanceID)
	}
	if err := global.APP_DB.First(&instance, uint(id)).Error; err != nil {
		return nil, fmt.Errorf("instance not found: %v", err)
	}
	return &instance, nil
}

func (p *PodmanPortMapping) getProvider(providerID uint) (*provider.Provider, error) {
	var providerInfo provider.Provider
	if err := global.APP_DB.First(&providerInfo, providerID).Error; err != nil {
		return nil, fmt.Errorf("provider not found: %v", err)
	}
	return &providerInfo, nil
}

func (p *PodmanPortMapping) getPublicIP(providerInfo *provider.Provider) string {
	endpoint := providerInfo.PortIP
	if endpoint == "" {
		endpoint = providerInfo.Endpoint
	}
	if endpoint == "" {
		return ""
	}
	if idx := strings.LastIndex(endpoint, ":"); idx > 0 {
		if strings.Count(endpoint, ":") == 1 || endpoint[0] != '[' {
			return endpoint[:idx]
		}
	}
	return endpoint
}

func (p *PodmanPortMapping) createPortMapping(ctx context.Context, instance *provider.Instance, hostPort, guestPort int, protocol string, providerInfo *provider.Provider) error {
	global.APP_LOG.Debug("Creating Podman native port mapping",
		zap.String("instance", instance.Name),
		zap.Int("hostPort", hostPort),
		zap.Int("guestPort", guestPort),
		zap.String("protocol", protocol))

	providerSvc := providerService.GetProviderService()
	providerInstance, exists := providerSvc.GetProviderByID(providerInfo.ID)

	if !exists || !providerInstance.IsConnected() {
		global.APP_LOG.Warn("Provider未连接，使用临时SSH连接",
			zap.Uint("providerId", providerInfo.ID),
			zap.String("providerName", providerInfo.Name))
		return p.createPortMappingWithTempSSH(ctx, instance, hostPort, guestPort, protocol, providerInfo)
	}

	checkCmd := fmt.Sprintf(podmanCLI+" inspect %s --format '{{.State.Status}}'", instance.Name)
	status, err := providerInstance.ExecuteSSHCommand(ctx, checkCmd)
	if err != nil {
		return fmt.Errorf("failed to check container status: %v", err)
	}

	status = strings.TrimSpace(strings.ToLower(status))

	if strings.Contains(status, "running") || strings.Contains(status, "exited") {
		inspectCmd := fmt.Sprintf(podmanCLI+" inspect %s --format '{{.Config.Image}} {{.Config.Cmd}} {{.HostConfig.Memory}} {{.HostConfig.NanoCpus}}'", instance.Name)
		configInfo, err := providerInstance.ExecuteSSHCommand(ctx, inspectCmd)
		if err != nil {
			return fmt.Errorf("failed to get container config: %v", err)
		}

		portsCmd := fmt.Sprintf(podmanCLI+" port %s", instance.Name)
		existingPorts, _ := providerInstance.ExecuteSSHCommand(ctx, portsCmd)

		stopCmd := fmt.Sprintf(podmanCLI+" stop %s", instance.Name)
		_, err = providerInstance.ExecuteSSHCommand(ctx, stopCmd)
		if err != nil {
			global.APP_LOG.Warn("Failed to stop container", zap.Error(err))
		}

		removeCmd := fmt.Sprintf(podmanCLI+" rm %s", instance.Name)
		_, err = providerInstance.ExecuteSSHCommand(ctx, removeCmd)
		if err != nil {
			return fmt.Errorf("failed to remove container: %v", err)
		}

		recreateCmd := p.buildRunCommand(instance, configInfo, existingPorts, hostPort, guestPort, protocol)
		_, err = providerInstance.ExecuteSSHCommand(ctx, recreateCmd)
		if err != nil {
			return fmt.Errorf("failed to recreate container with port mapping: %v", err)
		}

		ensureSubnetIptables(podmanSubnet, func(cmd string) (string, error) {
			return providerInstance.ExecuteSSHCommand(ctx, cmd)
		})

		global.APP_LOG.Debug("Container recreated with new port mapping",
			zap.String("instance", instance.Name),
			zap.Int("hostPort", hostPort),
			zap.Int("guestPort", guestPort))
	} else {
		return fmt.Errorf("container %s is in unexpected state: %s", instance.Name, status)
	}

	return nil
}

func (p *PodmanPortMapping) createPortMappingWithTempSSH(ctx context.Context, instance *provider.Instance, hostPort, guestPort int, protocol string, providerInfo *provider.Provider) error {
	sshClient, err := p.getSSHClient(providerInfo)
	if err != nil {
		return fmt.Errorf("failed to create SSH client: %v", err)
	}
	defer sshClient.Close()

	checkCmd := fmt.Sprintf(podmanCLI+" inspect %s --format '{{.State.Status}}'", instance.Name)
	status, err := sshClient.Execute(checkCmd)
	if err != nil {
		return fmt.Errorf("failed to check container status: %v", err)
	}

	status = strings.TrimSpace(strings.ToLower(status))

	if strings.Contains(status, "running") || strings.Contains(status, "exited") {
		inspectCmd := fmt.Sprintf(podmanCLI+" inspect %s --format '{{.Config.Image}} {{.Config.Cmd}} {{.HostConfig.Memory}} {{.HostConfig.NanoCpus}}'", instance.Name)
		configInfo, err := sshClient.Execute(inspectCmd)
		if err != nil {
			return fmt.Errorf("failed to get container config: %v", err)
		}

		portsCmd := fmt.Sprintf(podmanCLI+" port %s", instance.Name)
		existingPorts, _ := sshClient.Execute(portsCmd)

		stopCmd := fmt.Sprintf(podmanCLI+" stop %s", instance.Name)
		_, err = sshClient.Execute(stopCmd)
		if err != nil {
			global.APP_LOG.Warn("Failed to stop container", zap.Error(err))
		}

		removeCmd := fmt.Sprintf(podmanCLI+" rm %s", instance.Name)
		_, err = sshClient.Execute(removeCmd)
		if err != nil {
			return fmt.Errorf("failed to remove container: %v", err)
		}

		recreateCmd := p.buildRunCommand(instance, configInfo, existingPorts, hostPort, guestPort, protocol)
		_, err = sshClient.Execute(recreateCmd)
		if err != nil {
			return fmt.Errorf("failed to recreate container with port mapping: %v", err)
		}

		ensureSubnetIptables(podmanSubnet, func(cmd string) (string, error) {
			return sshClient.Execute(cmd)
		})
	} else {
		return fmt.Errorf("container %s is in unexpected state: %s", instance.Name, status)
	}

	return nil
}

func (p *PodmanPortMapping) removePortMapping(ctx context.Context, instance *provider.Instance, hostPort, guestPort int, protocol string) error {
	global.APP_LOG.Debug("Removing Podman native port mapping",
		zap.String("instance", instance.Name),
		zap.Int("hostPort", hostPort),
		zap.Int("guestPort", guestPort),
		zap.String("protocol", protocol))

	providerInfo, err := p.getProvider(instance.ProviderID)
	if err != nil {
		return fmt.Errorf("failed to get provider: %v", err)
	}

	sshClient, err := p.getSSHClient(providerInfo)
	if err != nil {
		return fmt.Errorf("failed to create SSH client: %v", err)
	}
	defer sshClient.Close()

	inspectCmd := fmt.Sprintf(podmanCLI+" inspect %s --format '{{.Config.Image}} {{.Config.Cmd}} {{.HostConfig.Memory}} {{.HostConfig.NanoCpus}}'", instance.Name)
	configInfo, err := sshClient.Execute(inspectCmd)
	if err != nil {
		return fmt.Errorf("failed to get container config: %v", err)
	}

	portsCmd := fmt.Sprintf(podmanCLI+" port %s", instance.Name)
	existingPorts, _ := sshClient.Execute(portsCmd)

	filteredPorts := filterPortMappings(existingPorts, hostPort, guestPort, protocol)

	stopCmd := fmt.Sprintf(podmanCLI+" stop %s", instance.Name)
	_, err = sshClient.Execute(stopCmd)
	if err != nil {
		global.APP_LOG.Warn("Failed to stop container", zap.Error(err))
	}

	removeCmd := fmt.Sprintf(podmanCLI+" rm %s", instance.Name)
	_, err = sshClient.Execute(removeCmd)
	if err != nil {
		return fmt.Errorf("failed to remove container: %v", err)
	}

	recreateCmd := p.buildRunCommandWithFilteredPorts(instance, configInfo, filteredPorts)
	_, err = sshClient.Execute(recreateCmd)
	if err != nil {
		return fmt.Errorf("failed to recreate container: %v", err)
	}

	ensureSubnetIptables(podmanSubnet, func(cmd string) (string, error) {
		return sshClient.Execute(cmd)
	})

	return nil
}

func (p *PodmanPortMapping) cleanupPortMapping(ctx context.Context, instance *provider.Instance, hostPort, guestPort int, protocol string) {
	if err := p.removePortMapping(ctx, instance, hostPort, guestPort, protocol); err != nil {
		global.APP_LOG.Error("Failed to cleanup podman port mapping", zap.Error(err))
	}
}

func (p *PodmanPortMapping) getSSHClient(providerInfo *provider.Provider) (*utils.SSHClient, error) {
	var authConfig provider.ProviderAuthConfig
	if providerInfo.AuthConfig != "" {
		if err := json.Unmarshal([]byte(providerInfo.AuthConfig), &authConfig); err != nil {
			return nil, fmt.Errorf("failed to parse auth config: %v", err)
		}
	} else {
		authConfig = provider.ProviderAuthConfig{
			SSH: &provider.SSHConfig{
				Host:       strings.Split(providerInfo.Endpoint, ":")[0],
				Port:       providerInfo.SSHPort,
				Username:   providerInfo.Username,
				Password:   providerInfo.Password,
				KeyContent: providerInfo.SSHKey,
			},
		}
	}

	if authConfig.SSH == nil {
		return nil, fmt.Errorf("SSH configuration not found")
	}

	config := utils.SSHConfig{
		Host:       authConfig.SSH.Host,
		Port:       authConfig.SSH.Port,
		Username:   authConfig.SSH.Username,
		Password:   authConfig.SSH.Password,
		PrivateKey: authConfig.SSH.KeyContent,
	}

	client, err := utils.NewSSHClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create SSH client: %v", err)
	}

	return client, nil
}

// buildRunCommand 构建Podman运行命令（含扩展能力）
func (p *PodmanPortMapping) buildRunCommand(instance *provider.Instance, configInfo, existingPorts string, newHostPort, newGuestPort int, protocol string) string {
	configParts := strings.Fields(strings.TrimSpace(configInfo))
	if len(configParts) < 1 {
		return ""
	}

	image := configParts[0]
	cmd := fmt.Sprintf(podmanCLI+" run -d --name %s", instance.Name)
	cmd += fmt.Sprintf(" --network=%s", podmanNetwork)

	if len(configParts) >= 3 && configParts[2] != "0" {
		cmd += fmt.Sprintf(" --memory=%s", configParts[2])
	}
	if len(configParts) >= 4 && configParts[3] != "0" {
		cmd += fmt.Sprintf(" --cpus=%s", configParts[3])
	}

	if existingPorts != "" {
		lines := strings.Split(strings.TrimSpace(existingPorts), "\n")
		for _, line := range lines {
			if strings.Contains(line, "->") {
				parts := strings.Split(line, "->")
				if len(parts) == 2 {
					hostPart := strings.TrimSpace(parts[0])
					guestPart := strings.TrimSpace(parts[1])
					if strings.Contains(hostPart, ":") {
						hostPortStr := strings.Split(hostPart, ":")[1]
						cmd += fmt.Sprintf(" -p 0.0.0.0:%s:%s", hostPortStr, guestPart)
					}
				}
			}
		}
	}

	if protocol == "both" {
		cmd += fmt.Sprintf(" -p 0.0.0.0:%d:%d/tcp", newHostPort, newGuestPort)
		cmd += fmt.Sprintf(" -p 0.0.0.0:%d:%d/udp", newHostPort, newGuestPort)
	} else {
		cmd += fmt.Sprintf(" -p 0.0.0.0:%d:%d/%s", newHostPort, newGuestPort, protocol)
	}

	// Podman需要NET_ADMIN和NET_RAW才能正确配置iptables转发规则
	cmd += " --cap-add=MKNOD --cap-add=NET_ADMIN --cap-add=NET_RAW"
	cmd += fmt.Sprintf(" %s", image)

	return cmd
}

// buildRunCommandWithFilteredPorts 使用过滤后的端口映射构建Podman运行命令
func (p *PodmanPortMapping) buildRunCommandWithFilteredPorts(instance *provider.Instance, configInfo string, filteredPorts []string) string {
	configParts := strings.Fields(strings.TrimSpace(configInfo))
	if len(configParts) < 1 {
		return ""
	}

	image := configParts[0]
	cmd := fmt.Sprintf(podmanCLI+" run -d --name %s", instance.Name)
	cmd += fmt.Sprintf(" --network=%s", podmanNetwork)

	if len(configParts) >= 3 && configParts[2] != "0" {
		cmd += fmt.Sprintf(" --memory=%s", configParts[2])
	}
	if len(configParts) >= 4 && configParts[3] != "0" {
		cmd += fmt.Sprintf(" --cpus=%s", configParts[3])
	}

	for _, portLine := range filteredPorts {
		if strings.Contains(portLine, "->") {
			parts := strings.Split(portLine, "->")
			if len(parts) == 2 {
				hostPart := strings.TrimSpace(parts[0])
				guestPart := strings.TrimSpace(parts[1])
				if strings.Contains(hostPart, ":") {
					hostPortStr := strings.Split(hostPart, ":")[1]
					cmd += fmt.Sprintf(" -p 0.0.0.0:%s:%s", hostPortStr, guestPart)
				}
			}
		}
	}

	cmd += " --cap-add=MKNOD --cap-add=NET_ADMIN --cap-add=NET_RAW"
	cmd += fmt.Sprintf(" %s", image)

	return cmd
}

// filterPortMappings 过滤端口映射（排除指定端口）
func filterPortMappings(existingPorts string, excludeHostPort, excludeGuestPort int, excludeProtocol string) []string {
	var filtered []string
	if existingPorts == "" {
		return filtered
	}

	lines := strings.Split(strings.TrimSpace(existingPorts), "\n")
	for _, line := range lines {
		if strings.Contains(line, "->") {
			parts := strings.Split(line, "->")
			if len(parts) == 2 {
				hostPart := strings.TrimSpace(parts[0])
				guestPart := strings.TrimSpace(parts[1])

				shouldExclude := false
				if strings.Contains(hostPart, ":") {
					hostPortStr := strings.Split(hostPart, ":")[1]
					if hostPortStr == strconv.Itoa(excludeHostPort) {
						if strings.Contains(guestPart, "/") {
							guestParts := strings.Split(guestPart, "/")
							if len(guestParts) == 2 {
								guestPortStr := guestParts[0]
								protocol := guestParts[1]
								if guestPortStr == strconv.Itoa(excludeGuestPort) {
									if excludeProtocol == "both" {
										shouldExclude = (protocol == "tcp" || protocol == "udp")
									} else if protocol == excludeProtocol {
										shouldExclude = true
									}
								}
							}
						} else if guestPart == strconv.Itoa(excludeGuestPort) {
							shouldExclude = true
						}
					}
				}

				if !shouldExclude {
					filtered = append(filtered, line)
				}
			}
		}
	}

	return filtered
}

// ensureSubnetIptables 确保子网iptables路由规则存在
func ensureSubnetIptables(subnet string, execute func(cmd string) (string, error)) {
	rules := []string{
		fmt.Sprintf("iptables -t nat -C POSTROUTING -s %s ! -d %s -j MASQUERADE 2>/dev/null || iptables -t nat -A POSTROUTING -s %s ! -d %s -j MASQUERADE", subnet, subnet, subnet, subnet),
		fmt.Sprintf("iptables -C FORWARD -s %s -j ACCEPT 2>/dev/null || iptables -A FORWARD -s %s -j ACCEPT", subnet, subnet),
		fmt.Sprintf("iptables -C FORWARD -d %s -j ACCEPT 2>/dev/null || iptables -A FORWARD -d %s -j ACCEPT", subnet, subnet),
	}
	for _, rule := range rules {
		if _, err := execute(rule); err != nil {
			global.APP_LOG.Warn("iptables路由规则设置失败（非致命）",
				zap.String("subnet", subnet),
				zap.Error(err))
		}
	}
}

func init() {
	portmapping.RegisterProvider("podman", func(config *portmapping.ManagerConfig) portmapping.PortMappingProvider {
		return &PodmanPortMapping{
			BaseProvider: portmapping.NewBaseProvider("podman", config),
		}
	})
}
