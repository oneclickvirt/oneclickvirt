package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"oneclickvirt/service/database"

	"oneclickvirt/config"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	userModel "oneclickvirt/model/user"

	"go.uber.org/zap"
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
	userPassword, err := bcrypt.GenerateFromPassword([]byte("user123"), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
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
	levelConfig, exists := global.GetAppConfig().Quota.LevelLimits[level]
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
