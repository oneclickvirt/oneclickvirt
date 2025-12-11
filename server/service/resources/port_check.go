package resources

import (
	"fmt"
	"oneclickvirt/global"
	"oneclickvirt/model/admin"
	"oneclickvirt/model/provider"
	"oneclickvirt/utils"
	"strconv"
	"strings"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// CheckPortAvailability 统一的端口可用性检测服务
// 支持单个端口和端口段的批量检测，返回详细的检测结果
func (s *PortMappingService) CheckPortAvailability(req admin.CheckPortAvailabilityRequest) (*admin.CheckPortAvailabilityResponse, error) {
	// 获取Provider信息
	var providerInfo provider.Provider
	if err := global.APP_DB.Where("id = ?", req.ProviderID).First(&providerInfo).Error; err != nil {
		return nil, fmt.Errorf("Provider不存在")
	}

	// 默认端口数量为1
	portCount := req.PortCount
	if portCount == 0 {
		portCount = 1
	}

	// 检查端口范围是否合法
	if req.HostPort < providerInfo.PortRangeStart || req.HostPort > providerInfo.PortRangeEnd {
		return &admin.CheckPortAvailabilityResponse{
			Available: false,
			Message: fmt.Sprintf("端口 %d 不在节点允许的范围内 (%d-%d)",
				req.HostPort, providerInfo.PortRangeStart, providerInfo.PortRangeEnd),
		}, nil
	}

	endPort := req.HostPort + portCount - 1
	if endPort > providerInfo.PortRangeEnd {
		return &admin.CheckPortAvailabilityResponse{
			Available: false,
			Message: fmt.Sprintf("端口段 %d-%d 超出节点允许的范围 (%d-%d)",
				req.HostPort, endPort, providerInfo.PortRangeStart, providerInfo.PortRangeEnd),
		}, nil
	}

	// 一次性检查所有端口的可用性
	availablePorts, unavailablePorts := s.batchCheckPortsAvailability(&providerInfo, req.HostPort, endPort)

	// 构建响应
	response := &admin.CheckPortAvailabilityResponse{
		Available:        len(unavailablePorts) == 0,
		AvailablePorts:   availablePorts,
		UnavailablePorts: unavailablePorts,
	}

	// 生成端口范围描述
	if portCount == 1 {
		response.PortRange = fmt.Sprintf("%d", req.HostPort)
	} else {
		response.PortRange = fmt.Sprintf("%d-%d", req.HostPort, endPort)
	}

	// 生成检查结果描述
	if response.Available {
		if portCount == 1 {
			response.Message = fmt.Sprintf("端口 %d 可用", req.HostPort)
		} else {
			response.Message = fmt.Sprintf("端口段 %s 的所有 %d 个端口均可用", response.PortRange, portCount)
		}
	} else {
		unavailableCount := len(unavailablePorts)
		availableCount := len(availablePorts)

		if portCount == 1 {
			response.Message = fmt.Sprintf("端口 %d 已被占用", req.HostPort)
		} else {
			response.Message = fmt.Sprintf("端口段 %s 中有 %d 个端口被占用，%d 个端口可用",
				response.PortRange, unavailableCount, availableCount)
		}

		// 提供替代建议
		if unavailableCount > 0 {
			suggestion := s.suggestAlternativePorts(&providerInfo, portCount, req.HostPort)
			if suggestion != "" {
				response.Suggestion = suggestion
			}
		}
	}

	global.APP_LOG.Info("端口可用性检查完成",
		zap.Uint("providerId", req.ProviderID),
		zap.String("portRange", response.PortRange),
		zap.Bool("available", response.Available),
		zap.Int("availableCount", len(availablePorts)),
		zap.Int("unavailableCount", len(unavailablePorts)))

	return response, nil
}

// suggestAlternativePorts 查找并建议可用的替代端口
func (s *PortMappingService) suggestAlternativePorts(providerInfo *provider.Provider, portCount int, preferStart int) string {
	// 尝试查找连续可用的端口段
	maxAttempts := 10 // 最多尝试10个位置
	attemptCount := 0
	currentStart := preferStart + 100 // 从首选位置之后100开始搜索

	for attemptCount < maxAttempts && currentStart+portCount-1 <= providerInfo.PortRangeEnd {
		// 使用批量检测方法
		availablePorts, _ := s.batchCheckPortsAvailability(providerInfo, currentStart, currentStart+portCount-1)

		// 检查是否找到连续的可用端口
		if len(availablePorts) == portCount {
			if portCount == 1 {
				return fmt.Sprintf("建议使用端口 %d", currentStart)
			}
			return fmt.Sprintf("建议使用端口段 %d-%d (%d个端口)", currentStart, currentStart+portCount-1, portCount)
		}

		currentStart += 10 // 每次跳过10个端口继续搜索
		attemptCount++
	}

	// 如果向后搜索没找到，尝试向前搜索
	currentStart = providerInfo.PortRangeStart
	attemptCount = 0

	for attemptCount < maxAttempts && currentStart+portCount-1 < preferStart {
		// 使用批量检测方法
		availablePorts, _ := s.batchCheckPortsAvailability(providerInfo, currentStart, currentStart+portCount-1)

		if len(availablePorts) == portCount {
			if portCount == 1 {
				return fmt.Sprintf("建议使用端口 %d", currentStart)
			}
			return fmt.Sprintf("建议使用端口段 %d-%d (%d个端口)", currentStart, currentStart+portCount-1, portCount)
		}

		currentStart += 10
		attemptCount++
	}

	return "未找到合适的可用端口，请联系管理员"
}

// BatchCheckPortAvailability 批量检查多个端口的可用性（用于前端实时检查）
func (s *PortMappingService) BatchCheckPortAvailability(providerID uint, ports []int, protocol string) map[int]bool {
	var providerInfo provider.Provider
	if err := global.APP_DB.Where("id = ?", providerID).First(&providerInfo).Error; err != nil {
		global.APP_LOG.Error("获取Provider信息失败", zap.Error(err))
		// 返回全部不可用
		result := make(map[int]bool)
		for _, port := range ports {
			result[port] = false
		}
		return result
	}

	if len(ports) == 0 {
		return make(map[int]bool)
	}

	// 找到端口范围
	minPort, maxPort := ports[0], ports[0]
	for _, port := range ports {
		if port < minPort {
			minPort = port
		}
		if port > maxPort {
			maxPort = port
		}
	}

	// 使用批量检测
	availablePorts, _ := s.batchCheckPortsAvailability(&providerInfo, minPort, maxPort)

	// 构建可用端口集合
	availableSet := make(map[int]bool)
	for _, port := range availablePorts {
		availableSet[port] = true
	}

	// 返回结果
	result := make(map[int]bool)
	for _, port := range ports {
		result[port] = availableSet[port]
	}

	return result
}

// GetPortConflictDetails 获取端口冲突的详细信息
func (s *PortMappingService) GetPortConflictDetails(providerID uint, port int) (*PortConflictInfo, error) {
	var providerInfo provider.Provider
	if err := global.APP_DB.Where("id = ?", providerID).First(&providerInfo).Error; err != nil {
		return nil, fmt.Errorf("Provider不存在")
	}

	// 查询数据库中的端口占用情况
	var existingPort provider.Port
	dbConflict := false
	if err := global.APP_DB.Where("provider_id = ? AND host_port = ? AND status = 'active'",
		providerID, port).First(&existingPort).Error; err == nil {
		dbConflict = true
	}

	// 系统级别的端口检查
	systemConflict := !s.isGenericPortAvailable(&providerInfo, port)

	conflictInfo := &PortConflictInfo{
		Port:           port,
		IsConflict:     dbConflict || systemConflict,
		DBConflict:     dbConflict,
		SystemConflict: systemConflict,
	}

	if dbConflict {
		// 获取占用此端口的实例信息
		var instance provider.Instance
		if err := global.APP_DB.Where("id = ?", existingPort.InstanceID).First(&instance).Error; err == nil {
			conflictInfo.InstanceName = instance.Name
			conflictInfo.InstanceID = instance.ID
		}
		conflictInfo.GuestPort = existingPort.GuestPort
		conflictInfo.Protocol = existingPort.Protocol
		conflictInfo.Description = existingPort.Description
	}

	return conflictInfo, nil
}

// PortConflictInfo 端口冲突详细信息
type PortConflictInfo struct {
	Port           int    `json:"port"`           // 端口号
	IsConflict     bool   `json:"isConflict"`     // 是否有冲突
	DBConflict     bool   `json:"dbConflict"`     // 数据库中是否有冲突
	SystemConflict bool   `json:"systemConflict"` // 系统级别是否有冲突
	InstanceName   string `json:"instanceName"`   // 占用端口的实例名称
	InstanceID     uint   `json:"instanceId"`     // 占用端口的实例ID
	GuestPort      int    `json:"guestPort"`      // 映射的内部端口
	Protocol       string `json:"protocol"`       // 协议
	Description    string `json:"description"`    // 描述
}

// ValidatePortRange 验证端口段的合法性
func (s *PortMappingService) ValidatePortRange(providerID uint, startPort int, portCount int) error {
	var providerInfo provider.Provider
	if err := global.APP_DB.Where("id = ?", providerID).First(&providerInfo).Error; err != nil {
		return fmt.Errorf("Provider不存在")
	}

	// 检查起始端口是否在范围内
	if startPort < providerInfo.PortRangeStart || startPort > providerInfo.PortRangeEnd {
		return fmt.Errorf("%w: 起始端口 %d 不在节点允许的范围内 (%d-%d)",
			ErrPortRangeValidation,
			startPort, providerInfo.PortRangeStart, providerInfo.PortRangeEnd)
	}

	// 检查端口段是否超出范围
	endPort := startPort + portCount - 1
	if endPort > providerInfo.PortRangeEnd {
		return fmt.Errorf("%w: 端口段 %d-%d 超出节点允许的范围 (最大端口: %d)",
			ErrPortRangeValidation,
			startPort, endPort, providerInfo.PortRangeEnd)
	}

	// 检查端口数量是否合理
	if portCount < 1 {
		return fmt.Errorf("端口数量必须大于0")
	}

	if portCount > 1500 {
		return fmt.Errorf("单次最多只能添加1500个端口")
	}

	return nil
}

// FormatPortRange 格式化端口范围显示
func FormatPortRange(startPort, endPort int) string {
	if startPort == endPort || endPort == 0 {
		return fmt.Sprintf("%d", startPort)
	}
	return fmt.Sprintf("%d-%d", startPort, endPort)
}

// ParsePortDescription 解析端口描述，支持批量端口的描述模板
func ParsePortDescription(description string, port int, totalPorts int) string {
	if description == "" {
		if totalPorts > 1 {
			return fmt.Sprintf("端口段 %d/%d", port, totalPorts)
		}
		return "手动添加的端口"
	}

	// 支持模板变量替换
	result := strings.ReplaceAll(description, "{port}", fmt.Sprintf("%d", port))
	result = strings.ReplaceAll(result, "{total}", fmt.Sprintf("%d", totalPorts))
	return result
}

// batchCheckPortsAvailability 批量检查端口可用性
// 使用系统命令(ss/netstat)一次性获取所有占用端口，然后与数据库记录合并判断
func (s *PortMappingService) batchCheckPortsAvailability(providerInfo *provider.Provider, startPort, endPort int) ([]int, []int) {
	var availablePorts []int
	var unavailablePorts []int

	// 1. 构建需要检查的端口列表
	portsToCheck := make([]int, 0, endPort-startPort+1)
	for port := startPort; port <= endPort; port++ {
		portsToCheck = append(portsToCheck, port)
	}

	// 2. 批量查询数据库中已占用的端口（一次查询）
	dbOccupiedPorts := make(map[int]bool)
	var dbPorts []provider.Port
	err := global.APP_DB.Where("provider_id = ? AND host_port >= ? AND host_port <= ? AND status = ?",
		providerInfo.ID, startPort, endPort, "active").
		Select("host_port").
		Find(&dbPorts).Error

	if err != nil && err != gorm.ErrRecordNotFound {
		global.APP_LOG.Error("批量查询数据库端口占用失败", zap.Error(err))
	}

	for _, p := range dbPorts {
		dbOccupiedPorts[p.HostPort] = true
	}

	global.APP_LOG.Debug("数据库端口占用查询完成",
		zap.Int("查询范围", endPort-startPort+1),
		zap.Int("数据库占用", len(dbOccupiedPorts)))

	// 3. 使用系统命令批量检查宿主机端口占用（一次系统调用）
	systemOccupiedPorts := make(map[int]bool)

	// 创建SSH客户端连接到Provider节点
	sshClient, err := s.createSSHClientForProvider(providerInfo)
	if err != nil {
		global.APP_LOG.Warn("创建SSH连接失败，仅使用数据库结果",
			zap.Error(err),
			zap.Uint("providerId", providerInfo.ID))
	} else {
		defer sshClient.Close()

		// 使用新的批量检测工具
		scanResult := utils.BatchCheckPortsOccupied(sshClient, portsToCheck)
		if scanResult.Error != nil {
			global.APP_LOG.Warn("系统端口扫描失败，仅使用数据库结果",
				zap.Error(scanResult.Error),
				zap.Uint("providerId", providerInfo.ID))
		} else {
			systemOccupiedPorts = scanResult.OccupiedPorts
			global.APP_LOG.Debug("系统端口扫描完成",
				zap.Int("扫描端口数", len(portsToCheck)),
				zap.Int("系统占用", len(systemOccupiedPorts)),
				zap.String("工具", string(scanResult.ScannerType)))
		}
	}

	// 4. 合并结果：数据库占用或系统占用的都视为不可用
	for _, port := range portsToCheck {
		if dbOccupiedPorts[port] || systemOccupiedPorts[port] {
			unavailablePorts = append(unavailablePorts, port)
		} else {
			availablePorts = append(availablePorts, port)
		}
	}

	global.APP_LOG.Info("批量端口检查完成",
		zap.Int("总端口数", len(portsToCheck)),
		zap.Int("可用端口", len(availablePorts)),
		zap.Int("不可用端口", len(unavailablePorts)))

	return availablePorts, unavailablePorts
}

// createSSHClientForProvider 为Provider创建SSH客户端
func (s *PortMappingService) createSSHClientForProvider(providerInfo *provider.Provider) (*utils.SSHClient, error) {
	// 获取SSH连接信息
	host := providerInfo.Endpoint
	if host == "" {
		return nil, fmt.Errorf("Provider的Endpoint为空")
	}

	// 解析主机地址（去除可能的端口号）
	hostParts := strings.Split(host, ":")
	hostname := hostParts[0]

	sshConfig := utils.SSHConfig{
		Host:     hostname,
		Port:     providerInfo.SSHPort,
		Username: providerInfo.Username,
		Password: providerInfo.Password,
	}

	// 如果有SSH密钥，优先使用密钥
	if providerInfo.SSHKey != "" {
		sshConfig.PrivateKey = providerInfo.SSHKey
	}

	// 如果Endpoint包含端口，使用指定的端口
	if len(hostParts) > 1 {
		if port, err := strconv.Atoi(hostParts[1]); err == nil {
			sshConfig.Port = port
		}
	}

	// 创建SSH客户端
	sshClient, err := utils.NewSSHClient(sshConfig)
	if err != nil {
		return nil, fmt.Errorf("创建SSH客户端失败: %v", err)
	}

	return sshClient, nil
}
