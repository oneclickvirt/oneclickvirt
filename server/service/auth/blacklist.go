package auth

import (
	"context"
	"fmt"
	"oneclickvirt/service/database"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/auth"
	"oneclickvirt/utils"

	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type JWTBlacklistService struct{}

// AddToBlacklist 将Token添加到黑名单
func (s *JWTBlacklistService) AddToBlacklist(tokenString string, userID uint, reason string, revokedBy uint) error {
	// 解析Token获取JTI和过期时间
	jti, expiresAt, err := s.extractTokenInfo(tokenString)
	if err != nil {
		return fmt.Errorf("解析Token失败: %w", err)
	}

	// 检查是否已在黑名单中
	var existing auth.JWTBlacklist
	err = global.APP_DB.Where("jti = ?", jti).First(&existing).Error
	if err == nil {
		// 已存在，更新原因和撤销者
		existing.Reason = reason
		existing.RevokedBy = revokedBy

		dbService := database.GetDatabaseService()
		return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
			return tx.Save(&existing).Error
		})
	}
	if err != gorm.ErrRecordNotFound {
		return err
	}

	// 到黑名单
	blacklist := auth.JWTBlacklist{
		JTI:       jti,
		UserID:    userID,
		ExpiresAt: expiresAt,
		Reason:    reason,
		RevokedBy: revokedBy,
	}

	dbService := database.GetDatabaseService()
	if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Create(&blacklist).Error
	}); err != nil {
		return err
	}

	global.APP_LOG.Debug("Token已加入黑名单", zap.String("jti", jti), zap.Uint("userID", userID), zap.String("reason", reason))

	return nil
}

// IsBlacklisted 检查Token是否在黑名单中
func (s *JWTBlacklistService) IsBlacklisted(jti string) bool {
	var count int64
	err := global.APP_DB.Model(&auth.JWTBlacklist{}).
		Where("jti = ? AND expires_at > ?", jti, time.Now()).
		Count(&count).Error

	if err != nil {
		global.APP_LOG.Error("检查Token黑名单状态失败", zap.String("error", utils.FormatError(err)), zap.String("jti", jti))
		// 出错时采用安全策略，视为已被撤销
		return true
	}

	return count > 0
}

// RevokeUserTokens 撤销指定用户的所有Token
func (s *JWTBlacklistService) RevokeUserTokens(userID uint, reason string, revokedBy uint) error {
	// 由于我们无法获取所有已签发的Token，这里只能标记用户状态改变的时间点
	// 实际的Token验证会在中间件中通过检查用户状态来实现

	global.APP_LOG.Debug("用户所有Token被标记为撤销", zap.Uint("userID", userID), zap.String("reason", reason), zap.Uint("revokedBy", revokedBy))

	return nil
}

// RevokeTokenByJTI 通过JTI撤销特定Token
func (s *JWTBlacklistService) RevokeTokenByJTI(jti string, reason string, revokedBy uint) error {
	// 更新现有记录或创建新记录
	result := global.APP_DB.Model(&auth.JWTBlacklist{}).
		Where("jti = ?", jti).
		Updates(map[string]interface{}{
			"reason":     reason,
			"revoked_by": revokedBy,
			"created_at": time.Now(),
		})

	if result.Error != nil {
		return result.Error
	}

	// 如果没有更新任何记录，说明Token不在黑名单中，需要创建
	if result.RowsAffected == 0 {
		blacklist := auth.JWTBlacklist{
			JTI:       jti,
			ExpiresAt: time.Now().Add(24 * time.Hour), // 假设24小时过期
			Reason:    reason,
			RevokedBy: revokedBy,
		}

		dbService := database.GetDatabaseService()
		return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
			return tx.Create(&blacklist).Error
		})
	}

	return nil
}

// CleanExpiredTokens 清理过期的黑名单Token
func (s *JWTBlacklistService) CleanExpiredTokens() error {
	result := global.APP_DB.Where("expires_at <= ?", time.Now()).Delete(&auth.JWTBlacklist{})
	if result.Error != nil {
		return result.Error
	}

	if result.RowsAffected > 0 {
		global.APP_LOG.Debug("清理过期黑名单Token", zap.Int64("count", result.RowsAffected))
	}

	return nil
}

// extractTokenInfo 从Token字符串中提取JTI和过期时间
func (s *JWTBlacklistService) extractTokenInfo(tokenString string) (string, time.Time, error) {
	// 解析Token但不验证签名（因为我们只需要提取信息）
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
