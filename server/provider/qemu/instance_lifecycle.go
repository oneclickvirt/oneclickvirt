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

// StartInstance 启动虚拟机
func (p *QEMUProvider) StartInstance(ctx context.Context, id string) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}

	// 检查当前状态
	statusOutput, err := p.sshClient.Execute(fmt.Sprintf("virsh domstate '%s' 2>/dev/null", id))
	if err != nil {
		return fmt.Errorf("failed to check VM status: %w", err)
	}

	status := strings.ToLower(strings.TrimSpace(statusOutput))
	if strings.Contains(status, "running") {
		return nil // 已在运行
	}

	// 启动VM
	output, err := p.sshClient.Execute(fmt.Sprintf("virsh start '%s' 2>&1", id))
	if err != nil {
		global.APP_LOG.Error("QEMU虚拟机启动失败",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("failed to start VM: %w", err)
	}

	// 等待VM运行
	for i := 0; i < 15; i++ {
		statusOutput, err := p.sshClient.Execute(fmt.Sprintf("virsh domstate '%s' 2>/dev/null", id))
		if err == nil && strings.Contains(strings.TrimSpace(statusOutput), "running") {
			return nil
		}
		time.Sleep(2 * time.Second)
	}

	return nil
}

// StopInstance 停止虚拟机
func (p *QEMUProvider) StopInstance(ctx context.Context, id string) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}

	// 先尝试优雅关机
	output, err := p.sshClient.Execute(fmt.Sprintf("virsh shutdown '%s' 2>&1", id))
	if err != nil {
		global.APP_LOG.Warn("QEMU虚拟机优雅关机失败，尝试强制关闭",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
	}

	// 等待关机
	for i := 0; i < 15; i++ {
		statusOutput, err := p.sshClient.Execute(fmt.Sprintf("virsh domstate '%s' 2>/dev/null", id))
		if err == nil {
			status := strings.ToLower(strings.TrimSpace(statusOutput))
			if strings.Contains(status, "shut off") || strings.Contains(status, "shutoff") {
				return nil
			}
		}
		time.Sleep(2 * time.Second)
	}

	// 强制关闭
	output, err = p.sshClient.Execute(fmt.Sprintf("virsh destroy '%s' 2>&1", id))
	if err != nil {
		global.APP_LOG.Error("QEMU虚拟机强制关闭失败",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("failed to stop VM: %w", err)
	}

	return nil
}

// RestartInstance 重启虚拟机
func (p *QEMUProvider) RestartInstance(ctx context.Context, id string) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}

	// 检查状态
	statusOutput, err := p.sshClient.Execute(fmt.Sprintf("virsh domstate '%s' 2>/dev/null", id))
	if err != nil {
		return fmt.Errorf("failed to check VM status: %w", err)
	}

	status := strings.ToLower(strings.TrimSpace(statusOutput))
	if strings.Contains(status, "shut off") || strings.Contains(status, "shutoff") {
		// 如果已关机，直接启动
		return p.StartInstance(ctx, id)
	}

	// 尝试reboot
	output, err := p.sshClient.Execute(fmt.Sprintf("virsh reboot '%s' 2>&1", id))
	if err != nil {
		global.APP_LOG.Warn("QEMU虚拟机reboot失败，尝试destroy+start",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("output", utils.TruncateString(output, 500)))
		// 强制destroy再start
		p.sshClient.Execute(fmt.Sprintf("virsh destroy '%s' 2>/dev/null", id))
		time.Sleep(2 * time.Second)
		return p.StartInstance(ctx, id)
	}

	return nil
}

// DeleteInstance 删除虚拟机
func (p *QEMUProvider) DeleteInstance(ctx context.Context, id string) error {
	// 增强版删除，带重连机制
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

		// 检查是否是连接错误
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

// sshDeleteInstance 通过SSH删除虚拟机
func (p *QEMUProvider) sshDeleteInstance(ctx context.Context, id string) error {
	global.APP_LOG.Info("开始删除QEMU虚拟机", zap.String("id", utils.TruncateString(id, 32)))

	// 检查 delete_qemu.sh 脚本是否存在
	output, err := p.sshClient.Execute(fmt.Sprintf("test -f %s/delete_qemu.sh && echo 'exists' || echo 'missing'", ScriptDir))
	if err == nil && strings.TrimSpace(output) == "exists" {
		// 使用脚本删除
		output, err := p.sshClient.Execute(fmt.Sprintf("bash %s/delete_qemu.sh '%s' 2>&1", ScriptDir, id))
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
	p.sshClient.Execute(fmt.Sprintf("virsh destroy '%s' 2>/dev/null", id))
	time.Sleep(1 * time.Second)

	// 2. 清理iptables DNAT规则
	p.cleanupIptablesRules(ctx, id)

	// 3. 删除VM定义和磁盘
	p.sshClient.Execute(fmt.Sprintf("virsh undefine '%s' --remove-all-storage 2>/dev/null", id))

	// 4. 清除残留磁盘文件
	p.sshClient.Execute(fmt.Sprintf("rm -f %s/%s.qcow2 %s/%s_*.qcow2 2>/dev/null", ImageDir, id, ImageDir, id))

	// 5. 清理vmlog
	p.sshClient.Execute(fmt.Sprintf("rm -f %s/%s.log 2>/dev/null", VMLogDir, id))

	// 验证删除
	output, err = p.sshClient.Execute(fmt.Sprintf("virsh dominfo '%s' 2>&1", id))
	if err != nil || strings.Contains(output, "Domain not found") || strings.Contains(output, "failed to get domain") {
		global.APP_LOG.Info("QEMU虚拟机删除成功", zap.String("id", utils.TruncateString(id, 32)))
		return nil
	}

	return fmt.Errorf("VM %s still exists after deletion", id)
}

// cleanupIptablesRules 清理VM相关的iptables DNAT规则
func (p *QEMUProvider) cleanupIptablesRules(ctx context.Context, name string) {
	// 获取VM的内网IP
	ip := p.getVMIPAddress(ctx, name)
	if ip == "" {
		return
	}

	// 获取所有指向该IP的DNAT规则的完整规则描述，然后逐条删除
	// 使用 -S 输出规则描述（而非行号），避免竞态条件
	output, err := p.sshClient.Execute(fmt.Sprintf(
		"iptables -t nat -S PREROUTING 2>/dev/null | grep '%s'", ip))
	if err != nil || strings.TrimSpace(output) == "" {
		return
	}

	for _, rule := range strings.Split(strings.TrimSpace(output), "\n") {
		rule = strings.TrimSpace(rule)
		if rule == "" {
			continue
		}
		// -S 输出的规则以 -A 开头，替换为 -D 来删除
		deleteRule := strings.Replace(rule, "-A PREROUTING", "-D PREROUTING", 1)
		p.sshClient.Execute(fmt.Sprintf("iptables -t nat %s 2>/dev/null", deleteRule))
	}
}
