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
	global.APP_LOG.Info("设置QEMU实例密码",
		zap.String("instance", utils.TruncateString(instanceID, 32)))

	if p.isLXCInstance(instanceID) {
		rootfs := fmt.Sprintf("%s/%s/rootfs", LXCBaseDir, qemuSafeFileComponent(instanceID))
		cmd := fmt.Sprintf("test -d %s && chroot %s /bin/sh -c %s", shellSingleQuote(rootfs), shellSingleQuote(rootfs), shellSingleQuote("echo root:"+password+" | chpasswd"))
		if output, err := p.sshClient.Execute(cmd + " 2>&1"); err != nil {
			return fmt.Errorf("failed to set LXC password: %s, %w", utils.TruncateString(output, 200), err)
		}
		return nil
	}

	// 检查VM状态
	statusOutput, err := p.sshClient.Execute(fmt.Sprintf("virsh -c qemu:///system domstate %s 2>/dev/null", shellSingleQuote(instanceID)))
	if err != nil {
		return fmt.Errorf("failed to check VM status: %w", err)
	}

	status := strings.ToLower(strings.TrimSpace(statusOutput))

	// 方法1: 如果guest-agent可用，使用 virsh set-user-password
	if strings.Contains(status, "running") {
		output, err := p.sshClient.Execute(fmt.Sprintf(
			"virsh -c qemu:///system set-user-password %s root %s 2>&1",
			shellSingleQuote(instanceID),
			shellSingleQuote(password)))
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
		remoteCmd := fmt.Sprintf("printf 'root:%%s' %s | chpasswd", shellSingleQuote(password))
		chpasswdCmd := fmt.Sprintf(
			"SSHPASS=%s sshpass -e ssh -o StrictHostKeyChecking=no -o ConnectTimeout=5 %s %s 2>/dev/null",
			shellSingleQuote(password),
			shellSingleQuote("root@"+vmIP),
			shellSingleQuote(remoteCmd))
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
			"virsh -c qemu:///system domblklist %s 2>/dev/null | grep -E '\\.(qcow2|img|raw)' | awk '{print $2}'",
			shellSingleQuote(instanceID)))
		if err == nil {
			diskPath := strings.TrimSpace(output)
			if diskPath != "" {
				output, err := p.sshClient.Execute(fmt.Sprintf(
					"virt-customize -a %s --root-password %s 2>&1",
					shellSingleQuote(diskPath),
					shellSingleQuote("password:"+password)))
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
			"virsh -c qemu:///system set-user-password %s root %s 2>&1",
			shellSingleQuote(instanceID),
			shellSingleQuote(password)))
		if err == nil && !strings.Contains(output, "error") {
			return nil
		}
		global.APP_LOG.Warn("所有密码设置方法均失败",
			zap.String("instance", utils.TruncateString(instanceID, 32)),
			zap.String("lastOutput", utils.TruncateString(output, 200)))
	}

	return fmt.Errorf("failed to set password for VM %s: all methods exhausted", instanceID)
}
