package lxd

import (
	"context"
	"fmt"
	"oneclickvirt/global"
	"oneclickvirt/model/provider"
	"oneclickvirt/provider/portmapping"
	"strconv"

	"go.uber.org/zap"
)

// LXDPortMapping LXD端口映射实现
type LXDPortMapping struct {
	*portmapping.BaseProvider
}

// NewLXDPortMapping 创建LXD端口映射Provider
func NewLXDPortMapping(config *portmapping.ManagerConfig) portmapping.PortMappingProvider {
	return &LXDPortMapping{
		BaseProvider: portmapping.NewBaseProvider("lxd", config),
	}
}

// SupportsDynamicMapping LXD支持动态端口映射
func (l *LXDPortMapping) SupportsDynamicMapping() bool {
	return true
}

// CreatePortMapping 创建LXD端口映射
func (l *LXDPortMapping) CreatePortMapping(ctx context.Context, req *portmapping.PortMappingRequest) (*portmapping.PortMappingResult, error) {
	global.APP_LOG.Info("Creating LXD port mapping",
		zap.String("instanceId", req.InstanceID),
		zap.Int("hostPort", req.HostPort),
		zap.Int("guestPort", req.GuestPort),
		zap.String("protocol", req.Protocol))

	// 验证请求参数
	if err := l.validateRequest(req); err != nil {
		return nil, fmt.Errorf("invalid request: %v", err)
	}

	// 获取Provider信息
	providerInfo, err := l.getProvider(req.ProviderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider: %v", err)
	}
	instance, err := l.getInstance(req.InstanceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get instance: %v", err)
	}
	if instance.ProviderID != req.ProviderID {
		return nil, fmt.Errorf("instance does not belong to provider")
	}

	// 分配端口
	hostPort := req.HostPort
	if hostPort == 0 {
		hostPort, err = l.BaseProvider.AllocatePort(ctx, req.ProviderID, 0)
		if err != nil {
			return nil, fmt.Errorf("failed to allocate port: %v", err)
		}
	}

	// LXD端口映射（proxy device）由provider层的configurePortMappingsWithIP函数处理
	// 这里只负责数据库记录的管理

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
		HostPortEnd:   req.HostPortEnd,
		GuestPortEnd:  req.GuestPortEnd,
		PortCount:     normalizedPortCount(req.PortCount),
		HostIP:        providerInfo.Endpoint,
		PublicIP:      l.getPublicIP(providerInfo),
		IPv6Address:   req.IPv6Address,
		IPv6Enabled:   req.IPv6Enabled || req.IPv6Address != "",
		Status:        "active",
		Description:   req.Description,
		MappingMethod: l.determineMappingMethod(req, providerInfo),
		MappingType:   defaultMappingType(req.MappingType),
		InternalHost:  req.InternalHost,
		IsSSH:         isSSH,
		IsAutomatic:   req.HostPort == 0,
	}

	// 转换为数据库模型并保存
	portModel := l.BaseProvider.ToDBModel(result)
	if err := global.APP_DB.Create(portModel).Error; err != nil {
		global.APP_LOG.Error("Failed to save port mapping to database", zap.Error(err))
		return nil, fmt.Errorf("failed to save port mapping: %v", err)
	}

	result.ID = portModel.ID
	result.CreatedAt = portModel.CreatedAt.Format("2006-01-02T15:04:05Z07:00")
	result.UpdatedAt = portModel.UpdatedAt.Format("2006-01-02T15:04:05Z07:00")

	global.APP_LOG.Info("LXD port mapping created successfully",
		zap.Uint("id", result.ID),
		zap.Int("hostPort", hostPort),
		zap.Int("guestPort", req.GuestPort))

	return result, nil
}

// DeletePortMapping 删除LXD端口映射
func (l *LXDPortMapping) DeletePortMapping(ctx context.Context, req *portmapping.DeletePortMappingRequest) error {
	global.APP_LOG.Info("Deleting LXD port mapping",
		zap.Uint("id", req.ID),
		zap.String("instanceId", req.InstanceID))

	// 获取端口映射信息
	portModel, err := l.BaseProvider.LoadOwnedPort(req.ID, req.InstanceID, 0)
	if err != nil {
		return err
	}

	// LXD proxy device的删除由provider层处理，这里只管理数据库记录

	// 从数据库删除
	if err := global.APP_DB.Delete(portModel).Error; err != nil {
		return fmt.Errorf("failed to delete port mapping from database: %v", err)
	}

	global.APP_LOG.Info("LXD port mapping deleted successfully", zap.Uint("id", req.ID))
	return nil
}

// UpdatePortMapping 更新LXD端口映射
func (l *LXDPortMapping) UpdatePortMapping(ctx context.Context, req *portmapping.UpdatePortMappingRequest) (*portmapping.PortMappingResult, error) {
	global.APP_LOG.Info("Updating LXD port mapping", zap.Uint("id", req.ID))

	// 获取现有端口映射
	portModel, err := l.BaseProvider.LoadOwnedPort(req.ID, req.InstanceID, 0)
	if err != nil {
		return nil, err
	}

	// 获取Provider信息
	providerInfo, err := l.getProvider(portModel.ProviderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider: %v", err)
	}

	// 如果端口发生变化，LXD proxy device的重建由provider层处理
	// 这里只更新数据库记录

	// 更新数据库记录
	if req.Protocol == "" {
		req.Protocol = portModel.Protocol
	}
	if req.Status == "" {
		req.Status = portModel.Status
	}
	if req.MappingMethod == "" {
		req.MappingMethod = portModel.MappingMethod
	}
	if req.MappingType == "" {
		req.MappingType = portModel.MappingType
	}
	if req.PortCount == 0 {
		req.PortCount = normalizedPortCount(portModel.PortCount)
	}
	if req.HostPortEnd == 0 && portModel.HostPortEnd > 0 {
		req.HostPortEnd = portModel.HostPortEnd
	}
	if req.GuestPortEnd == 0 && portModel.GuestPortEnd > 0 {
		req.GuestPortEnd = portModel.GuestPortEnd
	}
	if req.IPv6Address == "" && portModel.IPv6Address != "" {
		req.IPv6Address = portModel.IPv6Address
	}
	if err := l.validateUpdateRequest(req); err != nil {
		return nil, err
	}
	updates := map[string]interface{}{
		"host_port":      req.HostPort,
		"host_port_end":  req.HostPortEnd,
		"guest_port":     req.GuestPort,
		"guest_port_end": req.GuestPortEnd,
		"port_count":     normalizedPortCount(req.PortCount),
		"protocol":       req.Protocol,
		"description":    req.Description,
		"status":         req.Status,
		"ipv6_enabled":   req.IPv6Enabled,
		"ipv6_address":   req.IPv6Address,
		"mapping_method": req.MappingMethod,
		"mapping_type":   req.MappingType,
		"internal_host":  req.InternalHost,
	}

	if err := global.APP_DB.Model(portModel).Updates(updates).Error; err != nil {
		return nil, fmt.Errorf("failed to update port mapping: %v", err)
	}

	// 重新获取更新后的记录
	if err := global.APP_DB.First(portModel, req.ID).Error; err != nil {
		return nil, fmt.Errorf("failed to get updated port mapping: %v", err)
	}

	result := l.BaseProvider.FromDBModel(portModel)
	result.HostIP = providerInfo.Endpoint
	result.PublicIP = l.getPublicIP(providerInfo)

	global.APP_LOG.Info("LXD port mapping updated successfully", zap.Uint("id", req.ID))
	return result, nil
}

// ListPortMappings 列出LXD端口映射
func (l *LXDPortMapping) ListPortMappings(ctx context.Context, instanceID string) ([]*portmapping.PortMappingResult, error) {
	var ports []provider.Port
	if err := global.APP_DB.Where("instance_id = ?", instanceID).Find(&ports).Error; err != nil {
		return nil, fmt.Errorf("failed to list port mappings: %v", err)
	}

	providerCache := make(map[uint]*provider.Provider)
	var results []*portmapping.PortMappingResult
	for _, port := range ports {
		result := l.BaseProvider.FromDBModel(&port)

		// 获取Provider信息以填充IP地址
		providerInfo, ok := providerCache[port.ProviderID]
		if !ok {
			providerInfo, _ = l.getProvider(port.ProviderID)
			providerCache[port.ProviderID] = providerInfo
		}
		if providerInfo != nil {
			result.HostIP = providerInfo.Endpoint
			result.PublicIP = l.getPublicIP(providerInfo)
		}

		results = append(results, result)
	}

	return results, nil
}

// validateRequest 验证请求参数
func (l *LXDPortMapping) validateRequest(req *portmapping.PortMappingRequest) error {
	if req.InstanceID == "" {
		return fmt.Errorf("instance ID is required")
	}
	if req.GuestPort <= 0 || req.GuestPort > 65535 {
		return fmt.Errorf("invalid guest port: %d", req.GuestPort)
	}
	if req.HostPort < 0 || req.HostPort > 65535 {
		return fmt.Errorf("invalid host port: %d", req.HostPort)
	}
	if req.PortCount == 0 {
		req.PortCount = 1
	}
	if req.HostPort == 0 && req.PortCount > 1 {
		return fmt.Errorf("automatic allocation of port ranges is not supported by the legacy adapter")
	}
	if req.HostPort == 0 {
		req.HostPortEnd = 0
	} else if err := portmapping.ValidateMappingRange(req.HostPort, req.HostPortEnd, req.GuestPort, req.GuestPortEnd, req.PortCount); err != nil {
		return err
	}
	if req.Protocol == "" {
		req.Protocol = "tcp"
	}
	return portmapping.ValidateProtocol(req.Protocol)
}

func (l *LXDPortMapping) validateUpdateRequest(req *portmapping.UpdatePortMappingRequest) error {
	if err := portmapping.ValidateProtocol(req.Protocol); err != nil {
		return err
	}
	return portmapping.ValidateMappingRange(req.HostPort, req.HostPortEnd, req.GuestPort, req.GuestPortEnd, req.PortCount)
}

func normalizedPortCount(count int) int {
	if count <= 0 {
		return 1
	}
	return count
}

func defaultMappingType(mappingType string) string {
	if mappingType == "" {
		return "node"
	}
	return mappingType
}

// getInstance 获取实例信息
func (l *LXDPortMapping) getInstance(instanceID string) (*provider.Instance, error) {
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
func (l *LXDPortMapping) getProvider(providerID uint) (*provider.Provider, error) {
	var providerInfo provider.Provider
	if err := global.APP_DB.First(&providerInfo, providerID).Error; err != nil {
		return nil, fmt.Errorf("provider not found: %v", err)
	}
	return &providerInfo, nil
}

// getPublicIP 获取公网IP
func (l *LXDPortMapping) getPublicIP(providerInfo *provider.Provider) string {
	// 优先使用PortIP（端口映射专用IP），如果为空则使用Endpoint（SSH地址）
	if providerInfo.PortIP != "" {
		return providerInfo.PortIP
	}
	return providerInfo.Endpoint
}

// determineMappingMethod 确定端口映射方法
func (l *LXDPortMapping) determineMappingMethod(req *portmapping.PortMappingRequest, providerInfo *provider.Provider) string {
	// 如果请求中指定了映射方法，使用指定的方法
	if req.MappingMethod != "" {
		return req.MappingMethod
	}

	// 如果启用了IPv6，根据Provider配置确定方法
	if req.IPv6Enabled {
		switch providerInfo.IPv6PortMappingMethod {
		case "iptables":
			return "lxd-iptables-ipv6"
		case "device_proxy":
			return "lxd-device-proxy-ipv6"
		default:
			return "lxd-device-proxy-ipv6"
		}
	}

	// IPv4映射
	switch providerInfo.IPv4PortMappingMethod {
	case "iptables":
		return "lxd-iptables"
	case "device_proxy":
		return "lxd-device-proxy"
	default:
		return "lxd-device-proxy"
	}
}

// init 注册LXD端口映射Provider
func init() {
	portmapping.RegisterProvider("lxd", func(config *portmapping.ManagerConfig) portmapping.PortMappingProvider {
		return NewLXDPortMapping(config)
	})
}
