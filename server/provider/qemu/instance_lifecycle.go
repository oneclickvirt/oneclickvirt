package qemu

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

func (p *QEMUProvider) libvirtURIForInstance(id string) (string, string) {
	if p.isLXCInstance(id) {
		return "lxc:///", "QEMU/LXC容器"
	}
	return "qemu:///system", "QEMU虚拟机"
}

// StartInstance 启动实例
func (p *QEMUProvider) StartInstance(ctx context.Context, id string) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}

	uri, kind := p.libvirtURIForInstance(id)
	statusOutput, err := p.sshClient.Execute(fmt.Sprintf("virsh -c %s domstate %s 2>/dev/null", shellSingleQuote(uri), shellSingleQuote(id)))
	if err != nil {
		return fmt.Errorf("failed to check %s status: %w", kind, err)
	}

	status := strings.ToLower(strings.TrimSpace(statusOutput))
	if strings.Contains(status, "running") {
		return nil
	}

	output, err := p.sshClient.Execute(fmt.Sprintf("virsh -c %s start %s 2>&1", shellSingleQuote(uri), shellSingleQuote(id)))
	if err != nil {
		global.APP_LOG.Error("QEMU虚拟机启动失败",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("failed to start %s: %w", kind, err)
	}

	for i := 0; i < 15; i++ {
		statusOutput, err := p.sshClient.Execute(fmt.Sprintf("virsh -c %s domstate %s 2>/dev/null", shellSingleQuote(uri), shellSingleQuote(id)))
		if err == nil && strings.Contains(strings.TrimSpace(statusOutput), "running") {
			return nil
		}
		if err := sleepWithContext(ctx, 2*time.Second); err != nil {
			return fmt.Errorf("waiting for VM '%s' to start cancelled: %w", id, err)
		}
	}

	return fmt.Errorf("%s '%s' did not reach running state within timeout", kind, id)
}

// StopInstance 停止虚拟机
func (p *QEMUProvider) StopInstance(ctx context.Context, id string) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}

	uri, kind := p.libvirtURIForInstance(id)
	output, err := p.sshClient.Execute(fmt.Sprintf("virsh -c %s shutdown %s 2>&1", shellSingleQuote(uri), shellSingleQuote(id)))
	if err != nil {
		global.APP_LOG.Warn("QEMU虚拟机优雅关机失败，尝试强制关闭",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
	}

	for i := 0; i < 15; i++ {
		statusOutput, err := p.sshClient.Execute(fmt.Sprintf("virsh -c %s domstate %s 2>/dev/null", shellSingleQuote(uri), shellSingleQuote(id)))
		if err == nil {
			status := strings.ToLower(strings.TrimSpace(statusOutput))
			if strings.Contains(status, "shut off") || strings.Contains(status, "shutoff") {
				return nil
			}
		}
		if err := sleepWithContext(ctx, 2*time.Second); err != nil {
			return fmt.Errorf("waiting for VM '%s' to stop cancelled: %w", id, err)
		}
	}

	output, err = p.sshClient.Execute(fmt.Sprintf("virsh -c %s destroy %s 2>&1", shellSingleQuote(uri), shellSingleQuote(id)))
	if err != nil {
		global.APP_LOG.Error("QEMU虚拟机强制关闭失败",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("failed to stop %s: %w", kind, err)
	}

	return nil
}

// RestartInstance 重启虚拟机
func (p *QEMUProvider) RestartInstance(ctx context.Context, id string) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}

	uri, kind := p.libvirtURIForInstance(id)
	statusOutput, err := p.sshClient.Execute(fmt.Sprintf("virsh -c %s domstate %s 2>/dev/null", shellSingleQuote(uri), shellSingleQuote(id)))
	if err != nil {
		return fmt.Errorf("failed to check %s status: %w", kind, err)
	}

	status := strings.ToLower(strings.TrimSpace(statusOutput))
	if strings.Contains(status, "shut off") || strings.Contains(status, "shutoff") {
		return p.StartInstance(ctx, id)
	}

	output, err := p.sshClient.Execute(fmt.Sprintf("virsh -c %s reboot %s 2>&1", shellSingleQuote(uri), shellSingleQuote(id)))
	if err != nil {
		global.APP_LOG.Warn("QEMU虚拟机reboot失败，尝试destroy+start",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("output", utils.TruncateString(output, 500)))
		p.sshClient.Execute(fmt.Sprintf("virsh -c %s destroy %s 2>/dev/null", shellSingleQuote(uri), shellSingleQuote(id)))
		if err := sleepWithContext(ctx, 2*time.Second); err != nil {
			return fmt.Errorf("waiting before fallback start cancelled: %w", err)
		}
		return p.StartInstance(ctx, id)
	}

	return nil
}

// DeleteInstance 删除虚拟机
func (p *QEMUProvider) DeleteInstance(ctx context.Context, id string) error {
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
					return fmt.Errorf("等待重试删除QEMU虚拟机已取消: %w", sleepErr)
				}
				continue
			}
		}

		var err error
		if p.isLXCInstance(id) {
			err = p.sshDeleteLXCContainer(ctx, id)
		} else {
			err = p.sshDeleteInstance(ctx, id)
		}
		if err == nil {
			return nil
		}

		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "connection") || strings.Contains(errStr, "ssh") {
			p.connected = false
			if attempt < maxAttempts {
				if sleepErr := sleepWithContext(ctx, time.Duration(attempt)*time.Second); sleepErr != nil {
					return fmt.Errorf("等待重试删除QEMU虚拟机已取消: %w", sleepErr)
				}
				continue
			}
		}
		return err
	}
	return nil
}

// sshDeleteInstance 通过SSH删除虚拟机
func (p *QEMUProvider) sshDeleteInstance(ctx context.Context, id string) error {
	global.APP_LOG.Info("开始删除QEMU虚拟机", zap.String("id", utils.TruncateString(id, 32)))

	// 1. 停止VM
	p.sshClient.Execute(fmt.Sprintf("virsh destroy %s 2>/dev/null", shellSingleQuote(id)))
	time.Sleep(1 * time.Second)

	// 2. 获取VM的内网IP（在undefine之前，因为之后信息丢失）
	vmIP := p.getVMIPAddress(ctx, id)

	// 3. 清理防火墙规则
	fwMgr := firewall.NewManager(p.sshClient, NFTTableName, InternalSubnet)
	if _, err := fwMgr.DetectBackend(FWBackendFile); err == nil {
		// nft 后端：通过 comment 精确删除
		if fwMgr.GetBackend() == firewall.BackendNft {
			fwMgr.DeleteRulesByComment(fmt.Sprintf("vm:%s", id))
		}
		// 同时通过 IP 清理（兼容）
		if vmIP != "" {
			fwMgr.DeleteRulesByIP(vmIP)
		}
		fwMgr.SaveRules()
	}

	// 4. 删除 DHCP 预留
	p.removeDHCPReservation(id, vmIP)

	// 5. 删除VM定义和磁盘
	p.sshClient.Execute(fmt.Sprintf("virsh undefine %s --remove-all-storage 2>/dev/null || virsh undefine %s 2>/dev/null || true", shellSingleQuote(id), shellSingleQuote(id)))

	// 6. 清除残留文件
	artifactName := qemuSafeFileComponent(id)
	p.sshClient.Execute(fmt.Sprintf("rm -f %s %s 2>/dev/null",
		shellSingleQuote(fmt.Sprintf("%s/vm-%s.qcow2", ImageDir, artifactName)),
		shellSingleQuote(fmt.Sprintf("%s/vm-%s-cloudinit.iso", ImageDir, artifactName))))

	// 7. 清理 vmlog 记录
	p.sshClient.Execute(fmt.Sprintf("grep -v '^%s ' /root/vmlog > /root/vmlog.tmp && mv /root/vmlog.tmp /root/vmlog 2>/dev/null || true", utils.SanitizeShellArg(id)))

	// 验证删除
	output, err := p.sshClient.Execute(fmt.Sprintf("virsh dominfo %s 2>&1", shellSingleQuote(id)))
	if err != nil || strings.Contains(output, "Domain not found") || strings.Contains(output, "failed to get domain") {
		global.APP_LOG.Info("QEMU虚拟机删除成功", zap.String("id", utils.TruncateString(id, 32)))
		return nil
	}

	return fmt.Errorf("VM %s still exists after deletion", id)
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

// removeDHCPReservation 删除 DHCP 预留
func (p *QEMUProvider) removeDHCPReservation(vmName, vmIP string) {
	// 从 libvirt 网络 XML 获取预留信息
	dhcpMAC, _ := p.sshClient.Execute(fmt.Sprintf(
		"virsh net-dumpxml default 2>/dev/null | grep -F %s | grep -oP \"mac='[^']+\" | cut -d\"'\" -f2",
		shellSingleQuote("name='"+vmName+"'")))
	dhcpMAC = strings.TrimSpace(dhcpMAC)
	dhcpIP, _ := p.sshClient.Execute(fmt.Sprintf(
		"virsh net-dumpxml default 2>/dev/null | grep -F %s | grep -oP \"ip='[^']+\" | cut -d\"'\" -f2",
		shellSingleQuote("name='"+vmName+"'")))
	dhcpIP = strings.TrimSpace(dhcpIP)

	if dhcpMAC != "" && dhcpIP != "" {
		hostXML := fmt.Sprintf("<host mac='%s' name='%s' ip='%s' />", dhcpMAC, vmName, dhcpIP)
		p.sshClient.Execute(fmt.Sprintf(
			"virsh net-update default delete ip-dhcp-host %s --live --config 2>/dev/null || "+
				"virsh net-update default delete ip-dhcp-host %s --config 2>/dev/null || true",
			shellSingleQuote(hostXML), shellSingleQuote(hostXML)))
	}
}
