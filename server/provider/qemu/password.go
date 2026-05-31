package qemu

import (
	"context"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// SetInstancePassword 设置虚拟机密码
func (p *QEMUProvider) SetInstancePassword(ctx context.Context, instanceID, password string) error {
	if !p.connected || p.sshClient == nil {
		return fmt.Errorf("not connected")
	}
	return p.sshSetPassword(ctx, instanceID, password)
}

// ResetInstancePassword 重置虚拟机密码
func (p *QEMUProvider) ResetInstancePassword(ctx context.Context, instanceID string) (string, error) {
	if !p.connected || p.sshClient == nil {
		return "", fmt.Errorf("not connected")
	}

	password := utils.GenerateInstancePassword()
	if err := p.sshSetPassword(ctx, instanceID, password); err != nil {
		return "", err
	}
	return password, nil
}

// sshSetPassword 通过SSH设置VM密码
func (p *QEMUProvider) sshSetPassword(ctx context.Context, instanceID, password string) error {
	global.APP_LOG.Info("设置QEMU虚拟机密码",
		zap.String("instance", utils.TruncateString(instanceID, 32)))

	// 检查VM状态
	statusOutput, err := p.sshClient.Execute(fmt.Sprintf("virsh domstate '%s' 2>/dev/null", instanceID))
	if err != nil {
		return fmt.Errorf("failed to check VM status: %w", err)
	}

	status := strings.ToLower(strings.TrimSpace(statusOutput))

	// 方法1: 如果guest-agent可用，使用 virsh set-user-password
	if strings.Contains(status, "running") {
		output, err := p.sshClient.Execute(fmt.Sprintf(
			"virsh set-user-password '%s' root '%s' 2>&1", instanceID, password))
		if err == nil && !strings.Contains(output, "error") {
			global.APP_LOG.Info("通过guest-agent设置密码成功",
				zap.String("instance", utils.TruncateString(instanceID, 32)))
			return nil
		}
		global.APP_LOG.Debug("guest-agent设置密码失败，尝试其他方法",
			zap.String("output", utils.TruncateString(output, 200)))
	}

	// 方法2: 通过SSH连接到VM内部设置密码
	vmIP := p.getVMIPAddress(ctx, instanceID)
	if vmIP != "" {
		// 通过SSH连接到VM内部修改密码（使用SSHPASS环境变量避免密码出现在进程列表中）
		escapedPw := strings.ReplaceAll(password, "'", "'\\''")
		chpasswdCmd := fmt.Sprintf(
			"SSHPASS='%s' sshpass -e ssh -o StrictHostKeyChecking=no -o ConnectTimeout=5 root@%s 'printf \"root:%%s\" \"%s\" | chpasswd' 2>/dev/null",
			escapedPw, vmIP, escapedPw)
		_, err := p.sshClient.Execute(chpasswdCmd)
		if err == nil {
			global.APP_LOG.Info("通过SSH设置密码成功",
				zap.String("instance", utils.TruncateString(instanceID, 32)))
			return nil
		}
	}

	// 方法3: 使用 virt-customize (离线模式,需要VM关机)
	if strings.Contains(status, "shut off") || strings.Contains(status, "shutoff") {
		// 查找VM的磁盘文件
		output, err := p.sshClient.Execute(fmt.Sprintf(
			"virsh domblklist '%s' 2>/dev/null | grep -E '\\.(qcow2|img|raw)' | awk '{print $2}'", instanceID))
		if err == nil {
			diskPath := strings.TrimSpace(output)
			if diskPath != "" {
				output, err := p.sshClient.Execute(fmt.Sprintf(
					"virt-customize -a '%s' --root-password password:'%s' 2>&1", diskPath, password))
				if err == nil && !strings.Contains(output, "error") {
					global.APP_LOG.Info("通过virt-customize设置密码成功",
						zap.String("instance", utils.TruncateString(instanceID, 32)))
					return nil
				}
			}
		}
	}

	// 方法4: 等待VM上线后通过 guestfish/virt-cat 修改shadow文件
	// 这是最后的回退方案
	if strings.Contains(status, "running") {
		// 等待一段时间再次尝试guest-agent
		if err := sleepWithContext(ctx, 5*time.Second); err != nil {
			return fmt.Errorf("waiting before password retry cancelled: %w", err)
		}
		output, err := p.sshClient.Execute(fmt.Sprintf(
			"virsh set-user-password '%s' root '%s' 2>&1", instanceID, password))
		if err == nil && !strings.Contains(output, "error") {
			return nil
		}
		global.APP_LOG.Warn("所有密码设置方法均失败",
			zap.String("instance", utils.TruncateString(instanceID, 32)),
			zap.String("lastOutput", utils.TruncateString(output, 200)))
	}

	return fmt.Errorf("failed to set password for VM %s: all methods exhausted", instanceID)
}
