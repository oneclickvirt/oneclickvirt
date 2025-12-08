package provider

import (
	"context"

	"oneclickvirt/model/provider"
)

// 这个文件包含支持context的wrapper方法

// autoConfigureLXDWithStreamContext LXD自动配置的context版本
func (cs *CertService) autoConfigureLXDWithStreamContext(ctx context.Context, prov *provider.Provider, outputChan chan<- string) error {
	// 检查context
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// 调用原始方法（原始方法内部应该检查长时间操作）
	return cs.autoConfigureLXDWithStream(prov, outputChan)
}

// autoConfigureIncusWithStreamContext Incus自动配置的context版本
func (cs *CertService) autoConfigureIncusWithStreamContext(ctx context.Context, prov *provider.Provider, outputChan chan<- string) error {
	// 检查context
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// 调用原始方法
	return cs.autoConfigureIncusWithStream(prov, outputChan)
}

// autoConfigureProxmoxWithStreamContext Proxmox自动配置的context版本
func (cs *CertService) autoConfigureProxmoxWithStreamContext(ctx context.Context, prov *provider.Provider, outputChan chan<- string) error {
	// 检查context
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// 调用原始方法
	return cs.autoConfigureProxmoxWithStream(prov, outputChan)
}
