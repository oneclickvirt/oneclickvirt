package user

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	auth2 "oneclickvirt/service/auth"
	"oneclickvirt/service/database"

	"oneclickvirt/config"
	"oneclickvirt/global"
	"oneclickvirt/model/admin"
	"oneclickvirt/model/auth"
	"oneclickvirt/model/common"
	providerModel "oneclickvirt/model/provider"
	userModel "oneclickvirt/model/user"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// Service 管理员用户管理服务
type Service struct{}

// NewService 创建用户管理服务
func NewService() *Service {
	return &Service{}
}

// GetUserList 获取用户列表
func (s *Service) GetUserList(req admin.UserListRequest) ([]admin.UserManageResponse, int64, error) {
	var users []userModel.User
	var total int64

	query := global.APP_DB.Model(&userModel.User{})

	if req.Username != "" {
		query = query.Where("username LIKE ?", "%"+req.Username+"%")
	}
	if req.UserType != "" {
		query = query.Where("user_type = ?", req.UserType)
	}
	// 状态筛选逻辑 - 只有明确指定了状态时才筛选
	if req.Status != nil {
		query = query.Where("status = ?", *req.Status)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (req.Page - 1) * req.PageSize
	if err := query.Offset(offset).Limit(req.PageSize).Find(&users).Error; err != nil {
		return nil, 0, err
	}

	// 批量统计实例数量
	var userIDs []uint
	for _, user := range users {
		userIDs = append(userIDs, user.ID)
	}

	// 使用GROUP BY一次性统计所有用户的实例数量
	type InstanceCountResult struct {
		UserID        uint
		InstanceCount int64
	}
	var countResults []InstanceCountResult
	if len(userIDs) > 0 {
		global.APP_DB.Model(&providerModel.Instance{}).
			Select("user_id, COUNT(*) as instance_count").
			Where("user_id IN ?", userIDs).
			Group("user_id").
			Scan(&countResults)
	}

	// 将统计结果按user_id映射
	instanceCountMap := make(map[uint]int64)
	for _, result := range countResults {
		instanceCountMap[result.UserID] = result.InstanceCount
	}

	var userResponses []admin.UserManageResponse
	for _, user := range users {
		// 从预统计的map中获取实例数量
		instanceCount := instanceCountMap[user.ID]

		userResponse := admin.UserManageResponse{
			User:          user,
			InstanceCount: int(instanceCount),
			LastLoginAt:   user.UpdatedAt,
		}
		userResponses = append(userResponses, userResponse)
	}

	return userResponses, total, nil
}

// CreateUser 创建用户
func (s *Service) CreateUser(req admin.CreateUserRequest) error {
	global.APP_LOG.Debug("开始创建用户", zap.String("username", utils.TruncateString(req.Username, 32)))

	var existingUser userModel.User
	if err := global.APP_DB.Where("username = ?", req.Username).First(&existingUser).Error; err == nil {
		global.APP_LOG.Warn("用户创建失败：用户名已存在", zap.String("username", utils.TruncateString(req.Username, 32)))
		return errors.New("用户名已存在")
	}

	// 管理员创建用户时不进行密码强度验证，允许管理员设置任意密码
	// 只进行基本的长度检查
	if len(req.Password) < 1 {
		global.APP_LOG.Warn("用户创建失败：密码不能为空",
			zap.String("username", utils.TruncateString(req.Username, 32)))
		return errors.New("密码不能为空")
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		global.APP_LOG.Error("密码哈希生成失败",
			zap.String("username", utils.TruncateString(req.Username, 32)),
			zap.Error(err))
		return err
	}

	user := userModel.User{
		Username:   req.Username,
		Password:   string(hashedPassword),
		Nickname:   req.Nickname,
		Email:      req.Email,
		Phone:      req.Phone,
		Telegram:   req.Telegram,
		QQ:         req.QQ,
		UserType:   req.UserType,
		Level:      req.Level,
		TotalQuota: req.TotalQuota,
		Status:     req.Status,
	}

	// 使用数据库抽象层创建
	dbService := database.GetDatabaseService()
	if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Create(&user).Error
	}); err != nil {
		global.APP_LOG.Error("用户创建失败",
			zap.String("username", utils.TruncateString(req.Username, 32)),
			zap.Error(err))
		return err
	}

	// 异步同步用户资源限制到对应等级配置
	go func() {
		if syncErr := s.syncSingleUserResourceLimits(user.Level, user.ID); syncErr != nil {
			global.APP_LOG.Error("新创建用户的资源限制同步失败",
				zap.Uint("userID", user.ID),
				zap.Int("level", user.Level),
				zap.Error(syncErr))
		} else {
			global.APP_LOG.Info("新创建用户的资源限制同步成功",
				zap.Uint("userID", user.ID),
				zap.Int("level", user.Level))
		}
	}()

	global.APP_LOG.Info("用户创建成功",
		zap.String("username", utils.TruncateString(req.Username, 32)),
		zap.String("userType", req.UserType),
		zap.Int("level", req.Level))
	return nil
}

// UpdateUser 更新用户
func (s *Service) UpdateUser(req admin.UpdateUserRequest, currentUserID uint) error {
	global.APP_LOG.Debug("开始更新用户", zap.Uint("userID", req.ID), zap.Uint("currentUserID", currentUserID))

	var user userModel.User
	if err := global.APP_DB.First(&user, req.ID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			global.APP_LOG.Warn("用户更新失败：用户不存在", zap.Uint("userID", req.ID))
			return common.NewError(common.CodeUserNotFound)
		}
		global.APP_LOG.Error("查询用户失败", zap.Uint("userID", req.ID), zap.Error(err))
		return err
	}

	// 防止管理员修改自己的用户类型
	if req.ID == currentUserID && req.UserType != "" && req.UserType != user.UserType {
		global.APP_LOG.Warn("用户更新失败：不能修改当前登录用户的用户类型",
			zap.Uint("userID", req.ID),
			zap.String("currentType", user.UserType),
			zap.String("requestType", req.UserType))
		return common.NewError(common.CodeForbidden, "不能修改当前登录用户的用户类型")
	}

	// 检查用户名是否被其他用户使用
	if req.Username != "" && req.Username != user.Username {
		var count int64
		global.APP_DB.Model(&userModel.User{}).Where("username = ? AND id != ?", req.Username, req.ID).Count(&count)
		if count > 0 {
			global.APP_LOG.Warn("用户更新失败：用户名已存在",
				zap.Uint("userID", req.ID),
				zap.String("username", utils.TruncateString(req.Username, 32)))
			return common.NewError(common.CodeUserExists, "用户名已存在")
		}
		user.Username = req.Username
	}

	// 检查邮箱是否被其他用户使用
	if req.Email != "" && req.Email != user.Email {
		var count int64
		global.APP_DB.Model(&userModel.User{}).Where("email = ? AND id != ?", req.Email, req.ID).Count(&count)
		if count > 0 {
			global.APP_LOG.Warn("用户更新失败：邮箱已存在",
				zap.Uint("userID", req.ID),
				zap.String("email", utils.TruncateString(req.Email, 32)))
			return common.NewError(common.CodeUserExists, "邮箱已存在")
		}
		user.Email = req.Email
	}

	// 更新基本信息
	if req.Nickname != "" {
		user.Nickname = req.Nickname
	}
	if req.Phone != "" {
		user.Phone = req.Phone
	}
	if req.Telegram != "" {
		user.Telegram = req.Telegram
	}
	if req.QQ != "" {
		user.QQ = req.QQ
	}
	if req.Level > 0 {
		user.Level = req.Level
	}
	if req.TotalQuota >= 0 {
		user.TotalQuota = req.TotalQuota
	}
	if req.Status >= 0 {
		user.Status = req.Status
	}

	// 处理角色相关的用户类型更新
	if req.RoleID > 0 {
		var role auth.Role
		if err := global.APP_DB.First(&role, req.RoleID).Error; err != nil {
			global.APP_LOG.Warn("用户更新失败：角色不存在",
				zap.Uint("userID", req.ID),
				zap.Uint("roleID", req.RoleID))
			return common.NewError(common.CodeRoleNotFound, "角色不存在")
		}

		// 只有在不是修改自己的情况下才允许修改用户类型
		if req.ID != currentUserID {
			user.UserType = role.Code
			// 角色关联将在事务内更新
		}
	} else if req.UserType != "" && req.ID != currentUserID {
		// 直接指定的用户类型（仅在不是修改自己时允许）
		user.UserType = req.UserType
	}

	// 保存更新（在事务内完成所有操作）
	dbService := database.GetDatabaseService()
	if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		// 如果需要更新角色关联，在事务内进行
		if req.RoleID > 0 && req.ID != currentUserID {
			var role auth.Role
			if err := tx.First(&role, req.RoleID).Error; err != nil {
				return err
			}
			// 清除旧的角色关联
			if err := tx.Model(&user).Association("Roles").Clear(); err != nil {
				return err
			}
			// 添加新的角色关联
			if err := tx.Model(&user).Association("Roles").Append(&role); err != nil {
				return err
			}
		}
		// 保存用户信息
		return tx.Save(&user).Error
	}); err != nil {
		global.APP_LOG.Error("用户更新失败", zap.Uint("userID", req.ID), zap.Error(err))
		return err
	} // 清除用户权限缓存，确保权限变更立即生效
	permissionService := auth2.PermissionService{}
	permissionService.ClearUserPermissionCache(user.ID)

	global.APP_LOG.Info("用户更新成功",
		zap.Uint("userID", req.ID),
		zap.String("username", utils.TruncateString(user.Username, 32)),
		zap.String("userType", user.UserType))
	return nil
}

// DeleteUser 删除用户
func (s *Service) DeleteUser(userID uint) error {
	global.APP_LOG.Debug("开始删除用户", zap.Uint("userID", userID))

	var instanceCount int64
	global.APP_DB.Model(&providerModel.Instance{}).Where("user_id = ?", userID).Count(&instanceCount)
	if instanceCount > 0 {
		global.APP_LOG.Warn("用户删除失败：用户还有实例",
			zap.Uint("userID", userID),
			zap.Int64("instanceCount", instanceCount))
		return errors.New("用户还有实例，无法删除")
	}

	// 使用数据库抽象层进行硬删除（永久删除）
	dbService := database.GetDatabaseService()
	if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		// 使用Unscoped().Delete进行硬删除，彻底从数据库中移除记录
		return tx.Unscoped().Delete(&userModel.User{}, userID).Error
	}); err != nil {
		global.APP_LOG.Error("用户删除失败", zap.Uint("userID", userID), zap.Error(err))
		return err
	}

	global.APP_LOG.Info("用户删除成功", zap.Uint("userID", userID))
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

	// 如果禁用用户，撤销其所有Token
	if status == 0 {
		blacklistService := auth2.GetJWTBlacklistService()
		adminUserID := s.getCurrentAdminID() // 从上下文获取当前管理员ID

		for _, userID := range userIDs {
			if err := blacklistService.RevokeUserTokens(userID, "disable", adminUserID); err != nil {
				global.APP_LOG.Error("撤销用户Token失败",
					zap.Uint("userID", userID),
					zap.Error(err))
				// 不阻止状态更新，但记录错误
			}
		}
		global.APP_LOG.Info("批量禁用用户，已撤销所有Token",
			zap.Uints("userIDs", userIDs))
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
		if levelConfig, exists := global.APP_CONFIG.Quota.LevelLimits[level]; exists {
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

			global.APP_LOG.Info("同步用户资源限制成功",
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

	// 验证等级范围
	if level < 1 || level > 5 {
		return errors.New("用户等级必须在1-5之间")
	}

	// 检查是否有管理员用户，管理员用户应该始终是最高等级
	var specialUsers []userModel.User
	global.APP_DB.Where("id IN ? AND user_type IN ?", userIDs, []string{"admin"}).Find(&specialUsers)

	// 为特殊用户设置最高等级
	if len(specialUsers) > 0 {
		specialUserIDs := make([]uint, len(specialUsers))
		for i, user := range specialUsers {
			specialUserIDs[i] = user.ID
		}
		global.APP_DB.Model(&userModel.User{}).Where("id IN ?", specialUserIDs).Update("level", 5)

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
		global.APP_LOG.Error("同步用户资源限制失败", zap.Error(err))
		// 不返回错误，因为等级更新已经成功，资源限制同步失败只记录日志
	}

	return nil
}

// UpdateUserLevel 更新单个用户等级
func (s *Service) UpdateUserLevel(userID uint, level int) error {
	// 验证等级范围
	if level < 1 || level > 5 {
		return common.NewError(common.CodeValidationError, "用户等级必须在1-5之间")
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
		level = 5
	}

	if err := global.APP_DB.Model(&user).Update("level", level).Error; err != nil {
		return err
	}

	// 清除用户权限缓存
	permissionService := auth2.PermissionService{}
	permissionService.ClearUserPermissionCache(userID)

	// 同步用户资源限制
	if err := s.syncUserResourceLimits([]uint{userID}); err != nil {
		global.APP_LOG.Error("同步用户资源限制失败",
			zap.Uint("userID", userID),
			zap.Int("level", level),
			zap.Error(err))
		// 不返回错误，因为等级更新已经成功，资源限制同步失败只记录日志
	}

	return nil
}

// ResetUserPassword 管理员强制重置用户密码
func (s *Service) ResetUserPassword(userID uint) (string, error) {
	// 获取用户信息
	var user userModel.User
	if err := global.APP_DB.First(&user, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", common.NewError(common.CodeUserNotFound, "用户不存在")
		}
		return "", err
	}

	// 生成强密码（12位）
	newPassword := utils.GenerateStrongPassword(12)

	// 管理员重置密码使用放宽的策略（不要求特殊字符，因为生成的密码仅包含字母和数字）
	adminResetPolicy := utils.PasswordStrengthConfig{
		MinLength:        8,     // 最小8位
		RequireUpperCase: true,  // 要求大写字母
		RequireLowerCase: true,  // 要求小写字母
		RequireDigit:     true,  // 要求数字
		RequireSpecial:   false, // 不要求特殊字符（管理员生成的密码）
		ForbidCommon:     true,  // 禁止常见弱密码
		ForbidPersonal:   true,  // 禁止包含个人信息
	}

	// 密码强度验证（确保生成的密码符合策略）
	if err := utils.ValidatePasswordStrength(newPassword, adminResetPolicy, user.Username); err != nil {
		return "", common.NewError(common.CodeValidationError, err.Error())
	}

	// 加密新密码
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}

	// 更新密码
	if err := global.APP_DB.Model(&user).Update("password", string(hashedPassword)).Error; err != nil {
		return "", err
	}

	// 记录操作日志
	global.APP_LOG.Info("管理员重置用户密码",
		zap.Uint("target_user_id", userID),
		zap.String("target_username", user.Username),
	)

	return newPassword, nil
}

// ResetUserPasswordAndNotify 管理员重置用户密码并发送到用户通信渠道
func (s *Service) ResetUserPasswordAndNotify(userID uint) error {
	// 获取用户信息
	var user userModel.User
	if err := global.APP_DB.First(&user, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.NewError(common.CodeUserNotFound, "用户不存在")
		}
		return err
	}

	// 生成强密码（12位）
	newPassword := utils.GenerateStrongPassword(12)

	// 密码强度验证（确保生成的密码符合策略）
	if err := utils.ValidatePasswordStrength(newPassword, utils.DefaultPasswordPolicy, user.Username); err != nil {
		return common.NewError(common.CodeValidationError, err.Error())
	}

	// 加密新密码
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	// 更新密码
	if err := global.APP_DB.Model(&user).Update("password", string(hashedPassword)).Error; err != nil {
		return err
	}

	// 发送新密码到用户绑定的通信渠道
	if err := s.sendPasswordToUser(&user, newPassword); err != nil {
		// 记录日志但不阻止密码重置完成
		global.APP_LOG.Error("发送新密码失败",
			zap.Uint("user_id", userID),
			zap.String("username", user.Username),
			zap.Error(err))
		return errors.New("密码重置成功，但发送新密码到通信渠道失败，请联系管理员")
	}

	// 记录操作日志
	global.APP_LOG.Info("管理员重置用户密码并发送到通信渠道",
		zap.Uint("target_user_id", userID),
		zap.String("target_username", user.Username),
	)

	return nil
}

// sendPasswordToUser 发送新密码到用户绑定的通信渠道
func (s *Service) sendPasswordToUser(user *userModel.User, newPassword string) error {
	// 优先级：邮箱 > Telegram > QQ > 手机号

	if user.Email != "" {
		return s.sendPasswordByEmail(user.Email, user.Username, newPassword)
	}

	if user.Telegram != "" {
		return s.sendPasswordByTelegram(user.Telegram, user.Username, newPassword)
	}

	if user.QQ != "" {
		return s.sendPasswordByQQ(user.QQ, user.Username, newPassword)
	}

	if user.Phone != "" {
		return s.sendPasswordBySMS(user.Phone, user.Username, newPassword)
	}

	return errors.New("用户未绑定任何通信渠道")
}

// sendPasswordByEmail 通过邮箱发送新密码
func (s *Service) sendPasswordByEmail(email, username, newPassword string) error {
	// 检查邮箱配置是否可用
	if !s.isEmailConfigured() {
		global.APP_LOG.Warn("邮箱服务未配置，跳过发送",
			zap.String("email", email),
			zap.String("username", username))
		return nil
	}

	// 构建邮件内容
	subject := "密码重置通知"
	body := fmt.Sprintf(`
尊敬的用户 %s：

您的密码已由管理员重置，新密码为：%s

请使用新密码登录系统，并建议您尽快修改密码。

系统自动发送，请勿回复。
`, username, newPassword)

	// 发送邮件
	err := s.sendEmail(email, subject, body)
	if err != nil {
		global.APP_LOG.Error("发送密码重置邮件失败",
			zap.String("email", email),
			zap.String("username", username),
			zap.Error(err))
		return fmt.Errorf("邮件发送失败: %w", err)
	}

	global.APP_LOG.Info("管理员操作：成功发送新密码到邮箱",
		zap.String("email", email),
		zap.String("username", username))
	return nil
}

// sendPasswordByTelegram 通过Telegram发送新密码
func (s *Service) sendPasswordByTelegram(telegram, username, newPassword string) error {
	// TODO: 实现Telegram发送功能
	global.APP_LOG.Info("管理员操作：模拟发送新密码到Telegram",
		zap.String("telegram", telegram),
		zap.String("username", username))
	return nil
}

// sendPasswordByQQ 通过QQ发送新密码
func (s *Service) sendPasswordByQQ(qq, username, newPassword string) error {
	// TODO: 实现QQ发送功能
	global.APP_LOG.Info("管理员操作：模拟发送新密码到QQ",
		zap.String("qq", qq),
		zap.String("username", username))
	return nil
}

// sendPasswordBySMS 通过短信发送新密码
func (s *Service) sendPasswordBySMS(phone, username, newPassword string) error {
	// TODO: 实现短信发送功能
	global.APP_LOG.Info("管理员操作：模拟发送新密码到手机",
		zap.String("phone", phone),
		zap.String("username", username))
	return nil
}

// generateRandomPassword 生成随机密码（仅包含数字和大小写英文字母，长度不低于8位）
func (s *Service) generateRandomPassword(length int) string {
	if length < 8 {
		length = 8
	}
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	password := make([]byte, length)
	for i := range password {
		num, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		password[i] = charset[num.Int64()]
	}
	return string(password)
}

// syncSingleUserResourceLimits 同步单个用户的资源限制
func (s *Service) syncSingleUserResourceLimits(level int, userID uint) error {
	// 获取等级配置
	levelConfig, exists := global.APP_CONFIG.Quota.LevelLimits[level]
	if !exists {
		global.APP_LOG.Warn("等级配置不存在，使用默认配置", zap.Int("level", level))
		// 使用默认配置
		levelConfig = config.LevelLimitInfo{
			MaxInstances: 1,
			MaxTraffic:   102400, // 100GB
			MaxResources: map[string]interface{}{
				"cpu":       1,
				"memory":    512,
				"disk":      10240,
				"bandwidth": 100,
			},
		}
	}

	// 构建更新数据 - 不再自动设置 total_traffic
	updateData := map[string]interface{}{
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

	// 更新用户资源限制
	if err := global.APP_DB.Table("users").
		Where("id = ?", userID).
		Updates(updateData).Error; err != nil {
		return err
	}

	global.APP_LOG.Debug("用户资源限制已同步",
		zap.Uint("userID", userID),
		zap.Int("level", level),
		zap.Any("updateData", updateData))

	return nil
}

// isEmailConfigured 检查邮箱配置是否可用
func (s *Service) isEmailConfigured() bool {
	// 检查系统配置中是否配置了邮箱服务
	var emailConfig admin.SystemConfig
	if err := global.APP_DB.Where("key = ?", "email_enabled").First(&emailConfig).Error; err != nil {
		return false
	}
	return emailConfig.Value == "true"
}

// sendEmail 发送邮件的基础函数
func (s *Service) sendEmail(to, subject, body string) error {
	// 这里应该集成真正的邮件服务，如SMTP
	// 目前只做记录，实际项目需要根据配置的邮件服务商实现
	global.APP_LOG.Info("邮件发送请求",
		zap.String("to", to),
		zap.String("subject", subject))

	// 模拟邮件发送成功
	// 在实际实现中，这里会调用邮件服务API
	return nil
}

// getCurrentAdminID 获取当前管理员ID
// 在实际实现中，这应该从HTTP请求上下文中获取
func (s *Service) getCurrentAdminID() uint {
	// 目前返回0表示系统操作
	// 实际实现中应该从JWT token或session中获取管理员ID
	return 0
}
