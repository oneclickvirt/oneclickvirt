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

	if exists, _ := p.sshK3sContainerExists(id); exists {
		return p.sshScaleK3sContainer(ctx, id, 1)
	}

	statusOutput, err := p.sshClient.Execute(fmt.Sprintf(
		"kubectl get vm %s -n %s -o jsonpath='{.status.printableStatus}' 2>/dev/null", shellSingleQuote(id), shellSingleQuote(Namespace)))
	if err != nil {
		return fmt.Errorf("failed to check VM status: %w", err)
	}

	status := strings.ToLower(strings.TrimSpace(statusOutput))
	if strings.Contains(status, "running") {
		return nil
	}

	output, err := p.sshClient.Execute(withKubeVirtKubeconfig(fmt.Sprintf("virtctl start %s -n %s 2>&1", shellSingleQuote(id), shellSingleQuote(Namespace))))
	if err != nil {
		diagnostics := p.collectVMDiagnostics(id)
		global.APP_LOG.Error("KubeVirt虚拟机启动失败",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("output", utils.TruncateString(output, 2000)),
			zap.String("diagnostics", utils.TruncateString(diagnostics, 4000)),
			zap.Error(err))
		return fmt.Errorf("failed to start VM: %w; output: %s; diagnostics: %s", err, utils.TruncateString(strings.TrimSpace(output), 8000), utils.TruncateString(strings.TrimSpace(diagnostics), 8000))
	}

	for i := 0; i < 30; i++ {
		statusOutput, err := p.sshClient.Execute(fmt.Sprintf(
			"kubectl get vmi %s -n %s -o jsonpath='{.status.phase}' 2>/dev/null", shellSingleQuote(id), shellSingleQuote(Namespace)))
		if err == nil && strings.TrimSpace(statusOutput) == "Running" {
			return nil
		}
		if err := sleepWithContext(ctx, 3*time.Second); err != nil {
			return fmt.Errorf("waiting for VM '%s' to start cancelled: %w", id, err)
		}
	}

	return fmt.Errorf("VM '%s' did not reach Running state within timeout; diagnostics: %s", id, utils.TruncateString(strings.TrimSpace(p.collectVMDiagnostics(id)), 8000))
}

// StopInstance 停止虚拟机
func (p *KubeVirtProvider) StopInstance(ctx context.Context, id string) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}

	if exists, _ := p.sshK3sContainerExists(id); exists {
		return p.sshScaleK3sContainer(ctx, id, 0)
	}

	output, err := p.sshClient.Execute(withKubeVirtKubeconfig(fmt.Sprintf("virtctl stop %s -n %s 2>&1", shellSingleQuote(id), shellSingleQuote(Namespace))))
	if err != nil {
		diagnostics := p.collectVMDiagnostics(id)
		global.APP_LOG.Error("KubeVirt虚拟机停止失败",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("output", utils.TruncateString(output, 2000)),
			zap.String("diagnostics", utils.TruncateString(diagnostics, 4000)),
			zap.Error(err))
		return fmt.Errorf("failed to stop VM: %w; output: %s; diagnostics: %s", err, utils.TruncateString(strings.TrimSpace(output), 8000), utils.TruncateString(strings.TrimSpace(diagnostics), 8000))
	}

	for i := 0; i < 20; i++ {
		statusOutput, err := p.sshClient.Execute(fmt.Sprintf(
			"kubectl get vm %s -n %s -o jsonpath='{.status.printableStatus}' 2>/dev/null", shellSingleQuote(id), shellSingleQuote(Namespace)))
		if err == nil && strings.Contains(strings.ToLower(strings.TrimSpace(statusOutput)), "stopped") {
			return nil
		}
		if err := sleepWithContext(ctx, 3*time.Second); err != nil {
			return fmt.Errorf("waiting for VM '%s' to stop cancelled: %w", id, err)
		}
	}

	return fmt.Errorf("VM '%s' did not reach Stopped state within timeout; diagnostics: %s", id, utils.TruncateString(strings.TrimSpace(p.collectVMDiagnostics(id)), 8000))
}

// RestartInstance 重启虚拟机
func (p *KubeVirtProvider) RestartInstance(ctx context.Context, id string) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}

	if exists, _ := p.sshK3sContainerExists(id); exists {
		if err := p.sshScaleK3sContainer(ctx, id, 0); err != nil {
			return err
		}
		if err := sleepWithContext(ctx, 2*time.Second); err != nil {
			return fmt.Errorf("waiting before container restart cancelled: %w", err)
		}
		return p.sshScaleK3sContainer(ctx, id, 1)
	}

	statusOutput, err := p.sshClient.Execute(fmt.Sprintf(
		"kubectl get vm %s -n %s -o jsonpath='{.status.printableStatus}' 2>/dev/null", shellSingleQuote(id), shellSingleQuote(Namespace)))
	if err != nil {
		return fmt.Errorf("failed to check VM status: %w", err)
	}

	status := strings.ToLower(strings.TrimSpace(statusOutput))
	if strings.Contains(status, "stopped") {
		return p.StartInstance(ctx, id)
	}

	output, err := p.sshClient.Execute(withKubeVirtKubeconfig(fmt.Sprintf("virtctl restart %s -n %s 2>&1", shellSingleQuote(id), shellSingleQuote(Namespace))))
	if err != nil {
		global.APP_LOG.Warn("KubeVirt虚拟机restart失败，尝试stop+start",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("output", utils.TruncateString(output, 500)))
		if stopErr := p.StopInstance(ctx, id); stopErr != nil {
			return fmt.Errorf("failed to stop VM for restart after virtctl restart failed: %w; restart output: %s", stopErr, utils.TruncateString(strings.TrimSpace(output), 8000))
		}
		if err := sleepWithContext(ctx, 3*time.Second); err != nil {
			return fmt.Errorf("waiting before fallback start cancelled: %w", err)
		}
		return p.StartInstance(ctx, id)
	}

	return nil
}

func (p *KubeVirtProvider) collectVMDiagnostics(name string) string {
	commands := []struct {
		label string
		cmd   string
	}{
		{"vm yaml", fmt.Sprintf("kubectl get vm %s -n %s -o yaml 2>&1", shellSingleQuote(name), shellSingleQuote(Namespace))},
		{"vmi yaml", fmt.Sprintf("kubectl get vmi %s -n %s -o yaml 2>&1", shellSingleQuote(name), shellSingleQuote(Namespace))},
		{"datavolume yaml", fmt.Sprintf("kubectl get datavolume %s -n %s -o yaml 2>&1", shellSingleQuote(name+"-dv"), shellSingleQuote(Namespace))},
		{"launcher pods", fmt.Sprintf("kubectl get pods -n %s -l %s -o wide 2>&1", shellSingleQuote(Namespace), shellSingleQuote("kubevirt.io/domain="+name))},
		{"launcher describe", fmt.Sprintf("kubectl describe pods -n %s -l %s 2>&1", shellSingleQuote(Namespace), shellSingleQuote("kubevirt.io/domain="+name))},
		{"launcher logs", fmt.Sprintf("kubectl logs -n %s -l %s --all-containers --tail=120 2>&1", shellSingleQuote(Namespace), shellSingleQuote("kubevirt.io/domain="+name))},
		{"namespace events", fmt.Sprintf("kubectl get events -n %s --sort-by=.lastTimestamp 2>&1 | tail -120", shellSingleQuote(Namespace))},
	}
	var parts []string
	for _, command := range commands {
		output, err := p.sshClient.Execute(command.cmd)
		if trimmed := strings.TrimSpace(output); trimmed != "" {
			parts = append(parts, fmt.Sprintf("[%s]\n%s", command.label, trimmed))
		}
		if err != nil {
			parts = append(parts, fmt.Sprintf("[%s error]\n%v", command.label, err))
		}
	}
	return strings.Join(parts, "\n\n")
}

// DeleteInstance 删除虚拟机
func (p *KubeVirtProvider) DeleteInstance(ctx context.Context, id string) error {
	maxAttempts := 3
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if !p.connected || p.sshClient == nil {
			// 使用 EnsureConnection 重连（SSH模式重建TCP连接，Agent模式重建WebSocket）
			// 避免在 Agent 模式下错误调用 Connect（无直接 SSH 端点）
			if err := p.EnsureConnection(); err != nil {
				if attempt == maxAttempts {
					return fmt.Errorf("重连失败: %w", err)
				}
				if sleepErr := sleepWithContext(ctx, time.Duration(attempt)*time.Second); sleepErr != nil {
					return fmt.Errorf("等待重试删除KubeVirt虚拟机已取消: %w", sleepErr)
				}
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
				if sleepErr := sleepWithContext(ctx, time.Duration(attempt)*time.Second); sleepErr != nil {
					return fmt.Errorf("等待重试删除KubeVirt虚拟机已取消: %w", sleepErr)
				}
				continue
			}
		}
		return err
	}
	return nil
}

// sshDeleteInstance 通过SSH删除KubeVirt虚拟机（不依赖外部shell脚本）
func (p *KubeVirtProvider) sshDeleteInstance(ctx context.Context, id string) error {
	if exists, _ := p.sshK3sContainerExists(id); exists {
		return p.sshDeleteK3sContainer(ctx, id)
	}

	global.APP_LOG.Info("开始删除KubeVirt虚拟机", zap.String("id", utils.TruncateString(id, 32)))

	// 1. 停止VM
	if output, err := p.sshClient.Execute(withKubeVirtKubeconfig(fmt.Sprintf("virtctl stop %s -n %s 2>&1", shellSingleQuote(id), shellSingleQuote(Namespace)))); err != nil && !kubeVirtNotFound(output, err) {
		return fmt.Errorf("停止KubeVirt虚拟机失败: %w (output: %s)", err, utils.TruncateString(strings.TrimSpace(output), 1000))
	}
	if err := sleepWithContext(ctx, 2*time.Second); err != nil {
		return fmt.Errorf("等待KubeVirt虚拟机停止被取消: %w", err)
	}

	// Clean owned host rules before deleting the VM and its identifying data.
	fwMgr := firewall.NewManager(p.sshClient, NFTTableName, "")
	if _, err := fwMgr.DetectBackend(FWBackendFile); err != nil {
		return fmt.Errorf("删除实例前检测防火墙失败: %w", err)
	}
	if err := fwMgr.DeleteRulesByComment(fmt.Sprintf("vm:%s", id)); err != nil {
		return fmt.Errorf("删除实例前清理防火墙失败: %w", err)
	}
	if err := fwMgr.SaveRules(); err != nil {
		return fmt.Errorf("删除实例前保存防火墙失败: %w", err)
	}

	// 2. 删除VM资源
	if err := p.deleteKubeVirtResource(fmt.Sprintf("kubectl delete vm %s -n %s --grace-period=30 --ignore-not-found=true 2>&1", shellSingleQuote(id), shellSingleQuote(Namespace)), "删除VM"); err != nil {
		return err
	}

	// 3. 删除关联的Service (NodePort)
	for _, resource := range []struct{ kind, name string }{{"SSH Service", id + "-ssh"}, {"端口 Service", id + "-ports"}} {
		if err := p.deleteKubeVirtResource(fmt.Sprintf("kubectl delete svc %s -n %s --ignore-not-found=true 2>&1", shellSingleQuote(resource.name), shellSingleQuote(Namespace)), "删除"+resource.kind); err != nil {
			return err
		}
	}
	// 4. 删除关联的 DataVolume 和 PVC
	// DataVolume 名称为 {id}-dv（与创建时保持一致），删除 DataVolume 后 CDI 会同步删除其 PVC
	if err := p.deleteKubeVirtResource(fmt.Sprintf("kubectl delete datavolume %s -n %s --ignore-not-found=true 2>&1", shellSingleQuote(id+"-dv"), shellSingleQuote(Namespace)), "删除DataVolume"); err != nil {
		return err
	}
	// 兼容旧版本/手动创建的 PVC：尝试删除多种命名格式
	for _, command := range []string{
		fmt.Sprintf("kubectl delete pvc -n %s -l %s --ignore-not-found=true 2>&1", shellSingleQuote(Namespace), shellSingleQuote("vm.kubevirt.io/name="+id)),
		fmt.Sprintf("kubectl delete pvc %s -n %s --ignore-not-found=true 2>&1", shellSingleQuote(id+"-dv"), shellSingleQuote(Namespace)),
		fmt.Sprintf("kubectl delete pvc %s -n %s --ignore-not-found=true 2>&1", shellSingleQuote(id+"-disk"), shellSingleQuote(Namespace)),
	} {
		if err := p.deleteKubeVirtResource(command, "删除PVC"); err != nil {
			return err
		}
	}

	// 6. 清理vmlog
	p.sshClient.Execute(fmt.Sprintf("grep -Fv %s /root/vmlog > /root/vmlog.tmp 2>/dev/null && mv /root/vmlog.tmp /root/vmlog || true", shellSingleQuote(id+" ")))

	// 等待删除完成
	time.Sleep(3 * time.Second)

	// 验证
	output, err := p.sshClient.Execute(fmt.Sprintf(
		"kubectl get vm %s -n %s 2>&1", shellSingleQuote(id), shellSingleQuote(Namespace)))
	if kubeVirtNotFound(output, err) {
		// Multus may still read the NAD while tearing down the launcher pod. Only
		// remove the per-instance definition after the VM object is gone.
		if cleanupErr := p.deleteRoutedKubeVirtNADByInstance(id); cleanupErr != nil {
			return cleanupErr
		}
		global.APP_LOG.Info("KubeVirt虚拟机删除成功", zap.String("id", utils.TruncateString(id, 32)))
		return nil
	}

	if err != nil {
		return fmt.Errorf("验证KubeVirt虚拟机删除状态失败: %w (output: %s)", err, utils.TruncateString(strings.TrimSpace(output), 1000))
	}
	return fmt.Errorf("VM %s still exists after deletion", id)
}

func kubeVirtNotFound(output string, err error) bool {
	text := strings.ToLower(strings.TrimSpace(output))
	if err != nil {
		text += "\n" + strings.ToLower(err.Error())
	}
	for _, transportMarker := range []string{"connection refused", "unable to connect", "i/o timeout", "dial tcp", "tls handshake timeout", "context deadline exceeded", "server is currently unable"} {
		if strings.Contains(text, transportMarker) {
			return false
		}
	}
	return strings.Contains(text, "notfound") || strings.Contains(text, "not found") || strings.Contains(text, "does not exist")
}

func (p *KubeVirtProvider) deleteKubeVirtResource(command, description string) error {
	output, err := p.sshClient.Execute(command)
	if err != nil && !kubeVirtNotFound(output, err) {
		return fmt.Errorf("%s失败: %w (output: %s)", description, err, utils.TruncateString(strings.TrimSpace(output), 1000))
	}
	return nil
}

func sleepWithContext(ctx context.Context, duration time.Duration) error {
	if ctx == nil {
		time.Sleep(duration)
		return nil
	}

	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
