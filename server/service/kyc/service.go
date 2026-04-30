package kyc

import (
	"crypto/sha256"
	"fmt"

	"oneclickvirt/global"
	kycModel "oneclickvirt/model/kyc"

	"go.uber.org/zap"
	"gorm.io/gorm"
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

// SubmitKYC 提交实名认证（手动审核方式）
func (s *Service) SubmitKYC(userID uint, req *SubmitKYCRequest) (*kycModel.KYCRecord, error) {
	// 检查是否已有认证记录
	var existing kycModel.KYCRecord
	err := global.APP_DB.Where("user_id = ?", userID).First(&existing).Error
	if err == nil {
		if existing.Status == "approved" {
			return nil, fmt.Errorf("已通过实名认证，不能重复提交")
		}
		if existing.Status == "pending" {
			return nil, fmt.Errorf("认证申请已存在，请等待审核")
		}
		// rejected: allow resubmit by updating existing record
		idHash := fmt.Sprintf("%x", sha256.Sum256([]byte(req.IDNumber)))
		var hashCount int64
		if err := global.APP_DB.Model(&kycModel.KYCRecord{}).Where("id_number_hash = ? AND user_id != ?", idHash, userID).Count(&hashCount).Error; err != nil {
			return nil, fmt.Errorf("校验身份证号失败: %v", err)
		}
		if hashCount > 0 {
			return nil, fmt.Errorf("该身份证号已被其他账户认证")
		}
		existing.RealName = req.RealName
		existing.IDNumber = req.IDNumber
		existing.IDNumberHash = idHash
		existing.Method = "manual"
		existing.Status = "pending"
		existing.RejectReason = ""
		if err := global.APP_DB.Save(&existing).Error; err != nil {
			return nil, fmt.Errorf("重新提交认证失败: %v", err)
		}
		return &existing, nil
	}

	// 身份证号哈希(查重)
	idHash := fmt.Sprintf("%x", sha256.Sum256([]byte(req.IDNumber)))

	// 检查身份证号是否已被使用 + 创建记录在同一事务中，避免 TOCTOU 竞争
	record := &kycModel.KYCRecord{
		UserID:       userID,
		RealName:     req.RealName,
		IDNumber:     req.IDNumber,
		IDNumberHash: idHash,
		Method:       "manual",
		Status:       "pending",
	}
	if err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
		var hashCount int64
		if err := tx.Model(&kycModel.KYCRecord{}).Where("id_number_hash = ?", idHash).Count(&hashCount).Error; err != nil {
			return err
		}
		if hashCount > 0 {
			return fmt.Errorf("该身份证号已被其他账户认证")
		}
		return tx.Create(record).Error
	}); err != nil {
		return nil, err
	}

	global.APP_LOG.Info("用户提交实名认证",
		zap.Uint("userID", userID),
		zap.String("method", "manual"))

	return record, nil
}

// SubmitAlipayKYC 通过支付宝人脸认证提交实名
func (s *Service) SubmitAlipayKYC(userID uint, req *SubmitKYCRequest) (certifyURL string, err error) {
	// 检查是否已有通过的认证
	var existing kycModel.KYCRecord
	findErr := global.APP_DB.Where("user_id = ?", userID).First(&existing).Error
	if findErr == nil && existing.Status == "approved" {
		return "", fmt.Errorf("已通过实名认证")
	}

	idHash := fmt.Sprintf("%x", sha256.Sum256([]byte(req.IDNumber)))
	var hashCount int64
	if err := global.APP_DB.Model(&kycModel.KYCRecord{}).Where("id_number_hash = ? AND user_id != ?", idHash, userID).Count(&hashCount).Error; err != nil {
		return "", fmt.Errorf("校验身份证号失败: %w", err)
	}
	if hashCount > 0 {
		return "", fmt.Errorf("该身份证号已被其他账户认证")
	}

	outerOrderNo := fmt.Sprintf("kyc_%d_%d", userID, global.APP_DB.NowFunc().Unix())

	certifyID, err := s.AlipayFaceCertifyInit(req.RealName, req.IDNumber, outerOrderNo)
	if err != nil {
		return "", err
	}

	certifyURL, err = s.AlipayFaceCertifyURL(certifyID)
	if err != nil {
		return "", err
	}

	if findErr == nil {
		// Update existing record
		existing.RealName = req.RealName
		existing.IDNumber = req.IDNumber
		existing.IDNumberHash = idHash
		existing.Method = "alipay"
		existing.Status = "pending"
		existing.AlipayCertifyID = certifyID
		existing.RejectReason = ""
		if err := global.APP_DB.Save(&existing).Error; err != nil {
			return "", fmt.Errorf("更新KYC记录失败: %w", err)
		}
	} else {
		// Create new record
		record := &kycModel.KYCRecord{
			UserID:          userID,
			RealName:        req.RealName,
			IDNumber:        req.IDNumber,
			IDNumberHash:    idHash,
			Method:          "alipay",
			Status:          "pending",
			AlipayCertifyID: certifyID,
		}
		if err := global.APP_DB.Create(record).Error; err != nil {
			return "", fmt.Errorf("创建KYC记录失败: %w", err)
		}
	}

	global.APP_LOG.Info("用户发起支付宝人脸认证",
		zap.Uint("userID", userID),
		zap.String("certifyID", certifyID))

	return certifyURL, nil
}

// QueryAlipayKYCResult 查询支付宝认证结果
func (s *Service) QueryAlipayKYCResult(userID uint) (bool, error) {
	var record kycModel.KYCRecord
	if err := global.APP_DB.Where("user_id = ? AND method = ?", userID, "alipay").First(&record).Error; err != nil {
		return false, fmt.Errorf("未找到支付宝认证记录")
	}
	if record.Status == "approved" {
		return true, nil
	}
	if record.AlipayCertifyID == "" {
		return false, fmt.Errorf("缺少认证ID，参数无效")
	}

	passed, err := s.AlipayFaceCertifyQuery(record.AlipayCertifyID)
	if err != nil {
		return false, err
	}

	if passed {
		tx := global.APP_DB.Begin()
		defer tx.Rollback()
		now := global.APP_DB.NowFunc()
		if err := tx.Model(&record).Updates(map[string]interface{}{
			"status":      "approved",
			"reviewed_by": 0,
			"reviewed_at": now,
		}).Error; err != nil {
			return false, fmt.Errorf("更新KYC状态失败: %w", err)
		}
		if err := tx.Table("users").Where("id = ?", record.UserID).Update("real_name_verified", true).Error; err != nil {
			return false, fmt.Errorf("更新用户实名状态失败: %w", err)
		}
		if err := tx.Commit().Error; err != nil {
			return false, err
		}
		global.APP_LOG.Info("支付宝人脸认证通过",
			zap.Uint("userID", userID))
	}

	return passed, nil
}

// AdminGetKYCList 管理员获取认证列表
func (s *Service) AdminGetKYCList(status string, page, pageSize int) ([]kycModel.KYCRecord, int64, error) {
	var records []kycModel.KYCRecord
	var total int64
	query := global.APP_DB.Model(&kycModel.KYCRecord{})
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计认证记录失败: %w", err)
	}
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
		return fmt.Errorf("该认证记录已存在审核结果，不能重复审核")
	}

	status := "approved"
	if !approved {
		status = "rejected"
	}

	tx := global.APP_DB.Begin()
	defer tx.Rollback()

	now := global.APP_DB.NowFunc()
	if err := tx.Model(&record).Updates(map[string]interface{}{
		"status":        status,
		"reviewed_by":   reviewerID,
		"reviewed_at":   now,
		"reject_reason": rejectReason,
	}).Error; err != nil {
		return err
	}

	// 审核通过时更新用户实名状态
	if approved {
		if err := tx.Table("users").Where("id = ?", record.UserID).
			Update("real_name_verified", true).Error; err != nil {
			return err
		}
	}

	return tx.Commit().Error
}

// Request types

type SubmitKYCRequest struct {
	RealName string `json:"realName" binding:"required,max=100"`
	IDNumber string `json:"idNumber" binding:"required,max=50"`
}
