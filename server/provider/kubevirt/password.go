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

// SetInstancePasswordWithCurrent authenticates with the persisted current
// guest password when rotating an already customized password.
func (p *KubeVirtProvider) SetInstancePasswordWithCurrent(ctx context.Context, instanceID, currentPassword, password string) error {
	if !p.connected || p.sshClient == nil {
		return fmt.Errorf("not connected")
	}
	return p.sshSetPassword(ctx, instanceID, password, currentPassword)
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
func (p *KubeVirtProvider) sshSetPassword(ctx context.Context, instanceID, password string, currentPassword ...string) error {
	global.APP_LOG.Info("设置KubeVirt实例密码",
		zap.String("instance", utils.TruncateString(instanceID, 32)))

	exists, discoveryErr := p.sshK3sContainerExists(ctx, instanceID)
	if discoveryErr != nil {
		return fmt.Errorf("detect instance backend: %w", discoveryErr)
	}
	if exists {
		if err := p.sshSetK3sContainerPassword(ctx, instanceID, password); err == nil {
			global.APP_LOG.Info("通过kubectl exec设置KubeVirt容器密码成功", zap.String("instance", utils.TruncateString(instanceID, 32)))
			return nil
		} else {
			global.APP_LOG.Warn("通过kubectl exec设置KubeVirt容器密码失败", zap.String("instance", utils.TruncateString(instanceID, 32)), zap.Error(err))
		}
	}

	runCtx, cancel := context.WithTimeout(ctx, kubeVirtPasswordSetTimeout)
	defer cancel()

	passwordCandidates := kubeVirtPasswordCandidates(password, currentPassword...)
	var lastErr error
	for attempt := 1; ; attempt++ {
		if err := runCtx.Err(); err != nil {
			if lastErr != nil {
				return fmt.Errorf("failed to set password for VM %s before timeout: %w; last error: %v", instanceID, err, lastErr)
			}
			return fmt.Errorf("failed to set password for VM %s before timeout: %w", instanceID, err)
		}

		if sshPort, err := p.kubeVirtSSHNodePort(runCtx, instanceID); err == nil {
			if err := p.ensureSSHPassAvailable(runCtx); err != nil {
				lastErr = err
			} else {
				for _, host := range p.kubeVirtSSHNodePortHosts(runCtx) {
					for _, candidate := range passwordCandidates {
						if err := p.kubeVirtSSHSetPasswordViaNodePort(runCtx, host, sshPort, candidate, password); err == nil {
							if verifyErr := p.kubeVirtSSHCheckPasswordViaNodePort(runCtx, host, sshPort, password); verifyErr != nil {
								lastErr = verifyErr
								continue
							}
							global.APP_LOG.Info("通过SSH设置密码成功",
								zap.String("instance", utils.TruncateString(instanceID, 32)),
								zap.String("host", utils.TruncateString(host, 50)),
								zap.Int("attempt", attempt))
							return nil
						} else {
							lastErr = err
						}
					}
				}
			}
		} else {
			lastErr = err
			if err := p.ensureSSHPassAvailable(runCtx); err != nil {
				lastErr = err
			} else {
				for _, candidate := range passwordCandidates {
					if err := p.kubeVirtSetPasswordViaVirtctl(runCtx, instanceID, candidate, password); err == nil {
						global.APP_LOG.Info("通过virtctl端口隧道设置并验证密码成功",
							zap.String("instance", utils.TruncateString(instanceID, 32)),
							zap.Int("attempt", attempt))
						return nil
					} else {
						lastErr = err
					}
				}
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

func (p *KubeVirtProvider) sshSetK3sContainerPassword(ctx context.Context, instanceID, password string) error {
	name := k8sResourceName(instanceID)
	if name == "" {
		return fmt.Errorf("invalid KubeVirt container name: %s", instanceID)
	}
	if strings.TrimSpace(password) == "" {
		return fmt.Errorf("empty KubeVirt container password")
	}
	podName, err := p.kubeVirtK3sContainerPodName(ctx, name)
	if err != nil {
		return err
	}

	output, err := utils.ExecuteShellCommandContext(ctx, p.sshClient, kubeVirtK3sChpasswdCommand(Namespace, podName, password))
	if err != nil {
		return fmt.Errorf("KubeVirt container chpasswd failed: %w; output: %s", err, utils.TruncateString(strings.TrimSpace(output), 300))
	}
	if output, err = utils.ExecuteShellCommandContext(ctx, p.sshClient, kubeVirtPersistContainerPasswordCommand(Namespace, name, password)); err != nil {
		return fmt.Errorf("KubeVirt container password persistence failed: %w; output: %s", err, utils.TruncateString(strings.TrimSpace(output), 300))
	}
	rolloutOutput, rolloutErr := utils.ExecuteShellCommandContext(ctx, p.sshClient, fmt.Sprintf(
		"kubectl rollout status deployment/%s -n %s --timeout=180s 2>&1",
		shellSingleQuote(name), shellSingleQuote(Namespace)))
	if rolloutErr != nil {
		return fmt.Errorf("KubeVirt container password rollout failed: %w; output: %s", rolloutErr, utils.TruncateString(strings.TrimSpace(rolloutOutput), 500))
	}

	sshPort, err := p.kubeVirtContainerSSHNodePort(ctx, name)
	if err != nil {
		global.APP_LOG.Debug("KubeVirt容器未找到SSH NodePort，跳过外部密码验证",
			zap.String("instance", utils.TruncateString(instanceID, 32)),
			zap.Error(err))
		return nil
	}
	if err := p.ensureSSHPassAvailable(ctx); err != nil {
		return err
	}
	var lastErr error
	for _, host := range p.kubeVirtSSHNodePortHosts(ctx) {
		if err := p.kubeVirtSSHCheckPasswordViaNodePort(ctx, host, sshPort, password); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	for _, host := range p.kubeVirtSSHNodePortHosts(ctx) {
		for _, candidate := range kubeVirtPasswordCandidates(password) {
			if err := p.kubeVirtSSHSetPasswordViaNodePort(ctx, host, sshPort, candidate, password); err == nil {
				if verifyErr := p.kubeVirtSSHCheckPasswordViaNodePort(ctx, host, sshPort, password); verifyErr == nil {
					return nil
				} else {
					lastErr = verifyErr
				}
			} else {
				lastErr = err
			}
		}
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
	}
	return fmt.Errorf("KubeVirt container password update could not be verified via NodePort %d: %v", sshPort, lastErr)
}

func (p *KubeVirtProvider) kubeVirtK3sContainerPodName(ctx context.Context, name string) (string, error) {
	podOutput, podErr := utils.ExecuteShellCommandContext(ctx, p.sshClient, fmt.Sprintf(
		"kubectl get pod -n %s -l %s --field-selector=status.phase=Running -o jsonpath='{.items[0].metadata.name}' 2>/dev/null",
		shellSingleQuote(Namespace), shellSingleQuote("oneclickvirt.io/instance="+name)))
	podName := strings.TrimSpace(podOutput)
	if podErr != nil || podName == "" {
		podOutput, podErr = utils.ExecuteShellCommandContext(ctx, p.sshClient, fmt.Sprintf(
			"kubectl get pod -n %s -l %s -o jsonpath='{.items[0].metadata.name}' 2>/dev/null",
			shellSingleQuote(Namespace), shellSingleQuote("oneclickvirt.io/instance="+name)))
		podName = strings.TrimSpace(podOutput)
	}
	if podErr != nil {
		return "", podErr
	}
	if podName == "" {
		return "", fmt.Errorf("no KubeVirt container pod found for %s", name)
	}
	return podName, nil
}

func kubeVirtK3sChpasswdCommand(namespace, podName, password string) string {
	return fmt.Sprintf(
		"printf 'root:%%s\\n' %s | kubectl exec -i -n %s %s -- chpasswd 2>&1",
		shellSingleQuote(password), shellSingleQuote(namespace), shellSingleQuote(podName))
}

func kubeVirtPersistContainerPasswordCommand(namespace, deploymentName, password string) string {
	password = strings.ReplaceAll(strings.ReplaceAll(password, "\r", ""), "\n", "")
	strategyPatch := `{"spec":{"strategy":{"type":"Recreate","rollingUpdate":null}}}`
	return fmt.Sprintf("kubectl patch deployment/%s -n %s --type=merge -p %s >/dev/null 2>&1 && kubectl set env deployment/%s -n %s %s --overwrite 2>&1",
		shellSingleQuote(deploymentName), shellSingleQuote(namespace), shellSingleQuote(strategyPatch),
		shellSingleQuote(deploymentName), shellSingleQuote(namespace),
		shellSingleQuote("ONECLICKVIRT_ROOT_PASSWORD="+password))
}

func (p *KubeVirtProvider) kubeVirtContainerSSHNodePort(ctx context.Context, instanceID string) (int, error) {
	name := k8sResourceName(instanceID)
	if name == "" {
		return 0, fmt.Errorf("invalid KubeVirt container name: %s", instanceID)
	}
	output, err := utils.ExecuteShellCommandContext(ctx, p.sshClient, fmt.Sprintf(
		"kubectl get svc %s -n %s -o jsonpath='{range .spec.ports[*]}{.targetPort}:{.protocol}:{.nodePort}{\"\\n\"}{end}' 2>/dev/null | awk -F: '$1==\"22\" && $2==\"TCP\" {print $3; exit}'",
		shellSingleQuote(name+"-ports"), shellSingleQuote(Namespace)))
	if err != nil {
		return 0, err
	}
	sshPort, tokenErr := utils.ParseFirstCommandLineMatching(output, func(value string) bool {
		port, parseErr := strconv.Atoi(value)
		return parseErr == nil && port > 0 && port <= 65535
	})
	if tokenErr != nil {
		return 0, fmt.Errorf("invalid SSH nodePort output for KubeVirt container %s: %w", instanceID, tokenErr)
	}
	port, parseErr := strconv.Atoi(sshPort)
	if parseErr != nil || port <= 0 || port > 65535 {
		return 0, fmt.Errorf("invalid SSH nodePort for KubeVirt container %s: %q", instanceID, sshPort)
	}
	return port, nil
}

func kubeVirtPasswordCandidates(password string, currentPassword ...string) []string {
	candidates := make([]string, 0, 3)
	// Try the target first so a retried successful rotation remains idempotent.
	for _, value := range append(append([]string{password}, currentPassword...), kubeVirtDefaultGuestPassword) {
		if strings.TrimSpace(value) == "" {
			continue
		}
		duplicate := false
		for _, existing := range candidates {
			if existing == value {
				duplicate = true
				break
			}
		}
		if !duplicate {
			candidates = append(candidates, value)
		}
	}
	return candidates
}

func (p *KubeVirtProvider) kubeVirtSSHNodePort(ctx context.Context, instanceID string) (int, error) {
	sshPortOutput, err := utils.ExecuteShellCommandContext(ctx, p.sshClient, fmt.Sprintf(
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

func (p *KubeVirtProvider) kubeVirtSSHNodePortHosts(ctx context.Context) []string {
	output, _ := utils.ExecuteShellCommandContext(ctx, p.sshClient, "kubectl get nodes -o jsonpath='{range .items[*].status.addresses[*]}{.address}{\"\\n\"}{end}' 2>/dev/null")
	return kubeVirtNodePortSSHHosts(p.config.Host, output)
}

func kubeVirtNodePortSSHHosts(configHost, nodeAddresses string) []string {
	hosts := make([]string, 0, 4)
	seen := make(map[string]struct{})
	add := func(host string) {
		host = strings.TrimSpace(host)
		if host == "" || host == "<none>" {
			return
		}
		if _, ok := seen[host]; ok {
			return
		}
		seen[host] = struct{}{}
		hosts = append(hosts, host)
	}

	add(configHost)
	for _, host := range strings.Fields(nodeAddresses) {
		add(host)
	}
	add("127.0.0.1")
	return hosts
}

func (p *KubeVirtProvider) kubeVirtSSHSetPasswordViaNodePort(ctx context.Context, host string, sshPort int, authPassword, newPassword string) error {
	if strings.TrimSpace(authPassword) == "" {
		return fmt.Errorf("empty SSH auth password")
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("empty SSH nodePort host")
	}
	remoteCmd := fmt.Sprintf("printf 'root:%%s\\n' %s | chpasswd", shellSingleQuote(newPassword))
	chpasswdCmd := fmt.Sprintf(
		"SSHPASS=%s sshpass -e ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o PreferredAuthentications=password -o PasswordAuthentication=yes -o PubkeyAuthentication=no -o KbdInteractiveAuthentication=no -o NumberOfPasswordPrompts=1 -o ConnectTimeout=10 -p %d %s %s 2>&1",
		shellSingleQuote(authPassword),
		sshPort,
		shellSingleQuote("root@"+host),
		shellSingleQuote(remoteCmd))
	output, err := utils.ExecuteShellCommandContext(ctx, p.sshClient, chpasswdCmd)
	if err != nil {
		return fmt.Errorf("SSH password update failed via %s:%d: %w; output: %s", host, sshPort, err, utils.TruncateString(strings.TrimSpace(output), 300))
	}
	return nil
}

func (p *KubeVirtProvider) kubeVirtSSHCheckPasswordViaNodePort(ctx context.Context, host string, sshPort int, password string) error {
	if strings.TrimSpace(password) == "" {
		return fmt.Errorf("empty SSH auth password")
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("empty SSH nodePort host")
	}
	checkCmd := fmt.Sprintf(
		"SSHPASS=%s sshpass -e ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o PreferredAuthentications=password -o PasswordAuthentication=yes -o PubkeyAuthentication=no -o KbdInteractiveAuthentication=no -o NumberOfPasswordPrompts=1 -o ConnectTimeout=10 -p %d %s %s 2>&1",
		shellSingleQuote(password),
		sshPort,
		shellSingleQuote("root@"+host),
		shellSingleQuote("echo kubevirt-password-ok"))
	output, err := utils.ExecuteShellCommandContext(ctx, p.sshClient, checkCmd)
	if err != nil {
		return fmt.Errorf("SSH password check failed via %s:%d: %w; output: %s", host, sshPort, err, utils.TruncateString(strings.TrimSpace(output), 300))
	}
	if !strings.Contains(output, "kubevirt-password-ok") {
		return fmt.Errorf("SSH password check via %s:%d returned unexpected output: %s", host, sshPort, utils.TruncateString(strings.TrimSpace(output), 300))
	}
	return nil
}

// Use the stable stdio port-forward transport instead of virtctl ssh flags,
// which changed across releases. OpenSSH handles noninteractive password
// authentication even when a pure IPv6 VM intentionally has no NodePort.
func kubeVirtPasswordProxyCommand(instanceID, authPassword, remoteCommand string) string {
	proxy := fmt.Sprintf("virtctl port-forward --stdio=true -n %s %s 22",
		shellSingleQuote(Namespace), shellSingleQuote("vmi/"+instanceID))
	return withKubeVirtKubeconfig(fmt.Sprintf(
		"SSHPASS=%s sshpass -e ssh -T -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o PreferredAuthentications=password -o PasswordAuthentication=yes -o PubkeyAuthentication=no -o KbdInteractiveAuthentication=no -o NumberOfPasswordPrompts=1 -o ConnectTimeout=10 -o ServerAliveInterval=5 -o ServerAliveCountMax=2 -o %s %s %s 2>&1",
		shellSingleQuote(authPassword), shellSingleQuote("ProxyCommand="+proxy), shellSingleQuote("root@kubevirt-guest"), shellSingleQuote(remoteCommand)))
}

func (p *KubeVirtProvider) kubeVirtSetPasswordViaVirtctl(ctx context.Context, instanceID, authPassword, password string) error {
	if strings.TrimSpace(authPassword) == "" || strings.TrimSpace(password) == "" {
		return fmt.Errorf("empty KubeVirt guest password")
	}
	remoteCmd := fmt.Sprintf("printf 'root:%%s\\n' %s | chpasswd", shellSingleQuote(password))
	_, err := utils.ExecuteShellCommandContext(ctx, p.sshClient, kubeVirtPasswordProxyCommand(instanceID, authPassword, remoteCmd))
	if err != nil {
		return fmt.Errorf("virtctl port-forward SSH password update failed: %w", err)
	}
	output, err := utils.ExecuteShellCommandContext(ctx, p.sshClient, kubeVirtPasswordProxyCommand(instanceID, password, "printf 'kubevirt-password-ok\\n'"))
	if err != nil {
		return fmt.Errorf("virtctl port-forward SSH password verification failed: %w", err)
	}
	if !strings.Contains(output, "kubevirt-password-ok") {
		return fmt.Errorf("virtctl port-forward SSH password verification returned no proof")
	}
	return nil
}

func (p *KubeVirtProvider) ensureSSHPassAvailable(ctx context.Context) error {
	output, err := utils.ExecuteShellCommandContext(ctx, p.sshClient, `if command -v sshpass >/dev/null 2>&1; then
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
