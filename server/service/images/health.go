package images

import (
	"context"

	"oneclickvirt/global"
	"oneclickvirt/model/provider"
	"oneclickvirt/provider/health"

	"go.uber.org/zap"
)

// HealthConfigAdapter 健康检查配置适配器
type HealthConfigAdapter struct {
	authConfig *provider.ProviderAuthConfig
}

// NewHealthConfigAdapter 创建健康检查配置适配器
func NewHealthConfigAdapter(authConfig *provider.ProviderAuthConfig) *HealthConfigAdapter {
	return &HealthConfigAdapter{
		authConfig: authConfig,
	}
}

// GetType 获取类型
func (h *HealthConfigAdapter) GetType() string {
	return h.authConfig.Type
}

// GetCertificate 获取证书信息
func (h *HealthConfigAdapter) GetCertificate() health.CertificateInfo {
	if h.authConfig.Certificate != nil {
		return &CertConfigAdapter{cert: h.authConfig.Certificate}
	}
	return nil
}

// GetToken 获取Token信息
func (h *HealthConfigAdapter) GetToken() health.TokenInfo {
	if h.authConfig.Token != nil {
		return &TokenConfigAdapter{token: h.authConfig.Token}
	}
	return nil
}

// CertConfigAdapter 证书配置适配器
type CertConfigAdapter struct {
	cert *provider.CertConfig
}

// GetCertPath 获取证书路径
func (c *CertConfigAdapter) GetCertPath() string {
	return c.cert.CertPath
}

// GetKeyPath 获取私钥路径
func (c *CertConfigAdapter) GetKeyPath() string {
	return c.cert.KeyPath
}

// GetCertContent 获取证书内容
func (c *CertConfigAdapter) GetCertContent() string {
	return c.cert.CertContent
}

// GetKeyContent 获取私钥内容
func (c *CertConfigAdapter) GetKeyContent() string {
	return c.cert.KeyContent
}

// TokenConfigAdapter Token配置适配器
type TokenConfigAdapter struct {
	token *provider.TokenConfig
}

// GetTokenID 获取TokenID
func (t *TokenConfigAdapter) GetTokenID() string {
	return t.token.TokenID
}

// GetTokenSecret 获取Token密钥
func (t *TokenConfigAdapter) GetTokenSecret() string {
	return t.token.TokenSecret
}

// CheckProviderHealthWithConfig 使用配置进行健康检查
// 返回: sshStatus, apiStatus, hostName, error
func CheckProviderHealthWithConfig(ctx context.Context, providerType, host, username, password, sshKey string, port int, authConfig *provider.ProviderAuthConfig) (string, string, string, error) {
	// 使用全局logger，如果没有则传nil
	var logger *zap.Logger
	if global.APP_LOG != nil {
		logger = global.APP_LOG
	}

	healthChecker := health.NewProviderHealthChecker(logger)
	adapter := NewHealthConfigAdapter(authConfig)
	return healthChecker.CheckProviderHealthWithAuthConfig(ctx, providerType, host, username, password, sshKey, port, adapter)
}
