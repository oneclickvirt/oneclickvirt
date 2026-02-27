package resources

import (
	"fmt"
	"oneclickvirt/global"
	"oneclickvirt/model/provider"
	"strings"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

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
					global.APP_LOG.Debug("成功分配连续端口区间",
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
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", providerID).First(&providerInfo).Error; err != nil {
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
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", providerID).First(&providerInfo).Error; err != nil {
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
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", providerID).First(&providerInfo).Error; err != nil {
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
