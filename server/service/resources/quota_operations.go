package resources

import (
	"fmt"

	"oneclickvirt/global"
	"oneclickvirt/model/user"
	"oneclickvirt/service/userquota"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AllocatePendingQuota 分配待确认配额（创建实例时调用）
func (s *QuotaService) AllocatePendingQuota(tx *gorm.DB, userID uint, resources ResourceUsage) error {
	var user user.User
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, userID).Error; err != nil {
		return fmt.Errorf("用户不存在: %v", err)
	}

	newPendingQuota := user.PendingQuota + resources.GetResourceUsage()
	if err := tx.Model(&user).Update("pending_quota", newPendingQuota).Error; err != nil {
		return fmt.Errorf("更新待确认配额失败: %v", err)
	}

	global.APP_LOG.Debug(fmt.Sprintf("用户 %d 待确认配额已分配: %d -> %d (+%d)",
		userID, user.PendingQuota, newPendingQuota, resources.GetResourceUsage()))
	return nil
}

// ConfirmPendingQuota 确认待确认配额（实例创建成功时调用）
func (s *QuotaService) ConfirmPendingQuota(tx *gorm.DB, userID uint, resources ResourceUsage) error {
	var user user.User
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, userID).Error; err != nil {
		return fmt.Errorf("用户不存在: %v", err)
	}

	resourceUsage := resources.GetResourceUsage()
	newPendingQuota := user.PendingQuota - resourceUsage
	if newPendingQuota < 0 {
		newPendingQuota = 0
	}
	newUsedQuota := user.UsedQuota + resourceUsage

	updates := map[string]interface{}{
		"pending_quota": newPendingQuota,
		"used_quota":    newUsedQuota,
	}
	if err := tx.Model(&user).Updates(updates).Error; err != nil {
		return fmt.Errorf("确认配额失败: %v", err)
	}

	global.APP_LOG.Debug(fmt.Sprintf("用户 %d 配额已确认: pending %d -> %d, used %d -> %d",
		userID, user.PendingQuota, newPendingQuota, user.UsedQuota, newUsedQuota))
	return nil
}

// ReleasePendingQuota 释放待确认配额（实例创建失败时调用）
func (s *QuotaService) ReleasePendingQuota(tx *gorm.DB, userID uint, resources ResourceUsage) error {
	var user user.User
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, userID).Error; err != nil {
		return fmt.Errorf("用户不存在: %v", err)
	}

	resourceUsage := resources.GetResourceUsage()
	newPendingQuota := user.PendingQuota - resourceUsage
	if newPendingQuota < 0 {
		newPendingQuota = 0
	}

	if err := tx.Model(&user).Update("pending_quota", newPendingQuota).Error; err != nil {
		return fmt.Errorf("释放待确认配额失败: %v", err)
	}

	global.APP_LOG.Debug(fmt.Sprintf("用户 %d 待确认配额已释放: %d -> %d (-%d)",
		userID, user.PendingQuota, newPendingQuota, resourceUsage))
	return nil
}

// ReleaseUsedQuota 释放已使用配额（删除稳定状态实例时调用）
func (s *QuotaService) ReleaseUsedQuota(tx *gorm.DB, userID uint, resources ResourceUsage) error {
	var user user.User
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, userID).Error; err != nil {
		return fmt.Errorf("用户不存在: %v", err)
	}

	resourceUsage := resources.GetResourceUsage()
	newUsedQuota := user.UsedQuota - resourceUsage
	if newUsedQuota < 0 {
		newUsedQuota = 0
	}

	if err := tx.Model(&user).Update("used_quota", newUsedQuota).Error; err != nil {
		return fmt.Errorf("释放已使用配额失败: %v", err)
	}

	global.APP_LOG.Debug(fmt.Sprintf("用户 %d 已使用配额已释放: %d -> %d (-%d)",
		userID, user.UsedQuota, newUsedQuota, resourceUsage))
	return nil
}

// UpdateUserQuotaAfterCreationWithTx 在指定事务中更新用户配额（向后兼容，已废弃，使用 AllocatePendingQuota）
func (s *QuotaService) UpdateUserQuotaAfterCreationWithTx(tx *gorm.DB, userID uint, resources ResourceUsage) error {
	// 为了向后兼容，这里调用新的 AllocatePendingQuota 方法
	return s.AllocatePendingQuota(tx, userID, resources)
}

// UpdateUserQuotaAfterDeletionWithTx 在指定事务中删除用户配额（向后兼容，根据实例状态决定释放哪种配额）
func (s *QuotaService) UpdateUserQuotaAfterDeletionWithTx(tx *gorm.DB, userID uint, resources ResourceUsage) error {
	// 这个方法需要根据实例状态来决定释放 used_quota 还是 pending_quota
	// 但由于调用方已经删除了实例，无法再查询状态
	// 因此这个兼容方法默认释放 used_quota
	return s.ReleaseUsedQuota(tx, userID, resources)
}

// ValidateAdminInstanceCreation 管理员创建实例的配额验证
func (s *QuotaService) ValidateAdminInstanceCreation(req ResourceRequest) (*QuotaCheckResult, error) {
	// 管理员创建实例也需要检查用户的配额限制
	// 这样可以防止管理员无意中创建超过用户限制的实例
	return s.ValidateInstanceCreation(req)
}

// RecalculateUserQuota 重新计算用户配额（两阶段配额系统）
func (s *QuotaService) RecalculateUserQuota(userID uint) error {
	return global.APP_DB.Transaction(func(tx *gorm.DB) error {
		return s.RecalculateUserQuotaInTx(tx, userID)
	})
}

// RecalculateUserQuotaInTx 在已有事务中重新计算用户配额。
func (s *QuotaService) RecalculateUserQuotaInTx(tx *gorm.DB, userID uint) error {
	var user user.User
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, userID).Error; err != nil {
		return fmt.Errorf("用户不存在: %v", err)
	}

	_, stableResources, pendingResources, err := s.getCurrentResourceUsageWithPending(tx, userID)
	if err != nil {
		return fmt.Errorf("获取当前资源使用情况失败: %v", err)
	}

	actualUsedQuota := stableResources.GetResourceUsage()
	actualPendingQuota := pendingResources.GetResourceUsage()
	updates := make(map[string]interface{})

	if user.UsedQuota != actualUsedQuota {
		updates["used_quota"] = actualUsedQuota
	}
	if user.PendingQuota != actualPendingQuota {
		updates["pending_quota"] = actualPendingQuota
	}

	if len(updates) == 0 {
		return nil
	}
	if err := tx.Model(&user).Updates(updates).Error; err != nil {
		return fmt.Errorf("更新用户配额失败: %v", err)
	}

	global.APP_LOG.Debug(fmt.Sprintf("用户 %d 配额已重新计算: used %d -> %d, pending %d -> %d",
		userID, user.UsedQuota, actualUsedQuota, user.PendingQuota, actualPendingQuota))
	return nil
}
// GetUserQuotaInfo 获取用户配额信息
func (s *QuotaService) GetUserQuotaInfo(userID uint) (*QuotaCheckResult, error) {
	// 简单的读取操作不需要锁，数据库本身保证读取一致性
	var user user.User
	if err := global.APP_DB.First(&user, userID).Error; err != nil {
		return nil, fmt.Errorf("用户不存在: %v", err)
	}

	// 获取用户等级限制
	levelLimits, err := userquota.ResolveLevelLimit(user.Level)
	if err != nil {
		return nil, err
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
