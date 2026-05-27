package kubevirt

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
	global.APP_LOG.Info("设置KubeVirt虚拟机密码",
		zap.String("instance", utils.TruncateString(instanceID, 32)))

	// 方法1: 通过 virtctl console/ssh 连接到VM内部
	// 先获取VM的NodePort SSH端口
	sshPortOutput, err := p.sshClient.Execute(fmt.Sprintf(
		"kubectl get svc '%s-ssh' -n %s -o jsonpath='{.spec.ports[0].nodePort}' 2>/dev/null", instanceID, Namespace))
	if err == nil {
		sshPort := strings.TrimSpace(sshPortOutput)
		if sshPort != "" {
			// 通过SSH连接到VM并修改密码（使用SSHPASS环境变量避免密码出现在进程列表中）
			escapedPw := strings.ReplaceAll(password, "'", "'\\''")
			chpasswdCmd := fmt.Sprintf(
				"SSHPASS='%s' sshpass -e ssh -o StrictHostKeyChecking=no -o ConnectTimeout=5 -p %s root@127.0.0.1 'printf \"root:%%s\" \"%s\" | chpasswd' 2>/dev/null",
				escapedPw, sshPort, escapedPw)
			_, err := p.sshClient.Execute(chpasswdCmd)
			if err == nil {
				global.APP_LOG.Info("通过SSH设置密码成功",
					zap.String("instance", utils.TruncateString(instanceID, 32)))
				return nil
			}
		}
	}

	// 方法2: 使用 virtctl ssh (如果可用)
	output, err := p.sshClient.Execute(fmt.Sprintf(
		"echo 'echo \"root:%s\" | chpasswd' | virtctl ssh --local-ssh=false -n %s root@'%s' 2>&1",
		password, Namespace, instanceID))
	if err == nil && !strings.Contains(output, "error") {
		global.APP_LOG.Info("通过virtctl ssh设置密码成功",
			zap.String("instance", utils.TruncateString(instanceID, 32)))
		return nil
	}

	// 方法3: 等待后重试
	time.Sleep(5 * time.Second)
	if sshPortOutput, err := p.sshClient.Execute(fmt.Sprintf(
		"kubectl get svc '%s-ssh' -n %s -o jsonpath='{.spec.ports[0].nodePort}' 2>/dev/null", instanceID, Namespace)); err == nil {
		sshPort := strings.TrimSpace(sshPortOutput)
		if sshPort != "" {
			escapedPw := strings.ReplaceAll(password, "'", "'\\''")
			chpasswdCmd := fmt.Sprintf(
				"SSHPASS='%s' sshpass -e ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 -p %s root@127.0.0.1 'printf \"root:%%s\" \"%s\" | chpasswd' 2>/dev/null",
				escapedPw, sshPort, escapedPw)
			_, err := p.sshClient.Execute(chpasswdCmd)
			if err == nil {
				return nil
			}
		}
	}

	return fmt.Errorf("failed to set password for VM %s: all methods exhausted", instanceID)
}
