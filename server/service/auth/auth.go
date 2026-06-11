package auth

import (
	"context"
	"errors"
	"oneclickvirt/service/database"
	"oneclickvirt/service/userquota"
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

type AuthService struct{}

func (s *AuthService) Login(req auth.LoginRequest) (*userModel.User, string, error) {
	return s.LoginWithContext(req, "")
}

func (s *AuthService) LoginWithContext(req auth.LoginRequest, ip string) (*userModel.User, string, error) {
	if global.APP_DB == nil {
		return nil, "", common.NewError(common.CodeDatabaseError, "数据库服务暂时不可用，请稍后重试")
	}

	// 根据登录类型调用不同的登录逻辑
	loginType := req.LoginType
	if loginType == "" {
		loginType = "username" // 默认使用用户名密码登录
	}

	identity := loginThrottleIdentity(req, loginType)
	if err := globalLoginAttemptThrottle.Check(ip, identity); err != nil {
		global.APP_LOG.Warn("登录失败次数过多",
			zap.String("loginType", loginType),
			zap.String("identity", utils.SanitizeUserInput(identity)),
			zap.String("ip", ip))
		return nil, "", err
	}

	var user *userModel.User
	var token string
	var err error
	switch loginType {
	case "username":
		user, token, err = s.loginWithPassword(req)
	case "email":
		user, token, err = s.loginWithEmailCode(req)
	case "telegram":
		user, token, err = s.loginWithTelegramCode(req)
	case "qq":
		user, token, err = s.loginWithQQCode(req)
	default:
		return nil, "", common.NewError(common.CodeInvalidParam, "不支持的登录类型")
	}

	if err != nil {
		globalLoginAttemptThrottle.RecordFailure(ip, identity)
		return nil, "", err
	}
	globalLoginAttemptThrottle.ResetIdentity(ip, identity)
	return user, token, nil
}

// loginWithPassword 用户名密码登录
func (s *AuthService) loginWithPassword(req auth.LoginRequest) (*userModel.User, string, error) {
	// 先检查验证码格式，但不消费
	authValidationService := AuthValidationService{}
	if authValidationService.ShouldCheckCaptcha() {
		if req.CaptchaId == "" || req.Captcha == "" {
			return nil, "", common.NewError(common.CodeCaptchaRequired, "请填写验证码")
		}
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

	// 所有检查通过后，验证并消费验证码
	// 这样可以避免用户名或密码错误时验证码被消费
	if authValidationService.ShouldCheckCaptcha() {
		if err := s.verifyCaptcha(req.CaptchaId, req.Captcha); err != nil {
			return nil, "", common.NewError(common.CodeCaptchaInvalid, err.Error())
		}
	}

	global.APP_LOG.Info("用户登录成功", zap.String("username", user.Username), zap.String("userType", user.UserType), zap.Uint("userID", user.ID))

	// 生成JWT令牌
	token, err := utils.GenerateToken(user.ID, user.Username, user.UserType)
	if err != nil {
		global.APP_LOG.Error("生成JWT令牌失败", zap.Error(err))
		return nil, "", errors.New("登录失败，请稍后重试")
	}
	// 更新最后登录时间
	if err := global.APP_DB.Model(&user).UpdateColumn("last_login_at", time.Now()).Error; err != nil {
		global.APP_LOG.Warn("更新最后登录时间失败", zap.Uint("userID", user.ID), zap.Error(err))
	}
	return &user, token, nil
}

// loginWithEmailCode 邮箱验证码登录
func (s *AuthService) loginWithEmailCode(req auth.LoginRequest) (*userModel.User, string, error) {
	// 检查邮箱登录是否启用
	if !global.GetAppConfig().Auth.EnableEmail {
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
	if err := global.APP_DB.Model(&user).UpdateColumn("last_login_at", time.Now()).Error; err != nil {
		global.APP_LOG.Warn("更新最后登录时间失败", zap.Uint("userID", user.ID), zap.Error(err))
	}
	return &user, token, nil
}

// loginWithTelegramCode Telegram验证码登录
func (s *AuthService) loginWithTelegramCode(req auth.LoginRequest) (*userModel.User, string, error) {
	// 检查Telegram登录是否启用
	if !global.GetAppConfig().Auth.EnableTelegram {
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
	if err := global.APP_DB.Model(&user).UpdateColumn("last_login_at", time.Now()).Error; err != nil {
		global.APP_LOG.Warn("更新最后登录时间失败", zap.Uint("userID", user.ID), zap.Error(err))
	}
	return &user, token, nil
}

// loginWithQQCode QQ验证码登录
func (s *AuthService) loginWithQQCode(req auth.LoginRequest) (*userModel.User, string, error) {
	// 检查QQ登录是否启用
	if !global.GetAppConfig().Auth.EnableQQ {
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
	if err := global.APP_DB.Model(&user).UpdateColumn("last_login_at", time.Now()).Error; err != nil {
		global.APP_LOG.Warn("更新最后登录时间失败", zap.Uint("userID", user.ID), zap.Error(err))
	}
	return &user, token, nil
}

func (s *AuthService) RegisterWithContext(req auth.RegisterRequest, ip string, userAgent string) error {
	// 检查注册是否启用
	enableRegistration := global.GetAppConfig().Auth.EnablePublicRegistration
	if !enableRegistration && !global.GetAppConfig().InviteCode.Enabled {
		return errors.New("注册功能已被禁用")
	}

	if err := utils.ValidateUsername(req.Username); err != nil {
		return common.NewError(common.CodeValidationError, err.Error())
	}

	if err := utils.ValidateOptionalEmail(req.Email); err != nil {
		return common.NewError(common.CodeValidationError, err.Error())
	}

	// 先验证验证码（在所有其他检查之前），但在检查用户名是否存在之后再消费
	// 注意：此时只验证格式，不消费验证码
	authValidationService := AuthValidationService{}
	if authValidationService.ShouldCheckCaptcha() {
		global.APP_LOG.Debug("注册时检查验证码",
			zap.String("username", utils.SanitizeUserInput(req.Username)),
			zap.String("captchaId", req.CaptchaId),
			zap.Bool("captchaProvided", req.Captcha != ""),
			zap.Bool("shouldCheck", authValidationService.ShouldCheckCaptcha()),
			zap.String("env", global.GetAppConfig().System.Env),
			zap.Bool("captchaEnabled", global.GetAppConfig().Captcha.Enabled))
		if req.CaptchaId == "" || req.Captcha == "" {
			global.APP_LOG.Warn("注册验证码参数缺失",
				zap.String("username", utils.SanitizeUserInput(req.Username)),
				zap.String("captchaId", req.CaptchaId),
				zap.Bool("captchaProvided", req.Captcha != ""))
			return common.NewError(common.CodeCaptchaRequired, "请填写验证码")
		}
	} else {
		global.APP_LOG.Debug("注册跳过验证码检查",
			zap.String("username", utils.SanitizeUserInput(req.Username)),
			zap.String("env", global.GetAppConfig().System.Env),
			zap.Bool("captchaEnabled", global.GetAppConfig().Captcha.Enabled))
	}

	// 邀请码验证逻辑
	// 如果启用邀请码系统且未启用公开注册，则必须要邀请码
	if global.GetAppConfig().InviteCode.Enabled && !global.GetAppConfig().Auth.EnablePublicRegistration {
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

	// 优先检查用户名和邮箱是否已存在（排除已软删除的用户）
	// 这样可以在邀请码和验证码验证之前就发现冲突，避免误导用户和浪费资源
	var existingUser userModel.User
	if err := global.APP_DB.Unscoped().Where("username = ? AND deleted_at IS NULL", req.Username).First(&existingUser).Error; err == nil {
		return common.NewError(common.CodeUsernameExists, "用户名已存在")
	}

	// 检查邮箱是否已存在（如果提供了邮箱）
	if req.Email != "" {
		var existingEmailUser userModel.User
		if err := global.APP_DB.Unscoped().Where("email = ? AND email != '' AND deleted_at IS NULL", req.Email).First(&existingEmailUser).Error; err == nil {
			return common.NewError(common.CodeUserExists, "邮箱已被使用")
		}
	}

	// 如果提供了邀请码，提前验证邀请码的有效性（不消费）
	// 这样可以在验证码被消费前就发现邀请码无效的问题
	if req.InviteCode != "" {
		if err := s.validateInviteCodeBeforeUse(req.InviteCode); err != nil {
			return err
		}
	}

	// 用户名检查通过后，验证并消费验证码
	// 这样可以避免用户名已存在时验证码被消费的问题
	if authValidationService.ShouldCheckCaptcha() {
		if err := s.verifyCaptcha(req.CaptchaId, req.Captcha); err != nil {
			return common.NewError(common.CodeCaptchaInvalid, err.Error())
		}
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	user := userModel.User{
		Username:       req.Username,
		Password:       string(hashedPassword),
		Nickname:       req.Nickname,
		Email:          req.Email,
		Phone:          req.Phone,
		Telegram:       req.Telegram,
		QQ:             req.QQ,
		UserType:       "user",
		Level:          defaultConfiguredUserLevel(),
		Status:         1, // 默认状态为正常
		TrafficLimited: false,
	}
	if err := userquota.ApplyLimitFields(&user, user.Level); err != nil {
		return common.NewError(common.CodeValidationError, err.Error())
	}

	// 设置流量重置时间为下个月1号
	now := time.Now()
	resetTime := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location())
	user.TrafficResetAt = &resetTime

	// 根据全局配置设置用户过期时间
	if levelLimit, err := userquota.ResolveLevelLimit(user.Level); err == nil && levelLimit.ExpiryDays > 0 {
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

	return transactionErr
}

// RegisterAndLogin 注册并自动登录
func (s *AuthService) RegisterAndLogin(req auth.RegisterRequest, ip string, userAgent string) (*userModel.User, string, error) {
	// 先执行注册
	if err := s.RegisterWithContext(req, ip, userAgent); err != nil {
		return nil, "", err
	}

	// 注册成功后直接查询用户并生成token，不走登录流程
	// 避免登录时的验证码检查导致注册成功但返回错误
	var user userModel.User
	if err := global.APP_DB.Where("username = ?", req.Username).First(&user).Error; err != nil {
		return nil, "", errors.New("用户查询失败")
	}

	// 生成JWT令牌
	token, err := utils.GenerateToken(user.ID, user.Username, user.UserType)
	if err != nil {
		global.APP_LOG.Error("注册后生成JWT令牌失败", zap.Error(err))
		return nil, "", errors.New("登录失败，请稍后重试")
	}

	// 更新最后登录时间
	global.APP_DB.Model(&user).Update("last_login_at", time.Now())

	global.APP_LOG.Info("用户注册并自动登录成功",
		zap.String("username", user.Username),
		zap.Uint("user_id", user.ID))

	return &user, token, nil
}

// UserInfo 用户信息结构体
type UserInfo struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Email    string `json:"email"`
	Enabled  bool   `json:"enabled"`
}
