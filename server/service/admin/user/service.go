package user

import (
	"context"
	"errors"
	auth2 "oneclickvirt/service/auth"
	"oneclickvirt/service/cache"
	"oneclickvirt/service/database"
	"oneclickvirt/service/userquota"

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
	if req.Nickname != "" {
		query = query.Where("nickname LIKE ?", "%"+req.Nickname+"%")
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
		if err := global.APP_DB.Model(&providerModel.Instance{}).
			Select("user_id, COUNT(*) as instance_count").
			Where("user_id IN ?", userIDs).
			Group("user_id").
			Scan(&countResults).Error; err != nil {
			// 查询实例统计失败时记录日志但不中断流程
			global.APP_LOG.Warn("批量查询用户实例数量失败",
				zap.Error(err),
				zap.Int("userCount", len(userIDs)))
		}
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

	if err := utils.ValidateUsername(req.Username); err != nil {
		return common.NewError(common.CodeValidationError, err.Error())
	}
	if err := utils.ValidateOptionalEmail(req.Email); err != nil {
		return common.NewError(common.CodeValidationError, err.Error())
	}

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
	level := req.Level
	if isAdminUserType(req.UserType) {
		level = highestConfiguredUserLevel()
	}
	if err := validateConfiguredUserLevel(level); err != nil {
		return common.NewError(common.CodeValidationError, err.Error())
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
		Level:      level,
		TotalQuota: req.TotalQuota,
		Status:     req.Status,
	}
	if err := userquota.ApplyLimitFields(&user, level); err != nil {
		return common.NewError(common.CodeValidationError, err.Error())
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

	global.APP_LOG.Info("用户创建成功",
		zap.String("username", utils.TruncateString(req.Username, 32)),
		zap.String("userType", req.UserType),
		zap.Int("level", level))
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
		if err := utils.ValidateUsername(req.Username); err != nil {
			return common.NewError(common.CodeValidationError, err.Error())
		}
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
		if err := utils.ValidateOptionalEmail(req.Email); err != nil {
			return common.NewError(common.CodeValidationError, err.Error())
		}
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
		if err := validateConfiguredUserLevel(req.Level); err != nil {
			return common.NewError(common.CodeValidationError, err.Error())
		}
		if err := userquota.ApplyLevelAndLimitFields(&user, req.Level); err != nil {
			return common.NewError(common.CodeValidationError, err.Error())
		}
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

	// 管理员类账号始终保持最高等级与最高等级配额，防止后台编辑产生等级/配额不一致。
	if isAdminUserType(user.UserType) {
		adminLevel := highestConfiguredUserLevel()
		if err := userquota.ApplyLevelAndLimitFields(&user, adminLevel); err != nil {
			return common.NewError(common.CodeValidationError, err.Error())
		}
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
	permissionService.ClearUserPermissionCache(user.ID) // 同时清除认证上下文缓存（包括用户状态/类型/权限变更）
	cache.GetUserCacheService().InvalidateUserCache(user.ID)
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
