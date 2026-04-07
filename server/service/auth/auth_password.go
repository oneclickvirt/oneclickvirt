package auth

import (
	"context"
	"errors"
	"fmt"
	"oneclickvirt/service/database"
	"oneclickvirt/utils/messaging"
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
	// 发送重置邮件（非生产环境下只模拟发送）
	if global.GetAppConfig().System.Env != "production" {
		global.APP_LOG.Debug("非生产环境：模拟发送密码重置邮件",
			zap.String("email", req.Email),
			zap.String("token", resetToken))
		return nil
	}
	frontendURL := global.GetAppConfig().System.FrontendURL
	if frontendURL == "" {
		frontendURL = "http://localhost:3000"
	}
	resetURL := fmt.Sprintf("%s/reset-password?token=%s", frontendURL, resetToken)
	emailBody := fmt.Sprintf("请点击以下链接重置密码：<br><a href='%s'>重置密码</a><br>链接有效期为24小时。", resetURL)
	return s.sendEmail(req.Email, "密码重置", emailBody)
}

func (s *AuthService) ResetPassword(token, newPassword string) error {
	// 获取用户信息（在事务外做密码强度验证，避免长事务）
	var passwordReset userModel.PasswordReset
	err := global.APP_DB.Where("token = ? AND expires_at > ?", token, time.Now()).First(&passwordReset).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("重置链接无效或已过期")
		}
		return err
	}

	var user userModel.User
	if err := global.APP_DB.Where("uuid = ?", passwordReset.UserUUID).First(&user).Error; err != nil {
		return err
	}

	// 密码强度验证（事务外，避免长事务）
	if err := utils.ValidatePasswordStrength(newPassword, utils.DefaultPasswordPolicy, user.Username); err != nil {
		return err
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	// 原子性事务：检查token有效性 + 删除token + 更新密码
	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		// 在事务中再次验证并删除token（原子性防止并发重用）
		result := tx.Where("token = ? AND expires_at > ?", token, time.Now()).Delete(&userModel.PasswordReset{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errors.New("重置链接无效或已过期")
		}
		return tx.Model(&userModel.User{}).Where("uuid = ?", passwordReset.UserUUID).UpdateColumn("password", string(hashedPassword)).Error
	})
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

	// 生成强密码（12位）（事务外执行，避免长事务）
	newPassword := utils.GenerateStrongPassword(12)
	if err := utils.ValidatePasswordStrength(newPassword, utils.DefaultPasswordPolicy, user.Username); err != nil {
		return err
	}
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	// 原子性事务：删除token + 更新密码
	dbService := database.GetDatabaseService()
	if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		result := tx.Where("token = ? AND expires_at > ?", token, time.Now()).Delete(&userModel.PasswordReset{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errors.New("重置链接无效或已过期")
		}
		return tx.Model(&userModel.User{}).Where("uuid = ?", passwordReset.UserUUID).UpdateColumn("password", string(hashedPassword)).Error
	}); err != nil {
		return err
	}

	// 发送新密码到用户绑定的通信渠道
	if err := s.sendPasswordToUser(&user, newPassword); err != nil {
		global.APP_LOG.Error("发送新密码失败（密码已重置）",
			zap.String("user_uuid", user.UUID),
			zap.String("username", user.Username),
			zap.Error(err))
		return errors.New("密码重置成功，但发送新密码到通信渠道失败，请联系管理员")
	}
	return nil
}

// sendPasswordToUser 发送新密码到用户绑定的通信渠道
func (s *AuthService) sendPasswordToUser(user *userModel.User, newPassword string) error {
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
func (s *AuthService) sendPasswordByEmail(email, username, newPassword string) error {
	if !s.isEmailConfigured() {
		return fmt.Errorf("邮箱服务未配置")
	}

	global.APP_LOG.Debug("发送新密码到邮箱",
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
	config := global.GetAppConfig().Auth

	if !config.EnableTelegram || config.TelegramBotToken == "" {
		return fmt.Errorf("Telegram未配置")
	}

	if global.GetAppConfig().System.Env != "production" {
		global.APP_LOG.Debug("非生产环境模拟发送成功")
		return nil
	}

	message := fmt.Sprintf("您的密码已重置。\n用户名：<b>%s</b>\n新密码：<code>%s</code>\n请及时登录并修改密码。", username, newPassword)
	return messaging.SendTelegramMessage(config.TelegramBotToken, telegram, message)
}

// sendPasswordByQQ 通过QQ发送新密码
func (s *AuthService) sendPasswordByQQ(qq, username, newPassword string) error {
	config := global.GetAppConfig().Auth

	if !config.EnableQQ || config.QQAppID == "" {
		return fmt.Errorf("QQ未配置")
	}

	if global.GetAppConfig().System.Env != "production" {
		global.APP_LOG.Debug("非生产环境模拟发送成功")
		return nil
	}

	global.APP_LOG.Warn("QQ消息通道暂不可用，建议使用邮箱或Telegram", zap.String("qq", qq))
	return fmt.Errorf("QQ消息通道暂不可用")
}

// sendPasswordBySMS 通过短信发送新密码
func (s *AuthService) sendPasswordBySMS(phone, username, newPassword string) error {
	if global.GetAppConfig().System.Env != "production" {
		global.APP_LOG.Debug("非生产环境模拟发送成功")
		return nil
	}

	return fmt.Errorf("短信服务未配置")
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
