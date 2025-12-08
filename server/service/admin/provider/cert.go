package provider

import (
	"fmt"
	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	provider2 "oneclickvirt/service/provider"
)

// GenerateProviderCert 为Provider生成证书配置
func (s *Service) GenerateProviderCert(providerID uint) (string, error) {
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, providerID).Error; err != nil {
		return "", fmt.Errorf("Provider不存在")
	}

	// 支持LXD、Incus和Proxmox
	if provider.Type != "lxd" && provider.Type != "incus" && provider.Type != "proxmox" {
		return "", fmt.Errorf("只支持为LXD、Incus和Proxmox生成配置")
	}

	certService := &provider2.CertService{}

	// 执行自动配置（现在包含完整的数据库和文件保存）
	err := certService.AutoConfigureProvider(&provider)
	if err != nil {
		return "", fmt.Errorf("自动配置失败: %w", err)
	}

	// 根据类型返回不同的成功消息
	var message string
	switch provider.Type {
	case "proxmox":
		message = "Proxmox VE API 自动配置成功，认证配置已保存到数据库和文件"
	case "lxd":
		message = "LXD 自动配置成功，证书已安装并保存到数据库和文件"
	case "incus":
		message = "Incus 自动配置成功，证书已安装并保存到数据库和文件"
	}

	return message, nil
}
