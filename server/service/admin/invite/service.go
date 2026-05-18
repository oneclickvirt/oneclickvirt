package invite

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"oneclickvirt/service/database"
	"strings"
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
		if err := global.APP_DB.Select("id, username").
			Where("id IN ?", creatorIDs).
			Limit(500).
			Find(&users).Error; err != nil {
			// 查询用户失败时记录日志但不中断流程，仅返回没有用户名的邀请码列表
			global.APP_LOG.Warn("查询邀请码创建者用户信息失败，将返回不含用户名的列表",
				zap.Error(err),
				zap.Int("creatorCount", len(creatorIDs)))
		} else {
			for _, user := range users {
				userMap[user.ID] = user.Username
			}
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
		expiresAt := s.parseInviteExpiresAt(req.ExpiresAt, "解析自定义邀请码过期时间", "邀请码过期时间解析失败")
		inviteCode := system.InviteCode{
			Code:        req.Code,
			CreatorID:   createdBy,
			CreatorName: "", // 将由数据库触发器或其他逻辑填充
			Description: req.Remark,
			MaxUses:     req.MaxUses,
			ExpiresAt:   expiresAt,
			Status:      1,
		}
		if err := s.insertInviteCodes([]system.InviteCode{inviteCode}); err != nil {
			return err
		}
		return nil
	}

	_, err := s.generateAndInsertInviteCodes(req, createdBy)
	return err
}

// GenerateInviteCodes 生成批量邀请码
func (s *Service) GenerateInviteCodes(req admin.CreateInviteCodeRequest, createdBy uint) ([]string, error) {
	return s.generateAndInsertInviteCodes(req, createdBy)
}

func (s *Service) generateAndInsertInviteCodes(req admin.CreateInviteCodeRequest, createdBy uint) ([]string, error) {
	if req.Count <= 0 {
		return nil, fmt.Errorf("生成数量必须大于0")
	}

	codeLength := req.Length
	if codeLength <= 0 {
		codeLength = 8
	}

	expiresAt := s.parseInviteExpiresAt(req.ExpiresAt, "解析批量邀请码过期时间", "批量邀请码过期时间解析失败")

	const maxRetries = 5
	for attempt := 0; attempt < maxRetries; attempt++ {
		inviteCodes, codes := s.buildInviteCodeBatch(req.Count, codeLength, createdBy, req.Remark, req.MaxUses, expiresAt)
		if err := s.insertInviteCodes(inviteCodes); err != nil {
			if isDuplicateInviteCodeError(err) && attempt < maxRetries-1 {
				global.APP_LOG.Warn("批量邀请码生成发生冲突，准备重试",
					zap.Int("attempt", attempt+1),
					zap.Int("count", req.Count),
					zap.Error(err))
				continue
			}
			return nil, err
		}
		return codes, nil
	}

	return nil, fmt.Errorf("生成邀请码失败，请重试")
}

func (s *Service) buildInviteCodeBatch(count, codeLength int, createdBy uint, remark string, maxUses int, expiresAt *time.Time) ([]system.InviteCode, []string) {
	inviteCodes := make([]system.InviteCode, 0, count)
	codes := make([]string, 0, count)
	generated := make(map[string]struct{}, count)

	for len(inviteCodes) < count {
		code := s.generateInviteCodeWithLength(codeLength)
		if _, exists := generated[code]; exists {
			continue
		}
		generated[code] = struct{}{}

		inviteCodes = append(inviteCodes, system.InviteCode{
			Code:        code,
			CreatorID:   createdBy,
			CreatorName: "",
			Description: remark,
			MaxUses:     maxUses,
			ExpiresAt:   expiresAt,
			Status:      1,
		})
		codes = append(codes, code)
	}

	return inviteCodes, codes
}

func (s *Service) insertInviteCodes(inviteCodes []system.InviteCode) error {
	if len(inviteCodes) == 0 {
		return nil
	}

	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.CreateInBatches(inviteCodes, len(inviteCodes)).Error
	})
}

func (s *Service) parseInviteExpiresAt(value, debugMessage, warnMessage string) *time.Time {
	if value == "" {
		return nil
	}

	parsedTime, err := time.ParseInLocation("2006-01-02 15:04:05", value, time.Local)
	if err != nil {
		global.APP_LOG.Warn(warnMessage,
			zap.String("input", value),
			zap.Error(err))
		return nil
	}

	global.APP_LOG.Debug(debugMessage,
		zap.String("input", value),
		zap.Time("parsed", parsedTime),
		zap.String("timezone", parsedTime.Location().String()))
	return &parsedTime
}

func isDuplicateInviteCodeError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "duplicate") || strings.Contains(lower, "unique")
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
