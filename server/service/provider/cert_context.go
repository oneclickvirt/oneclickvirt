package provider

import (
	"context"

	"oneclickvirt/model/provider"
	"oneclickvirt/utils"
)

// checkConfigContext keeps cancellation checks at every boundary between local
// work and a remote provider call. The remote call itself is interrupted by
// watchSSHClientCancellation below.
func checkConfigContext(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

// watchSSHClientCancellation closes the short-lived SSH client used by a
// configuration task when its context is cancelled. Closing this private
// client interrupts SFTP and SSH command sessions without touching the
// provider-wide SSH pool.
func watchSSHClientCancellation(ctx context.Context, client *utils.SSHClient) func() {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = client.Close()
		case <-done:
		}
	}()
	return func() { close(done) }
}

// autoConfigureLXDWithStreamContext LXD自动配置的context版本
func (cs *CertService) autoConfigureLXDWithStreamContext(ctx context.Context, prov *provider.Provider, outputChan chan<- string) error {
	// 检查context
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	return cs.autoConfigureLXDWithStream(ctx, prov, outputChan)
}

// autoConfigureIncusWithStreamContext Incus自动配置的context版本
func (cs *CertService) autoConfigureIncusWithStreamContext(ctx context.Context, prov *provider.Provider, outputChan chan<- string) error {
	// 检查context
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	return cs.autoConfigureIncusWithStream(ctx, prov, outputChan)
}

// autoConfigureProxmoxWithStreamContext Proxmox自动配置的context版本
func (cs *CertService) autoConfigureProxmoxWithStreamContext(ctx context.Context, prov *provider.Provider, outputChan chan<- string) error {
	// 检查context
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	return cs.autoConfigureProxmoxWithStream(ctx, prov, outputChan)
}
