package invite

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"oneclickvirt/service/database"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/admin"
	"oneclickvirt/model/system"
	userModel "oneclickvirt/model/user"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Service 管理员邀请码管理服务
type Service struct{}

// NewService 创建邀请码管理服务
func NewService() *Service {
	return &Service{}
}

// GetInviteCodeList 获取邀请码列表
func (s *Service) GetInviteCodeList(req admin.InviteCodeListRequest) ([]admin.InviteCodeResponse, int64, error) {
	var inviteCodes []system.InviteCode
	var total int64

	query := global.APP_DB.Model(&system.InviteCode{})

	if req.Code != "" {
		query = query.Where("code LIKE ?", "%"+req.Code+"%")
	}

	// 按使用状态筛选
	if req.IsUsed != nil {
		if *req.IsUsed {
			// 已使用：UsedCount > 0
			query = query.Where("used_count > ?", 0)
		} else {
			// 未使用：UsedCount = 0
			query = query.Where("used_count = ?", 0)
		}
	}

	if req.Status != 0 {
		query = query.Where("status = ?", req.Status)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (req.Page - 1) * req.PageSize
	if err := query.Order("created_at DESC").Offset(offset).Limit(req.PageSize).Find(&inviteCodes).Error; err != nil {
		return nil, 0, err
	}

	// 批量查询创建者用户信息
	var creatorIDs []uint
	creatorIDSet := make(map[uint]bool)
	for _, code := range inviteCodes {
		if code.CreatorID != 0 && !creatorIDSet[code.CreatorID] {
			creatorIDs = append(creatorIDs, code.CreatorID)
			creatorIDSet[code.CreatorID] = true
		}
	}

	var users []userModel.User
	userMap := make(map[uint]string)
	if len(creatorIDs) > 0 {
		global.APP_DB.Select("id, username").
			Where("id IN ?", creatorIDs).
			Limit(500).
			Find(&users)
		for _, user := range users {
			userMap[user.ID] = user.Username
		}
	}

	var codeResponses []admin.InviteCodeResponse
	now := time.Now()
	for _, code := range inviteCodes {
		var createdByUser string
		if code.CreatorID != 0 {
			createdByUser = userMap[code.CreatorID]
		}

		// 检查邀请码是否真实过期（优先级高于数据库status字段）
		actualStatus := code.Status
		if code.ExpiresAt != nil && code.ExpiresAt.Before(now) {
			actualStatus = 0 // 已过期
			global.APP_LOG.Debug("邀请码列表检测到过期码",
				zap.String("code", code.Code),
				zap.Time("expiresAt", *code.ExpiresAt),
				zap.Time("now", now))
		}
		// 检查是否已达到最大使用次数
		if code.MaxUses > 0 && code.UsedCount >= code.MaxUses {
			actualStatus = 0 // 已用完
		}

		// 创建响应时使用实际状态
		codeWithActualStatus := code
		codeWithActualStatus.Status = actualStatus

		codeResponse := admin.InviteCodeResponse{
			InviteCode:    codeWithActualStatus,
			CreatedByUser: createdByUser,
		}
		codeResponses = append(codeResponses, codeResponse)
	}

	return codeResponses, total, nil
}

// CreateInviteCode 创建邀请码
func (s *Service) CreateInviteCode(req admin.CreateInviteCodeRequest, createdBy uint) error {
	// 如果指定了自定义邀请码
	if req.Code != "" {
		// 验证自定义邀请码格式（仅允许数字和大写字母）
		for _, c := range req.Code {
			if !((c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z')) {
				return fmt.Errorf("自定义邀请码只能包含数字和英文大写字母")
			}
		}

		// 验证邀请码是否已存在
		var existingCode system.InviteCode
		if err := global.APP_DB.Where("code = ?", req.Code).First(&existingCode).Error; err == nil {
			return fmt.Errorf("邀请码 %s 已存在", req.Code)
		}
		var expiresAt *time.Time
		if req.ExpiresAt != "" {
			// 使用本地时区解析时间，避免时区问题
			if parsedTime, err := time.ParseInLocation("2006-01-02 15:04:05", req.ExpiresAt, time.Local); err == nil {
				expiresAt = &parsedTime
				global.APP_LOG.Debug("解析自定义邀请码过期时间",
					zap.String("input", req.ExpiresAt),
					zap.Time("parsed", parsedTime),
					zap.String("timezone", parsedTime.Location().String()))
			} else {
				global.APP_LOG.Warn("邀请码过期时间解析失败",
					zap.String("input", req.ExpiresAt),
					zap.Error(err))
			}
		}
		inviteCode := system.InviteCode{
			Code:        req.Code,
			CreatorID:   createdBy,
			CreatorName: "", // 将由数据库触发器或其他逻辑填充
			Description: req.Remark,
			MaxUses:     req.MaxUses,
			ExpiresAt:   expiresAt,
			Status:      1,
		}
		dbService := database.GetDatabaseService()
		if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
			return tx.Create(&inviteCode).Error
		}); err != nil {
			return err
		}
		return nil
	}
	// 如果没有指定自定义邀请码，按原来的逻辑批量生成
	codeLength := req.Length
	if codeLength <= 0 {
		codeLength = 8 // 默认8位
	}

	for i := 0; i < req.Count; i++ {
		code := s.generateInviteCodeWithLength(codeLength)
		// 确保生成的邀请码不重复
		var existingCode system.InviteCode
		for {
			if err := global.APP_DB.Where("code = ?", code).First(&existingCode).Error; err != nil {
				if err.Error() == "record not found" {
					break
				}
				return fmt.Errorf("检查邀请码唯一性失败: %v", err)
			}
			// 如果邀请码已存在，重新生成
			code = s.generateInviteCodeWithLength(codeLength)
		}
		var expiresAt *time.Time
		if req.ExpiresAt != "" {
			// 使用本地时区解析时间，避免时区问题
			if parsedTime, err := time.ParseInLocation("2006-01-02 15:04:05", req.ExpiresAt, time.Local); err == nil {
				expiresAt = &parsedTime
				global.APP_LOG.Debug("解析批量邀请码过期时间",
					zap.String("input", req.ExpiresAt),
					zap.Time("parsed", parsedTime),
					zap.String("timezone", parsedTime.Location().String()))
			} else {
				global.APP_LOG.Warn("批量邀请码过期时间解析失败",
					zap.String("input", req.ExpiresAt),
					zap.Error(err))
			}
		}
		inviteCode := system.InviteCode{
			Code:        code,
			CreatorID:   createdBy,
			CreatorName: "", // 将由数据库触发器或其他逻辑填充
			Description: req.Remark,
			MaxUses:     req.MaxUses,
			ExpiresAt:   expiresAt,
			Status:      1,
		}
		dbService := database.GetDatabaseService()
		if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
			return tx.Create(&inviteCode).Error
		}); err != nil {
			return err
		}
	}
	return nil
}

// GenerateInviteCodes 生成批量邀请码
func (s *Service) GenerateInviteCodes(req admin.CreateInviteCodeRequest, createdBy uint) ([]string, error) {
	var codes []string

	codeLength := req.Length
	if codeLength <= 0 {
		codeLength = 8 // 默认8位
	}

	for i := 0; i < req.Count; i++ {
		code := s.generateInviteCodeWithLength(codeLength)

		var expiresAt *time.Time
		if req.ExpiresAt != "" {
			if parsedTime, err := time.Parse("2006-01-02 15:04:05", req.ExpiresAt); err == nil {
				expiresAt = &parsedTime
			}
		}

		inviteCode := system.InviteCode{
			Code:        code,
			CreatorID:   createdBy,
			CreatorName: "", // 将由数据库触发器或其他逻辑填充
			Description: req.Remark,
			MaxUses:     req.MaxUses,
			ExpiresAt:   expiresAt,
			Status:      1,
		}

		dbService := database.GetDatabaseService()
		if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
			return tx.Create(&inviteCode).Error
		}); err != nil {
			return nil, err
		}

		codes = append(codes, code)
	}

	return codes, nil
}

// generateInviteCodeWithLength 生成指定长度的随机邀请码 (仅数字和英文大写字母)
func (s *Service) generateInviteCodeWithLength(length int) string {
	const charset = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	bytes := make([]byte, length)

	for i := range bytes {
		randBig, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			// 如果随机数生成失败，使用默认字符
			bytes[i] = charset[0]
		} else {
			bytes[i] = charset[randBig.Int64()]
		}
	}

	return string(bytes)
}

// DeleteInviteCode 删除邀请码（硬删除）
func (s *Service) DeleteInviteCode(codeID uint) error {
	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		// 使用Unscoped()进行硬删除，而不是软删除
		return tx.Unscoped().Delete(&system.InviteCode{}, codeID).Error
	})
}

// BatchDeleteInviteCodes 批量删除邀请码（硬删除）
func (s *Service) BatchDeleteInviteCodes(ids []uint) error {
	if len(ids) == 0 {
		return fmt.Errorf("请选择要删除的邀请码")
	}

	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		// 使用Unscoped()进行硬删除，而不是软删除
		return tx.Unscoped().Delete(&system.InviteCode{}, ids).Error
	})
}

// ExportInviteCodes 导出邀请码为文本格式（每行一个）
func (s *Service) ExportInviteCodes(ids []uint) ([]string, error) {
	var codes []system.InviteCode
	query := global.APP_DB.Model(&system.InviteCode{})

	if len(ids) > 0 {
		// 如果指定了ID，只导出指定的邀请码
		query = query.Where("id IN ?", ids)
	}

	if err := query.Find(&codes).Error; err != nil {
		return nil, err
	}

	var result []string
	for _, code := range codes {
		result = append(result, code.Code)
	}

	return result, nil
}
