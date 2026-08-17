package proxmox

import (
	"context"
	"fmt"

	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

func (p *ProxmoxProvider) ListInstances(ctx context.Context) ([]provider.Instance, error) {
	if !p.connected {
		return nil, fmt.Errorf("not connected")
	}

	// 根据执行规则判断使用哪种方式
	if p.shouldUseAPI() {
		instances, err := p.apiListInstances(ctx)
		if err == nil {
			global.APP_LOG.Debug("Proxmox API调用成功 - 获取实例列表")
			return instances, nil
		}
		if fallbackErr := p.ensureSSHBeforeFallback(err, "获取实例列表"); fallbackErr != nil {
			return nil, fallbackErr
		}
	}

	// 使用SSH方式
	if !p.shouldUseSSH() {
		return nil, fmt.Errorf("执行规则不允许使用SSH")
	}

	return p.sshListInstances(ctx)
}

func (p *ProxmoxProvider) CreateInstance(ctx context.Context, config provider.InstanceConfig) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}
	if err := p.ensureNATIPv4DataPlane(ctx, config); err != nil {
		return err
	}

	// 根据执行规则判断使用哪种方式
	forceSSHInstaller := p.shouldForceSSHForInstaller(ctx, &config)
	if p.shouldUseAPI() && !forceSSHInstaller {
		err := p.apiCreateInstance(ctx, config)
		if err == nil {
			global.APP_LOG.Debug("Proxmox API调用成功 - 创建实例", zap.String("name", utils.TruncateString(config.Name, 50)))
			return nil
		}
		if proxmoxAPICreateFallbackBlocked(err) {
			global.APP_LOG.Error("PVE API创建远端状态不确定，禁止SSH回退", zap.Error(err))
			return err
		}
		if fallbackErr := p.ensureSSHBeforeFallback(err, "创建实例"); fallbackErr != nil {
			return fallbackErr
		}
	}

	// 使用SSH方式
	if !p.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	return p.sshCreateInstance(ctx, config)
}

func (p *ProxmoxProvider) CreateInstanceWithProgress(ctx context.Context, config provider.InstanceConfig, progressCallback provider.ProgressCallback) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}
	if p.requiresNATIPv4DataPlane(config) && progressCallback != nil {
		progressCallback(5, "检查PVE NAT IPv4数据面...")
	}
	if err := p.ensureNATIPv4DataPlane(ctx, config); err != nil {
		return err
	}

	// 根据执行规则判断使用哪种方式
	forceSSHInstaller := p.shouldForceSSHForInstaller(ctx, &config)
	if p.shouldUseAPI() && !forceSSHInstaller {
		err := p.apiCreateInstanceWithProgress(ctx, config, progressCallback)
		if err == nil {
			global.APP_LOG.Debug("Proxmox API调用成功 - 创建实例", zap.String("name", utils.TruncateString(config.Name, 50)))
			return nil
		}
		if proxmoxAPICreateFallbackBlocked(err) {
			global.APP_LOG.Error("PVE API创建远端状态不确定，禁止SSH回退", zap.Error(err))
			return err
		}
		if fallbackErr := p.ensureSSHBeforeFallback(err, "创建实例"); fallbackErr != nil {
			return fallbackErr
		}
	}

	// 使用SSH方式
	if !p.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	return p.sshCreateInstanceWithProgress(ctx, config, progressCallback)
}

func (p *ProxmoxProvider) StartInstance(ctx context.Context, id string) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}

	// 根据执行规则判断使用哪种方式
	if p.shouldUseAPI() {
		err := p.apiStartInstance(ctx, id)
		if err == nil {
			global.APP_LOG.Debug("Proxmox API调用成功 - 启动实例", zap.String("id", utils.TruncateString(id, 50)))
			return nil
		}
		if fallbackErr := p.ensureSSHBeforeFallback(err, "启动实例"); fallbackErr != nil {
			return fallbackErr
		}
	}

	// 使用SSH方式
	if !p.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	return p.sshStartInstance(ctx, id)
}

func (p *ProxmoxProvider) StopInstance(ctx context.Context, id string) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}

	// 根据执行规则判断使用哪种方式
	if p.shouldUseAPI() {
		err := p.apiStopInstance(ctx, id)
		if err == nil {
			global.APP_LOG.Debug("Proxmox API调用成功 - 停止实例", zap.String("id", utils.TruncateString(id, 50)))
			return nil
		}
		if fallbackErr := p.ensureSSHBeforeFallback(err, "停止实例"); fallbackErr != nil {
			return fallbackErr
		}
	}

	// 使用SSH方式
	if !p.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	return p.sshStopInstance(ctx, id)
}

func (p *ProxmoxProvider) RestartInstance(ctx context.Context, id string) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}

	// 根据执行规则判断使用哪种方式
	if p.shouldUseAPI() {
		err := p.apiRestartInstance(ctx, id)
		if err == nil {
			global.APP_LOG.Debug("Proxmox API调用成功 - 重启实例", zap.String("id", utils.TruncateString(id, 50)))
			return nil
		}
		if fallbackErr := p.ensureSSHBeforeFallback(err, "重启实例"); fallbackErr != nil {
			return fallbackErr
		}
	}

	// 使用SSH方式
	if !p.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	return p.sshRestartInstance(ctx, id)
}

func (p *ProxmoxProvider) DeleteInstance(ctx context.Context, id string) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}

	// 根据执行规则判断使用哪种方式
	if p.shouldUseAPI() {
		err := p.apiDeleteInstance(ctx, id)
		if err == nil {
			if reconcileErr := p.reconcileAllRoutedIPv6Neighbors(); reconcileErr != nil {
				global.APP_LOG.Warn("Proxmox实例已删除，但隧道路由IPv6邻居清理失败", zap.String("id", utils.TruncateString(id, 50)), zap.Error(reconcileErr))
			}
			global.APP_LOG.Debug("Proxmox API调用成功 - 删除实例", zap.String("id", utils.TruncateString(id, 50)))
			return nil
		}
		if fallbackErr := p.ensureSSHBeforeFallback(err, "删除实例"); fallbackErr != nil {
			return fallbackErr
		}
	}

	// 使用SSH方式
	if !p.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	if err := p.sshDeleteInstance(ctx, id); err != nil {
		return err
	}
	if reconcileErr := p.reconcileAllRoutedIPv6Neighbors(); reconcileErr != nil {
		global.APP_LOG.Warn("Proxmox实例已删除，但隧道路由IPv6邻居清理失败", zap.String("id", utils.TruncateString(id, 50)), zap.Error(reconcileErr))
	}
	return nil
}

func (p *ProxmoxProvider) GetInstance(ctx context.Context, id string) (*provider.Instance, error) {
	instances, err := p.ListInstances(ctx)
	if err != nil {
		return nil, err
	}

	for _, instance := range instances {
		if instance.ID == id || instance.Name == id {
			return &instance, nil
		}
	}

	return nil, fmt.Errorf("instance not found: %s", id)
}
