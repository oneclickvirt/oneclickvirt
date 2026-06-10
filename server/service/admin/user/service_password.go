package user

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"

	"oneclickvirt/config"
	"oneclickvirt/global"
	"oneclickvirt/model/admin"
	"oneclickvirt/model/common"
	userModel "oneclickvirt/model/user"
	"oneclickvirt/utils"
	"oneclickvirt/utils/messaging"

	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// ResetUserPassword 管理员强制重置用户密码
func (s *Service) ResetUserPassword(userID uint) (string, error) {
	// 获取用户信息
	var user userModel.User
	if err := global.APP_DB.First(&user, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", common.NewError(common.CodeUserNotFound, "用户不存在")
		}
		return "", err
	}

	// 生成强密码（12位）
	newPassword := utils.GenerateStrongPassword(12)

	// 管理员重置密码使用放宽的策略（不要求特殊字符，因为生成的密码仅包含字母和数字）
	adminResetPolicy := utils.PasswordStrengthConfig{
		MinLength:        8,     // 最小8位
		RequireUpperCase: true,  // 要求大写字母
		RequireLowerCase: true,  // 要求小写字母
		RequireDigit:     true,  // 要求数字
		RequireSpecial:   false, // 不要求特殊字符（管理员生成的密码）
		ForbidCommon:     true,  // 禁止常见弱密码
		ForbidPersonal:   true,  // 禁止包含个人信息
	}

	// 密码强度验证（确保生成的密码符合策略）
	if err := utils.ValidatePasswordStrength(newPassword, adminResetPolicy, user.Username); err != nil {
		return "", common.NewError(common.CodeValidationError, err.Error())
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

	// 记录操作日志
	global.APP_LOG.Info("管理员重置用户密码",
		zap.Uint("target_user_id", userID),
		zap.String("target_username", user.Username),
	)

	return newPassword, nil
}

// ResetUserPasswordAndNotify 管理员重置用户密码并发送到用户通信渠道
func (s *Service) ResetUserPasswordAndNotify(userID uint) error {
	// 获取用户信息
	var user userModel.User
	if err := global.APP_DB.First(&user, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.NewError(common.CodeUserNotFound, "用户不存在")
		}
		return err
	}

	// 生成强密码（12位）
	newPassword := utils.GenerateStrongPassword(12)

	// 密码强度验证（确保生成的密码符合策略）
	if err := utils.ValidatePasswordStrength(newPassword, utils.DefaultPasswordPolicy, user.Username); err != nil {
		return common.NewError(common.CodeValidationError, err.Error())
	}

	// 加密新密码
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	// 更新密码
	if err := global.APP_DB.Model(&user).Update("password", string(hashedPassword)).Error; err != nil {
		return err
	}

	// 发送新密码到用户绑定的通信渠道
	if err := s.sendPasswordToUser(&user, newPassword); err != nil {
		// 记录日志但不阻止密码重置完成
		global.APP_LOG.Error("发送新密码失败",
			zap.Uint("user_id", userID),
			zap.String("username", user.Username),
			zap.Error(err))
		return errors.New("密码重置成功，但发送新密码到通信渠道失败，请联系管理员")
	}

	// 记录操作日志
	global.APP_LOG.Info("管理员重置用户密码并发送到通信渠道",
		zap.Uint("target_user_id", userID),
		zap.String("target_username", user.Username),
	)

	return nil
}

// sendPasswordToUser 发送新密码到用户绑定的通信渠道
func (s *Service) sendPasswordToUser(user *userModel.User, newPassword string) error {
	// Try all available channels in order of priority: Email > Telegram > QQ > SMS
	// If a channel is not configured or sending fails, try the next one.

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
	if !s.isEmailConfigured() {
		return fmt.Errorf("邮箱服务未配置")
	}

	// 构建邮件内容
	subject := "密码重置通知"
	body := fmt.Sprintf(`
尊敬的用户 %s：

您的密码已由管理员重置，新密码为：%s

请使用新密码登录系统，并建议您尽快修改密码。

系统自动发送，请勿回复。
`, username, newPassword)

	// 发送邮件
	err := s.sendEmail(email, subject, body)
	if err != nil {
		global.APP_LOG.Error("发送密码重置邮件失败",
			zap.String("email", email),
			zap.String("username", username),
			zap.Error(err))
		return fmt.Errorf("邮件发送失败: %w", err)
	}

	global.APP_LOG.Info("管理员操作：成功发送新密码到邮箱",
		zap.String("email", email),
		zap.String("username", username))
	return nil
}

// sendPasswordByTelegram 通过Telegram发送新密码
func (s *Service) sendPasswordByTelegram(telegram, username, newPassword string) error {
	config := global.GetAppConfig().Auth
	if !config.EnableTelegram || config.TelegramBotToken == "" {
		global.APP_LOG.Debug("Telegram未配置，跳过发送",
			zap.String("telegram", telegram))
		return nil
	}
	if global.GetAppConfig().System.Env != "production" {
		global.APP_LOG.Debug("非生产环境模拟发送Telegram密码通知")
		return nil
	}
	message := fmt.Sprintf("管理员已重置您的密码。\n用户名：<b>%s</b>\n新密码：<code>%s</code>\n请及时登录并修改密码。", username, newPassword)
	return messaging.SendTelegramMessage(config.TelegramBotToken, telegram, message)
}

// sendPasswordByQQ 通过QQ发送新密码
func (s *Service) sendPasswordByQQ(qq, username, newPassword string) error {
	config := global.GetAppConfig().Auth
	if !config.EnableQQ || config.QQAppID == "" {
		global.APP_LOG.Debug("QQ未配置，跳过发送", zap.String("qq", qq))
		return nil
	}
	if global.GetAppConfig().System.Env != "production" {
		global.APP_LOG.Debug("非生产环境模拟发送QQ密码通知")
		return nil
	}
	global.APP_LOG.Warn("QQ消息通道暂不可用，建议使用邮箱或Telegram", zap.String("qq", qq))
	return nil
}

// sendPasswordBySMS 通过短信发送新密码
func (s *Service) sendPasswordBySMS(phone, username, newPassword string) error {
	if global.GetAppConfig().System.Env != "production" {
		global.APP_LOG.Debug("非生产环境模拟发送短信密码通知")
		return nil
	}
	global.APP_LOG.Warn("短信服务未配置，跳过发送", zap.String("phone", phone))
	return nil
}

// generateRandomPassword 生成随机密码（仅包含数字和大小写英文字母，长度不低于8位）
func (s *Service) generateRandomPassword(length int) string {
	if length < 8 {
		length = 8
	}
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	password := make([]byte, length)
	for i := range password {
		num, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		password[i] = charset[num.Int64()]
	}
	return string(password)
}

// syncSingleUserResourceLimits 同步单个用户的资源限制
func (s *Service) syncSingleUserResourceLimits(level int, userID uint) error {
	// 获取等级配置
	levelConfig, exists := global.GetAppConfig().Quota.LevelLimits[level]
	if !exists {
		global.APP_LOG.Warn("等级配置不存在，使用默认配置", zap.Int("level", level))
		// 使用内置默认配置
		if defaultConfig, ok := config.DefaultLevelLimitInfo(level); ok {
			levelConfig = defaultConfig
		} else if defaultConfig, ok := config.DefaultLevelLimitInfo(global.GetAppConfig().Quota.DefaultLevel); ok {
			levelConfig = defaultConfig
		}
	}
	levelConfig = config.NormalizeLevelLimitInfo(level, levelConfig)

	// 构建更新数据 - 不再自动设置 total_traffic
	updateData := map[string]interface{}{
		"max_instances": levelConfig.MaxInstances,
	}

	// 从 MaxResources 中提取各项资源限制
	if levelConfig.MaxResources != nil {
		if cpu, ok := levelConfig.MaxResources["cpu"].(int); ok {
			updateData["max_cpu"] = cpu
		} else if cpu, ok := levelConfig.MaxResources["cpu"].(float64); ok {
			updateData["max_cpu"] = int(cpu)
		}

		if memory, ok := levelConfig.MaxResources["memory"].(int); ok {
			updateData["max_memory"] = memory
		} else if memory, ok := levelConfig.MaxResources["memory"].(float64); ok {
			updateData["max_memory"] = int(memory)
		}

		if disk, ok := levelConfig.MaxResources["disk"].(int); ok {
			updateData["max_disk"] = disk
		} else if disk, ok := levelConfig.MaxResources["disk"].(float64); ok {
			updateData["max_disk"] = int(disk)
		}

		if bandwidth, ok := levelConfig.MaxResources["bandwidth"].(int); ok {
			updateData["max_bandwidth"] = bandwidth
		} else if bandwidth, ok := levelConfig.MaxResources["bandwidth"].(float64); ok {
			updateData["max_bandwidth"] = int(bandwidth)
		}
	}

	// 更新用户资源限制
	if err := global.APP_DB.Table("users").
		Where("id = ?", userID).
		Updates(updateData).Error; err != nil {
		return err
	}

	global.APP_LOG.Debug("用户资源限制已同步",
		zap.Uint("userID", userID),
		zap.Int("level", level),
		zap.Any("updateData", updateData))

	return nil
}

// isEmailConfigured 检查邮箱配置是否可用
func (s *Service) isEmailConfigured() bool {
	// 检查系统配置中是否配置了邮箱服务
	var emailConfig admin.SystemConfig
	if err := global.APP_DB.Where("key = ?", "email_enabled").First(&emailConfig).Error; err != nil {
		return false
	}
	return emailConfig.Value == "true"
}

// sendEmail 发送邮件的基础函数
func (s *Service) sendEmail(to, subject, body string) error {
	authConfig := global.GetAppConfig().Auth
	if authConfig.EmailSMTPHost == "" {
		return fmt.Errorf("SMTP未配置，无法发送邮件")
	}
	return messaging.SendEmail(
		authConfig.EmailSMTPHost,
		authConfig.EmailSMTPPort,
		authConfig.EmailUsername,
		authConfig.EmailPassword,
		to, subject, body,
	)
}

// getCurrentAdminID 获取当前管理员ID
// 在实际实现中，这应该从HTTP请求上下文中获取
func (s *Service) getCurrentAdminID() uint {
	// 目前返回0表示系统操作
	// 实际实现中应该从JWT token或session中获取管理员ID
	return 0
}
