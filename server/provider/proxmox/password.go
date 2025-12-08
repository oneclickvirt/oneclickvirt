package proxmox

import (
	"context"
	"fmt"

	"oneclickvirt/global"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// SetInstancePassword 设置实例密码
func (p *ProxmoxProvider) SetInstancePassword(ctx context.Context, instanceID, password string) error {
	if !p.connected {
		return fmt.Errorf("provider not connected")
	}

	// 根据执行规则判断使用哪种方式
	if p.shouldUseAPI() {
		if err := p.apiSetInstancePassword(ctx, instanceID, password); err == nil {
			global.APP_LOG.Info("Proxmox API设置实例密码成功",
				zap.String("instanceID", utils.TruncateString(instanceID, 50)))
			return nil
		} else {
			global.APP_LOG.Warn("Proxmox API设置实例密码失败", zap.Error(err))

			// 检查是否可回退到SSH并确保SSH健康
			if fallbackErr := p.ensureSSHBeforeFallback(err, "设置实例密码"); fallbackErr != nil {
				return fallbackErr
			}
		}
	}

	// 使用SSH方式
	if !p.shouldUseSSH() {
		if p.config.ExecutionRule == "api_only" {
			return fmt.Errorf("执行规则为api_only，但API不可用且不允许使用SSH")
		}
		return fmt.Errorf("SSH连接不可用")
	}

	return p.sshSetInstancePassword(ctx, instanceID, password)
}

// ResetInstancePassword 重置实例密码
func (p *ProxmoxProvider) ResetInstancePassword(ctx context.Context, instanceID string) (string, error) {
	if !p.connected {
		return "", fmt.Errorf("provider not connected")
	}

	// 生成随机密码
	newPassword := p.generateRandomPassword()

	// 设置新密码
	err := p.SetInstancePassword(ctx, instanceID, newPassword)
	if err != nil {
		return "", err
	}

	return newPassword, nil
}

// generateRandomPassword 生成随机密码（仅包含数字和大小写英文字母，长度不低于8位）
func (p *ProxmoxProvider) generateRandomPassword() string {
	return utils.GenerateInstancePassword()
}
