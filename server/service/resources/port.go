package resources

import (
	"errors"
	"fmt"
	"oneclickvirt/global"
	"oneclickvirt/model/admin"
	"oneclickvirt/model/provider"
	"oneclickvirt/utils"
	"strings"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

type PortMappingService struct{}

// GetPortMappingList 获取端口映射列表
func (s *PortMappingService) GetPortMappingList(req admin.PortMappingListRequest) ([]provider.Port, int64, error) {
	var ports []provider.Port
	var total int64

	query := global.APP_DB.Model(&provider.Port{})

	// 查询条件
	if req.ProviderID > 0 {
		query = query.Where("provider_id = ?", req.ProviderID)
	}
	if req.InstanceID > 0 {
		query = query.Where("instance_id = ?", req.InstanceID)
	}
	if req.Protocol != "" {
		query = query.Where("protocol = ?", req.Protocol)
	}
	if req.Status != "" {
		query = query.Where("status = ?", req.Status)
	}

	// 获取总数
	if err := query.Count(&total).Error; err != nil {
		global.APP_LOG.Error("获取端口映射总数失败", zap.Error(err))
		return nil, 0, err
	}

	// 分页查询
	offset := (req.Page - 1) * req.PageSize
	if err := query.Offset(offset).Limit(req.PageSize).Order("created_at DESC").Find(&ports).Error; err != nil {
		global.APP_LOG.Error("获取端口映射列表失败", zap.Error(err))
		return nil, 0, err
	}

	return ports, total, nil
}

// CreatePortMapping 创建端口映射
func (s *PortMappingService) CreatePortMapping(req admin.CreatePortMappingRequest) error {
	// 获取实例信息
	var instance provider.Instance
	if err := global.APP_DB.Where("id = ?", req.InstanceID).First(&instance).Error; err != nil {
		return fmt.Errorf("实例不存在")
	}

	// 获取Provider信息
	var providerInfo provider.Provider
	if err := global.APP_DB.Where("id = ?", instance.ProviderID).First(&providerInfo).Error; err != nil {
		return fmt.Errorf("Provider不存在")
	}

	// 检查是否为独立IPv4模式或纯IPv6模式，如果是则不允许创建IPv4端口映射
	if providerInfo.NetworkType == "dedicated_ipv4" || providerInfo.NetworkType == "dedicated_ipv4_ipv6" || providerInfo.NetworkType == "ipv6_only" {
		var reason string
		switch providerInfo.NetworkType {
		case "dedicated_ipv4":
			reason = "独立IPv4模式下不需要端口映射，实例已具有独立的IPv4地址"
		case "dedicated_ipv4_ipv6":
			reason = "独立IPv4+IPv6模式下不需要端口映射，实例已具有独立的IP地址"
		case "ipv6_only":
			reason = "纯IPv6模式下不允许IPv4端口映射，请使用IPv6直接访问"
		}
		return fmt.Errorf("%s", reason)
	}

	// 分配主机端口
	hostPort := req.HostPort
	if hostPort == 0 {
		allocatedPort, err := s.allocateHostPort(providerInfo.ID, providerInfo.PortRangeStart, providerInfo.PortRangeEnd)
		if err != nil {
			return fmt.Errorf("端口分配失败: %v", err)
		}
		hostPort = allocatedPort
	} else {
		// 检查端口是否已被占用
		var existingPort provider.Port
		if err := global.APP_DB.Where("provider_id = ? AND host_port = ? AND status = 'active'",
			providerInfo.ID, hostPort).First(&existingPort).Error; err == nil {
			return fmt.Errorf("端口 %d 已被占用", hostPort)
		}
	}

	// 创建端口映射
	port := provider.Port{
		InstanceID:  req.InstanceID,
		ProviderID:  providerInfo.ID,
		HostPort:    hostPort,
		GuestPort:   req.GuestPort,
		Protocol:    req.Protocol,
		Description: req.Description,
		Status:      "active",
		IsSSH:       req.GuestPort == 22,
		IsAutomatic: req.HostPort == 0,
		IPv6Enabled: providerInfo.NetworkType == "nat_ipv4_ipv6" || providerInfo.NetworkType == "dedicated_ipv4_ipv6" || providerInfo.NetworkType == "ipv6_only",
		// IPv6Address: providerInfo.IPv6Address,
	}

	if err := global.APP_DB.Create(&port).Error; err != nil {
		global.APP_LOG.Error("创建端口映射失败", zap.Error(err))
		return fmt.Errorf("创建端口映射失败: %v", err)
	}

	// 更新Provider的下一个可用端口
	if req.HostPort == 0 {
		global.APP_DB.Model(&providerInfo).Update("next_available_port", hostPort+1)
	}

	global.APP_LOG.Info("创建端口映射成功",
		zap.Uint("instance_id", req.InstanceID),
		zap.Int("host_port", hostPort),
		zap.Int("guest_port", req.GuestPort),
		zap.String("protocol", req.Protocol))

	return nil
}

// UpdatePortMapping 更新端口映射
func (s *PortMappingService) UpdatePortMapping(req admin.UpdatePortMappingRequest) error {
	var port provider.Port
	if err := global.APP_DB.Where("id = ?", req.ID).First(&port).Error; err != nil {
		return fmt.Errorf("端口映射不存在")
	}

	// 更新字段
	port.GuestPort = req.GuestPort
	port.Protocol = req.Protocol
	port.Description = req.Description
	port.Status = req.Status
	port.IsSSH = req.GuestPort == 22

	if err := global.APP_DB.Save(&port).Error; err != nil {
		global.APP_LOG.Error("更新端口映射失败", zap.Error(err))
		return fmt.Errorf("更新端口映射失败: %v", err)
	}

	global.APP_LOG.Info("更新端口映射成功", zap.Uint("port_id", req.ID))
	return nil
}

// DeletePortMapping 删除端口映射
func (s *PortMappingService) DeletePortMapping(id uint) error {
	var port provider.Port
	if err := global.APP_DB.Where("id = ?", id).First(&port).Error; err != nil {
		return fmt.Errorf("端口映射不存在")
	}

	if err := global.APP_DB.Delete(&port).Error; err != nil {
		global.APP_LOG.Error("删除端口映射失败", zap.Error(err))
		return fmt.Errorf("删除端口映射失败: %v", err)
	}

	global.APP_LOG.Info("删除端口映射成功", zap.Uint("port_id", id))
	return nil
}

// BatchDeletePortMapping 批量删除端口映射
func (s *PortMappingService) BatchDeletePortMapping(req admin.BatchDeletePortMappingRequest) error {
	if err := global.APP_DB.Where("id IN ?", req.IDs).Delete(&provider.Port{}).Error; err != nil {
		global.APP_LOG.Error("批量删除端口映射失败", zap.Error(err))
		return fmt.Errorf("批量删除端口映射失败: %v", err)
	}

	global.APP_LOG.Info("批量删除端口映射成功", zap.Any("ids", req.IDs))
	return nil
}

// UpdateProviderPortConfig 更新Provider端口配置
func (s *PortMappingService) UpdateProviderPortConfig(providerID uint, req admin.ProviderPortConfigRequest) error {
	// 验证端口范围
	if req.PortRangeStart >= req.PortRangeEnd {
		return fmt.Errorf("端口范围起始值必须小于结束值")
	}

	var providerInfo provider.Provider
	if err := global.APP_DB.Where("id = ?", providerID).First(&providerInfo).Error; err != nil {
		return fmt.Errorf("Provider不存在")
	}

	// 更新端口配置
	providerInfo.DefaultPortCount = req.DefaultPortCount
	providerInfo.PortRangeStart = req.PortRangeStart
	providerInfo.PortRangeEnd = req.PortRangeEnd
	if req.NetworkType != "" {
		providerInfo.NetworkType = req.NetworkType
	}

	// 如果没有设置NextAvailablePort，则设置为范围起始值
	if providerInfo.NextAvailablePort < req.PortRangeStart {
		providerInfo.NextAvailablePort = req.PortRangeStart
	}

	if err := global.APP_DB.Save(&providerInfo).Error; err != nil {
		global.APP_LOG.Error("更新Provider端口配置失败", zap.Error(err))
		return fmt.Errorf("更新Provider端口配置失败: %v", err)
	}

	global.APP_LOG.Info("更新Provider端口配置成功", zap.Uint("provider_id", providerID))
	return nil
}

// CreateDefaultPortMappings 为实例创建默认端口映射 - 增强版本，防止端口冲突
func (s *PortMappingService) CreateDefaultPortMappings(instanceID uint, providerID uint) error {
	// 获取Provider配置
	var providerInfo provider.Provider
	if err := global.APP_DB.Where("id = ?", providerID).First(&providerInfo).Error; err != nil {
		return fmt.Errorf("Provider不存在")
	}

	// 检查是否为独立IPv4模式或纯IPv6模式，如果是则跳过默认端口映射创建
	if providerInfo.NetworkType == "dedicated_ipv4" || providerInfo.NetworkType == "dedicated_ipv4_ipv6" || providerInfo.NetworkType == "ipv6_only" {
		global.APP_LOG.Info("独立IP模式或纯IPv6模式，跳过默认端口映射创建",
			zap.Uint("instanceID", instanceID),
			zap.Uint("providerID", providerID),
			zap.String("networkType", providerInfo.NetworkType))
		return nil
	}

	defaultPortCount := providerInfo.DefaultPortCount
	if defaultPortCount <= 0 {
		defaultPortCount = 10 // 默认值
	}

	// 计算实际可用的端口范围
	availablePortCount := providerInfo.PortRangeEnd - providerInfo.PortRangeStart + 1
	if availablePortCount <= 0 {
		return fmt.Errorf("无效的端口范围配置")
	}

	// 如果可用端口数量小于请求数量，调整为可用数量
	if defaultPortCount > availablePortCount {
		defaultPortCount = availablePortCount
	}

	// 使用事务确保端口分配的原子性，防止并发创建时的端口冲突
	return global.APP_DB.Transaction(func(tx *gorm.DB) error {
		var createdPorts []provider.Port

		// 首先创建SSH端口映射（22端口）- 使用端口区间的第一个端口
		sshHostPort := providerInfo.PortRangeStart

		// 检查第一个端口是否已被占用
		var existingPort provider.Port
		err := tx.Where("provider_id = ? AND host_port = ? AND status = 'active'", providerID, sshHostPort).First(&existingPort).Error
		if err != gorm.ErrRecordNotFound {
			if err == nil {
				// 端口已被占用，使用动态分配作为fallback
				sshHostPort, err = s.allocateHostPortInTx(tx, providerID, providerInfo.PortRangeStart+1, providerInfo.PortRangeEnd)
				if err != nil {
					return fmt.Errorf("SSH端口分配失败: %v", err)
				}
			} else {
				return fmt.Errorf("检查SSH端口占用状态失败: %v", err)
			}
		}

		sshPort := provider.Port{
			InstanceID:  instanceID,
			ProviderID:  providerID,
			HostPort:    sshHostPort,
			GuestPort:   22, // SSH端口固定为22
			Protocol:    "tcp",
			Description: "SSH",
			Status:      "active",
			IsSSH:       true,
			IsAutomatic: true,
			IPv6Enabled: providerInfo.NetworkType == "nat_ipv4_ipv6" || providerInfo.NetworkType == "dedicated_ipv4_ipv6" || providerInfo.NetworkType == "ipv6_only",
		}

		if err := tx.Create(&sshPort).Error; err != nil {
			return fmt.Errorf("创建SSH端口映射失败: %v", err)
		}
		createdPorts = append(createdPorts, sshPort)

		// 更新实例的SSH端口
		if err := tx.Model(&provider.Instance{}).Where("id = ?", instanceID).Update("ssh_port", sshHostPort).Error; err != nil {
			global.APP_LOG.Warn("更新实例SSH端口失败", zap.Error(err))
		}

		// 如果只有1个端口可用，或者只需要1个端口，则只创建SSH映射
		if defaultPortCount <= 1 || availablePortCount <= 1 {
			global.APP_LOG.Info("创建默认端口映射成功（仅SSH）",
				zap.Uint("instance_id", instanceID),
				zap.Int("total_ports", 1),
				zap.Int("ssh_port", sshHostPort))
			return nil
		}

		// 为其他服务创建1:1端口映射 - 内外端口完全相同
		successCount := 1 // SSH端口已创建

		// 从端口范围的下一个端口开始分配1:1映射（跳过SSH端口）
		for port := providerInfo.PortRangeStart + 1; port <= providerInfo.PortRangeEnd && successCount < defaultPortCount; port++ {
			// 检查端口是否已被其他实例占用
			var existingPort provider.Port
			err := tx.Where("provider_id = ? AND host_port = ? AND status = 'active'", providerID, port).First(&existingPort).Error
			if err != gorm.ErrRecordNotFound {
				continue // 端口已被占用或查询出错，跳过
			}

			// 创建1:1端口映射记录（内外端口完全相同）
			portRecord := provider.Port{
				InstanceID:  instanceID,
				ProviderID:  providerID,
				HostPort:    port,
				GuestPort:   port, // 内外端口完全相同
				Protocol:    "tcp",
				Description: fmt.Sprintf("端口%d", port),
				Status:      "active",
				IsSSH:       false,
				IsAutomatic: true,
				IPv6Enabled: providerInfo.NetworkType == "nat_ipv4_ipv6" || providerInfo.NetworkType == "dedicated_ipv4_ipv6" || providerInfo.NetworkType == "ipv6_only",
			}

			if err := tx.Create(&portRecord).Error; err != nil {
				global.APP_LOG.Warn("创建端口映射失败，跳过", zap.Error(err), zap.Int("port", port))
				continue
			}

			createdPorts = append(createdPorts, portRecord)
			successCount++
		}

		global.APP_LOG.Info("创建默认端口映射成功",
			zap.Uint("instance_id", instanceID),
			zap.Int("total_ports", successCount),
			zap.Int("ssh_port", sshHostPort))

		return nil
	})
}

// allocateHostPortInTx 在事务中分配主机端口 - 防止并发冲突
func (s *PortMappingService) allocateHostPortInTx(tx *gorm.DB, providerID uint, rangeStart, rangeEnd int) (int, error) {
	// 获取Provider信息（带锁）
	var providerInfo provider.Provider
	if err := tx.Set("gorm:query_option", "FOR UPDATE").Where("id = ?", providerID).First(&providerInfo).Error; err != nil {
		return 0, fmt.Errorf("Provider不存在: %v", err)
	}

	startPort := providerInfo.NextAvailablePort
	if startPort < rangeStart {
		startPort = rangeStart
	}

	// 从下一个可用端口开始查找
	for port := startPort; port <= rangeEnd; port++ {
		// 检查数据库中是否已有active状态的端口映射
		var existingPort provider.Port
		err := tx.Where("provider_id = ? AND host_port = ?", providerID, port).First(&existingPort).Error

		if err == gorm.ErrRecordNotFound {
			// 端口在数据库中不存在，检查是否有残留的Docker端口映射
			if s.isPortAvailableOnProvider(&providerInfo, port) {
				// 端口可用，立即更新NextAvailablePort
				nextPort := port + 1
				if nextPort > rangeEnd {
					nextPort = rangeStart // 循环使用端口范围
				}

				if err := tx.Model(&provider.Provider{}).
					Where("id = ?", providerID).
					Update("next_available_port", nextPort).Error; err != nil {
					return 0, fmt.Errorf("更新NextAvailablePort失败: %v", err)
				}

				return port, nil
			}
		} else if err != nil {
			return 0, fmt.Errorf("检查端口失败: %v", err)
		}
		// 如果端口存在且为active状态，或者端口被实际占用，继续下一个端口
	}

	// 如果从当前位置到结束都没有可用端口，从范围开始重新查找
	if startPort > rangeStart {
		for port := rangeStart; port < startPort; port++ {
			var existingPort provider.Port
			err := tx.Where("provider_id = ? AND host_port = ?", providerID, port).First(&existingPort).Error

			if err == gorm.ErrRecordNotFound {
				if s.isPortAvailableOnProvider(&providerInfo, port) {
					// 端口可用，立即更新NextAvailablePort
					nextPort := port + 1
					if nextPort > rangeEnd {
						nextPort = rangeStart // 循环使用端口范围
					}

					if err := tx.Model(&provider.Provider{}).
						Where("id = ?", providerID).
						Update("next_available_port", nextPort).Error; err != nil {
						return 0, fmt.Errorf("更新NextAvailablePort失败: %v", err)
					}

					return port, nil
				}
			} else if err != nil {
				return 0, fmt.Errorf("检查端口失败: %v", err)
			}
		}
	}

	return 0, fmt.Errorf("没有可用端口")
}

// isPortAvailableOnProvider 检查端口在Provider上是否真正可用
func (s *PortMappingService) isPortAvailableOnProvider(providerInfo *provider.Provider, port int) bool {
	// 根据Provider类型检查端口是否被占用
	switch providerInfo.Type {
	case "docker":
		return s.isDockerPortAvailable(providerInfo, port)
	case "lxd", "incus":
		return s.isLXDPortAvailable(providerInfo, port)
	case "proxmox":
		return s.isProxmoxPortAvailable(providerInfo, port)
	default:
		// 对于未知类型，使用通用的端口检查
		return s.isGenericPortAvailable(providerInfo, port)
	}
}

// isDockerPortAvailable 检查Docker端口是否可用
func (s *PortMappingService) isDockerPortAvailable(providerInfo *provider.Provider, port int) bool {
	// 这里可以通过SSH检查端口是否被Docker容器占用
	// 简化实现：检查是否有进程监听该端口
	return s.isGenericPortAvailable(providerInfo, port)
}

// isLXDPortAvailable 检查LXD端口是否可用
func (s *PortMappingService) isLXDPortAvailable(providerInfo *provider.Provider, port int) bool {
	return s.isGenericPortAvailable(providerInfo, port)
}

// isProxmoxPortAvailable 检查Proxmox端口是否可用
func (s *PortMappingService) isProxmoxPortAvailable(providerInfo *provider.Provider, port int) bool {
	return s.isGenericPortAvailable(providerInfo, port)
}

// isGenericPortAvailable 通用端口可用性检查
func (s *PortMappingService) isGenericPortAvailable(providerInfo *provider.Provider, port int) bool {
	// 实现真实的端口可用性检查
	// 可以通过SSH连接到Provider检查端口是否被占用

	// 首先检查数据库中是否已经有端口映射记录
	var existingMapping provider.Port
	err := global.APP_DB.Where("provider_id = ? AND host_port = ? AND status = ?",
		providerInfo.ID, port, "active").First(&existingMapping).Error

	if err == nil {
		// 如果数据库中已有活跃的端口映射，则认为端口不可用
		return false
	}

	if !errors.Is(err, gorm.ErrRecordNotFound) {
		// 数据库查询出错，为安全起见认为端口不可用
		global.APP_LOG.Error("检查端口映射时数据库查询失败",
			zap.Uint("providerId", providerInfo.ID),
			zap.Int("port", port),
			zap.Error(err))
		return false
	}

	// 检查端口是否在Provider的可用范围内
	if port < providerInfo.PortRangeStart || port > providerInfo.PortRangeEnd {
		return false
	}

	// 如果有SSH连接信息，尝试通过SSH检查端口
	if providerInfo.Endpoint != "" && providerInfo.Username != "" && providerInfo.Password != "" {
		sshConfig := utils.SSHConfig{
			Host:     providerInfo.Endpoint,
			Port:     providerInfo.SSHPort,
			Username: providerInfo.Username,
			Password: providerInfo.Password,
		}

		sshClient, err := utils.NewSSHClient(sshConfig)
		if err != nil {
			global.APP_LOG.Warn("创建SSH连接失败，无法检查端口状态",
				zap.String("endpoint", providerInfo.Endpoint),
				zap.Int("port", port),
				zap.Error(err))
			// SSH连接失败，基于数据库查询结果判断端口可用
			return true
		}
		defer sshClient.Close()

		// 使用ss命令检查端口是否被监听
		command := fmt.Sprintf("ss -tuln | grep ':%d '", port)
		output, err := sshClient.Execute(command)
		if err != nil {
			global.APP_LOG.Warn("SSH执行端口检查命令失败，尝试使用netstat",
				zap.String("command", command),
				zap.Error(err))

			// ss命令失败，尝试使用netstat作为fallback
			netstatCommand := fmt.Sprintf("netstat -tuln | grep ':%d '", port)
			netstatOutput, netstatErr := sshClient.Execute(netstatCommand)
			if netstatErr != nil {
				global.APP_LOG.Warn("SSH执行netstat端口检查命令也失败",
					zap.String("command", netstatCommand),
					zap.Error(netstatErr))
				// 两个命令都失败，基于数据库查询结果判断端口可用
				return true
			}

			// 如果netstat命令有输出，说明端口被占用
			if strings.TrimSpace(netstatOutput) != "" {
				global.APP_LOG.Debug("通过netstat检测到端口被占用",
					zap.Int("port", port),
					zap.String("output", netstatOutput))
				return false
			}
		} else {
			// 如果ss命令有输出，说明端口被占用
			if strings.TrimSpace(output) != "" {
				global.APP_LOG.Debug("通过ss检测到端口被占用",
					zap.Int("port", port),
					zap.String("output", output))
				return false
			}
		}
	}

	// 所有检查都通过，端口可用
	return true
}

// allocateHostPort 分配主机端口 - 带并发保护和事务安全
func (s *PortMappingService) allocateHostPort(providerID uint, rangeStart, rangeEnd int) (int, error) {
	var allocatedPort int
	var providerInfo provider.Provider

	// 使用数据库事务确保端口分配的原子性
	err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
		// 获取Provider信息（带锁）
		if err := tx.Set("gorm:query_option", "FOR UPDATE").Where("id = ?", providerID).First(&providerInfo).Error; err != nil {
			return fmt.Errorf("Provider不存在: %v", err)
		}

		startPort := providerInfo.NextAvailablePort
		if startPort < rangeStart {
			startPort = rangeStart
		}

		// 从下一个可用端口开始查找
		for port := startPort; port <= rangeEnd; port++ {
			var existingPort provider.Port
			err := tx.Where("provider_id = ? AND host_port = ? AND status = 'active'",
				providerID, port).First(&existingPort).Error

			if err == gorm.ErrRecordNotFound {
				// 端口可用，立即更新NextAvailablePort
				allocatedPort = port
				nextPort := port + 1
				if nextPort > rangeEnd {
					nextPort = rangeStart // 循环使用端口范围
				}

				return tx.Model(&provider.Provider{}).
					Where("id = ?", providerID).
					Update("next_available_port", nextPort).Error
			} else if err != nil {
				return fmt.Errorf("检查端口失败: %v", err)
			}
		}

		// 如果从当前位置到结束都没有可用端口，从范围开始重新查找
		if startPort > rangeStart {
			for port := rangeStart; port < startPort; port++ {
				var existingPort provider.Port
				err := tx.Where("provider_id = ? AND host_port = ? AND status = 'active'",
					providerID, port).First(&existingPort).Error

				if err == gorm.ErrRecordNotFound {
					// 端口可用，立即更新NextAvailablePort
					allocatedPort = port
					nextPort := port + 1
					if nextPort > rangeEnd {
						nextPort = rangeStart // 循环使用端口范围
					}

					return tx.Model(&provider.Provider{}).
						Where("id = ?", providerID).
						Update("next_available_port", nextPort).Error
				} else if err != nil {
					return fmt.Errorf("检查端口失败: %v", err)
				}
			}
		}

		return fmt.Errorf("没有可用端口")
	})

	if err != nil {
		return 0, err
	}

	global.APP_LOG.Info("分配端口成功",
		zap.Uint("providerId", providerID),
		zap.Int("allocatedPort", allocatedPort),
		zap.Int("nextPort", providerInfo.NextAvailablePort))

	return allocatedPort, nil
}

// updateNextAvailablePort 更新下一个可用端口
func (s *PortMappingService) updateNextAvailablePort(providerID uint) error {
	var maxPort int
	err := global.APP_DB.Model(&provider.Port{}).
		Where("provider_id = ? AND status = 'active'", providerID).
		Select("COALESCE(MAX(host_port), 0)").
		Scan(&maxPort).Error

	if err != nil {
		return err
	}

	nextPort := maxPort + 1
	return global.APP_DB.Model(&provider.Provider{}).
		Where("id = ?", providerID).
		Update("next_available_port", nextPort).Error
}

// GetInstancePortMappings 获取实例的端口映射
func (s *PortMappingService) GetInstancePortMappings(instanceID uint) ([]provider.Port, error) {
	var ports []provider.Port

	if err := global.APP_DB.Where("instance_id = ?", instanceID).Find(&ports).Error; err != nil {
		global.APP_LOG.Error("获取实例端口映射失败", zap.Error(err), zap.Uint("instanceID", instanceID))
		return nil, err
	}

	return ports, nil
}

// GetPortMappingsByInstanceID 获取指定实例的端口映射（别名方法）
func (s *PortMappingService) GetPortMappingsByInstanceID(instanceID uint) ([]provider.Port, error) {
	return s.GetInstancePortMappings(instanceID)
}

// GetUserPortMappings 获取用户的端口映射列表 - 简化显示格式
func (s *PortMappingService) GetUserPortMappings(userID uint, page, limit int, keyword string) ([]map[string]interface{}, int64, error) {
	// 首先获取用户的所有实例
	var instances []provider.Instance
	instanceQuery := global.APP_DB.Where("user_id = ?", userID)

	if keyword != "" {
		instanceQuery = instanceQuery.Where("name LIKE ?", "%"+keyword+"%")
	}

	if err := instanceQuery.Find(&instances).Error; err != nil {
		global.APP_LOG.Error("获取用户实例失败", zap.Error(err))
		return nil, 0, err
	}

	if len(instances) == 0 {
		return []map[string]interface{}{}, 0, nil
	}

	// 获取实例ID列表
	instanceIDs := make([]uint, len(instances))
	instanceMap := make(map[uint]provider.Instance)
	for i, instance := range instances {
		instanceIDs[i] = instance.ID
		instanceMap[instance.ID] = instance
	}

	// 查询这些实例的端口映射
	var allPorts []provider.Port
	if err := global.APP_DB.Where("instance_id IN (?)", instanceIDs).
		Order("instance_id ASC, is_ssh DESC, created_at ASC").
		Find(&allPorts).Error; err != nil {
		global.APP_LOG.Error("获取端口映射失败", zap.Error(err))
		return nil, 0, err
	}

	// 按实例分组端口映射
	portsByInstance := make(map[uint][]provider.Port)
	for _, port := range allPorts {
		portsByInstance[port.InstanceID] = append(portsByInstance[port.InstanceID], port)
	}

	// 构建简化的返回结构
	var result []map[string]interface{}
	for _, instance := range instances {
		ports, exists := portsByInstance[instance.ID]
		if !exists || len(ports) == 0 {
			continue // 跳过没有端口映射的实例
		}

		// 分离SSH端口和其他端口
		var sshPort *provider.Port
		var otherPorts []provider.Port
		var samePortMappings []int // 内外端口相同的映射

		for _, port := range ports {
			if port.IsSSH {
				sshPort = &port
			} else {
				otherPorts = append(otherPorts, port)
				if port.HostPort == port.GuestPort {
					samePortMappings = append(samePortMappings, port.HostPort)
				}
			}
		}

		// 构建端口显示字符串
		var portDisplay string
		if sshPort != nil {
			portDisplay = fmt.Sprintf("SSH: %d", sshPort.HostPort)
		}

		// 如果有其他内外端口相同的映射，用逗号分隔显示
		if len(samePortMappings) > 0 {
			portsStr := make([]string, len(samePortMappings))
			for i, port := range samePortMappings {
				portsStr[i] = fmt.Sprintf("%d", port)
			}
			if portDisplay != "" {
				portDisplay += ", " + strings.Join(portsStr, ", ")
			} else {
				portDisplay = strings.Join(portsStr, ", ")
			}
		}

		instanceData := map[string]interface{}{
			"instanceId":   instance.ID,
			"instanceName": instance.Name,
			"instanceType": instance.InstanceType,
			"status":       instance.Status,
			"sshPort":      nil,
			"portDisplay":  portDisplay,
			"totalPorts":   len(ports),
			"createdAt":    instance.CreatedAt,
		}

		if sshPort != nil {
			instanceData["sshPort"] = sshPort.HostPort
		}

		// 获取Provider信息以显示公网IP（不带端口）
		if instance.ProviderID > 0 {
			var providerInfo provider.Provider
			if err := global.APP_DB.Where("id = ?", instance.ProviderID).First(&providerInfo).Error; err == nil {
				// 处理Endpoint，移除端口号部分
				endpoint := providerInfo.Endpoint
				if endpoint != "" {
					// 如果Endpoint包含端口（如 "192.168.1.1:22"），只取IP部分
					if colonIndex := strings.LastIndex(endpoint, ":"); colonIndex > 0 {
						// 检查是否是IPv6地址
						if strings.Count(endpoint, ":") > 1 && !strings.HasPrefix(endpoint, "[") {
							// IPv6地址处理
							instanceData["publicIP"] = endpoint
						} else {
							// IPv4地址，移除端口部分
							instanceData["publicIP"] = endpoint[:colonIndex]
						}
					} else {
						instanceData["publicIP"] = endpoint
					}
				}
				instanceData["providerName"] = providerInfo.Name
			}
		}

		result = append(result, instanceData)
	}

	// 分页处理
	total := int64(len(result))
	start := (page - 1) * limit
	end := start + limit

	if start >= len(result) {
		return []map[string]interface{}{}, total, nil
	}

	if end > len(result) {
		end = len(result)
	}

	return result[start:end], total, nil
}

// DeleteInstancePortMappings 删除实例的所有端口映射并释放端口
func (s *PortMappingService) DeleteInstancePortMappings(instanceID uint) error {
	// 获取实例的所有端口映射
	var ports []provider.Port
	if err := global.APP_DB.Where("instance_id = ?", instanceID).Find(&ports).Error; err != nil {
		global.APP_LOG.Error("获取实例端口映射失败", zap.Error(err))
		return err
	}

	// 使用事务确保端口释放的原子性
	return global.APP_DB.Transaction(func(tx *gorm.DB) error {
		return s.DeleteInstancePortMappingsInTx(tx, instanceID)
	})
}

// DeleteInstancePortMappingsInTx 在事务中删除实例的所有端口映射并释放端口
func (s *PortMappingService) DeleteInstancePortMappingsInTx(tx *gorm.DB, instanceID uint) error {
	// 获取实例的所有端口映射
	var ports []provider.Port
	if err := tx.Where("instance_id = ?", instanceID).Find(&ports).Error; err != nil {
		global.APP_LOG.Error("获取实例端口映射失败", zap.Error(err))
		return err
	}

	// 直接删除端口映射记录（失败实例的端口直接释放）
	if err := tx.Where("instance_id = ?", instanceID).Delete(&provider.Port{}).Error; err != nil {
		return fmt.Errorf("删除端口映射失败: %v", err)
	}

	// 按Provider分组，更新NextAvailablePort以便端口重用
	portsByProvider := make(map[uint][]int)
	for _, port := range ports {
		portsByProvider[port.ProviderID] = append(portsByProvider[port.ProviderID], port.HostPort)
	}

	// 为每个Provider更新NextAvailablePort以优化端口重用
	for providerID, releasedPorts := range portsByProvider {
		if err := s.optimizeNextAvailablePortInTx(tx, providerID, releasedPorts); err != nil {
			global.APP_LOG.Warn("优化Provider端口重用失败", zap.Uint("providerId", providerID), zap.Error(err))
			// 不阻止删除操作，只记录警告
		}
	}

	global.APP_LOG.Info("删除实例端口映射成功",
		zap.Uint("instance_id", instanceID),
		zap.Int("releasedPortCount", len(ports)))

	return nil
}

// optimizeNextAvailablePortInTx 在事务中优化Provider的NextAvailablePort以促进端口重用
func (s *PortMappingService) optimizeNextAvailablePortInTx(tx *gorm.DB, providerID uint, releasedPorts []int) error {
	// 获取Provider当前配置
	var providerInfo provider.Provider
	if err := tx.Where("id = ?", providerID).First(&providerInfo).Error; err != nil {
		return fmt.Errorf("Provider不存在: %v", err)
	}

	// 找到最小的已释放端口
	minReleasedPort := providerInfo.PortRangeEnd + 1
	for _, port := range releasedPorts {
		if port >= providerInfo.PortRangeStart && port <= providerInfo.PortRangeEnd && port < minReleasedPort {
			minReleasedPort = port
		}
	}

	// 如果释放的端口中有比当前NextAvailablePort更小的，更新以促进重用
	if minReleasedPort < providerInfo.NextAvailablePort {
		return tx.Model(&provider.Provider{}).
			Where("id = ?", providerID).
			Update("next_available_port", minReleasedPort).Error
	}

	return nil
}

// GetProviderPortUsage 获取Provider端口使用情况
func (s *PortMappingService) GetProviderPortUsage(providerID uint) (map[string]interface{}, error) {
	var providerInfo provider.Provider
	if err := global.APP_DB.Where("id = ?", providerID).First(&providerInfo).Error; err != nil {
		return nil, fmt.Errorf("Provider不存在")
	}

	// 统计端口使用情况
	var totalPorts, usedPorts int64
	totalPorts = int64(providerInfo.PortRangeEnd - providerInfo.PortRangeStart + 1)

	global.APP_DB.Model(&provider.Port{}).
		Where("provider_id = ? AND status = 'active'", providerID).
		Count(&usedPorts)

	return map[string]interface{}{
		"providerID":        providerID,
		"portRangeStart":    providerInfo.PortRangeStart,
		"portRangeEnd":      providerInfo.PortRangeEnd,
		"nextAvailablePort": providerInfo.NextAvailablePort,
		"totalPorts":        totalPorts,
		"usedPorts":         usedPorts,
		"availablePorts":    totalPorts - usedPorts,
		"usageRate":         float64(usedPorts) / float64(totalPorts) * 100,
		"defaultPortCount":  providerInfo.DefaultPortCount,
		"enableIPv6":        providerInfo.NetworkType == "nat_ipv4_ipv6" || providerInfo.NetworkType == "dedicated_ipv4_ipv6" || providerInfo.NetworkType == "ipv6_only",
	}, nil
}
