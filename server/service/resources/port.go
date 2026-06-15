package resources

import (
	"errors"
	"fmt"
	"oneclickvirt/global"
	"oneclickvirt/model/admin"
	"oneclickvirt/model/provider"
	"oneclickvirt/service/taskgate"
	"oneclickvirt/utils"
	"strings"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// ControllerPortForwardFunc 是启动控制端端口转发的函数类型。
// 由 service/agent 包在初始化时注入，避免循环依赖。
var ControllerPortForwardFunc func(portID uint, providerID uint, listenPort int, targetHost string, targetPort int) error

// StopControllerPortForwardFunc 是停止控制端端口转发的函数类型。
// 由 service/agent 包在初始化时注入，避免循环依赖。
var StopControllerPortForwardFunc func(portID uint)

// 定义错误类型
var (
	// ErrPortRangeValidation 端口范围验证错误（用于区分业务验证错误和系统错误）
	ErrPortRangeValidation = errors.New("port range validation error")
)

type PortMappingService struct{}

// GetPortMappingList 获取端口映射列表
func (s *PortMappingService) GetPortMappingList(req admin.PortMappingListRequest, ownerAdminID uint) ([]provider.Port, int64, error) {
	var ports []provider.Port
	var total int64

	query := global.APP_DB.Model(&provider.Port{})

	// 普通管理员数据隔离：只能看到归属自己的Provider的端口映射
	if ownerAdminID > 0 {
		var providerIDs []uint
		if err := global.APP_DB.Model(&provider.Provider{}).
			Where("owner_admin_id = ?", ownerAdminID).
			Pluck("id", &providerIDs).Error; err != nil {
			return nil, 0, err
		}
		if len(providerIDs) == 0 {
			return []provider.Port{}, 0, nil
		}
		query = query.Where("provider_id IN ?", providerIDs)
	}

	// 关键字搜索（实例名称）
	if req.Keyword != "" {
		// 子查询：查找名称匹配的实例ID列表
		var instanceIDs []uint
		if err := global.APP_DB.Model(&provider.Instance{}).
			Where("name LIKE ?", "%"+req.Keyword+"%").
			Pluck("id", &instanceIDs).Error; err != nil {
			global.APP_LOG.Warn("搜索实例失败", zap.Error(err))
		} else if len(instanceIDs) > 0 {
			query = query.Where("instance_id IN ?", instanceIDs)
		} else {
			// 没有匹配的实例，返回空结果
			return []provider.Port{}, 0, nil
		}
	}

	// 其他查询条件
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

// CreatePortMappingWithTask 手动创建端口映射（通过任务系统异步执行，仅支持 LXD/Incus/PVE，不支持 Docker）
// 支持单个端口和端口段批量创建
// 返回端口ID和任务数据（由调用者创建和启动任务）
func (s *PortMappingService) CreatePortMappingWithTask(req admin.CreatePortMappingRequest) (uint, *admin.CreatePortMappingTaskRequest, error) {
	if err := taskgate.EnsureAccepting(); err != nil {
		return 0, nil, err
	}

	// 防御：检查数据库连接是否可用
	if global.APP_DB == nil {
		return 0, nil, fmt.Errorf("数据库连接不可用")
	}

	// 获取实例信息
	var instance provider.Instance
	if err := global.APP_DB.Where("id = ?", req.InstanceID).First(&instance).Error; err != nil {
		return 0, nil, fmt.Errorf("实例不存在")
	}

	// 获取Provider信息
	var providerInfo provider.Provider
	if err := global.APP_DB.Where("id = ?", instance.ProviderID).First(&providerInfo).Error; err != nil {
		return 0, nil, fmt.Errorf("Provider不存在")
	}

	// 手动添加端口支持：
	// - LXD/Incus/Proxmox/QEMU/KubeVirt/VMware/VirtualBox/Multipass/Vagrant：支持节点侧映射（device_proxy/iptables）和控制端转发
	// - Docker/Podman/Containerd/Orbstack：仅支持控制端转发（内网穿透），不支持节点侧映射
	isContainerRuntime := utils.IsDockerFamilyProvider(providerInfo.Type)
	isNodeSupported := providerInfo.Type == "lxd" || providerInfo.Type == "incus" || providerInfo.Type == "proxmox" || providerInfo.Type == "proxmoxve" || providerInfo.Type == "kubevirt" || providerInfo.Type == "qemu" || utils.IsVMOnlyProvider(providerInfo.Type)

	if isContainerRuntime {
		// Docker/Podman/Containerd/Orbstack 仅支持控制端端口转发（内网穿透）
		if req.MappingType != "controller" {
			return 0, nil, fmt.Errorf("Docker/Podman/Containerd/Orbstack 实例仅支持控制端端口转发模式（内网穿透），不支持节点侧端口映射")
		}
	} else if !isNodeSupported {
		return 0, nil, fmt.Errorf("不支持的 Provider 类型，手动添加端口仅支持 LXD/Incus/Proxmox/QEMU/KubeVirt/VMware/VirtualBox/Multipass/Vagrant/Docker/Podman/Containerd/Orbstack")
	}

	// 检查是否为独立IPv4模式或纯IPv6模式或无端口映射模式
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
		return 0, nil, fmt.Errorf("%s", reason)
	}

	// 无端口映射模式：不允许节点侧映射，仅允许控制端转发模式
	if providerInfo.NetworkType == "no_port_mapping" && req.MappingType != "controller" {
		return 0, nil, fmt.Errorf("无端口映射模式下不支持节点侧端口映射，请使用控制端转发模式")
	}

	// 默认端口数量为1
	portCount := req.PortCount
	if portCount == 0 {
		portCount = 1
	}

	// 验证端口数量
	if portCount < 1 || portCount > 1500 {
		return 0, nil, fmt.Errorf("端口数量必须在1-1500之间")
	}

	// 确定映射类型
	mappingType := req.MappingType
	if mappingType == "" {
		mappingType = "node"
	}

	// 分配主机端口（起始端口）
	hostPort := req.HostPort

	if mappingType == "controller" {
		controllerPortAllocateMu.Lock()
		defer controllerPortAllocateMu.Unlock()

		// 控制端转发模式：使用控制端端口，不受节点端口范围限制
		if hostPort == 0 {
			// 自动分配控制端端口（10000-65535 范围，避免与已有控制端端口冲突）
			allocatedPort, err := s.allocateControllerPort(providerInfo.ID, 10000, 65535, portCount)
			if err != nil {
				return 0, nil, fmt.Errorf("控制端端口分配失败: %v", err)
			}
			hostPort = allocatedPort
		} else {
			// 检查端口有效范围
			if hostPort < 1 || hostPort > 65535 {
				return 0, nil, fmt.Errorf("控制端端口 %d 无效，必须在 1-65535 范围内", hostPort)
			}
			hostPortEnd := hostPort + portCount - 1
			if hostPortEnd > 65535 {
				return 0, nil, fmt.Errorf("控制端端口段 %d-%d 超出有效范围", hostPort, hostPortEnd)
			}
			// 检查控制端端口是否已被占用。pending/deleting 期间监听器或恢复流程可能仍在运行，
			// 不能把这些端口重新分配给其他控制端转发。
			var occupiedPorts []int
			err := global.APP_DB.Model(&provider.Port{}).
				Where("mapping_type = 'controller' AND host_port BETWEEN ? AND ? AND status IN ?",
					hostPort, hostPort+portCount-1, []string{"active", "pending", "deleting"}).
				Pluck("host_port", &occupiedPorts).Error
			if err != nil {
				return 0, nil, fmt.Errorf("检查控制端端口占用失败: %v", err)
			}
			if len(occupiedPorts) > 0 {
				return 0, nil, fmt.Errorf("控制端端口段中有端口已被占用: %v", occupiedPorts)
			}
		}
	} else {
		// 节点侧映射模式：验证内部端口段合法性
		if err := s.ValidatePortRange(providerInfo.ID, req.GuestPort, portCount); err != nil {
			return 0, nil, fmt.Errorf("内部端口段验证失败: %v", err)
		}

		if hostPort == 0 {
			// 自动分配连续端口段
			allocatedPort, err := s.allocateConsecutivePorts(providerInfo.ID, providerInfo.PortRangeStart, providerInfo.PortRangeEnd, portCount)
			if err != nil {
				return 0, nil, fmt.Errorf("端口分配失败: %v", err)
			}
			hostPort = allocatedPort
		} else {
			// 检查主机端口是否在Provider允许的范围内
			if hostPort < providerInfo.PortRangeStart || hostPort > providerInfo.PortRangeEnd {
				return 0, nil, fmt.Errorf("%w: 主机端口 %d 不在节点允许的范围内 (%d-%d) / Host port %d is not within the node's allowed range (%d-%d)",
					ErrPortRangeValidation,
					hostPort, providerInfo.PortRangeStart, providerInfo.PortRangeEnd,
					hostPort, providerInfo.PortRangeStart, providerInfo.PortRangeEnd)
			}

			// 检查端口段是否超出范围
			hostPortEnd := hostPort + portCount - 1
			if hostPortEnd > providerInfo.PortRangeEnd {
				return 0, nil, fmt.Errorf("%w: 主机端口段 %d-%d 超出节点允许的范围 (最大端口: %d) / Host port range %d-%d exceeds the node's allowed range (maximum port: %d)",
					ErrPortRangeValidation,
					hostPort, hostPortEnd, providerInfo.PortRangeEnd,
					hostPort, hostPortEnd, providerInfo.PortRangeEnd)
			}

			// 批量检查指定的端口段是否可用
			var occupiedPorts []int
			err := global.APP_DB.Model(&provider.Port{}).
				Where("provider_id = ? AND host_port BETWEEN ? AND ? AND status = 'active'",
					providerInfo.ID, hostPort, hostPort+portCount-1).
				Pluck("host_port", &occupiedPorts).Error
			if err != nil {
				return 0, nil, fmt.Errorf("检查端口占用失败: %v", err)
			}
			if len(occupiedPorts) > 0 {
				return 0, nil, fmt.Errorf("端口段中有端口已被占用: %v", occupiedPorts)
			}
		}
	}

	// 计算端口段的结束端口
	hostPortEnd := 0
	guestPortEnd := 0
	if portCount > 1 {
		hostPortEnd = hostPort + portCount - 1
		guestPortEnd = req.GuestPort + portCount - 1
	}

	internalHost := req.InternalHost

	// 创建数据库记录（状态为 pending）
	// 根据端口数量决定 PortType: 单端口为 manual，多端口段为 batch
	portTypeValue := "manual"
	if portCount > 1 {
		portTypeValue = "batch"
	}

	port := provider.Port{
		InstanceID:    req.InstanceID,
		ProviderID:    providerInfo.ID,
		HostPort:      hostPort,
		HostPortEnd:   hostPortEnd,
		GuestPort:     req.GuestPort,
		GuestPortEnd:  guestPortEnd,
		PortCount:     portCount,
		Protocol:      req.Protocol,
		Description:   req.Description,
		Status:        "pending",
		IsSSH:         req.GuestPort == 22,
		IsAutomatic:   false,
		PortType:      portTypeValue,
		IPv6Enabled:   false,
		MappingMethod: providerInfo.IPv4PortMappingMethod,
		MappingType:   mappingType,
		InternalHost:  internalHost,
	}

	if err := global.APP_DB.Create(&port).Error; err != nil {
		global.APP_LOG.Error("创建端口映射数据库记录失败", zap.Error(err))
		return 0, nil, fmt.Errorf("创建端口映射失败: %v", err)
	}

	// 控制端转发模式：直接启动 TCP 监听，跳过节点侧任务
	if mappingType == "controller" {
		targetHost := internalHost
		if targetHost == "" {
			targetHost = instance.PrivateIP // 使用实例私有IP
		}
		if targetHost == "" {
			global.APP_DB.Unscoped().Delete(&port)
			return 0, nil, fmt.Errorf("控制端转发模式需要指定目标地址（internalHost）或实例须有私有IP")
		}
		if ControllerPortForwardFunc == nil {
			global.APP_DB.Unscoped().Delete(&port)
			return 0, nil, fmt.Errorf("控制端转发功能未初始化")
		}
		if err := ControllerPortForwardFunc(port.ID, providerInfo.ID, hostPort, targetHost, req.GuestPort); err != nil {
			// 回滚端口记录（硬删除，释放唯一索引槽位）
			global.APP_DB.Unscoped().Delete(&port)
			return 0, nil, fmt.Errorf("启动控制端端口转发失败: %v", err)
		}
		// 标记为 active（控制端模式不需要任务）
		global.APP_DB.Model(&port).Update("status", "active")
		global.APP_LOG.Info("控制端端口转发已启动",
			zap.Uint("port_id", port.ID), zap.Int("host_port", hostPort))
		return port.ID, nil, nil
	}

	// 创建任务数据
	taskData := &admin.CreatePortMappingTaskRequest{
		PortID:       port.ID,
		InstanceID:   req.InstanceID,
		ProviderID:   providerInfo.ID,
		HostPort:     hostPort,
		HostPortEnd:  hostPortEnd,
		GuestPort:    req.GuestPort,
		GuestPortEnd: guestPortEnd,
		PortCount:    portCount,
		Protocol:     req.Protocol,
		Description:  req.Description,
	}

	if portCount == 1 {
		global.APP_LOG.Info("端口映射记录已创建，准备创建任务",
			zap.Uint("port_id", port.ID),
			zap.Uint("instance_id", req.InstanceID),
			zap.Int("host_port", hostPort),
			zap.Int("guest_port", req.GuestPort))
	} else {
		global.APP_LOG.Info("端口段映射记录已创建，准备创建任务",
			zap.Uint("port_id", port.ID),
			zap.Uint("instance_id", req.InstanceID),
			zap.String("host_port_range", fmt.Sprintf("%d-%d", hostPort, hostPortEnd)),
			zap.String("guest_port_range", fmt.Sprintf("%d-%d", req.GuestPort, guestPortEnd)),
			zap.Int("port_count", portCount))
	}

	return port.ID, taskData, nil
}

// UpdateProviderPortConfig 更新Provider端口配置
func (s *PortMappingService) UpdateProviderPortConfig(providerID uint, req admin.ProviderPortConfigRequest) error {
	// 验证端口范围
	if req.PortRangeStart >= req.PortRangeEnd {
		return fmt.Errorf("端口范围起始值必须小于结束值")
	}
	availablePortCount := req.PortRangeEnd - req.PortRangeStart + 1
	if req.DefaultPortCount > availablePortCount {
		return fmt.Errorf("默认端口数 %d 不能超过端口范围容量 %d", req.DefaultPortCount, availablePortCount)
	}
	fixedPorts, err := NormalizeProviderFixedPorts(req.FixedPorts, req.DefaultPortCount)
	if err != nil {
		return err
	}

	var providerInfo provider.Provider
	if err := global.APP_DB.Where("id = ?", providerID).First(&providerInfo).Error; err != nil {
		return fmt.Errorf("Provider不存在")
	}

	// 更新端口配置
	providerInfo.DefaultPortCount = req.DefaultPortCount
	providerInfo.PortRangeStart = req.PortRangeStart
	providerInfo.PortRangeEnd = req.PortRangeEnd
	providerInfo.FixedPorts = fixedPorts
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

// CreateDefaultPortMappings 为实例创建默认端口映射
func (s *PortMappingService) CreateDefaultPortMappings(instanceID uint, providerID uint) error {
	// 获取Provider配置
	var providerInfo provider.Provider
	if err := global.APP_DB.Where("id = ?", providerID).First(&providerInfo).Error; err != nil {
		return fmt.Errorf("Provider不存在")
	}

	useControllerMapping := providerInfo.ConnectionType == "agent" && providerInfo.PortIP == ""

	// 检查是否为独立IPv4模式、纯IPv6模式或无端口映射模式，如果是则跳过默认端口映射创建。
	// "无端口映射"语义上表示不自动创建任何端口映射，无论 SSH 还是 Agent 模式均不例外。
	// Agent 模式下若需要控制端转发，应使用 nat_ipv4 / nat_ipv4_ipv6 等 NAT 网络类型。
	if providerInfo.NetworkType == "dedicated_ipv4" || providerInfo.NetworkType == "dedicated_ipv4_ipv6" || providerInfo.NetworkType == "ipv6_only" || providerInfo.NetworkType == "no_port_mapping" {
		global.APP_LOG.Debug("独立IP模式或纯IPv6模式或无端口映射模式，跳过默认端口映射创建",
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

	if defaultPortCount > availablePortCount {
		return fmt.Errorf("端口范围可用数量不足，默认端口数 %d 大于范围容量 %d", defaultPortCount, availablePortCount)
	}

	fixedPorts, err := NormalizeProviderFixedPorts(providerInfo.FixedPorts, defaultPortCount)
	if err != nil {
		return err
	}
	ordinaryPortCount := defaultPortCount - len(fixedPorts)

	if useControllerMapping {
		controllerPortAllocateMu.Lock()
		defer controllerPortAllocateMu.Unlock()
	}

	// 使用事务确保端口分配的原子性，防止并发创建时的端口冲突
	return global.APP_DB.Transaction(func(tx *gorm.DB) error {
		var createdPorts []provider.Port

		var startPort int
		var allocatedPorts []int
		var err error
		if useControllerMapping {
			startPort, err = s.allocateControllerPortInTx(tx, providerInfo.ID, 10000, 65535, defaultPortCount)
			if err != nil {
				return fmt.Errorf("分配控制端连续端口区间失败: %v", err)
			}
			allocatedPorts = make([]int, defaultPortCount)
			for i := 0; i < defaultPortCount; i++ {
				allocatedPorts[i] = startPort + i
			}
		} else {
			// 分配连续的端口区间，确保所有端口都可用（数据库+实际占用检测）
			startPort, allocatedPorts, err = s.allocateConsecutivePortsInTx(tx, &providerInfo, defaultPortCount)
			if err != nil {
				return fmt.Errorf("分配连续端口区间失败: %v", err)
			}
		}

		// 确定映射类型：agent模式且无PortIP/no_port_mapping时使用控制端转发
		mappingType := "node"
		internalHost := ""
		if useControllerMapping {
			// Agent模式且无PortIP -> 控制端隧道内穿映射
			mappingType = "controller"
			// 获取实例的私有IP作为隧道目标
			var instance provider.Instance
			if err := tx.Where("id = ?", instanceID).Select("private_ip").First(&instance).Error; err == nil {
				internalHost = instance.PrivateIP
			}
		}

		sshHostPort := 0
		usedGuestPorts := make(map[int]struct{}, defaultPortCount)
		var portRecords []provider.Port
		for i, guestPort := range fixedPorts {
			usedGuestPorts[guestPort] = struct{}{}
			hostPort := allocatedPorts[i]
			if guestPort == requiredFixedSSHPort {
				sshHostPort = hostPort
			}
			description := fmt.Sprintf("固定端口%d", guestPort)
			if guestPort == requiredFixedSSHPort {
				description = "SSH"
			}
			portRecords = append(portRecords, provider.Port{
				InstanceID:    instanceID,
				ProviderID:    providerID,
				HostPort:      hostPort,
				GuestPort:     guestPort,
				Protocol:      "both",
				Description:   description,
				Status:        "active",
				IsSSH:         guestPort == requiredFixedSSHPort,
				IsAutomatic:   true,
				PortType:      "range_mapped",
				IPv6Enabled:   providerInfo.NetworkType == "nat_ipv4_ipv6" || providerInfo.NetworkType == "dedicated_ipv4_ipv6" || providerInfo.NetworkType == "ipv6_only",
				MappingMethod: providerInfo.IPv4PortMappingMethod,
				MappingType:   mappingType,
				InternalHost:  internalHost,
			})
		}
		if sshHostPort == 0 {
			return fmt.Errorf("固定端口配置缺少SSH端口22")
		}

		// 更新实例的SSH端口
		if err := tx.Model(&provider.Instance{}).Where("id = ?", instanceID).Update("ssh_port", sshHostPort).Error; err != nil {
			global.APP_LOG.Warn("更新实例SSH端口失败", zap.Error(err))
		}

		// 批量创建剩余普通映射。默认使用 host port 作为 guest port；
		// 若与固定 guest port 冲突，则顺延到下一个未占用 guest port。
		for i := len(fixedPorts); i < len(allocatedPorts); i++ {
			port := allocatedPorts[i]
			guestPort := port
			for attempts := 0; attempts < 65535; attempts++ {
				if _, exists := usedGuestPorts[guestPort]; !exists {
					break
				}
				guestPort++
				if guestPort > 65535 {
					guestPort = 1
				}
			}
			if _, exists := usedGuestPorts[guestPort]; exists {
				return fmt.Errorf("无法为普通端口映射分配未冲突的实例内端口")
			}
			usedGuestPorts[guestPort] = struct{}{}
			portRecords = append(portRecords, provider.Port{
				InstanceID:    instanceID,
				ProviderID:    providerID,
				HostPort:      port,
				GuestPort:     guestPort,
				Protocol:      "both",
				Description:   fmt.Sprintf("端口%d", guestPort),
				Status:        "active",
				IsSSH:         false,
				IsAutomatic:   true,
				PortType:      "range_mapped",
				IPv6Enabled:   providerInfo.NetworkType == "nat_ipv4_ipv6" || providerInfo.NetworkType == "dedicated_ipv4_ipv6" || providerInfo.NetworkType == "ipv6_only",
				MappingMethod: providerInfo.IPv4PortMappingMethod,
				MappingType:   mappingType,
				InternalHost:  internalHost,
			})
		}

		// 批量插入端口映射
		if err := tx.CreateInBatches(portRecords, 100).Error; err != nil {
			return fmt.Errorf("批量创建端口映射失败: %v", err)
		}
		createdPorts = append(createdPorts, portRecords...)

		if !useControllerMapping {
			// 更新NextAvailablePort到下一个端口；控制端端口不占用节点端口池。
			nextPort := startPort + defaultPortCount
			if nextPort > providerInfo.PortRangeEnd {
				nextPort = providerInfo.PortRangeStart
			}
			if err := tx.Model(&provider.Provider{}).Where("id = ?", providerID).Update("next_available_port", nextPort).Error; err != nil {
				global.APP_LOG.Warn("更新NextAvailablePort失败", zap.Error(err))
			}
		}

		global.APP_LOG.Info("创建默认端口映射成功",
			zap.Uint("instance_id", instanceID),
			zap.Int("total_ports", len(createdPorts)),
			zap.Int("fixed_ports", len(fixedPorts)),
			zap.Int("ordinary_ports", ordinaryPortCount),
			zap.Int("ssh_port", sshHostPort),
			zap.Int("start_port", startPort),
			zap.Int("end_port", allocatedPorts[len(allocatedPorts)-1]),
			zap.String("mapping_type", mappingType))

		return nil
	})
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
func (s *PortMappingService) GetUserPortMappings(userID uint, page, limit int, keyword, controllerHost string) ([]map[string]interface{}, int64, error) {
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

	// 获取实例ID列表和Provider ID列表
	instanceIDs := make([]uint, len(instances))
	instanceMap := make(map[uint]provider.Instance)
	providerIDsSet := make(map[uint]bool)

	for i, instance := range instances {
		instanceIDs[i] = instance.ID
		instanceMap[instance.ID] = instance
		if instance.ProviderID > 0 {
			providerIDsSet[instance.ProviderID] = true
		}
	}

	// 批量查询Provider信息
	providerMap := make(map[uint]provider.Provider)
	if len(providerIDsSet) > 0 {
		providerIDs := make([]uint, 0, len(providerIDsSet))
		for id := range providerIDsSet {
			providerIDs = append(providerIDs, id)
		}

		var providers []provider.Provider
		if err := global.APP_DB.Where("id IN ?", providerIDs).Find(&providers).Error; err == nil {
			for _, prov := range providers {
				providerMap[prov.ID] = prov
			}
		}
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

		// 从预加载的map中获取Provider信息
		if instance.ProviderID > 0 {
			if providerInfo, ok := providerMap[instance.ProviderID]; ok {
				// agent+no_port_mapping模式：显示控制端访问地址，保持与前端访问路径一致。
				if providerInfo.ConnectionType == "agent" && providerInfo.NetworkType == "no_port_mapping" {
					if controllerHost != "" {
						instanceData["publicIP"] = controllerHost
					}
				} else {
					// 优先使用PortIP，如果为空则使用Endpoint
					endpoint := providerInfo.PortIP
					if endpoint == "" {
						endpoint = providerInfo.Endpoint
					}
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
