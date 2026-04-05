package checkin

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
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
		"captcha_site_key":    req.CaptchaSiteKey,
		"captcha_secret_key":  req.CaptchaSecretKey,
		"pow_difficulty":      req.PowDifficulty,
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
	}
	var instance InstanceInfo
	dbResult := global.APP_DB.Table("instances").
		Select("provider_id").
		Where("id = ? AND user_id = ?", instanceID, userID).
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
		ExpireAt   *time.Time
	}
	var instance InstanceInfo
	result := global.APP_DB.Table("instances").
		Select("id, provider_id, expire_at").
		Where("id = ? AND user_id = ?", instanceID, userID).
		Take(&instance)
	if result.Error != nil {
		return fmt.Errorf("实例不存在或不属于您")
	}

	config, err := s.GetCheckinConfig(instance.ProviderID)
	if err != nil || !config.Enabled {
		return fmt.Errorf("该服务商未启用签到续期")
	}

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

	now := time.Now()
	oldExpireAt := now
	if instance.ExpireAt != nil {
		oldExpireAt = *instance.ExpireAt
	}
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
	defer tx.Rollback()

	if err := tx.Table("instances").Where("id = ?", instanceID).
		Update("expire_at", newExpireAt).Error; err != nil {
		tx.Rollback()
		return err
	}

	record := &checkinModel.CheckinRecord{
		UserID:      userID,
		InstanceID:  instanceID,
		ProviderID:  instance.ProviderID,
		Method:      config.CheckinMethod,
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
		zap.String("method", config.CheckinMethod),
		zap.Int("renewalDays", config.RenewalDays))

	return nil
}

func (s *Service) verifyCaptchaCode(userID, instanceID uint, code string) error {
	if code == "" {
		return fmt.Errorf("验证码不能为空")
	}
	var verification checkinModel.CheckinVerification
	err := global.APP_DB.Where(
		"user_id = ? AND instance_id = ? AND code = ? AND used = ? AND expired_at > ?",
		userID, instanceID, code, false, time.Now(),
	).First(&verification).Error
	if err != nil {
		return fmt.Errorf("验证码无效或已过期")
	}
	global.APP_DB.Model(&verification).Update("used", true)
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
	client := &http.Client{Timeout: 10 * time.Second}
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

	var verification checkinModel.CheckinVerification
	err := global.APP_DB.Where(
		"user_id = ? AND instance_id = ? AND method = ? AND code = ? AND used = ? AND expired_at > ?",
		userID, instanceID, "pow", challenge, false, time.Now(),
	).First(&verification).Error
	if err != nil {
		return fmt.Errorf("PoW challenge无效或已过期")
	}

	hash := sha256.Sum256([]byte(challenge + nonce))
	hashHex := hex.EncodeToString(hash[:])
	prefix := strings.Repeat("0", difficulty)
	if !strings.HasPrefix(hashHex, prefix) {
		return fmt.Errorf("PoW验证失败：哈希值不满足难度要求")
	}

	global.APP_DB.Model(&verification).Update("used", true)
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
	CheckinMethod     string `json:"checkinMethod" binding:"omitempty,oneof=captcha turnstile recaptcha hcaptcha pow"`
	CaptchaSiteKey    string `json:"captchaSiteKey"`
	CaptchaSecretKey  string `json:"captchaSecretKey"`
	PowDifficulty     int    `json:"powDifficulty"`
}

type DoCheckinRequest struct {
	InstanceID uint   `json:"instanceId" binding:"required"`
	Code       string `json:"code"`      // 内置验证码
	Token      string `json:"token"`     // 第三方captcha token (turnstile/recaptcha/hcaptcha)
	Challenge  string `json:"challenge"` // PoW challenge
	Nonce      string `json:"nonce"`     // PoW nonce
}
