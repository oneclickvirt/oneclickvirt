package user

import (
	"context"
	"errors"
	"fmt"
	auth2 "oneclickvirt/service/auth"
	"oneclickvirt/service/cache"
	"oneclickvirt/service/database"
	"oneclickvirt/service/userquota"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/common"
	providerModel "oneclickvirt/model/provider"
	userModel "oneclickvirt/model/user"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

func highestConfiguredUserLevel() int {
	return userquota.HighestConfiguredLevel()
}

func validateConfiguredUserLevel(level int) error {
	if level < 1 || level > 99 {
		return errors.New("用户等级必须在1-99之间")
	}
	if _, ok := global.GetAppConfig().Quota.LevelLimits[level]; !ok {
		return fmt.Errorf("用户等级 %d 未配置资源限制", level)
	}
	return nil
}

// UpdateUserStatus 更新用户状态
func (s *Service) UpdateUserStatus(userID uint, status int) error {
	var user userModel.User
	if err := global.APP_DB.First(&user, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.NewError(common.CodeUserNotFound, "用户不存在")
		}
		return err
	}

	// 获取管理员信息用于日志记录
	adminUserID := s.getCurrentAdminID() // 从上下文获取当前管理员ID

	if err := global.APP_DB.Model(&user).Update("status", status).Error; err != nil {
		return err
	}

	// 如果禁用用户，撤销其所有Token
	if status == 0 {
		blacklistService := auth2.GetJWTBlacklistService()
		if err := blacklistService.RevokeUserTokens(userID, "disable", adminUserID); err != nil {
			global.APP_LOG.Error("撤销用户Token失败",
				zap.Uint("userID", userID),
				zap.Error(err))
			// 不阻止状态更新，但记录错误
		}
		global.APP_LOG.Info("用户被禁用，已撤销所有Token",
			zap.Uint("userID", userID),
			zap.String("username", user.Username))
	}

	// 清除用户权限和用户资料缓存，确保状态变更立即生效
	permissionService := auth2.PermissionService{}
	permissionService.ClearUserPermissionCache(userID)
	cache.GetUserCacheService().InvalidateUserCache(userID)

	return nil
}

// BatchDeleteUsers 批量删除用户
func (s *Service) BatchDeleteUsers(userIDs []uint) error {
	if len(userIDs) == 0 {
		return errors.New("没有要删除的用户")
	}

	// 检查是否有管理员用户
	var adminCount int64
	global.APP_DB.Model(&userModel.User{}).Where("id IN ? AND user_type = ?", userIDs, "admin").Count(&adminCount)
	if adminCount > 0 {
		return errors.New("不能删除管理员用户")
	}

	// 检查是否有用户拥有活跃实例
	var instanceCount int64
	global.APP_DB.Model(&providerModel.Instance{}).
		Where("user_id IN ? AND status NOT IN ?", userIDs, []string{"deleted", "deleting"}).
		Count(&instanceCount)
	if instanceCount > 0 {
		return fmt.Errorf("部分用户仍拥有 %d 个活跃实例，请先删除实例", instanceCount)
	}

	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		// 使用Unscoped().Delete进行硬删除，彻底从数据库中移除记录
		return tx.Unscoped().Delete(&userModel.User{}, userIDs).Error
	})
}

// BatchUpdateUserStatus 批量更新用户状态
func (s *Service) BatchUpdateUserStatus(userIDs []uint, status int) error {
	if len(userIDs) == 0 {
		return errors.New("没有要更新的用户")
	}

	// 检查是否有管理员用户
	var adminCount int64
	global.APP_DB.Model(&userModel.User{}).Where("id IN ? AND user_type = ?", userIDs, "admin").Count(&adminCount)
	if adminCount > 0 {
		return errors.New("不能修改管理员用户状态")
	}

	if err := global.APP_DB.Model(&userModel.User{}).Where("id IN ?", userIDs).Update("status", status).Error; err != nil {
		return err
	}

	// 如果禁用用户，撤销其所有Token（批量更新，避免逐条查询）
	if status == 0 {
		now := time.Now()
		if err := global.APP_DB.Model(&userModel.User{}).
			Where("id IN ?", userIDs).
			UpdateColumn("tokens_invalidated_at", now).Error; err != nil {
			global.APP_LOG.Error("批量撤销用户Token失败",
				zap.Uints("userIDs", userIDs),
				zap.Error(err))
			// 不阻止状态更新，但记录错误
		} else {
			for _, userID := range userIDs {
				cache.GetUserCacheService().InvalidateUserCache(userID)
			}
			global.APP_LOG.Info("批量禁用用户，已撤销所有Token",
				zap.Uints("userIDs", userIDs))
		}
	}

	// 批量清除用户权限和资料缓存
	permissionService := auth2.PermissionService{}
	cacheService := cache.GetUserCacheService()
	for _, userID := range userIDs {
		permissionService.ClearUserPermissionCache(userID)
		cacheService.InvalidateUserCache(userID)
	}

	return nil
}

// syncUserResourceLimits 同步用户资源限制到对应等级配置
func (s *Service) syncUserResourceLimits(userIDs []uint) error {
	return s.syncUserResourceLimitsWithDB(global.APP_DB, userIDs)
}

// syncUserResourceLimitsWithDB 在指定 DB/事务中同步用户资源限制。
func (s *Service) syncUserResourceLimitsWithDB(db *gorm.DB, userIDs []uint) error {
	if len(userIDs) == 0 {
		return nil
	}

	var users []userModel.User
	if err := db.Select("id, level").Where("id IN ?", userIDs).Find(&users).Error; err != nil {
		global.APP_LOG.Error("查询用户信息失败", zap.Error(err))
		return err
	}

	levelGroups := make(map[int][]uint)
	for _, user := range users {
		levelGroups[user.Level] = append(levelGroups[user.Level], user.ID)
	}

	for level, ids := range levelGroups {
		updateData, err := userquota.BuildLimitUpdateMap(level)
		if err != nil {
			global.APP_LOG.Error("构建用户资源限制失败",
				zap.Int("level", level),
				zap.Uints("userIDs", ids),
				zap.Error(err))
			return err
		}

		for _, batch := range splitUintBatch(ids, 500) {
			if err := db.Model(&userModel.User{}).
				Where("id IN ?", batch).
				Updates(updateData).Error; err != nil {
				global.APP_LOG.Error("同步用户资源限制失败",
					zap.Int("level", level),
					zap.Uints("userIDs", batch),
					zap.Error(err))
				return err
			}
		}

		global.APP_LOG.Debug("同步用户资源限制成功",
			zap.Int("level", level),
			zap.Int("userCount", len(ids)),
			zap.Any("updateData", updateData))
	}

	return nil
}

func splitUintBatch(ids []uint, size int) [][]uint {
	if len(ids) == 0 {
		return nil
	}
	if size <= 0 || len(ids) <= size {
		return [][]uint{ids}
	}
	batches := make([][]uint, 0, (len(ids)+size-1)/size)
	for start := 0; start < len(ids); start += size {
		end := start + size
		if end > len(ids) {
			end = len(ids)
		}
		batches = append(batches, ids[start:end])
	}
	return batches
}

func isAdminUserType(userType string) bool {
	return userType == "admin" || userType == "super_admin" || userType == "normal_admin"
}

// BatchUpdateUserLevel 批量更新用户等级
func (s *Service) BatchUpdateUserLevel(userIDs []uint, level int) error {
	if len(userIDs) == 0 {
		return errors.New("没有要更新的用户")
	}

	if err := validateConfiguredUserLevel(level); err != nil {
		return err
	}

	var specialUsers []userModel.User
	if err := global.APP_DB.Select("id").Where("id IN ? AND user_type IN ?", userIDs, []string{"admin", "super_admin", "normal_admin"}).Find(&specialUsers).Error; err != nil {
		global.APP_LOG.Warn("检查管理员用户失败",
			zap.Error(err),
			zap.Int("userCount", len(userIDs)))
		return err
	}

	specialSet := make(map[uint]struct{}, len(specialUsers))
	specialUserIDs := make([]uint, 0, len(specialUsers))
	for _, user := range specialUsers {
		specialSet[user.ID] = struct{}{}
		specialUserIDs = append(specialUserIDs, user.ID)
	}

	normalUserIDs := make([]uint, 0, len(userIDs))
	for _, id := range userIDs {
		if _, isSpecial := specialSet[id]; !isSpecial {
			normalUserIDs = append(normalUserIDs, id)
		}
	}

	normalUpdates, err := userquota.BuildLevelAndLimitUpdateMap(level)
	if err != nil {
		return err
	}
	specialLevel := highestConfiguredUserLevel()
	specialUpdates, err := userquota.BuildLevelAndLimitUpdateMap(specialLevel)
	if err != nil {
		return err
	}

	dbService := database.GetDatabaseService()
	if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		for _, batch := range splitUintBatch(normalUserIDs, 500) {
			if err := tx.Model(&userModel.User{}).Where("id IN ?", batch).Updates(normalUpdates).Error; err != nil {
				return err
			}
		}
		for _, batch := range splitUintBatch(specialUserIDs, 500) {
			if err := tx.Model(&userModel.User{}).Where("id IN ?", batch).Updates(specialUpdates).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	permissionService := auth2.PermissionService{}
	cacheService := cache.GetUserCacheService()
	for _, userID := range userIDs {
		permissionService.ClearUserPermissionCache(userID)
		cacheService.InvalidateUserCache(userID)
	}

	return nil
}

// UpdateUserLevel 更新单个用户等级
func (s *Service) UpdateUserLevel(userID uint, level int) error {
	if err := validateConfiguredUserLevel(level); err != nil {
		return common.NewError(common.CodeValidationError, err.Error())
	}

	var user userModel.User
	if err := global.APP_DB.Select("id, user_type").First(&user, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.NewError(common.CodeUserNotFound, "用户不存在")
		}
		return err
	}

	if isAdminUserType(user.UserType) {
		level = highestConfiguredUserLevel()
	}

	updates, err := userquota.BuildLevelAndLimitUpdateMap(level)
	if err != nil {
		return common.NewError(common.CodeValidationError, err.Error())
	}

	dbService := database.GetDatabaseService()
	if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Model(&userModel.User{}).Where("id = ?", userID).Updates(updates).Error
	}); err != nil {
		return err
	}

	permissionService := auth2.PermissionService{}
	permissionService.ClearUserPermissionCache(userID)
	cache.GetUserCacheService().InvalidateUserCache(userID)

	return nil
}
