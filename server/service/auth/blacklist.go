package auth

import (
	"fmt"
	"sync"
	"time"

	"oneclickvirt/global"

	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
)

// blacklistItem 黑名单项
type blacklistItem struct {
	userID    uint
	expiresAt time.Time
	reason    string
	revokedBy uint
}

// JWTBlacklistService 基于内存的JWT黑名单服务
type JWTBlacklistService struct {
	data        map[string]*blacklistItem
	mutex       sync.RWMutex
	stopCleanup chan struct{}
}

var (
	blacklistService     *JWTBlacklistService
	blacklistServiceOnce sync.Once
)

// GetJWTBlacklistService 获取JWT黑名单服务单例
func GetJWTBlacklistService() *JWTBlacklistService {
	blacklistServiceOnce.Do(func() {
		blacklistService = &JWTBlacklistService{
			data:        make(map[string]*blacklistItem),
			stopCleanup: make(chan struct{}),
		}
		blacklistService.startCleanup()
	})
	return blacklistService
}

// startCleanup 启动自适应自动清理过期Token
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
				// 检查黑名单Token数量
				s.mutex.RLock()
				tokenCount := len(s.data)
				s.mutex.RUnlock()

				// 有Token时5分钟清理，无Token时30分钟检查（节省资源）
				newInterval := 30 * time.Minute
				if tokenCount > 0 {
					newInterval = 5 * time.Minute
					s.CleanExpiredTokens()
				}
				ticker.Reset(newInterval)

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

// AddToBlacklist 将Token添加到黑名单
func (s *JWTBlacklistService) AddToBlacklist(tokenString string, userID uint, reason string, revokedBy uint) error {
	// 解析Token获取JTI和过期时间
	jti, expiresAt, err := s.extractTokenInfo(tokenString)
	if err != nil {
		return fmt.Errorf("解析Token失败: %w", err)
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.data[jti] = &blacklistItem{
		userID:    userID,
		expiresAt: expiresAt,
		reason:    reason,
		revokedBy: revokedBy,
	}

	global.APP_LOG.Debug("Token已加入内存黑名单",
		zap.String("jti", jti),
		zap.Uint("userID", userID),
		zap.String("reason", reason))

	return nil
}

// IsBlacklisted 检查Token是否在黑名单中
func (s *JWTBlacklistService) IsBlacklisted(jti string) bool {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	item, exists := s.data[jti]
	if !exists {
		return false
	}

	// 检查是否过期
	if time.Now().After(item.expiresAt) {
		return false
	}

	return true
}

// RevokeUserTokens 撤销指定用户的所有Token
func (s *JWTBlacklistService) RevokeUserTokens(userID uint, reason string, revokedBy uint) error {
	// 由于无法枚举所有已签发的Token，这里只记录撤销意图
	// 实际的Token验证会在中间件中通过检查用户状态来实现
	global.APP_LOG.Debug("用户所有Token被标记为撤销",
		zap.Uint("userID", userID),
		zap.String("reason", reason),
		zap.Uint("revokedBy", revokedBy))

	return nil
}

// CleanExpiredTokens 清理过期的黑名单Token
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

// extractTokenInfo 从Token字符串中提取JTI和过期时间
func (s *JWTBlacklistService) extractTokenInfo(tokenString string) (string, time.Time, error) {
	// 解析Token但不验证签名（因为只需要提取信息）
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	token, _, err := parser.ParseUnverified(tokenString, jwt.MapClaims{})
	if err != nil {
		return "", time.Time{}, err
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", time.Time{}, fmt.Errorf("无效的Token claims")
	}

	// 提取JTI
	jti, ok := claims["jti"].(string)
	if !ok || jti == "" {
		return "", time.Time{}, fmt.Errorf("Token缺少JTI字段")
	}

	// 提取过期时间
	exp, ok := claims["exp"].(float64)
	if !ok {
		return "", time.Time{}, fmt.Errorf("Token缺少exp字段")
	}

	expiresAt := time.Unix(int64(exp), 0)
	return jti, expiresAt, nil
}
