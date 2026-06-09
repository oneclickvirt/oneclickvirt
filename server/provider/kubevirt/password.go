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

	// 方法1: 通过 virtctl console/ssh 连接到VM内部
	// 先获取VM的NodePort SSH端口
	sshPortOutput, err := p.sshClient.Execute(fmt.Sprintf(
		"kubectl get svc %s -n %s -o jsonpath='{.spec.ports[0].nodePort}' 2>/dev/null",
		shellSingleQuote(instanceID+"-ssh"),
		shellSingleQuote(Namespace)))
	if err == nil {
		sshPort := strings.TrimSpace(sshPortOutput)
		if _, parseErr := strconv.Atoi(sshPort); sshPort != "" && parseErr == nil {
			remoteCmd := fmt.Sprintf("printf 'root:%%s' %s | chpasswd", shellSingleQuote(password))
			chpasswdCmd := fmt.Sprintf(
				"SSHPASS=%s sshpass -e ssh -o StrictHostKeyChecking=no -o ConnectTimeout=5 -p %s %s %s 2>/dev/null",
				shellSingleQuote(password),
				shellSingleQuote(sshPort),
				shellSingleQuote("root@127.0.0.1"),
				shellSingleQuote(remoteCmd))
			_, err := p.sshClient.Execute(chpasswdCmd)
			if err == nil {
				global.APP_LOG.Info("通过SSH设置密码成功",
					zap.String("instance", utils.TruncateString(instanceID, 32)))
				return nil
			}
		}
	}

	// 方法2: 使用 virtctl ssh (如果可用)
	remoteCmd := fmt.Sprintf("echo %s | chpasswd", shellSingleQuote("root:"+password))
	output, err := p.sshClient.Execute(fmt.Sprintf(
		"printf '%%s\\n' %s | virtctl ssh --local-ssh=false -n %s %s 2>&1",
		shellSingleQuote(remoteCmd),
		shellSingleQuote(Namespace),
		shellSingleQuote("root@"+instanceID)))
	if err == nil && !strings.Contains(output, "error") {
		global.APP_LOG.Info("通过virtctl ssh设置密码成功",
			zap.String("instance", utils.TruncateString(instanceID, 32)))
		return nil
	}

	// 方法3: 等待后重试
	if err := sleepWithContext(ctx, 5*time.Second); err != nil {
		return fmt.Errorf("waiting before password retry cancelled: %w", err)
	}
	if sshPortOutput, err := p.sshClient.Execute(fmt.Sprintf(
		"kubectl get svc %s -n %s -o jsonpath='{.spec.ports[0].nodePort}' 2>/dev/null",
		shellSingleQuote(instanceID+"-ssh"),
		shellSingleQuote(Namespace))); err == nil {
		sshPort := strings.TrimSpace(sshPortOutput)
		if _, parseErr := strconv.Atoi(sshPort); sshPort != "" && parseErr == nil {
			remoteCmd := fmt.Sprintf("printf 'root:%%s' %s | chpasswd", shellSingleQuote(password))
			chpasswdCmd := fmt.Sprintf(
				"SSHPASS=%s sshpass -e ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 -p %s %s %s 2>/dev/null",
				shellSingleQuote(password),
				shellSingleQuote(sshPort),
				shellSingleQuote("root@127.0.0.1"),
				shellSingleQuote(remoteCmd))
			_, err := p.sshClient.Execute(chpasswdCmd)
			if err == nil {
				return nil
			}
		}
	}

	return fmt.Errorf("failed to set password for VM %s: all methods exhausted", instanceID)
}
