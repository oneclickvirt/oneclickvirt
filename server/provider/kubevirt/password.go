package kubevirt

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

const (
	kubeVirtDefaultGuestPassword = "password"
	kubeVirtPasswordRetryDelay   = 5 * time.Second
	kubeVirtPasswordSetTimeout   = 90 * time.Second
)

// SetInstancePassword 设置虚拟机密码
func (p *KubeVirtProvider) SetInstancePassword(ctx context.Context, instanceID, password string) error {
	if !p.connected || p.sshClient == nil {
		return fmt.Errorf("not connected")
	}
	return p.sshSetPassword(ctx, instanceID, password)
}

// ResetInstancePassword 重置虚拟机密码
func (p *KubeVirtProvider) ResetInstancePassword(ctx context.Context, instanceID string) (string, error) {
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
func (p *KubeVirtProvider) sshSetPassword(ctx context.Context, instanceID, password string) error {
	global.APP_LOG.Info("设置KubeVirt实例密码",
		zap.String("instance", utils.TruncateString(instanceID, 32)))

	if exists, _ := p.sshK3sContainerExists(instanceID); exists {
		name := k8sResourceName(instanceID)
		podOutput, podErr := p.sshClient.Execute(fmt.Sprintf(
			"kubectl get pod -n %s -l %s -o jsonpath='{.items[0].metadata.name}' 2>/dev/null",
			shellSingleQuote(Namespace), shellSingleQuote("oneclickvirt.io/instance="+name)))
		podName := strings.TrimSpace(podOutput)
		if podErr == nil && podName != "" {
			remoteCmd := fmt.Sprintf("printf 'root:%%s\n' %s | chpasswd", shellSingleQuote(password))
			output, err := p.sshClient.Execute(fmt.Sprintf(
				"kubectl exec -n %s %s -- /bin/sh -c %s 2>&1",
				shellSingleQuote(Namespace), shellSingleQuote(podName), shellSingleQuote(remoteCmd)))
			if err == nil {
				global.APP_LOG.Info("通过kubectl exec设置KubeVirt容器密码成功", zap.String("instance", utils.TruncateString(instanceID, 32)))
				return nil
			}
			global.APP_LOG.Warn("通过kubectl exec设置KubeVirt容器密码失败", zap.String("instance", utils.TruncateString(instanceID, 32)), zap.String("output", utils.TruncateString(output, 300)), zap.Error(err))
		}
	}

	runCtx := ctx
	cancel := func() {}
	if _, ok := ctx.Deadline(); !ok {
		runCtx, cancel = context.WithTimeout(ctx, kubeVirtPasswordSetTimeout)
	}
	defer cancel()

	passwordCandidates := kubeVirtPasswordCandidates(password)
	var lastErr error
	for attempt := 1; ; attempt++ {
		if err := runCtx.Err(); err != nil {
			if lastErr != nil {
				return fmt.Errorf("failed to set password for VM %s before timeout: %w; last error: %v", instanceID, err, lastErr)
			}
			return fmt.Errorf("failed to set password for VM %s before timeout: %w", instanceID, err)
		}

		if sshPort, err := p.kubeVirtSSHNodePort(instanceID); err == nil {
			if err := p.ensureSSHPassAvailable(); err != nil {
				lastErr = err
			} else {
				for _, candidate := range passwordCandidates {
					if err := p.kubeVirtSSHSetPasswordViaNodePort(sshPort, candidate, password); err == nil {
						global.APP_LOG.Info("通过SSH设置密码成功",
							zap.String("instance", utils.TruncateString(instanceID, 32)),
							zap.Int("attempt", attempt))
						return nil
					} else {
						lastErr = err
					}
				}
			}
		} else {
			lastErr = err
			if err := p.kubeVirtSetPasswordViaVirtctl(instanceID, password); err == nil {
				global.APP_LOG.Info("通过virtctl ssh设置密码成功",
					zap.String("instance", utils.TruncateString(instanceID, 32)),
					zap.Int("attempt", attempt))
				return nil
			} else {
				lastErr = err
			}
		}

		global.APP_LOG.Debug("KubeVirt VM密码设置等待重试",
			zap.String("instance", utils.TruncateString(instanceID, 32)),
			zap.Int("attempt", attempt),
			zap.Error(lastErr))
		if err := sleepWithContext(runCtx, kubeVirtPasswordRetryDelay); err != nil {
			if lastErr != nil {
				return fmt.Errorf("failed to set password for VM %s before timeout: %w; last error: %v", instanceID, err, lastErr)
			}
			return fmt.Errorf("failed to set password for VM %s before timeout: %w", instanceID, err)
		}
	}
}

func kubeVirtPasswordCandidates(password string) []string {
	password = strings.TrimSpace(password)
	candidates := make([]string, 0, 2)
	if password != "" {
		candidates = append(candidates, password)
	}
	if password != kubeVirtDefaultGuestPassword {
		candidates = append(candidates, kubeVirtDefaultGuestPassword)
	}
	return candidates
}

func (p *KubeVirtProvider) kubeVirtSSHNodePort(instanceID string) (int, error) {
	sshPortOutput, err := p.sshClient.Execute(fmt.Sprintf(
		"kubectl get svc %s -n %s -o jsonpath='{.spec.ports[0].nodePort}' 2>/dev/null",
		shellSingleQuote(instanceID+"-ssh"),
		shellSingleQuote(Namespace)))
	if err != nil {
		return 0, err
	}
	sshPort := strings.TrimSpace(sshPortOutput)
	port, parseErr := strconv.Atoi(sshPort)
	if sshPort == "" || parseErr != nil || port <= 0 {
		return 0, fmt.Errorf("invalid SSH nodePort for VM %s: %q", instanceID, sshPort)
	}
	return port, nil
}

func (p *KubeVirtProvider) kubeVirtSSHSetPasswordViaNodePort(sshPort int, authPassword, newPassword string) error {
	if strings.TrimSpace(authPassword) == "" {
		return fmt.Errorf("empty SSH auth password")
	}
	remoteCmd := fmt.Sprintf("printf 'root:%%s\\n' %s | chpasswd", shellSingleQuote(newPassword))
	chpasswdCmd := fmt.Sprintf(
		"SSHPASS=%s sshpass -e ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o PreferredAuthentications=password -o PasswordAuthentication=yes -o PubkeyAuthentication=no -o KbdInteractiveAuthentication=no -o NumberOfPasswordPrompts=1 -o ConnectTimeout=10 -p %d %s %s 2>&1",
		shellSingleQuote(authPassword),
		sshPort,
		shellSingleQuote("root@127.0.0.1"),
		shellSingleQuote(remoteCmd))
	output, err := p.sshClient.Execute(chpasswdCmd)
	if err != nil {
		return fmt.Errorf("SSH password update failed: %w; output: %s", err, utils.TruncateString(strings.TrimSpace(output), 300))
	}
	return nil
}

func (p *KubeVirtProvider) kubeVirtSetPasswordViaVirtctl(instanceID, password string) error {
	remoteCmd := fmt.Sprintf("printf 'root:%%s\\n' %s | chpasswd", shellSingleQuote(password))
	output, err := p.sshClient.Execute(fmt.Sprintf(
		"printf '%%s\\n' %s | virtctl ssh --local-ssh=false -n %s %s 2>&1",
		shellSingleQuote(remoteCmd),
		shellSingleQuote(Namespace),
		shellSingleQuote("root@"+instanceID)))
	if err == nil && !strings.Contains(strings.ToLower(output), "error") {
		return nil
	}
	if err != nil {
		return fmt.Errorf("virtctl ssh password update failed: %w; output: %s", err, utils.TruncateString(strings.TrimSpace(output), 300))
	}
	return fmt.Errorf("virtctl ssh password update failed: %s", utils.TruncateString(strings.TrimSpace(output), 300))
}

func (p *KubeVirtProvider) ensureSSHPassAvailable() error {
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
