package instance

import (
	"fmt"

	"oneclickvirt/constant"
	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	userModel "oneclickvirt/model/user"

	"go.uber.org/zap"
)

// GetUserInstances 获取用户实例列表
func (s *Service) GetUserInstances(userID uint, req userModel.UserInstanceListRequest) ([]userModel.UserInstanceResponse, int64, error) {
	var instances []providerModel.Instance
	var total int64

	// 获取可显示的状态：隐藏删除/失败等终止状态，保留操作中过渡态，避免实例短暂从列表消失
	displayableStatuses := constant.GetDisplayableStatuses()

	query := global.APP_DB.Model(&providerModel.Instance{}).
		Where("user_id = ? AND deleted_at IS NULL AND status IN (?)", userID, displayableStatuses)

	if req.Name != "" {
		query = query.Where("name LIKE ?", "%"+req.Name+"%")
	}
	if req.Status != "" {
		query = query.Where("status = ?", req.Status)
	}
	if req.InstanceType != "" {
		query = query.Where("instance_type = ?", req.InstanceType)
	}
	// 支持type字段（兼容前端）
	if req.Type != "" {
		query = query.Where("instance_type = ?", req.Type)
	}
	// 支持节点名称搜索
	if req.ProviderName != "" {
		query = query.Where("provider LIKE ?", "%"+req.ProviderName+"%")
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (req.Page - 1) * req.PageSize
	if err := query.Offset(offset).Limit(req.PageSize).Find(&instances).Error; err != nil {
		return nil, 0, err
	}

	// 批量预加载端口映射
	var instanceIDs []uint
	for _, instance := range instances {
		instanceIDs = append(instanceIDs, instance.ID)
	}

	// 批量查询所有实例的端口映射
	var allPorts []providerModel.Port
	if len(instanceIDs) > 0 {
		if err := global.APP_DB.Where("instance_id IN ? AND status = 'active'", instanceIDs).
			Order("instance_id, is_ssh DESC, created_at ASC").Find(&allPorts).Error; err != nil {
			return nil, 0, fmt.Errorf("查询端口映射失败: %v", err)
		}
	}

	// 将端口映射按instance_id分组
	portsByInstance := make(map[uint][]providerModel.Port)
	for _, port := range allPorts {
		portsByInstance[port.InstanceID] = append(portsByInstance[port.InstanceID], port)
	}

	// 批量查询Provider信息
	var providerIDs []uint
	providerIDSet := make(map[uint]bool)
	for _, instance := range instances {
		if instance.ProviderID > 0 && !providerIDSet[instance.ProviderID] {
			providerIDs = append(providerIDs, instance.ProviderID)
			providerIDSet[instance.ProviderID] = true
		}
	}

	var providers []providerModel.Provider
	if len(providerIDs) > 0 {
		if err := global.APP_DB.Select("id, name, type, status, port_ip, endpoint, connection_type, network_type").
			Where("id IN ?", providerIDs).
			Limit(1000).
			Find(&providers).Error; err != nil {
			return nil, 0, fmt.Errorf("查询节点信息失败: %v", err)
		}
	}

	// 将Provider信息按ID映射
	providerMap := make(map[uint]providerModel.Provider)
	for _, provider := range providers {
		providerMap[provider.ID] = provider
	}

	var userInstances []userModel.UserInstanceResponse
	for _, instance := range instances {
		// 从预加载的数据中获取端口映射信息
		ports := portsByInstance[instance.ID]

		// 获取SSH端口（映射的公网端口）
		var sshPort int
		var providerType string
		var providerStatus string

		// 查找SSH端口映射
		for _, port := range ports {
			if port.IsSSH {
				sshPort = port.HostPort // 使用映射的公网端口而不是22
				break
			}
		}

		// 从预加载的Provider map中获取Provider信息
		if instance.ProviderID > 0 {
			if providerInfo, ok := providerMap[instance.ProviderID]; ok {
				providerType = providerInfo.Type
				providerStatus = providerInfo.Status

				// 如果实例状态是unavailable，检查provider是否已经恢复
				if instance.Status == "unavailable" && providerInfo.Status == "active" {
					global.APP_LOG.Debug("实例处于unavailable状态但provider已恢复",
						zap.Uint("instance_id", instance.ID),
						zap.String("instance_name", instance.Name),
						zap.String("provider_status", providerInfo.Status))
				}
			}
		}

		// 构建端口映射列表
		var portMappings []userModel.PortMappingResponse
		for _, port := range ports {
			portMappings = append(portMappings, userModel.PortMappingResponse{
				ID:          port.ID,
				HostPort:    port.HostPort,
				GuestPort:   port.GuestPort,
				Protocol:    port.Protocol,
				Status:      port.Status,
				Description: port.Description,
				IsSSH:       port.IsSSH,
				PortType:    port.PortType,
				MappingType: port.MappingType,
				CreatedAt:   port.CreatedAt,
			})
		}

		// 创建修改后的实例副本，更新SSH端口
		modifiedInstance := instance
		if sshPort > 0 {
			modifiedInstance.SSHPort = sshPort // 使用映射的公网端口
		}

		// 确定公网IP：agent录入模式+无端口映射模式的实例不显示公网IP
		// 因为该模式下的端口转发是通过控制端内网穿透实现的，节点本身没有对外的公网IP
		publicIP := instance.PublicIP
		if providerInfo, ok := providerMap[instance.ProviderID]; ok {
			if providerInfo.ConnectionType == "agent" && providerInfo.NetworkType == "no_port_mapping" {
				publicIP = ""
			}
			// 兼容旧实例：如果实例的NetworkType为空或为默认值，使用Provider的NetworkType
			if modifiedInstance.NetworkType == "" || modifiedInstance.NetworkType == "nat_ipv4" {
				if providerInfo.NetworkType != "" {
					modifiedInstance.NetworkType = providerInfo.NetworkType
				}
			}
		}

		userInstance := userModel.UserInstanceResponse{
			Instance:       modifiedInstance,
			CanStart:       instance.Status == "stopped" && !instance.TrafficLimited, // 流量受限时不能启动
			CanStop:        instance.Status == "running" || instance.Status == "unavailable",
			CanRestart:     instance.Status == "running" && !instance.TrafficLimited, // 流量受限时不能重启
			CanDelete:      instance.Status != "deleting",
			PortMappings:   portMappings,
			PublicIP:       publicIP, // 公网IP（agent+no_port_mapping模式下为空）
			ProviderType:   providerType,
			ProviderStatus: providerStatus,
		}
		userInstances = append(userInstances, userInstance)
	}

	return userInstances, total, nil
}
