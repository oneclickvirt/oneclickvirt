package auth

import (
	"context"
	"errors"
	"fmt"
	"oneclickvirt/service/database"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/auth"
	"oneclickvirt/model/common"
	userModel "oneclickvirt/model/user"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func (s *AuthService) ForgotPassword(req auth.ForgotPasswordRequest) error {
	// 先检查验证码格式，但不消费
	authValidationService := AuthValidationService{}
	if authValidationService.ShouldCheckCaptcha() {
		if req.CaptchaId == "" || req.Captcha == "" {
			return common.NewError(common.CodeCaptchaRequired, "请填写验证码")
		}
	}

	// 查询用户
	var user userModel.User
	query := global.APP_DB.Where("email = ?", req.Email)
	if req.UserType != "" {
		query = query.Where("user_type = ?", req.UserType)
	}
	if err := query.First(&user).Error; err != nil {
		return errors.New("未找到该邮箱对应的用户")
	}

	// 用户存在，现在验证并消费验证码
	if authValidationService.ShouldCheckCaptcha() {
		if err := s.verifyCaptcha(req.CaptchaId, req.Captcha); err != nil {
			return common.NewError(common.CodeCaptchaInvalid, err.Error())
		}
	}

	// 生成重置令牌
	resetToken := GenerateRandomString(32)
	// 保存重置令牌
	passwordReset := userModel.PasswordReset{
		UserUUID:  user.UUID,
		Token:     resetToken,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	dbService := database.GetDatabaseService()
	if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Create(&passwordReset).Error
	}); err != nil {
		return err
	}
	// 发送重置邮件（开发环境下只模拟发送）
	if global.APP_CONFIG.System.Env == "development" {
		global.APP_LOG.Info("开发环境：模拟发送密码重置邮件",
			zap.String("email", req.Email),
			zap.String("token", resetToken))
		return nil
	}
	resetURL := fmt.Sprintf("http://localhost:3000/reset-password?token=%s", resetToken)
	emailBody := fmt.Sprintf("请点击以下链接重置密码：<br><a href='%s'>重置密码</a><br>链接有效期为24小时。", resetURL)
	return s.sendEmail(req.Email, "密码重置", emailBody)
}

func (s *AuthService) ResetPassword(token, newPassword string) error {
	var passwordReset userModel.PasswordReset
	err := global.APP_DB.Where("token = ? AND expires_at > ?", token, time.Now()).First(&passwordReset).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("重置链接无效或已过期")
		}
		return err
	}

	// 获取用户信息进行密码强度验证
	var user userModel.User
	if err := global.APP_DB.Where("uuid = ?", passwordReset.UserUUID).First(&user).Error; err != nil {
		return err
	}

	// 密码强度验证
	if err := utils.ValidatePasswordStrength(newPassword, utils.DefaultPasswordPolicy, user.Username); err != nil {
		return err
	}

	// 加密新密码
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	// 更新密码
	if err := global.APP_DB.Where("uuid = ?", passwordReset.UserUUID).First(&user).Error; err != nil {
		return err
	}
	user.Password = string(hashedPassword)
	dbService := database.GetDatabaseService()
	if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Save(&user).Error
	}); err != nil {
		return err
	}
	// 删除重置记录
	dbService = database.GetDatabaseService()
	dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Delete(&passwordReset).Error
	})
	return nil
}

// ResetPasswordWithToken 使用令牌重置密码（自动生成新密码并发送到用户通信渠道）
func (s *AuthService) ResetPasswordWithToken(token string) error {
	var passwordReset userModel.PasswordReset
	err := global.APP_DB.Where("token = ? AND expires_at > ?", token, time.Now()).First(&passwordReset).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("重置链接无效或已过期")
		}
		return err
	}

	// 获取用户信息
	var user userModel.User
	if err := global.APP_DB.Where("uuid = ?", passwordReset.UserUUID).First(&user).Error; err != nil {
		return err
	}

	// 生成强密码（12位）
	newPassword := utils.GenerateStrongPassword(12)

	// 密码强度验证（确保生成的密码符合策略）
	if err := utils.ValidatePasswordStrength(newPassword, utils.DefaultPasswordPolicy, user.Username); err != nil {
		return err
	}

	// 加密新密码
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	// 更新密码
	user.Password = string(hashedPassword)
	dbService := database.GetDatabaseService()
	if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Save(&user).Error
	}); err != nil {
		return err
	}

	// 发送新密码到用户绑定的通信渠道
	if err := s.sendPasswordToUser(&user, newPassword); err != nil {
		// 记录日志但不阻止密码重置完成
		global.APP_LOG.Error("发送新密码失败",
			zap.String("user_uuid", user.UUID),
			zap.String("username", user.Username),
			zap.Error(err))
		// 删除重置记录
		dbService := database.GetDatabaseService()
		dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
			return tx.Delete(&passwordReset).Error
		})
		return errors.New("密码重置成功，但发送新密码到通信渠道失败，请联系管理员")
	}

	// 删除重置记录
	dbService = database.GetDatabaseService()
	dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Delete(&passwordReset).Error
	})
	return nil
}

// sendPasswordToUser 发送新密码到用户绑定的通信渠道
func (s *AuthService) sendPasswordToUser(user *userModel.User, newPassword string) error {
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
func (s *AuthService) sendPasswordByEmail(email, username, newPassword string) error {
	// 检查邮箱配置是否可用
	if !s.isEmailConfigured() {
		global.APP_LOG.Warn("邮箱服务未配置，跳过发送",
			zap.String("email", email),
			zap.String("username", username),
			zap.String("operation", "password_reset_by_token"))
		return nil
	}

	global.APP_LOG.Info("发送新密码到邮箱",
		zap.String("email", email),
		zap.String("username", username),
		zap.String("operation", "password_reset_by_token"))

	// 实际实现中应该调用邮件服务
	subject := "密码重置成功"
	body := fmt.Sprintf("您好 %s，<br><br>您的新密码是：<strong>%s</strong><br><br>请妥善保管并尽快登录修改密码。", username, newPassword)
	return s.sendEmail(email, subject, body)
}

// sendPasswordByTelegram 通过Telegram发送新密码
func (s *AuthService) sendPasswordByTelegram(telegram, username, newPassword string) error {
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
		zap.String("operation", "password_reset_by_token"))

	// 在开发环境下直接返回成功
	if global.APP_CONFIG.System.Env == "development" {
		global.APP_LOG.Info("开发环境模拟发送成功")
		return nil
	}

	// 构造消息内容
	message := fmt.Sprintf("用户 %s 的新密码：%s\n请及时登录并修改密码。", username, newPassword)

	// 这里应该调用Telegram Bot API发送消息
	// 可以使用 go-telegram-bot-api 包
	// 示例实现：
	// bot, err := tgbotapi.NewBotAPI(config.TelegramBotToken)
	// if err != nil {
	//     return fmt.Errorf("创建Telegram Bot失败: %v", err)
	// }
	//
	// chatID, err := strconv.ParseInt(telegram, 10, 64)
	// if err != nil {
	//     return fmt.Errorf("无效的Telegram Chat ID: %v", err)
	// }
	//
	// msg := tgbotapi.NewMessage(chatID, message)
	// _, err = bot.Send(msg)
	// return err

	// 暂时返回未实现错误，但保留完整的配置检查逻辑
	global.APP_LOG.Warn("Telegram Bot API集成待实现",
		zap.String("message", message),
		zap.String("chatId", telegram))
	return errors.New("Telegram Bot API集成待实现，请安装并配置 go-telegram-bot-api 包")
}

// sendPasswordByQQ 通过QQ发送新密码
func (s *AuthService) sendPasswordByQQ(qq, username, newPassword string) error {
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
		zap.String("operation", "password_reset_by_token"))

	// 在开发环境下直接返回成功
	if global.APP_CONFIG.System.Env == "development" {
		global.APP_LOG.Info("开发环境模拟发送成功")
		return nil
	}

	// 构造消息内容
	message := fmt.Sprintf("用户 %s 的新密码：%s\n请及时登录并修改密码。", username, newPassword)

	// 这里应该调用QQ机器人API发送消息
	// 可以使用QQ官方的OpenAPI或第三方SDK
	// 示例实现：
	// qqBot := qqapi.NewBot(config.QQAppID, config.QQAppKey)
	// err := qqBot.SendPrivateMessage(qq, message)
	// return err

	// 暂时返回未实现错误，但保留完整的配置检查逻辑
	global.APP_LOG.Warn("QQ机器人API集成待实现",
		zap.String("message", message),
		zap.String("qqNumber", qq))
	return errors.New("QQ机器人API集成待实现，请安装并配置相应的QQ SDK")
}

// sendPasswordBySMS 通过短信发送新密码
func (s *AuthService) sendPasswordBySMS(phone, username, newPassword string) error {
	global.APP_LOG.Info("发送新密码到手机",
		zap.String("phone", phone),
		zap.String("username", username),
		zap.String("operation", "password_reset_by_token"))

	// 在开发环境下直接返回成功
	if global.APP_CONFIG.System.Env == "development" {
		global.APP_LOG.Info("开发环境模拟发送成功")
		return nil
	}

	// 构造短信内容
	message := fmt.Sprintf("用户 %s 的新密码：%s，请及时登录并修改密码。", username, newPassword)

	// 这里应该调用短信服务商API
	// 可以集成阿里云、腾讯云、华为云等短信服务
	// 示例实现：
	// smsClient := sms.NewClient(config.SMSAccessKey, config.SMSSecretKey)
	// err := smsClient.SendSMS(phone, message, config.SMSTemplateID)
	// return err

	// 暂时返回未实现错误，但保留完整的日志记录
	global.APP_LOG.Warn("短信服务API集成待实现",
		zap.String("message", message),
		zap.String("phone", phone))
	return errors.New("短信服务API集成待实现，请配置短信服务商（如阿里云、腾讯云等）")
}

// ChangePassword 修改密码
func (s *AuthService) ChangePassword(userID uint, oldPassword, newPassword string) error {
	var user userModel.User
	if err := global.APP_DB.First(&user, userID).Error; err != nil {
		return errors.New("用户不存在")
	}
	// 验证旧密码
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(oldPassword)); err != nil {
		return errors.New("原密码错误")
	}
	// 加密新密码
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	// 更新密码
	return global.APP_DB.Model(&user).Update("password", string(hashedPassword)).Error
}
