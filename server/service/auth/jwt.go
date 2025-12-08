package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"oneclickvirt/service/database"
	"sync"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// JWTKeyService JWT密钥管理服务
type JWTKeyService struct {
	mu sync.RWMutex
}

// JWTKey JWT密钥结构
type JWTKey struct {
	Version   int       `json:"version"`
	Key       string    `json:"key"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	IsActive  bool      `json:"is_active"`
}

const (
	JWT_KEY_CATEGORY = "jwt"
	JWT_KEY_PREFIX   = "signing_key_v"
	JWT_ACTIVE_KEY   = "active_version"
	MIN_KEY_SIZE     = 32 // 256位最小密钥长度
	KEY_LIFETIME     = 24 // 密钥生命周期（小时）
	ROTATION_WINDOW  = 12 // 密钥轮换窗口（小时）
)

// InitializeJWTKeys 初始化JWT密钥系统
func (s *JWTKeyService) InitializeJWTKeys() error {
	// 检查数据库是否可用
	if global.APP_DB == nil {
		global.APP_LOG.Error("数据库未初始化，无法初始化JWT密钥系统")
		return fmt.Errorf("数据库未初始化")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 检查是否已有活跃密钥
	activeVersion, err := s.getActiveKeyVersion()
	if err != nil || activeVersion == 0 {
		global.APP_LOG.Info("未找到活跃JWT密钥，开始生成初始密钥")

		// 生成初始密钥
		version, err := s.generateNewKey()
		if err != nil {
			global.APP_LOG.Error("生成初始JWT密钥失败",
				zap.String("error", utils.TruncateString(err.Error(), 200)))
			return fmt.Errorf("生成初始JWT密钥失败: %w", err)
		}

		// 设置为活跃密钥
		if err := s.setActiveKeyVersion(version); err != nil {
			global.APP_LOG.Error("设置活跃密钥失败",
				zap.Int("version", version),
				zap.String("error", utils.TruncateString(err.Error(), 200)))
			return fmt.Errorf("设置活跃密钥失败: %w", err)
		}

		global.APP_LOG.Info("JWT密钥系统初始化完成", zap.Int("version", version))
	} else {
		global.APP_LOG.Info("JWT密钥系统已初始化", zap.Int("activeVersion", activeVersion))
	}

	return nil
}

// generateSecureKey 生成安全的密钥
func (s *JWTKeyService) generateSecureKey() (string, error) {
	// 生成256位（32字节）的安全随机密钥
	bytes := make([]byte, MIN_KEY_SIZE)
	if _, err := rand.Read(bytes); err != nil {
		global.APP_LOG.Error("生成随机密钥失败",
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return "", fmt.Errorf("生成随机密钥失败: %w", err)
	}

	// 转换为十六进制字符串
	key := hex.EncodeToString(bytes)

	// 验证密钥强度
	if len(key) < MIN_KEY_SIZE*2 { // 十六进制字符串长度是字节数的2倍
		global.APP_LOG.Error("生成的密钥强度不足",
			zap.Int("actualLength", len(key)),
			zap.Int("minRequired", MIN_KEY_SIZE*2))
		return "", fmt.Errorf("生成的密钥强度不足，长度: %d, 最小要求: %d", len(key), MIN_KEY_SIZE*2)
	}

	global.APP_LOG.Debug("安全密钥生成成功",
		zap.Int("keyBits", len(key)*4),
		zap.Int("keyLength", len(key)))

	return key, nil
}

// generateNewKey 生成新的JWT密钥
func (s *JWTKeyService) generateNewKey() (int, error) {
	// 生成安全密钥
	key, err := s.generateSecureKey()
	if err != nil {
		global.APP_LOG.Error("生成安全密钥失败",
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return 0, err
	}

	// 获取下一个版本号
	version, err := s.getNextKeyVersion()
	if err != nil {
		global.APP_LOG.Error("获取下一个密钥版本失败",
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return 0, err
	}

	// 计算过期时间
	now := time.Now()
	expiresAt := now.Add(time.Duration(KEY_LIFETIME) * time.Hour)

	// 构建密钥信息
	keyInfo := JWTKey{
		Version:   version,
		Key:       key,
		CreatedAt: now,
		ExpiresAt: expiresAt,
		IsActive:  false, // 新生成的密钥默认不激活
	}

	// 序列化密钥信息
	keyData, err := s.serializeKeyInfo(keyInfo)
	if err != nil {
		global.APP_LOG.Error("序列化密钥信息失败",
			zap.Int("version", version),
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return 0, fmt.Errorf("序列化密钥信息失败: %w", err)
	}

	// 保存到数据库
	config := adminModel.SystemConfig{
		Category:    JWT_KEY_CATEGORY,
		Key:         fmt.Sprintf("%s%d", JWT_KEY_PREFIX, version),
		Value:       keyData,
		Description: fmt.Sprintf("JWT签名密钥版本%d", version),
	}

	dbService := database.GetDatabaseService()
	if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Create(&config).Error
	}); err != nil {
		global.APP_LOG.Error("保存JWT密钥失败",
			zap.Int("version", version),
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return 0, fmt.Errorf("保存JWT密钥失败: %w", err)
	}

	global.APP_LOG.Info("生成新的JWT密钥",
		zap.Int("version", version),
		zap.String("keyLength", fmt.Sprintf("%d bits", len(key)*4)),
		zap.Time("expiresAt", expiresAt))

	return version, nil
}

// GetActiveKey 获取当前活跃的JWT密钥
func (s *JWTKeyService) GetActiveKey() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 获取活跃密钥版本
	version, err := s.getActiveKeyVersion()
	if err != nil {
		global.APP_LOG.Error("获取活跃密钥版本失败",
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return "", err
	}

	if version == 0 {
		global.APP_LOG.Warn("没有活跃的JWT密钥")
		return "", fmt.Errorf("没有活跃的JWT密钥")
	}

	// 获取密钥信息
	keyInfo, err := s.getKeyByVersion(version)
	if err != nil {
		global.APP_LOG.Error("获取密钥信息失败",
			zap.Int("version", version),
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return "", err
	}

	// 检查密钥是否过期
	if time.Now().After(keyInfo.ExpiresAt) {
		global.APP_LOG.Warn("活跃JWT密钥已过期",
			zap.Int("version", version),
			zap.Time("expiresAt", keyInfo.ExpiresAt))
		return "", fmt.Errorf("活跃JWT密钥已过期")
	}

	global.APP_LOG.Debug("获取活跃JWT密钥成功", zap.Int("version", version))
	return keyInfo.Key, nil
}

// GetKeyByVersion 根据版本获取JWT密钥（用于验证旧token）
func (s *JWTKeyService) GetKeyByVersion(version int) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	keyInfo, err := s.getKeyByVersion(version)
	if err != nil {
		global.APP_LOG.Error("根据版本获取密钥失败",
			zap.Int("version", version),
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return "", err
	}

	global.APP_LOG.Debug("根据版本获取密钥成功", zap.Int("version", version))
	return keyInfo.Key, nil
}

// RotateKey 轮换JWT密钥
func (s *JWTKeyService) RotateKey() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	global.APP_LOG.Info("开始JWT密钥轮换")

	// 生成新密钥
	newVersion, err := s.generateNewKey()
	if err != nil {
		global.APP_LOG.Error("生成新密钥失败",
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return fmt.Errorf("生成新密钥失败: %w", err)
	}

	// 激活新密钥
	if err := s.setActiveKeyVersion(newVersion); err != nil {
		global.APP_LOG.Error("激活新密钥失败",
			zap.Int("newVersion", newVersion),
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return fmt.Errorf("激活新密钥失败: %w", err)
	}

	// 清理过期密钥
	if err := s.cleanupExpiredKeys(); err != nil {
		global.APP_LOG.Warn("清理过期密钥失败",
			zap.String("error", utils.TruncateString(err.Error(), 200)))
	}

	global.APP_LOG.Info("JWT密钥轮换完成", zap.Int("newVersion", newVersion))

	return nil
}

// ShouldRotateKey 检查是否应该轮换密钥
func (s *JWTKeyService) ShouldRotateKey() (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 获取当前活跃密钥
	version, err := s.getActiveKeyVersion()
	if err != nil {
		global.APP_LOG.Error("检查密钥轮换时获取活跃版本失败",
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return false, err
	}

	if version == 0 {
		global.APP_LOG.Info("没有活跃密钥，需要生成")
		return true, nil // 没有活跃密钥，需要生成
	}

	// 获取密钥信息
	keyInfo, err := s.getKeyByVersion(version)
	if err != nil {
		global.APP_LOG.Warn("获取密钥信息失败，建议重新生成",
			zap.Int("version", version),
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return true, nil // 获取失败，需要重新生成
	}

	// 检查是否需要轮换
	now := time.Now()
	rotationTime := keyInfo.CreatedAt.Add(time.Duration(ROTATION_WINDOW) * time.Hour)
	shouldRotate := now.After(rotationTime)

	if shouldRotate {
		global.APP_LOG.Info("JWT密钥需要轮换",
			zap.Int("version", version),
			zap.Time("createdAt", keyInfo.CreatedAt),
			zap.Time("rotationTime", rotationTime))
	} else {
		global.APP_LOG.Debug("JWT密钥暂不需要轮换",
			zap.Int("version", version),
			zap.Duration("remainingTime", rotationTime.Sub(now)))
	}

	return shouldRotate, nil
}

// getActiveKeyVersion 获取活跃密钥版本
func (s *JWTKeyService) getActiveKeyVersion() (int, error) {
	var config adminModel.SystemConfig
	err := global.APP_DB.Where("category = ? AND `key` = ?", JWT_KEY_CATEGORY, JWT_ACTIVE_KEY).First(&config).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			global.APP_LOG.Debug("未找到活跃密钥配置")
			return 0, nil
		}
		global.APP_LOG.Error("查询活跃密钥版本失败",
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return 0, err
	}

	var version int
	if _, err := fmt.Sscanf(config.Value, "%d", &version); err != nil {
		global.APP_LOG.Error("解析活跃密钥版本失败",
			zap.String("value", config.Value),
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return 0, fmt.Errorf("解析活跃密钥版本失败: %w", err)
	}

	return version, nil
}

// setActiveKeyVersion 设置活跃密钥版本
func (s *JWTKeyService) setActiveKeyVersion(version int) error {
	config := adminModel.SystemConfig{
		Category:    JWT_KEY_CATEGORY,
		Key:         JWT_ACTIVE_KEY,
		Value:       fmt.Sprintf("%d", version),
		Description: "当前活跃的JWT密钥版本",
	}

	// 使用upsert逻辑
	err := global.APP_DB.Where("category = ? AND `key` = ?", JWT_KEY_CATEGORY, JWT_ACTIVE_KEY).
		Assign(config).
		FirstOrCreate(&config).Error

	if err != nil {
		global.APP_LOG.Error("设置活跃密钥版本失败",
			zap.Int("version", version),
			zap.String("error", utils.TruncateString(err.Error(), 200)))
	} else {
		global.APP_LOG.Debug("设置活跃密钥版本成功", zap.Int("version", version))
	}

	return err
}

// getNextKeyVersion 获取下一个密钥版本号
func (s *JWTKeyService) getNextKeyVersion() (int, error) {
	var maxVersion int

	// 查询最大版本号
	row := global.APP_DB.Raw(`
		SELECT COALESCE(MAX(CAST(SUBSTR(`+"`key`"+`, LENGTH(?) + 1) AS SIGNED)), 0)
		FROM system_configs 
		WHERE category = ? AND `+"`key`"+` LIKE ?
	`, JWT_KEY_PREFIX, JWT_KEY_CATEGORY, JWT_KEY_PREFIX+"%").Row()

	if err := row.Scan(&maxVersion); err != nil {
		global.APP_LOG.Error("查询最大版本号失败",
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return 0, fmt.Errorf("查询最大版本号失败: %w", err)
	}

	nextVersion := maxVersion + 1
	global.APP_LOG.Debug("获取下一个密钥版本",
		zap.Int("maxVersion", maxVersion),
		zap.Int("nextVersion", nextVersion))

	return nextVersion, nil
}

// getKeyByVersion 根据版本获取密钥信息
func (s *JWTKeyService) getKeyByVersion(version int) (*JWTKey, error) {
	var config adminModel.SystemConfig
	keyName := fmt.Sprintf("%s%d", JWT_KEY_PREFIX, version)

	err := global.APP_DB.Where("category = ? AND `key` = ?", JWT_KEY_CATEGORY, keyName).First(&config).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			global.APP_LOG.Warn("密钥版本不存在", zap.Int("version", version))
		} else {
			global.APP_LOG.Error("查询密钥版本失败",
				zap.Int("version", version),
				zap.String("error", utils.TruncateString(err.Error(), 200)))
		}
		return nil, fmt.Errorf("密钥版本%d不存在: %w", version, err)
	}

	// 反序列化密钥信息
	keyInfo, err := s.deserializeKeyInfo(config.Value)
	if err != nil {
		global.APP_LOG.Error("反序列化密钥信息失败",
			zap.Int("version", version),
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return nil, fmt.Errorf("反序列化密钥信息失败: %w", err)
	}

	return keyInfo, nil
}

// serializeKeyInfo 序列化密钥信息
func (s *JWTKeyService) serializeKeyInfo(keyInfo JWTKey) (string, error) {
	data, err := json.Marshal(keyInfo)
	if err != nil {
		global.APP_LOG.Error("序列化密钥信息失败",
			zap.Int("version", keyInfo.Version),
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return "", err
	}
	return string(data), nil
}

// deserializeKeyInfo 反序列化密钥信息
func (s *JWTKeyService) deserializeKeyInfo(data string) (*JWTKey, error) {
	var keyInfo JWTKey
	if err := json.Unmarshal([]byte(data), &keyInfo); err != nil {
		global.APP_LOG.Error("反序列化密钥数据失败",
			zap.String("data", utils.TruncateString(data, 100)),
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return nil, fmt.Errorf("解析密钥数据失败: %w", err)
	}
	return &keyInfo, nil
}

// cleanupExpiredKeys 清理过期的密钥
func (s *JWTKeyService) cleanupExpiredKeys() error {
	// 保留最近的3个版本的密钥，删除更早的过期密钥
	keepVersions := 3

	// 获取所有密钥版本，按版本号降序排列
	var configs []adminModel.SystemConfig
	err := global.APP_DB.Where("category = ? AND `key` LIKE ?", JWT_KEY_CATEGORY, JWT_KEY_PREFIX+"%").
		Order("`key` DESC").
		Limit(100). // 限制最多100条配置
		Find(&configs).Error
	if err != nil {
		global.APP_LOG.Error("查询密钥列表失败",
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return err
	}

	global.APP_LOG.Debug("开始清理过期密钥",
		zap.Int("totalKeys", len(configs)),
		zap.Int("keepVersions", keepVersions))

	// 如果密钥数量超过保留数量，删除多余的
	if len(configs) > keepVersions {
		deletedCount := 0
		for i := keepVersions; i < len(configs); i++ {
			config := configs[i]

			// 解析密钥信息检查是否过期
			keyInfo, err := s.deserializeKeyInfo(config.Value)
			if err != nil {
				global.APP_LOG.Warn("解析密钥信息失败，跳过清理",
					zap.String("key", config.Key),
					zap.String("error", utils.TruncateString(err.Error(), 200)))
				continue
			}

			// 只删除过期的密钥
			if time.Now().After(keyInfo.ExpiresAt) {
				dbService := database.GetDatabaseService()
				if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
					return tx.Delete(&config).Error
				}); err != nil {
					global.APP_LOG.Warn("删除过期密钥失败",
						zap.String("key", config.Key),
						zap.String("error", utils.TruncateString(err.Error(), 200)))
				} else {
					global.APP_LOG.Info("删除过期密钥",
						zap.String("key", config.Key),
						zap.Int("version", keyInfo.Version),
						zap.Time("expiresAt", keyInfo.ExpiresAt))
					deletedCount++
				}
			}
		}

		if deletedCount > 0 {
			global.APP_LOG.Info("密钥清理完成", zap.Int("deletedCount", deletedCount))
		}
	}

	return nil
}

// GetAllKeys 获取所有密钥信息（用于调试和监控）
func (s *JWTKeyService) GetAllKeys() ([]JWTKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var configs []adminModel.SystemConfig
	err := global.APP_DB.Where("category = ? AND key LIKE ?", JWT_KEY_CATEGORY, JWT_KEY_PREFIX+"%").
		Order("key ASC").
		Limit(100). // 限制最多100条配置
		Find(&configs).Error
	if err != nil {
		global.APP_LOG.Error("查询所有密钥失败",
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return nil, err
	}

	var keys []JWTKey
	for _, config := range configs {
		keyInfo, err := s.deserializeKeyInfo(config.Value)
		if err != nil {
			global.APP_LOG.Warn("解析密钥信息失败",
				zap.String("key", config.Key),
				zap.String("error", utils.TruncateString(err.Error(), 200)))
			continue
		}
		keys = append(keys, *keyInfo)
	}

	global.APP_LOG.Debug("获取所有密钥信息成功", zap.Int("keyCount", len(keys)))
	return keys, nil
}
