package utils

import (
	"fmt"
	"os"
	"time"

	"oneclickvirt/global"

	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
)

// GetJWTKey 获取JWT密钥，优先使用环境变量
func GetJWTKey() string {
	// 优先使用环境变量（用于紧急情况）
	if key := os.Getenv("JWT_SIGNING_KEY"); key != "" {
		global.APP_LOG.Warn("使用环境变量中的JWT密钥，建议使用密钥管理服务")
		return key
	}

	// 使用配置文件中的密钥（启动时自动生成）
	return global.APP_CONFIG.JWT.SigningKey
}

// GenerateToken 生成JWT token（包含密钥版本信息）
func GenerateToken(userID uint, username, userType string) (string, error) {
	now := time.Now()

	claims := jwt.MapClaims{
		"user_id":   userID,
		"username":  username,
		"user_type": userType,
		"exp":       now.Add(24 * time.Hour).Unix(),
		"iat":       now.Unix(),
		"nbf":       now.Unix(),
		"jti":       generateTokenID(), // 唯一token ID
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(GetJWTKey()))
}

// ValidateToken 验证JWT token
func ValidateToken(tokenString string) (*jwt.MapClaims, error) {
	claims := &jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		// 验证签名方法
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("意外的签名方法: %v", token.Header["alg"])
		}
		return []byte(GetJWTKey()), nil
	})

	if err != nil {
		global.APP_LOG.Warn("JWT token验证失败", zap.Error(err))
		return nil, err
	}

	if !token.Valid {
		return nil, fmt.Errorf("token无效")
	}

	return claims, nil
}

// generateTokenID 生成唯一的token ID
func generateTokenID() string {
	return fmt.Sprintf("%d_%d", time.Now().UnixNano(), os.Getpid())
}
