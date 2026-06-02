package user

import (
	"context"
	"errors"
	"fmt"
	auth2 "oneclickvirt/service/auth"
	"oneclickvirt/service/cache"
	"oneclickvirt/service/database"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/common"
	providerModel "oneclickvirt/model/provider"
	userModel "oneclickvirt/model/user"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

func highestConfiguredUserLevel() int {
	maxLevel := 1
	for level := range global.GetAppConfig().Quota.LevelLimits {
		if level > maxLevel {
			maxLevel = level
		}
	}
	return maxLevel
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

	// 清除用户权限缓存，确保状态变更立即生效
	permissionService := auth2.PermissionService{}
	permissionService.ClearUserPermissionCache(userID)

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

	// 批量清除用户权限缓存
	permissionService := auth2.PermissionService{}
	for _, userID := range userIDs {
		permissionService.ClearUserPermissionCache(userID)
	}

	return nil
}

// syncUserResourceLimits 同步用户资源限制到对应等级配置
func (s *Service) syncUserResourceLimits(userIDs []uint) error {
	if len(userIDs) == 0 {
		return nil
	}

	// 按等级分组查询用户
	// 批量查询用户level信息
	var users []userModel.User
	if err := global.APP_DB.Select("id, level").
		Where("id IN ?", userIDs).
		Limit(1000).
		Find(&users).Error; err != nil {
		global.APP_LOG.Error("查询用户信息失败", zap.Error(err))
		return err
	}

	// 按等级分组
	levelGroups := make(map[int][]uint)
	for _, user := range users {
		levelGroups[user.Level] = append(levelGroups[user.Level], user.ID)
	}

	// 为每个等级的用户更新资源限制
	for level, userIDList := range levelGroups {
		if levelConfig, exists := global.GetAppConfig().Quota.LevelLimits[level]; exists {
			// 构建完整的资源限制更新数据
			updateData := map[string]interface{}{
				"total_traffic": levelConfig.MaxTraffic,
				"max_instances": levelConfig.MaxInstances,
			}

			// 从 MaxResources 中提取各项资源限制
			if levelConfig.MaxResources != nil {
				if cpu, ok := levelConfig.MaxResources["cpu"].(int); ok {
					updateData["max_cpu"] = cpu
				} else if cpu, ok := levelConfig.MaxResources["cpu"].(float64); ok {
					updateData["max_cpu"] = int(cpu)
				}

				if memory, ok := levelConfig.MaxResources["memory"].(int); ok {
					updateData["max_memory"] = memory
				} else if memory, ok := levelConfig.MaxResources["memory"].(float64); ok {
					updateData["max_memory"] = int(memory)
				}

				if disk, ok := levelConfig.MaxResources["disk"].(int); ok {
					updateData["max_disk"] = disk
				} else if disk, ok := levelConfig.MaxResources["disk"].(float64); ok {
					updateData["max_disk"] = int(disk)
				}

				if bandwidth, ok := levelConfig.MaxResources["bandwidth"].(int); ok {
					updateData["max_bandwidth"] = bandwidth
				} else if bandwidth, ok := levelConfig.MaxResources["bandwidth"].(float64); ok {
					updateData["max_bandwidth"] = int(bandwidth)
				}
			}

			if err := global.APP_DB.Table("users").
				Where("id IN ?", userIDList).
				Updates(updateData).Error; err != nil {
				global.APP_LOG.Error("同步用户资源限制失败",
					zap.Int("level", level),
					zap.Uints("userIDs", userIDList),
					zap.Error(err))
				return err
			}

			global.APP_LOG.Debug("同步用户资源限制成功",
				zap.Int("level", level),
				zap.Int("userCount", len(userIDList)),
				zap.Int64("newTrafficLimit", levelConfig.MaxTraffic),
				zap.Int("maxInstances", levelConfig.MaxInstances),
				zap.Any("updateData", updateData))
		} else {
			global.APP_LOG.Warn("等级配置不存在，跳过资源限制同步",
				zap.Int("level", level),
				zap.Uints("userIDs", userIDList))
		}
	}

	return nil
}

// BatchUpdateUserLevel 批量更新用户等级
func (s *Service) BatchUpdateUserLevel(userIDs []uint, level int) error {
	if len(userIDs) == 0 {
		return errors.New("没有要更新的用户")
	}

	if err := validateConfiguredUserLevel(level); err != nil {
		return err
	}

	// 检查是否有管理员用户，管理员用户应该始终是最高等级
	var specialUsers []userModel.User
	if err := global.APP_DB.Where("id IN ? AND user_type IN ?", userIDs, []string{"admin"}).Find(&specialUsers).Error; err != nil {
		global.APP_LOG.Warn("检查管理员用户失败",
			zap.Error(err),
			zap.Int("userCount", len(userIDs)))
		// 出错时继续，假设没有特殊用户
		specialUsers = []userModel.User{}
	}

	// 为特殊用户设置最高等级
	if len(specialUsers) > 0 {
		specialUserIDs := make([]uint, len(specialUsers))
		for i, user := range specialUsers {
			specialUserIDs[i] = user.ID
		}
		if err := global.APP_DB.Model(&userModel.User{}).Where("id IN ?", specialUserIDs).Update("level", highestConfiguredUserLevel()).Error; err != nil {
			global.APP_LOG.Error("更新管理员用户等级失败",
				zap.Uints("specialUserIDs", specialUserIDs),
				zap.Error(err))
			return err
		}

		// 从原列表中移除特殊用户
		normalUserIDs := make([]uint, 0)
		for _, id := range userIDs {
			isSpecial := false
			for _, specialID := range specialUserIDs {
				if id == specialID {
					isSpecial = true
					break
				}
			}
			if !isSpecial {
				normalUserIDs = append(normalUserIDs, id)
			}
		}
		userIDs = normalUserIDs
	}

	// 更新普通用户等级
	if len(userIDs) > 0 {
		if err := global.APP_DB.Model(&userModel.User{}).Where("id IN ?", userIDs).Update("level", level).Error; err != nil {
			return err
		}
	}

	// 清除所有相关用户的权限缓存
	permissionService := auth2.PermissionService{}
	allUserIDs := append(userIDs, func() []uint {
		var specialIDs []uint
		for _, user := range specialUsers {
			specialIDs = append(specialIDs, user.ID)
		}
		return specialIDs
	}()...)

	for _, userID := range allUserIDs {
		permissionService.ClearUserPermissionCache(userID)
	}

	// 同步所有更新用户的资源限制
	if err := s.syncUserResourceLimits(allUserIDs); err != nil {
		global.APP_LOG.Warn("同步用户资源限制失败", zap.Error(err))
		// 不返回错误，因为等级更新已经成功，资源限制同步失败只记录日志
	}

	return nil
}

// UpdateUserLevel 更新单个用户等级
func (s *Service) UpdateUserLevel(userID uint, level int) error {
	if err := validateConfiguredUserLevel(level); err != nil {
		return common.NewError(common.CodeValidationError, err.Error())
	}

	// 获取用户信息
	var user userModel.User
	if err := global.APP_DB.First(&user, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.NewError(common.CodeUserNotFound, "用户不存在")
		}
		return err
	}

	// 管理员应该始终是最高等级
	if user.UserType == "admin" {
		level = highestConfiguredUserLevel()
	}

	if err := global.APP_DB.Model(&user).Update("level", level).Error; err != nil {
		return err
	}

	// 清除用户权限缓存
	permissionService := auth2.PermissionService{}
	permissionService.ClearUserPermissionCache(userID)

	// 同步用户资源限制
	if err := s.syncUserResourceLimits([]uint{userID}); err != nil {
		global.APP_LOG.Warn("同步用户资源限制失败",
			zap.Uint("userID", userID),
			zap.Int("level", level),
			zap.Error(err))
		// 不返回错误，因为等级更新已经成功，资源限制同步失败只记录日志
	}

	return nil
}
