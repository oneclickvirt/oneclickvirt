package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/service/database"
	"oneclickvirt/service/storage"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// ExtractHostFromEndpoint 从端点提取主机地址（使用全局工具函数）
func ExtractHostFromEndpoint(endpoint string) string {
	return utils.ExtractHost(endpoint)
}

// ProviderConfigService Provider配置管理服务
type ProviderConfigService struct{}

const (
	ConfigFilePrefix = "provider_"
	ConfigFileSuffix = ".json"
)

var (
	// 动态获取存储路径
	storageService = storage.GetStorageService()
)

// SaveProviderConfig 保存Provider完整配置（数据库+文件）
func (s *ProviderConfigService) SaveProviderConfig(provider *providerModel.Provider, authConfig *providerModel.ProviderAuthConfig) error {
	now := time.Now()
	provider.LastConfigUpdate = &now
	provider.ConfigVersion++
	provider.AutoConfigured = true

	// 1. 序列化认证配置并保存到数据库
	authConfigJSON, err := json.Marshal(authConfig)
	if err != nil {
		return fmt.Errorf("序列化认证配置失败: %w", err)
	}
	provider.AuthConfig = string(authConfigJSON)

	// 2. 根据配置类型设置相应的数据库字段
	if err := s.setProviderSpecificFields(provider, authConfig); err != nil {
		return fmt.Errorf("设置Provider特定字段失败: %w", err)
	}

	// 3. 保存到数据库
	dbService := database.GetDatabaseService()
	if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Save(provider).Error
	}); err != nil {
		return fmt.Errorf("保存Provider到数据库失败: %w", err)
	}

	// 4. 创建文件备份
	if err := s.createFileBackups(provider, authConfig); err != nil {
		global.APP_LOG.Warn("创建文件备份失败",
			zap.String("provider", provider.Name),
			zap.Error(err))
		// 文件备份失败不影响主流程
	}

	global.APP_LOG.Info("Provider配置保存成功",
		zap.String("provider", provider.Name),
		zap.String("type", provider.Type),
		zap.Int("version", provider.ConfigVersion))

	return nil
}

// setProviderSpecificFields 根据认证配置类型设置Provider的特定字段
func (s *ProviderConfigService) setProviderSpecificFields(provider *providerModel.Provider, authConfig *providerModel.ProviderAuthConfig) error {
	switch authConfig.Type {
	case "lxd", "incus":
		if authConfig.Certificate != nil {
			provider.CertPath = authConfig.Certificate.CertPath
			provider.KeyPath = authConfig.Certificate.KeyPath
			provider.CertFingerprint = authConfig.Certificate.CertFingerprint
			provider.CertContent = authConfig.Certificate.CertContent
			provider.KeyContent = authConfig.Certificate.KeyContent
		}
	case "proxmox":
		if authConfig.Token != nil {
			provider.Token = fmt.Sprintf("%s=%s", authConfig.Token.TokenID, authConfig.Token.TokenSecret)
			provider.TokenContent = s.marshalTokenContent(authConfig.Token)
		}
	}

	return nil
}

// marshalTokenContent 序列化Token内容
func (s *ProviderConfigService) marshalTokenContent(token *providerModel.TokenConfig) string {
	data, _ := json.Marshal(token)
	return string(data)
}

// createFileBackups 创建文件备份
func (s *ProviderConfigService) createFileBackups(provider *providerModel.Provider, authConfig *providerModel.ProviderAuthConfig) error {
	// 确保目录存在
	if err := s.ensureDirectories(); err != nil {
		return err
	}

	// 1. 保存证书文件（如果有）
	if authConfig.Certificate != nil {
		if err := s.saveCertificateFiles(provider.UUID, authConfig.Certificate); err != nil {
			return fmt.Errorf("保存证书文件失败: %w", err)
		}
	}

	// 2. 保存Token文件（如果有）
	if authConfig.Token != nil {
		if err := s.saveTokenFile(provider.UUID, authConfig.Token); err != nil {
			return fmt.Errorf("保存Token文件失败: %w", err)
		}
	}

	// 3. 保存完整配置备份文件
	if err := s.saveConfigBackupFile(provider, authConfig); err != nil {
		return fmt.Errorf("保存配置备份文件失败: %w", err)
	}

	return nil
}

// ensureDirectories 确保所需目录存在（使用全局工具函数）
func (s *ProviderConfigService) ensureDirectories() error {
	return utils.EnsureDirs(storageService.GetConfigsPath(), storageService.GetCertsPath())
}

// saveCertificateFiles 保存证书文件
func (s *ProviderConfigService) saveCertificateFiles(providerUUID string, cert *providerModel.CertConfig) error {
	if cert.CertContent != "" {
		certPath := filepath.Join(storageService.GetCertsPath(), fmt.Sprintf("%s.crt", providerUUID))
		if err := os.WriteFile(certPath, []byte(cert.CertContent), 0644); err != nil {
			return fmt.Errorf("写入证书文件失败: %w", err)
		}
		cert.CertPath = certPath
	}

	if cert.KeyContent != "" {
		keyPath := filepath.Join(storageService.GetCertsPath(), fmt.Sprintf("%s.key", providerUUID))
		if err := os.WriteFile(keyPath, []byte(cert.KeyContent), 0600); err != nil {
			return fmt.Errorf("写入私钥文件失败: %w", err)
		}
		cert.KeyPath = keyPath
	}

	return nil
}

// saveTokenFile 保存Token文件
func (s *ProviderConfigService) saveTokenFile(providerUUID string, token *providerModel.TokenConfig) error {
	tokenData, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化Token失败: %w", err)
	}

	tokenPath := filepath.Join(storageService.GetCertsPath(), fmt.Sprintf("%s.token", providerUUID))
	if err := os.WriteFile(tokenPath, tokenData, 0600); err != nil {
		return fmt.Errorf("写入Token文件失败: %w", err)
	}

	return nil
}

// saveConfigBackupFile 保存完整配置备份文件
func (s *ProviderConfigService) saveConfigBackupFile(provider *providerModel.Provider, authConfig *providerModel.ProviderAuthConfig) error {
	backup := &providerModel.ConfigBackup{
		ProviderID:    provider.ID,
		ProviderUUID:  provider.UUID,
		ProviderName:  provider.Name,
		ProviderType:  provider.Type,
		AuthConfig:    authConfig,
		Status:        provider.Status,
		LastUpdated:   time.Now(),
		ConfigVersion: provider.ConfigVersion,
		CreatedAt:     provider.CreatedAt,
		UpdatedAt:     provider.UpdatedAt,
	}

	backupData, err := json.MarshalIndent(backup, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化配置备份失败: %w", err)
	}

	// 只保存到configs目录按UUID命名
	configPath := filepath.Join(storageService.GetConfigsPath(), fmt.Sprintf("%s%s%s", ConfigFilePrefix, provider.UUID, ConfigFileSuffix))
	if err := os.WriteFile(configPath, backupData, 0644); err != nil {
		return fmt.Errorf("写入配置文件失败: %w", err)
	}

	// 更新数据库中的备份路径
	provider.ConfigBackupPath = configPath

	return nil
}

// LoadProviderConfig 从数据库加载Provider配置
func (s *ProviderConfigService) LoadProviderConfig(providerID uint) (*providerModel.ProviderAuthConfig, error) {
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, providerID).Error; err != nil {
		return nil, fmt.Errorf("Provider不存在: %w", err)
	}

	if provider.AuthConfig == "" {
		return nil, fmt.Errorf("Provider尚未配置认证信息")
	}

	var authConfig providerModel.ProviderAuthConfig
	if err := json.Unmarshal([]byte(provider.AuthConfig), &authConfig); err != nil {
		return nil, fmt.Errorf("解析认证配置失败: %w", err)
	}

	return &authConfig, nil
}

// SyncConfigsAndCerts 同步configs文件夹和certs文件夹的数据
func (s *ProviderConfigService) SyncConfigsAndCerts() error {
	// 确保目录存在
	if err := s.ensureDirectories(); err != nil {
		return err
	}

	// 读取所有configs文件
	configFiles, err := filepath.Glob(filepath.Join(storageService.GetConfigsPath(), ConfigFilePrefix+"*"+ConfigFileSuffix))
	if err != nil {
		return fmt.Errorf("读取配置文件失败: %w", err)
	}

	for _, configFile := range configFiles {
		if err := s.syncSingleConfig(configFile); err != nil {
			global.APP_LOG.Warn("同步配置文件失败",
				zap.String("file", configFile),
				zap.Error(err))
		}
	}

	return nil
}

// syncSingleConfig 同步单个配置文件
func (s *ProviderConfigService) syncSingleConfig(configFilePath string) error {
	// 读取配置文件
	data, err := os.ReadFile(configFilePath)
	if err != nil {
		return fmt.Errorf("读取配置文件失败: %w", err)
	}

	var backup providerModel.ConfigBackup
	if err := json.Unmarshal(data, &backup); err != nil {
		return fmt.Errorf("解析配置文件失败: %w", err)
	}

	// 检查并创建证书文件
	if backup.AuthConfig != nil && backup.AuthConfig.Certificate != nil {
		cert := backup.AuthConfig.Certificate

		// 检查证书文件是否存在
		certPath := filepath.Join(storageService.GetCertsPath(), fmt.Sprintf("%s.crt", backup.ProviderUUID))
		keyPath := filepath.Join(storageService.GetCertsPath(), fmt.Sprintf("%s.key", backup.ProviderUUID))

		// 如果证书内容存在但文件不存在，创建文件
		if cert.CertContent != "" {
			if _, err := os.Stat(certPath); os.IsNotExist(err) {
				if err := os.WriteFile(certPath, []byte(cert.CertContent), 0644); err != nil {
					return fmt.Errorf("创建证书文件失败: %w", err)
				}
				global.APP_LOG.Info("创建证书文件",
					zap.String("provider", backup.ProviderName),
					zap.String("file", certPath))
			}
		}

		if cert.KeyContent != "" {
			if _, err := os.Stat(keyPath); os.IsNotExist(err) {
				if err := os.WriteFile(keyPath, []byte(cert.KeyContent), 0600); err != nil {
					return fmt.Errorf("创建私钥文件失败: %w", err)
				}
				global.APP_LOG.Info("创建私钥文件",
					zap.String("provider", backup.ProviderName),
					zap.String("file", keyPath))
			}
		}

		// 更新配置中的路径
		if cert.CertPath == "" && cert.CertContent != "" {
			cert.CertPath = certPath
		}
		if cert.KeyPath == "" && cert.KeyContent != "" {
			cert.KeyPath = keyPath
		}
	}

	// 检查并创建Token文件
	if backup.AuthConfig != nil && backup.AuthConfig.Token != nil {
		tokenPath := filepath.Join(storageService.GetCertsPath(), fmt.Sprintf("%s.token", backup.ProviderUUID))

		if _, err := os.Stat(tokenPath); os.IsNotExist(err) {
			tokenData, err := json.MarshalIndent(backup.AuthConfig.Token, "", "  ")
			if err != nil {
				return fmt.Errorf("序列化Token失败: %w", err)
			}

			if err := os.WriteFile(tokenPath, tokenData, 0600); err != nil {
				return fmt.Errorf("创建Token文件失败: %w", err)
			}
			global.APP_LOG.Info("创建Token文件",
				zap.String("provider", backup.ProviderName),
				zap.String("file", tokenPath))
		}
	}

	return nil
}

// LoadProviderConfigFromFile 从文件加载Provider配置
func (s *ProviderConfigService) LoadProviderConfigFromFile(providerUUID string) (*providerModel.ConfigBackup, error) {
	configPath := filepath.Join(storageService.GetConfigsPath(), fmt.Sprintf("%s%s%s", ConfigFilePrefix, providerUUID, ConfigFileSuffix))

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("配置文件不存在: %s", configPath)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var backup providerModel.ConfigBackup
	if err := json.Unmarshal(data, &backup); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	return &backup, nil
}

// ExportAllConfigs 导出所有Provider配置到指定目录
func (s *ProviderConfigService) ExportAllConfigs(exportDir string) error {
	if err := os.MkdirAll(exportDir, 0755); err != nil {
		return fmt.Errorf("创建导出目录失败: %w", err)
	}

	var providers []providerModel.Provider
	if err := global.APP_DB.Where("auto_configured = ?", true).Find(&providers).Error; err != nil {
		return fmt.Errorf("查询Provider失败: %w", err)
	}

	for _, provider := range providers {
		if provider.AuthConfig == "" {
			continue
		}

		var authConfig providerModel.ProviderAuthConfig
		if err := json.Unmarshal([]byte(provider.AuthConfig), &authConfig); err != nil {
			global.APP_LOG.Warn("跳过无效配置",
				zap.String("provider", provider.Name),
				zap.Error(err))
			continue
		}

		backup := &providerModel.ConfigBackup{
			ProviderID:    provider.ID,
			ProviderUUID:  provider.UUID,
			ProviderName:  provider.Name,
			ProviderType:  provider.Type,
			AuthConfig:    &authConfig,
			Status:        provider.Status,
			LastUpdated:   *provider.LastConfigUpdate,
			ConfigVersion: provider.ConfigVersion,
			CreatedAt:     provider.CreatedAt,
			UpdatedAt:     provider.UpdatedAt,
		}

		backupData, err := json.MarshalIndent(backup, "", "  ")
		if err != nil {
			global.APP_LOG.Warn("序列化配置失败",
				zap.String("provider", provider.Name),
				zap.Error(err))
			continue
		}

		exportPath := filepath.Join(exportDir, fmt.Sprintf("%s_%s%s", provider.Name, provider.Type, ConfigFileSuffix))
		if err := os.WriteFile(exportPath, backupData, 0644); err != nil {
			global.APP_LOG.Warn("导出配置失败",
				zap.String("provider", provider.Name),
				zap.String("path", exportPath),
				zap.Error(err))
			continue
		}

		global.APP_LOG.Info("导出配置成功",
			zap.String("provider", provider.Name),
			zap.String("path", exportPath))
	}

	return nil
}

// CleanupProviderConfig 清理Provider配置文件
func (s *ProviderConfigService) CleanupProviderConfig(providerUUID string) error {
	// 清理证书文件
	certPath := filepath.Join(storageService.GetCertsPath(), fmt.Sprintf("%s.crt", providerUUID))
	keyPath := filepath.Join(storageService.GetCertsPath(), fmt.Sprintf("%s.key", providerUUID))
	tokenPath := filepath.Join(storageService.GetCertsPath(), fmt.Sprintf("%s.token", providerUUID))
	configPath := filepath.Join(storageService.GetConfigsPath(), fmt.Sprintf("%s%s%s", ConfigFilePrefix, providerUUID, ConfigFileSuffix))

	files := []string{certPath, keyPath, tokenPath, configPath}
	for _, file := range files {
		if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
			global.APP_LOG.Warn("删除文件失败",
				zap.String("path", file),
				zap.Error(err))
		}
	}

	return nil
}

// CreateAuthConfigFromCertInfo 从CertInfo创建认证配置
func (s *ProviderConfigService) CreateAuthConfigFromCertInfo(provider *providerModel.Provider, certInfo *CertInfo, endpoint string) *providerModel.ProviderAuthConfig {
	return &providerModel.ProviderAuthConfig{
		Type:     provider.Type,
		Endpoint: endpoint,
		SSH: &providerModel.SSHConfig{
			Host:       ExtractHostFromEndpoint(provider.Endpoint),
			Port:       provider.SSHPort,
			Username:   provider.Username,
			Password:   provider.Password,
			KeyContent: provider.SSHKey,
		},
		Certificate: &providerModel.CertConfig{
			CertPath:        certInfo.CertPath,
			KeyPath:         certInfo.KeyPath,
			CertFingerprint: certInfo.CertFingerprint,
			CertContent:     certInfo.CertContent,
			KeyContent:      certInfo.KeyContent,
		},
	}
}

// CreateAuthConfigFromTokenInfo 从TokenInfo创建认证配置
func (s *ProviderConfigService) CreateAuthConfigFromTokenInfo(provider *providerModel.Provider, tokenInfo *TokenInfo, endpoint string) *providerModel.ProviderAuthConfig {
	return &providerModel.ProviderAuthConfig{
		Type:     provider.Type,
		Endpoint: endpoint,
		SSH: &providerModel.SSHConfig{
			Host:       ExtractHostFromEndpoint(provider.Endpoint),
			Port:       provider.SSHPort,
			Username:   provider.Username,
			Password:   provider.Password,
			KeyContent: provider.SSHKey,
		},
		Token: &providerModel.TokenConfig{
			TokenID:     tokenInfo.TokenID,
			TokenSecret: tokenInfo.TokenSecret,
			Username:    tokenInfo.Username,
		},
	}
}
