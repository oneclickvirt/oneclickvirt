package auth

import (
	"fmt"
	"sync"
	"time"

	"oneclickvirt/global"
	userModel "oneclickvirt/model/user"
	"oneclickvirt/service/cache"

	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
)

// blacklistItem 内存黑名单项
type blacklistItem struct {
	userID    uint
	expiresAt time.Time
}

// JWTBlacklistService JWT黑名单服务（内存热缓存 + 数据库持久化）
type JWTBlacklistService struct {
	data        map[string]*blacklistItem
	mutex       sync.RWMutex
	stopCleanup chan struct{}
}

var (
	blacklistService     *JWTBlacklistService
	blacklistServiceOnce sync.Once
)

// GetJWTBlacklistService 获取JWT黑名单服务单例（首次调用时从数据库加载历史记录）
func GetJWTBlacklistService() *JWTBlacklistService {
	blacklistServiceOnce.Do(func() {
		blacklistService = &JWTBlacklistService{
			data:        make(map[string]*blacklistItem),
			stopCleanup: make(chan struct{}),
		}
		// 从数据库恢复未过期的黑名单记录（处理重启场景）
		if global.APP_DB != nil {
			blacklistService.loadFromDB()
		}
		blacklistService.startCleanup()
	})
	return blacklistService
}

// loadFromDB 从数据库加载未过期的黑名单记录到内存
func (s *JWTBlacklistService) loadFromDB() {
	var entries []userModel.JWTBlacklistedToken
	if err := global.APP_DB.Where("expires_at > ?", time.Now()).Find(&entries).Error; err != nil {
		global.APP_LOG.Error("从数据库加载JWT黑名单失败", zap.Error(err))
		return
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()
	for _, e := range entries {
		s.data[e.JTI] = &blacklistItem{
			userID:    e.UserID,
			expiresAt: e.ExpiresAt,
		}
	}
	if len(entries) > 0 {
		global.APP_LOG.Info("从数据库恢复JWT黑名单记录", zap.Int("count", len(entries)))
	}
}

// startCleanup 启动自适应自动清理（同时清理内存和数据库过期记录）
func (s *JWTBlacklistService) startCleanup() {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				global.APP_LOG.Error("JWT黑名单清理goroutine panic", zap.Any("panic", r))
			}
		}()

		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				s.CleanExpiredTokens()
				// 同步清理数据库过期记录（低优先级，忽略错误）
				if global.APP_DB != nil {
					global.APP_DB.Where("expires_at <= ?", time.Now()).Delete(&userModel.JWTBlacklistedToken{})
				}
			case <-s.stopCleanup:
				return
			}
		}
	}()
}

// Stop 停止清理任务
func (s *JWTBlacklistService) Stop() {
	close(s.stopCleanup)
}

// AddToBlacklist 将Token加入黑名单（内存 + 数据库双写）
func (s *JWTBlacklistService) AddToBlacklist(tokenString string, userID uint, reason string, revokedBy uint) error {
	jti, expiresAt, err := s.extractTokenInfo(tokenString)
	if err != nil {
		return fmt.Errorf("解析Token失败: %w", err)
	}

	// 持久化到数据库（防止重启后黑名单丢失）
	if global.APP_DB != nil {
		entry := &userModel.JWTBlacklistedToken{
			JTI:       jti,
			UserID:    userID,
			ExpiresAt: expiresAt,
			Reason:    reason,
			RevokedBy: revokedBy,
		}
		if err := global.APP_DB.Create(entry).Error; err != nil {
			global.APP_LOG.Warn("持久化JWT黑名单记录失败（内存黑名单仍有效）",
				zap.String("jti", jti), zap.Error(err))
		}
	}

	// 写入内存缓存
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.data[jti] = &blacklistItem{userID: userID, expiresAt: expiresAt}

	global.APP_LOG.Debug("Token已加入黑名单",
		zap.String("jti", jti),
		zap.Uint("userID", userID),
		zap.String("reason", reason))

	return nil
}

// IsBlacklisted 检查 JTI 是否在黑名单中（仅查内存，高性能）
func (s *JWTBlacklistService) IsBlacklisted(jti string) bool {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	item, exists := s.data[jti]
	if !exists {
		return false
	}
	return time.Now().Before(item.expiresAt)
}

// RevokeUserTokens 将用户的所有存量 Token 设为无效
// 通过更新 tokens_invalidated_at 时间戳实现，无需枚举已签发的 Token
func (s *JWTBlacklistService) RevokeUserTokens(userID uint, reason string, revokedBy uint) error {
	if global.APP_DB == nil {
		return fmt.Errorf("数据库未初始化")
	}
	now := time.Now()
	if err := global.APP_DB.Model(&userModel.User{}).
		Where("id = ?", userID).
		UpdateColumn("tokens_invalidated_at", now).Error; err != nil {
		return err
	}

	// 立即使该用户的认证上下文缓存失效，确保下次请求重新从数据库验证
	cache.GetUserCacheService().InvalidateUserCache(userID)

	global.APP_LOG.Info("用户所有Token已标记为无效",
		zap.Uint("userID", userID),
		zap.String("reason", reason),
		zap.Uint("revokedBy", revokedBy))

	return nil
}

// CleanExpiredTokens 清理内存中过期的黑名单条目
func (s *JWTBlacklistService) CleanExpiredTokens() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	now := time.Now()
	count := 0
	for jti, item := range s.data {
		if now.After(item.expiresAt) {
			delete(s.data, jti)
			count++
		}
	}
	if count > 0 {
		global.APP_LOG.Debug("清理过期内存黑名单Token", zap.Int("count", count))
	}
	return nil
}

// extractTokenInfo 从 Token 字符串中提取 JTI 和过期时间（不验证签名）
func (s *JWTBlacklistService) extractTokenInfo(tokenString string) (string, time.Time, error) {
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	token, _, err := parser.ParseUnverified(tokenString, jwt.MapClaims{})
	if err != nil {
		return "", time.Time{}, err
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", time.Time{}, fmt.Errorf("无效的Token claims")
	}

	jti, ok := claims["jti"].(string)
	if !ok || jti == "" {
		return "", time.Time{}, fmt.Errorf("Token缺少JTI字段")
	}

	exp, ok := claims["exp"].(float64)
	if !ok {
		return "", time.Time{}, fmt.Errorf("Token缺少exp字段")
	}

	return jti, time.Unix(int64(exp), 0), nil
}
