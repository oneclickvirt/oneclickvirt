package resources

import (
	"errors"
	"fmt"
	"oneclickvirt/global"
	"oneclickvirt/model/admin"
	"oneclickvirt/model/provider"
	"strings"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// 定义错误类型
var (
	// ErrPortRangeValidation 端口范围验证错误（用于区分业务验证错误和系统错误）
	ErrPortRangeValidation = errors.New("port range validation error")
)

type PortMappingService struct{}

// GetPortMappingList 获取端口映射列表
func (s *PortMappingService) GetPortMappingList(req admin.PortMappingListRequest) ([]provider.Port, int64, error) {
	var ports []provider.Port
	var total int64

	query := global.APP_DB.Model(&provider.Port{})

	// 关键字搜索（实例名称）
	if req.Keyword != "" {
		// 子查询：查找名称匹配的实例ID列表
		var instanceIDs []uint
		if err := global.APP_DB.Model(&provider.Instance{}).
			Where("name LIKE ?", "%"+req.Keyword+"%").
			Pluck("id", &instanceIDs).Error; err != nil {
			global.APP_LOG.Error("搜索实例失败", zap.Error(err))
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

	// 只支持 LXD/Incus/Proxmox 手动添加端口
	if providerInfo.Type != "lxd" && providerInfo.Type != "incus" && providerInfo.Type != "proxmox" {
		return 0, nil, fmt.Errorf("不支持的 Provider 类型，手动添加端口仅支持 LXD/Incus/Proxmox")
	}

	// 检查是否为独立IPv4模式或纯IPv6模式
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

	// 默认端口数量为1
	portCount := req.PortCount
	if portCount == 0 {
		portCount = 1
	}

	// 验证端口数量
	if portCount < 1 || portCount > 1500 {
		return 0, nil, fmt.Errorf("端口数量必须在1-1500之间")
	}

	// 验证端口段合法性
	if err := s.ValidatePortRange(providerInfo.ID, req.GuestPort, portCount); err != nil {
		return 0, nil, fmt.Errorf("内部端口段验证失败: %v", err)
	}

	// 分配主机端口（起始端口）
	hostPort := req.HostPort
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

	// 计算端口段的结束端口
	hostPortEnd := 0
	guestPortEnd := 0
	if portCount > 1 {
		hostPortEnd = hostPort + portCount - 1
		guestPortEnd = req.GuestPort + portCount - 1
	}

	// 创建数据库记录（状态为 pending）
	// 对于端口段，创建一个主记录来代表整个端口段
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
		Status:        "pending", // 初始状态为 pending
		IsSSH:         req.GuestPort == 22,
		IsAutomatic:   false,
		PortType:      "batch", // 标记为批量添加（即使是单个端口也用batch类型）
		IPv6Enabled:   false,   // 手动添加的端口映射默认不启用IPv6
		MappingMethod: providerInfo.IPv4PortMappingMethod,
	}

	if err := global.APP_DB.Create(&port).Error; err != nil {
		global.APP_LOG.Error("创建端口映射数据库记录失败", zap.Error(err))
		return 0, nil, fmt.Errorf("创建端口映射失败: %v", err)
	}

	// 更新Provider的下一个可用端口
	if req.HostPort == 0 {
		global.APP_DB.Model(&providerInfo).Update("next_available_port", hostPort+portCount)
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

// CreateDefaultPortMappings 为实例创建默认端口映射
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

		// 分配连续的端口区间，确保所有端口都可用（数据库+实际占用检测）
		startPort, allocatedPorts, err := s.allocateConsecutivePortsInTx(tx, &providerInfo, defaultPortCount)
		if err != nil {
			return fmt.Errorf("分配连续端口区间失败: %v", err)
		}

		// 第一个端口作为SSH端口
		sshHostPort := allocatedPorts[0]
		sshPort := provider.Port{
			InstanceID:  instanceID,
			ProviderID:  providerID,
			HostPort:    sshHostPort,
			GuestPort:   22,     // SSH端口固定为22
			Protocol:    "both", // SSH 使用 TCP/UDP 通用协议
			Description: "SSH",
			Status:      "active",
			IsSSH:       true,
			IsAutomatic: true,
			PortType:    "range_mapped", // 标记为区间映射
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

		// 批量创建其余端口的1:1映射（避免循环插入）
		if len(allocatedPorts) > 1 {
			var portRecords []provider.Port
			for i := 1; i < len(allocatedPorts); i++ {
				port := allocatedPorts[i]
				portRecord := provider.Port{
					InstanceID:  instanceID,
					ProviderID:  providerID,
					HostPort:    port,
					GuestPort:   port,   // 内外端口完全相同
					Protocol:    "both", // 区间映射使用 TCP/UDP 通用协议
					Description: fmt.Sprintf("端口%d", port),
					Status:      "active",
					IsSSH:       false,
					IsAutomatic: true,
					PortType:    "range_mapped", // 标记为区间映射
					IPv6Enabled: providerInfo.NetworkType == "nat_ipv4_ipv6" || providerInfo.NetworkType == "dedicated_ipv4_ipv6" || providerInfo.NetworkType == "ipv6_only",
				}
				portRecords = append(portRecords, portRecord)
			}

			// 批量插入端口映射
			if err := tx.CreateInBatches(portRecords, 100).Error; err != nil {
				return fmt.Errorf("批量创建端口映射失败: %v", err)
			}
			createdPorts = append(createdPorts, portRecords...)
		}

		// 更新NextAvailablePort到下一个端口
		nextPort := startPort + defaultPortCount
		if nextPort > providerInfo.PortRangeEnd {
			nextPort = providerInfo.PortRangeStart
		}
		if err := tx.Model(&provider.Provider{}).Where("id = ?", providerID).Update("next_available_port", nextPort).Error; err != nil {
			global.APP_LOG.Warn("更新NextAvailablePort失败", zap.Error(err))
		}

		global.APP_LOG.Info("创建默认端口映射成功",
			zap.Uint("instance_id", instanceID),
			zap.Int("total_ports", len(createdPorts)),
			zap.Int("ssh_port", sshHostPort),
			zap.Int("start_port", startPort),
			zap.Int("end_port", allocatedPorts[len(allocatedPorts)-1]))

		return nil
	})
}

// allocateConsecutivePortsInTx 在事务中分配连续的端口区间
// 返回: 起始端口, 分配的端口列表, 错误
func (s *PortMappingService) allocateConsecutivePortsInTx(tx *gorm.DB, providerInfo *provider.Provider, count int) (int, []int, error) {
	rangeStart := providerInfo.PortRangeStart
	rangeEnd := providerInfo.PortRangeEnd

	// 检查端口范围是否足够
	if rangeEnd-rangeStart+1 < count {
		return 0, nil, fmt.Errorf("端口范围不足: 需要%d个端口, 但只有%d个端口可用", count, rangeEnd-rangeStart+1)
	}

	// 从NextAvailablePort开始查找
	startSearchPort := providerInfo.NextAvailablePort
	if startSearchPort < rangeStart || startSearchPort > rangeEnd {
		startSearchPort = rangeStart
	}

	// 批量检查整个范围内的端口可用性
	availablePorts, _ := s.batchCheckPortsAvailability(providerInfo, rangeStart, rangeEnd)

	// 构建可用端口集合以便快速查找
	availableSet := make(map[int]bool)
	for _, port := range availablePorts {
		availableSet[port] = true
	}

	global.APP_LOG.Debug("批量检查端口可用性完成",
		zap.Int("总范围", rangeEnd-rangeStart+1),
		zap.Int("可用端口数", len(availablePorts)),
		zap.Int("需要端口数", count))

	// 查找连续可用的端口段
	// 尝试两轮查找: 第一轮从NextAvailablePort到结尾，第二轮从开头到NextAvailablePort
	searchRanges := []struct{ start, end int }{
		{startSearchPort, rangeEnd - count + 1},
		{rangeStart, startSearchPort - 1},
	}

	for _, searchRange := range searchRanges {
		if searchRange.start > searchRange.end {
			continue
		}

		// 在当前搜索范围内查找连续可用的端口
		for startPort := searchRange.start; startPort <= searchRange.end; startPort++ {
			ports := make([]int, count)
			allAvailable := true

			// 检查从startPort开始的连续count个端口是否都可用
			for i := 0; i < count; i++ {
				port := startPort + i
				ports[i] = port

				if !availableSet[port] {
					allAvailable = false
					// 跳过这个已知不可用的区域
					startPort = port // 下次循环会从port+1开始
					break
				}
			}

			// 如果找到了连续的可用端口区间
			if allAvailable {
				// 在事务中再次确认（防止并发冲突）
				conflict := false
				for _, port := range ports {
					var existingPort provider.Port
					err := tx.Where("provider_id = ? AND host_port = ? AND status = 'active'",
						providerInfo.ID, port).First(&existingPort).Error

					if err != gorm.ErrRecordNotFound {
						conflict = true
						break
					}
				}

				if !conflict {
					global.APP_LOG.Info("成功分配连续端口区间",
						zap.Uint("providerId", providerInfo.ID),
						zap.Int("startPort", startPort),
						zap.Int("endPort", startPort+count-1),
						zap.Int("count", count),
						zap.Ints("ports", ports))
					return startPort, ports, nil
				}
			}
		}
	}

	// 没有找到足够的连续端口
	return 0, nil, fmt.Errorf("无法找到%d个连续的可用端口在范围%d-%d内", count, rangeStart, rangeEnd)
}

// allocateHostPort 分配主机端口 - 带并发保护和事务安全（先查询再事务）
func (s *PortMappingService) allocateHostPort(providerID uint, rangeStart, rangeEnd int) (int, error) {
	var allocatedPort int
	var providerInfo provider.Provider

	// 第一步：事务外查询已使用的端口（减少事务持有时间）
	if err := global.APP_DB.Where("id = ?", providerID).First(&providerInfo).Error; err != nil {
		return 0, fmt.Errorf("Provider不存在: %v", err)
	}

	startPort := providerInfo.NextAvailablePort
	if startPort < rangeStart {
		startPort = rangeStart
	}

	// 一次性查询该Provider所有活动端口，构建已用端口集合
	var usedPorts []int
	if err := global.APP_DB.Model(&provider.Port{}).
		Where("provider_id = ? AND status = 'active'", providerID).
		Pluck("host_port", &usedPorts).Error; err != nil && err != gorm.ErrRecordNotFound {
		return 0, fmt.Errorf("查询已用端口失败: %v", err)
	}

	// 构建已用端口的快速查找集合
	usedPortSet := make(map[int]bool)
	for _, port := range usedPorts {
		usedPortSet[port] = true
	}

	// 在事务外查找可用端口（快速遍历）
	var candidatePort int
	found := false

	// 从下一个可用端口开始查找
	for port := startPort; port <= rangeEnd; port++ {
		if !usedPortSet[port] {
			candidatePort = port
			found = true
			break
		}
	}

	// 如果从当前位置到结束都没有可用端口，从范围开始重新查找
	if !found && startPort > rangeStart {
		for port := rangeStart; port < startPort; port++ {
			if !usedPortSet[port] {
				candidatePort = port
				found = true
				break
			}
		}
	}

	if !found {
		return 0, fmt.Errorf("没有可用端口")
	}

	// 第二步：使用短事务进行最终分配（仅更新操作）
	err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
		// 获取Provider信息并锁定
		if err := tx.Set("gorm:query_option", "FOR UPDATE").Where("id = ?", providerID).First(&providerInfo).Error; err != nil {
			return fmt.Errorf("Provider不存在: %v", err)
		}

		// 二次确认端口未被占用（防止并发问题）
		var existingPort provider.Port
		err := tx.Where("provider_id = ? AND host_port = ? AND status = 'active'",
			providerID, candidatePort).First(&existingPort).Error

		if err != nil && err != gorm.ErrRecordNotFound {
			return fmt.Errorf("检查端口失败: %v", err)
		}

		if err == nil {
			// 端口已被占用，事务失败需要重试
			return fmt.Errorf("端口 %d 已被占用，需要重试", candidatePort)
		}

		// 端口可用，更新NextAvailablePort
		allocatedPort = candidatePort
		nextPort := candidatePort + 1
		if nextPort > rangeEnd {
			nextPort = rangeStart // 循环使用端口范围
		}

		return tx.Model(&provider.Provider{}).
			Where("id = ?", providerID).
			Update("next_available_port", nextPort).Error
	})

	if err != nil {
		// 如果是端口被占用，尝试重试一次（使用递归，但最多重试3次）
		if strings.Contains(err.Error(), "已被占用") {
			return s.allocateHostPortWithRetry(providerID, rangeStart, rangeEnd, 0)
		}
		return 0, err
	}

	global.APP_LOG.Info("分配端口成功",
		zap.Uint("providerId", providerID),
		zap.Int("allocatedPort", allocatedPort),
		zap.Int("nextPort", providerInfo.NextAvailablePort))

	return allocatedPort, nil
}

// allocateHostPortWithRetry 带重试的端口分配（内部辅助函数）
func (s *PortMappingService) allocateHostPortWithRetry(providerID uint, rangeStart, rangeEnd int, retryCount int) (int, error) {
	const maxRetries = 3
	if retryCount >= maxRetries {
		return 0, fmt.Errorf("端口分配失败：超过最大重试次数 %d", maxRetries)
	}

	// 短暂延迟后重试
	time.Sleep(time.Duration(50*(retryCount+1)) * time.Millisecond)

	var allocatedPort int
	var providerInfo provider.Provider

	// 重新查询已用端口
	if err := global.APP_DB.Where("id = ?", providerID).First(&providerInfo).Error; err != nil {
		return 0, fmt.Errorf("Provider不存在: %v", err)
	}

	startPort := providerInfo.NextAvailablePort
	if startPort < rangeStart {
		startPort = rangeStart
	}

	var usedPorts []int
	if err := global.APP_DB.Model(&provider.Port{}).
		Where("provider_id = ? AND status = 'active'", providerID).
		Pluck("host_port", &usedPorts).Error; err != nil && err != gorm.ErrRecordNotFound {
		return 0, fmt.Errorf("查询已用端口失败: %v", err)
	}

	usedPortSet := make(map[int]bool)
	for _, port := range usedPorts {
		usedPortSet[port] = true
	}

	// 查找可用端口
	var candidatePort int
	found := false
	for port := startPort; port <= rangeEnd; port++ {
		if !usedPortSet[port] {
			candidatePort = port
			found = true
			break
		}
	}

	if !found && startPort > rangeStart {
		for port := rangeStart; port < startPort; port++ {
			if !usedPortSet[port] {
				candidatePort = port
				found = true
				break
			}
		}
	}

	if !found {
		return 0, fmt.Errorf("没有可用端口")
	}

	// 使用短事务进行分配
	err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Set("gorm:query_option", "FOR UPDATE").Where("id = ?", providerID).First(&providerInfo).Error; err != nil {
			return fmt.Errorf("Provider不存在: %v", err)
		}

		var existingPort provider.Port
		err := tx.Where("provider_id = ? AND host_port = ? AND status = 'active'",
			providerID, candidatePort).First(&existingPort).Error

		if err != nil && err != gorm.ErrRecordNotFound {
			return fmt.Errorf("检查端口失败: %v", err)
		}

		if err == nil {
			return fmt.Errorf("端口 %d 已被占用，需要重试", candidatePort)
		}

		allocatedPort = candidatePort
		nextPort := candidatePort + 1
		if nextPort > rangeEnd {
			nextPort = rangeStart
		}

		return tx.Model(&provider.Provider{}).
			Where("id = ?", providerID).
			Update("next_available_port", nextPort).Error
	})

	if err != nil {
		if strings.Contains(err.Error(), "已被占用") {
			return s.allocateHostPortWithRetry(providerID, rangeStart, rangeEnd, retryCount+1)
		}
		return 0, err
	}

	return allocatedPort, nil
}

// allocateConsecutivePorts 分配连续的端口段
// 返回起始端口号，如果无法找到连续端口段则返回错误
func (s *PortMappingService) allocateConsecutivePorts(providerID uint, rangeStart, rangeEnd int, portCount int) (int, error) {
	if portCount <= 0 {
		return 0, fmt.Errorf("端口数量必须大于0")
	}

	if portCount == 1 {
		// 单个端口直接使用原来的方法
		return s.allocateHostPort(providerID, rangeStart, rangeEnd)
	}

	var providerInfo provider.Provider
	if err := global.APP_DB.Where("id = ?", providerID).First(&providerInfo).Error; err != nil {
		return 0, fmt.Errorf("Provider不存在: %v", err)
	}

	// 检查端口段是否超出范围
	if rangeStart+portCount-1 > rangeEnd {
		return 0, fmt.Errorf("所需端口数量(%d)超出可用范围", portCount)
	}

	// 使用批量检测获取所有可用端口
	availablePorts, _ := s.batchCheckPortsAvailability(&providerInfo, rangeStart, rangeEnd)

	// 构建可用端口集合
	availableSet := make(map[int]bool)
	for _, port := range availablePorts {
		availableSet[port] = true
	}

	global.APP_LOG.Debug("批量端口检查完成",
		zap.Int("总端口数", rangeEnd-rangeStart+1),
		zap.Int("可用端口数", len(availablePorts)),
		zap.Int("需要端口数", portCount))

	// 查找连续可用的端口段
	startPort := providerInfo.NextAvailablePort
	if startPort < rangeStart {
		startPort = rangeStart
	}

	// 辅助函数：检查从某个端口开始的连续端口是否都可用
	isConsecutiveAvailable := func(start int) bool {
		if start+portCount-1 > rangeEnd {
			return false
		}
		for i := 0; i < portCount; i++ {
			if !availableSet[start+i] {
				return false
			}
		}
		return true
	}

	// 从NextAvailablePort开始查找
	var candidateStart int
	found := false

	for port := startPort; port <= rangeEnd-portCount+1; port++ {
		if isConsecutiveAvailable(port) {
			candidateStart = port
			found = true
			break
		}
	}

	// 如果从当前位置到结束都没找到，从范围开始重新查找
	if !found && startPort > rangeStart {
		for port := rangeStart; port < startPort && port <= rangeEnd-portCount+1; port++ {
			if isConsecutiveAvailable(port) {
				candidateStart = port
				found = true
				break
			}
		}
	}

	if !found {
		return 0, fmt.Errorf("无法找到%d个连续可用端口", portCount)
	}

	// 使用事务确保端口段分配的原子性
	var allocatedPort int
	err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
		// 锁定Provider行
		if err := tx.Set("gorm:query_option", "FOR UPDATE").Where("id = ?", providerID).First(&providerInfo).Error; err != nil {
			return fmt.Errorf("Provider不存在: %v", err)
		}

		// 再次确认端口段可用（防止并发冲突）
		for i := 0; i < portCount; i++ {
			checkPort := candidateStart + i
			var existingPort provider.Port
			err := tx.Where("provider_id = ? AND host_port = ? AND status = 'active'",
				providerID, checkPort).First(&existingPort).Error

			if err != nil && err != gorm.ErrRecordNotFound {
				return fmt.Errorf("检查端口%d失败: %v", checkPort, err)
			}

			if err == nil {
				return fmt.Errorf("端口%d已被占用", checkPort)
			}
		}

		// 更新NextAvailablePort
		allocatedPort = candidateStart
		nextPort := candidateStart + portCount
		if nextPort > rangeEnd {
			nextPort = rangeStart
		}

		return tx.Model(&provider.Provider{}).
			Where("id = ?", providerID).
			Update("next_available_port", nextPort).Error
	})

	if err != nil {
		global.APP_LOG.Error("分配连续端口段失败",
			zap.Uint("providerId", providerID),
			zap.Int("portCount", portCount),
			zap.Error(err))
		return 0, err
	}

	global.APP_LOG.Info("成功分配连续端口段",
		zap.Uint("providerId", providerID),
		zap.Int("startPort", allocatedPort),
		zap.Int("endPort", allocatedPort+portCount-1),
		zap.Int("portCount", portCount))

	return allocatedPort, nil
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

// optimizeNextAvailablePortInTx 在事务中Provider的NextAvailablePort以促进端口重用
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
