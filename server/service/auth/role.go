package auth

import (
	"context"
	"errors"
	"oneclickvirt/service/database"

	"oneclickvirt/global"
	"oneclickvirt/model/auth"
	"oneclickvirt/model/common"
	"oneclickvirt/model/user"

	"gorm.io/gorm"
)

type RoleService struct{}

// GetRoleList 获取角色列表
func (s *RoleService) GetRoleList(req common.PageInfo) (interface{}, error) {
	limit := req.PageSize
	offset := req.PageSize * (req.Page - 1)

	var roles []auth.Role
	var total int64

	db := global.APP_DB.Model(&auth.Role{})

	// 计算总数
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}

	// 获取分页数据
	if err := db.Limit(limit).Offset(offset).Find(&roles).Error; err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"list":     roles,
		"total":    total,
		"page":     req.Page,
		"pageSize": req.PageSize,
	}, nil
}

// CreateRole 创建角色
func (s *RoleService) CreateRole(name, code, remark string) error {
	// 检查角色名称是否已存在
	var count int64
	global.APP_DB.Model(&auth.Role{}).Where("name = ?", name).Count(&count)
	if count > 0 {
		return errors.New("角色名称已存在")
	}

	// 检查角色代码是否已存在
	global.APP_DB.Model(&auth.Role{}).Where("code = ?", code).Count(&count)
	if count > 0 {
		return errors.New("角色代码已存在")
	}

	role := auth.Role{
		Name:   name,
		Code:   code,
		Remark: remark,
		Status: 1,
	}

	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Create(&role).Error
	})
}

// UpdateRole 更新角色
func (s *RoleService) UpdateRole(roleID uint, name, code, remark string) error {
	var role auth.Role
	if err := global.APP_DB.First(&role, roleID).Error; err != nil {
		return errors.New("角色不存在")
	}

	// 检查角色名称是否被其他角色使用
	if name != role.Name {
		var count int64
		global.APP_DB.Model(&auth.Role{}).Where("name = ? AND id != ?", name, roleID).Count(&count)
		if count > 0 {
			return errors.New("角色名称已存在")
		}
	}

	// 检查角色代码是否被其他角色使用
	if code != role.Code {
		var count int64
		global.APP_DB.Model(&auth.Role{}).Where("code = ? AND id != ?", code, roleID).Count(&count)
		if count > 0 {
			return errors.New("角色代码已存在")
		}
	}

	updates := map[string]interface{}{
		"name":   name,
		"code":   code,
		"remark": remark,
	}

	return global.APP_DB.Model(&role).Updates(updates).Error
}

// DeleteRole 删除角色
func (s *RoleService) DeleteRole(roleID uint) error {
	var role auth.Role
	if err := global.APP_DB.First(&role, roleID).Error; err != nil {
		return errors.New("角色不存在")
	}

	// 检查角色是否有关联的用户
	var userCount int64
	global.APP_DB.Model(&user.User{}).Where("role_id = ?", roleID).Count(&userCount)
	if userCount > 0 {
		return errors.New("角色还有关联的用户，无法删除")
	}

	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Delete(&role).Error
	})
}

// GetRoleByID 根据ID获取角色
func (s *RoleService) GetRoleByID(roleID uint) (*auth.Role, error) {
	var role auth.Role
	if err := global.APP_DB.First(&role, roleID).Error; err != nil {
		return nil, errors.New("角色不存在")
	}
	return &role, nil
}

// GetAllRoles 获取所有角色（不分页）
func (s *RoleService) GetAllRoles() ([]auth.Role, error) {
	var roles []auth.Role
	if err := global.APP_DB.Find(&roles).Error; err != nil {
		return nil, err
	}
	return roles, nil
}
