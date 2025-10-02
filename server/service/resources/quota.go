package resources

import (
	"errors"
	"fmt"
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
	Bandwidth    int // 添加带宽字段
	InstanceType string
}

// QuotaCheckResult 配额检查结果
type QuotaCheckResult struct {
	Allowed           bool
	Reason            string
	CurrentInstances  int
	MaxInstances      int
	CurrentResources  ResourceUsage
	MaxResources      ResourceUsage
	MaxQuota          ResourceUsage // 添加MaxQuota字段
	RequiredResources ResourceUsage
}

// ResourceUsage 资源使用情况
type ResourceUsage struct {
	CPU       int
	Memory    int64
	Disk      int64
	Bandwidth int // 添加带宽字段
}

// GetResourceUsage 计算资源使用量（标准化计算方式）
func (r ResourceUsage) GetResourceUsage() int {
	// 统一的资源计算方式：CPU权重4，内存权重2，磁盘权重1
	// 这样可以更合理地反映资源价值
	return r.CPU*4 + int(r.Memory/512)*2 + int(r.Disk/5)*1
}

// ValidateInstanceCreation 验证实例创建请求
func (s *QuotaService) ValidateInstanceCreation(req ResourceRequest) (*QuotaCheckResult, error) {
	// 在事务中进行完整的配额检查，依赖数据库事务保证原子性
	var result *QuotaCheckResult
	var err error

	err = global.APP_DB.Transaction(func(tx *gorm.DB) error {
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

// validateInTransaction 在事务中进行配额验证
func (s *QuotaService) validateInTransaction(tx *gorm.DB, req ResourceRequest) (*QuotaCheckResult, error) {
	// 获取用户信息（使用行锁防止并发问题）
	var user user.User
	if err := tx.Set("gorm:query_option", "FOR UPDATE").First(&user, req.UserID).Error; err != nil {
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

	// 统计当前实例数量和资源使用
	currentInstances, currentResources, err := s.getCurrentResourceUsage(tx, req.UserID)
	if err != nil {
		return nil, fmt.Errorf("获取当前资源使用情况失败: %v", err)
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

	// 1. 检查实例数量限制
	if currentInstances >= levelLimits.MaxInstances {
		result.Allowed = false
		result.Reason = fmt.Sprintf("实例数量已达上限：当前 %d/%d", currentInstances, levelLimits.MaxInstances)
		return result, nil
	}

	// 2. 检查CPU限制
	if currentResources.CPU+requestedResources.CPU > maxResources.CPU {
		result.Allowed = false
		result.Reason = fmt.Sprintf("CPU资源不足：需要 %d，当前使用 %d，最大允许 %d",
			requestedResources.CPU, currentResources.CPU, maxResources.CPU)
		return result, nil
	}

	// 3. 检查内存限制
	if currentResources.Memory+requestedResources.Memory > maxResources.Memory {
		result.Allowed = false
		result.Reason = fmt.Sprintf("内存资源不足：需要 %dMB，当前使用 %dMB，最大允许 %dMB",
			requestedResources.Memory, currentResources.Memory, maxResources.Memory)
		return result, nil
	}

	// 4. 检查磁盘限制
	if currentResources.Disk+requestedResources.Disk > maxResources.Disk {
		result.Allowed = false
		result.Reason = fmt.Sprintf("磁盘资源不足：需要 %dMB，当前使用 %dMB，最大允许 %dMB",
			requestedResources.Disk, currentResources.Disk, maxResources.Disk)
		return result, nil
	}

	// 5. 检查带宽限制
	levelBandwidthLimit := s.getLevelBandwidthLimit(user.Level)
	if requestedResources.Bandwidth > levelBandwidthLimit {
		result.Allowed = false
		result.Reason = fmt.Sprintf("带宽超出等级限制：需要 %dMbps，等级 %d 最大允许 %dMbps",
			requestedResources.Bandwidth, user.Level, levelBandwidthLimit)
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

// getCurrentResourceUsage 获取当前资源使用情况
func (s *QuotaService) getCurrentResourceUsage(tx *gorm.DB, userID uint) (int, ResourceUsage, error) {
	var instances []provider.Instance

	// 查询非删除状态的实例
	err := tx.Where("user_id = ? AND status != ? AND status != ?", userID, "deleting", "deleted").Find(&instances).Error
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

// RecalculateUserQuota 重新计算用户配额（用于数据修复）
// 注意：由于系统会重新初始化数据库，这个功能主要用于运行时的配额同步
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
