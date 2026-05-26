package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/model/provider"
	providerService "oneclickvirt/service/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

func (d *DockerPortMapping) createDockerPortMapping(ctx context.Context, instance *provider.Instance, hostPort, guestPort int, protocol string, providerInfo *provider.Provider) error {
	global.APP_LOG.Debug("Creating Docker native port mapping",
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
	checkCmd := fmt.Sprintf(d.cli()+" inspect %s --format '{{.State.Status}}'", instance.Name)
	status, err := providerInstance.ExecuteSSHCommand(ctx, checkCmd)
	if err != nil {
		return fmt.Errorf("failed to check container status: %v", err)
	}

	status = strings.TrimSpace(strings.ToLower(status))

	// Docker不支持动态端口映射，需要重新创建容器
	if strings.Contains(status, "running") || strings.Contains(status, "exited") {
		// 获取现有容器的配置
		inspectCmd := fmt.Sprintf(d.cli()+" inspect %s --format '{{.Config.Image}} {{.Config.Cmd}} {{.HostConfig.Memory}} {{.HostConfig.NanoCpus}}'", instance.Name)
		configInfo, err := providerInstance.ExecuteSSHCommand(ctx, inspectCmd)
		if err != nil {
			return fmt.Errorf("failed to get container config: %v", err)
		}

		// 获取现有的端口映射
		portsCmd := fmt.Sprintf(d.cli()+" port %s", instance.Name)
		existingPorts, _ := providerInstance.ExecuteSSHCommand(ctx, portsCmd)

		// 停止并删除现有容器
		stopCmd := fmt.Sprintf(d.cli()+" stop %s", instance.Name)
		_, err = providerInstance.ExecuteSSHCommand(ctx, stopCmd)
		if err != nil {
			global.APP_LOG.Warn("Failed to stop container", zap.Error(err))
		}

		removeCmd := fmt.Sprintf(d.cli()+" rm %s", instance.Name)
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

		// 对于podman/containerd，确保iptables路由规则存在（幂等操作，规则已存在时直接跳过）
		if subnet := d.containerSubnet(); subnet != "" {
			ensureSubnetIptables(subnet, func(cmd string) (string, error) {
				return providerInstance.ExecuteSSHCommand(ctx, cmd)
			})
		}

		global.APP_LOG.Debug("Container recreated with new port mapping",
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
	checkCmd := fmt.Sprintf(d.cli()+" inspect %s --format '{{.State.Status}}'", instance.Name)
	status, err := sshClient.Execute(checkCmd)
	if err != nil {
		return fmt.Errorf("failed to check container status: %v", err)
	}

	status = strings.TrimSpace(strings.ToLower(status))

	// Docker不支持动态端口映射，需要重新创建容器
	if strings.Contains(status, "running") || strings.Contains(status, "exited") {
		// 获取现有容器的配置
		inspectCmd := fmt.Sprintf(d.cli()+" inspect %s --format '{{.Config.Image}} {{.Config.Cmd}} {{.HostConfig.Memory}} {{.HostConfig.NanoCpus}}'", instance.Name)
		configInfo, err := sshClient.Execute(inspectCmd)
		if err != nil {
			return fmt.Errorf("failed to get container config: %v", err)
		}

		// 获取现有的端口映射
		portsCmd := fmt.Sprintf(d.cli()+" port %s", instance.Name)
		existingPorts, _ := sshClient.Execute(portsCmd)

		// 停止并删除现有容器
		stopCmd := fmt.Sprintf(d.cli()+" stop %s", instance.Name)
		_, err = sshClient.Execute(stopCmd)
		if err != nil {
			global.APP_LOG.Warn("Failed to stop container", zap.Error(err))
		}

		removeCmd := fmt.Sprintf(d.cli()+" rm %s", instance.Name)
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

		// 对于podman/containerd，确保iptables路由规则存在（幂等操作，规则已存在时直接跳过）
		if subnet := d.containerSubnet(); subnet != "" {
			ensureSubnetIptables(subnet, func(cmd string) (string, error) {
				return sshClient.Execute(cmd)
			})
		}

		global.APP_LOG.Debug("Container recreated with new port mapping",
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
				Host:       utils.ExtractHost(providerInfo.Endpoint),
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
	cmd := fmt.Sprintf(d.cli()+" run -d --name %s", instance.Name)

	// 网络配置（podman/containerd需要明确指定自定义网络，否则重建后丢失网络配置）
	if networkName := d.containerNetworkName(); networkName != "" {
		cmd += fmt.Sprintf(" --network=%s", networkName)
	}

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
	// Podman需要NET_ADMIN和NET_RAW才能正确配置iptables转发规则
	if d.GetProviderType() == "podman" {
		cmd += " --cap-add=NET_ADMIN --cap-add=NET_RAW"
	}

	// 镜像
	cmd += fmt.Sprintf(" %s", image)

	return cmd
}

// removeDockerPortMapping 删除Docker原生端口映射
func (d *DockerPortMapping) removeDockerPortMapping(ctx context.Context, instance *provider.Instance, hostPort, guestPort int, protocol string) error {
	global.APP_LOG.Debug("Removing Docker native port mapping",
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
	inspectCmd := fmt.Sprintf(d.cli()+" inspect %s --format '{{.Config.Image}} {{.Config.Cmd}} {{.HostConfig.Memory}} {{.HostConfig.NanoCpus}}'", instance.Name)
	configInfo, err := sshClient.Execute(inspectCmd)
	if err != nil {
		return fmt.Errorf("failed to get container config: %v", err)
	}

	// 获取现有的端口映射（排除要删除的）
	portsCmd := fmt.Sprintf(d.cli()+" port %s", instance.Name)
	existingPorts, _ := sshClient.Execute(portsCmd)

	// 过滤掉要删除的端口映射
	filteredPorts := d.filterPortMappings(existingPorts, hostPort, guestPort, protocol)

	// 停止并删除现有容器
	stopCmd := fmt.Sprintf(d.cli()+" stop %s", instance.Name)
	_, err = sshClient.Execute(stopCmd)
	if err != nil {
		global.APP_LOG.Warn("Failed to stop container", zap.Error(err))
	}

	removeCmd := fmt.Sprintf(d.cli()+" rm %s", instance.Name)
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

	// 对于podman/containerd，确保iptables路由规则存在（幂等操作，规则已存在时直接跳过）
	if subnet := d.containerSubnet(); subnet != "" {
		ensureSubnetIptables(subnet, func(cmd string) (string, error) {
			return sshClient.Execute(cmd)
		})
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
	cmd := fmt.Sprintf(d.cli()+" run -d --name %s", instance.Name)

	// 网络配置（podman/containerd需要明确指定自定义网络，否则重建后丢失网络配置）
	if networkName := d.containerNetworkName(); networkName != "" {
		cmd += fmt.Sprintf(" --network=%s", networkName)
	}

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
	// Podman需要NET_ADMIN和NET_RAW才能正确配置iptables转发规则
	if d.GetProviderType() == "podman" {
		cmd += " --cap-add=NET_ADMIN --cap-add=NET_RAW"
	}

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
