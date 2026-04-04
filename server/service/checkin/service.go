package checkin

import (
	"fmt"
	"math/rand"
	"time"

	"oneclickvirt/global"
	checkinModel "oneclickvirt/model/checkin"

	"go.uber.org/zap"
)

// Service 签到续期服务
type Service struct{}

// GetCheckinConfig 获取签到配置
func (s *Service) GetCheckinConfig(providerID uint) (*checkinModel.CheckinConfig, error) {
	var config checkinModel.CheckinConfig
	err := global.APP_DB.Where("provider_id = ?", providerID).First(&config).Error
	if err != nil {
		return nil, err
	}
	return &config, nil
}

// UpdateCheckinConfig 更新签到配置（管理员）
func (s *Service) UpdateCheckinConfig(providerID uint, req *UpdateCheckinConfigRequest) error {
	var config checkinModel.CheckinConfig
	result := global.APP_DB.Where("provider_id = ?", providerID).First(&config)

	updates := map[string]interface{}{
		"enabled":             req.Enabled,
		"default_expire_days": req.DefaultExpireDays,
		"renewal_days":        req.RenewalDays,
		"max_expire_days":     req.MaxExpireDays,
		"overdue_action":      req.OverdueAction,
		"checkin_method":      req.CheckinMethod,
	}

	if result.RowsAffected == 0 {
		config = checkinModel.CheckinConfig{
			ProviderID:        providerID,
			Enabled:           req.Enabled,
			DefaultExpireDays: req.DefaultExpireDays,
			RenewalDays:       req.RenewalDays,
			MaxExpireDays:     req.MaxExpireDays,
			OverdueAction:     req.OverdueAction,
			CheckinMethod:     req.CheckinMethod,
		}
		return global.APP_DB.Create(&config).Error
	}
	return global.APP_DB.Model(&config).Updates(updates).Error
}

// GenerateVerification 生成验证码（签到时调用）
func (s *Service) GenerateVerification(userID, instanceID uint) (*checkinModel.CheckinVerification, error) {
	code := fmt.Sprintf("%06d", rand.Intn(1000000))
	verification := &checkinModel.CheckinVerification{
		UserID:     userID,
		InstanceID: instanceID,
		Method:     "captcha",
		Code:       code,
		ExpiredAt:  time.Now().Add(5 * time.Minute),
	}
	if err := global.APP_DB.Create(verification).Error; err != nil {
		return nil, err
	}
	return verification, nil
}

// DoCheckin 用户签到续期
func (s *Service) DoCheckin(userID, instanceID uint, code string) error {
	// 验证验证码
	var verification checkinModel.CheckinVerification
	err := global.APP_DB.Where(
		"user_id = ? AND instance_id = ? AND code = ? AND used = ? AND expired_at > ?",
		userID, instanceID, code, false, time.Now(),
	).First(&verification).Error
	if err != nil {
		return fmt.Errorf("验证码无效或已过期")
	}

	// 标记验证码已使用
	global.APP_DB.Model(&verification).Update("used", true)

	// 获取实例及其签到配置
	type InstanceInfo struct {
		ID         uint
		ProviderID uint
		ExpireAt   *time.Time
	}
	var instance InstanceInfo
	err = global.APP_DB.Table("instances").
		Select("id, provider_id, expire_at").
		Where("id = ? AND user_id = ?", instanceID, userID).
		Scan(&instance).Error
	if err != nil {
		return fmt.Errorf("实例不存在或不属于您")
	}

	config, err := s.GetCheckinConfig(instance.ProviderID)
	if err != nil || !config.Enabled {
		return fmt.Errorf("该服务商未启用签到续期")
	}

	// 计算新过期时间
	now := time.Now()
	oldExpireAt := now
	if instance.ExpireAt != nil {
		oldExpireAt = *instance.ExpireAt
	}

	// 如果已过期，从当前时间开始计算
	baseTime := oldExpireAt
	if baseTime.Before(now) {
		baseTime = now
	}

	newExpireAt := baseTime.Add(time.Duration(config.RenewalDays) * 24 * time.Hour)
	if config.MaxExpireDays > 0 {
		maxExpireAt := now.Add(time.Duration(config.MaxExpireDays) * 24 * time.Hour)
		if newExpireAt.After(maxExpireAt) {
			newExpireAt = maxExpireAt
		}
	}

	tx := global.APP_DB.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 更新实例过期时间
	if err := tx.Table("instances").Where("id = ?", instanceID).
		Update("expire_at", newExpireAt).Error; err != nil {
		tx.Rollback()
		return err
	}

	// 记录签到
	record := &checkinModel.CheckinRecord{
		UserID:      userID,
		InstanceID:  instanceID,
		ProviderID:  instance.ProviderID,
		Method:      "captcha",
		RenewalDays: config.RenewalDays,
		NewExpireAt: newExpireAt,
		OldExpireAt: &oldExpireAt,
	}
	if err := tx.Create(record).Error; err != nil {
		tx.Rollback()
		return err
	}

	if err := tx.Commit().Error; err != nil {
		return err
	}

	global.APP_LOG.Info("用户签到续期成功",
		zap.Uint("userID", userID),
		zap.Uint("instanceID", instanceID),
		zap.Int("renewalDays", config.RenewalDays))

	return nil
}

// GetCheckinRecords 获取用户签到记录
func (s *Service) GetCheckinRecords(userID uint, page, pageSize int) ([]checkinModel.CheckinRecord, int64, error) {
	var records []checkinModel.CheckinRecord
	var total int64
	query := global.APP_DB.Model(&checkinModel.CheckinRecord{}).Where("user_id = ?", userID)
	query.Count(&total)
	err := query.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&records).Error
	return records, total, err
}

// Request types

type UpdateCheckinConfigRequest struct {
	Enabled           bool   `json:"enabled"`
	DefaultExpireDays int    `json:"defaultExpireDays"`
	RenewalDays       int    `json:"renewalDays"`
	MaxExpireDays     int    `json:"maxExpireDays"`
	OverdueAction     string `json:"overdueAction" binding:"omitempty,oneof=stop delete"`
	CheckinMethod     string `json:"checkinMethod" binding:"omitempty,oneof=captcha"`
}
