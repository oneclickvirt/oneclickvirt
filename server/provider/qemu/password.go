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

const qemuDefaultGuestPassword = "password"

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
	var lastErr error
	vmIP := p.getVMIPAddress(ctx, instanceID)
	if vmIP != "" {
		if err := p.ensureSSHPassAvailable(); err != nil {
			lastErr = err
		} else {
			remoteCmd := fmt.Sprintf("printf 'root:%%s\\n' %s | chpasswd", shellSingleQuote(password))
			for _, authPassword := range qemuPasswordCandidates(password) {
				chpasswdCmd := fmt.Sprintf(
					"SSHPASS=%s sshpass -e ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o PreferredAuthentications=password -o PasswordAuthentication=yes -o PubkeyAuthentication=no -o KbdInteractiveAuthentication=no -o NumberOfPasswordPrompts=1 -o ConnectTimeout=5 %s %s 2>&1",
					shellSingleQuote(authPassword),
					shellSingleQuote("root@"+vmIP),
					shellSingleQuote(remoteCmd))
				output, err := p.sshClient.Execute(chpasswdCmd)
				if err == nil {
					global.APP_LOG.Info("通过SSH设置密码成功",
						zap.String("instance", utils.TruncateString(instanceID, 32)))
					return nil
				}
				lastErr = fmt.Errorf("SSH password update failed: %w; output: %s", err, utils.TruncateString(strings.TrimSpace(output), 300))
			}
		}
	}

	// 方法3: 使用 virt-customize (离线模式,需要VM关机)
	if strings.Contains(status, "shut off") || strings.Contains(status, "shutoff") {
		// 查找VM的磁盘文件
		output, err := p.sshClient.Execute(fmt.Sprintf(
			"virsh -c qemu:///system domblklist %s 2>/dev/null | grep -E '\\.(qcow2|img|raw)' | awk '{print $2}'",
			shellSingleQuote(instanceID)))
		if err == nil {
			diskPath, parseErr := utils.ParseFirstCommandLineMatching(output, func(value string) bool {
				return strings.HasPrefix(value, "/")
			})
			if parseErr == nil {
				output, err := p.sshClient.Execute(fmt.Sprintf(
					"virt-customize -a %s --root-password %s 2>&1",
					shellSingleQuote(diskPath),
					shellSingleQuote("password:"+password)))
				if err == nil && !strings.Contains(output, "error") {
					global.APP_LOG.Info("通过virt-customize设置密码成功",
						zap.String("instance", utils.TruncateString(instanceID, 32)))
					return nil
				}
				if err != nil {
					lastErr = fmt.Errorf("virt-customize failed: %w; output: %s", err, utils.TruncateString(strings.TrimSpace(output), 300))
				} else if strings.Contains(output, "error") {
					lastErr = fmt.Errorf("virt-customize returned error output: %s", utils.TruncateString(strings.TrimSpace(output), 300))
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
		if err != nil {
			lastErr = fmt.Errorf("guest-agent password retry failed: %w; output: %s", err, utils.TruncateString(strings.TrimSpace(output), 300))
		} else if strings.Contains(output, "error") {
			lastErr = fmt.Errorf("guest-agent password retry returned error output: %s", utils.TruncateString(strings.TrimSpace(output), 300))
		}
		global.APP_LOG.Warn("所有密码设置方法均失败",
			zap.String("instance", utils.TruncateString(instanceID, 32)),
			zap.String("lastOutput", utils.TruncateString(output, 200)))
	}

	if lastErr != nil {
		return fmt.Errorf("failed to set password for VM %s: all methods exhausted; last error: %v", instanceID, lastErr)
	}
	return fmt.Errorf("failed to set password for VM %s: all methods exhausted", instanceID)
}

func qemuPasswordCandidates(password string) []string {
	password = strings.TrimSpace(password)
	candidates := make([]string, 0, 2)
	if password != "" {
		candidates = append(candidates, password)
	}
	if password != qemuDefaultGuestPassword {
		candidates = append(candidates, qemuDefaultGuestPassword)
	}
	return candidates
}

func (p *QEMUProvider) ensureSSHPassAvailable() error {
	output, err := p.sshClient.Execute(`if command -v sshpass >/dev/null 2>&1; then
  exit 0
fi
if command -v apt-get >/dev/null 2>&1; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -y >/dev/null 2>&1 || true
  apt-get install -y sshpass >/dev/null 2>&1 || true
elif command -v dnf >/dev/null 2>&1; then
  dnf install -y sshpass >/dev/null 2>&1 || true
elif command -v yum >/dev/null 2>&1; then
  yum install -y sshpass >/dev/null 2>&1 || true
elif command -v apk >/dev/null 2>&1; then
  apk add --no-cache sshpass >/dev/null 2>&1 || true
elif command -v pacman >/dev/null 2>&1; then
  pacman -Sy --noconfirm sshpass >/dev/null 2>&1 || true
fi
command -v sshpass >/dev/null 2>&1`)
	if err != nil {
		return fmt.Errorf("sshpass is required for VM password SSH fallback and could not be installed: %w; output: %s", err, utils.TruncateString(strings.TrimSpace(output), 300))
	}
	return nil
}
