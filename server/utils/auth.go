package utils

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/global"

	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
)

// GetJWTKey 获取JWT密钥
// 优先级：环境变量 > 自动生成的随机密钥
func GetJWTKey() string {
	// 优先使用环境变量（用于多实例部署时统一密钥）
	if key := os.Getenv("JWT_SIGNING_KEY"); key != "" {
		return key
	}

	// 使用自动生成的随机密钥（在core/viper.go中生成）
	return global.APP_CONFIG.JWT.SigningKey
}

// parseDuration 解析配置中的时间字符串（支持 1d, 7d, 24h 等格式）
func parseDuration(durationStr string) time.Duration {
	durationStr = strings.TrimSpace(durationStr)
	if durationStr == "" {
		return 24 * time.Hour // 默认24小时
	}

	// 尝试直接解析（如 "24h", "1h30m"）
	if d, err := time.ParseDuration(durationStr); err == nil {
		return d
	}

	// 解析天数格式（如 "7d", "1d"）
	if strings.HasSuffix(durationStr, "d") {
		dayStr := strings.TrimSuffix(durationStr, "d")
		if days, err := strconv.Atoi(dayStr); err == nil {
			return time.Duration(days) * 24 * time.Hour
		}
	}

	// 解析失败，返回默认值
	global.APP_LOG.Warn("无法解析时间配置，使用默认24小时", zap.String("input", durationStr))
	return 24 * time.Hour
}

// GenerateToken 生成JWT token（使用配置的过期时间）
func GenerateToken(userID uint, username, userType string) (string, error) {
	now := time.Now()

	// 从配置读取过期时间
	expiresTime := parseDuration(global.APP_CONFIG.JWT.ExpiresTime)

	claims := jwt.MapClaims{
		"user_id":   userID,
		"username":  username,
		"user_type": userType,
		"exp":       now.Add(expiresTime).Unix(),
		"iat":       now.Unix(),
		"nbf":       now.Unix(),
		"jti":       generateTokenID(), // 唯一token ID
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(GetJWTKey()))
}

// ShouldRefreshToken 检查token是否需要刷新（还剩不到1/3有效期）
func ShouldRefreshToken(claims *jwt.MapClaims) bool {
	if claims == nil {
		return false
	}

	// 获取过期时间和签发时间
	expFloat, expOk := (*claims)["exp"].(float64)
	iatFloat, iatOk := (*claims)["iat"].(float64)

	if !expOk || !iatOk {
		return false
	}

	exp := time.Unix(int64(expFloat), 0)
	iat := time.Unix(int64(iatFloat), 0)
	now := time.Now()

	// 计算总有效期和已用时间
	totalDuration := exp.Sub(iat)
	elapsed := now.Sub(iat)

	// 如果已经使用了超过2/3的有效期，应该刷新
	// 例如：7天有效期，超过4.67天(2/3)后就可以刷新
	return elapsed > totalDuration*2/3
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
