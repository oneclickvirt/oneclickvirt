package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	mathRand "math/rand"
	"oneclickvirt/service/database"
	"oneclickvirt/utils/messaging"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/common"
	userModel "oneclickvirt/model/user"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

func (s *AuthService) SendVerifyCode(codeType, target, captchaId, captcha string) error {
	// 先检查图形验证码格式，但不消费
	authValidationService := AuthValidationService{}
	if authValidationService.ShouldCheckCaptcha() {
		if captchaId == "" || captcha == "" {
			return common.NewError(common.CodeCaptchaRequired, "请填写验证码")
		}
	}

	// 检查对应的通信渠道是否启用
	switch codeType {
	case "email":
		if !global.GetAppConfig().Auth.EnableEmail {
			return common.NewError(common.CodeInvalidParam, "邮箱登录未启用")
		}
	case "telegram":
		if !global.GetAppConfig().Auth.EnableTelegram {
			return common.NewError(common.CodeInvalidParam, "Telegram登录未启用")
		}
	case "qq":
		if !global.GetAppConfig().Auth.EnableQQ {
			return common.NewError(common.CodeInvalidParam, "QQ登录未启用")
		}
	default:
		return errors.New("不支持的验证码类型")
	}

	// 所有检查通过后，验证并消费图形验证码
	if authValidationService.ShouldCheckCaptcha() {
		if err := s.verifyCaptcha(captchaId, captcha); err != nil {
			return common.NewError(common.CodeCaptchaInvalid, err.Error())
		}
	}

	// 生成6位数字验证码
	code := generateRandomCode()
	expiresAt := time.Now().Add(5 * time.Minute)

	verifyCode := userModel.VerifyCode{
		Code:      code,
		Type:      codeType,
		Target:    target,
		ExpiresAt: expiresAt,
		Used:      false,
	}

	// 删除该目标之前未使用的验证码
	dbService := database.GetDatabaseService()
	if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		// 删除旧验证码
		if err := tx.Where("target = ? AND type = ? AND used = ?", target, codeType, false).Delete(&userModel.VerifyCode{}).Error; err != nil {
			return err
		}
		// 创建新验证码
		return tx.Create(&verifyCode).Error
	}); err != nil {
		return err
	}

	// 根据类型发送验证码
	switch codeType {
	case "email":
		return s.sendEmailCode(target, code)
	case "telegram":
		return s.sendTelegramCode(target, code)
	case "qq":
		return s.sendQQCode(target, code)
	default:
		return errors.New("不支持的验证码类型")
	}
}

// verifyCode 验证验证码
func (s *AuthService) verifyCode(codeType, target, code string) error {
	var verifyCode userModel.VerifyCode

	// 查找匹配的验证码
	err := global.APP_DB.Where("target = ? AND type = ? AND code = ? AND used = ? AND expires_at > ?",
		target, codeType, code, false, time.Now()).First(&verifyCode).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.NewError(common.CodeInvalidParam, "验证码错误或已过期")
		}
		return err
	}

	// 标记验证码为已使用
	dbService := database.GetDatabaseService()
	if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Model(&verifyCode).Update("used", true).Error
	}); err != nil {
		return err
	}

	return nil
}

func (s *AuthService) sendEmailCode(email, code string) error {
	// 检查邮箱配置是否可用
	if !s.isEmailConfigured() {
		global.APP_LOG.Warn("邮箱服务未配置，无法发送验证码",
			zap.String("email", email),
			zap.String("operation", "send_email_verify_code"))
		return errors.New("邮箱服务未配置，请联系管理员")
	}

	subject := "登录验证码"
	body := fmt.Sprintf("您的登录验证码是：<strong>%s</strong><br><br>验证码5分钟内有效，请勿泄露给他人。", code)
	return s.sendEmail(email, subject, body)
}

func (s *AuthService) sendTelegramCode(telegram, code string) error {
	config := global.GetAppConfig().Auth

	// 检查Telegram是否启用
	if !config.EnableTelegram {
		return errors.New("Telegram登录未启用")
	}

	// 检查Bot Token是否配置
	if config.TelegramBotToken == "" {
		return errors.New("Telegram Bot Token未配置")
	}

	global.APP_LOG.Debug("发送验证码到Telegram",
		zap.String("telegram", telegram),
		zap.String("operation", "send_telegram_verify_code"))

	// 在非生产环境下直接返回成功并记录验证码
	if global.GetAppConfig().System.Env != "production" {
		global.APP_LOG.Debug("非生产环境模拟发送Telegram验证码",
			zap.String("telegram", telegram))
		return nil
	}

	// 构造消息内容
	message := fmt.Sprintf("您的登录验证码是：<b>%s</b>\n验证码5分钟内有效，请勿泄露给他人。", code)

	if err := messaging.SendTelegramMessage(config.TelegramBotToken, telegram, message); err != nil {
		global.APP_LOG.Error("发送Telegram验证码失败",
			zap.String("telegram", telegram),
			zap.Error(err))
		return fmt.Errorf("Telegram消息发送失败: %w", err)
	}

	return nil
}

func (s *AuthService) sendQQCode(qq, code string) error {
	config := global.GetAppConfig().Auth

	// 检查QQ是否启用
	if !config.EnableQQ {
		return errors.New("QQ登录未启用")
	}

	// 检查QQ配置是否完整
	if config.QQAppID == "" || config.QQAppKey == "" {
		return errors.New("QQ应用配置不完整")
	}

	global.APP_LOG.Debug("发送验证码到QQ",
		zap.String("qq", qq),
		zap.String("operation", "send_qq_verify_code"))

	// 在非生产环境下直接返回成功并记录验证码
	if global.GetAppConfig().System.Env != "production" {
		global.APP_LOG.Debug("非生产环境模拟发送QQ验证码",
			zap.String("qq", qq))
		return nil
	}

	// QQ官方Bot API需要企业认证，这里通过配置的Webhook转发
	// 用户可配置一个接收QQ消息的HTTP Webhook地址（如go-cqhttp、NoneBot等）
	// 当QQAppID格式为 "http://" 或 "https://" 开头时，视为Webhook地址
	message := fmt.Sprintf("您的登录验证码是：%s\n验证码5分钟内有效，请勿泄露给他人。", code)

	global.APP_LOG.Warn("QQ消息发送：QQ官方Bot API需要企业资质认证，建议通过邮箱或Telegram发送验证码",
		zap.String("qq", qq))

	// 如果未配置可用的QQ消息通道，返回友好错误
	_ = message // keep for future webhook implementation
	return fmt.Errorf("QQ验证码发送未配置有效的消息通道（QQ号: %s, 验证码: 已生成）。建议使用邮箱或Telegram接收验证码", qq)
}

func (s *AuthService) sendSMSCode(phone, code string) error {
	global.APP_LOG.Debug("发送验证码到手机",
		zap.String("phone", phone),
		zap.String("operation", "send_verification_code"))

	// 在非生产环境下直接返回成功
	if global.GetAppConfig().System.Env != "production" {
		global.APP_LOG.Debug("非生产环境模拟短信验证码发送成功", zap.String("phone", phone))
		return nil
	}

	global.APP_LOG.Warn("短信验证码服务未配置",
		zap.String("phone", phone))
	return errors.New("短信验证码服务未配置，请使用邮箱、Telegram或QQ验证码")
}

func (s *AuthService) sendEmail(to, subject, body string) error {
	config := global.GetAppConfig().Auth
	if config.EmailSMTPHost == "" {
		return common.NewError(common.CodeError, "邮件服务未配置，请联系管理员配置 SMTP 邮件服务")
	}
	return messaging.SendEmail(
		config.EmailSMTPHost, config.EmailSMTPPort,
		config.EmailUsername, config.EmailPassword,
		to, subject, body,
	)
}

func generateRandomCode() string {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		rng := mathRand.New(mathRand.NewSource(time.Now().UnixNano()))
		return fmt.Sprintf("%06d", rng.Intn(1000000))
	}
	return fmt.Sprintf("%06d", n.Int64())
}
