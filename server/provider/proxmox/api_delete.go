package proxmox

import (
	"context"
	"fmt"
	"net/http"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

// apiDeleteInstance 通过API方式删除Proxmox实例
func (p *ProxmoxProvider) apiDeleteInstance(ctx context.Context, id string) error {
	// 先通过SSH查找实例信息（API可能无法直接获取所有必要信息）
	vmid, instanceType, err := p.findVMIDByNameOrID(ctx, id)
	if err != nil {
		global.APP_LOG.Error("API删除: 无法找到实例对应的VMID",
			zap.String("id", id),
			zap.Error(err))
		return fmt.Errorf("无法找到实例 %s 对应的VMID: %w", id, err)
	}

	// 获取实例IP地址用于后续清理
	ipAddress, err := p.getInstanceIPAddress(ctx, vmid, instanceType)
	if err != nil {
		global.APP_LOG.Warn("API删除: 无法获取实例IP地址",
			zap.String("id", id),
			zap.String("vmid", vmid),
			zap.Error(err))
		ipAddress = "" // 继续执行，但IP地址为空
	}

	global.APP_LOG.Info("开始API删除Proxmox实例",
		zap.String("id", id),
		zap.String("vmid", vmid),
		zap.String("type", instanceType),
		zap.String("ip", ipAddress))

	// 在删除实例前先清理pmacct监控
	if err := p.cleanupPmacctMonitoring(ctx, id); err != nil {
		global.APP_LOG.Warn("API删除: 清理pmacct监控失败",
			zap.String("id", id),
			zap.String("vmid", vmid),
			zap.Error(err))
	}

	// 根据实例类型选择不同的API端点
	if instanceType == "container" {
		return p.apiDeleteContainer(ctx, vmid, ipAddress)
	} else {
		return p.apiDeleteVM(ctx, vmid, ipAddress)
	}
}

// apiDeleteVM 通过API删除虚拟机
func (p *ProxmoxProvider) apiDeleteVM(ctx context.Context, vmid string, ipAddress string) error {
	global.APP_LOG.Info("开始API删除VM流程",
		zap.String("vmid", vmid),
		zap.String("ip", ipAddress))

	// 1. 解锁VM（通过SSH，因为API可能不支持unlock操作）
	global.APP_LOG.Info("解锁VM", zap.String("vmid", vmid))
	_, err := p.sshClient.Execute(fmt.Sprintf("qm unlock %s 2>/dev/null || true", vmid))
	if err != nil {
		global.APP_LOG.Warn("解锁VM失败", zap.String("vmid", vmid), zap.Error(err))
	}

	// 2. 停止VM
	global.APP_LOG.Info("停止VM", zap.String("vmid", vmid))
	stopURL := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/qemu/%s/status/stop", p.config.Host, p.node, vmid)
	stopReq, err := http.NewRequestWithContext(ctx, "POST", stopURL, nil)
	if err != nil {
		return fmt.Errorf("创建停止请求失败: %w", err)
	}
	p.setAPIAuth(stopReq)

	stopResp, err := p.apiClient.Do(stopReq)
	if err != nil {
		global.APP_LOG.Warn("API停止VM失败，尝试SSH方式", zap.String("vmid", vmid), zap.Error(err))
		_, _ = p.sshClient.Execute(fmt.Sprintf("qm stop %s 2>/dev/null || true", vmid))
	} else {
		stopResp.Body.Close()
	}

	// 3. 检查VM是否完全停止
	if err := p.checkVMCTStatus(ctx, vmid, "vm"); err != nil {
		global.APP_LOG.Warn("VM未完全停止", zap.String("vmid", vmid), zap.Error(err))
		// 继续执行删除，但记录警告
	}

	// 4. 删除VM
	global.APP_LOG.Info("销毁VM", zap.String("vmid", vmid))
	deleteURL := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/qemu/%s", p.config.Host, p.node, vmid)
	deleteReq, err := http.NewRequestWithContext(ctx, "DELETE", deleteURL, nil)
	if err != nil {
		return fmt.Errorf("创建删除请求失败: %w", err)
	}
	p.setAPIAuth(deleteReq)

	deleteResp, err := p.apiClient.Do(deleteReq)
	if err != nil {
		return fmt.Errorf("API删除VM失败: %w", err)
	}
	defer deleteResp.Body.Close()

	if deleteResp.StatusCode != http.StatusOK {
		return fmt.Errorf("API删除VM失败，状态码: %d", deleteResp.StatusCode)
	}

	// 执行后续清理工作（通过SSH，因为这些操作API通常不支持）
	return p.performPostDeletionCleanup(ctx, vmid, ipAddress, "vm")
}

// apiDeleteContainer 通过API删除容器
func (p *ProxmoxProvider) apiDeleteContainer(ctx context.Context, ctid string, ipAddress string) error {
	global.APP_LOG.Info("开始API删除CT流程",
		zap.String("ctid", ctid),
		zap.String("ip", ipAddress))

	// 1. 停止容器
	global.APP_LOG.Info("停止CT", zap.String("ctid", ctid))
	stopURL := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/lxc/%s/status/stop", p.config.Host, p.node, ctid)
	stopReq, err := http.NewRequestWithContext(ctx, "POST", stopURL, nil)
	if err != nil {
		return fmt.Errorf("创建停止请求失败: %w", err)
	}
	p.setAPIAuth(stopReq)

	stopResp, err := p.apiClient.Do(stopReq)
	if err != nil {
		global.APP_LOG.Warn("API停止CT失败，尝试SSH方式", zap.String("ctid", ctid), zap.Error(err))
		_, _ = p.sshClient.Execute(fmt.Sprintf("pct stop %s 2>/dev/null || true", ctid))
	} else {
		stopResp.Body.Close()
	}

	// 2. 检查容器是否完全停止
	if err := p.checkVMCTStatus(ctx, ctid, "container"); err != nil {
		global.APP_LOG.Warn("CT未完全停止", zap.String("ctid", ctid), zap.Error(err))
		// 继续执行删除，但记录警告
	}

	// 3. 删除容器
	global.APP_LOG.Info("销毁CT", zap.String("ctid", ctid))
	deleteURL := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/lxc/%s", p.config.Host, p.node, ctid)
	deleteReq, err := http.NewRequestWithContext(ctx, "DELETE", deleteURL, nil)
	if err != nil {
		return fmt.Errorf("创建删除请求失败: %w", err)
	}
	p.setAPIAuth(deleteReq)

	deleteResp, err := p.apiClient.Do(deleteReq)
	if err != nil {
		return fmt.Errorf("API删除CT失败: %w", err)
	}
	defer deleteResp.Body.Close()

	if deleteResp.StatusCode != http.StatusOK {
		return fmt.Errorf("API删除CT失败，状态码: %d", deleteResp.StatusCode)
	}

	// 执行后续清理工作（通过SSH）
	return p.performPostDeletionCleanup(ctx, ctid, ipAddress, "container")
}

// performPostDeletionCleanup 执行删除后的清理工作
func (p *ProxmoxProvider) performPostDeletionCleanup(ctx context.Context, vmctid string, ipAddress string, instanceType string) error {
	global.APP_LOG.Info("执行删除后清理工作",
		zap.String("vmctid", vmctid),
		zap.String("type", instanceType),
		zap.String("ip", ipAddress))

	// 清理IPv6 NAT映射规则
	if err := p.cleanupIPv6NATRules(ctx, vmctid); err != nil {
		global.APP_LOG.Warn("清理IPv6 NAT规则失败", zap.String("vmctid", vmctid), zap.Error(err))
	}

	// 清理文件
	if instanceType == "vm" {
		if err := p.cleanupVMFiles(ctx, vmctid); err != nil {
			global.APP_LOG.Warn("清理VM文件失败", zap.String("vmid", vmctid), zap.Error(err))
		}
	} else {
		if err := p.cleanupCTFiles(ctx, vmctid); err != nil {
			global.APP_LOG.Warn("清理CT文件失败", zap.String("ctid", vmctid), zap.Error(err))
		}
	}

	// 更新iptables规则
	if ipAddress != "" {
		if err := p.updateIPTablesRules(ctx, ipAddress); err != nil {
			global.APP_LOG.Warn("更新iptables规则失败", zap.String("ip", ipAddress), zap.Error(err))
		}
	}

	// 重建iptables规则
	if err := p.rebuildIPTablesRules(ctx); err != nil {
		global.APP_LOG.Warn("重建iptables规则失败", zap.Error(err))
	}

	// 重启ndpresponder服务
	if err := p.restartNDPResponder(ctx); err != nil {
		global.APP_LOG.Warn("重启ndpresponder服务失败", zap.Error(err))
	}

	global.APP_LOG.Info("通过API成功删除Proxmox实例",
		zap.String("vmctid", vmctid),
		zap.String("type", instanceType))
	return nil
}
