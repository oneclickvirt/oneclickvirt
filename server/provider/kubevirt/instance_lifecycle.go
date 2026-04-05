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

// StartInstance 启动虚拟机
func (p *KubeVirtProvider) StartInstance(ctx context.Context, id string) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}

	// 检查当前状态
	statusOutput, err := p.sshClient.Execute(fmt.Sprintf(
		"kubectl get vm '%s' -n %s -o jsonpath='{.status.printableStatus}' 2>/dev/null", id, Namespace))
	if err != nil {
		return fmt.Errorf("failed to check VM status: %w", err)
	}

	status := strings.ToLower(strings.TrimSpace(statusOutput))
	if strings.Contains(status, "running") {
		return nil
	}

	// 使用 virtctl start
	output, err := p.sshClient.Execute(fmt.Sprintf("virtctl start '%s' -n %s 2>&1", id, Namespace))
	if err != nil {
		global.APP_LOG.Error("KubeVirt虚拟机启动失败",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("failed to start VM: %w", err)
	}

	// 等待VM运行
	for i := 0; i < 30; i++ {
		statusOutput, err := p.sshClient.Execute(fmt.Sprintf(
			"kubectl get vmi '%s' -n %s -o jsonpath='{.status.phase}' 2>/dev/null", id, Namespace))
		if err == nil && strings.TrimSpace(statusOutput) == "Running" {
			return nil
		}
		time.Sleep(3 * time.Second)
	}

	return nil
}

// StopInstance 停止虚拟机
func (p *KubeVirtProvider) StopInstance(ctx context.Context, id string) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}

	output, err := p.sshClient.Execute(fmt.Sprintf("virtctl stop '%s' -n %s 2>&1", id, Namespace))
	if err != nil {
		global.APP_LOG.Error("KubeVirt虚拟机停止失败",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("failed to stop VM: %w", err)
	}

	// 等待VM停止
	for i := 0; i < 20; i++ {
		statusOutput, err := p.sshClient.Execute(fmt.Sprintf(
			"kubectl get vm '%s' -n %s -o jsonpath='{.status.printableStatus}' 2>/dev/null", id, Namespace))
		if err == nil && strings.Contains(strings.ToLower(strings.TrimSpace(statusOutput)), "stopped") {
			return nil
		}
		time.Sleep(3 * time.Second)
	}

	return nil
}

// RestartInstance 重启虚拟机
func (p *KubeVirtProvider) RestartInstance(ctx context.Context, id string) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}

	// 检查状态
	statusOutput, err := p.sshClient.Execute(fmt.Sprintf(
		"kubectl get vm '%s' -n %s -o jsonpath='{.status.printableStatus}' 2>/dev/null", id, Namespace))
	if err != nil {
		return fmt.Errorf("failed to check VM status: %w", err)
	}

	status := strings.ToLower(strings.TrimSpace(statusOutput))
	if strings.Contains(status, "stopped") {
		return p.StartInstance(ctx, id)
	}

	// 使用 virtctl restart
	output, err := p.sshClient.Execute(fmt.Sprintf("virtctl restart '%s' -n %s 2>&1", id, Namespace))
	if err != nil {
		global.APP_LOG.Warn("KubeVirt虚拟机restart失败，尝试stop+start",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("output", utils.TruncateString(output, 500)))
		if stopErr := p.StopInstance(ctx, id); stopErr != nil {
			return fmt.Errorf("failed to stop VM for restart: %w", stopErr)
		}
		time.Sleep(3 * time.Second)
		return p.StartInstance(ctx, id)
	}

	return nil
}

// DeleteInstance 删除虚拟机
func (p *KubeVirtProvider) DeleteInstance(ctx context.Context, id string) error {
	maxAttempts := 3
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if !p.connected {
			if err := p.Connect(ctx, p.config); err != nil {
				if attempt == maxAttempts {
					return fmt.Errorf("重连失败: %w", err)
				}
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
		}

		err := p.sshDeleteInstance(ctx, id)
		if err == nil {
			return nil
		}

		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "connection") || strings.Contains(errStr, "ssh") {
			p.connected = false
			if attempt < maxAttempts {
				time.Sleep(time.Duration(attempt) * time.Second)
				continue
			}
		}
		return err
	}
	return nil
}

// sshDeleteInstance 通过SSH删除KubeVirt虚拟机
func (p *KubeVirtProvider) sshDeleteInstance(ctx context.Context, id string) error {
	global.APP_LOG.Info("开始删除KubeVirt虚拟机", zap.String("id", utils.TruncateString(id, 32)))

	// 检查 deletevm.sh 脚本是否存在
	output, err := p.sshClient.Execute(fmt.Sprintf("test -f %s/deletevm.sh && echo 'exists' || echo 'missing'", ScriptDir))
	if err == nil && strings.TrimSpace(output) == "exists" {
		output, err := p.sshClient.Execute(fmt.Sprintf("bash %s/deletevm.sh '%s' 2>&1", ScriptDir, id))
		if err != nil {
			global.APP_LOG.Warn("使用脚本删除VM失败，尝试手动删除",
				zap.String("id", utils.TruncateString(id, 32)),
				zap.String("output", utils.TruncateString(output, 500)))
		} else {
			return nil
		}
	}

	// 手动删除流程
	// 1. 停止VM
	p.sshClient.Execute(fmt.Sprintf("virtctl stop '%s' -n %s 2>/dev/null", id, Namespace))
	time.Sleep(2 * time.Second)

	// 2. 删除VM资源
	p.sshClient.Execute(fmt.Sprintf("kubectl delete vm '%s' -n %s --grace-period=30 2>/dev/null", id, Namespace))

	// 3. 删除关联的Service (NodePort)
	p.sshClient.Execute(fmt.Sprintf("kubectl delete svc '%s-ssh' -n %s 2>/dev/null", id, Namespace))
	p.sshClient.Execute(fmt.Sprintf("kubectl delete svc '%s-ports' -n %s 2>/dev/null", id, Namespace))

	// 4. 删除关联的PVC
	p.sshClient.Execute(fmt.Sprintf("kubectl delete pvc -n %s -l vm.kubevirt.io/name='%s' 2>/dev/null", Namespace, id))
	p.sshClient.Execute(fmt.Sprintf("kubectl delete pvc '%s-disk' -n %s 2>/dev/null", id, Namespace))

	// 5. 清理iptables DNAT规则
	p.cleanupIptablesRules(ctx, id)

	// 6. 清理vmlog
	p.sshClient.Execute(fmt.Sprintf("rm -f %s/%s.log 2>/dev/null", VMLogDir, id))

	// 等待删除完成
	time.Sleep(3 * time.Second)

	// 验证
	output, err = p.sshClient.Execute(fmt.Sprintf(
		"kubectl get vm '%s' -n %s 2>&1", id, Namespace))
	if err != nil || strings.Contains(output, "NotFound") || strings.Contains(output, "not found") {
		global.APP_LOG.Info("KubeVirt虚拟机删除成功", zap.String("id", utils.TruncateString(id, 32)))
		return nil
	}

	return fmt.Errorf("VM %s still exists after deletion", id)
}

// cleanupIptablesRules 清理iptables DNAT规则
func (p *KubeVirtProvider) cleanupIptablesRules(ctx context.Context, name string) {
	// 从vmlog获取端口信息
	output, err := p.sshClient.Execute(fmt.Sprintf("cat %s/%s.log 2>/dev/null", VMLogDir, name))
	if err != nil || strings.TrimSpace(output) == "" {
		return
	}

	// 获取所有与该VM相关的DNAT规则的完整规则描述
	// 使用 -S 输出规则描述（而非行号），避免竞态条件
	ruleOutput, err := p.sshClient.Execute(fmt.Sprintf(
		"iptables -t nat -S PREROUTING 2>/dev/null | grep -i '%s'", name))
	if err != nil || strings.TrimSpace(ruleOutput) == "" {
		return
	}

	for _, rule := range strings.Split(strings.TrimSpace(ruleOutput), "\n") {
		rule = strings.TrimSpace(rule)
		if rule == "" {
			continue
		}
		// -S 输出的规则以 -A 开头，替换为 -D 来删除
		deleteRule := strings.Replace(rule, "-A PREROUTING", "-D PREROUTING", 1)
		p.sshClient.Execute(fmt.Sprintf("iptables -t nat %s 2>/dev/null", deleteRule))
	}
}
