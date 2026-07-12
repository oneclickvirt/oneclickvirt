package resources

import (
	"fmt"
	"oneclickvirt/global"
	"oneclickvirt/model/provider"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var controllerPortAllocateMu sync.Mutex

// allocateConsecutivePortsInTx 从事务外已探测可用的端口中分配连续区间。
// 调用方必须先锁定 Provider 行，避免同一 Provider 的默认端口分配并发冲突。
// 返回: 起始端口, 分配的端口列表, 错误
func (s *PortMappingService) allocateConsecutivePortsInTx(tx *gorm.DB, providerInfo *provider.Provider, count int, scannedAvailablePorts []int) (int, []int, error) {
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

	// 系统端口探测可能涉及 SSH，必须在事务外完成。事务内只合并最新数据库占用状态。
	availableSet := make(map[int]bool)
	for _, port := range scannedAvailablePorts {
		if port >= rangeStart && port <= rangeEnd {
			availableSet[port] = true
		}
	}

	var occupiedRecords []provider.Port
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("provider_id = ? AND host_port <= ?", providerInfo.ID, rangeEnd).
		Where("mapping_type IS NULL OR mapping_type = '' OR mapping_type <> 'controller'").
		Select("host_port", "host_port_end", "port_count").
		Find(&occupiedRecords).Error; err != nil {
		return 0, nil, fmt.Errorf("查询已占用端口失败: %w", err)
	}
	for port := range collectOccupiedHostPorts(occupiedRecords, rangeStart, rangeEnd) {
		delete(availableSet, port)
	}

	global.APP_LOG.Debug("合并事务外端口探测与数据库占用完成",
		zap.Int("总范围", rangeEnd-rangeStart+1),
		zap.Int("可用端口数", len(availableSet)),
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

			// Provider 行已由调用方锁定，当前事务内不会有第二个默认分配器越过该游标。
			if allAvailable {
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

	// 一次性查询该Provider所有端口，构建已用端口集合
	// 不过滤status：unique index 在 (provider_id, host_port) 上，任何status的记录都占用该端口
	var usedRecords []provider.Port
	if err := global.APP_DB.
		Where("provider_id = ?", providerID).
		Where("mapping_type IS NULL OR mapping_type = '' OR mapping_type <> 'controller'").
		Select("host_port", "host_port_end", "port_count").
		Find(&usedRecords).Error; err != nil && err != gorm.ErrRecordNotFound {
		return 0, fmt.Errorf("查询已用端口失败: %v", err)
	}

	// 构建已用端口的快速查找集合
	usedPortSet := make(map[int]bool)
	for port := range collectOccupiedHostPorts(usedRecords, rangeStart, rangeEnd) {
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

		// 二次确认端口未被占用（使用 LOCK IN SHARE MODE 防止并发幻读）
		// 不过滤status：unique index 在 (provider_id, host_port) 上，任何status的记录都占用该端口
		var existingPort provider.Port
		err := tx.Clauses(clause.Locking{Strength: "SHARE"}).
			Where("provider_id = ? AND host_port <= ?", providerID, candidatePort).
			Where("mapping_type IS NULL OR mapping_type = '' OR mapping_type <> 'controller'").
			Where("CASE WHEN host_port_end > 0 THEN host_port_end WHEN port_count > 1 THEN host_port + port_count - 1 ELSE host_port END >= ?", candidatePort).
			First(&existingPort).Error

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

	// 不过滤status：unique index 在 (provider_id, host_port) 上，任何status的记录都占用该端口
	var usedRecords []provider.Port
	if err := global.APP_DB.
		Where("provider_id = ?", providerID).
		Where("mapping_type IS NULL OR mapping_type = '' OR mapping_type <> 'controller'").
		Select("host_port", "host_port_end", "port_count").
		Find(&usedRecords).Error; err != nil && err != gorm.ErrRecordNotFound {
		return 0, fmt.Errorf("查询已用端口失败: %v", err)
	}

	usedPortSet := make(map[int]bool)
	for port := range collectOccupiedHostPorts(usedRecords, rangeStart, rangeEnd) {
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

		// 不过滤status：unique index 在 (provider_id, host_port) 上，任何status的记录都占用该端口
		var existingPort provider.Port
		err := tx.Where("provider_id = ? AND host_port <= ?", providerID, candidatePort).
			Where("mapping_type IS NULL OR mapping_type = '' OR mapping_type <> 'controller'").
			Where("CASE WHEN host_port_end > 0 THEN host_port_end WHEN port_count > 1 THEN host_port + port_count - 1 ELSE host_port END >= ?", candidatePort).
			First(&existingPort).Error

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

	var providerInfo provider.Provider
	if err := global.APP_DB.Where("id = ?", providerID).First(&providerInfo).Error; err != nil {
		return 0, fmt.Errorf("Provider不存在: %v", err)
	}

	// 检查端口段是否超出范围
	if rangeStart+portCount-1 > rangeEnd {
		return 0, fmt.Errorf("所需端口数量(%d)超出可用范围", portCount)
	}

	// SSH/系统端口探测在事务外完成，事务内只锁定 Provider、合并最新数据库占用并更新游标。
	availablePorts, _ := s.batchCheckPortsAvailability(&providerInfo, rangeStart, rangeEnd)
	if len(availablePorts) < portCount {
		return 0, fmt.Errorf("无法找到%d个连续可用端口", portCount)
	}

	var allocatedPort int
	err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", providerID).First(&providerInfo).Error; err != nil {
			return fmt.Errorf("Provider不存在: %v", err)
		}
		if providerInfo.PortRangeStart != rangeStart || providerInfo.PortRangeEnd != rangeEnd {
			return fmt.Errorf("Provider端口范围在分配期间发生变化，请重试")
		}

		startPort, _, allocateErr := s.allocateConsecutivePortsInTx(tx, &providerInfo, portCount, availablePorts)
		if allocateErr != nil {
			return allocateErr
		}
		allocatedPort = startPort
		nextPort := startPort + portCount
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

// OptimizeNextAvailablePortInTx 在事务中优化Provider的NextAvailablePort以促进端口重用
func (s *PortMappingService) OptimizeNextAvailablePortInTx(tx *gorm.DB, providerID uint, releasedPorts []int) error {
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

// allocateControllerPort 在控制端分配可用端口（不受节点端口范围限制）
// 查找 rangeStart-rangeEnd 范围内未被其他控制端转发占用的连续端口
func (s *PortMappingService) allocateControllerPort(providerID uint, rangeStart, rangeEnd, portCount int) (int, error) {
	return s.allocateControllerPortWithDB(global.APP_DB, providerID, rangeStart, rangeEnd, portCount)
}

func (s *PortMappingService) allocateControllerPortInTx(tx *gorm.DB, providerID uint, rangeStart, rangeEnd, portCount int) (int, error) {
	return s.allocateControllerPortWithDB(tx, providerID, rangeStart, rangeEnd, portCount)
}

func (s *PortMappingService) allocateControllerPortWithDB(db *gorm.DB, providerID uint, rangeStart, rangeEnd, portCount int) (int, error) {
	if portCount <= 0 {
		return 0, fmt.Errorf("端口数量必须大于0")
	}

	// 获取所有控制端转发模式的已用端口段。历史数据可能包含端口段，
	// 因此不能只比较每条记录的起始端口。
	var usedRecords []provider.Port
	if err := db.
		Where("mapping_type = 'controller' AND host_port <= ?", rangeEnd).
		Select("host_port", "host_port_end", "port_count").
		Find(&usedRecords).Error; err != nil {
		return 0, fmt.Errorf("查询控制端端口占用失败: %v", err)
	}

	usedSet := make(map[int]bool)
	for port := range collectOccupiedHostPorts(usedRecords, rangeStart, rangeEnd) {
		usedSet[port] = true
	}

	// 查找连续可用端口段
	for start := rangeStart; start <= rangeEnd-portCount+1; start++ {
		available := true
		for i := 0; i < portCount; i++ {
			if usedSet[start+i] {
				available = false
				break
			}
		}
		if available {
			return start, nil
		}
	}

	return 0, fmt.Errorf("控制端在 %d-%d 范围内找不到 %d 个连续可用端口", rangeStart, rangeEnd, portCount)
}
