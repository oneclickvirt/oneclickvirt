package kubevirt

import (
	"context"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/provider/firewall"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// StartInstance 启动虚拟机
func (p *KubeVirtProvider) StartInstance(ctx context.Context, id string) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}

	statusOutput, err := p.sshClient.Execute(fmt.Sprintf(
		"kubectl get vm '%s' -n %s -o jsonpath='{.status.printableStatus}' 2>/dev/null", id, Namespace))
	if err != nil {
		return fmt.Errorf("failed to check VM status: %w", err)
	}

	status := strings.ToLower(strings.TrimSpace(statusOutput))
	if strings.Contains(status, "running") {
		return nil
	}

	output, err := p.sshClient.Execute(fmt.Sprintf("virtctl start '%s' -n %s 2>&1", id, Namespace))
	if err != nil {
		global.APP_LOG.Error("KubeVirt虚拟机启动失败",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("failed to start VM: %w", err)
	}

	for i := 0; i < 30; i++ {
		statusOutput, err := p.sshClient.Execute(fmt.Sprintf(
			"kubectl get vmi '%s' -n %s -o jsonpath='{.status.phase}' 2>/dev/null", id, Namespace))
		if err == nil && strings.TrimSpace(statusOutput) == "Running" {
			return nil
		}
		time.Sleep(3 * time.Second)
	}

	return fmt.Errorf("VM '%s' did not reach Running state within timeout", id)
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

	for i := 0; i < 20; i++ {
		statusOutput, err := p.sshClient.Execute(fmt.Sprintf(
			"kubectl get vm '%s' -n %s -o jsonpath='{.status.printableStatus}' 2>/dev/null", id, Namespace))
		if err == nil && strings.Contains(strings.ToLower(strings.TrimSpace(statusOutput)), "stopped") {
			return nil
		}
		time.Sleep(3 * time.Second)
	}

	return fmt.Errorf("VM '%s' did not reach Stopped state within timeout", id)
}

// RestartInstance 重启虚拟机
func (p *KubeVirtProvider) RestartInstance(ctx context.Context, id string) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}

	statusOutput, err := p.sshClient.Execute(fmt.Sprintf(
		"kubectl get vm '%s' -n %s -o jsonpath='{.status.printableStatus}' 2>/dev/null", id, Namespace))
	if err != nil {
		return fmt.Errorf("failed to check VM status: %w", err)
	}

	status := strings.ToLower(strings.TrimSpace(statusOutput))
	if strings.Contains(status, "stopped") {
		return p.StartInstance(ctx, id)
	}

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

// sshDeleteInstance 通过SSH删除KubeVirt虚拟机（不依赖外部shell脚本）
func (p *KubeVirtProvider) sshDeleteInstance(ctx context.Context, id string) error {
	global.APP_LOG.Info("开始删除KubeVirt虚拟机", zap.String("id", utils.TruncateString(id, 32)))

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

	// 5. 通过firewall.Manager清理防火墙规则（nft优先，iptables回退）
	fwMgr := firewall.NewManager(p.sshClient, NFTTableName, "")
	backend, _ := fwMgr.DetectBackend(FWBackendFile)
	if backend == "nft" {
		fwMgr.DeleteRulesByComment(fmt.Sprintf("vm:%s", id))
	} else {
		// iptables backend: use comment-based deletion (same comment format)
		fwMgr.DeleteRulesByComment(fmt.Sprintf("vm:%s", id))
	}
	fwMgr.SaveRules()

	// 6. 清理vmlog
	p.sshClient.Execute(fmt.Sprintf("sed -i '/^%s /d' /root/vmlog 2>/dev/null", id))

	// 等待删除完成
	time.Sleep(3 * time.Second)

	// 验证
	output, err := p.sshClient.Execute(fmt.Sprintf(
		"kubectl get vm '%s' -n %s 2>&1", id, Namespace))
	if err != nil || strings.Contains(output, "NotFound") || strings.Contains(output, "not found") {
		global.APP_LOG.Info("KubeVirt虚拟机删除成功", zap.String("id", utils.TruncateString(id, 32)))
		return nil
	}

	return fmt.Errorf("VM %s still exists after deletion", id)
}
