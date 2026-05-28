package incus

import (
	"context"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// SetInstancePassword 设置实例密码
func (i *IncusProvider) SetInstancePassword(ctx context.Context, instanceID, password string) error {
	if !i.connected {
		return fmt.Errorf("provider not connected")
	}

	// 根据执行规则判断使用哪种方式
	if i.shouldUseAPI() {
		if err := i.apiSetInstancePassword(ctx, instanceID, password); err == nil {
			global.APP_LOG.Info("Incus API调用成功 - 设置实例密码", zap.String("instanceID", utils.TruncateString(instanceID, 12)))
			return nil
		} else {
			global.APP_LOG.Warn("Incus API失败", zap.Error(err))

			// 检查是否可回退到SSH并确保SSH健康
			if fallbackErr := i.ensureSSHBeforeFallback(err, "设置实例密码"); fallbackErr != nil {
				return fallbackErr
			}
		}
	}

	// 如果执行规则不允许使用SSH，则返回错误
	if !i.shouldUseSSH() {
		if i.config.ExecutionRule == "api_only" {
			return fmt.Errorf("执行规则为api_only，但API不可用且不允许使用SSH")
		}
		return fmt.Errorf("SSH连接不可用")
	}

	// SSH 方式
	return i.sshSetInstancePassword(instanceID, password)
}

// ResetInstancePassword 重置实例密码
func (i *IncusProvider) ResetInstancePassword(ctx context.Context, instanceID string) (string, error) {
	if !i.connected {
		return "", fmt.Errorf("provider not connected")
	}

	// 生成随机密码
	newPassword := i.generateRandomPassword()

	// 设置新密码
	err := i.SetInstancePassword(ctx, instanceID, newPassword)
	if err != nil {
		return "", err
	}

	return newPassword, nil
}

// generateRandomPassword 生成随机密码（仅包含数字和大小写英文字母，长度不低于8位）
func (i *IncusProvider) generateRandomPassword() string {
	return utils.GenerateInstancePassword()
}

// sshSetInstancePassword 通过SSH设置实例密码
func (i *IncusProvider) sshSetInstancePassword(instanceID, password string) error {
	// 精确匹配实例名并读取状态，避免 grep 模式注入与误匹配
	simpleCheckCmd := fmt.Sprintf(
		"incus list --format csv -c n,s | awk -F, -v n=%s '$1==n {print $2}'",
		shellSingleQuote(instanceID),
	)
	output, err := i.sshClient.Execute(simpleCheckCmd)
	if err != nil {
		global.APP_LOG.Error("检查Incus实例状态失败",
			zap.String("instanceID", instanceID),
			zap.Error(err))
		return fmt.Errorf("检查实例状态失败: %w", err)
	}
	status := strings.TrimSpace(output)
	// 检查实例是否存在且运行
	if status == "" {
		return fmt.Errorf("实例 %s 不存在", instanceID)
	}
	if !strings.EqualFold(status, "RUNNING") {
		return fmt.Errorf("实例 %s 未运行，无法设置密码", instanceID)
	}
	// 使用临时脚本设置密码（支持超时回退），避免 agent 模式下 WebSocket 连接中断
	script := utils.BuildTempScript(utils.TempScriptConfig{
		PrimaryCmd:     buildIncusChpasswdCommand(instanceID, password),
		FallbackCmd:    buildIncusChpasswdCommand(instanceID, password),
		TimeoutSeconds: 30,
	})
	_, err = i.sshClient.ExecuteViaTempScript(script, nil, 120*time.Second)
	if err != nil {
		global.APP_LOG.Error("设置Incus实例密码失败",
			zap.String("instanceID", instanceID),
			zap.Error(err))
		return fmt.Errorf("设置实例密码失败: %w", err)
	}
	global.APP_LOG.Info("Incus实例密码设置成功(SSH)",
		zap.String("instanceID", utils.TruncateString(instanceID, 12)))

	return nil
}
