package proxmox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

// TokenInfo 用于存储 Proxmox Token 信息
type TokenInfo struct {
	TokenID     string `json:"tokenId"`
	TokenSecret string `json:"tokenSecret"`
	Username    string `json:"username"`
	Created     string `json:"created"`
}

// saveTokenToFiles 将 Token 信息保存到本地文件
func (p *ProxmoxProvider) saveTokenToFiles(tokenID, tokenSecret string) error {
	if p.providerUUID == "" {
		return fmt.Errorf("provider UUID is empty")
	}

	// 创建 certs 目录
	certsDir := "certs"
	if err := os.MkdirAll(certsDir, 0755); err != nil {
		return fmt.Errorf("failed to create certs directory: %w", err)
	}

	// 创建 Token 信息结构
	tokenInfo := TokenInfo{
		TokenID:     tokenID,
		TokenSecret: tokenSecret,
		Username:    "oneclickvirt", // 假设使用固定用户名
		Created:     time.Now().Format(time.RFC3339),
	}

	// 将 Token 信息序列化为 JSON
	tokenData, err := json.MarshalIndent(tokenInfo, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal token info: %w", err)
	}

	// 保存到文件，使用 .token 扩展名以区别于证书文件
	tokenPath := filepath.Join(certsDir, fmt.Sprintf("%s.token", p.providerUUID))
	if err := os.WriteFile(tokenPath, tokenData, 0600); err != nil {
		return fmt.Errorf("failed to write token file: %w", err)
	}

	global.APP_LOG.Info("Proxmox token saved to local file",
		zap.String("provider", p.providerUUID),
		zap.String("tokenPath", tokenPath),
		zap.String("tokenID", tokenID))

	return nil
}

// loadTokenFromFiles 从本地文件加载 Token 信息
func (p *ProxmoxProvider) loadTokenFromFiles() error {
	if p.providerUUID == "" {
		return fmt.Errorf("provider UUID is empty")
	}

	tokenPath := filepath.Join("certs", fmt.Sprintf("%s.token", p.providerUUID))

	// 检查文件是否存在
	if _, err := os.Stat(tokenPath); os.IsNotExist(err) {
		return fmt.Errorf("token file does not exist: %s", tokenPath)
	}

	// 读取文件内容
	tokenData, err := os.ReadFile(tokenPath)
	if err != nil {
		return fmt.Errorf("failed to read token file: %w", err)
	}

	// 解析 JSON
	var tokenInfo TokenInfo
	if err := json.Unmarshal(tokenData, &tokenInfo); err != nil {
		return fmt.Errorf("failed to unmarshal token info: %w", err)
	}

	// 更新配置
	p.config.TokenID = tokenInfo.TokenID
	p.config.Token = tokenInfo.TokenSecret

	global.APP_LOG.Info("Proxmox token loaded from local file",
		zap.String("provider", p.providerUUID),
		zap.String("tokenPath", tokenPath),
		zap.String("tokenID", tokenInfo.TokenID))

	return nil
}

// UpdateToken 更新 Token 信息并保存到本地文件
func (p *ProxmoxProvider) UpdateToken(tokenID, tokenSecret string) error {
	// 更新内存中的配置
	p.config.TokenID = tokenID
	p.config.Token = tokenSecret

	// 保存到本地文件
	return p.saveTokenToFiles(tokenID, tokenSecret)
}

// loadTokenFromConfig 从 NodeConfig 的扩展配置中加载 Token 信息
func (p *ProxmoxProvider) loadTokenFromConfig() error {
	// 如果没有扩展配置，尝试使用直接的 Token 和 TokenID 字段
	if p.config.TokenID != "" && p.config.Token != "" {
		global.APP_LOG.Info("Using direct token fields from config",
			zap.String("tokenID", p.config.TokenID))
		return nil
	}
	// TODO: 如果有其他扩展配置字段，可以在这里处理
	return fmt.Errorf("no token configuration found")
}

// GetTokenPath 获取 Token 文件路径
func (p *ProxmoxProvider) GetTokenPath() string {
	if p.providerUUID == "" {
		return ""
	}
	return filepath.Join("certs", fmt.Sprintf("%s.token", p.providerUUID))
}
