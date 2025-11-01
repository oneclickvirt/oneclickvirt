package system

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"sync"

	"oneclickvirt/global"
	systemModel "oneclickvirt/model/system"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// JWTSecretService JWT密钥管理服务
type JWTSecretService struct {
	mutex     sync.RWMutex
	secretKey string // 缓存的密钥
}

var (
	jwtSecretService     *JWTSecretService
	jwtSecretServiceOnce sync.Once
)

// GetJWTSecretService 获取JWT密钥服务单例
func GetJWTSecretService() *JWTSecretService {
	jwtSecretServiceOnce.Do(func() {
		jwtSecretService = &JWTSecretService{}
	})
	return jwtSecretService
}

// InitializeJWTSecret 初始化JWT密钥（系统启动时调用）
func (s *JWTSecretService) InitializeJWTSecret(db *gorm.DB) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	// 优先使用环境变量（用于多实例部署）
	if envKey := os.Getenv("JWT_SIGNING_KEY"); envKey != "" {
		if len(envKey) < 32 {
			return fmt.Errorf("环境变量JWT_SIGNING_KEY长度不足32字符")
		}
		s.secretKey = envKey
		global.APP_CONFIG.JWT.SigningKey = envKey
		global.APP_LOG.Info("使用环境变量中的JWT密钥")
		return nil
	}

	// 尝试从数据库加载
	var jwtSecret systemModel.JWTSecret
	err := db.First(&jwtSecret).Error

	if err == nil {
		// 找到了密钥，使用它
		s.secretKey = jwtSecret.SecretKey
		global.APP_CONFIG.JWT.SigningKey = jwtSecret.SecretKey
		global.APP_LOG.Info("从数据库加载JWT密钥")
		return nil
	}

	if err != gorm.ErrRecordNotFound {
		// 数据库错误
		return fmt.Errorf("查询JWT密钥失败: %w", err)
	}

	// 数据库中没有密钥，生成新密钥
	newKey, err := s.generateSecureKey()
	if err != nil {
		return fmt.Errorf("生成JWT密钥失败: %w", err)
	}

	// 保存到数据库
	jwtSecret = systemModel.JWTSecret{
		SecretKey: newKey,
	}
	if err := db.Create(&jwtSecret).Error; err != nil {
		return fmt.Errorf("保存JWT密钥到数据库失败: %w", err)
	}

	s.secretKey = newKey
	global.APP_CONFIG.JWT.SigningKey = newKey
	global.APP_LOG.Info("生成并保存新的JWT密钥到数据库")

	return nil
}

// GetSecretKey 获取当前的JWT密钥
func (s *JWTSecretService) GetSecretKey() string {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	// 如果缓存为空，返回全局配置中的密钥
	if s.secretKey == "" {
		return global.APP_CONFIG.JWT.SigningKey
	}

	return s.secretKey
}

// generateSecureKey 生成安全的密钥
func (s *JWTSecretService) generateSecureKey() (string, error) {
	bytes := make([]byte, 32) // 256位密钥
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// RotateSecret 轮换JWT密钥（管理功能）
func (s *JWTSecretService) RotateSecret(db *gorm.DB) (string, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	// 生成新密钥
	newKey, err := s.generateSecureKey()
	if err != nil {
		return "", fmt.Errorf("生成新密钥失败: %w", err)
	}

	// 更新数据库中的所有记录（正常情况下只有一条）
	if err := db.Model(&systemModel.JWTSecret{}).Update("secret_key", newKey).Error; err != nil {
		return "", fmt.Errorf("更新数据库密钥失败: %w", err)
	}

	s.secretKey = newKey
	global.APP_CONFIG.JWT.SigningKey = newKey

	global.APP_LOG.Warn("JWT密钥已轮换，所有现有token将失效",
		zap.String("newKeyPrefix", newKey[:8]+"..."))

	return newKey, nil
}
