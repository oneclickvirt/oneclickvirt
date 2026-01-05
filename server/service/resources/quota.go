package resources

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"oneclickvirt/service/database"

	"oneclickvirt/config"
	"oneclickvirt/global"
	"oneclickvirt/model/provider"
	"oneclickvirt/model/user"

	"gorm.io/gorm"
)

// QuotaService 资源配额验证服务
type QuotaService struct {
	dbService *database.DatabaseService // 数据库服务
}

// NewQuotaService 创建配额服务
func NewQuotaService() *QuotaService {
	return &QuotaService{
		dbService: database.GetDatabaseService(),
	}
}

// ResourceRequest 资源请求
type ResourceRequest struct {
	UserID       uint
	CPU          int
	Memory       int64
	Disk         int64
	Bandwidth    int // 带宽字段
	InstanceType string
	ProviderID   uint //  Provider ID 用于节点级限制检查
}

// QuotaCheckResult 配额检查结果
type QuotaCheckResult struct {
	Allowed           bool
	Reason            string
	CurrentInstances  int
	MaxInstances      int
	CurrentResources  ResourceUsage
	MaxResources      ResourceUsage
	MaxQuota          ResourceUsage // MaxQuota字段
	RequiredResources ResourceUsage
}

// ResourceUsage 资源使用情况
type ResourceUsage struct {
	CPU       int
	Memory    int64
	Disk      int64
	Bandwidth int // 带宽字段
}

// GetResourceUsage 计算资源使用量（标准化计算方式）
func (r ResourceUsage) GetResourceUsage() int {
	// 统一的资源计算方式：CPU权重4，内存权重2，磁盘权重1
	// 这样可以更合理地反映资源价值
	return r.CPU*4 + int(r.Memory/512)*2 + int(r.Disk/5)*1
}

// ValidateInstanceCreation 验证实例创建请求
func (s *QuotaService) ValidateInstanceCreation(req ResourceRequest) (*QuotaCheckResult, error) {
	// 使用可序列化隔离级别的事务，防止幻读和并发超配
	var result *QuotaCheckResult
	var err error

	// 开启串行化事务隔离级别（最高级别，完全避免并发问题）
	err = global.APP_DB.Transaction(func(tx *gorm.DB) error {
		// 设置事务隔离级别为 SERIALIZABLE
		if err := tx.Exec("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE").Error; err != nil {
			return fmt.Errorf("设置事务隔离级别失败: %v", err)
		}

		result, err = s.validateInTransaction(tx, req)
		if err != nil {
			return err
		}

		if !result.Allowed {
			return errors.New(result.Reason)
		}

		return nil
	})

	return result, err
}

// ValidateInTransaction 在事务中进行配额验证（公开方法）
func (s *QuotaService) ValidateInTransaction(tx *gorm.DB, req ResourceRequest) (*QuotaCheckResult, error) {
	return s.validateInTransaction(tx, req)
}

// validateInTransaction 在事务中进行配额验证（增强版，防止并发竞争）
func (s *QuotaService) validateInTransaction(tx *gorm.DB, req ResourceRequest) (*QuotaCheckResult, error) {
	// 使用 SELECT FOR UPDATE 锁定用户记录
	// MySQL 5.x和部分MariaDB不支持NOWAIT，使用标准FOR UPDATE（兼容所有版本）
	var user user.User
	if err := tx.Set("gorm:query_option", "FOR UPDATE").First(&user, req.UserID).Error; err != nil {
		if strings.Contains(err.Error(), "Lock wait timeout") || strings.Contains(err.Error(), "timeout") {
			return nil, fmt.Errorf("系统繁忙，请稍后重试")
		}
		return nil, fmt.Errorf("用户不存在: %v", err)
	}

	// 检查用户状态
	if user.Status != 1 {
		return &QuotaCheckResult{
			Allowed: false,
			Reason:  "用户账户已被禁用",
		}, nil
	}

	// 获取用户等级限制
	levelLimits, exists := global.APP_CONFIG.Quota.LevelLimits[user.Level]
	if !exists {
		return &QuotaCheckResult{
			Allowed: false,
			Reason:  fmt.Sprintf("用户等级 %d 没有配置资源限制", user.Level),
		}, nil
	}

	// 如果提供了 ProviderID，需要获取并合并 Provider 的等级限制
	var providerLevelLimits *config.LevelLimitInfo
	var prov *provider.Provider
	if req.ProviderID > 0 {
		var err error
		var providerModel provider.Provider
		if err := tx.First(&providerModel, req.ProviderID).Error; err != nil {
			return nil, fmt.Errorf("Provider 不存在: %v", err)
		}
		prov = &providerModel

		providerLevelLimits, err = s.getProviderLevelLimits(tx, req.ProviderID, user.Level)
		if err != nil {
			return nil, fmt.Errorf("获取 Provider 等级限制失败: %v", err)
		}

		// 如果 Provider 有等级限制配置，则取两者的最小值用于后续验证
		// 但需要考虑 Provider 的超分配设置
		if providerLevelLimits != nil {
			levelLimits = s.mergeLevelLimitsWithOvercommit(levelLimits, *providerLevelLimits, prov, req.InstanceType)
		}
	}

	// 统计当前实例数量和资源使用
	currentInstances, currentResources, err := s.getCurrentResourceUsage(tx, req.UserID)
	if err != nil {
		return nil, fmt.Errorf("获取当前资源使用情况失败: %v", err)
	}

	// 如果提供了 ProviderID，还需要检查该用户在此节点上的实例数量
	var currentProviderInstances int
	if req.ProviderID > 0 {
		currentProviderInstances, err = s.getCurrentProviderInstanceCount(tx, req.UserID, req.ProviderID)
		if err != nil {
			return nil, fmt.Errorf("获取节点实例数量失败: %v", err)
		}
	}

	// 计算请求的资源
	requestedResources := ResourceUsage{
		CPU:       req.CPU,
		Memory:    req.Memory,
		Disk:      req.Disk,
		Bandwidth: req.Bandwidth,
	}

	// 获取最大允许资源
	maxResources := s.GetLevelMaxResources(levelLimits)

	result := &QuotaCheckResult{
		CurrentInstances:  currentInstances,
		MaxInstances:      levelLimits.MaxInstances,
		CurrentResources:  currentResources,
		MaxResources:      maxResources,
		MaxQuota:          maxResources, // 设置MaxQuota字段
		RequiredResources: requestedResources,
	}

	// 1. 检查用户全局实例数量限制
	if currentInstances >= levelLimits.MaxInstances {
		result.Allowed = false
		result.Reason = fmt.Sprintf("实例数量已达上限：当前 %d/%d", currentInstances, levelLimits.MaxInstances)
		return result, nil
	}

	// 1.5 如果有 Provider 限制，还需要检查用户在该节点的实例数量
	if req.ProviderID > 0 && providerLevelLimits != nil && providerLevelLimits.MaxInstances > 0 {
		// 这里使用的是合并前的 providerLevelLimits，因为我们要检查节点本身的限制
		if currentProviderInstances >= providerLevelLimits.MaxInstances {
			result.Allowed = false
			result.Reason = fmt.Sprintf("该节点实例数量已达上限：当前在此节点 %d/%d",
				currentProviderInstances, providerLevelLimits.MaxInstances)
			return result, nil
		}
	}

	// 2. 检查CPU限制（考虑超分配设置）
	shouldCheckCPU := true
	if req.ProviderID > 0 && prov != nil {
		switch req.InstanceType {
		case "container":
			shouldCheckCPU = prov.ContainerLimitCPU
		case "vm":
			shouldCheckCPU = prov.VMLimitCPU
		}
	}
	if shouldCheckCPU && currentResources.CPU+requestedResources.CPU > maxResources.CPU {
		result.Allowed = false
		result.Reason = fmt.Sprintf("CPU资源不足：需要 %d，当前使用 %d，最大允许 %d",
			requestedResources.CPU, currentResources.CPU, maxResources.CPU)
		return result, nil
	}

	// 3. 检查内存限制（考虑超分配设置）
	shouldCheckMemory := true
	if req.ProviderID > 0 && prov != nil {
		switch req.InstanceType {
		case "container":
			shouldCheckMemory = prov.ContainerLimitMemory
		case "vm":
			shouldCheckMemory = prov.VMLimitMemory
		}
	}
	if shouldCheckMemory && currentResources.Memory+requestedResources.Memory > maxResources.Memory {
		result.Allowed = false
		result.Reason = fmt.Sprintf("内存资源不足：需要 %dMB，当前使用 %dMB，最大允许 %dMB",
			requestedResources.Memory, currentResources.Memory, maxResources.Memory)
		return result, nil
	}

	// 4. 检查磁盘限制（考虑超分配设置）
	shouldCheckDisk := true
	if req.ProviderID > 0 && prov != nil {
		switch req.InstanceType {
		case "container":
			shouldCheckDisk = prov.ContainerLimitDisk
		case "vm":
			shouldCheckDisk = prov.VMLimitDisk
		}
	}
	if shouldCheckDisk && currentResources.Disk+requestedResources.Disk > maxResources.Disk {
		result.Allowed = false
		result.Reason = fmt.Sprintf("磁盘资源不足：需要 %dMB，当前使用 %dMB，最大允许 %dMB",
			requestedResources.Disk, currentResources.Disk, maxResources.Disk)
		return result, nil
	}

	// 5. 检查带宽限制
	if requestedResources.Bandwidth > maxResources.Bandwidth {
		result.Allowed = false
		result.Reason = fmt.Sprintf("带宽超出等级限制：需要 %dMbps，等级 %d 最大允许 %dMbps",
			requestedResources.Bandwidth, user.Level, maxResources.Bandwidth)
		return result, nil
	}

	// 6. 检查实例类型权限
	if !s.checkInstanceTypePermission(user.Level, req.InstanceType) {
		result.Allowed = false
		result.Reason = fmt.Sprintf("等级 %d 不允许创建 %s 类型的实例", user.Level, req.InstanceType)
		return result, nil
	}

	result.Allowed = true
	result.Reason = "资源验证通过"
	return result, nil
}

// getCurrentResourceUsage 获取当前资源使用情况（增强版，使用共享锁防止幻读）
func (s *QuotaService) getCurrentResourceUsage(tx *gorm.DB, userID uint) (int, ResourceUsage, error) {
	var instances []provider.Instance

	// 使用 LOCK IN SHARE MODE 共享锁，允许其他事务读取但不允许修改
	// MySQL 5.5 不支持 FOR SHARE，使用 LOCK IN SHARE MODE（MySQL 5.x/9.x 和 MariaDB 都支持）
	// 这样可以防止在统计过程中有新实例被创建（防止幻读）
	// 排除所有中间状态和无效状态：deleting(删除中)、deleted(已删除)、failed(失败)、creating(创建中)、resetting(重置中)
	// 只计算稳定状态的实例：running、stopped、paused等
	err := tx.Set("gorm:query_option", "LOCK IN SHARE MODE").
		Where("user_id = ? AND status NOT IN (?)", userID, []string{"deleting", "deleted", "failed", "creating", "resetting"}).
		Find(&instances).Error
	if err != nil {
		return 0, ResourceUsage{}, err
	}

	instanceCount := len(instances)
	totalResources := ResourceUsage{
		CPU:       0,
		Memory:    0,
		Disk:      0,
		Bandwidth: 0,
	}

	for _, instance := range instances {
		totalResources.CPU += instance.CPU
		totalResources.Memory += instance.Memory
		totalResources.Disk += instance.Disk
		totalResources.Bandwidth += instance.Bandwidth
	}

	return instanceCount, totalResources, nil
}

// getCurrentProviderInstanceCount 获取用户在指定 Provider 上的实例数量（增强版）
func (s *QuotaService) getCurrentProviderInstanceCount(tx *gorm.DB, userID uint, providerID uint) (int, error) {
	var count int64

	// 使用 LOCK IN SHARE MODE 共享锁，防止幻读
	// MySQL 5.5 不支持 FOR SHARE，使用 LOCK IN SHARE MODE（MySQL 5.x/9.x 和 MariaDB 都支持）
	// 排除所有中间状态和无效状态，只计算稳定状态的实例
	err := tx.Model(&provider.Instance{}).
		Set("gorm:query_option", "LOCK IN SHARE MODE").
		Where("user_id = ? AND provider_id = ? AND status NOT IN (?)",
			userID, providerID, []string{"deleting", "deleted", "failed", "creating", "resetting"}).
		Count(&count).Error

	if err != nil {
		return 0, err
	}

	return int(count), nil
}

// GetCurrentResourceUsageInTx 公开方法：在事务中获取当前资源使用情况
func (s *QuotaService) GetCurrentResourceUsageInTx(tx *gorm.DB, userID uint) (int, ResourceUsage, error) {
	return s.getCurrentResourceUsage(tx, userID)
}

// GetCurrentProviderInstanceCountInTx 公开方法：在事务中获取用户在指定 Provider 上的实例数量
func (s *QuotaService) GetCurrentProviderInstanceCountInTx(tx *gorm.DB, userID uint, providerID uint) (int, error) {
	return s.getCurrentProviderInstanceCount(tx, userID, providerID)
}

// GetProviderLevelLimitsInTx 公开方法：在事务中获取 Provider 的等级限制
func (s *QuotaService) GetProviderLevelLimitsInTx(tx *gorm.DB, providerID uint, userLevel int) (*config.LevelLimitInfo, error) {
	return s.getProviderLevelLimits(tx, providerID, userLevel)
}

// getLevelMaxResources 获取等级最大资源限制
func (s *QuotaService) GetLevelMaxResources(levelLimits config.LevelLimitInfo) ResourceUsage {
	maxResources := ResourceUsage{
		CPU:       1,     // 默认值
		Memory:    512,   // 默认值 (MB)
		Disk:      10240, // 默认值 (MB) 10GB = 10240MB
		Bandwidth: 100,   // 默认值 (Mbps)
	}

	if levelLimits.MaxResources != nil {
		if cpu, ok := levelLimits.MaxResources["cpu"].(int); ok {
			maxResources.CPU = cpu
		} else if cpuFloat, ok := levelLimits.MaxResources["cpu"].(float64); ok {
			maxResources.CPU = int(cpuFloat)
		}

		if memory, ok := levelLimits.MaxResources["memory"].(int); ok {
			maxResources.Memory = int64(memory)
		} else if memoryFloat, ok := levelLimits.MaxResources["memory"].(float64); ok {
			maxResources.Memory = int64(memoryFloat)
		}

		if disk, ok := levelLimits.MaxResources["disk"].(int); ok {
			maxResources.Disk = int64(disk)
		} else if diskFloat, ok := levelLimits.MaxResources["disk"].(float64); ok {
			maxResources.Disk = int64(diskFloat)
		}

		if bandwidth, ok := levelLimits.MaxResources["bandwidth"].(int); ok {
			maxResources.Bandwidth = bandwidth
		} else if bandwidthFloat, ok := levelLimits.MaxResources["bandwidth"].(float64); ok {
			maxResources.Bandwidth = int(bandwidthFloat)
		}
	}

	return maxResources
}

// getLevelBandwidthLimit 获取等级带宽限制
func (s *QuotaService) getLevelBandwidthLimit(level int) int {
	// 默认带宽限制：每个等级+100Mbps，从100Mbps开始
	baseBandwidth := 100
	return baseBandwidth + (level-1)*100
}

// checkInstanceTypePermission 检查实例类型权限
func (s *QuotaService) checkInstanceTypePermission(level int, instanceType string) bool {
	// 从配置中获取实例类型权限设置
	permissions := global.APP_CONFIG.Quota.InstanceTypePermissions

	switch instanceType {
	case "container":
		// 容器：所有等级用户都可创建
		return true
	case "vm":
		return level >= permissions.MinLevelForVM
	default:
		// 未知类型使用容器权限（所有等级可用）
		return true
	}
}

// UpdateUserQuotaAfterCreationWithTx 在指定事务中更新用户配额
func (s *QuotaService) UpdateUserQuotaAfterCreationWithTx(tx *gorm.DB, userID uint, resources ResourceUsage) error {
	// 直接使用事务，依赖数据库FOR UPDATE锁保证原子性
	updateOperation := func(db *gorm.DB) error {
		var user user.User
		// 使用FOR UPDATE防止并发修改
		query := db.Set("gorm:query_option", "FOR UPDATE").First(&user, userID)

		if err := query.Error; err != nil {
			return fmt.Errorf("用户不存在: %v", err)
		}

		// 更新用户的使用配额
		newUsedQuota := user.UsedQuota + resources.GetResourceUsage()
		if err := db.Model(&user).Update("used_quota", newUsedQuota).Error; err != nil {
			return fmt.Errorf("更新用户配额失败: %v", err)
		}

		global.APP_LOG.Info(fmt.Sprintf("用户 %d 配额已更新: %d -> %d", userID, user.UsedQuota, newUsedQuota))
		return nil
	}

	// 如果传入了事务，直接使用；否则创建新事务
	if tx != nil {
		return updateOperation(tx)
	}

	// 使用事务执行
	return global.APP_DB.Transaction(updateOperation)
}

// UpdateUserQuotaAfterDeletionWithTx 在指定事务中删除用户配额
func (s *QuotaService) UpdateUserQuotaAfterDeletionWithTx(tx *gorm.DB, userID uint, resources ResourceUsage) error {
	// 直接使用事务，依赖数据库FOR UPDATE锁保证原子性
	updateOperation := func(db *gorm.DB) error {
		var user user.User
		// 使用FOR UPDATE防止并发修改
		query := db.Set("gorm:query_option", "FOR UPDATE").First(&user, userID)

		if err := query.Error; err != nil {
			return fmt.Errorf("用户不存在: %v", err)
		}

		// 释放用户的使用配额
		newUsedQuota := user.UsedQuota - resources.GetResourceUsage()
		if newUsedQuota < 0 {
			newUsedQuota = 0 // 防止负数
		}

		if err := db.Model(&user).Update("used_quota", newUsedQuota).Error; err != nil {
			return fmt.Errorf("更新用户配额失败: %v", err)
		}

		global.APP_LOG.Info(fmt.Sprintf("用户 %d 配额已释放: %d -> %d", userID, user.UsedQuota, newUsedQuota))
		return nil
	}

	// 如果传入了事务，直接使用；否则创建新事务
	if tx != nil {
		return updateOperation(tx)
	}

	// 使用事务执行
	return global.APP_DB.Transaction(updateOperation)
}

// ValidateAdminInstanceCreation 管理员创建实例的配额验证
func (s *QuotaService) ValidateAdminInstanceCreation(req ResourceRequest) (*QuotaCheckResult, error) {
	// 管理员创建实例也需要检查用户的配额限制
	// 这样可以防止管理员无意中创建超过用户限制的实例
	return s.ValidateInstanceCreation(req)
}

// RecalculateUserQuota 重新计算用户配额
// 由于系统会重新初始化数据库，这个功能主要用于运行时的配额同步
func (s *QuotaService) RecalculateUserQuota(userID uint) error {
	// 直接使用事务，依赖数据库FOR UPDATE锁保证原子性
	return global.APP_DB.Transaction(func(tx *gorm.DB) error {
		var user user.User
		if err := tx.Set("gorm:query_option", "FOR UPDATE").First(&user, userID).Error; err != nil {
			return fmt.Errorf("用户不存在: %v", err)
		}

		// 重新计算实际使用的配额
		_, currentResources, err := s.getCurrentResourceUsage(tx, userID)
		if err != nil {
			return fmt.Errorf("获取当前资源使用情况失败: %v", err)
		}

		actualUsedQuota := currentResources.GetResourceUsage()

		if err := tx.Model(&user).Update("used_quota", actualUsedQuota).Error; err != nil {
			return fmt.Errorf("更新用户配额失败: %v", err)
		}

		global.APP_LOG.Info(fmt.Sprintf("用户 %d 配额已重新计算: %d -> %d", userID, user.UsedQuota, actualUsedQuota))
		return nil
	})
}

// GetUserQuotaInfo 获取用户配额信息
func (s *QuotaService) GetUserQuotaInfo(userID uint) (*QuotaCheckResult, error) {
	// 简单的读取操作不需要锁，数据库本身保证读取一致性
	var user user.User
	if err := global.APP_DB.First(&user, userID).Error; err != nil {
		return nil, fmt.Errorf("用户不存在: %v", err)
	}

	// 获取用户等级限制
	levelLimits, exists := global.APP_CONFIG.Quota.LevelLimits[user.Level]
	if !exists {
		return nil, fmt.Errorf("用户等级 %d 没有配置资源限制", user.Level)
	}

	// 获取当前资源使用情况
	currentInstances, currentResources, err := s.getCurrentResourceUsage(global.APP_DB, userID)
	if err != nil {
		return nil, fmt.Errorf("获取当前资源使用情况失败: %v", err)
	}

	maxResources := s.GetLevelMaxResources(levelLimits)

	return &QuotaCheckResult{
		Allowed:          true,
		Reason:           "配额信息查询成功",
		CurrentInstances: currentInstances,
		MaxInstances:     levelLimits.MaxInstances,
		CurrentResources: currentResources,
		MaxResources:     maxResources,
		MaxQuota:         maxResources, // 设置MaxQuota
	}, nil
}

// CheckUserQuota 检查用户配额是否足够
func (s *QuotaService) CheckUserQuota(req interface{}) error {
	// 处理ResourceRequest类型的请求
	resourceReq, ok := req.(ResourceRequest)
	if !ok {
		// 尝试处理指针类型
		if reqPtr, ok := req.(*ResourceRequest); ok {
			resourceReq = *reqPtr
		} else {
			return fmt.Errorf("不支持的请求类型: %T", req)
		}
	}

	// 使用现有的ValidateInstanceCreation方法进行配额检查
	result, err := s.ValidateInstanceCreation(resourceReq)
	if err != nil {
		return fmt.Errorf("配额验证失败: %v", err)
	}

	if !result.Allowed {
		return fmt.Errorf("配额不足: %s", result.Reason)
	}

	return nil
}

// getProviderLevelLimits 获取 Provider 的等级限制配置
func (s *QuotaService) getProviderLevelLimits(tx *gorm.DB, providerID uint, userLevel int) (*config.LevelLimitInfo, error) {
	var prov provider.Provider
	if err := tx.First(&prov, providerID).Error; err != nil {
		return nil, fmt.Errorf("Provider 不存在: %v", err)
	}

	// 如果 Provider 没有配置 LevelLimits，返回 nil
	if prov.LevelLimits == "" {
		return nil, nil
	}

	// 解析 JSON 格式的 LevelLimits
	var providerLimits map[int]config.LevelLimitInfo
	if err := json.Unmarshal([]byte(prov.LevelLimits), &providerLimits); err != nil {
		return nil, fmt.Errorf("解析 Provider 等级限制失败: %v", err)
	}

	// 获取对应用户等级的限制
	if limitInfo, exists := providerLimits[userLevel]; exists {
		return &limitInfo, nil
	}

	// 如果没有配置该等级的限制，返回 nil
	return nil, nil
}

// mergeLevelLimits 合并用户等级限制和 Provider 等级限制，取两者最小值
func (s *QuotaService) mergeLevelLimits(userLimits, providerLimits config.LevelLimitInfo) config.LevelLimitInfo {
	merged := config.LevelLimitInfo{
		MaxInstances: userLimits.MaxInstances,
		MaxResources: make(map[string]interface{}),
		MaxTraffic:   userLimits.MaxTraffic,
	}

	// 取实例数量的最小值
	if providerLimits.MaxInstances > 0 && providerLimits.MaxInstances < userLimits.MaxInstances {
		merged.MaxInstances = providerLimits.MaxInstances
	}

	// 取流量限制的最小值
	if providerLimits.MaxTraffic > 0 && providerLimits.MaxTraffic < userLimits.MaxTraffic {
		merged.MaxTraffic = providerLimits.MaxTraffic
	}

	// 合并资源限制，取每项的最小值
	resourceKeys := []string{"cpu", "memory", "disk", "bandwidth"}
	for _, key := range resourceKeys {
		userVal := s.getResourceValue(userLimits.MaxResources, key)
		providerVal := s.getResourceValue(providerLimits.MaxResources, key)

		// 如果 Provider 没有配置该资源，使用用户限制
		if providerVal == 0 {
			merged.MaxResources[key] = userVal
		} else if userVal == 0 {
			// 如果用户没有配置该资源（理论上不应该发生），使用 Provider 限制
			merged.MaxResources[key] = providerVal
		} else {
			// 取两者最小值
			if providerVal < userVal {
				merged.MaxResources[key] = providerVal
			} else {
				merged.MaxResources[key] = userVal
			}
		}
	}

	return merged
}

// mergeLevelLimitsWithOvercommit 合并用户等级限制和 Provider 等级限制，同时考虑超分配设置
// 如果 Provider 允许某资源超分配，则不应用 Provider 的该资源限制
func (s *QuotaService) mergeLevelLimitsWithOvercommit(userLimits, providerLimits config.LevelLimitInfo, prov *provider.Provider, instanceType string) config.LevelLimitInfo {
	merged := config.LevelLimitInfo{
		MaxInstances: userLimits.MaxInstances,
		MaxResources: make(map[string]interface{}),
		MaxTraffic:   userLimits.MaxTraffic,
	}

	// 取实例数量的最小值
	if providerLimits.MaxInstances > 0 && providerLimits.MaxInstances < userLimits.MaxInstances {
		merged.MaxInstances = providerLimits.MaxInstances
	}

	// 取流量限制的最小值
	if providerLimits.MaxTraffic > 0 && providerLimits.MaxTraffic < userLimits.MaxTraffic {
		merged.MaxTraffic = providerLimits.MaxTraffic
	}

	// 根据实例类型和超分配设置合并资源限制
	resourceKeys := []string{"cpu", "memory", "disk", "bandwidth"}
	for _, key := range resourceKeys {
		userVal := s.getResourceValue(userLimits.MaxResources, key)
		providerVal := s.getResourceValue(providerLimits.MaxResources, key)

		// 检查该资源是否允许超分配
		allowOvercommit := false
		if instanceType == "container" {
			switch key {
			case "cpu":
				allowOvercommit = !prov.ContainerLimitCPU
			case "memory":
				allowOvercommit = !prov.ContainerLimitMemory
			case "disk":
				allowOvercommit = !prov.ContainerLimitDisk
			}
		} else if instanceType == "vm" {
			switch key {
			case "cpu":
				allowOvercommit = !prov.VMLimitCPU
			case "memory":
				allowOvercommit = !prov.VMLimitMemory
			case "disk":
				allowOvercommit = !prov.VMLimitDisk
			}
		}

		// 如果允许超分配，只使用用户限制，忽略 Provider 限制
		if allowOvercommit {
			merged.MaxResources[key] = userVal
			global.APP_LOG.Debug(fmt.Sprintf("资源 %s 允许超分配，使用用户限制: %d", key, userVal))
		} else {
			// 否则取两者最小值
			if providerVal == 0 {
				merged.MaxResources[key] = userVal
			} else if userVal == 0 {
				merged.MaxResources[key] = providerVal
			} else {
				if providerVal < userVal {
					merged.MaxResources[key] = providerVal
				} else {
					merged.MaxResources[key] = userVal
				}
			}
		}
	}

	return merged
}

// getResourceValue 从资源 map 中获取数值
func (s *QuotaService) getResourceValue(resources map[string]interface{}, key string) int64 {
	if resources == nil {
		return 0
	}

	val, exists := resources[key]
	if !exists {
		return 0
	}

	switch v := val.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		return int64(v)
	default:
		return 0
	}
}
