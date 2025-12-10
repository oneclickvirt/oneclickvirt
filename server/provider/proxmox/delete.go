package proxmox

import (
	"context"
	"fmt"
	"oneclickvirt/global"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

func (p *ProxmoxProvider) sshDeleteInstance(ctx context.Context, id string) error {
	global.APP_LOG.Info("开始在Proxmox节点上删除实例（使用SSH）",
		zap.String("node", p.node),
		zap.String("host", utils.TruncateString(p.config.Host, 32)),
		zap.String("instance_id", id))
	// 查找实例对应的VMID
	vmid, instanceType, err := p.findVMIDByNameOrID(ctx, id)
	if err != nil {
		global.APP_LOG.Error("无法找到实例对应的VMID",
			zap.String("id", id),
			zap.Error(err))
		return fmt.Errorf("无法找到实例 %s 对应的VMID: %w", id, err)
	}

	// 获取实例IP地址用于后续清理
	ipAddress, err := p.getInstanceIPAddress(ctx, vmid, instanceType)
	if err != nil {
		global.APP_LOG.Warn("无法获取实例IP地址",
			zap.String("id", id),
			zap.String("vmid", vmid),
			zap.Error(err))
		ipAddress = "" // 继续执行，但IP地址为空
	}

	global.APP_LOG.Info("开始删除Proxmox实例",
		zap.String("id", id),
		zap.String("vmid", vmid),
		zap.String("type", instanceType),
		zap.String("ip", ipAddress))

	// 在删除实例前先清理pmacct监控
	if err := p.cleanupPmacctMonitoring(ctx, id); err != nil {
		global.APP_LOG.Warn("清理pmacct监控失败",
			zap.String("id", id),
			zap.String("vmid", vmid),
			zap.Error(err))
	}

	// 执行完整的删除流程
	if instanceType == "container" {
		return p.handleCTDeletion(ctx, vmid, ipAddress)
	} else {
		return p.handleVMDeletion(ctx, vmid, ipAddress)
	}
}

// handleVMDeletion 处理VM删除
func (p *ProxmoxProvider) handleVMDeletion(ctx context.Context, vmid string, ipAddress string) error {
	global.APP_LOG.Info("开始VM删除流程",
		zap.String("vmid", vmid),
		zap.String("ip", ipAddress))

	// 1. 解锁VM
	global.APP_LOG.Info("解锁VM", zap.String("vmid", vmid))
	_, err := p.sshClient.Execute(fmt.Sprintf("qm unlock %s 2>/dev/null || true", vmid))
	if err != nil {
		global.APP_LOG.Warn("解锁VM失败", zap.String("vmid", vmid), zap.Error(err))
	}

	// 2. 停止VM
	global.APP_LOG.Info("停止VM", zap.String("vmid", vmid))
	_, err = p.sshClient.Execute(fmt.Sprintf("qm stop %s 2>/dev/null || true", vmid))
	if err != nil {
		global.APP_LOG.Warn("停止VM失败", zap.String("vmid", vmid), zap.Error(err))
	}

	// 3. 检查VM是否完全停止
	if err := p.checkVMCTStatus(ctx, vmid, "vm"); err != nil {
		global.APP_LOG.Warn("VM未完全停止", zap.String("vmid", vmid), zap.Error(err))
		// 继续执行删除，但记录警告
	}

	// 4. 清理端口映射 - 在停止VM之后清理，确保实例已停止
	if err := p.cleanupInstancePortMappings(ctx, vmid, "vm"); err != nil {
		global.APP_LOG.Warn("清理VM端口映射失败", zap.String("vmid", vmid), zap.Error(err))
		// 端口映射清理失败不应该阻止VM删除，继续执行
	}

	// 5. 删除VM
	global.APP_LOG.Info("销毁VM", zap.String("vmid", vmid))
	_, err = p.sshClient.Execute(fmt.Sprintf("qm destroy %s", vmid))
	if err != nil {
		global.APP_LOG.Error("销毁VM失败", zap.String("vmid", vmid), zap.Error(err))
		return fmt.Errorf("销毁VM失败 (VMID: %s): %w", vmid, err)
	}

	// 6. 清理IPv6 NAT映射规则
	if err := p.cleanupIPv6NATRules(ctx, vmid); err != nil {
		global.APP_LOG.Warn("清理IPv6 NAT规则失败", zap.String("vmid", vmid), zap.Error(err))
	}

	// 7. 清理VM相关文件
	if err := p.cleanupVMFiles(ctx, vmid); err != nil {
		global.APP_LOG.Warn("清理VM文件失败", zap.String("vmid", vmid), zap.Error(err))
	}

	// 8. 更新iptables规则
	if ipAddress != "" {
		if err := p.updateIPTablesRules(ctx, ipAddress); err != nil {
			global.APP_LOG.Warn("更新iptables规则失败", zap.String("ip", ipAddress), zap.Error(err))
		}
	}

	// 9. 重建iptables规则
	if err := p.rebuildIPTablesRules(ctx); err != nil {
		global.APP_LOG.Warn("重建iptables规则失败", zap.Error(err))
	}

	// 10. 重启ndpresponder服务
	if err := p.restartNDPResponder(ctx); err != nil {
		global.APP_LOG.Warn("重启ndpresponder服务失败", zap.Error(err))
	}

	global.APP_LOG.Info("通过SSH成功删除Proxmox虚拟机", zap.String("vmid", vmid))
	return nil
}

// handleCTDeletion 处理CT删除
func (p *ProxmoxProvider) handleCTDeletion(ctx context.Context, ctid string, ipAddress string) error {
	global.APP_LOG.Info("开始CT删除流程",
		zap.String("ctid", ctid),
		zap.String("ip", ipAddress))

	// 1. 停止容器
	global.APP_LOG.Info("停止CT", zap.String("ctid", ctid))
	_, err := p.sshClient.Execute(fmt.Sprintf("pct stop %s 2>/dev/null || true", ctid))
	if err != nil {
		global.APP_LOG.Warn("停止CT失败", zap.String("ctid", ctid), zap.Error(err))
	}

	// 2. 检查容器是否完全停止
	if err := p.checkVMCTStatus(ctx, ctid, "container"); err != nil {
		global.APP_LOG.Warn("CT未完全停止", zap.String("ctid", ctid), zap.Error(err))
		// 继续执行删除，但记录警告
	}

	// 3. 清理端口映射 - 在停止CT之后清理，确保实例已停止
	if err := p.cleanupInstancePortMappings(ctx, ctid, "container"); err != nil {
		global.APP_LOG.Warn("清理CT端口映射失败", zap.String("ctid", ctid), zap.Error(err))
		// 端口映射清理失败不应该阻止CT删除，继续执行
	}

	// 4. 删除容器
	global.APP_LOG.Info("销毁CT", zap.String("ctid", ctid))
	_, err = p.sshClient.Execute(fmt.Sprintf("pct destroy %s", ctid))
	if err != nil {
		global.APP_LOG.Error("销毁CT失败", zap.String("ctid", ctid), zap.Error(err))
		return fmt.Errorf("销毁CT失败 (CTID: %s): %w", ctid, err)
	}

	// 5. 清理CT相关文件
	if err := p.cleanupCTFiles(ctx, ctid); err != nil {
		global.APP_LOG.Warn("清理CT文件失败", zap.String("ctid", ctid), zap.Error(err))
	}

	// 6. 清理IPv6 NAT映射规则
	if err := p.cleanupIPv6NATRules(ctx, ctid); err != nil {
		global.APP_LOG.Warn("清理IPv6 NAT规则失败", zap.String("ctid", ctid), zap.Error(err))
	}

	// 7. 更新iptables规则
	if ipAddress != "" {
		if err := p.updateIPTablesRules(ctx, ipAddress); err != nil {
			global.APP_LOG.Warn("更新iptables规则失败", zap.String("ip", ipAddress), zap.Error(err))
		}
	}

	// 8. 重建iptables规则
	if err := p.rebuildIPTablesRules(ctx); err != nil {
		global.APP_LOG.Warn("重建iptables规则失败", zap.Error(err))
	}

	// 9. 重启ndpresponder服务
	if err := p.restartNDPResponder(ctx); err != nil {
		global.APP_LOG.Warn("重启ndpresponder服务失败", zap.Error(err))
	}

	global.APP_LOG.Info("通过SSH成功删除Proxmox容器", zap.String("ctid", ctid))
	return nil
}
