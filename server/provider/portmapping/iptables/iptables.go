package iptables

import (
	"context"
	"fmt"
	"oneclickvirt/global"
	"oneclickvirt/model/provider"
	"oneclickvirt/provider/portmapping"
	providerService "oneclickvirt/service/provider"
	"oneclickvirt/utils"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

// IptablesPortMapping iptables端口映射实现
type IptablesPortMapping struct {
	*portmapping.BaseProvider
}

// NewIptablesPortMapping 创建iptables端口映射Provider
func NewIptablesPortMapping(config *portmapping.ManagerConfig) portmapping.PortMappingProvider {
	return &IptablesPortMapping{
		BaseProvider: portmapping.NewBaseProvider("iptables", config),
	}
}

// SupportsDynamicMapping iptables支持动态端口映射
func (i *IptablesPortMapping) SupportsDynamicMapping() bool {
	return true
}

// CreatePortMapping 创建iptables端口映射
func (i *IptablesPortMapping) CreatePortMapping(ctx context.Context, req *portmapping.PortMappingRequest) (*portmapping.PortMappingResult, error) {
	global.APP_LOG.Info("Creating iptables port mapping",
		zap.String("instanceId", req.InstanceID),
		zap.Int("hostPort", req.HostPort),
		zap.Int("guestPort", req.GuestPort),
		zap.String("protocol", req.Protocol))

	// 验证请求参数
	if err := i.validateRequest(req); err != nil {
		return nil, fmt.Errorf("invalid request: %v", err)
	}

	// 获取实例信息
	instance, err := i.getInstance(req.InstanceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get instance: %v", err)
	}

	// 获取Provider信息
	providerInfo, err := i.getProvider(req.ProviderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider: %v", err)
	}

	// 分配端口
	hostPort := req.HostPort
	if hostPort == 0 {
		hostPort, err = i.BaseProvider.AllocatePort(ctx, req.ProviderID, 0)
		if err != nil {
			return nil, fmt.Errorf("failed to allocate port: %v", err)
		}
	}

	// 使用iptables进行端口映射
	if err := i.createIptablesRule(ctx, instance, hostPort, req.GuestPort, req.Protocol, providerInfo); err != nil {
		return nil, fmt.Errorf("failed to create iptables rule: %v", err)
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
		Protocol:      req.Protocol,
		HostPort:      hostPort,
		GuestPort:     req.GuestPort,
		HostIP:        providerInfo.Endpoint,
		PublicIP:      i.getPublicIP(providerInfo),
		IPv6Address:   req.IPv6Address,
		Status:        "active",
		Description:   req.Description,
		MappingMethod: "iptables-nat",
		IsSSH:         isSSH,
		IsAutomatic:   req.HostPort == 0,
	}

	// 转换为数据库模型并保存
	portModel := i.BaseProvider.ToDBModel(result)
	if err := global.APP_DB.Create(portModel).Error; err != nil {
		global.APP_LOG.Error("Failed to save port mapping to database", zap.Error(err))
		// 尝试清理已创建的iptables rule
		i.cleanupIptablesRule(ctx, instance, hostPort, req.GuestPort, req.Protocol)
		return nil, fmt.Errorf("failed to save port mapping: %v", err)
	}

	result.ID = portModel.ID
	result.CreatedAt = portModel.CreatedAt.Format("2006-01-02T15:04:05Z07:00")
	result.UpdatedAt = portModel.UpdatedAt.Format("2006-01-02T15:04:05Z07:00")

	global.APP_LOG.Info("iptables port mapping created successfully",
		zap.Uint("id", result.ID),
		zap.Int("hostPort", hostPort),
		zap.Int("guestPort", req.GuestPort))

	return result, nil
}

// DeletePortMapping 删除iptables端口映射
func (i *IptablesPortMapping) DeletePortMapping(ctx context.Context, req *portmapping.DeletePortMappingRequest) error {
	global.APP_LOG.Info("Deleting iptables port mapping",
		zap.Uint("id", req.ID),
		zap.String("instanceId", req.InstanceID))

	// 获取端口映射信息
	var portModel provider.Port
	if err := global.APP_DB.First(&portModel, req.ID).Error; err != nil {
		return fmt.Errorf("port mapping not found: %v", err)
	}

	// 获取实例信息
	instance, err := i.getInstance(req.InstanceID)
	if err != nil {
		return fmt.Errorf("failed to get instance: %v", err)
	}

	// 删除iptables rule
	if err := i.removeIptablesRule(ctx, instance, portModel.HostPort, portModel.GuestPort, portModel.Protocol); err != nil {
		if !req.ForceDelete {
			return fmt.Errorf("failed to remove iptables rule: %v", err)
		}
		global.APP_LOG.Warn("Failed to remove iptables rule, but force delete is enabled", zap.Error(err))
	}

	// 从数据库删除
	if err := global.APP_DB.Delete(&portModel).Error; err != nil {
		return fmt.Errorf("failed to delete port mapping from database: %v", err)
	}

	global.APP_LOG.Info("iptables port mapping deleted successfully", zap.Uint("id", req.ID))
	return nil
}

// UpdatePortMapping 更新iptables端口映射
func (i *IptablesPortMapping) UpdatePortMapping(ctx context.Context, req *portmapping.UpdatePortMappingRequest) (*portmapping.PortMappingResult, error) {
	global.APP_LOG.Info("Updating iptables port mapping", zap.Uint("id", req.ID))

	// 获取现有端口映射
	var portModel provider.Port
	if err := global.APP_DB.First(&portModel, req.ID).Error; err != nil {
		return nil, fmt.Errorf("port mapping not found: %v", err)
	}

	// 获取实例信息
	instance, err := i.getInstance(req.InstanceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get instance: %v", err)
	}

	// 获取Provider信息
	providerInfo, err := i.getProvider(portModel.ProviderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider: %v", err)
	}

	// 如果端口发生变化，需要重新创建rule
	if req.HostPort != portModel.HostPort || req.GuestPort != portModel.GuestPort || req.Protocol != portModel.Protocol {
		// 删除旧的rule
		if err := i.removeIptablesRule(ctx, instance, portModel.HostPort, portModel.GuestPort, portModel.Protocol); err != nil {
			global.APP_LOG.Warn("Failed to remove old iptables rule", zap.Error(err))
		}

		// 创建新的rule
		if err := i.createIptablesRule(ctx, instance, req.HostPort, req.GuestPort, req.Protocol, providerInfo); err != nil {
			return nil, fmt.Errorf("failed to create new iptables rule: %v", err)
		}
	}

	// 更新数据库记录
	updates := map[string]interface{}{
		"host_port":   req.HostPort,
		"guest_port":  req.GuestPort,
		"protocol":    req.Protocol,
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

	result := i.BaseProvider.FromDBModel(&portModel)
	result.HostIP = providerInfo.Endpoint
	result.PublicIP = i.getPublicIP(providerInfo)
	result.MappingMethod = "iptables-nat"

	global.APP_LOG.Info("iptables port mapping updated successfully", zap.Uint("id", req.ID))
	return result, nil
}

// ListPortMappings 列出iptables端口映射
func (i *IptablesPortMapping) ListPortMappings(ctx context.Context, instanceID string) ([]*portmapping.PortMappingResult, error) {
	var ports []provider.Port
	if err := global.APP_DB.Where("instance_id = ?", instanceID).Find(&ports).Error; err != nil {
		return nil, fmt.Errorf("failed to list port mappings: %v", err)
	}

	var results []*portmapping.PortMappingResult
	for _, port := range ports {
		result := i.BaseProvider.FromDBModel(&port)
		result.MappingMethod = "iptables-nat"

		// 获取Provider信息以填充IP地址
		if providerInfo, err := i.getProvider(port.ProviderID); err == nil {
			result.HostIP = providerInfo.Endpoint
			result.PublicIP = i.getPublicIP(providerInfo)
		}

		results = append(results, result)
	}

	return results, nil
}

// validateRequest 验证请求参数
func (i *IptablesPortMapping) validateRequest(req *portmapping.PortMappingRequest) error {
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
		req.Protocol = "both"
	}
	return portmapping.ValidateProtocol(req.Protocol)
}

// getInstance 获取实例信息
func (i *IptablesPortMapping) getInstance(instanceID string) (*provider.Instance, error) {
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
func (i *IptablesPortMapping) getProvider(providerID uint) (*provider.Provider, error) {
	var providerInfo provider.Provider
	if err := global.APP_DB.First(&providerInfo, providerID).Error; err != nil {
		return nil, fmt.Errorf("provider not found: %v", err)
	}
	return &providerInfo, nil
}

// getPublicIP 获取公网IP
func (i *IptablesPortMapping) getPublicIP(providerInfo *provider.Provider) string {
	// 优先使用PortIP（端口映射专用IP），如果为空则使用Endpoint（SSH地址）
	if providerInfo.PortIP != "" {
		return providerInfo.PortIP
	}
	return providerInfo.Endpoint
}

// createIptablesRule 创建iptables规则
func (i *IptablesPortMapping) createIptablesRule(ctx context.Context, instance *provider.Instance, hostPort, guestPort int, protocol string, providerInfo *provider.Provider) error {
	global.APP_LOG.Info("Creating iptables rule",
		zap.String("instance", instance.Name),
		zap.Int("hostPort", hostPort),
		zap.Int("guestPort", guestPort),
		zap.String("protocol", protocol))

	// 获取实例IP地址
	instanceIP := instance.PrivateIP
	if instanceIP == "" {
		return fmt.Errorf("instance private IP address not found for %s", instance.Name)
	}

	// 如果协议是both，需要同时创建TCP和UDP规则
	protocols := []string{protocol}
	if protocol == "both" {
		protocols = []string{"tcp", "udp"}
	}

	var allCommands []string

	for _, proto := range protocols {
		// 创建PREROUTING DNAT规则 - 将外部端口转发到内部实例
		dnatRule := fmt.Sprintf("iptables -t nat -A PREROUTING -p %s --dport %d -j DNAT --to-destination %s:%d",
			proto, hostPort, instanceIP, guestPort)

		// 创建FORWARD规则 - 允许转发到实例
		forwardRule := fmt.Sprintf("iptables -A FORWARD -p %s -d %s --dport %d -j ACCEPT",
			proto, instanceIP, guestPort)

		// 创建POSTROUTING MASQUERADE规则 - 对来自实例的响应进行SNAT
		masqueradeRule := fmt.Sprintf("iptables -t nat -A POSTROUTING -p %s -s %s --sport %d -j MASQUERADE",
			proto, instanceIP, guestPort)

		allCommands = append(allCommands, dnatRule, forwardRule, masqueradeRule)
	}

	global.APP_LOG.Info("Executing iptables commands",
		zap.String("protocol", protocol),
		zap.Int("commandCount", len(allCommands)))

	// 尝试从ProviderService获取Provider实例，以使用其SSH连接
	providerSvc := providerService.GetProviderService()
	providerInstance, exists := providerSvc.GetProviderByID(providerInfo.ID)

	if !exists || !providerInstance.IsConnected() {
		// 如果Provider未加载或未连接，回退到创建临时SSH连接
		global.APP_LOG.Warn("Provider未连接，使用临时SSH连接",
			zap.Uint("providerId", providerInfo.ID),
			zap.String("providerName", providerInfo.Name))
		return i.createIptablesRuleWithTempSSH(ctx, allCommands, instance, hostPort, guestPort, providerInfo)
	}

	// 使用Provider实例的SSH连接执行命令
	global.APP_LOG.Debug("使用Provider实例执行iptables命令",
		zap.Uint("providerId", providerInfo.ID),
		zap.String("providerName", providerInfo.Name))

	// 执行iptables命令
	for _, cmd := range allCommands {
		_, err := providerInstance.ExecuteSSHCommand(ctx, cmd)
		if err != nil {
			global.APP_LOG.Error("Failed to execute iptables command",
				zap.String("command", cmd),
				zap.Error(err))
			return fmt.Errorf("failed to execute iptables command '%s': %v", cmd, err)
		}
	}

	// 保存iptables规则
	saveCmd := "iptables-save > /etc/iptables/rules.v4 2>/dev/null || true"
	_, err := providerInstance.ExecuteSSHCommand(ctx, saveCmd)
	if err != nil {
		global.APP_LOG.Warn("Failed to save iptables rules", zap.Error(err))
	}

	global.APP_LOG.Info("Successfully created iptables rules",
		zap.String("instance", instance.Name),
		zap.Int("hostPort", hostPort),
		zap.Int("guestPort", guestPort))

	return nil
}

// createIptablesRuleWithTempSSH 使用临时SSH连接创建iptables规则（回退方案）
func (i *IptablesPortMapping) createIptablesRuleWithTempSSH(ctx context.Context, commands []string, instance *provider.Instance, hostPort, guestPort int, providerInfo *provider.Provider) error {
	global.APP_LOG.Warn("使用临时SSH连接创建iptables规则（回退方案）",
		zap.Uint("providerId", providerInfo.ID),
		zap.String("providerName", providerInfo.Name))

	// 创建SSH客户端连接到provider主机执行iptables命令
	sshClient, err := i.createSSHClient(providerInfo)
	if err != nil {
		return fmt.Errorf("failed to create SSH client: %v", err)
	}
	defer sshClient.Close()

	// 执行iptables命令
	for _, cmd := range commands {
		_, err := sshClient.Execute(cmd)
		if err != nil {
			global.APP_LOG.Error("Failed to execute iptables command",
				zap.String("command", cmd),
				zap.Error(err))
			return fmt.Errorf("failed to execute iptables command '%s': %v", cmd, err)
		}
	}

	// 保存iptables规则
	saveCmd := "iptables-save > /etc/iptables/rules.v4 2>/dev/null || true"
	_, err = sshClient.Execute(saveCmd)
	if err != nil {
		global.APP_LOG.Warn("Failed to save iptables rules", zap.Error(err))
	}

	global.APP_LOG.Info("Successfully created iptables rules",
		zap.String("instance", instance.Name),
		zap.Int("hostPort", hostPort),
		zap.Int("guestPort", guestPort))

	return nil
}

// removeIptablesRule 删除iptables规则
func (i *IptablesPortMapping) removeIptablesRule(ctx context.Context, instance *provider.Instance, hostPort, guestPort int, protocol string) error {
	global.APP_LOG.Info("Removing iptables rule",
		zap.String("instance", instance.Name),
		zap.Int("hostPort", hostPort),
		zap.Int("guestPort", guestPort),
		zap.String("protocol", protocol))

	// 获取实例IP地址
	instanceIP := instance.PrivateIP
	if instanceIP == "" {
		return fmt.Errorf("instance private IP address not found for %s", instance.Name)
	}

	// 如果协议是both，需要同时删除TCP和UDP规则
	protocols := []string{protocol}
	if protocol == "both" {
		protocols = []string{"tcp", "udp"}
	}

	var allCommands []string

	for _, proto := range protocols {
		// 删除PREROUTING DNAT规则
		dnatRule := fmt.Sprintf("iptables -t nat -D PREROUTING -p %s --dport %d -j DNAT --to-destination %s:%d",
			proto, hostPort, instanceIP, guestPort)

		// 删除FORWARD规则
		forwardRule := fmt.Sprintf("iptables -D FORWARD -p %s -d %s --dport %d -j ACCEPT",
			proto, instanceIP, guestPort)

		// 删除POSTROUTING MASQUERADE规则
		masqueradeRule := fmt.Sprintf("iptables -t nat -D POSTROUTING -p %s -s %s --sport %d -j MASQUERADE",
			proto, instanceIP, guestPort)

		allCommands = append(allCommands, dnatRule, forwardRule, masqueradeRule)
	}

	global.APP_LOG.Info("Executing iptables removal commands",
		zap.String("protocol", protocol),
		zap.Int("commandCount", len(allCommands)))

	// 获取provider信息以创建SSH连接
	var providerInfo *provider.Provider
	var err error

	// 从实例信息中获取provider ID，然后获取provider信息
	if providerInfo, err = i.getProvider(instance.ProviderID); err != nil {
		return fmt.Errorf("failed to get provider info: %v", err)
	}

	// 创建SSH客户端连接到provider主机执行iptables命令
	sshClient, err := i.createSSHClient(providerInfo)
	if err != nil {
		return fmt.Errorf("failed to create SSH client: %v", err)
	}
	defer sshClient.Close()

	// 执行iptables删除命令
	for _, cmd := range allCommands {
		_, err := sshClient.Execute(cmd)
		if err != nil {
			global.APP_LOG.Warn("Failed to execute iptables removal command",
				zap.String("command", cmd),
				zap.Error(err))
			// 对于删除命令，即使失败也继续执行其他命令
		}
	}

	// 保存iptables规则
	saveCmd := "iptables-save > /etc/iptables/rules.v4 2>/dev/null || true"
	_, err = sshClient.Execute(saveCmd)
	if err != nil {
		global.APP_LOG.Warn("Failed to save iptables rules", zap.Error(err))
	}

	global.APP_LOG.Info("Successfully removed iptables rules",
		zap.String("instance", instance.Name),
		zap.Int("hostPort", hostPort),
		zap.Int("guestPort", guestPort))

	return nil
}

// cleanupIptablesRule 清理iptables规则（在出错时调用）
func (i *IptablesPortMapping) cleanupIptablesRule(ctx context.Context, instance *provider.Instance, hostPort, guestPort int, protocol string) {
	if err := i.removeIptablesRule(ctx, instance, hostPort, guestPort, protocol); err != nil {
		global.APP_LOG.Error("Failed to cleanup iptables rule", zap.Error(err))
	}
}

// createSSHClient 创建SSH客户端连接到provider主机
func (i *IptablesPortMapping) createSSHClient(providerInfo *provider.Provider) (*utils.SSHClient, error) {
	// 解析endpoint获取host和port
	host, port := i.parseEndpoint(providerInfo.Endpoint)

	sshConfig := utils.SSHConfig{
		Host:           host,
		Port:           port,
		Username:       providerInfo.Username,
		Password:       providerInfo.Password,
		PrivateKey:     providerInfo.SSHKey,
		ConnectTimeout: 10 * time.Second,
		ExecuteTimeout: 60 * time.Second,
	}

	return utils.NewSSHClient(sshConfig)
}

// parseEndpoint 解析endpoint获取host和port
func (i *IptablesPortMapping) parseEndpoint(endpoint string) (host string, port int) {
	// 默认SSH端口
	port = 22
	host = endpoint

	// 如果endpoint包含端口，解析它
	if strings.Contains(endpoint, ":") {
		parts := strings.Split(endpoint, ":")
		if len(parts) == 2 {
			host = parts[0]
			if p, err := strconv.Atoi(parts[1]); err == nil {
				port = p
			}
		}
	}

	return host, port
}

// init 注册iptables端口映射Provider
func init() {
	portmapping.RegisterProvider("iptables", func(config *portmapping.ManagerConfig) portmapping.PortMappingProvider {
		return NewIptablesPortMapping(config)
	})
}
