package proxmox

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	"oneclickvirt/service/traffic"
	"oneclickvirt/service/vnstat"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

func (p *ProxmoxProvider) sshListInstances(ctx context.Context) ([]provider.Instance, error) {
	var instances []provider.Instance

	// 获取虚拟机列表
	vmOutput, err := p.sshClient.Execute("qm list")
	if err != nil {
		global.APP_LOG.Warn("获取虚拟机列表失败", zap.Error(err))
	} else {
		vmLines := strings.Split(strings.TrimSpace(vmOutput), "\n")
		if len(vmLines) > 1 {
			for _, line := range vmLines[1:] {
				fields := strings.Fields(line)
				if len(fields) < 3 {
					continue
				}

				status := "stopped"
				if len(fields) > 2 && fields[2] == "running" {
					status = "running"
				}

				instance := provider.Instance{
					ID:     fields[0],
					Name:   fields[1],
					Status: status,
					Type:   "vm",
				}

				// 获取VM的IP地址
				if ipAddress, err := p.getInstanceIPAddress(ctx, fields[0], "vm"); err == nil && ipAddress != "" {
					instance.IP = ipAddress
					instance.PrivateIP = ipAddress
				}

				// 获取VM的IPv6地址
				if ipv6Address, err := p.getInstanceIPv6ByVMID(ctx, fields[0], "vm"); err == nil && ipv6Address != "" {
					instance.IPv6Address = ipv6Address
				}
				instances = append(instances, instance)
			}
		}
	}

	// 获取容器列表
	ctOutput, err := p.sshClient.Execute("pct list")
	if err != nil {
		global.APP_LOG.Warn("获取容器列表失败", zap.Error(err))
	} else {
		ctLines := strings.Split(strings.TrimSpace(ctOutput), "\n")
		if len(ctLines) > 1 {
			for _, line := range ctLines[1:] {
				fields := strings.Fields(line)
				if len(fields) < 2 {
					continue
				}

				status := "stopped"
				name := ""

				// pct list 格式: VMID Status [Lock] [Name]
				if len(fields) >= 2 {
					if fields[1] == "running" {
						status = "running"
					}
				}

				// Name字段可能在不同位置，取最后一个非空字段作为名称
				if len(fields) >= 4 {
					name = fields[3] // 通常Name在第4列
				} else if len(fields) >= 3 && fields[2] != "" {
					name = fields[2] // 有时候Lock为空，Name在第3列
				} else {
					name = fields[0] // 默认使用VMID作为名称
				}

				instance := provider.Instance{
					ID:     fields[0],
					Name:   name,
					Status: status,
					Type:   "container",
				}

				// 获取容器的IP地址
				if ipAddress, err := p.getInstanceIPAddress(ctx, fields[0], "container"); err == nil && ipAddress != "" {
					instance.IP = ipAddress
					instance.PrivateIP = ipAddress
				}

				// 获取容器的IPv6地址
				if ipv6Address, err := p.getInstanceIPv6ByVMID(ctx, fields[0], "container"); err == nil && ipv6Address != "" {
					instance.IPv6Address = ipv6Address
				}
				instances = append(instances, instance)
			}
		}
	}

	global.APP_LOG.Info("通过SSH成功获取Proxmox实例列表",
		zap.Int("totalCount", len(instances)),
		zap.Int("vmCount", len(instances)-countContainers(instances)),
		zap.Int("containerCount", countContainers(instances)))
	return instances, nil
}

func (p *ProxmoxProvider) sshCreateInstance(ctx context.Context, config provider.InstanceConfig) error {
	return p.sshCreateInstanceWithProgress(ctx, config, nil)
}

func (p *ProxmoxProvider) sshCreateInstanceWithProgress(ctx context.Context, config provider.InstanceConfig, progressCallback provider.ProgressCallback) error {
	// 进度更新辅助函数
	updateProgress := func(percentage int, message string) {
		if progressCallback != nil {
			progressCallback(percentage, message)
		}
		global.APP_LOG.Info("Proxmox实例创建进度",
			zap.String("instance", config.Name),
			zap.Int("percentage", percentage),
			zap.String("message", message))
	}

	updateProgress(10, "开始创建Proxmox实例...")

	// 获取下一个可用的VMID
	vmid, err := p.getNextVMID(ctx, config.InstanceType)
	if err != nil {
		return fmt.Errorf("获取VMID失败: %w", err)
	}

	updateProgress(20, "准备镜像和资源...")

	// 确保必要的镜像存在
	if err := p.prepareImage(ctx, config.Image, config.InstanceType); err != nil {
		return fmt.Errorf("准备镜像失败: %w", err)
	}

	updateProgress(40, "创建虚拟机配置...")

	// 根据实例类型创建容器或虚拟机
	if config.InstanceType == "container" {
		if err := p.createContainer(ctx, vmid, config, updateProgress); err != nil {
			return fmt.Errorf("创建容器失败: %w", err)
		}
	} else {
		if err := p.createVM(ctx, vmid, config, updateProgress); err != nil {
			return fmt.Errorf("创建虚拟机失败: %w", err)
		}
	}

	updateProgress(90, "配置网络和启动...")

	// 配置网络
	if err := p.configureInstanceNetwork(ctx, vmid, config); err != nil {
		global.APP_LOG.Warn("网络配置失败", zap.Int("vmid", vmid), zap.Error(err))
	}

	// 启动实例
	if err := p.sshStartInstance(ctx, fmt.Sprintf("%d", vmid)); err != nil {
		global.APP_LOG.Warn("启动实例失败", zap.Int("vmid", vmid), zap.Error(err))
	}

	// 配置端口映射 - 在实例启动后配置
	updateProgress(91, "配置端口映射...")
	if err := p.configureInstancePortMappings(ctx, config, vmid); err != nil {
		global.APP_LOG.Warn("配置端口映射失败", zap.Error(err))
	}

	// 配置SSH密码 - 在实例启动后，使用vmid而不是实例名称
	updateProgress(92, "配置SSH密码...")
	if err := p.configureInstanceSSHPasswordByVMID(ctx, vmid, config); err != nil {
		// SSH密码设置失败也不应该阻止实例创建，记录错误即可
		global.APP_LOG.Warn("配置SSH密码失败", zap.Error(err))
	}

	// 初始化vnstat流量监控
	updateProgress(95, "初始化vnstat流量监控...")
	if err := p.initializeVnStatMonitoring(ctx, vmid, config.Name); err != nil {
		global.APP_LOG.Warn("初始化vnstat监控失败",
			zap.Int("vmid", vmid),
			zap.String("name", config.Name),
			zap.Error(err))
	}

	// 更新实例notes - 将配置信息写入到配置文件中
	updateProgress(97, "更新实例配置信息...")
	if err := p.updateInstanceNotes(ctx, vmid, config); err != nil {
		global.APP_LOG.Warn("更新实例notes失败",
			zap.Int("vmid", vmid),
			zap.String("name", config.Name),
			zap.Error(err))
	}

	updateProgress(100, "Proxmox实例创建完成")

	global.APP_LOG.Info("Proxmox实例创建成功",
		zap.String("name", config.Name),
		zap.Int("vmid", vmid),
		zap.String("type", config.InstanceType))

	return nil
}

func (p *ProxmoxProvider) sshStartInstance(ctx context.Context, id string) error {
	time.Sleep(3 * time.Second) // 等待3秒，确保命令执行环境稳定

	// 先查找实例的VMID和类型
	vmid, instanceType, err := p.findVMIDByNameOrID(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to find instance %s: %w", id, err)
	}

	// 先检查实例状态
	var statusCommand string
	switch instanceType {
	case "vm":
		statusCommand = fmt.Sprintf("qm status %s", vmid)
	case "container":
		statusCommand = fmt.Sprintf("pct status %s", vmid)
	default:
		return fmt.Errorf("unknown instance type: %s", instanceType)
	}

	statusOutput, err := p.sshClient.Execute(statusCommand)
	if err == nil && strings.Contains(statusOutput, "status: running") {
		// 实例已经在运行，等待3秒认为启动成功
		time.Sleep(3 * time.Second)
		global.APP_LOG.Info("Proxmox实例已经在运行",
			zap.String("id", utils.TruncateString(id, 50)),
			zap.String("vmid", vmid),
			zap.String("type", instanceType))
		return nil
	}

	// 实例未运行，执行启动命令
	var command string
	switch instanceType {
	case "vm":
		command = fmt.Sprintf("qm start %s", vmid)
	case "container":
		command = fmt.Sprintf("pct start %s", vmid)
	default:
		return fmt.Errorf("unknown instance type: %s", instanceType)
	}

	// 执行启动命令
	_, err = p.sshClient.Execute(command)
	if err != nil {
		return fmt.Errorf("failed to start %s %s: %w", instanceType, vmid, err)
	}

	global.APP_LOG.Info("已发送启动命令，等待实例启动",
		zap.String("id", utils.TruncateString(id, 50)),
		zap.String("vmid", vmid),
		zap.String("type", instanceType))

	// 等待实例真正启动 - 最多等待90秒
	maxWaitTime := 90 * time.Second
	checkInterval := 10 * time.Second
	startTime := time.Now()

	for {
		// 检查是否超时
		if time.Since(startTime) > maxWaitTime {
			return fmt.Errorf("等待实例启动超时 (90秒)")
		}

		// 等待一段时间后再检查
		time.Sleep(checkInterval)

		// 检查实例状态
		statusOutput, err := p.sshClient.Execute(statusCommand)
		if err == nil && strings.Contains(statusOutput, "status: running") {
			// 实例已经启动，再等待额外的时间确保系统完全就绪
			time.Sleep(5 * time.Second)
			global.APP_LOG.Info("Proxmox实例已成功启动并就绪",
				zap.String("id", utils.TruncateString(id, 50)),
				zap.String("vmid", vmid),
				zap.String("type", instanceType),
				zap.Duration("wait_time", time.Since(startTime)))
			return nil
		}

		global.APP_LOG.Debug("等待实例启动",
			zap.String("vmid", vmid),
			zap.Duration("elapsed", time.Since(startTime)))
	}
}

func (p *ProxmoxProvider) sshStopInstance(ctx context.Context, id string) error {
	// 先查找实例的VMID和类型
	vmid, instanceType, err := p.findVMIDByNameOrID(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to find instance %s: %w", id, err)
	}

	// 根据实例类型使用对应的停止命令
	var command string
	switch instanceType {
	case "vm":
		command = fmt.Sprintf("qm stop %s", vmid)
	case "container":
		command = fmt.Sprintf("pct stop %s", vmid)
	default:
		return fmt.Errorf("unknown instance type: %s", instanceType)
	}

	// 执行停止命令
	_, err = p.sshClient.Execute(command)
	if err != nil {
		return fmt.Errorf("failed to stop %s %s: %w", instanceType, vmid, err)
	}

	global.APP_LOG.Info("通过SSH成功停止Proxmox实例",
		zap.String("id", utils.TruncateString(id, 50)),
		zap.String("vmid", vmid),
		zap.String("type", instanceType))
	return nil
}

func (p *ProxmoxProvider) sshRestartInstance(ctx context.Context, id string) error {
	// 先查找实例的VMID和类型
	vmid, instanceType, err := p.findVMIDByNameOrID(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to find instance %s: %w", id, err)
	}

	// 根据实例类型使用对应的重启命令
	var command string
	var resetCommand string
	switch instanceType {
	case "vm":
		command = fmt.Sprintf("qm reboot %s", vmid)
		resetCommand = fmt.Sprintf("qm reset %s", vmid)
	case "container":
		command = fmt.Sprintf("pct reboot %s", vmid)
		resetCommand = fmt.Sprintf("pct stop %s && pct start %s", vmid, vmid)
	default:
		return fmt.Errorf("unknown instance type: %s", instanceType)
	}

	// 首先尝试优雅重启
	_, err = p.sshClient.Execute(command)
	if err != nil {
		global.APP_LOG.Warn("优雅重启失败，尝试强制重启",
			zap.String("id", utils.TruncateString(id, 50)),
			zap.String("vmid", vmid),
			zap.String("type", instanceType),
			zap.Error(err))

		// 等待2秒后尝试强制重启
		time.Sleep(2 * time.Second)

		// 尝试强制重启
		_, resetErr := p.sshClient.Execute(resetCommand)
		if resetErr != nil {
			return fmt.Errorf("failed to restart %s %s (both reboot and reset failed): reboot error: %w, reset error: %v", instanceType, vmid, err, resetErr)
		}

		global.APP_LOG.Info("通过强制重启成功重启Proxmox实例",
			zap.String("id", utils.TruncateString(id, 50)),
			zap.String("vmid", vmid),
			zap.String("type", instanceType))
	} else {
		global.APP_LOG.Info("通过SSH成功重启Proxmox实例",
			zap.String("id", utils.TruncateString(id, 50)),
			zap.String("vmid", vmid),
			zap.String("type", instanceType))
	}

	// 等待3秒让实例完成重启
	time.Sleep(3 * time.Second)
	return nil
}

// findVMIDByNameOrID 根据实例名称或ID查找对应的VMID和类型
func (p *ProxmoxProvider) findVMIDByNameOrID(ctx context.Context, identifier string) (string, string, error) {
	global.APP_LOG.Debug("查找实例VMID",
		zap.String("identifier", identifier))

	// 首先尝试从容器列表中查找
	output, err := p.sshClient.Execute("pct list")
	if err == nil {
		lines := strings.Split(strings.TrimSpace(output), "\n")
		for _, line := range lines[1:] { // 跳过标题行
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}

			vmid := fields[0]
			var name string

			// pct list 格式: VMID Status [Lock] [Name]
			// Name字段可能在不同位置，取最后一个非空字段作为名称
			if len(fields) >= 4 {
				name = fields[3] // 通常Name在第4列
			} else if len(fields) >= 3 && fields[2] != "" {
				name = fields[2] // 有时候Lock为空，Name在第3列
			} else {
				name = fields[0] // 默认使用VMID作为名称
			}

			// 匹配VMID或名称
			if vmid == identifier || name == identifier {
				global.APP_LOG.Debug("在容器列表中找到匹配项",
					zap.String("identifier", identifier),
					zap.String("vmid", vmid),
					zap.String("name", name))
				return vmid, "container", nil
			}
		}

		// 如果通过名称没找到，再检查hostname配置
		for _, line := range lines[1:] { // 跳过标题行
			fields := strings.Fields(line)
			if len(fields) >= 1 {
				vmid := fields[0]
				// 检查容器的hostname配置
				configCmd := fmt.Sprintf("pct config %s | grep hostname", vmid)
				configOutput, configErr := p.sshClient.Execute(configCmd)
				if configErr == nil && strings.Contains(configOutput, identifier) {
					global.APP_LOG.Debug("通过hostname在容器列表中找到匹配项",
						zap.String("identifier", identifier),
						zap.String("vmid", vmid),
						zap.String("hostname", configOutput))
					return vmid, "container", nil
				}
			}
		}
	}

	// 然后尝试从虚拟机列表中查找
	output, err = p.sshClient.Execute("qm list")
	if err == nil {
		lines := strings.Split(strings.TrimSpace(output), "\n")
		for _, line := range lines[1:] { // 跳过标题行
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				vmid := fields[0]
				name := fields[1]

				// qm list输出格式: VMID NAME STATUS MEM(MB) BOOTDISK(GB) PID UPTIME
				// 匹配VMID或名称
				if vmid == identifier || name == identifier {
					global.APP_LOG.Debug("在虚拟机列表中找到匹配项",
						zap.String("identifier", identifier),
						zap.String("vmid", vmid),
						zap.String("name", name))
					return vmid, "vm", nil
				}
			}
		}

		// 如果直接匹配失败，尝试检查虚拟机的配置中的名称
		for _, line := range lines[1:] {
			fields := strings.Fields(line)
			if len(fields) >= 1 {
				vmid := fields[0]
				// 检查虚拟机的配置中的name属性
				configCmd := fmt.Sprintf("qm config %s | grep -E '^name:' || true", vmid)
				configOutput, configErr := p.sshClient.Execute(configCmd)
				if configErr == nil && strings.Contains(configOutput, identifier) {
					global.APP_LOG.Debug("通过配置名称在虚拟机列表中找到匹配项",
						zap.String("identifier", identifier),
						zap.String("vmid", vmid),
						zap.String("config_name", configOutput))
					return vmid, "vm", nil
				}
			}
		}
	}

	return "", "", fmt.Errorf("未找到实例: %s", identifier)
}

func (p *ProxmoxProvider) sshDeleteInstance(ctx context.Context, id string) error {
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

	// 在删除实例前先清理vnstat监控
	if err := p.cleanupVnStatMonitoring(ctx, id); err != nil {
		global.APP_LOG.Warn("清理vnstat监控失败",
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

// getInstanceIPAddress 获取实例IP地址
func (p *ProxmoxProvider) getInstanceIPAddress(ctx context.Context, vmid string, instanceType string) (string, error) {
	var cmd string

	if instanceType == "container" {
		// 对于容器，首先尝试从配置中获取静态IP
		cmd = fmt.Sprintf("pct config %s | grep -oP 'ip=\\K[0-9.]+' || true", vmid)
		output, err := p.sshClient.Execute(cmd)
		if err == nil && strings.TrimSpace(output) != "" {
			return strings.TrimSpace(output), nil
		}

		// 如果没有静态IP，尝试从容器内部获取动态IP
		cmd = fmt.Sprintf("pct exec %s -- hostname -I | awk '{print $1}' || true", vmid)
	} else {
		// 对于虚拟机，首先尝试从配置中获取静态IP
		cmd = fmt.Sprintf("qm config %s | grep -oP 'ip=\\K[0-9.]+' || true", vmid)
		output, err := p.sshClient.Execute(cmd)
		if err == nil && strings.TrimSpace(output) != "" {
			return strings.TrimSpace(output), nil
		}

		// 如果没有静态IP配置，尝试通过guest agent获取IP
		cmd = fmt.Sprintf("qm guest cmd %s network-get-interfaces 2>/dev/null | grep -oP '\"ip-address\":\\s*\"\\K[^\"]+' | grep -E '^(172\\.|192\\.|10\\.)' | head -1 || true", vmid)
		output, err = p.sshClient.Execute(cmd)
		if err == nil && strings.TrimSpace(output) != "" {
			return strings.TrimSpace(output), nil
		}

		// 最后尝试从网络配置推断IP地址 (如果使用标准内网配置)
		// 基于buildvm.sh脚本中的IP分配规则: 172.16.1.${vm_num}
		vmidInt, err := strconv.Atoi(vmid)
		if err == nil && vmidInt > 0 && vmidInt < 255 {
			inferredIP := fmt.Sprintf("172.16.1.%d", vmidInt)
			// 验证这个IP是否能ping通
			pingCmd := fmt.Sprintf("ping -c 1 -W 2 %s >/dev/null 2>&1 && echo 'reachable' || echo 'unreachable'", inferredIP)
			pingOutput, pingErr := p.sshClient.Execute(pingCmd)
			if pingErr == nil && strings.Contains(pingOutput, "reachable") {
				return inferredIP, nil
			}
		}
	}

	output, err := p.sshClient.Execute(cmd)
	if err != nil {
		return "", err
	}

	ip := strings.TrimSpace(output)
	if ip == "" {
		return "", fmt.Errorf("no IP address found for %s %s", instanceType, vmid)
	}

	return ip, nil
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

	// 2. 清理端口映射 - 在停止VM之前清理，确保能获取到实例名称
	if err := p.cleanupInstancePortMappings(ctx, vmid, "vm"); err != nil {
		global.APP_LOG.Warn("清理VM端口映射失败", zap.String("vmid", vmid), zap.Error(err))
		// 端口映射清理失败不应该阻止VM删除，继续执行
	}

	// 3. 停止VM
	global.APP_LOG.Info("停止VM", zap.String("vmid", vmid))
	_, err = p.sshClient.Execute(fmt.Sprintf("qm stop %s 2>/dev/null || true", vmid))
	if err != nil {
		global.APP_LOG.Warn("停止VM失败", zap.String("vmid", vmid), zap.Error(err))
	}

	// 4. 检查VM是否完全停止
	if err := p.checkVMCTStatus(ctx, vmid, "vm"); err != nil {
		global.APP_LOG.Warn("VM未完全停止", zap.String("vmid", vmid), zap.Error(err))
		// 继续执行删除，但记录警告
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

	// 1. 清理端口映射 - 在停止CT之前清理，确保能获取到实例名称
	if err := p.cleanupInstancePortMappings(ctx, ctid, "container"); err != nil {
		global.APP_LOG.Warn("清理CT端口映射失败", zap.String("ctid", ctid), zap.Error(err))
		// 端口映射清理失败不应该阻止CT删除，继续执行
	}

	// 2. 停止容器
	global.APP_LOG.Info("停止CT", zap.String("ctid", ctid))
	_, err := p.sshClient.Execute(fmt.Sprintf("pct stop %s 2>/dev/null || true", ctid))
	if err != nil {
		global.APP_LOG.Warn("停止CT失败", zap.String("ctid", ctid), zap.Error(err))
	}

	// 3. 检查容器是否完全停止
	if err := p.checkVMCTStatus(ctx, ctid, "container"); err != nil {
		global.APP_LOG.Warn("CT未完全停止", zap.String("ctid", ctid), zap.Error(err))
		// 继续执行删除，但记录警告
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

func (p *ProxmoxProvider) sshListImages(ctx context.Context) ([]provider.Image, error) {
	output, err := p.sshClient.Execute(fmt.Sprintf("pvesh get /nodes/%s/storage/local/content --content iso", p.node))
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	var images []provider.Image

	for _, line := range lines {
		if strings.Contains(line, ".iso") {
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				image := provider.Image{
					ID:   fields[0],
					Name: fields[0],
					Tag:  "iso",
					Size: fields[1],
				}
				images = append(images, image)
			}
		}
	}

	global.APP_LOG.Info("通过 SSH 成功获取 Proxmox 镜像列表", zap.Int("count", len(images)))
	return images, nil
}

func (p *ProxmoxProvider) sshPullImage(ctx context.Context, imageURL string) error {
	_, err := p.sshPullImageToPath(ctx, imageURL, "")
	return err
}

func (p *ProxmoxProvider) sshPullImageToPath(ctx context.Context, imageURL, imageName string) (string, error) {
	// 确定镜像下载目录
	downloadDir := "/usr/local/bin/proxmox_images"

	// 创建下载目录
	_, err := p.sshClient.Execute(fmt.Sprintf("mkdir -p %s", downloadDir))
	if err != nil {
		return "", fmt.Errorf("创建下载目录失败: %w", err)
	}

	// 从URL中提取文件名
	fileName := p.extractFileName(imageURL)
	if imageName != "" {
		fileName = imageName
	}

	remotePath := fmt.Sprintf("%s/%s", downloadDir, fileName)

	global.APP_LOG.Info("开始下载Proxmox镜像",
		zap.String("imageURL", utils.TruncateString(imageURL, 200)),
		zap.String("remotePath", remotePath))

	// 检查文件是否已存在
	checkCmd := fmt.Sprintf("test -f %s && echo 'exists'", remotePath)
	output, _ := p.sshClient.Execute(checkCmd)
	if strings.TrimSpace(output) == "exists" {
		global.APP_LOG.Info("镜像已存在，跳过下载", zap.String("path", remotePath))
		return remotePath, nil
	}

	// 下载镜像
	downloadCmd := fmt.Sprintf("wget --no-check-certificate -O %s %s", remotePath, imageURL)
	_, err = p.sshClient.Execute(downloadCmd)
	if err != nil {
		// 尝试使用curl下载
		downloadCmd = fmt.Sprintf("curl -L -k -o %s %s", remotePath, imageURL)
		_, err = p.sshClient.Execute(downloadCmd)
		if err != nil {
			return "", fmt.Errorf("下载镜像失败: %w", err)
		}
	}

	global.APP_LOG.Info("Proxmox镜像下载完成", zap.String("remotePath", remotePath))

	// 根据文件类型移动到相应目录
	if strings.HasSuffix(fileName, ".iso") {
		// ISO文件移动到ISO目录
		isoPath := fmt.Sprintf("/var/lib/vz/template/iso/%s", fileName)
		moveCmd := fmt.Sprintf("mv %s %s", remotePath, isoPath)
		_, err = p.sshClient.Execute(moveCmd)
		if err != nil {
			global.APP_LOG.Warn("移动ISO文件失败", zap.Error(err))
			return remotePath, nil
		}
		return isoPath, nil
	} else {
		// 其他文件可能是LXC模板，移动到cache目录
		cachePath := fmt.Sprintf("/var/lib/vz/template/cache/%s", fileName)
		moveCmd := fmt.Sprintf("mv %s %s", remotePath, cachePath)
		_, err = p.sshClient.Execute(moveCmd)
		if err != nil {
			global.APP_LOG.Warn("移动模板文件失败", zap.Error(err))
			return remotePath, nil
		}
		return cachePath, nil
	}
}

// extractFileName 从URL中提取文件名
func (p *ProxmoxProvider) extractFileName(url string) string {
	parts := strings.Split(url, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return "downloaded_image"
}

func (p *ProxmoxProvider) sshDeleteImage(ctx context.Context, id string) error {
	_, err := p.sshClient.Execute(fmt.Sprintf("rm -f /var/lib/vz/template/iso/%s", id))
	if err != nil {
		return fmt.Errorf("failed to delete image: %w", err)
	}

	global.APP_LOG.Info("通过 SSH 成功删除 Proxmox 镜像", zap.String("id", id))
	return nil
}

// 获取下一个可用的 VMID
func (p *ProxmoxProvider) getNextVMID(ctx context.Context, instanceType string) (int, error) {
	// 根据实例类型确定VMID范围
	var minVMID, maxVMID int
	if instanceType == "vm" {
		minVMID = 100
		maxVMID = 177 // 虚拟机使用 100-177 (78个ID)
	} else if instanceType == "container" {
		minVMID = 178
		maxVMID = 255 // 容器使用 178-255 (78个ID)
	} else {
		return 0, fmt.Errorf("不支持的实例类型: %s", instanceType)
	}

	global.APP_LOG.Info("开始分配VMID",
		zap.String("instanceType", instanceType),
		zap.Int("minVMID", minVMID),
		zap.Int("maxVMID", maxVMID))

	// 获取已使用的VMID列表
	usedVMIDs := make(map[int]bool)

	// 获取虚拟机列表
	vmOutput, err := p.sshClient.Execute("qm list")
	if err == nil {
		lines := strings.Split(strings.TrimSpace(vmOutput), "\n")
		for _, line := range lines[1:] { // 跳过标题行
			fields := strings.Fields(line)
			if len(fields) >= 1 {
				if vmid, parseErr := strconv.Atoi(fields[0]); parseErr == nil {
					usedVMIDs[vmid] = true
				}
			}
		}
	}

	// 获取容器列表
	ctOutput, err := p.sshClient.Execute("pct list")
	if err == nil {
		lines := strings.Split(strings.TrimSpace(ctOutput), "\n")
		for _, line := range lines[1:] { // 跳过标题行
			fields := strings.Fields(line)
			if len(fields) >= 1 {
				if vmid, parseErr := strconv.Atoi(fields[0]); parseErr == nil {
					usedVMIDs[vmid] = true
				}
			}
		}
	}

	// 在指定范围内寻找最小的可用VMID
	for vmid := minVMID; vmid <= maxVMID; vmid++ {
		if !usedVMIDs[vmid] {
			global.APP_LOG.Info("分配VMID成功",
				zap.String("instanceType", instanceType),
				zap.Int("vmid", vmid),
				zap.Int("totalUsedVMIDs", len(usedVMIDs)))
			return vmid, nil
		}
	}

	// 如果没有可用的VMID，返回错误
	return 0, fmt.Errorf("在范围 %d-%d 内没有可用的VMID，实例类型: %s", minVMID, maxVMID, instanceType)
}

// sshSetInstancePassword 通过SSH设置实例密码
func (p *ProxmoxProvider) sshSetInstancePassword(ctx context.Context, instanceID, password string) error {
	// 先查找实例的VMID和类型
	vmid, instanceType, err := p.findVMIDByNameOrID(ctx, instanceID)
	if err != nil {
		global.APP_LOG.Error("查找Proxmox实例失败",
			zap.String("instanceID", instanceID),
			zap.Error(err))
		return fmt.Errorf("查找实例失败: %w", err)
	}

	// 检查实例状态
	var statusCmd string
	switch instanceType {
	case "container":
		statusCmd = fmt.Sprintf("pct status %s", vmid)
	case "vm":
		statusCmd = fmt.Sprintf("qm status %s", vmid)
	default:
		return fmt.Errorf("unknown instance type: %s", instanceType)
	}

	statusOutput, err := p.sshClient.Execute(statusCmd)
	if err != nil {
		return fmt.Errorf("检查实例状态失败: %w", err)
	}

	if !strings.Contains(statusOutput, "status: running") {
		return fmt.Errorf("实例 %s (VMID: %s) 未运行，无法设置密码", instanceID, vmid)
	}

	// 根据实例类型设置密码
	var setPasswordCmd string
	switch instanceType {
	case "container":
		// LXC容器
		setPasswordCmd = fmt.Sprintf("pct exec %s -- bash -c 'echo \"root:%s\" | chpasswd'", vmid, password)
	case "vm":
		// QEMU虚拟机 - 使用cloud-init设置密码
		// 首先尝试通过cloud-init设置密码
		setPasswordCmd = fmt.Sprintf("qm set %s --cipassword '%s'", vmid, password)

		// 执行设置命令
		_, err := p.sshClient.Execute(setPasswordCmd)
		if err != nil {
			global.APP_LOG.Error("通过cloud-init设置虚拟机密码失败",
				zap.String("instanceID", instanceID),
				zap.String("vmid", vmid),
				zap.Error(err))
			return fmt.Errorf("通过cloud-init设置虚拟机密码失败: %w", err)
		}

		// 检查虚拟机状态，如果已启动则重启以应用密码更改
		statusCmd := fmt.Sprintf("qm status %s", vmid)
		statusOutput, statusErr := p.sshClient.Execute(statusCmd)
		if statusErr == nil && strings.Contains(statusOutput, "status: running") {
			// 虚拟机正在运行，尝试重启以应用密码更改
			restartCmd := fmt.Sprintf("qm reboot %s", vmid)
			_, err = p.sshClient.Execute(restartCmd)
			if err != nil {
				global.APP_LOG.Warn("重启虚拟机应用密码更改失败，可能需要手动重启",
					zap.String("instanceID", instanceID),
					zap.String("vmid", vmid),
					zap.Error(err))
				// 不返回错误，因为密码已经设置，只是可能需要手动重启
			} else {
				global.APP_LOG.Info("已重启虚拟机以应用密码更改",
					zap.String("instanceID", instanceID),
					zap.String("vmid", vmid))
			}
		} else {
			// 虚拟机未运行，无需重启，密码将在下次启动时生效
			global.APP_LOG.Info("虚拟机未运行，密码将在启动时生效",
				zap.String("instanceID", instanceID),
				zap.String("vmid", vmid))
		}

		global.APP_LOG.Info("QEMU虚拟机密码设置成功",
			zap.String("instanceID", utils.TruncateString(instanceID, 12)),
			zap.String("vmid", vmid))

		return nil
	default:
		return fmt.Errorf("unsupported instance type: %s", instanceType)
	}

	// 执行密码设置命令
	_, err = p.sshClient.Execute(setPasswordCmd)
	if err != nil {
		global.APP_LOG.Error("设置Proxmox实例密码失败",
			zap.String("instanceID", instanceID),
			zap.String("vmid", vmid),
			zap.String("type", instanceType),
			zap.Error(err))
		return fmt.Errorf("设置实例密码失败: %w", err)
	}

	global.APP_LOG.Info("Proxmox实例密码设置成功",
		zap.String("instanceID", utils.TruncateString(instanceID, 12)),
		zap.String("vmid", vmid),
		zap.String("type", instanceType))

	return nil
}

// createContainer 创建LXC容器
func (p *ProxmoxProvider) createContainer(ctx context.Context, vmid int, config provider.InstanceConfig, updateProgress func(int, string)) error {
	updateProgress(10, "准备容器系统镜像...")

	// 获取系统镜像 - 从数据库驱动
	systemConfig := &provider.InstanceConfig{
		Image:        config.Image,
		InstanceType: config.InstanceType,
	}

	err := p.queryAndSetSystemImage(ctx, systemConfig)
	if err != nil {
		return fmt.Errorf("获取系统镜像失败: %v", err)
	}

	// 生成本地镜像文件路径
	fileName := p.generateRemoteFileName(config.Image, systemConfig.ImageURL, p.config.Architecture)
	localImagePath := filepath.Join("/var/lib/vz/template/cache", fileName)

	// 检查镜像是否已存在，不存在则下载
	checkCmd := fmt.Sprintf("[ -f %s ] && echo 'exists' || echo 'missing'", localImagePath)
	output, err := p.sshClient.Execute(checkCmd)
	if err != nil {
		return fmt.Errorf("检查镜像文件失败: %v", err)
	}

	if strings.TrimSpace(output) == "missing" {
		updateProgress(20, "下载容器镜像...")
		// 创建缓存目录
		_, err = p.sshClient.Execute("mkdir -p /var/lib/vz/template/cache")
		if err != nil {
			return fmt.Errorf("创建缓存目录失败: %v", err)
		}

		// 确定下载URL（支持CDN）
		downloadURL := p.getDownloadURL(systemConfig.ImageURL, config.UseCDN)
		global.APP_LOG.Info("下载容器镜像",
			zap.String("downloadURL", utils.TruncateString(downloadURL, 100)),
			zap.Bool("useCDN", config.UseCDN))

		// 下载镜像文件
		downloadCmd := fmt.Sprintf("curl -L -o %s %s", localImagePath, downloadURL)
		_, err = p.sshClient.Execute(downloadCmd)
		if err != nil {
			return fmt.Errorf("下载镜像失败: %v", err)
		}
		global.APP_LOG.Info("容器镜像下载完成",
			zap.String("image_path", localImagePath),
			zap.String("url", downloadURL))
	}

	updateProgress(50, "创建LXC容器...")

	// 获取存储盘配置 - 从数据库查询Provider记录
	var providerRecord providerModel.Provider
	if err := global.APP_DB.Where("name = ?", p.config.Name).First(&providerRecord).Error; err != nil {
		global.APP_LOG.Warn("获取Provider记录失败，使用默认存储", zap.Error(err))
	}

	storage := providerRecord.StoragePool
	if storage == "" {
		storage = "local" // 默认存储
	}

	// 转换参数格式以适配Proxmox VE命令要求
	cpuFormatted := convertCPUFormat(config.CPU)
	memoryFormatted := convertMemoryFormat(config.Memory)
	diskFormatted := convertDiskFormat(config.Disk)

	global.APP_LOG.Info("转换参数格式",
		zap.String("原始CPU", config.CPU), zap.String("转换后CPU", cpuFormatted),
		zap.String("原始Memory", config.Memory), zap.String("转换后Memory", memoryFormatted),
		zap.String("原始Disk", config.Disk), zap.String("转换后Disk", diskFormatted))

	// 构建容器创建命令
	createCmd := fmt.Sprintf(
		"pct create %d %s -cores %s -memory %s -swap 128 -rootfs %s:%s -onboot 1 -features nesting=1 -hostname %s",
		vmid,
		localImagePath,
		cpuFormatted,
		memoryFormatted,
		storage,
		diskFormatted,
		config.Name,
	)

	global.APP_LOG.Info("执行容器创建命令", zap.String("command", createCmd))

	_, err = p.sshClient.Execute(createCmd)
	if err != nil {
		return fmt.Errorf("创建容器失败: %w", err)
	}

	updateProgress(70, "配置容器网络...")

	// 配置网络
	user_ip := fmt.Sprintf("172.16.1.%d", vmid)
	netCmd := fmt.Sprintf("pct set %d --net0 name=eth0,ip=%s/24,bridge=vmbr1,gw=172.16.1.1", vmid, user_ip)
	_, err = p.sshClient.Execute(netCmd)
	if err != nil {
		global.APP_LOG.Warn("容器网络配置失败", zap.Int("vmid", vmid), zap.Error(err))
	}

	updateProgress(80, "启动容器...")
	time.Sleep(3 * time.Second)
	// 启动容器
	_, err = p.sshClient.Execute(fmt.Sprintf("pct start %d", vmid))
	if err != nil {
		global.APP_LOG.Warn("容器启动失败", zap.Int("vmid", vmid), zap.Error(err))
	}

	// 等待容器启动
	time.Sleep(5 * time.Second)

	updateProgress(85, "配置容器SSH...")

	// 配置SSH
	p.configureContainerSSH(ctx, vmid)

	return nil
}

// createVM 创建QEMU虚拟机
func (p *ProxmoxProvider) createVM(ctx context.Context, vmid int, config provider.InstanceConfig, updateProgress func(int, string)) error {
	updateProgress(10, "准备虚拟机系统镜像...")

	// 获取系统镜像 - 从数据库驱动
	systemConfig := &provider.InstanceConfig{
		Image:        config.Image,
		InstanceType: config.InstanceType,
	}

	err := p.queryAndSetSystemImage(ctx, systemConfig)
	if err != nil {
		return fmt.Errorf("获取系统镜像失败: %v", err)
	}

	// 生成本地镜像文件路径
	fileName := p.generateRemoteFileName(config.Image, systemConfig.ImageURL, p.config.Architecture)
	localImagePath := fmt.Sprintf("/root/qcow/%s", fileName)

	// 检查镜像是否已存在，不存在则下载
	checkCmd := fmt.Sprintf("[ -f %s ] && echo 'exists' || echo 'missing'", localImagePath)
	output, err := p.sshClient.Execute(checkCmd)
	if err != nil {
		return fmt.Errorf("检查镜像文件失败: %v", err)
	}

	if strings.TrimSpace(output) == "missing" {
		updateProgress(20, "下载系统镜像...")
		// 创建qcow目录
		_, err = p.sshClient.Execute("mkdir -p /root/qcow")
		if err != nil {
			return fmt.Errorf("创建qcow目录失败: %v", err)
		}

		// 确定下载URL（支持CDN）
		downloadURL := p.getDownloadURL(systemConfig.ImageURL, config.UseCDN)
		global.APP_LOG.Info("下载虚拟机镜像",
			zap.String("downloadURL", utils.TruncateString(downloadURL, 100)),
			zap.Bool("useCDN", config.UseCDN))

		// 下载镜像文件
		downloadCmd := fmt.Sprintf("curl -L -o %s %s", localImagePath, downloadURL)
		_, err = p.sshClient.Execute(downloadCmd)
		if err != nil {
			return fmt.Errorf("下载镜像失败: %v", err)
		}
		global.APP_LOG.Info("虚拟机镜像下载完成",
			zap.String("image_path", localImagePath),
			zap.String("url", systemConfig.ImageURL))
	}

	updateProgress(30, "获取系统架构和KVM支持...")

	// 检测系统架构（参考脚本 get_system_arch）
	archCmd := "uname -m"
	archOutput, err := p.sshClient.Execute(archCmd)
	if err != nil {
		return fmt.Errorf("获取系统架构失败: %v", err)
	}
	systemArch := strings.TrimSpace(archOutput)

	// 检测KVM支持（参考脚本 check_kvm_support）
	kvmFlag := "--kvm 1"
	cpuType := "host"
	kvmCheckCmd := "[ -e /dev/kvm ] && [ -r /dev/kvm ] && [ -w /dev/kvm ] && echo 'kvm_available' || echo 'kvm_unavailable'"
	kvmOutput, _ := p.sshClient.Execute(kvmCheckCmd)
	if strings.TrimSpace(kvmOutput) != "kvm_available" {
		// 如果KVM不可用，使用软件模拟
		kvmFlag = "--kvm 0"
		switch systemArch {
		case "aarch64", "armv7l", "armv8", "armv8l":
			cpuType = "max"
		case "i386", "i686", "x86":
			cpuType = "qemu32"
		default:
			cpuType = "qemu64"
		}
		global.APP_LOG.Warn("KVM不可用，使用软件模拟", zap.String("cpu_type", cpuType))
	}

	updateProgress(40, "创建虚拟机基础配置...")

	// 转换参数格式以适配Proxmox VE命令要求
	cpuFormatted := convertCPUFormat(config.CPU)
	memoryFormatted := convertMemoryFormat(config.Memory)
	diskFormatted := convertDiskFormat(config.Disk)

	global.APP_LOG.Info("转换虚拟机参数格式",
		zap.String("原始CPU", config.CPU), zap.String("转换后CPU", cpuFormatted),
		zap.String("原始Memory", config.Memory), zap.String("转换后Memory", memoryFormatted),
		zap.String("原始Disk", config.Disk), zap.String("转换后Disk", diskFormatted))

	// 获取存储盘配置 - 从数据库查询Provider记录
	var providerRecord providerModel.Provider
	if err := global.APP_DB.Where("name = ?", p.config.Name).First(&providerRecord).Error; err != nil {
		global.APP_LOG.Warn("获取Provider记录失败，使用默认存储", zap.Error(err))
	}

	storage := providerRecord.StoragePool
	if storage == "" {
		storage = "local" // 默认存储
	}

	// 获取IPv6配置信息来决定网络桥接
	ipv6Info, err := p.getIPv6Info(ctx)
	if err != nil {
		global.APP_LOG.Warn("获取IPv6信息失败，使用默认网络配置", zap.Error(err))
		ipv6Info = &IPv6Info{HasAppendedAddresses: false}
	}

	// 根据IPv6配置选择第二个网络桥接
	var net1Bridge string
	if ipv6Info.HasAppendedAddresses {
		net1Bridge = "vmbr1"
	} else {
		net1Bridge = "vmbr2"
	}

	// 创建虚拟机，包含IPv6网络接口
	createCmd := fmt.Sprintf(
		"qm create %d --agent 1 --scsihw virtio-scsi-single --serial0 socket --cores %s --sockets 1 --cpu %s --net0 virtio,bridge=vmbr1,firewall=0 --net1 virtio,bridge=%s,firewall=0 --ostype l26 %s",
		vmid, cpuFormatted, cpuType, net1Bridge, kvmFlag,
	)

	_, err = p.sshClient.Execute(createCmd)
	if err != nil {
		return fmt.Errorf("创建虚拟机失败: %v", err)
	}

	updateProgress(50, "导入系统镜像到虚拟机...")

	// 导入磁盘镜像（参考脚本）
	var importCmd string
	if systemArch == "aarch64" || systemArch == "armv7l" || systemArch == "armv8" || systemArch == "armv8l" {
		// ARM架构需要设置BIOS
		_, err = p.sshClient.Execute(fmt.Sprintf("qm set %d --bios ovmf", vmid))
		if err != nil {
			return fmt.Errorf("设置ARM BIOS失败: %v", err)
		}
		importCmd = fmt.Sprintf("qm importdisk %d %s %s", vmid, localImagePath, storage)
	} else {
		// x86/x64架构
		importCmd = fmt.Sprintf("qm importdisk %d %s %s", vmid, localImagePath, storage)
	}

	_, err = p.sshClient.Execute(importCmd)
	if err != nil {
		return fmt.Errorf("导入磁盘镜像失败: %v", err)
	}

	updateProgress(60, "配置虚拟机磁盘...")

	// 等待导入完成
	time.Sleep(3 * time.Second)

	// 查找导入的磁盘文件（参考脚本逻辑）
	findDiskCmd := fmt.Sprintf("pvesm list %s | awk -v vmid=\"%d\" '$5 == vmid && $1 ~ /\\.raw$/ {print $1}' | tail -n 1", storage, vmid)
	diskOutput, err := p.sshClient.Execute(findDiskCmd)
	if err != nil {
		return fmt.Errorf("查找导入磁盘失败: %v", err)
	}

	volid := strings.TrimSpace(diskOutput)
	if volid == "" {
		// 如果没找到.raw文件，查找其他格式
		findDiskCmd = fmt.Sprintf("pvesm list %s | awk -v vmid=\"%d\" '$5 == vmid {print $1}' | tail -n 1", storage, vmid)
		diskOutput, err = p.sshClient.Execute(findDiskCmd)
		if err != nil {
			return fmt.Errorf("查找导入磁盘失败: %v", err)
		}
		volid = strings.TrimSpace(diskOutput)
		if volid == "" {
			return fmt.Errorf("找不到导入的磁盘文件")
		}
	}

	// 设置SCSI磁盘（参考脚本逻辑，优先尝试标准命名）
	scsiSetCmds := []string{
		fmt.Sprintf("qm set %d --scsihw virtio-scsi-pci --scsi0 %s:%d/vm-%d-disk-0.raw", vmid, storage, vmid, vmid),
		fmt.Sprintf("qm set %d --scsihw virtio-scsi-pci --scsi0 %s", vmid, volid),
	}

	var scsiSetErr error
	for _, cmd := range scsiSetCmds {
		_, scsiSetErr = p.sshClient.Execute(cmd)
		if scsiSetErr == nil {
			break
		}
	}
	if scsiSetErr != nil {
		return fmt.Errorf("设置SCSI磁盘失败: %v", scsiSetErr)
	}

	updateProgress(70, "配置虚拟机启动...")

	// 设置启动磁盘
	_, err = p.sshClient.Execute(fmt.Sprintf("qm set %d --bootdisk scsi0", vmid))
	if err != nil {
		return fmt.Errorf("设置启动磁盘失败: %v", err)
	}

	// 设置启动顺序
	_, err = p.sshClient.Execute(fmt.Sprintf("qm set %d --boot order=scsi0", vmid))
	if err != nil {
		return fmt.Errorf("设置启动顺序失败: %v", err)
	}

	// 设置内存
	_, err = p.sshClient.Execute(fmt.Sprintf("qm set %d --memory %s", vmid, memoryFormatted))
	if err != nil {
		return fmt.Errorf("设置内存失败: %v", err)
	}

	updateProgress(80, "配置云初始化...")

	// 配置云初始化磁盘（参考脚本）
	if systemArch == "aarch64" || systemArch == "armv7l" || systemArch == "armv8" || systemArch == "armv8l" {
		_, err = p.sshClient.Execute(fmt.Sprintf("qm set %d --scsi1 %s:cloudinit", vmid, storage))
	} else {
		_, err = p.sshClient.Execute(fmt.Sprintf("qm set %d --ide1 %s:cloudinit", vmid, storage))
	}
	if err != nil {
		global.APP_LOG.Warn("设置云初始化失败", zap.Int("vmid", vmid), zap.Error(err))
	}

	updateProgress(85, "调整磁盘大小...")

	// 调整磁盘大小
	// Proxmox 不支持缩小磁盘，所以需要先检查当前磁盘大小，只在需要扩大时才resize
	if diskFormatted != "" {
		// 尝试解析目标磁盘大小（单位：GB）
		targetDiskGB := 0
		if diskNum, parseErr := strconv.Atoi(diskFormatted); parseErr == nil {
			targetDiskGB = diskNum
		}

		if targetDiskGB > 0 {
			// 获取当前磁盘大小
			getCurrentSizeCmd := fmt.Sprintf("qm config %d | grep 'scsi0' | awk -F'size=' '{print $2}' | awk '{print $1}'", vmid)
			currentSizeOutput, _ := p.sshClient.Execute(getCurrentSizeCmd)
			currentSize := strings.TrimSpace(currentSizeOutput)

			shouldResize := true
			if currentSize != "" {
				// 解析当前磁盘大小（可能是 10G, 1024M 等格式）
				currentGB := 0
				if strings.HasSuffix(currentSize, "G") {
					if num, err := strconv.Atoi(strings.TrimSuffix(currentSize, "G")); err == nil {
						currentGB = num
					}
				} else if strings.HasSuffix(currentSize, "M") {
					if num, err := strconv.Atoi(strings.TrimSuffix(currentSize, "M")); err == nil {
						currentGB = (num + 1023) / 1024 // 向上取整
					}
				}

				// 只有当目标大小大于当前大小时才resize
				if currentGB > 0 && targetDiskGB <= currentGB {
					shouldResize = false
					global.APP_LOG.Info("磁盘无需调整",
						zap.Int("vmid", vmid),
						zap.Int("current_gb", currentGB),
						zap.Int("target_gb", targetDiskGB))
				}
			}

			if shouldResize {
				resizeCmd := fmt.Sprintf("qm resize %d scsi0 %sG", vmid, diskFormatted)
				_, err = p.sshClient.Execute(resizeCmd)
				if err != nil {
					// 尝试以MB为单位重试
					diskMB := targetDiskGB * 1024
					resizeCmd = fmt.Sprintf("qm resize %d scsi0 %dM", vmid, diskMB)
					_, err = p.sshClient.Execute(resizeCmd)
					if err != nil {
						global.APP_LOG.Warn("调整磁盘大小失败", zap.Int("vmid", vmid), zap.Error(err))
					}
				}
			}
		}
	}

	updateProgress(90, "配置网络...")

	// 配置网络（参考脚本 configure_network）
	userIP := fmt.Sprintf("172.16.1.%d", vmid)
	_, err = p.sshClient.Execute(fmt.Sprintf("qm set %d --ipconfig0 ip=%s/24,gw=172.16.1.1", vmid, userIP))
	if err != nil {
		global.APP_LOG.Warn("设置IP配置失败", zap.Int("vmid", vmid), zap.Error(err))
	}

	// 设置DNS
	_, err = p.sshClient.Execute(fmt.Sprintf("qm set %d --nameserver 8.8.8.8", vmid))
	if err != nil {
		global.APP_LOG.Warn("设置DNS失败", zap.Int("vmid", vmid), zap.Error(err))
	}

	// 设置搜索域
	_, err = p.sshClient.Execute(fmt.Sprintf("qm set %d --searchdomain local", vmid))
	if err != nil {
		global.APP_LOG.Warn("设置搜索域失败", zap.Int("vmid", vmid), zap.Error(err))
	}

	// 设置用户密码 - 从config.Metadata获取或生成新密码
	var password string
	if config.Metadata != nil {
		if metadataPassword, ok := config.Metadata["password"]; ok && metadataPassword != "" {
			password = metadataPassword
		}
	}
	if password == "" {
		// 如果metadata中没有密码，生成新密码
		password = utils.GenerateInstancePassword()
	}

	_, err = p.sshClient.Execute(fmt.Sprintf("qm set %d --cipassword %s --ciuser root", vmid, password))
	if err != nil {
		global.APP_LOG.Warn("设置用户密码失败", zap.Int("vmid", vmid), zap.Error(err))
	}

	// 设置虚拟机名称，以便后续能够通过名称查找
	_, err = p.sshClient.Execute(fmt.Sprintf("qm set %d --name %s", vmid, config.Name))
	if err != nil {
		global.APP_LOG.Warn("设置虚拟机名称失败", zap.Int("vmid", vmid), zap.String("name", config.Name), zap.Error(err))
	} else {
		global.APP_LOG.Info("虚拟机名称设置成功", zap.Int("vmid", vmid), zap.String("name", config.Name))
	}

	updateProgress(95, "启动虚拟机...")

	// 启动虚拟机（参考脚本）
	_, err = p.sshClient.Execute(fmt.Sprintf("qm start %d", vmid))
	if err != nil {
		return fmt.Errorf("启动虚拟机失败: %v", err)
	}

	updateProgress(100, "虚拟机创建完成")
	global.APP_LOG.Info("虚拟机创建成功",
		zap.Int("vmid", vmid),
		zap.String("image", config.Image),
		zap.String("storage", storage),
		zap.String("cpu_type", cpuType))

	return nil
}

// configureInstanceNetwork 配置实例网络
func (p *ProxmoxProvider) configureInstanceNetwork(ctx context.Context, vmid int, config provider.InstanceConfig) error {
	// 根据实例类型配置网络
	if config.InstanceType == "container" {
		return p.configureContainerNetwork(ctx, vmid, config)
	} else {
		return p.configureVMNetwork(ctx, vmid, config)
	}
}

// configureContainerNetwork 配置容器网络
func (p *ProxmoxProvider) configureContainerNetwork(ctx context.Context, vmid int, config provider.InstanceConfig) error {
	// 解析网络配置
	networkConfig := p.parseNetworkConfigFromInstanceConfig(config)

	global.APP_LOG.Info("配置容器网络",
		zap.Int("vmid", vmid),
		zap.String("networkType", networkConfig.NetworkType))

	// 检查是否包含IPv6
	hasIPv6 := networkConfig.NetworkType == "nat_ipv4_ipv6" ||
		networkConfig.NetworkType == "dedicated_ipv4_ipv6" ||
		networkConfig.NetworkType == "ipv6_only"

	if hasIPv6 {
		// 配置IPv6网络（会根据NetworkType自动处理IPv4+IPv6或纯IPv6）
		if err := p.configureInstanceIPv6(ctx, vmid, config, "container"); err != nil {
			global.APP_LOG.Warn("配置容器IPv6失败，回退到IPv4-only", zap.Int("vmid", vmid), zap.Error(err))
			// IPv6配置失败，回退到IPv4-only配置
			hasIPv6 = false
		}
	}

	// 如果没有IPv6或IPv6配置失败，配置IPv4-only网络
	if !hasIPv6 {
		user_ip := fmt.Sprintf("172.16.1.%d", vmid)
		netCmd := fmt.Sprintf("pct set %d --net0 name=eth0,ip=%s/24,bridge=vmbr1,gw=172.16.1.1", vmid, user_ip)
		_, err := p.sshClient.Execute(netCmd)
		if err != nil {
			return fmt.Errorf("配置容器IPv4网络失败: %w", err)
		}

		// 配置端口转发（只在IPv4模式下需要）
		if len(config.Ports) > 0 {
			p.configurePortForwarding(ctx, vmid, user_ip, config.Ports)
		}
	}

	return nil
}

// configureVMNetwork 配置虚拟机网络
func (p *ProxmoxProvider) configureVMNetwork(ctx context.Context, vmid int, config provider.InstanceConfig) error {
	// 解析网络配置
	networkConfig := p.parseNetworkConfigFromInstanceConfig(config)

	global.APP_LOG.Info("配置虚拟机网络",
		zap.Int("vmid", vmid),
		zap.String("networkType", networkConfig.NetworkType))

	// 检查是否包含IPv6
	hasIPv6 := networkConfig.NetworkType == "nat_ipv4_ipv6" ||
		networkConfig.NetworkType == "dedicated_ipv4_ipv6" ||
		networkConfig.NetworkType == "ipv6_only"

	if hasIPv6 {
		// 配置IPv6网络（会根据NetworkType自动处理IPv4+IPv6或纯IPv6）
		if err := p.configureInstanceIPv6(ctx, vmid, config, "vm"); err != nil {
			global.APP_LOG.Warn("配置虚拟机IPv6失败，回退到IPv4-only", zap.Int("vmid", vmid), zap.Error(err))
			// IPv6配置失败，回退到IPv4-only配置
			hasIPv6 = false
		}
	}

	// 如果没有IPv6或IPv6配置失败，配置IPv4-only网络
	if !hasIPv6 {
		user_ip := fmt.Sprintf("172.16.1.%d", vmid)

		// 配置云初始化网络
		ipCmd := fmt.Sprintf("qm set %d --ipconfig0 ip=%s/24,gw=172.16.1.1", vmid, user_ip)
		_, err := p.sshClient.Execute(ipCmd)
		if err != nil {
			return fmt.Errorf("配置虚拟机IPv4网络失败: %w", err)
		}

		// 配置端口转发（只在IPv4模式下需要）
		if len(config.Ports) > 0 {
			p.configurePortForwarding(ctx, vmid, user_ip, config.Ports)
		}
	}

	return nil
}

// configurePortForwarding 配置端口转发
func (p *ProxmoxProvider) configurePortForwarding(ctx context.Context, vmid int, userIP string, ports []string) {
	for _, port := range ports {
		// 简单的端口字符串解析，假设格式为 "hostPort:containerPort"
		parts := strings.Split(port, ":")
		if len(parts) != 2 {
			continue
		}

		// iptables规则进行端口转发
		rule := fmt.Sprintf("iptables -t nat -A PREROUTING -i vmbr0 -p tcp --dport %s -j DNAT --to-destination %s:%s",
			parts[0], userIP, parts[1])

		_, err := p.sshClient.Execute(rule)
		if err != nil {
			global.APP_LOG.Warn("配置端口转发失败",
				zap.Int("vmid", vmid),
				zap.String("port", port),
				zap.Error(err))
		}
	}

	// 保存iptables规则
	_, err := p.sshClient.Execute("iptables-save > /etc/iptables/rules.v4")
	if err != nil {
		global.APP_LOG.Warn("保存iptables规则失败", zap.Error(err))
	}
}

// configureContainerSSH 配置容器SSH
func (p *ProxmoxProvider) configureContainerSSH(ctx context.Context, vmid int) {
	// 等待容器完全启动
	time.Sleep(3 * time.Second)

	global.APP_LOG.Info("开始配置容器SSH", zap.Int("vmid", vmid))

	// 检测容器包管理器类型
	pkgManager := p.detectContainerPackageManager(vmid)
	global.APP_LOG.Info("检测到容器包管理器", zap.Int("vmid", vmid), zap.String("packageManager", pkgManager))

	// 备份并配置DNS
	p.configureContainerDNS(vmid)

	// 根据包管理器类型配置SSH
	switch pkgManager {
	case "apk":
		p.configureAlpineSSH(vmid)
	case "opkg":
		p.configureOpenWrtSSH(vmid)
	case "pacman":
		p.configureArchSSH(vmid)
	case "apt-get", "apt":
		p.configureDebianBasedSSH(vmid)
	case "yum", "dnf":
		p.configureRHELBasedSSH(vmid)
	case "zypper":
		p.configureOpenSUSESSH(vmid)
	default:
		// 默认尝试Debian-based配置
		global.APP_LOG.Warn("未知的包管理器，尝试使用Debian-based配置", zap.Int("vmid", vmid), zap.String("packageManager", pkgManager))
		p.configureDebianBasedSSH(vmid)
	}

	global.APP_LOG.Info("容器SSH配置完成", zap.Int("vmid", vmid), zap.String("packageManager", pkgManager))
}

// executeContainerCommands 执行容器命令的辅助函数
func (p *ProxmoxProvider) executeContainerCommands(vmid int, commands []string, osType string) {
	for _, cmd := range commands {
		fullCmd := fmt.Sprintf("pct exec %d -- %s", vmid, cmd)
		_, err := p.sshClient.Execute(fullCmd)
		if err != nil {
			global.APP_LOG.Warn("配置容器SSH命令失败",
				zap.Int("vmid", vmid),
				zap.String("osType", osType),
				zap.String("command", cmd),
				zap.Error(err))
		}
	}
}

// initializeVnStatMonitoring 初始化vnstat流量监控
func (p *ProxmoxProvider) initializeVnStatMonitoring(ctx context.Context, vmid int, instanceName string) error {
	// 首先检查实例状态，确保实例正在运行
	vmidStr := fmt.Sprintf("%d", vmid)

	// 查找实例类型
	_, instanceType, err := p.findVMIDByNameOrID(ctx, vmidStr)
	if err != nil {
		global.APP_LOG.Warn("查找实例类型失败，跳过vnstat初始化",
			zap.String("instance_name", instanceName),
			zap.Int("vmid", vmid),
			zap.Error(err))
		return err
	}

	// 检查实例状态
	var statusCmd string
	if instanceType == "container" {
		statusCmd = fmt.Sprintf("pct status %s", vmidStr)
	} else {
		statusCmd = fmt.Sprintf("qm status %s", vmidStr)
	}

	// 等待实例运行 - 最多等待30秒
	maxWaitTime := 30 * time.Second
	checkInterval := 6 * time.Second
	startTime := time.Now()
	isRunning := false

	for {
		if time.Since(startTime) > maxWaitTime {
			global.APP_LOG.Warn("等待实例运行超时，跳过vnstat初始化",
				zap.String("instance_name", instanceName),
				zap.Int("vmid", vmid))
			return fmt.Errorf("等待实例运行超时")
		}

		statusOutput, err := p.sshClient.Execute(statusCmd)
		if err == nil && strings.Contains(statusOutput, "status: running") {
			isRunning = true
			global.APP_LOG.Info("实例已确认运行，准备初始化vnstat",
				zap.String("instance_name", instanceName),
				zap.Int("vmid", vmid),
				zap.Duration("wait_time", time.Since(startTime)))
			break
		}

		global.APP_LOG.Debug("等待实例启动以初始化vnstat",
			zap.Int("vmid", vmid),
			zap.Duration("elapsed", time.Since(startTime)))

		time.Sleep(checkInterval)
	}

	if !isRunning {
		global.APP_LOG.Warn("实例未运行，跳过vnstat初始化",
			zap.String("instance_name", instanceName),
			zap.Int("vmid", vmid))
		return fmt.Errorf("instance not running")
	}

	// 查找实例ID用于vnstat初始化
	var instanceID uint
	var instance providerModel.Instance

	// 通过provider名称查找provider记录
	var providerRecord providerModel.Provider
	if err := global.APP_DB.Where("name = ?", p.config.Name).First(&providerRecord).Error; err != nil {
		global.APP_LOG.Warn("查找provider记录失败，跳过vnstat初始化",
			zap.String("provider_name", p.config.Name),
			zap.Error(err))
		return err
	}

	if err := global.APP_DB.Where("name = ? AND provider_id = ?", instanceName, providerRecord.ID).First(&instance).Error; err != nil {
		global.APP_LOG.Warn("查找实例记录失败，跳过vnstat初始化",
			zap.String("instance_name", instanceName),
			zap.Uint("provider_id", providerRecord.ID),
			zap.Error(err))
		return err
	}

	instanceID = instance.ID

	// 初始化vnstat监控
	vnstatService := vnstat.NewService()
	if vnstatErr := vnstatService.InitializeVnStatForInstance(instanceID); vnstatErr != nil {
		global.APP_LOG.Warn("Proxmox实例创建后初始化vnStat监控失败",
			zap.Uint("instanceId", instanceID),
			zap.String("instanceName", instanceName),
			zap.Int("vmid", vmid),
			zap.Error(vnstatErr))
		return vnstatErr
	}

	global.APP_LOG.Info("Proxmox实例创建后vnStat监控初始化成功",
		zap.Uint("instanceId", instanceID),
		zap.String("instanceName", instanceName),
		zap.Int("vmid", vmid))

	// 触发流量数据同步
	syncTrigger := traffic.NewSyncTriggerService()
	syncTrigger.TriggerInstanceTrafficSync(instanceID, "Proxmox实例创建后同步")

	return nil
}

// configureInstanceSSHPasswordByVMID 专门用于设置Proxmox实例的SSH密码（使用VMID）
func (p *ProxmoxProvider) configureInstanceSSHPasswordByVMID(ctx context.Context, vmid int, config provider.InstanceConfig) error {
	global.APP_LOG.Info("开始配置Proxmox实例SSH密码",
		zap.String("instanceName", config.Name),
		zap.Int("vmid", vmid))

	// 生成随机密码
	password := p.generateRandomPassword()

	// 从metadata中获取密码，如果有的话
	if config.Metadata != nil {
		if metadataPassword, ok := config.Metadata["password"]; ok && metadataPassword != "" {
			password = metadataPassword
		}
	}

	// 等待实例完全启动并确认状态 - 最多等待90秒
	maxWaitTime := 90 * time.Second
	checkInterval := 10 * time.Second
	startTime := time.Now()
	vmidStr := fmt.Sprintf("%d", vmid)

	// 确定实例类型
	var statusCmd string
	if config.InstanceType == "container" {
		statusCmd = fmt.Sprintf("pct status %s", vmidStr)
	} else {
		statusCmd = fmt.Sprintf("qm status %s", vmidStr)
	}

	// 循环检查实例状态
	isRunning := false
	for {
		if time.Since(startTime) > maxWaitTime {
			return fmt.Errorf("等待实例启动超时，无法设置密码")
		}

		statusOutput, err := p.sshClient.Execute(statusCmd)
		if err == nil && strings.Contains(statusOutput, "status: running") {
			isRunning = true
			global.APP_LOG.Info("实例已确认运行，准备设置密码",
				zap.String("instanceName", config.Name),
				zap.Int("vmid", vmid),
				zap.Duration("wait_time", time.Since(startTime)))
			break
		}

		global.APP_LOG.Debug("等待实例启动以设置密码",
			zap.Int("vmid", vmid),
			zap.Duration("elapsed", time.Since(startTime)))

		time.Sleep(checkInterval)
	}

	// 如果是容器，额外等待一些时间确保SSH服务就绪
	if config.InstanceType == "container" && isRunning {
		time.Sleep(3 * time.Second)
	}

	// 设置SSH密码，使用vmid而不是名称
	if err := p.SetInstancePassword(ctx, vmidStr, password); err != nil {
		global.APP_LOG.Error("设置实例密码失败",
			zap.String("instanceName", config.Name),
			zap.Int("vmid", vmid),
			zap.Error(err))
		return fmt.Errorf("设置实例密码失败: %w", err)
	}

	global.APP_LOG.Info("Proxmox实例SSH密码配置成功",
		zap.String("instanceName", config.Name),
		zap.Int("vmid", vmid))

	// 更新数据库中的密码记录，确保数据库与实际密码一致
	err := global.APP_DB.Model(&providerModel.Instance{}).
		Where("name = ?", config.Name).
		Update("password", password).Error
	if err != nil {
		global.APP_LOG.Warn("更新数据库密码记录失败",
			zap.String("instanceName", config.Name),
			zap.Error(err))
		// 不返回错误，因为SSH密码已经设置成功
	}

	return nil
}

// updateInstanceNotes 更新虚拟机/容器的notes，将配置信息写入到配置文件中
func (p *ProxmoxProvider) updateInstanceNotes(ctx context.Context, vmid int, config provider.InstanceConfig) error {
	// 构建配置信息
	var notesBuilder strings.Builder
	notesBuilder.WriteString(fmt.Sprintf("VMID: %d\n", vmid))
	notesBuilder.WriteString("#\n")
	notesBuilder.WriteString(fmt.Sprintf("Name: %s\n", config.Name))
	notesBuilder.WriteString("#\n")
	notesBuilder.WriteString(fmt.Sprintf("CPU: %s\n", config.CPU))
	notesBuilder.WriteString("#\n")
	notesBuilder.WriteString(fmt.Sprintf("Memory: %s\n", config.Memory))
	notesBuilder.WriteString("#\n")
	notesBuilder.WriteString(fmt.Sprintf("Disk: %s\n", config.Disk))
	notesBuilder.WriteString("#\n")
	notesBuilder.WriteString(fmt.Sprintf("Image: %s\n", config.Image))
	notesBuilder.WriteString("#\n")
	notesBuilder.WriteString(fmt.Sprintf("Type: %s\n", config.InstanceType))
	notesBuilder.WriteString("#\n")

	// 添加网络信息
	internalIP := fmt.Sprintf("172.16.1.%d", vmid)
	notesBuilder.WriteString(fmt.Sprintf("Internal IP: %s\n", internalIP))
	notesBuilder.WriteString("#\n")

	// 添加端口信息（如果有）
	if len(config.Ports) > 0 {
		notesBuilder.WriteString("Ports: ")
		for i, port := range config.Ports {
			if i > 0 {
				notesBuilder.WriteString(", ")
			}
			notesBuilder.WriteString(port)
		}
		notesBuilder.WriteString("\n")
		notesBuilder.WriteString("#\n")
	}

	// 添加创建时间
	notesBuilder.WriteString(fmt.Sprintf("Created: %s\n", time.Now().Format("2006-01-02 15:04:05")))

	notes := notesBuilder.String()

	// 根据实例类型更新配置文件
	var configPath string
	if config.InstanceType == "container" {
		configPath = fmt.Sprintf("/etc/pve/lxc/%d.conf", vmid)
	} else {
		configPath = fmt.Sprintf("/etc/pve/qemu-server/%d.conf", vmid)
	}

	// 创建临时文件并写入notes
	tmpFile := fmt.Sprintf("/tmp/notes_%d.txt", vmid)

	// 将notes内容写入临时文件，每行前加#作为注释
	escapedNotes := strings.ReplaceAll(notes, "'", "'\\''")
	writeNotesCmd := fmt.Sprintf("echo '%s' | sed 's/^/# /' > %s", escapedNotes, tmpFile)
	_, err := p.sshClient.Execute(writeNotesCmd)
	if err != nil {
		return fmt.Errorf("写入notes临时文件失败: %w", err)
	}

	// 将原配置文件内容追加到临时文件
	appendConfigCmd := fmt.Sprintf("cat %s >> %s", configPath, tmpFile)
	_, err = p.sshClient.Execute(appendConfigCmd)
	if err != nil {
		// 清理临时文件
		p.sshClient.Execute(fmt.Sprintf("rm -f %s", tmpFile))
		return fmt.Errorf("追加配置文件失败: %w", err)
	}

	// 用临时文件替换原配置文件
	replaceCmd := fmt.Sprintf("mv %s %s", tmpFile, configPath)
	_, err = p.sshClient.Execute(replaceCmd)
	if err != nil {
		// 清理临时文件
		p.sshClient.Execute(fmt.Sprintf("rm -f %s", tmpFile))
		return fmt.Errorf("替换配置文件失败: %w", err)
	}

	global.APP_LOG.Info("成功更新实例notes",
		zap.Int("vmid", vmid),
		zap.String("name", config.Name),
		zap.String("type", config.InstanceType))

	return nil
}
