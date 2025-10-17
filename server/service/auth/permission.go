package auth

import (
	"fmt"
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/model/permission"
	"oneclickvirt/model/user"

	"go.uber.org/zap"
)

type PermissionService struct{}

// UserEffectivePermission 用户有效权限
type UserEffectivePermission struct {
	UserID         uint     `json:"userId"`
	EffectiveType  string   `json:"effectiveType"`  // 最高权限类型
	EffectiveLevel int      `json:"effectiveLevel"` // 有效等级
	AllTypes       []string `json:"allTypes"`       // 所有权限类型
}

// GetUserEffectivePermission 获取用户有效权限（直接从数据库查询，无缓存）
func (s *PermissionService) GetUserEffectivePermission(userID uint) (*UserEffectivePermission, error) {
	// 验证用户ID的有效性
	if userID == 0 {
		return nil, fmt.Errorf("无效的用户ID")
	}

	// 从数据库获取用户信息，确保用户存在且状态正常
	var user user.User
	if err := global.APP_DB.Select("id, username, user_type, status, level").First(&user, userID).Error; err != nil {
		return nil, fmt.Errorf("用户不存在: %v", err)
	}

	// 严格检查用户状态
	if user.Status != 1 {
		return nil, fmt.Errorf("用户账户已被禁用")
	}

	// 验证用户基础权限类型的合法性
	validTypes := map[string]bool{"user": true, "admin": true}
	if !validTypes[user.UserType] {
		global.APP_LOG.Error("用户基础权限类型无效",
			zap.Uint("userID", userID),
			zap.String("invalidUserType", user.UserType))
		return nil, fmt.Errorf("用户基础权限类型无效")
	}

	// 获取用户的权限组合
	var permissions []permission.UserPermission
	if err := global.APP_DB.Where("user_id = ? AND is_active = ?", userID, true).Find(&permissions).Error; err != nil {
		return nil, fmt.Errorf("查询用户权限失败: %v", err)
	}

	// 计算有效权限
	effective := s.calculateEffectivePermission(&user, permissions)

	// 最终权限验证：确保计算出的权限不会超出合理范围
	if effective == nil {
		return nil, fmt.Errorf("计算用户权限失败")
	}

	// 验证有效权限的合法性
	if !validTypes[effective.EffectiveType] {
		global.APP_LOG.Error("计算出的有效权限类型无效",
			zap.Uint("userID", userID),
			zap.String("effectiveType", effective.EffectiveType))
		effective.EffectiveType = user.UserType
	}

	// 确保权限级别在合理范围内
	if effective.EffectiveLevel < 0 {
		effective.EffectiveLevel = 1
	}
	if effective.EffectiveLevel > 10 { // 设置权限级别上限
		effective.EffectiveLevel = 10
	}

	return effective, nil
}

// calculateEffectivePermission 计算用户的有效权限
func (s *PermissionService) calculateEffectivePermission(user *user.User, permissions []permission.UserPermission) *UserEffectivePermission {
	effective := &UserEffectivePermission{
		UserID:         user.ID,
		EffectiveType:  user.UserType,
		EffectiveLevel: user.Level,
		AllTypes:       []string{user.UserType},
	}

	// 遍历所有权限组合，找到最高权限
	for _, perm := range permissions {
		if !perm.IsActive {
			continue
		}

		userTypes := perm.GetUserTypes()
		for _, userType := range userTypes {
			// 如果找到admin权限，立即设置为最高权限
			if userType == "admin" {
				effective.EffectiveType = "admin"
				if perm.Level > effective.EffectiveLevel {
					effective.EffectiveLevel = perm.Level
				}
			}

			// 到所有权限类型中（去重）
			found := false
			for _, existingType := range effective.AllTypes {
				if existingType == userType {
					found = true
					break
				}
			}
			if !found {
				effective.AllTypes = append(effective.AllTypes, userType)
			}
		}

		// 更新有效等级为最高等级
		if perm.Level > effective.EffectiveLevel {
			effective.EffectiveLevel = perm.Level
		}
	}

	return effective
}

// HasPermission 检查用户是否有指定权限
func (s *PermissionService) HasPermission(userID uint, requiredType string) bool {
	effective, err := s.GetUserEffectivePermission(userID)
	if err != nil {
		global.APP_LOG.Error("获取用户权限失败", zap.Uint("userID", userID), zap.Error(err))
		return false
	}

	// admin权限可以访问所有资源
	if effective.EffectiveType == "admin" {
		return true
	}

	// 检查是否有指定的权限类型
	for _, userType := range effective.AllTypes {
		if userType == requiredType {
			return true
		}
	}

	return false
}

// RequireAdminPermission 检查是否具有管理员权限
func (s *PermissionService) RequireAdminPermission(userID uint) bool {
	return s.HasPermission(userID, "admin")
}

// RequireUserPermission 检查是否具有用户权限（包括admin）
func (s *PermissionService) RequireUserPermission(userID uint) bool {
	return s.HasPermission(userID, "user") || s.HasPermission(userID, "admin")
}

// CheckInstancePermission 检查实例创建权限
func (s *PermissionService) CheckInstancePermission(userID uint, instanceType string) bool {
	effective, err := s.GetUserEffectivePermission(userID)
	if err != nil {
		return false
	}

	// admin 权限可以创建任何类型的实例
	if effective.EffectiveType == "admin" {
		return true
	}

	// 根据配置检查用户等级是否满足实例类型要求
	permissions := global.APP_CONFIG.Quota.InstanceTypePermissions
	switch instanceType {
	case "container":
		return effective.EffectiveLevel >= permissions.MinLevelForContainer
	case "vm":
		return effective.EffectiveLevel >= permissions.MinLevelForVM
	default:
		return false
	}
}

// CheckInstanceDeletePermission 检查实例删除权限
func (s *PermissionService) CheckInstanceDeletePermission(userID uint, instanceType string) bool {
	effective, err := s.GetUserEffectivePermission(userID)
	if err != nil {
		return false
	}

	// admin 权限可以删除任何实例
	if effective.EffectiveType == "admin" {
		return true
	}

	// 根据配置和实例类型检查用户等级是否满足删除权限要求
	permissions := global.APP_CONFIG.Quota.InstanceTypePermissions
	switch instanceType {
	case "container":
		return effective.EffectiveLevel >= permissions.MinLevelForDeleteContainer
	case "vm":
		return effective.EffectiveLevel >= permissions.MinLevelForDeleteVM
	default:
		return false
	}
}

// CheckInstanceResetPermission 检查实例重置权限
func (s *PermissionService) CheckInstanceResetPermission(userID uint, instanceType string) bool {
	effective, err := s.GetUserEffectivePermission(userID)
	if err != nil {
		return false
	}

	// admin 权限可以重置任何实例
	if effective.EffectiveType == "admin" {
		return true
	}

	// 根据配置和实例类型检查用户等级是否满足重置权限要求
	permissions := global.APP_CONFIG.Quota.InstanceTypePermissions
	switch instanceType {
	case "container":
		return effective.EffectiveLevel >= permissions.MinLevelForResetContainer
	case "vm":
		return effective.EffectiveLevel >= permissions.MinLevelForResetVM
	default:
		return false
	}
}

// CheckAPIAccess 检查API访问权限
func (s *PermissionService) CheckAPIAccess(userID uint, path string, method string) bool {
	// 检查是否为管理员API
	if strings.HasPrefix(path, "/api/v1/admin") {
		return s.RequireAdminPermission(userID)
	}

	// 检查实例创建权限
	if strings.Contains(path, "/instances") && method == "POST" {
		return s.RequireUserPermission(userID)
	}

	// 检查是否为用户API
	return strings.HasPrefix(path, "/api/v1/user") || strings.HasPrefix(path, "/api/v1/dashboard")
}

// AddUserPermission 添加用户权限
func (s *PermissionService) AddUserPermission(userID uint, userTypes []string, level int, remark string) error {
	permission := permission.UserPermission{
		UserID:    userID,
		UserTypes: strings.Join(userTypes, ","),
		Level:     level,
		IsActive:  true,
		Remark:    remark,
	}

	return global.APP_DB.Create(&permission).Error
}

// RemoveUserPermission 删除用户权限
func (s *PermissionService) RemoveUserPermission(userID uint, permissionID uint) error {
	return global.APP_DB.Delete(&permission.UserPermission{}, "id = ? AND user_id = ?", permissionID, userID).Error
}

// GetUserPermissions 获取用户权限列表
func (s *PermissionService) GetUserPermissions(userID uint) ([]permission.UserPermission, error) {
	var permissions []permission.UserPermission
	if err := global.APP_DB.Where("user_id = ?", userID).Find(&permissions).Error; err != nil {
		return nil, fmt.Errorf("查询用户权限失败: %v", err)
	}

	return permissions, nil
}

// VerifyAdminPrivilege 双重验证管理员权限
func (s *PermissionService) VerifyAdminPrivilege(userID uint) bool {
	// 从数据库直接查询用户的基础权限类型
	var user user.User
	if err := global.APP_DB.Select("user_type, status").First(&user, userID).Error; err != nil {
		global.APP_LOG.Error("验证管理员权限时查询用户失败",
			zap.Uint("userID", userID),
			zap.Error(err))
		return false
	}

	// 检查用户状态
	if user.Status != 1 {
		return false
	}

	// 检查基础用户类型是否为管理员
	if user.UserType == "admin" {
		return true
	}

	// 检查是否通过权限组合获得了管理员权限
	var permissions []permission.UserPermission
	if err := global.APP_DB.Where("user_id = ? AND is_active = ?", userID, true).
		Find(&permissions).Error; err != nil {
		global.APP_LOG.Error("验证管理员权限组合时查询失败",
			zap.Uint("userID", userID),
			zap.Error(err))
		return false
	}

	// 在应用层精确验证权限类型，避免SQL注入
	for _, perm := range permissions {
		userTypes := perm.GetUserTypes()
		for _, userType := range userTypes {
			if userType == "admin" {
				return true
			}
		}
	}

	return false
}

// ClearUserPermissionCache 清除指定用户的权限缓存（兼容性方法，现在是空操作）
func (s *PermissionService) ClearUserPermissionCache(userID uint) {
	// 无缓存，无需操作
	global.APP_LOG.Debug("权限缓存已禁用，无需清理", zap.Uint("userID", userID))
}

// CanAccessResource 检查用户是否可以访问指定资源（兼容性方法）
func (s *PermissionService) CanAccessResource(userID uint, path string, method string) (bool, error) {
	// 使用CheckAPIAccess方法来检查权限
	hasPermission := s.CheckAPIAccess(userID, path, method)
	return hasPermission, nil
}
