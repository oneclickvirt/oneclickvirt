package notification

import (
	"errors"
	"fmt"

	"oneclickvirt/global"
	userModel "oneclickvirt/model/user"
	"oneclickvirt/utils"

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
		global.APP_LOG.Error("发送新密码失败",
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
	// 优先级：邮箱 > Telegram > QQ > 手机号

	if user.Email != "" {
		return s.sendPasswordByEmail(user.Email, user.Username, newPassword)
	}

	if user.Telegram != "" {
		return s.sendPasswordByTelegram(user.Telegram, user.Username, newPassword)
	}

	if user.QQ != "" {
		return s.sendPasswordByQQ(user.QQ, user.Username, newPassword)
	}

	if user.Phone != "" {
		return s.sendPasswordBySMS(user.Phone, user.Username, newPassword)
	}

	return errors.New("用户未绑定任何通信渠道")
}

// sendPasswordByEmail 通过邮箱发送新密码
func (s *Service) sendPasswordByEmail(email, username, newPassword string) error {
	config := global.APP_CONFIG.Auth

	// 检查邮箱是否启用
	if !config.EnableEmail {
		return errors.New("邮箱服务未启用")
	}

	// 检查邮箱配置是否完整
	if config.EmailSMTPHost == "" {
		return errors.New("邮箱SMTP配置不完整")
	}

	global.APP_LOG.Info("发送新密码到邮箱",
		zap.String("email", email),
		zap.String("username", username),
		zap.String("operation", "password_reset"))

	// 在开发环境下直接返回成功
	if global.APP_CONFIG.System.Env == "development" {
		global.APP_LOG.Info("开发环境模拟发送成功")
		return nil
	}

	// 构造邮件内容
	subject := "密码重置通知"
	body := fmt.Sprintf("用户 %s 的新密码：%s\n请及时登录并修改密码。", username, newPassword)

	// 这里应该直接调用邮件发送功能
	// 可以使用 gomail 或其他邮件库
	// 示例实现：
	// m := gomail.NewMessage()
	// m.SetHeader("From", config.EmailUsername)
	// m.SetHeader("To", email)
	// m.SetHeader("Subject", subject)
	// m.SetBody("text/plain", body)
	//
	// d := gomail.NewDialer(config.EmailSMTPHost, config.EmailSMTPPort, config.EmailUsername, config.EmailPassword)
	// return d.DialAndSend(m)

	global.APP_LOG.Warn("邮件发送功能待实现",
		zap.String("subject", subject),
		zap.String("body", body),
		zap.String("email", email))
	return errors.New("邮件发送功能待实现，请安装并配置邮件发送库（如 gomail）")
}

// sendPasswordByTelegram 通过Telegram发送新密码
func (s *Service) sendPasswordByTelegram(telegram, username, newPassword string) error {
	config := global.APP_CONFIG.Auth

	// 检查Telegram是否启用
	if !config.EnableTelegram {
		return errors.New("Telegram通知服务未启用")
	}

	// 检查Bot Token是否配置
	if config.TelegramBotToken == "" {
		return errors.New("Telegram Bot Token未配置")
	}

	global.APP_LOG.Info("发送新密码到Telegram",
		zap.String("telegram", telegram),
		zap.String("username", username),
		zap.String("operation", "password_reset"))

	// 在开发环境下直接返回成功
	if global.APP_CONFIG.System.Env == "development" {
		global.APP_LOG.Info("开发环境模拟发送成功")
		return nil
	}

	// 构造消息内容
	message := fmt.Sprintf("用户 %s 的新密码：%s\n请及时登录并修改密码。", username, newPassword)

	// 这里应该调用Telegram Bot API发送消息
	global.APP_LOG.Warn("Telegram Bot API集成待实现",
		zap.String("message", message),
		zap.String("chatId", telegram))
	return errors.New("Telegram Bot API集成待实现")
}

// sendPasswordByQQ 通过QQ发送新密码
func (s *Service) sendPasswordByQQ(qq, username, newPassword string) error {
	config := global.APP_CONFIG.Auth

	// 检查QQ是否启用
	if !config.EnableQQ {
		return errors.New("QQ通知服务未启用")
	}

	// 检查QQ配置是否完整
	if config.QQAppID == "" || config.QQAppKey == "" {
		return errors.New("QQ应用配置不完整")
	}

	global.APP_LOG.Info("发送新密码到QQ",
		zap.String("qq", qq),
		zap.String("username", username),
		zap.String("operation", "password_reset"))

	// 在开发环境下直接返回成功
	if global.APP_CONFIG.System.Env == "development" {
		global.APP_LOG.Info("开发环境模拟发送成功")
		return nil
	}

	// 构造消息内容
	message := fmt.Sprintf("用户 %s 的新密码：%s\n请及时登录并修改密码。", username, newPassword)

	// 这里应该调用QQ机器人API发送消息
	global.APP_LOG.Warn("QQ机器人API集成待实现",
		zap.String("message", message),
		zap.String("qqNumber", qq))
	return errors.New("QQ机器人API集成待实现")
}

// sendPasswordBySMS 通过短信发送新密码
func (s *Service) sendPasswordBySMS(phone, username, newPassword string) error {
	global.APP_LOG.Info("发送新密码到手机",
		zap.String("phone", phone),
		zap.String("username", username),
		zap.String("operation", "password_reset"))

	// 在开发环境下直接返回成功
	if global.APP_CONFIG.System.Env == "development" {
		global.APP_LOG.Info("开发环境模拟发送成功")
		return nil
	}

	// 构造短信内容
	message := fmt.Sprintf("用户 %s 的新密码：%s，请及时登录并修改密码。", username, newPassword)

	// 这里应该调用短信服务商API
	global.APP_LOG.Warn("短信服务API集成待实现",
		zap.String("message", message),
		zap.String("phone", phone))
	return errors.New("短信服务API集成待实现")
}
