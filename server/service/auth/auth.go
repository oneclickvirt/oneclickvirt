package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	mathRand "math/rand"
	"net/smtp"
	"oneclickvirt/service/database"
	"time"

	"oneclickvirt/config"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/model/auth"
	"oneclickvirt/model/common"
	"oneclickvirt/model/system"
	userModel "oneclickvirt/model/user"
	"oneclickvirt/utils"

	"github.com/mojocn/base64Captcha"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type AuthService struct{}

func (s *AuthService) Login(req auth.LoginRequest) (*userModel.User, string, error) {
	// 根据登录类型调用不同的登录逻辑
	loginType := req.LoginType
	if loginType == "" {
		loginType = "username" // 默认使用用户名密码登录
	}

	switch loginType {
	case "username":
		return s.loginWithPassword(req)
	case "email":
		return s.loginWithEmailCode(req)
	case "telegram":
		return s.loginWithTelegramCode(req)
	case "qq":
		return s.loginWithQQCode(req)
	default:
		return nil, "", common.NewError(common.CodeInvalidParam, "不支持的登录类型")
	}
}

// loginWithPassword 用户名密码登录
func (s *AuthService) loginWithPassword(req auth.LoginRequest) (*userModel.User, string, error) {
	// 验证图形验证码（开发模式下可以跳过）
	if req.CaptchaId != "" && req.Captcha != "" {
		if err := s.verifyCaptcha(req.CaptchaId, req.Captcha); err != nil {
			return nil, "", common.NewError(common.CodeCaptchaInvalid)
		}
	} else if global.APP_CONFIG.System.Env != "development" {
		// 只在非开发环境下强制要求验证码
		return nil, "", common.NewError(common.CodeCaptchaRequired)
	}

	// 检查必要参数
	if req.Username == "" || req.Password == "" {
		return nil, "", common.NewError(common.CodeInvalidParam, "用户名和密码不能为空")
	}

	// 先查询用户是否存在
	var user userModel.User
	if err := global.APP_DB.Where("username = ?", req.Username).First(&user).Error; err != nil {
		global.APP_LOG.Debug("用户登录失败", zap.String("username", utils.SanitizeUserInput(req.Username)), zap.String("error", "record not found"))
		return nil, "", common.NewError(common.CodeInvalidCredentials)
	}

	// 检查用户状态
	if user.Status != 1 {
		global.APP_LOG.Warn("禁用用户尝试登录", zap.String("username", utils.SanitizeUserInput(req.Username)), zap.Int("status", user.Status))
		return nil, "", common.NewError(common.CodeUserDisabled)
	}

	// 验证密码
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		global.APP_LOG.Debug("用户密码验证失败", zap.String("username", utils.SanitizeUserInput(req.Username)), zap.String("userType", user.UserType))
		return nil, "", common.NewError(common.CodeInvalidCredentials)
	}

	global.APP_LOG.Info("用户登录成功", zap.String("username", user.Username), zap.String("userType", user.UserType), zap.Uint("userID", user.ID))

	// 生成JWT令牌
	token, err := utils.GenerateToken(user.ID, user.Username, user.UserType)
	if err != nil {
		global.APP_LOG.Error("生成JWT令牌失败", zap.Error(err))
		return nil, "", errors.New("登录失败，请稍后重试")
	}
	// 更新最后登录时间
	global.APP_DB.Model(&user).Update("last_login_at", time.Now())
	return &user, token, nil
}

// loginWithEmailCode 邮箱验证码登录
func (s *AuthService) loginWithEmailCode(req auth.LoginRequest) (*userModel.User, string, error) {
	// 检查邮箱登录是否启用
	if !global.APP_CONFIG.Auth.EnableEmail {
		return nil, "", common.NewError(common.CodeInvalidParam, "邮箱登录未启用")
	}

	// 检查必要参数
	if req.Target == "" || req.VerifyCode == "" {
		return nil, "", common.NewError(common.CodeInvalidParam, "邮箱地址和验证码不能为空")
	}

	// 验证验证码
	if err := s.verifyCode("email", req.Target, req.VerifyCode); err != nil {
		return nil, "", err
	}

	// 查找用户
	var user userModel.User
	if err := global.APP_DB.Where("email = ?", req.Target).First(&user).Error; err != nil {
		global.APP_LOG.Debug("邮箱登录失败", zap.String("email", req.Target), zap.String("error", "record not found"))
		return nil, "", common.NewError(common.CodeInvalidCredentials, "该邮箱未绑定任何账号")
	}

	// 检查用户状态
	if user.Status != 1 {
		global.APP_LOG.Warn("禁用用户尝试登录", zap.String("email", req.Target), zap.Int("status", user.Status))
		return nil, "", common.NewError(common.CodeUserDisabled)
	}

	global.APP_LOG.Info("用户邮箱登录成功", zap.String("email", req.Target), zap.String("username", user.Username), zap.Uint("userID", user.ID))

	// 生成JWT令牌
	token, err := utils.GenerateToken(user.ID, user.Username, user.UserType)
	if err != nil {
		global.APP_LOG.Error("生成JWT令牌失败", zap.Error(err))
		return nil, "", errors.New("登录失败，请稍后重试")
	}
	// 更新最后登录时间
	global.APP_DB.Model(&user).Update("last_login_at", time.Now())
	return &user, token, nil
}

// loginWithTelegramCode Telegram验证码登录
func (s *AuthService) loginWithTelegramCode(req auth.LoginRequest) (*userModel.User, string, error) {
	// 检查Telegram登录是否启用
	if !global.APP_CONFIG.Auth.EnableTelegram {
		return nil, "", common.NewError(common.CodeInvalidParam, "Telegram登录未启用")
	}

	// 检查必要参数
	if req.Target == "" || req.VerifyCode == "" {
		return nil, "", common.NewError(common.CodeInvalidParam, "Telegram用户名和验证码不能为空")
	}

	// 验证验证码
	if err := s.verifyCode("telegram", req.Target, req.VerifyCode); err != nil {
		return nil, "", err
	}

	// 查找用户
	var user userModel.User
	if err := global.APP_DB.Where("telegram = ?", req.Target).First(&user).Error; err != nil {
		global.APP_LOG.Debug("Telegram登录失败", zap.String("telegram", req.Target), zap.String("error", "record not found"))
		return nil, "", common.NewError(common.CodeInvalidCredentials, "该Telegram账号未绑定任何账号")
	}

	// 检查用户状态
	if user.Status != 1 {
		global.APP_LOG.Warn("禁用用户尝试登录", zap.String("telegram", req.Target), zap.Int("status", user.Status))
		return nil, "", common.NewError(common.CodeUserDisabled)
	}

	global.APP_LOG.Info("用户Telegram登录成功", zap.String("telegram", req.Target), zap.String("username", user.Username), zap.Uint("userID", user.ID))

	// 生成JWT令牌
	token, err := utils.GenerateToken(user.ID, user.Username, user.UserType)
	if err != nil {
		global.APP_LOG.Error("生成JWT令牌失败", zap.Error(err))
		return nil, "", errors.New("登录失败，请稍后重试")
	}
	// 更新最后登录时间
	global.APP_DB.Model(&user).Update("last_login_at", time.Now())
	return &user, token, nil
}

// loginWithQQCode QQ验证码登录
func (s *AuthService) loginWithQQCode(req auth.LoginRequest) (*userModel.User, string, error) {
	// 检查QQ登录是否启用
	if !global.APP_CONFIG.Auth.EnableQQ {
		return nil, "", common.NewError(common.CodeInvalidParam, "QQ登录未启用")
	}

	// 检查必要参数
	if req.Target == "" || req.VerifyCode == "" {
		return nil, "", common.NewError(common.CodeInvalidParam, "QQ号和验证码不能为空")
	}

	// 验证验证码
	if err := s.verifyCode("qq", req.Target, req.VerifyCode); err != nil {
		return nil, "", err
	}

	// 查找用户
	var user userModel.User
	if err := global.APP_DB.Where("qq = ?", req.Target).First(&user).Error; err != nil {
		global.APP_LOG.Debug("QQ登录失败", zap.String("qq", req.Target), zap.String("error", "record not found"))
		return nil, "", common.NewError(common.CodeInvalidCredentials, "该QQ号未绑定任何账号")
	}

	// 检查用户状态
	if user.Status != 1 {
		global.APP_LOG.Warn("禁用用户尝试登录", zap.String("qq", req.Target), zap.Int("status", user.Status))
		return nil, "", common.NewError(common.CodeUserDisabled)
	}

	global.APP_LOG.Info("用户QQ登录成功", zap.String("qq", req.Target), zap.String("username", user.Username), zap.Uint("userID", user.ID))

	// 生成JWT令牌
	token, err := utils.GenerateToken(user.ID, user.Username, user.UserType)
	if err != nil {
		global.APP_LOG.Error("生成JWT令牌失败", zap.Error(err))
		return nil, "", errors.New("登录失败，请稍后重试")
	}
	// 更新最后登录时间
	global.APP_DB.Model(&user).Update("last_login_at", time.Now())
	return &user, token, nil
}

func (s *AuthService) RegisterWithContext(req auth.RegisterRequest, ip string, userAgent string) error {
	// 检查注册是否启用
	enableRegistration := global.APP_CONFIG.Auth.EnablePublicRegistration
	if !enableRegistration && !global.APP_CONFIG.InviteCode.Enabled {
		return errors.New("注册功能已被禁用")
	}

	// 验证码验证已经在 API 层通过 ValidateCaptchaRequired 完成
	// 这里不再重复验证，避免验证码被重复消费

	// 邀请码验证逻辑
	// 如果启用邀请码系统且未启用公开注册，则必须要邀请码
	if global.APP_CONFIG.InviteCode.Enabled && !global.APP_CONFIG.Auth.EnablePublicRegistration {
		if req.InviteCode == "" {
			return common.NewError(common.CodeInvalidParam, "邀请码不能为空")
		}
	} else if req.InviteCode == "" && !enableRegistration {
		// 如果没有邀请码且公开注册未启用，则禁止注册
		return errors.New("注册功能已被禁用")
	}

	// 密码强度验证（仅在非初始化场景下执行）
	if err := utils.ValidatePasswordStrength(req.Password, utils.DefaultPasswordPolicy, req.Username); err != nil {
		return err
	}

	// 检查用户名是否已存在（排除已软删除的用户）
	var existingUser userModel.User
	if err := global.APP_DB.Unscoped().Where("username = ? AND deleted_at IS NULL", req.Username).First(&existingUser).Error; err == nil {
		return common.NewError(common.CodeUsernameExists, "用户名已存在")
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	user := userModel.User{
		Username: req.Username,
		Password: string(hashedPassword),
		Nickname: req.Nickname,
		Email:    req.Email,
		Phone:    req.Phone,
		Telegram: req.Telegram,
		QQ:       req.QQ,
		UserType: "user",
		Level:    global.APP_CONFIG.Quota.DefaultLevel,
		Status:   1, // 默认状态为正常
		// 资源限制将在创建后通过同步服务自动设置
		// UsedTraffic字段已删除，流量数据从pmacct_traffic_records实时查询
		TotalTraffic:   0, // 默认为0，不自动设置流量限制，只有当用户实例所在Provider启用流量统计时才设置
		TrafficLimited: false,
	}

	// 设置流量重置时间为下个月1号
	now := time.Now()
	resetTime := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location())
	user.TrafficResetAt = &resetTime

	// 根据全局配置设置用户过期时间
	levelLimits := global.APP_CONFIG.Quota.LevelLimits
	if levelLimit, exists := levelLimits[user.Level]; exists && levelLimit.ExpiryDays > 0 {
		// 如果配置了该等级的过期天数，设置过期时间
		expiryTime := now.AddDate(0, 0, levelLimit.ExpiryDays)
		user.ExpiresAt = &expiryTime
		user.IsManualExpiry = false // 标记为非手动设置
		global.APP_LOG.Info("为新注册用户设置过期时间",
			zap.String("username", req.Username),
			zap.Int("level", user.Level),
			zap.Int("expiry_days", levelLimit.ExpiryDays),
			zap.Time("expires_at", expiryTime))
	}

	// 使用数据库抽象层进行事务处理
	dbService := database.GetDatabaseService()
	transactionErr := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		if err := tx.Create(&user).Error; err != nil {
			return err
		}

		// 为新用户分配默认角色（普通用户角色）
		var defaultRole auth.Role
		if err := tx.Where("name = ?", "普通用户").First(&defaultRole).Error; err != nil {
			// 如果找不到普通用户角色，则创建一个
			defaultRole = auth.Role{
				Name:        "普通用户",
				Description: "普通用户角色，拥有基础权限",
				Code:        "user",
				Status:      1,
			}
			if createErr := tx.Create(&defaultRole).Error; createErr != nil {
				return errors.New("创建默认用户角色失败")
			}
		}

		// 创建用户角色关联
		userRole := userModel.UserRole{
			UserID: user.ID,
			RoleID: defaultRole.ID,
		}
		if err := tx.Create(&userRole).Error; err != nil {
			return errors.New("分配默认角色失败")
		}

		// 如果使用了邀请码，记录使用情况（只在注册成功时）
		if req.InviteCode != "" {
			if err := s.useInviteCodeWithTx(tx, req.InviteCode, ip, userAgent); err != nil {
				return err
			}
		}

		// 提交事务前完成所有创建操作
		return nil
	})

	// 事务成功后，同步用户资源限制到对应等级配置
	if transactionErr == nil {
		// 使用延迟函数避免循环导入，在用户创建后同步资源限制
		go func() {
			if syncErr := syncNewUserResourceLimits(user.Level, user.ID); syncErr != nil {
				// 用户已创建成功，资源同步失败只记录日志，不影响注册流程
				global.APP_LOG.Error("新用户资源限制同步失败",
					zap.Uint("userID", user.ID),
					zap.Int("level", user.Level),
					zap.Error(syncErr))
			} else {
				global.APP_LOG.Info("新用户资源限制同步成功",
					zap.Uint("userID", user.ID),
					zap.Int("level", user.Level))
			}
		}()
	}

	return transactionErr
}

// RegisterAndLogin 注册并自动登录
func (s *AuthService) RegisterAndLogin(req auth.RegisterRequest, ip string, userAgent string) (*userModel.User, string, error) {
	// 先执行注册
	if err := s.RegisterWithContext(req, ip, userAgent); err != nil {
		return nil, "", err
	}
	// 注册成功后自动登录
	loginReq := auth.LoginRequest{
		Username:  req.Username,
		Password:  req.Password,
		LoginType: "username",
		UserType:  "user",
	}
	return s.Login(loginReq)
}

func (s *AuthService) SendVerifyCode(codeType, target string) error {
	// 检查对应的通信渠道是否启用
	switch codeType {
	case "email":
		if !global.APP_CONFIG.Auth.EnableEmail {
			return common.NewError(common.CodeInvalidParam, "邮箱登录未启用")
		}
	case "telegram":
		if !global.APP_CONFIG.Auth.EnableTelegram {
			return common.NewError(common.CodeInvalidParam, "Telegram登录未启用")
		}
	case "qq":
		if !global.APP_CONFIG.Auth.EnableQQ {
			return common.NewError(common.CodeInvalidParam, "QQ登录未启用")
		}
	default:
		return errors.New("不支持的验证码类型")
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

func (s *AuthService) ForgotPassword(req auth.ForgotPasswordRequest) error {
	// 验证验证码（开发模式下可以跳过）
	if global.APP_CONFIG.System.Env != "development" {
		if err := s.verifyCaptcha(req.CaptchaId, req.Captcha); err != nil {
			return err
		}
	} else {
		// 开发环境下，如果提供了验证码就验证，没提供就跳过
		if req.CaptchaId != "" && req.Captcha != "" {
			if err := s.verifyCaptcha(req.CaptchaId, req.Captcha); err != nil {
				return err
			}
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
	config := global.APP_CONFIG.Auth

	// 检查Telegram是否启用
	if !config.EnableTelegram {
		return errors.New("Telegram登录未启用")
	}

	// 检查Bot Token是否配置
	if config.TelegramBotToken == "" {
		return errors.New("Telegram Bot Token未配置")
	}

	global.APP_LOG.Info("发送验证码到Telegram",
		zap.String("telegram", telegram),
		zap.String("operation", "send_telegram_verify_code"))

	// 在开发环境下直接返回成功并记录验证码
	if global.APP_CONFIG.System.Env == "development" {
		global.APP_LOG.Info("开发环境模拟发送Telegram验证码",
			zap.String("telegram", telegram),
			zap.String("code", code))
		return nil
	}

	// 构造消息内容
	message := fmt.Sprintf("您的登录验证码是：%s\n验证码5分钟内有效，请勿泄露给他人。", code)

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

func (s *AuthService) sendQQCode(qq, code string) error {
	config := global.APP_CONFIG.Auth

	// 检查QQ是否启用
	if !config.EnableQQ {
		return errors.New("QQ登录未启用")
	}

	// 检查QQ配置是否完整
	if config.QQAppID == "" || config.QQAppKey == "" {
		return errors.New("QQ应用配置不完整")
	}

	global.APP_LOG.Info("发送验证码到QQ",
		zap.String("qq", qq),
		zap.String("operation", "send_qq_verify_code"))

	// 在开发环境下直接返回成功并记录验证码
	if global.APP_CONFIG.System.Env == "development" {
		global.APP_LOG.Info("开发环境模拟发送QQ验证码",
			zap.String("qq", qq),
			zap.String("code", code))
		return nil
	}

	// 构造消息内容
	message := fmt.Sprintf("您的登录验证码是：%s\n验证码5分钟内有效，请勿泄露给他人。", code)

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

func (s *AuthService) sendSMSCode(phone, code string) error {
	global.APP_LOG.Info("发送验证码到手机",
		zap.String("phone", phone),
		zap.String("operation", "send_verification_code"))

	// 在开发环境下直接返回成功
	if global.APP_CONFIG.System.Env == "development" {
		global.APP_LOG.Info("开发环境模拟验证码发送成功", zap.String("code", code))
		return nil
	}

	// 构造短信内容
	message := fmt.Sprintf("验证码：%s，5分钟内有效，请勿泄露。", code)

	// 这里应该调用短信服务商API
	// 可以集成阿里云、腾讯云、华为云等短信服务
	// 示例实现：
	// smsClient := sms.NewClient(config.SMSAccessKey, config.SMSSecretKey)
	// err := smsClient.SendSMS(phone, message, config.SMSVerificationTemplateID)
	// return err

	global.APP_LOG.Warn("短信验证码服务API集成待实现",
		zap.String("message", message),
		zap.String("phone", phone))
	return errors.New("短信验证码服务API集成待实现，请配置短信服务商")
}

func (s *AuthService) sendEmail(to, subject, body string) error {
	config := global.APP_CONFIG.Auth
	if config.EmailSMTPHost == "" {
		return errors.New("邮件服务未配置")
	}
	auth := smtp.PlainAuth("", config.EmailUsername, config.EmailPassword, config.EmailSMTPHost)
	msg := fmt.Sprintf("To: %s\r\nSubject: %s\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n%s", to, subject, body)
	return smtp.SendMail(
		fmt.Sprintf("%s:%d", config.EmailSMTPHost, config.EmailSMTPPort),
		auth,
		config.EmailUsername,
		[]string{to},
		[]byte(msg),
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

// GenerateCaptcha 生成图形验证码
func (s *AuthService) GenerateCaptcha(width, height int) (*auth.CaptchaResponse, error) {
	captchaLen := global.APP_CONFIG.Captcha.Length
	if captchaLen <= 0 {
		captchaLen = 4
	}
	// 确保宽度和高度是有效的正整数
	if width <= 0 {
		width = 120
	}
	if height <= 0 {
		height = 40
	}
	// 设置验证码配置难度
	driver := base64Captcha.NewDriverDigit(height, width, captchaLen, 0.4, 40)
	// 使用全局LRU缓存存储
	c := base64Captcha.NewCaptcha(driver, global.APP_CAPTCHA_STORE)
	id, b64s, _, err := c.Generate()
	if err != nil {
		return nil, err
	}
	// 返回验证码信息
	return &auth.CaptchaResponse{
		CaptchaId: id,
		ImageData: b64s,
	}, nil
}

func (s *AuthService) verifyCaptcha(captchaId, code string) error {
	if captchaId == "" || code == "" {
		return errors.New("验证码参数不完整")
	}

	// 开发环境下允许测试验证码
	if global.APP_CONFIG.System.Env == "development" && code == "test" {
		return nil
	}

	// 使用全局LRU缓存验证
	match := global.APP_CAPTCHA_STORE.Verify(captchaId, code, true)
	if !match {
		return errors.New("验证码错误或已过期")
	}
	return nil
}

// verifyInviteCode 验证邀请码
func (s *AuthService) verifyInviteCode(code string) error {
	var inviteCode system.InviteCode
	err := global.APP_DB.Where("code = ? AND status = ?", code, 1).First(&inviteCode).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.NewError(common.CodeInviteCodeInvalid)
		}
		return err
	}
	// 检查使用次数
	if inviteCode.MaxUses > 0 && inviteCode.UsedCount >= inviteCode.MaxUses {
		return common.NewError(common.CodeInviteCodeUsed)
	}
	// 检查过期时间
	if inviteCode.ExpiresAt != nil && inviteCode.ExpiresAt.Before(time.Now()) {
		return common.NewError(common.CodeInviteCodeExpired)
	}
	return nil
}

// useInviteCodeWithTx 使用邀请码（带事务支持）
// 在事务内验证并标记邀请码为已使用，确保原子性
func (s *AuthService) useInviteCodeWithTx(db *gorm.DB, code string, ip string, userAgent string) error {
	var inviteCode system.InviteCode

	// 使用行级锁获取邀请码记录，防止并发使用
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("code = ? AND status = ?", code, 1).
		First(&inviteCode).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.NewError(common.CodeInviteCodeInvalid, "邀请码无效")
		}
		return err
	}

	// 检查过期时间
	if inviteCode.ExpiresAt != nil && inviteCode.ExpiresAt.Before(time.Now()) {
		return common.NewError(common.CodeInviteCodeExpired, "邀请码已过期")
	}

	// 检查使用次数
	if inviteCode.MaxUses > 0 && inviteCode.UsedCount >= inviteCode.MaxUses {
		return common.NewError(common.CodeInviteCodeUsed, "邀请码已被使用")
	}

	// 增加使用次数
	inviteCode.UsedCount++
	// 如果达到最大使用次数，设置为已用完
	if inviteCode.MaxUses > 0 && inviteCode.UsedCount >= inviteCode.MaxUses {
		inviteCode.Status = 0 // 0表示已用完
	}

	// 保存邀请码使用记录
	usage := system.InviteCodeUsage{
		InviteCodeID: inviteCode.ID,
		IP:           ip,
		UserAgent:    userAgent,
		UsedAt:       time.Now(),
	}

	// 使用传入的数据库连接（可能是事务）
	if err := db.Save(&inviteCode).Error; err != nil {
		return err
	}
	if err := db.Create(&usage).Error; err != nil {
		return err
	}
	return nil
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

// 生成随机字符串
func GenerateRandomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		b[i] = charset[n.Int64()]
	}
	return string(b)
}

// InitSystem 初始化系统
func (s *AuthService) InitSystem(adminUsername, adminPassword, adminEmail string) error {
	// 检查是否已经初始化
	var count int64
	global.APP_DB.Model(&userModel.User{}).Count(&count)
	if count > 0 {
		return errors.New("系统已初始化")
	}
	// 创建管理员用户
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(adminPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	admin := userModel.User{
		Username: adminUsername,
		Password: string(hashedPassword),
		Email:    adminEmail,
		UserType: "admin",
		Status:   1,
	}
	// 创建示例用户（默认禁用，防止未授权访问）
	userPassword, _ := bcrypt.GenerateFromPassword([]byte("user123"), bcrypt.DefaultCost)
	user := userModel.User{
		Username: "user",
		Password: string(userPassword),
		Email:    "user@spiritlhl.net",
		UserType: "user",
		Status:   0, // 默认禁用状态，需要管理员手动启用
	}

	// 使用数据库抽象层进行事务处理
	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		if err := tx.Create(&admin).Error; err != nil {
			return err
		}
		if err := tx.Create(&user).Error; err != nil {
			return err
		}
		return nil
	})
}

// UserInfo 用户信息结构体
type UserInfo struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Email    string `json:"email"`
}

// InitSystemWithUsers 使用自定义用户信息初始化系统
func (s *AuthService) InitSystemWithUsers(adminInfo, userInfo UserInfo) error {
	// 检查是否已经初始化
	var count int64
	global.APP_DB.Model(&userModel.User{}).Count(&count)
	if count > 0 {
		return errors.New("系统已初始化")
	}

	// 创建管理员用户
	adminPassword, err := bcrypt.GenerateFromPassword([]byte(adminInfo.Password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	admin := userModel.User{
		Username: adminInfo.Username,
		Password: string(adminPassword),
		Email:    adminInfo.Email,
		UserType: "admin",
		Level:    5, // 管理员等级设置为5（最高等级）
		Status:   1,
	}

	// 创建普通用户（默认禁用，防止未授权访问）
	userPassword, err := bcrypt.GenerateFromPassword([]byte(userInfo.Password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	user := userModel.User{
		Username: userInfo.Username,
		Password: string(userPassword),
		Email:    userInfo.Email,
		UserType: "user",
		Status:   0, // 默认禁用状态，需要管理员手动启用
	}

	// 使用数据库服务处理事务
	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		if err := tx.Create(&admin).Error; err != nil {
			return err
		}
		if err := tx.Create(&user).Error; err != nil {
			return err
		}
		return nil
	})
}

// syncNewUserResourceLimits 同步新用户的资源限制（避免循环导入）
func syncNewUserResourceLimits(level int, userID uint) error {
	// 获取等级配置
	levelConfig, exists := global.APP_CONFIG.Quota.LevelLimits[level]
	if !exists {
		global.APP_LOG.Warn("等级配置不存在，使用默认配置", zap.Int("level", level))
		// 使用默认配置
		levelConfig = config.LevelLimitInfo{
			MaxInstances: 1,
			MaxTraffic:   102400, // 100GB
			MaxResources: map[string]interface{}{
				"cpu":       1,
				"memory":    512,
				"disk":      10240,
				"bandwidth": 100,
			},
		}
	}

	// 构建更新数据 - 不再自动设置 total_traffic，保持为0
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

	global.APP_LOG.Debug("新用户资源限制已同步",
		zap.Uint("userID", userID),
		zap.Int("level", level),
		zap.Any("updateData", updateData))

	return nil
}

// isEmailConfigured 检查邮箱配置是否可用
func (s *AuthService) isEmailConfigured() bool {
	// 检查系统配置中是否配置了邮箱服务
	var emailConfig adminModel.SystemConfig
	if err := global.APP_DB.Where("key = ?", "email_enabled").First(&emailConfig).Error; err != nil {
		return false
	}
	return emailConfig.Value == "true"
}
