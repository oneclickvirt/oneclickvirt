package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	userModel "oneclickvirt/model/user"
	"oneclickvirt/service/database"
	"oneclickvirt/service/userquota"
	"oneclickvirt/utils"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

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

func highestConfiguredLevel() int {
	return userquota.HighestConfiguredLevel()
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
		Level:    highestConfiguredLevel(),
		Status:   1,
	}
	if err := userquota.ApplyLimitFields(&admin, admin.Level); err != nil {
		return err
	}
	// 创建示例用户（默认禁用，防止未授权访问）
	userPassword, err := bcrypt.GenerateFromPassword([]byte("user123"), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	user := userModel.User{
		Username: "user",
		Password: string(userPassword),
		Email:    "user@spiritlhl.net",
		UserType: "user",
		Level:    global.GetAppConfig().Quota.DefaultLevel,
		Status:   0, // 默认禁用状态，需要管理员手动启用
	}
	if err := userquota.ApplyLimitFields(&user, user.Level); err != nil {
		return err
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

// InitSystemWithUsers 使用自定义用户信息初始化系统
func (s *AuthService) InitSystemWithUsers(adminInfo UserInfo, userInfo *UserInfo) error {
	// 检查是否已经初始化
	var count int64
	global.APP_DB.Model(&userModel.User{}).Count(&count)
	if count > 0 {
		return errors.New("系统已初始化")
	}

	if err := utils.ValidateUsername(adminInfo.Username); err != nil {
		return err
	}
	if err := utils.ValidateOptionalEmail(adminInfo.Email); err != nil {
		return err
	}
	if userInfo != nil {
		if err := utils.ValidateUsername(userInfo.Username); err != nil {
			return err
		}
		if err := utils.ValidateOptionalEmail(userInfo.Email); err != nil {
			return err
		}
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
		Level:    highestConfiguredLevel(),
		Status:   1,
	}

	// 使用数据库服务处理事务
	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		if err := tx.Create(&admin).Error; err != nil {
			return err
		}
		// 只在 userInfo 不为 nil 时创建测试用户
		if userInfo != nil {
			userPassword, err := bcrypt.GenerateFromPassword([]byte(userInfo.Password), bcrypt.DefaultCost)
			if err != nil {
				return err
			}
			user := userModel.User{
				Username: userInfo.Username,
				Password: string(userPassword),
				Email:    userInfo.Email,
				UserType: "user",
				Level:    global.GetAppConfig().Quota.DefaultLevel,
				Status:   1,
			}
			if err := userquota.ApplyLimitFields(&user, user.Level); err != nil {
				return err
			}
			if err := tx.Create(&user).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// syncNewUserResourceLimits 同步新用户的资源限制（避免循环导入）

// isEmailConfigured 检查邮箱配置是否可用
func (s *AuthService) isEmailConfigured() bool {
	// 检查系统配置中是否配置了邮箱服务
	var emailConfig adminModel.SystemConfig
	if err := global.APP_DB.Where("key = ?", "email_enabled").First(&emailConfig).Error; err != nil {
		return false
	}
	return emailConfig.Value == "true"
}
