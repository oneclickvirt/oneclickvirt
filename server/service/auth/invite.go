package auth

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"oneclickvirt/service/database"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/auth"
	"oneclickvirt/model/common"
	"oneclickvirt/model/system"

	"gorm.io/gorm"
)

type InviteService struct{}

// BatchGenerateInviteCodes 批量生成邀请码
func (s *InviteService) BatchGenerateInviteCodes(userID uint, username string, req auth.BatchGenerateInviteCodesRequest) ([]system.InviteCode, error) {
	var inviteCodes []system.InviteCode

	// 计算过期时间
	var expireAt *time.Time
	if req.ExpireDays > 0 {
		expireTime := time.Now().AddDate(0, 0, req.ExpireDays)
		expireAt = &expireTime
	}

	// 使用数据库抽象层处理批量生成
	dbService := database.GetDatabaseService()
	err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		for i := 0; i < req.Count; i++ {
			// 生成随机邀请码，支持自定义长度
			codeLength := req.Length
			if codeLength <= 0 {
				codeLength = 8 // 默认8位
			}
			code, err := s.generateInviteCodeWithLength(codeLength)
			if err != nil {
				return common.NewError(common.CodeInternalError, "生成邀请码失败")
			}

			// 确保邀请码唯一
			var existingCode system.InviteCode
			for {
				if err := tx.Where("code = ?", code).First(&existingCode).Error; err != nil {
					if err.Error() == "record not found" {
						break
					}
					return common.NewError(common.CodeDatabaseError, "检查邀请码唯一性失败")
				}
				// 如果邀请码已存在，重新生成
				code, err = s.generateInviteCodeWithLength(codeLength)
				if err != nil {
					return common.NewError(common.CodeInternalError, "生成邀请码失败")
				}
			}

			maxUse := req.MaxUse
			if maxUse == 0 {
				maxUse = 1 // 默认单次使用
			}

			inviteCode := system.InviteCode{
				Code:        code,
				CreatorID:   userID,
				CreatorName: username,
				Description: fmt.Sprintf("%s - %s", req.Description, username),
				MaxUses:     maxUse,
				UsedCount:   0,
				ExpiresAt:   expireAt,
				Status:      1,
			}

			if err := tx.Create(&inviteCode).Error; err != nil {
				return common.NewError(common.CodeDatabaseError, "创建邀请码失败")
			}

			inviteCodes = append(inviteCodes, inviteCode)
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	return inviteCodes, nil
}

// generateInviteCodeWithLength 生成指定长度的随机邀请码 (仅数字和英文大写字母)
func (s *InviteService) generateInviteCodeWithLength(length int) (string, error) {
	const charset = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	bytes := make([]byte, length)

	for i := range bytes {
		randBig, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", err
		}
		bytes[i] = charset[randBig.Int64()]
	}

	return string(bytes), nil
}

// GetInviteCodeList 获取邀请码列表
func (s *InviteService) GetInviteCodeList(pageInfo common.PageInfo, userID *uint) ([]system.InviteCode, int64, error) {
	db := global.APP_DB.Model(&system.InviteCode{})

	// 如果指定了用户ID，只查询该用户创建的邀请码
	if userID != nil {
		db = db.Where("created_by = ?", *userID)
	}

	// 关键词搜索
	if pageInfo.Keyword != "" {
		db = db.Where("code LIKE ? OR description LIKE ?", "%"+pageInfo.Keyword+"%", "%"+pageInfo.Keyword+"%")
	}

	// 获取总数
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, common.NewError(common.CodeDatabaseError, "统计邀请码数量失败")
	}

	// 分页查询
	var inviteCodes []system.InviteCode
	offset := (pageInfo.Page - 1) * pageInfo.PageSize
	if err := db.Offset(offset).Limit(pageInfo.PageSize).Order("created_at DESC").Find(&inviteCodes).Error; err != nil {
		return nil, 0, common.NewError(common.CodeDatabaseError, "查询邀请码失败")
	}

	return inviteCodes, total, nil
}

// DeleteInviteCode 删除邀请码
func (s *InviteService) DeleteInviteCode(id uint, userID uint, isAdmin bool) error {
	var inviteCode system.InviteCode
	db := global.APP_DB

	// 非管理员只能删除自己创建的邀请码
	if !isAdmin {
		db = db.Where("created_by = ?", userID)
	}

	if err := db.First(&inviteCode, id).Error; err != nil {
		return common.NewError(common.CodeNotFound, "邀请码不存在")
	}

	// 检查是否已被使用
	if inviteCode.UsedCount > 0 {
		return common.NewError(common.CodeConflict, "邀请码已被使用，无法删除")
	}

	// 使用数据库抽象层删除（硬删除）
	dbService := database.GetDatabaseService()
	if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		// 使用Unscoped()进行硬删除，而不是软删除
		return tx.Unscoped().Delete(&inviteCode).Error
	}); err != nil {
		return common.NewError(common.CodeDatabaseError, "删除邀请码失败")
	}

	return nil
}

// UpdateInviteCodeStatus 更新邀请码状态
func (s *InviteService) UpdateInviteCodeStatus(id uint, status int, userID uint, isAdmin bool) error {
	var inviteCode system.InviteCode
	db := global.APP_DB

	// 非管理员只能操作自己创建的邀请码
	if !isAdmin {
		db = db.Where("created_by = ?", userID)
	}

	if err := db.First(&inviteCode, id).Error; err != nil {
		return common.NewError(common.CodeNotFound, "邀请码不存在")
	}

	if err := global.APP_DB.Model(&inviteCode).Update("status", status).Error; err != nil {
		return common.NewError(common.CodeDatabaseError, "更新邀请码状态失败")
	}

	return nil
}
