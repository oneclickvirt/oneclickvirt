package lxd

import (
	"context"
	"fmt"

	"oneclickvirt/global"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// SetInstancePassword 设置实例密码
func (l *LXDProvider) SetInstancePassword(ctx context.Context, instanceID, password string) error {
	if !l.connected {
		return fmt.Errorf("provider not connected")
	}

	var apiErr error
	// LXD 的 /exec API 返回 Accepted/OK 只代表操作已提交，旧版本和部分镜像上
	// 并不能可靠反映 chpasswd 是否真的成功。因此只在 api_only 或 SSH 不可用时
	// 把 API 结果作为最终结果；常规场景继续走 SSH/lxc exec 的可验证设置流程。
	if l.shouldUseAPI() {
		if err := l.apiSetInstancePassword(ctx, instanceID, password); err == nil {
			apiErr = nil
			global.APP_LOG.Info("LXD API调用已提交 - 设置实例密码，继续使用SSH确认写入",
				zap.String("instanceID", utils.TruncateString(instanceID, 12)))
			if !l.shouldUseSSH() {
				return nil
			}
		} else {
			apiErr = err
			global.APP_LOG.Warn("LXD API设置实例密码失败，准备回退SSH",
				zap.String("instanceID", utils.TruncateString(instanceID, 12)),
				zap.Error(err))
			if fallbackErr := l.ensureSSHBeforeFallback(err, "设置实例密码"); fallbackErr != nil {
				return fallbackErr
			}
		}
	}

	if l.shouldUseSSH() {
		return l.sshSetInstancePassword(ctx, instanceID, password)
	}

	if l.config.ExecutionRule == "api_only" {
		if apiErr == nil && l.shouldUseAPI() {
			return nil
		}
		return fmt.Errorf("执行规则为api_only，但API不可用且不允许使用SSH")
	}
	return fmt.Errorf("SSH连接不可用")
}

// ResetInstancePassword 重置实例密码
func (l *LXDProvider) ResetInstancePassword(ctx context.Context, instanceID string) (string, error) {
	if !l.connected {
		return "", fmt.Errorf("provider not connected")
	}

	// 密码重置通常只通过SSH进行，因为需要进入实例内部
	// 如果执行规则不允许使用SSH，则返回错误
	if !l.shouldUseSSH() {
		return "", fmt.Errorf("执行规则不允许使用SSH，无法重置实例密码")
	}

	// 生成随机密码
	newPassword := l.generateRandomPassword()

	// 设置新密码
	err := l.SetInstancePassword(ctx, instanceID, newPassword)
	if err != nil {
		return "", err
	}

	return newPassword, nil
}

// generateRandomPassword 生成随机密码（仅包含数字和大小写英文字母，长度不低于8位）
func (l *LXDProvider) generateRandomPassword() string {
	return utils.GenerateInstancePassword()
}
