package provider

import (
	"context"
	"fmt"
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	provider2 "oneclickvirt/service/provider"
)

// AutoConfigureProviderWithStream 带实时输出的自动配置Provider
func (s *Service) AutoConfigureProviderWithStream(providerID uint, outputChan chan<- string) error {
	return s.AutoConfigureProviderWithStreamContext(context.Background(), providerID, outputChan)
}

// AutoConfigureProviderWithStreamContext 带实时输出和context控制的自动配置Provider
func (s *Service) AutoConfigureProviderWithStreamContext(ctx context.Context, providerID uint, outputChan chan<- string) error {
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, providerID).Error; err != nil {
		outputChan <- fmt.Sprintf("错误: Provider不存在 (ID: %d)", providerID)
		return fmt.Errorf("Provider不存在")
	}

	// 检查context是否已取消
	select {
	case <-ctx.Done():
		outputChan <- "操作已取消"
		return ctx.Err()
	default:
	}

	// 支持LXD、Incus和Proxmox
	if provider.Type != "lxd" && provider.Type != "incus" && provider.Type != "proxmox" {
		outputChan <- fmt.Sprintf("错误: 不支持的Provider类型: %s (只支持LXD、Incus和Proxmox)", provider.Type)
		return fmt.Errorf("只支持为LXD、Incus和Proxmox生成配置")
	}

	outputChan <- fmt.Sprintf("=== 开始自动配置 %s Provider: %s ===", strings.ToUpper(provider.Type), provider.Name)
	outputChan <- fmt.Sprintf("Provider地址: %s", provider.Endpoint)
	outputChan <- fmt.Sprintf("SSH用户: %s", provider.Username)

	certService := &provider2.CertService{}

	// 执行自动配置（传递context以便取消）
	err := certService.AutoConfigureProviderWithStreamContext(ctx, &provider, outputChan)
	if err != nil {
		if ctx.Err() != nil {
			outputChan <- "操作已取消"
			return ctx.Err()
		}
		outputChan <- fmt.Sprintf("自动配置失败: %s", err.Error())
		return fmt.Errorf("自动配置失败: %w", err)
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

	outputChan <- fmt.Sprintf("✅ %s", message)
	outputChan <- "✅ 自动配置流程完成，配置信息已统一管理"

	return nil
}

// GetProviderStatus 获取Provider状态详情
func (s *Service) GetProviderStatus(providerID uint) (*admin.ProviderStatusResponse, error) {
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, providerID).Error; err != nil {
		return nil, fmt.Errorf("Provider不存在")
	}

	response := &admin.ProviderStatusResponse{
		ID:              provider.ID,
		UUID:            provider.UUID,
		Name:            provider.Name,
		Type:            provider.Type,
		Status:          provider.Status,
		APIStatus:       provider.APIStatus,
		SSHStatus:       provider.SSHStatus,
		LastAPICheck:    provider.LastAPICheck,
		LastSSHCheck:    provider.LastSSHCheck,
		CertPath:        provider.CertPath,
		KeyPath:         provider.KeyPath,
		CertFingerprint: provider.CertFingerprint,
		// 资源信息
		NodeCPUCores:     provider.NodeCPUCores,
		NodeMemoryTotal:  provider.NodeMemoryTotal,
		NodeDiskTotal:    provider.NodeDiskTotal,
		ResourceSynced:   provider.ResourceSynced,
		ResourceSyncedAt: provider.ResourceSyncedAt,
		// 冻结信息
		IsFrozen:     provider.IsFrozen,
		FrozenReason: provider.FrozenReason,
		FrozenAt:     provider.FrozenAt,
	}

	return response, nil
}
