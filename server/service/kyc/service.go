package kyc

import (
	"crypto/sha256"
	"fmt"

	"oneclickvirt/global"
	kycModel "oneclickvirt/model/kyc"

	"go.uber.org/zap"
)

// Service KYC实名认证服务
type Service struct{}

// GetUserKYC 获取用户实名认证状态
func (s *Service) GetUserKYC(userID uint) (*kycModel.KYCRecord, error) {
	var record kycModel.KYCRecord
	err := global.APP_DB.Where("user_id = ?", userID).First(&record).Error
	if err != nil {
		return nil, err
	}
	return &record, nil
}

// SubmitKYC 提交实名认证
func (s *Service) SubmitKYC(userID uint, req *SubmitKYCRequest) (*kycModel.KYCRecord, error) {
	// 检查是否已有认证记录
	var existing int64
	global.APP_DB.Model(&kycModel.KYCRecord{}).Where("user_id = ?", userID).Count(&existing)
	if existing > 0 {
		return nil, fmt.Errorf("已提交过实名认证，请勿重复提交")
	}

	// 身份证号哈希(查重)
	idHash := fmt.Sprintf("%x", sha256.Sum256([]byte(req.IDNumber)))

	// 检查身份证号是否已被使用
	var hashCount int64
	global.APP_DB.Model(&kycModel.KYCRecord{}).Where("id_number_hash = ?", idHash).Count(&hashCount)
	if hashCount > 0 {
		return nil, fmt.Errorf("该身份证号已被其他账户认证")
	}

	record := &kycModel.KYCRecord{
		UserID:       userID,
		RealName:     req.RealName,
		IDNumber:     req.IDNumber, // 实际生产中应加密存储
		IDNumberHash: idHash,
		Method:       "alipay",
		Status:       "pending",
	}
	if err := global.APP_DB.Create(record).Error; err != nil {
		return nil, fmt.Errorf("提交认证失败: %v", err)
	}

	global.APP_LOG.Info("用户提交实名认证",
		zap.Uint("userID", userID),
		zap.String("method", "alipay"))

	return record, nil
}

// AdminGetKYCList 管理员获取认证列表
func (s *Service) AdminGetKYCList(status string, page, pageSize int) ([]kycModel.KYCRecord, int64, error) {
	var records []kycModel.KYCRecord
	var total int64
	query := global.APP_DB.Model(&kycModel.KYCRecord{})
	if status != "" {
		query = query.Where("status = ?", status)
	}
	query.Count(&total)
	err := query.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&records).Error
	return records, total, err
}

// AdminReviewKYC 管理员审核认证
func (s *Service) AdminReviewKYC(kycID, reviewerID uint, approved bool, rejectReason string) error {
	var record kycModel.KYCRecord
	if err := global.APP_DB.First(&record, kycID).Error; err != nil {
		return fmt.Errorf("认证记录不存在")
	}
	if record.Status != "pending" {
		return fmt.Errorf("该认证已审核过")
	}

	status := "approved"
	if !approved {
		status = "rejected"
	}

	tx := global.APP_DB.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	now := global.APP_DB.NowFunc()
	if err := tx.Model(&record).Updates(map[string]interface{}{
		"status":        status,
		"reviewed_by":   reviewerID,
		"reviewed_at":   now,
		"reject_reason": rejectReason,
	}).Error; err != nil {
		tx.Rollback()
		return err
	}

	// 审核通过时更新用户实名状态
	if approved {
		if err := tx.Table("users").Where("id = ?", record.UserID).
			Update("real_name_verified", true).Error; err != nil {
			tx.Rollback()
			return err
		}
	}

	return tx.Commit().Error
}

// Request types

type SubmitKYCRequest struct {
	RealName string `json:"realName" binding:"required"`
	IDNumber string `json:"idNumber" binding:"required"`
}
