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
			global.APP_LOG.Info("Proxmox API调用成功 - 获取实例列表")
			return instances, nil
		}
		global.APP_LOG.Warn("Proxmox API失败 - 获取实例列表", zap.Error(err))

		// 检查是否可以回退到SSH
		if !p.shouldFallbackToSSH() {
			return nil, fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
		}
		global.APP_LOG.Info("回退到SSH方式 - 获取实例列表")
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

	// 根据执行规则判断使用哪种方式
	if p.shouldUseAPI() {
		err := p.apiCreateInstance(ctx, config)
		if err == nil {
			global.APP_LOG.Info("Proxmox API调用成功 - 创建实例", zap.String("name", utils.TruncateString(config.Name, 50)))
			return nil
		}
		global.APP_LOG.Warn("Proxmox API失败 - 创建实例", zap.String("name", utils.TruncateString(config.Name, 50)), zap.Error(err))

		// 检查是否可以回退到SSH
		if !p.shouldFallbackToSSH() {
			return fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
		}
		global.APP_LOG.Info("回退到SSH方式 - 创建实例", zap.String("name", utils.TruncateString(config.Name, 50)))
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

	// 根据执行规则判断使用哪种方式
	if p.shouldUseAPI() {
		err := p.apiCreateInstanceWithProgress(ctx, config, progressCallback)
		if err == nil {
			global.APP_LOG.Info("Proxmox API调用成功 - 创建实例", zap.String("name", utils.TruncateString(config.Name, 50)))
			return nil
		}
		global.APP_LOG.Warn("Proxmox API失败 - 创建实例", zap.String("name", utils.TruncateString(config.Name, 50)), zap.Error(err))

		// 检查是否可以回退到SSH
		if !p.shouldFallbackToSSH() {
			return fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
		}
		global.APP_LOG.Info("回退到SSH方式 - 创建实例", zap.String("name", utils.TruncateString(config.Name, 50)))
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
			global.APP_LOG.Info("Proxmox API调用成功 - 启动实例", zap.String("id", utils.TruncateString(id, 50)))
			return nil
		}
		global.APP_LOG.Warn("Proxmox API失败 - 启动实例", zap.String("id", utils.TruncateString(id, 50)), zap.Error(err))

		// 检查是否可以回退到SSH
		if !p.shouldFallbackToSSH() {
			return fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
		}
		global.APP_LOG.Info("回退到SSH方式 - 启动实例", zap.String("id", utils.TruncateString(id, 50)))
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
			global.APP_LOG.Info("Proxmox API调用成功 - 停止实例", zap.String("id", utils.TruncateString(id, 50)))
			return nil
		}
		global.APP_LOG.Warn("Proxmox API失败 - 停止实例", zap.String("id", utils.TruncateString(id, 50)), zap.Error(err))

		// 检查是否可以回退到SSH
		if !p.shouldFallbackToSSH() {
			return fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
		}
		global.APP_LOG.Info("回退到SSH方式 - 停止实例", zap.String("id", utils.TruncateString(id, 50)))
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
			global.APP_LOG.Info("Proxmox API调用成功 - 重启实例", zap.String("id", utils.TruncateString(id, 50)))
			return nil
		}
		global.APP_LOG.Warn("Proxmox API失败 - 重启实例", zap.String("id", utils.TruncateString(id, 50)), zap.Error(err))

		// 检查是否可以回退到SSH
		if !p.shouldFallbackToSSH() {
			return fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
		}
		global.APP_LOG.Info("回退到SSH方式 - 重启实例", zap.String("id", utils.TruncateString(id, 50)))
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
			global.APP_LOG.Info("Proxmox API调用成功 - 删除实例", zap.String("id", utils.TruncateString(id, 50)))
			return nil
		}
		global.APP_LOG.Warn("Proxmox API失败 - 删除实例", zap.String("id", utils.TruncateString(id, 50)), zap.Error(err))

		// 检查是否可以回退到SSH
		if !p.shouldFallbackToSSH() {
			return fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
		}
		global.APP_LOG.Info("回退到SSH方式 - 删除实例", zap.String("id", utils.TruncateString(id, 50)))
	}

	// 使用SSH方式
	if !p.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	return p.sshDeleteInstance(ctx, id)
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
