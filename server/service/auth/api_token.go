package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/auth"
	"oneclickvirt/model/common"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// ApiTokenService API Token管理服务
type ApiTokenService struct{}

// generateTokenValue 生成安全的Token值
func generateTokenValue() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// hashToken 对Token进行哈希（用于数据库存储）
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// generateTokenPrefix 生成Token前缀（用于列表显示，如"tk_ab12..."）
func generateTokenPrefix(token string) string {
	if len(token) >= 10 {
		return "tk_" + token[:8] + "..."
	}
	return "tk_" + token[:4] + "..."
}

// CreateToken 创建API Token（仅返回一次完整Token）
func (s *ApiTokenService) CreateToken(userID uint, username, userType string, req auth.ApiTokenCreateRequest) (*auth.ApiTokenCreateResponse, error) {
	if global.APP_DB == nil {
		return nil, fmt.Errorf("数据库连接不可用")
	}

	// 普通用户最多创建3个Token，管理员不限
	if userType == "user" {
		var count int64
		if err := global.APP_DB.Model(&auth.ApiToken{}).
			Where("user_id = ?", userID).
			Count(&count).Error; err != nil {
			return nil, fmt.Errorf("检查Token数量失败: %w", err)
		}
		if count >= 3 {
			return nil, fmt.Errorf("普通用户最多创建3个API Token")
		}
	}

	// 生成Token
	rawToken := generateTokenValue()
	hashedToken := hashToken(rawToken)
	prefix := generateTokenPrefix(rawToken)

	// 计算过期时间
	var expiresAt *time.Time
	if req.ExpireDays > 0 {
		t := time.Now().Add(time.Duration(req.ExpireDays) * 24 * time.Hour)
		expiresAt = &t
	}

	// 构建权限范围字符串
	scopeStr := ""
	if len(req.Scopes) > 0 {
		scopeStr = strings.Join(req.Scopes, ",")
	}

	apiToken := &auth.ApiToken{
		UserID:           userID,
		Username:         username,
		UserType:         userType,
		Name:             req.Name,
		Token:            hashedToken,
		TokenPrefix:      prefix,
		ExpiresAt:        expiresAt,
		Status:           1,
		ScopeRestriction: scopeStr,
	}

	if err := global.APP_DB.Create(apiToken).Error; err != nil {
		global.APP_LOG.Error("创建API Token失败", zap.Error(err))
		return nil, fmt.Errorf("创建Token失败")
	}

	global.APP_LOG.Info("用户创建API Token",
		zap.Uint("userID", userID),
		zap.String("username", username),
		zap.String("tokenName", req.Name))

	return &auth.ApiTokenCreateResponse{
		ID:          apiToken.ID,
		Name:        apiToken.Name,
		Token:       rawToken, // 仅创建时返回完整Token
		TokenPrefix: prefix,
		ExpiresAt:   expiresAt,
	}, nil
}

// GetTokenList 获取用户的API Token列表
func (s *ApiTokenService) GetTokenList(userID uint, req auth.ApiTokenListRequest) ([]auth.ApiToken, int64, error) {
	var tokens []auth.ApiToken
	var total int64

	query := global.APP_DB.Model(&auth.ApiToken{}).Where("user_id = ?", userID)

	if req.Keyword != "" {
		query = query.Where("name LIKE ?", "%"+req.Keyword+"%")
	}
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 || req.PageSize > 100 {
		req.PageSize = 10
	}
	offset := (req.Page - 1) * req.PageSize

	if err := query.Order("created_at DESC").Offset(offset).Limit(req.PageSize).Find(&tokens).Error; err != nil {
		return nil, 0, err
	}

	return tokens, total, nil
}

// GetAdminTokenList 管理员获取所有API Token列表
func (s *ApiTokenService) GetAdminTokenList(req auth.ApiTokenListRequest) ([]auth.ApiToken, int64, error) {
	var tokens []auth.ApiToken
	var total int64

	query := global.APP_DB.Model(&auth.ApiToken{})

	if req.Keyword != "" {
		query = query.Where("name LIKE ? OR username LIKE ?", "%"+req.Keyword+"%", "%"+req.Keyword+"%")
	}
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 || req.PageSize > 100 {
		req.PageSize = 10
	}
	offset := (req.Page - 1) * req.PageSize

	if err := query.Order("created_at DESC").Offset(offset).Limit(req.PageSize).Find(&tokens).Error; err != nil {
		return nil, 0, err
	}

	return tokens, total, nil
}

// DeleteToken 硬删除 API Token
func (s *ApiTokenService) DeleteToken(userID uint, tokenID uint) error {
	result := global.APP_DB.Unscoped().
		Where("id = ? AND user_id = ?", tokenID, userID).
		Delete(&auth.ApiToken{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("Token不存在或无权操作")
	}
	return nil
}

// AdminDeleteToken 管理员硬删除任意 API Token
func (s *ApiTokenService) AdminDeleteToken(tokenID uint) error {
	result := global.APP_DB.Unscoped().
		Where("id = ?", tokenID).
		Delete(&auth.ApiToken{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("Token不存在")
	}
	return nil
}

// AdminBatchDeleteTokens 管理员批量硬删除 API Token
func (s *ApiTokenService) AdminBatchDeleteTokens(ids []uint) error {
	if len(ids) == 0 {
		return fmt.Errorf("未指定要删除的Token ID")
	}
	result := global.APP_DB.Unscoped().
		Where("id IN ?", ids).
		Delete(&auth.ApiToken{})
	return result.Error
}

// ValidateToken 验证API Token并返回认证上下文
// 返回 authCtx, apiToken, error
func (s *ApiTokenService) ValidateToken(rawToken string) (*auth.AuthContext, *auth.ApiToken, error) {
	if rawToken == "" {
		return nil, nil, common.NewError(common.CodeUnauthorized, "未提供API Token")
	}

	hashedToken := hashToken(rawToken)

	var apiToken auth.ApiToken
	if err := global.APP_DB.Where("token = ? AND status = 1", hashedToken).First(&apiToken).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil, common.NewError(common.CodeUnauthorized, "无效的API Token")
		}
		return nil, nil, common.NewError(common.CodeInternalError, "验证Token失败")
	}

	// 检查过期
	if apiToken.ExpiresAt != nil && time.Now().After(*apiToken.ExpiresAt) {
		global.APP_DB.Unscoped().Delete(&apiToken)
		return nil, nil, common.NewError(common.CodeUnauthorized, "API Token已过期")
	}

	// 检查所属用户是否存在且状态正常
	var user struct {
		ID       uint
		Status   int
		UserType string
	}
	if err := global.APP_DB.Table("users").
		Select("id", "status", "user_type").
		Where("id = ?", apiToken.UserID).First(&user).Error; err != nil {
		return nil, nil, common.NewError(common.CodeUnauthorized, "Token关联的用户不存在")
	}
	if user.Status != 1 {
		return nil, nil, common.NewError(common.CodeUnauthorized, "Token关联的用户已被禁用")
	}

	// 更新使用记录（异步，不阻塞验证）
	go func() {
		now := time.Now()
		global.APP_DB.Model(&apiToken).Updates(map[string]interface{}{
			"last_used_at": now,
			"use_count":    gorm.Expr("use_count + 1"),
		})
	}()

	// 构建认证上下文（与JWT认证上下文一致的结构）
	authCtx := &auth.AuthContext{
		UserID:       apiToken.UserID,
		Username:     apiToken.Username,
		UserType:     apiToken.UserType,
		Level:        s.getLevelByUserType(apiToken.UserType),
		BaseUserType: apiToken.UserType,
		IsEffective:  true,
	}

	// 如果有权限范围限制，记录到上下文中（供中间件使用）
	if apiToken.ScopeRestriction != "" {
		// 通过userType的扩展传递scopes（利用AuthContext现有字段）
		// scopes信息通过context注入
	}

	return authCtx, &apiToken, nil
}

// getLevelByUserType 根据用户类型获取权限级别
func (s *ApiTokenService) getLevelByUserType(userType string) int {
	switch userType {
	case "admin", "super_admin":
		return int(auth.AuthLevelAdmin)
	case "normal_admin":
		return int(auth.AuthLevelNormalAdmin)
	default:
		return int(auth.AuthLevelUser)
	}
}
