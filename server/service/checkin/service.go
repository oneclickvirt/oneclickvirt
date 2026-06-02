package checkin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/url"
	"strings"
	"time"

	"oneclickvirt/global"
	checkinModel "oneclickvirt/model/checkin"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/service/database"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Service 签到续期服务
type Service struct{}

// GetCheckinConfig 获取签到配置
func (s *Service) GetCheckinConfig(providerID uint) (*checkinModel.CheckinConfig, error) {
	if err := s.ensureProviderExists(providerID); err != nil {
		return nil, err
	}
	var config checkinModel.CheckinConfig
	err := global.APP_DB.Where("provider_id = ?", providerID).First(&config).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 返回默认配置
			return defaultCheckinConfig(providerID), nil
		}
		return nil, err
	}
	normalizeCheckinConfig(&config)
	return &config, nil
}

// UpdateCheckinConfig 更新签到配置（管理员）
func (s *Service) UpdateCheckinConfig(providerID uint, req *UpdateCheckinConfigRequest) error {
	if err := s.ensureProviderExists(providerID); err != nil {
		return err
	}
	if req == nil {
		return fmt.Errorf("签到配置不能为空")
	}
	if err := normalizeUpdateRequest(req); err != nil {
		return err
	}

	var config checkinModel.CheckinConfig
	result := global.APP_DB.Where("provider_id = ?", providerID).First(&config)

	updates := map[string]interface{}{
		"enabled":             req.Enabled,
		"default_expire_days": req.DefaultExpireDays,
		"renewal_days":        req.RenewalDays,
		"max_expire_days":     req.MaxExpireDays,
		"overdue_action":      req.OverdueAction,
		"checkin_method":      req.CheckinMethod,
		"captcha_site_key":    req.CaptchaSiteKey,
		"captcha_secret_key":  req.CaptchaSecretKey,
		"pow_difficulty":      req.PowDifficulty,
	}

	if result.Error != nil {
		if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return fmt.Errorf("查询签到配置失败: %w", result.Error)
		}
		config = checkinModel.CheckinConfig{
			ProviderID:        providerID,
			Enabled:           req.Enabled,
			DefaultExpireDays: req.DefaultExpireDays,
			RenewalDays:       req.RenewalDays,
			MaxExpireDays:     req.MaxExpireDays,
			OverdueAction:     req.OverdueAction,
			CheckinMethod:     req.CheckinMethod,
			CaptchaSiteKey:    req.CaptchaSiteKey,
			CaptchaSecretKey:  req.CaptchaSecretKey,
			PowDifficulty:     req.PowDifficulty,
		}
		return global.APP_DB.Create(&config).Error
	}
	return global.APP_DB.Model(&config).Updates(updates).Error
}

// GetCheckinChallenge 获取签到挑战信息（前端呈现验证组件所需数据）
func (s *Service) GetCheckinChallenge(userID, instanceID uint) (map[string]interface{}, error) {
	type InstanceInfo struct {
		ProviderID uint
		IsFrozen   bool
	}
	var instance InstanceInfo
	dbResult := global.APP_DB.Table("instances").
		Select("provider_id, is_frozen").
		Where("id = ? AND user_id = ? AND status NOT IN ?", instanceID, userID, []string{"deleted", "deleting"}).
		Take(&instance)
	if dbResult.Error != nil {
		return nil, fmt.Errorf("实例不存在或不属于您")
	}

	config, err := s.GetCheckinConfig(instance.ProviderID)
	if err != nil || !config.Enabled {
		return nil, fmt.Errorf("该服务商未启用签到续期")
	}

	result := map[string]interface{}{
		"method": config.CheckinMethod,
	}

	switch config.CheckinMethod {
	case "turnstile", "recaptcha", "hcaptcha":
		if strings.TrimSpace(config.CaptchaSiteKey) == "" || strings.TrimSpace(config.CaptchaSecretKey) == "" {
			return nil, fmt.Errorf("该签到方式尚未完整配置")
		}
		result["siteKey"] = config.CaptchaSiteKey
	case "pow":
		challenge, err := s.generatePowChallenge(userID, instanceID, config.PowDifficulty)
		if err != nil {
			return nil, err
		}
		result["challenge"] = challenge
		result["difficulty"] = config.PowDifficulty
	case "captcha":
		verification, err := s.generateCaptchaCode(userID, instanceID)
		if err != nil {
			return nil, err
		}
		result["code"] = verification.Code
		result["expiredAt"] = verification.ExpiredAt
	}

	return result, nil
}

// generateCaptchaCode 生成内置数字验证码
func (s *Service) generateCaptchaCode(userID, instanceID uint) (*checkinModel.CheckinVerification, error) {
	s.cleanupVerificationRecords(userID, instanceID, "captcha")
	n, _ := rand.Int(rand.Reader, big.NewInt(1000000))
	code := fmt.Sprintf("%06d", n.Int64())
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

// generatePowChallenge 生成PoW挑战
func (s *Service) generatePowChallenge(userID, instanceID uint, difficulty int) (string, error) {
	s.cleanupVerificationRecords(userID, instanceID, "pow")
	if difficulty < 1 {
		difficulty = 4
	}
	if difficulty > 8 {
		difficulty = 8
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	challenge := hex.EncodeToString(b)

	verification := &checkinModel.CheckinVerification{
		UserID:     userID,
		InstanceID: instanceID,
		Method:     "pow",
		Code:       challenge,
		ExpiredAt:  time.Now().Add(10 * time.Minute),
	}
	if err := global.APP_DB.Create(verification).Error; err != nil {
		return "", err
	}
	return challenge, nil
}

// DoCheckin 用户签到续期 - 支持多种验证方式
func (s *Service) DoCheckin(userID, instanceID uint, req *DoCheckinRequest) error {
	type InstanceInfo struct {
		ID         uint
		ProviderID uint
		ExpiresAt  *time.Time
	}
	var instance InstanceInfo
	result := global.APP_DB.Table("instances").
		Select("id, provider_id, expires_at").
		Where("id = ? AND user_id = ?", instanceID, userID).
		Take(&instance)
	if result.Error != nil {
		return fmt.Errorf("实例不存在或不属于您")
	}

	config, err := s.GetCheckinConfig(instance.ProviderID)
	if err != nil || !config.Enabled {
		return fmt.Errorf("该服务商未启用签到续期")
	}
	normalizeCheckinConfig(config)

	switch config.CheckinMethod {
	case "captcha":
		if err := s.verifyCaptchaCode(userID, instanceID, req.Code); err != nil {
			return err
		}
	case "turnstile":
		if err := s.verifyTurnstile(config.CaptchaSecretKey, req.Token); err != nil {
			return err
		}
	case "recaptcha":
		if err := s.verifyRecaptcha(config.CaptchaSecretKey, req.Token); err != nil {
			return err
		}
	case "hcaptcha":
		if err := s.verifyHcaptcha(config.CaptchaSecretKey, req.Token); err != nil {
			return err
		}
	case "pow":
		if err := s.verifyPow(userID, instanceID, req.Challenge, req.Nonce, config.PowDifficulty); err != nil {
			return err
		}
	default:
		return fmt.Errorf("不支持的签到方式: %s", config.CheckinMethod)
	}

	dbService := database.GetDatabaseService()
	var oldExpireAt *time.Time
	var newExpireAt time.Time
	if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		var lockedInstance struct {
			ID           uint
			ProviderID   uint
			ExpiresAt    *time.Time
			IsFrozen     bool
			FrozenReason string
		}
		if err := tx.Table("instances").
			Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id, provider_id, expires_at, is_frozen, frozen_reason").
			Where("id = ? AND user_id = ? AND status NOT IN ?", instanceID, userID, []string{"deleted", "deleting"}).
			Take(&lockedInstance).Error; err != nil {
			return fmt.Errorf("实例不存在或不属于您")
		}
		if lockedInstance.ProviderID != config.ProviderID {
			return fmt.Errorf("签到配置与实例所属节点不匹配")
		}

		now := time.Now()
		baseTime := now
		if lockedInstance.ExpiresAt != nil {
			old := *lockedInstance.ExpiresAt
			oldExpireAt = &old
			if old.After(now) {
				baseTime = old
			}
		}

		newExpireAt = baseTime.Add(time.Duration(config.RenewalDays) * 24 * time.Hour)
		if config.MaxExpireDays > 0 {
			maxExpireAt := now.Add(time.Duration(config.MaxExpireDays) * 24 * time.Hour)
			if newExpireAt.After(maxExpireAt) {
				newExpireAt = maxExpireAt
			}
		}
		if !newExpireAt.After(now) {
			return fmt.Errorf("续期配置无效，新的到期时间未晚于当前时间")
		}

		updates := map[string]interface{}{
			"expires_at": newExpireAt,
		}
		if lockedInstance.IsFrozen && lockedInstance.FrozenReason == "expired" {
			updates["is_frozen"] = false
			updates["frozen_reason"] = ""
			updates["frozen_at"] = nil
		}
		if err := tx.Table("instances").Where("id = ?", lockedInstance.ID).Updates(updates).Error; err != nil {
			return err
		}

		record := &checkinModel.CheckinRecord{
			UserID:      userID,
			InstanceID:  lockedInstance.ID,
			ProviderID:  lockedInstance.ProviderID,
			Method:      config.CheckinMethod,
			RenewalDays: config.RenewalDays,
			NewExpireAt: newExpireAt,
			OldExpireAt: oldExpireAt,
		}
		if err := tx.Create(record).Error; err != nil {
			return err
		}

		return nil
	}); err != nil {
		return err
	}

	global.APP_LOG.Info("用户签到续期成功",
		zap.Uint("userID", userID),
		zap.Uint("instanceID", instanceID),
		zap.String("method", config.CheckinMethod),
		zap.Int("renewalDays", config.RenewalDays))

	return nil
}

func (s *Service) verifyCaptchaCode(userID, instanceID uint, code string) error {
	if code == "" {
		return fmt.Errorf("验证码不能为空")
	}
	// 原子操作：查找未使用的验证码并标记为已使用，防止TOCTOU竞争
	result := global.APP_DB.Model(&checkinModel.CheckinVerification{}).
		Where("user_id = ? AND instance_id = ? AND code = ? AND used = ? AND expired_at > ?",
			userID, instanceID, code, false, time.Now()).
		Update("used", true)
	if result.Error != nil {
		return fmt.Errorf("验证码验证失败")
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("验证码无效或已过期")
	}
	return nil
}

func (s *Service) verifyTurnstile(secretKey, token string) error {
	if token == "" {
		return fmt.Errorf("Turnstile token不能为空")
	}
	return s.verifyThirdPartyCaptcha("https://challenges.cloudflare.com/turnstile/v0/siteverify", secretKey, token, "Turnstile")
}

func (s *Service) verifyRecaptcha(secretKey, token string) error {
	if token == "" {
		return fmt.Errorf("reCAPTCHA token不能为空")
	}
	return s.verifyThirdPartyCaptcha("https://www.google.com/recaptcha/api/siteverify", secretKey, token, "reCAPTCHA")
}

func (s *Service) verifyHcaptcha(secretKey, token string) error {
	if token == "" {
		return fmt.Errorf("hCaptcha token不能为空")
	}
	return s.verifyThirdPartyCaptcha("https://hcaptcha.com/siteverify", secretKey, token, "hCaptcha")
}

func (s *Service) verifyThirdPartyCaptcha(verifyURL, secretKey, token, name string) error {
	if strings.TrimSpace(secretKey) == "" {
		return fmt.Errorf("%s未配置服务端密钥", name)
	}
	client := utils.GetHTTPClientWithTimeout(10 * time.Second)
	resp, err := client.PostForm(verifyURL, url.Values{
		"secret":   {secretKey},
		"response": {token},
	})
	if err != nil {
		return fmt.Errorf("%s验证请求失败: %w", name, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("%s验证响应解析失败", name)
	}
	if !result.Success {
		return fmt.Errorf("%s验证失败", name)
	}
	return nil
}

func (s *Service) verifyPow(userID, instanceID uint, challenge, nonce string, difficulty int) error {
	if challenge == "" || nonce == "" {
		return fmt.Errorf("PoW challenge和nonce不能为空")
	}
	if difficulty < 1 {
		difficulty = 4
	}

	// 先验证哈希值满足难度要求
	hash := sha256.Sum256([]byte(challenge + nonce))
	hashHex := hex.EncodeToString(hash[:])
	prefix := strings.Repeat("0", difficulty)
	if !strings.HasPrefix(hashHex, prefix) {
		return fmt.Errorf("PoW验证失败：哈希值不满足难度要求")
	}

	// 原子操作：查找未使用的challenge并标记为已使用，防止TOCTOU竞争
	result := global.APP_DB.Model(&checkinModel.CheckinVerification{}).
		Where("user_id = ? AND instance_id = ? AND method = ? AND code = ? AND used = ? AND expired_at > ?",
			userID, instanceID, "pow", challenge, false, time.Now()).
		Update("used", true)
	if result.Error != nil {
		return fmt.Errorf("PoW验证失败")
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("PoW challenge无效或已过期")
	}

	return nil
}

// GenerateVerification 向后兼容 - 生成内置验证码
func (s *Service) GenerateVerification(userID, instanceID uint) (*checkinModel.CheckinVerification, error) {
	return s.generateCaptchaCode(userID, instanceID)
}

// GetCheckinRecords 获取用户签到记录
func (s *Service) GetCheckinRecords(userID uint, page, pageSize int) ([]checkinModel.CheckinRecord, int64, error) {
	var records []checkinModel.CheckinRecord
	var total int64
	query := global.APP_DB.Model(&checkinModel.CheckinRecord{}).Where("user_id = ?", userID)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&records).Error
	return records, total, err
}

func (s *Service) ensureProviderExists(providerID uint) error {
	if providerID == 0 {
		return fmt.Errorf("无效的Provider ID")
	}
	var count int64
	if err := global.APP_DB.Model(&providerModel.Provider{}).Where("id = ?", providerID).Count(&count).Error; err != nil {
		return fmt.Errorf("查询Provider失败: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("Provider不存在")
	}
	return nil
}

func defaultCheckinConfig(providerID uint) *checkinModel.CheckinConfig {
	return &checkinModel.CheckinConfig{
		ProviderID:        providerID,
		Enabled:           false,
		DefaultExpireDays: 7,
		RenewalDays:       7,
		MaxExpireDays:     30,
		OverdueAction:     "stop",
		CheckinMethod:     "captcha",
		PowDifficulty:     4,
	}
}

func normalizeCheckinConfig(config *checkinModel.CheckinConfig) {
	if config.DefaultExpireDays <= 0 {
		config.DefaultExpireDays = 7
	}
	if config.RenewalDays <= 0 {
		config.RenewalDays = 7
	}
	if config.MaxExpireDays < 0 {
		config.MaxExpireDays = 30
	}
	if config.OverdueAction == "" {
		config.OverdueAction = "stop"
	}
	if config.CheckinMethod == "" {
		config.CheckinMethod = "captcha"
	}
	if config.PowDifficulty < 1 {
		config.PowDifficulty = 4
	}
	if config.PowDifficulty > 8 {
		config.PowDifficulty = 8
	}
}

func normalizeUpdateRequest(req *UpdateCheckinConfigRequest) error {
	if req.DefaultExpireDays <= 0 {
		req.DefaultExpireDays = 7
	}
	if req.RenewalDays <= 0 {
		req.RenewalDays = 7
	}
	if req.MaxExpireDays < 0 {
		return fmt.Errorf("最大到期天数不能为负数")
	}
	if req.MaxExpireDays == 0 {
		req.MaxExpireDays = 30
	}
	if req.OverdueAction == "" {
		req.OverdueAction = "stop"
	}
	if req.OverdueAction != "stop" && req.OverdueAction != "delete" {
		return fmt.Errorf("不支持的过期操作")
	}
	if req.CheckinMethod == "" {
		req.CheckinMethod = "captcha"
	}
	validMethods := map[string]bool{
		"captcha": true, "turnstile": true, "recaptcha": true, "hcaptcha": true, "pow": true,
	}
	if !validMethods[req.CheckinMethod] {
		return fmt.Errorf("不支持的签到方式")
	}
	if req.PowDifficulty < 1 {
		req.PowDifficulty = 4
	}
	if req.PowDifficulty > 8 {
		req.PowDifficulty = 8
	}
	if req.Enabled && (req.CheckinMethod == "turnstile" || req.CheckinMethod == "recaptcha" || req.CheckinMethod == "hcaptcha") {
		if strings.TrimSpace(req.CaptchaSiteKey) == "" || strings.TrimSpace(req.CaptchaSecretKey) == "" {
			return fmt.Errorf("第三方验证码启用时必须配置站点密钥和服务端密钥")
		}
	}
	return nil
}

func (s *Service) cleanupVerificationRecords(userID, instanceID uint, method string) {
	if global.APP_DB == nil {
		return
	}
	cutoff := time.Now().Add(-24 * time.Hour)
	result := global.APP_DB.Unscoped().
		Where("user_id = ? AND instance_id = ? AND method = ? AND (used = ? OR expired_at < ? OR created_at < ?)",
			userID, instanceID, method, true, time.Now(), cutoff).
		Delete(&checkinModel.CheckinVerification{})
	if result.Error != nil && global.APP_LOG != nil {
		global.APP_LOG.Debug("清理签到验证码失败",
			zap.Uint("userID", userID),
			zap.Uint("instanceID", instanceID),
			zap.String("method", method),
			zap.Error(result.Error))
	}
}

// Request types

type UpdateCheckinConfigRequest struct {
	Enabled           bool   `json:"enabled"`
	DefaultExpireDays int    `json:"defaultExpireDays" binding:"omitempty,min=0"`
	RenewalDays       int    `json:"renewalDays" binding:"omitempty,min=0"`
	MaxExpireDays     int    `json:"maxExpireDays" binding:"omitempty,min=0"`
	OverdueAction     string `json:"overdueAction" binding:"omitempty,oneof=stop delete"`
	CheckinMethod     string `json:"checkinMethod" binding:"omitempty,oneof=captcha turnstile recaptcha hcaptcha pow"`
	CaptchaSiteKey    string `json:"captchaSiteKey"`
	CaptchaSecretKey  string `json:"captchaSecretKey"`
	PowDifficulty     int    `json:"powDifficulty"`
}

type DoCheckinRequest struct {
	InstanceID uint   `json:"instanceId" binding:"required"`
	Code       string `json:"code" binding:"omitempty,max=128"`      // 内置验证码
	Token      string `json:"token" binding:"omitempty,max=512"`     // 第三方captcha token (turnstile/recaptcha/hcaptcha)
	Challenge  string `json:"challenge" binding:"omitempty,max=128"` // PoW challenge
	Nonce      string `json:"nonce" binding:"omitempty,max=64"`      // PoW nonce
}
