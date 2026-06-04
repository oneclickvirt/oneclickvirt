package docker

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/model/provider"
	"oneclickvirt/provider/portmapping"

	"go.uber.org/zap"
)

// DockerPortMapping Docker端口映射实现
type DockerPortMapping struct {
	*portmapping.BaseProvider
	cliName string
}

// cli 返回容器运行时CLI名称
func (d *DockerPortMapping) cli() string {
	if d.cliName != "" {
		return d.cliName
	}
	return "docker"
}

// NewContainerPortMapping 创建Container端口映射Provider
func NewContainerPortMapping(providerType, cliName string, config *portmapping.ManagerConfig) portmapping.PortMappingProvider {
	return &DockerPortMapping{
		BaseProvider: portmapping.NewBaseProvider(providerType, config),
		cliName:      cliName,
	}
}

// NewDockerPortMapping 创建Docker端口映射Provider
func NewDockerPortMapping(config *portmapping.ManagerConfig) portmapping.PortMappingProvider {
	return NewContainerPortMapping("docker", "docker", config)
}

// NewOrbstackPortMapping 创建Orbstack端口映射Provider（复用docker CLI）。
func NewOrbstackPortMapping(config *portmapping.ManagerConfig) portmapping.PortMappingProvider {
	return NewContainerPortMapping("orbstack", "docker", config)
}

// containerNetworkName 返回非Docker运行时需要显式指定的IPv4网络名称
func (d *DockerPortMapping) containerNetworkName() string {
	switch d.GetProviderType() {
	case "podman":
		return "podman-net"
	case "containerd":
		return "containerd-net"
	default:
		return "" // docker使用默认bridge，无需指定
	}
}

// containerSubnet 返回容器运行时IPv4子网（用于iptables规则）
func (d *DockerPortMapping) containerSubnet() string {
	switch d.GetProviderType() {
	case "podman", "containerd":
		return "172.20.0.0/16"
	default:
		return ""
	}
}

// ensureSubnetIptables 通过指定的执行函数确保子网iptables路由规则存在（幂等操作）
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

// init 注册Docker端口映射Provider
func init() {
	portmapping.RegisterProvider("docker", func(config *portmapping.ManagerConfig) portmapping.PortMappingProvider {
		return NewDockerPortMapping(config)
	})
	portmapping.RegisterProvider("orbstack", func(config *portmapping.ManagerConfig) portmapping.PortMappingProvider {
		return NewOrbstackPortMapping(config)
	})
}
