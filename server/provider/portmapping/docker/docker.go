package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"oneclickvirt/global"
	"oneclickvirt/model/provider"
	"oneclickvirt/provider/portmapping"
	providerService "oneclickvirt/service/provider"
	"oneclickvirt/utils"
	"strconv"
	"strings"

	"go.uber.org/zap"
)

// DockerPortMapping Docker端口映射实现
type DockerPortMapping struct {
	*portmapping.BaseProvider
}

// NewDockerPortMapping 创建Docker端口映射Provider
func NewDockerPortMapping(config *portmapping.ManagerConfig) portmapping.PortMappingProvider {
	return &DockerPortMapping{
		BaseProvider: portmapping.NewBaseProvider("docker", config),
	}
}

// SupportsDynamicMapping Docker不支持动态端口映射
func (d *DockerPortMapping) SupportsDynamicMapping() bool {
	return false
}

// CreatePortMapping 创建Docker端口映射
func (d *DockerPortMapping) CreatePortMapping(ctx context.Context, req *portmapping.PortMappingRequest) (*portmapping.PortMappingResult, error) {
	global.APP_LOG.Info("Creating Docker port mapping",
		zap.String("instanceId", req.InstanceID),
		zap.Int("hostPort", req.HostPort),
		zap.Int("guestPort", req.GuestPort),
		zap.String("protocol", req.Protocol))

	// 验证请求参数
	if err := d.validateRequest(req); err != nil {
		return nil, fmt.Errorf("invalid request: %v", err)
	}

	// 获取实例信息
	instance, err := d.getInstance(req.InstanceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get instance: %v", err)
	}

	// 获取Provider信息
	providerInfo, err := d.getProvider(req.ProviderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider: %v", err)
	}

	// 分配端口
	hostPort := req.HostPort
	if hostPort == 0 {
		hostPort, err = d.BaseProvider.AllocatePort(ctx, req.ProviderID, 0)
		if err != nil {
			return nil, fmt.Errorf("failed to allocate port: %v", err)
		}
	}

	// 使用Docker原生端口映射方法
	if err := d.createDockerPortMapping(ctx, instance, hostPort, req.GuestPort, req.Protocol, providerInfo); err != nil {
		return nil, fmt.Errorf("failed to create docker port mapping: %v", err)
	}

	// 判断是否为SSH端口：优先使用请求中的IsSSH字段，否则根据GuestPort判断
	isSSH := req.GuestPort == 22
	if req.IsSSH != nil {
		isSSH = *req.IsSSH
	}

	// 保存到数据库
	result := &portmapping.PortMappingResult{
		InstanceID:    req.InstanceID,
		ProviderID:    req.ProviderID,
		Protocol:      strings.ToLower(req.Protocol),
		HostPort:      hostPort,
		GuestPort:     req.GuestPort,
		HostIP:        providerInfo.Endpoint, // 使用Provider的endpoint作为主机IP
		PublicIP:      d.getPublicIP(providerInfo),
		IPv6Address:   req.IPv6Address,
		Status:        "active",
		Description:   req.Description,
		MappingMethod: "docker-native",
		IsSSH:         isSSH,
		IsAutomatic:   req.HostPort == 0,
	}

	// 转换为数据库模型并保存
	portModel := d.BaseProvider.ToDBModel(result)
	if err := global.APP_DB.Create(portModel).Error; err != nil {
		global.APP_LOG.Error("Failed to save port mapping to database", zap.Error(err))
		// 尝试清理已创建的Docker端口映射
		d.cleanupDockerPortMapping(ctx, instance, hostPort, req.GuestPort, req.Protocol)
		return nil, fmt.Errorf("failed to save port mapping: %v", err)
	}

	result.ID = portModel.ID
	result.CreatedAt = portModel.CreatedAt.Format("2006-01-02T15:04:05Z07:00")
	result.UpdatedAt = portModel.UpdatedAt.Format("2006-01-02T15:04:05Z07:00")

	global.APP_LOG.Info("Docker port mapping created successfully",
		zap.Uint("id", result.ID),
		zap.Int("hostPort", hostPort),
		zap.Int("guestPort", req.GuestPort))

	return result, nil
}

// DeletePortMapping 删除Docker端口映射
func (d *DockerPortMapping) DeletePortMapping(ctx context.Context, req *portmapping.DeletePortMappingRequest) error {
	global.APP_LOG.Info("Deleting Docker port mapping",
		zap.Uint("id", req.ID),
		zap.String("instanceId", req.InstanceID))

	// 获取端口映射信息
	var portModel provider.Port
	if err := global.APP_DB.First(&portModel, req.ID).Error; err != nil {
		return fmt.Errorf("port mapping not found: %v", err)
	}

	// 获取实例信息
	instance, err := d.getInstance(req.InstanceID)
	if err != nil {
		return fmt.Errorf("failed to get instance: %v", err)
	}

	// 删除Docker端口映射
	if err := d.removeDockerPortMapping(ctx, instance, portModel.HostPort, portModel.GuestPort, portModel.Protocol); err != nil {
		if !req.ForceDelete {
			return fmt.Errorf("failed to remove docker port mapping: %v", err)
		}
		global.APP_LOG.Warn("Failed to remove docker port mapping, but force delete is enabled", zap.Error(err))
	}

	// 从数据库删除
	if err := global.APP_DB.Delete(&portModel).Error; err != nil {
		return fmt.Errorf("failed to delete port mapping from database: %v", err)
	}

	global.APP_LOG.Info("Docker port mapping deleted successfully", zap.Uint("id", req.ID))
	return nil
}

// UpdatePortMapping Docker不支持动态端口映射更新
func (d *DockerPortMapping) UpdatePortMapping(ctx context.Context, req *portmapping.UpdatePortMappingRequest) (*portmapping.PortMappingResult, error) {
	global.APP_LOG.Warn("Docker does not support dynamic port mapping updates", zap.Uint("id", req.ID))

	// 获取现有端口映射
	var portModel provider.Port
	if err := global.APP_DB.First(&portModel, req.ID).Error; err != nil {
		return nil, fmt.Errorf("port mapping not found: %v", err)
	}

	// 检查是否尝试修改端口配置
	if req.HostPort != portModel.HostPort || req.GuestPort != portModel.GuestPort || req.Protocol != portModel.Protocol {
		return nil, fmt.Errorf("Docker containers do not support dynamic port mapping updates. Port mappings are fixed at container creation time. To change port mappings, you need to recreate the container with new port settings")
	}

	// 只允许更新描述和状态等非端口相关字段
	updates := map[string]interface{}{
		"description": req.Description,
		"status":      req.Status,
	}

	if err := global.APP_DB.Model(&portModel).Updates(updates).Error; err != nil {
		return nil, fmt.Errorf("failed to update port mapping: %v", err)
	}

	// 重新获取更新后的记录
	if err := global.APP_DB.First(&portModel, req.ID).Error; err != nil {
		return nil, fmt.Errorf("failed to get updated port mapping: %v", err)
	}

	// 获取Provider信息
	providerInfo, err := d.getProvider(portModel.ProviderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider: %v", err)
	}

	result := d.BaseProvider.FromDBModel(&portModel)
	result.HostIP = providerInfo.Endpoint
	result.PublicIP = d.getPublicIP(providerInfo)
	result.MappingMethod = "docker-native"

	global.APP_LOG.Info("Docker port mapping metadata updated successfully", zap.Uint("id", req.ID))
	return result, nil
}

// ListPortMappings 列出Docker端口映射
func (d *DockerPortMapping) ListPortMappings(ctx context.Context, instanceID string) ([]*portmapping.PortMappingResult, error) {
	var ports []provider.Port
	if err := global.APP_DB.Where("instance_id = ?", instanceID).Find(&ports).Error; err != nil {
		return nil, fmt.Errorf("failed to list port mappings: %v", err)
	}

	var results []*portmapping.PortMappingResult
	for _, port := range ports {
		result := d.BaseProvider.FromDBModel(&port)
		result.MappingMethod = "docker-native"

		// 获取Provider信息以填充IP地址
		if providerInfo, err := d.getProvider(port.ProviderID); err == nil {
			result.HostIP = providerInfo.Endpoint
			result.PublicIP = d.getPublicIP(providerInfo)
		}

		results = append(results, result)
	}

	return results, nil
}

// validateRequest 验证请求参数
func (d *DockerPortMapping) validateRequest(req *portmapping.PortMappingRequest) error {
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

// getInstance 获取实例信息
func (d *DockerPortMapping) getInstance(instanceID string) (*provider.Instance, error) {
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

// getProvider 获取Provider信息
func (d *DockerPortMapping) getProvider(providerID uint) (*provider.Provider, error) {
	var providerInfo provider.Provider
	if err := global.APP_DB.First(&providerInfo, providerID).Error; err != nil {
		return nil, fmt.Errorf("provider not found: %v", err)
	}
	return &providerInfo, nil
}

// getPublicIP 获取公网IP
func (d *DockerPortMapping) getPublicIP(providerInfo *provider.Provider) string {
	// 优先使用PortIP（端口映射专用IP），如果为空则使用Endpoint（SSH地址）
	endpoint := providerInfo.PortIP
	if endpoint == "" {
		endpoint = providerInfo.Endpoint
	}

	if endpoint == "" {
		return ""
	}

	// 如果endpoint包含端口，去掉端口部分
	if idx := strings.LastIndex(endpoint, ":"); idx > 0 {
		if strings.Count(endpoint, ":") == 1 || endpoint[0] != '[' {
			// IPv4 with port or IPv6 without brackets
			return endpoint[:idx]
		}
	}

	return endpoint
}

// createDockerPortMapping 创建Docker原生端口映射
func (d *DockerPortMapping) createDockerPortMapping(ctx context.Context, instance *provider.Instance, hostPort, guestPort int, protocol string, providerInfo *provider.Provider) error {
	global.APP_LOG.Info("Creating Docker native port mapping",
		zap.String("instance", instance.Name),
		zap.Int("hostPort", hostPort),
		zap.Int("guestPort", guestPort),
		zap.String("protocol", protocol))

	// 尝试从ProviderService获取Provider实例，以使用其SSH连接
	providerSvc := providerService.GetProviderService()
	providerInstance, exists := providerSvc.GetProviderByID(providerInfo.ID)

	if !exists || !providerInstance.IsConnected() {
		// 如果Provider未加载或未连接，回退到创建临时SSH连接
		global.APP_LOG.Warn("Provider未连接，使用临时SSH连接",
			zap.Uint("providerId", providerInfo.ID),
			zap.String("providerName", providerInfo.Name))
		return d.createDockerPortMappingWithTempSSH(ctx, instance, hostPort, guestPort, protocol, providerInfo)
	}

	// 使用Provider实例的SSH连接执行命令
	global.APP_LOG.Debug("使用Provider实例执行端口映射命令",
		zap.Uint("providerId", providerInfo.ID),
		zap.String("providerName", providerInfo.Name))

	// 检查容器是否存在
	checkCmd := fmt.Sprintf("docker inspect %s --format '{{.State.Status}}'", instance.Name)
	status, err := providerInstance.ExecuteSSHCommand(ctx, checkCmd)
	if err != nil {
		return fmt.Errorf("failed to check container status: %v", err)
	}

	status = strings.TrimSpace(strings.ToLower(status))

	// Docker不支持动态端口映射，需要重新创建容器
	if strings.Contains(status, "running") || strings.Contains(status, "exited") {
		// 获取现有容器的配置
		inspectCmd := fmt.Sprintf("docker inspect %s --format '{{.Config.Image}} {{.Config.Cmd}} {{.HostConfig.Memory}} {{.HostConfig.NanoCpus}}'", instance.Name)
		configInfo, err := providerInstance.ExecuteSSHCommand(ctx, inspectCmd)
		if err != nil {
			return fmt.Errorf("failed to get container config: %v", err)
		}

		// 获取现有的端口映射
		portsCmd := fmt.Sprintf("docker port %s", instance.Name)
		existingPorts, _ := providerInstance.ExecuteSSHCommand(ctx, portsCmd)

		// 停止并删除现有容器
		stopCmd := fmt.Sprintf("docker stop %s", instance.Name)
		_, err = providerInstance.ExecuteSSHCommand(ctx, stopCmd)
		if err != nil {
			global.APP_LOG.Warn("Failed to stop container", zap.Error(err))
		}

		removeCmd := fmt.Sprintf("docker rm %s", instance.Name)
		_, err = providerInstance.ExecuteSSHCommand(ctx, removeCmd)
		if err != nil {
			return fmt.Errorf("failed to remove container: %v", err)
		}

		// 重新创建容器，包含新的端口映射
		recreateCmd := d.buildDockerRunCommand(instance, configInfo, existingPorts, hostPort, guestPort, protocol)
		_, err = providerInstance.ExecuteSSHCommand(ctx, recreateCmd)
		if err != nil {
			return fmt.Errorf("failed to recreate container with port mapping: %v", err)
		}

		global.APP_LOG.Info("Container recreated with new port mapping",
			zap.String("instance", instance.Name),
			zap.Int("hostPort", hostPort),
			zap.Int("guestPort", guestPort))
	} else {
		return fmt.Errorf("container %s is in unexpected state: %s", instance.Name, status)
	}

	return nil
}

// createDockerPortMappingWithTempSSH 使用临时SSH连接创建Docker端口映射（回退方案）
func (d *DockerPortMapping) createDockerPortMappingWithTempSSH(ctx context.Context, instance *provider.Instance, hostPort, guestPort int, protocol string, providerInfo *provider.Provider) error {
	global.APP_LOG.Warn("使用临时SSH连接创建端口映射（回退方案）",
		zap.Uint("providerId", providerInfo.ID),
		zap.String("providerName", providerInfo.Name))

	// 构建SSH连接
	sshClient, err := d.getSSHClient(providerInfo)
	if err != nil {
		return fmt.Errorf("failed to create SSH client: %v", err)
	}
	defer sshClient.Close()

	// 检查容器是否存在
	checkCmd := fmt.Sprintf("docker inspect %s --format '{{.State.Status}}'", instance.Name)
	status, err := sshClient.Execute(checkCmd)
	if err != nil {
		return fmt.Errorf("failed to check container status: %v", err)
	}

	status = strings.TrimSpace(strings.ToLower(status))

	// Docker不支持动态端口映射，需要重新创建容器
	if strings.Contains(status, "running") || strings.Contains(status, "exited") {
		// 获取现有容器的配置
		inspectCmd := fmt.Sprintf("docker inspect %s --format '{{.Config.Image}} {{.Config.Cmd}} {{.HostConfig.Memory}} {{.HostConfig.NanoCpus}}'", instance.Name)
		configInfo, err := sshClient.Execute(inspectCmd)
		if err != nil {
			return fmt.Errorf("failed to get container config: %v", err)
		}

		// 获取现有的端口映射
		portsCmd := fmt.Sprintf("docker port %s", instance.Name)
		existingPorts, _ := sshClient.Execute(portsCmd)

		// 停止并删除现有容器
		stopCmd := fmt.Sprintf("docker stop %s", instance.Name)
		_, err = sshClient.Execute(stopCmd)
		if err != nil {
			global.APP_LOG.Warn("Failed to stop container", zap.Error(err))
		}

		removeCmd := fmt.Sprintf("docker rm %s", instance.Name)
		_, err = sshClient.Execute(removeCmd)
		if err != nil {
			return fmt.Errorf("failed to remove container: %v", err)
		}

		// 重新创建容器，包含新的端口映射
		recreateCmd := d.buildDockerRunCommand(instance, configInfo, existingPorts, hostPort, guestPort, protocol)
		_, err = sshClient.Execute(recreateCmd)
		if err != nil {
			return fmt.Errorf("failed to recreate container with port mapping: %v", err)
		}

		global.APP_LOG.Info("Container recreated with new port mapping",
			zap.String("instance", instance.Name),
			zap.Int("hostPort", hostPort),
			zap.Int("guestPort", guestPort))
	} else {
		return fmt.Errorf("container %s is in unexpected state: %s", instance.Name, status)
	}

	return nil
}

// getSSHClient 获取SSH客户端
func (d *DockerPortMapping) getSSHClient(providerInfo *provider.Provider) (*utils.SSHClient, error) {
	// 解析认证配置
	var authConfig provider.ProviderAuthConfig
	if providerInfo.AuthConfig != "" {
		if err := json.Unmarshal([]byte(providerInfo.AuthConfig), &authConfig); err != nil {
			return nil, fmt.Errorf("failed to parse auth config: %v", err)
		}
	} else {
		// 使用基础配置
		authConfig = provider.ProviderAuthConfig{
			SSH: &provider.SSHConfig{
				Host:       strings.Split(providerInfo.Endpoint, ":")[0], // 从endpoint提取host
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

	// 创建SSH配置
	config := utils.SSHConfig{
		Host:       authConfig.SSH.Host,
		Port:       authConfig.SSH.Port,
		Username:   authConfig.SSH.Username,
		Password:   authConfig.SSH.Password,
		PrivateKey: authConfig.SSH.KeyContent,
	}

	// 创建SSH客户端
	client, err := utils.NewSSHClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create SSH client: %v", err)
	}

	return client, nil
}

// buildDockerRunCommand 构建Docker运行命令
func (d *DockerPortMapping) buildDockerRunCommand(instance *provider.Instance, configInfo, existingPorts string, newHostPort, newGuestPort int, protocol string) string {
	// 解析配置信息
	configParts := strings.Fields(strings.TrimSpace(configInfo))
	if len(configParts) < 1 {
		return ""
	}

	image := configParts[0]

	// 构建基础命令
	cmd := fmt.Sprintf("docker run -d --name %s", instance.Name)

	// 资源限制（如果有的话）
	if len(configParts) >= 3 && configParts[2] != "0" {
		cmd += fmt.Sprintf(" --memory=%s", configParts[2])
	}
	if len(configParts) >= 4 && configParts[3] != "0" {
		nanoCpus := configParts[3]
		if nanoCpus != "0" {
			// 转换纳秒CPU到CPU核心数
			cmd += fmt.Sprintf(" --cpus=%s", nanoCpus)
		}
	}

	// 现有的端口映射
	if existingPorts != "" {
		lines := strings.Split(strings.TrimSpace(existingPorts), "\n")
		for _, line := range lines {
			if strings.Contains(line, "->") {
				parts := strings.Split(line, "->")
				if len(parts) == 2 {
					hostPart := strings.TrimSpace(parts[0])
					guestPart := strings.TrimSpace(parts[1])

					// 解析主机端口
					if strings.Contains(hostPart, ":") {
						hostPortStr := strings.Split(hostPart, ":")[1]
						// 只映射IPv4端口，明确指定0.0.0.0
						cmd += fmt.Sprintf(" -p 0.0.0.0:%s:%s", hostPortStr, guestPart)
					}
				}
			}
		}
	}

	// 新的端口映射 - 只映射IPv4端口
	// 如果协议是 both，需要创建两个端口映射（tcp 和 udp）
	if protocol == "both" {
		cmd += fmt.Sprintf(" -p 0.0.0.0:%d:%d/tcp", newHostPort, newGuestPort)
		cmd += fmt.Sprintf(" -p 0.0.0.0:%d:%d/udp", newHostPort, newGuestPort)
	} else {
		cmd += fmt.Sprintf(" -p 0.0.0.0:%d:%d/%s", newHostPort, newGuestPort, protocol)
	}

	// 必要的能力
	cmd += " --cap-add=MKNOD"

	// 镜像
	cmd += fmt.Sprintf(" %s", image)

	return cmd
}

// removeDockerPortMapping 删除Docker原生端口映射
func (d *DockerPortMapping) removeDockerPortMapping(ctx context.Context, instance *provider.Instance, hostPort, guestPort int, protocol string) error {
	global.APP_LOG.Info("Removing Docker native port mapping",
		zap.String("instance", instance.Name),
		zap.Int("hostPort", hostPort),
		zap.Int("guestPort", guestPort),
		zap.String("protocol", protocol))

	// 获取Provider信息
	providerInfo, err := d.getProvider(instance.ProviderID)
	if err != nil {
		return fmt.Errorf("failed to get provider: %v", err)
	}

	// 构建SSH连接
	sshClient, err := d.getSSHClient(providerInfo)
	if err != nil {
		return fmt.Errorf("failed to create SSH client: %v", err)
	}
	defer sshClient.Close()

	// Docker不支持动态移除端口映射，需要重新创建容器（不包含该端口映射）
	// 获取现有容器的配置
	inspectCmd := fmt.Sprintf("docker inspect %s --format '{{.Config.Image}} {{.Config.Cmd}} {{.HostConfig.Memory}} {{.HostConfig.NanoCpus}}'", instance.Name)
	configInfo, err := sshClient.Execute(inspectCmd)
	if err != nil {
		return fmt.Errorf("failed to get container config: %v", err)
	}

	// 获取现有的端口映射（排除要删除的）
	portsCmd := fmt.Sprintf("docker port %s", instance.Name)
	existingPorts, _ := sshClient.Execute(portsCmd)

	// 过滤掉要删除的端口映射
	filteredPorts := d.filterPortMappings(existingPorts, hostPort, guestPort, protocol)

	// 停止并删除现有容器
	stopCmd := fmt.Sprintf("docker stop %s", instance.Name)
	_, err = sshClient.Execute(stopCmd)
	if err != nil {
		global.APP_LOG.Warn("Failed to stop container", zap.Error(err))
	}

	removeCmd := fmt.Sprintf("docker rm %s", instance.Name)
	_, err = sshClient.Execute(removeCmd)
	if err != nil {
		return fmt.Errorf("failed to remove container: %v", err)
	}

	// 重新创建容器（不包含被删除的端口映射）
	recreateCmd := d.buildDockerRunCommandWithFilteredPorts(instance, configInfo, filteredPorts)
	_, err = sshClient.Execute(recreateCmd)
	if err != nil {
		return fmt.Errorf("failed to recreate container: %v", err)
	}

	return nil
}

// filterPortMappings 过滤端口映射
func (d *DockerPortMapping) filterPortMappings(existingPorts string, excludeHostPort, excludeGuestPort int, excludeProtocol string) []string {
	var filtered []string

	if existingPorts == "" {
		return filtered
	}

	lines := strings.Split(strings.TrimSpace(existingPorts), "\n")
	for _, line := range lines {
		if strings.Contains(line, "->") {
			// 解析端口映射
			parts := strings.Split(line, "->")
			if len(parts) == 2 {
				hostPart := strings.TrimSpace(parts[0])
				guestPart := strings.TrimSpace(parts[1])

				// 检查是否是要排除的端口映射
				shouldExclude := false
				if strings.Contains(hostPart, ":") {
					hostPortStr := strings.Split(hostPart, ":")[1]
					if hostPortStr == strconv.Itoa(excludeHostPort) {
						// 进一步检查guest端口和协议
						if strings.Contains(guestPart, "/") {
							guestParts := strings.Split(guestPart, "/")
							if len(guestParts) == 2 {
								guestPortStr := guestParts[0]
								protocol := guestParts[1]
								// 如果 excludeProtocol 是 "both"，需要排除 tcp 和 udp 两条规则
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

// buildDockerRunCommandWithFilteredPorts 使用过滤后的端口映射构建Docker运行命令
func (d *DockerPortMapping) buildDockerRunCommandWithFilteredPorts(instance *provider.Instance, configInfo string, filteredPorts []string) string {
	// 解析配置信息
	configParts := strings.Fields(strings.TrimSpace(configInfo))
	if len(configParts) < 1 {
		return ""
	}

	image := configParts[0]

	// 构建基础命令
	cmd := fmt.Sprintf("docker run -d --name %s", instance.Name)

	// 资源限制
	if len(configParts) >= 3 && configParts[2] != "0" {
		cmd += fmt.Sprintf(" --memory=%s", configParts[2])
	}
	if len(configParts) >= 4 && configParts[3] != "0" {
		cmd += fmt.Sprintf(" --cpus=%s", configParts[3])
	}

	// 过滤后的端口映射
	for _, portLine := range filteredPorts {
		if strings.Contains(portLine, "->") {
			parts := strings.Split(portLine, "->")
			if len(parts) == 2 {
				hostPart := strings.TrimSpace(parts[0])
				guestPart := strings.TrimSpace(parts[1])

				if strings.Contains(hostPart, ":") {
					hostPortStr := strings.Split(hostPart, ":")[1]
					// 只映射IPv4端口，明确指定0.0.0.0
					cmd += fmt.Sprintf(" -p 0.0.0.0:%s:%s", hostPortStr, guestPart)
				}
			}
		}
	}

	// 必要的能力
	cmd += " --cap-add=MKNOD"

	// 镜像
	cmd += fmt.Sprintf(" %s", image)

	return cmd
}

// cleanupDockerPortMapping 清理Docker端口映射（在出错时调用）
func (d *DockerPortMapping) cleanupDockerPortMapping(ctx context.Context, instance *provider.Instance, hostPort, guestPort int, protocol string) {
	if err := d.removeDockerPortMapping(ctx, instance, hostPort, guestPort, protocol); err != nil {
		global.APP_LOG.Error("Failed to cleanup docker port mapping", zap.Error(err))
	}
}

// init 注册Docker端口映射Provider
func init() {
	portmapping.RegisterProvider("docker", func(config *portmapping.ManagerConfig) portmapping.PortMappingProvider {
		return NewDockerPortMapping(config)
	})
}
