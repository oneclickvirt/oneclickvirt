package notification

import (
	"errors"
	"fmt"

	"oneclickvirt/global"
	userModel "oneclickvirt/model/user"
	"oneclickvirt/utils"
	"oneclickvirt/utils/messaging"

	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

// Service 处理用户密码重置和通知相关功能
type Service struct{}

// NewService 创建通知服务
func NewService() *Service {
	return &Service{}
}

// ResetPassword 用户重置自己的密码
func (s *Service) ResetPassword(userID uint) (string, error) {
	var user userModel.User
	if err := global.APP_DB.First(&user, userID).Error; err != nil {
		return "", errors.New("用户不存在")
	}

	// 生成强密码（12位）
	newPassword := utils.GenerateStrongPassword(12)

	// 密码强度验证（确保生成的密码符合策略）
	if err := utils.ValidatePasswordStrength(newPassword, utils.DefaultPasswordPolicy, user.Username); err != nil {
		return "", err
	}

	// 加密新密码
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}

	// 更新密码
	if err := global.APP_DB.Model(&user).Update("password", string(hashedPassword)).Error; err != nil {
		return "", err
	}

	return newPassword, nil
}

// ResetPasswordAndNotify 用户重置自己的密码并通过通信渠道发送
func (s *Service) ResetPasswordAndNotify(userID uint) (string, error) {
	var user userModel.User
	if err := global.APP_DB.First(&user, userID).Error; err != nil {
		return "", errors.New("用户不存在")
	}

	// 生成强密码（12位）
	newPassword := utils.GenerateStrongPassword(12)

	// 密码强度验证（确保生成的密码符合策略）
	if err := utils.ValidatePasswordStrength(newPassword, utils.DefaultPasswordPolicy, user.Username); err != nil {
		return "", err
	}

	// 加密新密码
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}

	// 更新密码
	if err := global.APP_DB.Model(&user).Update("password", string(hashedPassword)).Error; err != nil {
		return "", err
	}

	// 发送新密码到用户绑定的通信渠道
	if err := s.sendPasswordToUser(&user, newPassword); err != nil {
		// 记录日志但不阻止密码重置完成
		global.APP_LOG.Warn("发送新密码失败",
			zap.Uint("user_id", userID),
			zap.String("username", user.Username),
			zap.Error(err))
		// 仍然返回新密码，但提示发送失败
		return newPassword, errors.New("密码重置成功，但发送新密码到通信渠道失败，请联系管理员")
	}

	return newPassword, nil
}

// sendPasswordToUser 发送新密码到用户绑定的通信渠道
func (s *Service) sendPasswordToUser(user *userModel.User, newPassword string) error {
	// Try all available channels in order: Email > Telegram > QQ > SMS

	if user.Email != "" {
		if err := s.sendPasswordByEmail(user.Email, user.Username, newPassword); err == nil {
			return nil
		} else {
			global.APP_LOG.Warn("邮箱发送失败，尝试下一渠道", zap.Error(err))
		}
	}

	if user.Telegram != "" {
		if err := s.sendPasswordByTelegram(user.Telegram, user.Username, newPassword); err == nil {
			return nil
		} else {
			global.APP_LOG.Warn("Telegram发送失败，尝试下一渠道", zap.Error(err))
		}
	}

	if user.QQ != "" {
		if err := s.sendPasswordByQQ(user.QQ, user.Username, newPassword); err == nil {
			return nil
		} else {
			global.APP_LOG.Warn("QQ发送失败，尝试下一渠道", zap.Error(err))
		}
	}

	if user.Phone != "" {
		if err := s.sendPasswordBySMS(user.Phone, user.Username, newPassword); err == nil {
			return nil
		} else {
			global.APP_LOG.Warn("短信发送失败", zap.Error(err))
		}
	}

	return errors.New("所有通信渠道均不可用或发送失败")
}

// sendPasswordByEmail 通过邮箱发送新密码
func (s *Service) sendPasswordByEmail(email, username, newPassword string) error {
	config := global.GetAppConfig().Auth

	if !config.EnableEmail {
		return errors.New("邮箱服务未启用")
	}
	if config.EmailSMTPHost == "" {
		return errors.New("邮箱SMTP配置不完整")
	}

	global.APP_LOG.Debug("发送新密码到邮箱",
		zap.String("email", email),
		zap.String("username", username),
		zap.String("operation", "password_reset"))

	if global.GetAppConfig().System.Env == "development" {
		global.APP_LOG.Debug("开发环境模拟发送成功")
		return nil
	}

	subject := "密码重置通知"
	body := fmt.Sprintf(`<p>尊敬的用户 <b>%s</b>：</p>
<p>您的密码已被重置，新密码为：<b>%s</b></p>
<p>请使用新密码登录系统，并建议您尽快修改密码。</p>
<p>系统自动发送，请勿回复。</p>`, username, newPassword)

	return messaging.SendEmail(
		config.EmailSMTPHost, config.EmailSMTPPort,
		config.EmailUsername, config.EmailPassword,
		email, subject, body,
	)
}

// sendPasswordByTelegram 通过Telegram发送新密码
func (s *Service) sendPasswordByTelegram(telegram, username, newPassword string) error {
	config := global.GetAppConfig().Auth

	if !config.EnableTelegram {
		return errors.New("Telegram通知服务未启用")
	}
	if config.TelegramBotToken == "" {
		return errors.New("Telegram Bot Token未配置")
	}

	global.APP_LOG.Debug("发送新密码到Telegram",
		zap.String("telegram", telegram),
		zap.String("username", username),
		zap.String("operation", "password_reset"))

	if global.GetAppConfig().System.Env == "development" {
		global.APP_LOG.Debug("开发环境模拟发送成功")
		return nil
	}

	message := fmt.Sprintf("用户 <b>%s</b> 的新密码：<code>%s</code>\n请及时登录并修改密码。", username, newPassword)
	return messaging.SendTelegramMessage(config.TelegramBotToken, telegram, message)
}

// sendPasswordByQQ 通过QQ发送新密码
func (s *Service) sendPasswordByQQ(qq, username, newPassword string) error {
	config := global.GetAppConfig().Auth

	if !config.EnableQQ {
		return errors.New("QQ通知服务未启用")
	}
	if config.QQAppID == "" || config.QQAppKey == "" {
		return errors.New("QQ应用配置不完整")
	}

	global.APP_LOG.Debug("发送新密码到QQ",
		zap.String("qq", qq),
		zap.String("username", username),
		zap.String("operation", "password_reset"))

	if global.GetAppConfig().System.Env == "development" {
		global.APP_LOG.Debug("开发环境模拟发送成功")
		return nil
	}

	global.APP_LOG.Warn("QQ消息发送：QQ官方Bot API需要企业资质认证，建议通过邮箱或Telegram发送",
		zap.String("qq", qq))
	return fmt.Errorf("QQ消息通道未配置有效的发送方式，建议使用邮箱或Telegram")
}

// sendPasswordBySMS 通过短信发送新密码
func (s *Service) sendPasswordBySMS(phone, username, newPassword string) error {
	global.APP_LOG.Debug("发送新密码到手机",
		zap.String("phone", phone),
		zap.String("username", username),
		zap.String("operation", "password_reset"))

	if global.GetAppConfig().System.Env == "development" {
		global.APP_LOG.Debug("开发环境模拟发送成功")
		return nil
	}

	global.APP_LOG.Warn("短信服务需要配置短信服务商SDK（如阿里云、腾讯云）",
		zap.String("phone", phone))
	return fmt.Errorf("短信服务未配置，建议使用邮箱或Telegram接收密码重置通知")
}
