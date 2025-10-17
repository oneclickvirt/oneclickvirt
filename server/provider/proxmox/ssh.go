package proxmox

import (
	"context"
	"crypto/md5"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	systemModel "oneclickvirt/model/system"
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

// countContainers 计算容器数量的辅助函数
func countContainers(instances []provider.Instance) int {
	count := 0
	for _, instance := range instances {
		if instance.Type == "container" {
			count++
		}
	}
	return count
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

	global.APP_LOG.Info("通过SSH成功启动Proxmox实例",
		zap.String("id", utils.TruncateString(id, 50)),
		zap.String("vmid", vmid),
		zap.String("type", instanceType))
	return nil
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

// prepareImage 准备镜像，确保镜像存在且可用
func (p *ProxmoxProvider) prepareImage(ctx context.Context, imageName, instanceType string) error {
	global.APP_LOG.Info("准备Proxmox镜像",
		zap.String("image", imageName),
		zap.String("type", instanceType))

	// 创建配置结构
	config := &provider.InstanceConfig{
		Image:        imageName,
		InstanceType: instanceType,
	}

	// 首先从数据库查询匹配的系统镜像
	if err := p.queryAndSetSystemImage(ctx, config); err != nil {
		global.APP_LOG.Warn("从数据库查询系统镜像失败，使用原有镜像配置",
			zap.String("image", imageName),
			zap.Error(err))
	}

	// 如果有ImageURL，使用下载逻辑
	if config.ImageURL != "" {
		global.APP_LOG.Info("从数据库获取到镜像下载URL，开始下载",
			zap.String("imageURL", utils.TruncateString(config.ImageURL, 100)))

		return p.downloadImageFromURL(ctx, config.ImageURL, imageName, instanceType)
	}

	// 否则使用原有的模板检查逻辑
	if instanceType == "container" {
		global.APP_LOG.Warn("数据库中未找到镜像配置，无法准备容器镜像",
			zap.String("image", imageName))

		return fmt.Errorf("数据库中未找到镜像 %s 的配置，请联系管理员添加镜像", imageName)
	} else {
		// 对于VM，如果没有数据库配置，检查本地ISO文件
		global.APP_LOG.Warn("数据库中未找到VM镜像配置",
			zap.String("image", imageName))

		// 检查VM ISO文件是否存在
		checkCmd := fmt.Sprintf("ls /var/lib/vz/template/iso/ | grep -i %s", imageName)

		output, err := p.sshClient.Execute(checkCmd)
		if err != nil || strings.TrimSpace(output) == "" {
			// 镜像不存在，尝试下载
			return p.downloadImage(ctx, imageName, instanceType)
		}

		global.APP_LOG.Info("Proxmox VM镜像已存在",
			zap.String("image", imageName),
			zap.String("type", instanceType))
		return nil
	}
}

// downloadImage 下载镜像
func (p *ProxmoxProvider) downloadImage(ctx context.Context, imageName, instanceType string) error {
	global.APP_LOG.Info("开始下载Proxmox镜像",
		zap.String("image", imageName),
		zap.String("type", instanceType))

	// 检查是否有ImageURL配置
	config := &provider.InstanceConfig{
		Image:        imageName,
		InstanceType: instanceType,
	}

	// 从数据库查询镜像配置
	if err := p.queryAndSetSystemImage(ctx, config); err != nil {
		global.APP_LOG.Warn("从数据库查询镜像配置失败，回退到默认逻辑",
			zap.String("image", imageName),
			zap.Error(err))

		// 回退到原有的模板映射逻辑
		return p.downloadImageByTemplate(ctx, imageName, instanceType)
	}

	// 如果有ImageURL，使用下载逻辑
	if config.ImageURL != "" {
		return p.downloadImageFromURL(ctx, config.ImageURL, imageName, instanceType)
	}

	// 否则回退到模板逻辑
	return p.downloadImageByTemplate(ctx, imageName, instanceType)
}

// downloadImageFromURL 从URL下载镜像到远程服务器
func (p *ProxmoxProvider) downloadImageFromURL(ctx context.Context, imageURL, imageName, instanceType string) error {
	// 根据provider类型确定远程下载目录
	var downloadDir string
	if instanceType == "container" {
		downloadDir = "/var/lib/vz/template/cache"
	} else {
		downloadDir = "/var/lib/vz/template/iso"
	}

	// 生成远程文件名
	fileName := p.generateRemoteFileName(imageName, imageURL, p.config.Architecture)
	remotePath := filepath.Join(downloadDir, fileName)

	// 检查远程文件是否已存在且完整
	if p.isRemoteFileValid(remotePath) {
		global.APP_LOG.Info("远程镜像文件已存在且完整，跳过下载",
			zap.String("imageName", imageName),
			zap.String("remotePath", remotePath))
		return nil
	}

	global.APP_LOG.Info("开始在远程服务器下载镜像",
		zap.String("imageName", imageName),
		zap.String("downloadURL", imageURL),
		zap.String("remotePath", remotePath))

	// 在远程服务器上下载文件
	if err := p.downloadFileToRemote(imageURL, remotePath); err != nil {
		// 下载失败，删除不完整的文件
		p.removeRemoteFile(remotePath)
		return fmt.Errorf("远程下载镜像失败: %w", err)
	}

	global.APP_LOG.Info("远程镜像下载完成",
		zap.String("imageName", imageName),
		zap.String("remotePath", remotePath))

	return nil
}

// downloadImageByTemplate 使用模板映射下载镜像（保留原逻辑作为回退）
func (p *ProxmoxProvider) downloadImageByTemplate(ctx context.Context, imageName, instanceType string) error {
	if instanceType == "container" {
		// 对于容器，先列出可用模板
		availableCmd := "pveam available --section system"
		availableOutput, err := p.sshClient.Execute(availableCmd)
		if err != nil {
			global.APP_LOG.Warn("无法获取可用模板列表", zap.Error(err))
		} else {
			global.APP_LOG.Debug("可用模板列表", zap.String("output", availableOutput))
		}

		global.APP_LOG.Warn("数据库中未找到容器镜像配置，无法下载",
			zap.String("image", imageName),
			zap.String("type", instanceType))

		return fmt.Errorf("数据库中未找到镜像 %s 的配置，请联系管理员添加镜像", imageName)
	} else {
		// 对于VM镜像
		global.APP_LOG.Warn("数据库中未找到VM镜像配置，无法下载",
			zap.String("image", imageName),
			zap.String("type", instanceType))

		return fmt.Errorf("数据库中未找到VM镜像 %s 的配置，请联系管理员添加镜像", imageName)
	}
}

// generateRemoteFileName 生成远程文件名
func (p *ProxmoxProvider) generateRemoteFileName(imageName, imageURL, architecture string) string {
	// 组合字符串
	combined := fmt.Sprintf("%s_%s_%s", imageName, imageURL, architecture)

	// 计算MD5
	hasher := md5.New()
	hasher.Write([]byte(combined))
	md5Hash := fmt.Sprintf("%x", hasher.Sum(nil))

	// 使用镜像名称和MD5的前8位作为文件名，保持可读性
	safeName := strings.ReplaceAll(imageName, "/", "_")
	safeName = strings.ReplaceAll(safeName, ":", "_")

	// 根据URL中的文件扩展名决定下载后的文件扩展名
	if strings.Contains(imageURL, ".qcow2") {
		return fmt.Sprintf("%s_%s.qcow2", safeName, md5Hash[:8])
	} else if strings.Contains(imageURL, ".iso") {
		return fmt.Sprintf("%s_%s.iso", safeName, md5Hash[:8])
	} else if strings.Contains(imageURL, ".tar.xz") {
		return fmt.Sprintf("%s_%s.tar.xz", safeName, md5Hash[:8])
	} else if strings.Contains(imageURL, ".zip") {
		return fmt.Sprintf("%s_%s.zip", safeName, md5Hash[:8])
	} else {
		// 默认使用通用扩展名
		return fmt.Sprintf("%s_%s.img", safeName, md5Hash[:8])
	}
}

// isRemoteFileValid 检查远程文件是否存在且完整
func (p *ProxmoxProvider) isRemoteFileValid(remotePath string) bool {
	output, err := p.sshClient.Execute(fmt.Sprintf("test -f %s && echo 'exists'", remotePath))
	if err != nil {
		return false
	}
	return strings.TrimSpace(output) == "exists"
}

// removeRemoteFile 删除远程文件
func (p *ProxmoxProvider) removeRemoteFile(remotePath string) error {
	_, err := p.sshClient.Execute(fmt.Sprintf("rm -f %s", remotePath))
	return err
}

// queryAndSetSystemImage 从数据库查询匹配的系统镜像记录并设置到配置中
func (p *ProxmoxProvider) queryAndSetSystemImage(ctx context.Context, config *provider.InstanceConfig) error {
	// 构建查询条件
	var systemImage systemModel.SystemImage
	query := global.APP_DB.WithContext(ctx).Where("provider_type = ?", "proxmox")

	// 按实例类型筛选
	if config.InstanceType == "vm" {
		query = query.Where("instance_type = ?", "vm")
	} else {
		query = query.Where("instance_type = ?", "container")
	}

	// 按操作系统匹配（如果配置中有指定）
	if config.Image != "" {
		// 尝试从镜像名中提取操作系统信息
		imageLower := strings.ToLower(config.Image)
		query = query.Where("LOWER(os_type) LIKE ? OR LOWER(name) LIKE ?", "%"+imageLower+"%", "%"+imageLower+"%")
	}

	// 按架构筛选
	if p.config.Architecture != "" {
		query = query.Where("architecture = ?", p.config.Architecture)
	} else {
		// 默认使用amd64
		query = query.Where("architecture = ?", "amd64")
	}

	// 优先获取启用状态的镜像
	query = query.Where("status = ?", "active").Order("created_at DESC")

	err := query.First(&systemImage).Error
	if err != nil {
		return fmt.Errorf("未找到匹配的系统镜像: %w", err)
	}

	// 设置镜像配置
	if systemImage.URL != "" {
		config.ImageURL = systemImage.URL
		global.APP_LOG.Info("从数据库获取到系统镜像配置",
			zap.String("imageName", systemImage.Name),
			zap.String("downloadURL", utils.TruncateString(systemImage.URL, 100)),
			zap.String("osType", systemImage.OSType),
			zap.String("osVersion", systemImage.OSVersion),
			zap.String("architecture", systemImage.Architecture),
			zap.String("instanceType", systemImage.InstanceType))
	}

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

		// 下载镜像文件
		downloadCmd := fmt.Sprintf("curl -L -o %s %s", localImagePath, systemConfig.ImageURL)
		_, err = p.sshClient.Execute(downloadCmd)
		if err != nil {
			return fmt.Errorf("下载镜像失败: %v", err)
		}
		global.APP_LOG.Info("容器镜像下载完成",
			zap.String("image_path", localImagePath),
			zap.String("url", systemConfig.ImageURL))
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

		// 下载镜像文件
		downloadCmd := fmt.Sprintf("curl -L -o %s %s", localImagePath, systemConfig.ImageURL)
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

	// 创建虚拟机（参考脚本 create_vm），包含IPv6网络接口
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

	// 调整磁盘大小（参考脚本）
	resizeCmd := fmt.Sprintf("qm resize %d scsi0 %s", vmid, diskFormatted)
	_, err = p.sshClient.Execute(resizeCmd)
	if err != nil {
		// 尝试以MB为单位重试
		if strings.HasSuffix(diskFormatted, "G") {
			diskNum := diskFormatted[:len(diskFormatted)-1]
			if diskMB, parseErr := strconv.Atoi(diskNum); parseErr == nil {
				diskMB *= 1024
				resizeCmd = fmt.Sprintf("qm resize %d scsi0 %dM", vmid, diskMB)
				_, err = p.sshClient.Execute(resizeCmd)
			}
		}
		if err != nil {
			global.APP_LOG.Warn("调整磁盘大小失败", zap.Int("vmid", vmid), zap.Error(err))
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

	// 安装并配置SSH
	sshCommands := []string{
		"apt-get update -y",
		"apt-get install -y openssh-server curl",
		"systemctl enable ssh",
		"systemctl start ssh",
		"sed -i 's/#PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config",
		"sed -i 's/#PasswordAuthentication.*/PasswordAuthentication yes/' /etc/ssh/sshd_config",
		"systemctl restart ssh",
	}

	for _, cmd := range sshCommands {
		fullCmd := fmt.Sprintf("pct exec %d -- %s", vmid, cmd)
		_, err := p.sshClient.Execute(fullCmd)
		if err != nil {
			global.APP_LOG.Warn("配置容器SSH命令失败",
				zap.Int("vmid", vmid),
				zap.String("command", cmd),
				zap.Error(err))
		}
	}
}

// initializeVnStatMonitoring 初始化vnstat流量监控
func (p *ProxmoxProvider) initializeVnStatMonitoring(ctx context.Context, vmid int, instanceName string) error {
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

	// 等待实例完全启动
	time.Sleep(5 * time.Second)

	// 从metadata中获取密码，如果有的话
	if config.Metadata != nil {
		if metadataPassword, ok := config.Metadata["password"]; ok && metadataPassword != "" {
			password = metadataPassword
		}
	}

	// 设置SSH密码，使用vmid而不是名称
	vmidStr := fmt.Sprintf("%d", vmid)
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

// configureInstanceSSHPassword 专门用于设置Proxmox实例的SSH密码
func (p *ProxmoxProvider) configureInstanceSSHPassword(ctx context.Context, config provider.InstanceConfig) error {
	global.APP_LOG.Info("开始配置Proxmox实例SSH密码",
		zap.String("instanceName", config.Name))

	// 生成随机密码
	password := p.generateRandomPassword()

	// 等待实例完全启动
	time.Sleep(5 * time.Second)

	// 从metadata中获取密码，如果有的话
	if config.Metadata != nil {
		if metadataPassword, ok := config.Metadata["password"]; ok && metadataPassword != "" {
			password = metadataPassword
		}
	}

	// 设置SSH密码
	if err := p.SetInstancePassword(ctx, config.Name, password); err != nil {
		global.APP_LOG.Error("设置实例密码失败",
			zap.String("instanceName", config.Name),
			zap.Error(err))
		return fmt.Errorf("设置实例密码失败: %w", err)
	}

	global.APP_LOG.Info("Proxmox实例SSH密码配置成功",
		zap.String("instanceName", config.Name))

	// 更新数据库中的密码记录，确保数据库与实际密码一致
	err := global.APP_DB.Model(&providerModel.Instance{}).
		Where("name = ?", config.Name).
		Update("password", password).Error
	if err != nil {
		global.APP_LOG.Warn("更新实例密码到数据库失败",
			zap.String("instanceName", config.Name),
			zap.Error(err))
	} else {
		global.APP_LOG.Info("实例密码已同步到数据库",
			zap.String("instanceName", config.Name))
	}

	return nil
}

// configureInstancePortMappings 配置实例端口映射
func (p *ProxmoxProvider) configureInstancePortMappings(ctx context.Context, config provider.InstanceConfig, vmid int) error {
	// 等待实例完全启动
	time.Sleep(3 * time.Second)

	global.APP_LOG.Info("开始配置PVE实例端口映射",
		zap.String("instance", config.Name),
		zap.Int("vmid", vmid))

	// 确定实例类型
	instanceType := config.InstanceType
	if instanceType == "" {
		instanceType = "vm" // 默认为虚拟机
	}

	// 获取实例的内网IP地址，使用vmid而不是名称
	vmidStr := fmt.Sprintf("%d", vmid)
	instanceIP, err := p.getInstanceIPAddress(ctx, vmidStr, instanceType)
	if err != nil {
		global.APP_LOG.Error("获取实例内网IP失败",
			zap.String("instance", config.Name),
			zap.Int("vmid", vmid),
			zap.Error(err))
		return fmt.Errorf("获取实例内网IP失败: %w", err)
	}

	if instanceIP == "" {
		global.APP_LOG.Error("获取到空的实例IP地址",
			zap.String("instance", config.Name),
			zap.Int("vmid", vmid))
		return fmt.Errorf("无法获取实例 %s 的IP地址", config.Name)
	}

	global.APP_LOG.Info("获取到实例内网IP",
		zap.String("instance", config.Name),
		zap.Int("vmid", vmid),
		zap.String("instanceIP", instanceIP))

	// 解析网络配置
	networkConfig := p.parseNetworkConfigFromInstanceConfig(config)

	// 调用现有的端口映射配置函数（使用ports.go中的实现）
	err = p.configurePortMappingsWithIP(ctx, config.Name, networkConfig, instanceIP)
	if err != nil {
		global.APP_LOG.Error("配置端口映射失败",
			zap.String("instance", config.Name),
			zap.Error(err))
		return fmt.Errorf("配置端口映射失败: %w", err)
	}

	global.APP_LOG.Info("PVE实例端口映射配置成功",
		zap.String("instance", config.Name),
		zap.Int("vmid", vmid))

	return nil
}

// cleanupInstancePortMappings 清理实例的端口映射
func (p *ProxmoxProvider) cleanupInstancePortMappings(ctx context.Context, vmid string, instanceType string) error {
	global.APP_LOG.Info("开始清理实例端口映射",
		zap.String("vmid", vmid),
		zap.String("instanceType", instanceType))

	// 1. 查找通过vmid对应的实例名称
	instances, err := p.ListInstances(ctx)
	if err != nil {
		global.APP_LOG.Warn("获取实例列表失败，尝试通过vmid清理端口映射", zap.String("vmid", vmid), zap.Error(err))
		// 即使获取实例列表失败，也要尝试清理端口映射
	}

	var instanceName string
	for _, instance := range instances {
		// 从实例ID中提取vmid（假设ID格式是vmid或包含vmid）
		if instance.ID == vmid || strings.Contains(instance.ID, vmid) {
			instanceName = instance.Name
			break
		}
	}

	// 2. 如果找到了实例名称，尝试从数据库获取端口映射进行清理
	if instanceName != "" {
		global.APP_LOG.Info("找到实例名称，开始清理数据库中的端口映射",
			zap.String("vmid", vmid),
			zap.String("instanceName", instanceName))

		// 从数据库获取实例的端口映射
		var instance providerModel.Instance
		if err := global.APP_DB.Where("name = ?", instanceName).First(&instance).Error; err != nil {
			global.APP_LOG.Warn("从数据库获取实例信息失败", zap.String("instanceName", instanceName), zap.Error(err))
		} else {
			// 获取实例的所有端口映射
			var portMappings []providerModel.Port
			if err := global.APP_DB.Where("instance_id = ? AND status = 'active'", instance.ID).Find(&portMappings).Error; err != nil {
				global.APP_LOG.Warn("获取端口映射失败", zap.String("instanceName", instanceName), zap.Error(err))
			} else {
				// 清理每个端口映射
				for _, port := range portMappings {
					if err := p.removePortMapping(ctx, instanceName, port.HostPort, port.Protocol, port.MappingMethod); err != nil {
						global.APP_LOG.Warn("移除端口映射失败",
							zap.String("instanceName", instanceName),
							zap.Int("hostPort", port.HostPort),
							zap.String("protocol", port.Protocol),
							zap.Error(err))
					} else {
						global.APP_LOG.Info("端口映射清理成功",
							zap.String("instanceName", instanceName),
							zap.Int("hostPort", port.HostPort),
							zap.String("protocol", port.Protocol))
					}
				}
			}
		}
	}

	// 3. 尝试基于推断的IP地址清理iptables规则（针对虚拟机的标准IP分配规则）
	if instanceType == "vm" {
		vmidInt, err := strconv.Atoi(vmid)
		if err == nil && vmidInt > 0 && vmidInt < 255 {
			inferredIP := fmt.Sprintf("172.16.1.%d", vmidInt)
			global.APP_LOG.Info("尝试基于推断IP清理iptables规则",
				zap.String("vmid", vmid),
				zap.String("inferredIP", inferredIP))

			// 清理常见的端口映射规则
			if err := p.cleanupIptablesRulesForIP(ctx, inferredIP); err != nil {
				global.APP_LOG.Warn("清理推断IP的iptables规则失败",
					zap.String("inferredIP", inferredIP),
					zap.Error(err))
			}
		}
	}

	global.APP_LOG.Info("实例端口映射清理完成",
		zap.String("vmid", vmid),
		zap.String("instanceType", instanceType))

	return nil
}

// cleanupIptablesRulesForIP 清理指定IP地址的iptables规则
func (p *ProxmoxProvider) cleanupIptablesRulesForIP(ctx context.Context, ipAddress string) error {
	global.APP_LOG.Info("清理IP地址的iptables规则", zap.String("ipAddress", ipAddress))

	// 清理DNAT规则
	dnatCmd := fmt.Sprintf("iptables -t nat -S PREROUTING | grep 'DNAT.*%s' | sed 's/^-A /-D /' | while read line; do iptables -t nat $line 2>/dev/null || true; done", ipAddress)
	_, err := p.sshClient.Execute(dnatCmd)
	if err != nil {
		global.APP_LOG.Warn("清理DNAT规则失败", zap.String("ipAddress", ipAddress), zap.Error(err))
	}

	// 清理FORWARD规则
	forwardCmd := fmt.Sprintf("iptables -S FORWARD | grep '%s' | sed 's/^-A /-D /' | while read line; do iptables $line 2>/dev/null || true; done", ipAddress)
	_, err = p.sshClient.Execute(forwardCmd)
	if err != nil {
		global.APP_LOG.Warn("清理FORWARD规则失败", zap.String("ipAddress", ipAddress), zap.Error(err))
	}

	// 清理MASQUERADE规则
	masqueradeCmd := fmt.Sprintf("iptables -t nat -S POSTROUTING | grep '%s' | sed 's/^-A /-D /' | while read line; do iptables -t nat $line 2>/dev/null || true; done", ipAddress)
	_, err = p.sshClient.Execute(masqueradeCmd)
	if err != nil {
		global.APP_LOG.Warn("清理MASQUERADE规则失败", zap.String("ipAddress", ipAddress), zap.Error(err))
	}

	// 保存iptables规则
	_, err = p.sshClient.Execute("iptables-save > /etc/iptables/rules.v4 2>/dev/null || true")
	if err != nil {
		global.APP_LOG.Warn("保存iptables规则失败", zap.Error(err))
	}

	return nil
}

// configureInstanceIPv6 配置实例IPv6网络（参考buildvm_onlyv6.sh脚本）
func (p *ProxmoxProvider) configureInstanceIPv6(ctx context.Context, vmid int, config provider.InstanceConfig, instanceType string) error {
	// 解析网络配置
	networkConfig := p.parseNetworkConfigFromInstanceConfig(config)

	global.APP_LOG.Info("开始配置实例IPv6网络",
		zap.Int("vmid", vmid),
		zap.String("instance", config.Name),
		zap.String("type", instanceType),
		zap.String("networkType", networkConfig.NetworkType))

	// 检查是否需要配置IPv6
	hasIPv6 := networkConfig.NetworkType == "nat_ipv4_ipv6" ||
		networkConfig.NetworkType == "dedicated_ipv4_ipv6" ||
		networkConfig.NetworkType == "ipv6_only"

	if !hasIPv6 {
		global.APP_LOG.Info("网络类型不包含IPv6，跳过IPv6配置",
			zap.Int("vmid", vmid),
			zap.String("networkType", networkConfig.NetworkType))
		return nil
	}

	// 检查IPv6环境和配置
	if err := p.checkIPv6Environment(ctx); err != nil {
		// IPv6环境检查失败，如果是ipv6_only模式则返回错误，否则记录警告
		if networkConfig.NetworkType == "ipv6_only" {
			return fmt.Errorf("IPv6环境检查失败（ipv6_only模式要求IPv6环境）: %w", err)
		}
		global.APP_LOG.Warn("IPv6环境检查失败，跳过IPv6配置", zap.Error(err))
		return nil
	}

	// 获取IPv6基础信息
	ipv6Info, err := p.getIPv6Info(ctx)
	if err != nil {
		if networkConfig.NetworkType == "ipv6_only" {
			return fmt.Errorf("获取IPv6信息失败（ipv6_only模式要求IPv6信息）: %w", err)
		}
		global.APP_LOG.Warn("获取IPv6信息失败，跳过IPv6配置", zap.Error(err))
		return nil
	}

	// 根据网络类型配置IPv6
	switch networkConfig.NetworkType {
	case "nat_ipv4_ipv6":
		// NAT模式的IPv4+IPv6
		return p.configureIPv6Network(ctx, vmid, config, instanceType, ipv6Info, false)
	case "dedicated_ipv4_ipv6":
		// 独立的IPv4+IPv6
		return p.configureIPv6Network(ctx, vmid, config, instanceType, ipv6Info, false)
	case "ipv6_only":
		// 纯IPv6模式
		return p.configureIPv6Network(ctx, vmid, config, instanceType, ipv6Info, true)
	}

	return nil
}

// IPv6Info IPv6配置信息
type IPv6Info struct {
	HostIPv6Address      string // 主机IPv6地址
	IPv6AddressPrefix    string // IPv6地址前缀
	IPv6PrefixLen        string // IPv6前缀长度
	IPv6Gateway          string // IPv6网关
	HasAppendedAddresses bool   // 是否存在额外的IPv6地址
}

// checkIPv6Environment 检查IPv6环境（参考脚本check_environment函数）
func (p *ProxmoxProvider) checkIPv6Environment(ctx context.Context) error {
	appendedFile := "/usr/local/bin/pve_appended_content.txt"

	// 检查是否有appended_content文件
	checkCmd := fmt.Sprintf("[ -s '%s' ]", appendedFile)
	_, err := p.sshClient.Execute(checkCmd)

	if err != nil {
		// 如果没有appended_content文件，检查基础IPv6环境
		if err := p.checkBasicIPv6Environment(ctx); err != nil {
			return err
		}
	} else {
		global.APP_LOG.Info("检测到额外的IPv6地址用于NAT映射")
	}

	return nil
}

// checkBasicIPv6Environment 检查基础IPv6环境
func (p *ProxmoxProvider) checkBasicIPv6Environment(ctx context.Context) error {
	// 检查IPv6地址文件是否存在
	checkIPv6Cmd := "[ -f /usr/local/bin/pve_check_ipv6 ]"
	_, err := p.sshClient.Execute(checkIPv6Cmd)
	if err != nil {
		return fmt.Errorf("没有IPv6地址用于开设带独立IPv6地址的服务")
	}

	// 检查vmbr2网桥是否存在
	checkVmbrCmd := "grep -q 'vmbr2' /etc/network/interfaces"
	_, err = p.sshClient.Execute(checkVmbrCmd)
	if err != nil {
		return fmt.Errorf("没有vmbr2网桥用于开设带独立IPv6地址的服务")
	}

	// 检查ndpresponder服务状态
	checkServiceCmd := "systemctl is-active ndpresponder.service"
	output, err := p.sshClient.Execute(checkServiceCmd)
	if err != nil || strings.TrimSpace(output) != "active" {
		return fmt.Errorf("ndpresponder服务状态异常，无法开设带独立IPv6地址的服务")
	}

	global.APP_LOG.Info("ndpresponder服务运行正常，可以开设带独立IPv6地址的服务")
	return nil
}

// getIPv6Info 获取IPv6配置信息（参考脚本get_ipv6_info函数）
func (p *ProxmoxProvider) getIPv6Info(ctx context.Context) (*IPv6Info, error) {
	info := &IPv6Info{}

	// 检查是否存在额外的IPv6地址
	appendedFile := "/usr/local/bin/pve_appended_content.txt"
	checkCmd := fmt.Sprintf("[ -s '%s' ]", appendedFile)
	_, err := p.sshClient.Execute(checkCmd)
	info.HasAppendedAddresses = (err == nil)

	// 获取主机IPv6地址
	if _, err := p.sshClient.Execute("[ -f /usr/local/bin/pve_check_ipv6 ]"); err == nil {
		output, err := p.sshClient.Execute("cat /usr/local/bin/pve_check_ipv6")
		if err == nil {
			info.HostIPv6Address = strings.TrimSpace(output)
			// 生成IPv6地址前缀
			if info.HostIPv6Address != "" {
				parts := strings.Split(info.HostIPv6Address, ":")
				if len(parts) > 1 {
					info.IPv6AddressPrefix = strings.Join(parts[:len(parts)-1], ":") + ":"
				}
			}
		}
	}

	// 获取IPv6前缀长度
	if _, err := p.sshClient.Execute("[ -f /usr/local/bin/pve_ipv6_prefixlen ]"); err == nil {
		output, err := p.sshClient.Execute("cat /usr/local/bin/pve_ipv6_prefixlen")
		if err == nil {
			info.IPv6PrefixLen = strings.TrimSpace(output)
		}
	}

	// 获取IPv6网关
	if _, err := p.sshClient.Execute("[ -f /usr/local/bin/pve_ipv6_gateway ]"); err == nil {
		output, err := p.sshClient.Execute("cat /usr/local/bin/pve_ipv6_gateway")
		if err == nil {
			info.IPv6Gateway = strings.TrimSpace(output)
		}
	}

	return info, nil
}

// configureIPv6Network 配置IPv6网络（合并NAT和直接映射逻辑）
func (p *ProxmoxProvider) configureIPv6Network(ctx context.Context, vmid int, config provider.InstanceConfig, instanceType string, ipv6Info *IPv6Info, ipv6Only bool) error {
	// 选择网桥和配置模式
	var bridgeName string
	var useNATMapping bool

	if ipv6Info.HasAppendedAddresses {
		// 有额外IPv6地址，使用NAT映射模式
		bridgeName = "vmbr1"
		useNATMapping = true
	} else {
		// 使用直接分配模式
		bridgeName = "vmbr2"
		useNATMapping = false
	}

	global.APP_LOG.Info("配置IPv6网络",
		zap.Int("vmid", vmid),
		zap.String("instanceType", instanceType),
		zap.String("bridge", bridgeName),
		zap.Bool("useNAT", useNATMapping),
		zap.Bool("ipv6Only", ipv6Only))

	if instanceType == "vm" {
		return p.configureVMIPv6(ctx, vmid, config, bridgeName, useNATMapping, ipv6Info, ipv6Only)
	} else {
		return p.configureContainerIPv6(ctx, vmid, config, bridgeName, useNATMapping, ipv6Info, ipv6Only)
	}
}

// configureVMIPv6 配置虚拟机IPv6
func (p *ProxmoxProvider) configureVMIPv6(ctx context.Context, vmid int, config provider.InstanceConfig, bridgeName string, useNATMapping bool, ipv6Info *IPv6Info, ipv6Only bool) error {
	if useNATMapping {
		// NAT映射模式
		vmInternalIPv6 := fmt.Sprintf("2001:db8:1::%d", vmid)

		if ipv6Only {
			// IPv6-only: net0为IPv6
			net0Cmd := fmt.Sprintf("qm set %d --net0 virtio,bridge=%s,firewall=0", vmid, bridgeName)
			_, err := p.sshClient.Execute(net0Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置虚拟机IPv6-only net0接口失败", zap.Int("vmid", vmid), zap.Error(err))
			}

			ipv6Cmd := fmt.Sprintf("qm set %d --ipconfig0 ip6='%s/64',gw6='2001:db8:1::1'", vmid, vmInternalIPv6)
			_, err = p.sshClient.Execute(ipv6Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置虚拟机IPv6失败", zap.Int("vmid", vmid), zap.Error(err))
			}
		} else {
			// IPv4+IPv6: net1为IPv6
			netCmd := fmt.Sprintf("qm set %d --net1 virtio,bridge=%s,firewall=0", vmid, bridgeName)
			_, err := p.sshClient.Execute(netCmd)
			if err != nil {
				global.APP_LOG.Warn("添加虚拟机net1接口失败", zap.Int("vmid", vmid), zap.Error(err))
			}

			ipv6Cmd := fmt.Sprintf("qm set %d --ipconfig1 ip6='%s/64',gw6='2001:db8:1::1'", vmid, vmInternalIPv6)
			_, err = p.sshClient.Execute(ipv6Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置虚拟机IPv6失败", zap.Int("vmid", vmid), zap.Error(err))
			}
		}

		// 获取可用的外部IPv6地址并设置NAT映射
		hostExternalIPv6, err := p.getAvailableVmbr1IPv6(ctx)
		if err != nil {
			return fmt.Errorf("没有可用的IPv6地址用于NAT映射: %w", err)
		}

		return p.setupNATMapping(ctx, vmInternalIPv6, hostExternalIPv6)

	} else {
		// 直接分配模式
		vmExternalIPv6 := fmt.Sprintf("%s%d", ipv6Info.IPv6AddressPrefix, vmid)

		if ipv6Only {
			// IPv6-only: net0为IPv6
			net0Cmd := fmt.Sprintf("qm set %d --net0 virtio,bridge=%s,firewall=0", vmid, bridgeName)
			_, err := p.sshClient.Execute(net0Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置虚拟机IPv6-only net0接口失败", zap.Int("vmid", vmid), zap.Error(err))
			}

			ipv6Cmd := fmt.Sprintf("qm set %d --ipconfig0 ip6='%s/128',gw6='%s'", vmid, vmExternalIPv6, ipv6Info.HostIPv6Address)
			_, err = p.sshClient.Execute(ipv6Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置虚拟机IPv6失败", zap.Int("vmid", vmid), zap.Error(err))
			}
		} else {
			// IPv4+IPv6: net1为IPv6
			netCmd := fmt.Sprintf("qm set %d --net1 virtio,bridge=%s,firewall=0", vmid, bridgeName)
			_, err := p.sshClient.Execute(netCmd)
			if err != nil {
				global.APP_LOG.Warn("添加虚拟机net1接口失败", zap.Int("vmid", vmid), zap.Error(err))
			}

			ipv6Cmd := fmt.Sprintf("qm set %d --ipconfig1 ip6='%s/128',gw6='%s'", vmid, vmExternalIPv6, ipv6Info.HostIPv6Address)
			_, err = p.sshClient.Execute(ipv6Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置虚拟机IPv6失败", zap.Int("vmid", vmid), zap.Error(err))
			}
		}
	}

	return nil
}

// configureContainerIPv6 配置容器IPv6
func (p *ProxmoxProvider) configureContainerIPv6(ctx context.Context, vmid int, config provider.InstanceConfig, bridgeName string, useNATMapping bool, ipv6Info *IPv6Info, ipv6Only bool) error {
	if useNATMapping {
		// NAT映射模式
		vmInternalIPv6 := fmt.Sprintf("2001:db8:1::%d", vmid)

		if ipv6Only {
			// IPv6-only: net0为IPv6
			net0Cmd := fmt.Sprintf("pct set %d --net0 name=eth0,ip6='%s/64',bridge=%s,gw6='2001:db8:1::1'", vmid, vmInternalIPv6, bridgeName)
			_, err := p.sshClient.Execute(net0Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置容器IPv6-only接口失败", zap.Int("vmid", vmid), zap.Error(err))
			}
		} else {
			// IPv4+IPv6: net0为IPv4，net1为IPv6
			user_ip := fmt.Sprintf("172.16.1.%d", vmid)
			net0Cmd := fmt.Sprintf("pct set %d --net0 name=eth0,ip=%s/24,bridge=vmbr1,gw=172.16.1.1", vmid, user_ip)
			_, err := p.sshClient.Execute(net0Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置容器IPv4接口失败", zap.Int("vmid", vmid), zap.Error(err))
			}

			net1Cmd := fmt.Sprintf("pct set %d --net1 name=eth1,ip6='%s/64',bridge=%s,gw6='2001:db8:1::1'", vmid, vmInternalIPv6, bridgeName)
			_, err = p.sshClient.Execute(net1Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置容器IPv6接口失败", zap.Int("vmid", vmid), zap.Error(err))
			}
		}

		// 配置DNS
		var dnsCmd string
		if ipv6Only {
			dnsCmd = fmt.Sprintf("pct set %d --nameserver '2001:4860:4860::8888 2001:4860:4860::8844'", vmid)
		} else {
			dnsCmd = fmt.Sprintf("pct set %d --nameserver '8.8.8.8 8.8.4.4 2001:4860:4860::8888 2001:4860:4860::8844'", vmid)
		}
		_, err := p.sshClient.Execute(dnsCmd)
		if err != nil {
			global.APP_LOG.Warn("配置容器DNS失败", zap.Int("vmid", vmid), zap.Error(err))
		}

		// 获取可用的外部IPv6地址并设置NAT映射
		hostExternalIPv6, err := p.getAvailableVmbr1IPv6(ctx)
		if err != nil {
			return fmt.Errorf("没有可用的IPv6地址用于NAT映射: %w", err)
		}

		return p.setupNATMapping(ctx, vmInternalIPv6, hostExternalIPv6)

	} else {
		// 直接分配模式
		vmExternalIPv6 := fmt.Sprintf("%s%d", ipv6Info.IPv6AddressPrefix, vmid)

		if ipv6Only {
			// IPv6-only: net0为IPv6
			net0Cmd := fmt.Sprintf("pct set %d --net0 name=eth0,ip6='%s/128',bridge=%s,gw6='%s'", vmid, vmExternalIPv6, bridgeName, ipv6Info.HostIPv6Address)
			_, err := p.sshClient.Execute(net0Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置容器IPv6-only接口失败", zap.Int("vmid", vmid), zap.Error(err))
			}
		} else {
			// IPv4+IPv6: net0为IPv4，net1为IPv6
			user_ip := fmt.Sprintf("172.16.1.%d", vmid)
			net0Cmd := fmt.Sprintf("pct set %d --net0 name=eth0,ip=%s/24,bridge=vmbr1,gw=172.16.1.1", vmid, user_ip)
			_, err := p.sshClient.Execute(net0Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置容器IPv4接口失败", zap.Int("vmid", vmid), zap.Error(err))
			}

			net1Cmd := fmt.Sprintf("pct set %d --net1 name=eth1,ip6='%s/128',bridge=%s,gw6='%s'", vmid, vmExternalIPv6, bridgeName, ipv6Info.HostIPv6Address)
			_, err = p.sshClient.Execute(net1Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置容器IPv6接口失败", zap.Int("vmid", vmid), zap.Error(err))
			}
		}

		// 配置DNS
		var dnsCmd string
		if ipv6Only {
			dnsCmd = fmt.Sprintf("pct set %d --nameserver '2001:4860:4860::8888 2001:4860:4860::8844'", vmid)
		} else {
			dnsCmd = fmt.Sprintf("pct set %d --nameserver '8.8.8.8 8.8.4.4 2001:4860:4860::8888 2001:4860:4860::8844'", vmid)
		}
		_, err := p.sshClient.Execute(dnsCmd)
		if err != nil {
			global.APP_LOG.Warn("配置容器DNS失败", zap.Int("vmid", vmid), zap.Error(err))
		}
	}

	return nil
}

// configureIPv6WithNAT 使用NAT方式配置IPv6（参考脚本中的NAT映射逻辑）
func (p *ProxmoxProvider) configureIPv6WithNAT(ctx context.Context, vmid int, config provider.InstanceConfig, instanceType string, ipv6Info *IPv6Info, ipv6Only bool) error {
	// 配置内部IPv6地址
	vmInternalIPv6 := fmt.Sprintf("2001:db8:1::%d", vmid)

	if instanceType == "vm" {
		// 虚拟机：根据模式配置网络接口
		var netBridge string
		if ipv6Info.HasAppendedAddresses {
			netBridge = "vmbr1"
		} else {
			netBridge = "vmbr2"
		}

		if ipv6Only {
			// ipv6_only模式：只配置IPv6，net0为IPv6
			net0Cmd := fmt.Sprintf("qm set %d --net0 virtio,bridge=%s,firewall=0", vmid, netBridge)
			_, err := p.sshClient.Execute(net0Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置虚拟机IPv6-only net0接口失败", zap.Int("vmid", vmid), zap.Error(err))
			}

			// 配置IPv6地址
			ipv6Cmd := fmt.Sprintf("qm set %d --ipconfig0 ip6='%s/64',gw6='2001:db8:1::1'", vmid, vmInternalIPv6)
			_, err = p.sshClient.Execute(ipv6Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置虚拟机IPv6失败", zap.Int("vmid", vmid), zap.Error(err))
			}
		} else {
			// nat_ipv4_ipv6模式：IPv6在net1接口
			netCmd := fmt.Sprintf("qm set %d --net1 virtio,bridge=%s,firewall=0", vmid, netBridge)
			_, err := p.sshClient.Execute(netCmd)
			if err != nil {
				global.APP_LOG.Warn("添加虚拟机net1接口失败", zap.Int("vmid", vmid), zap.Error(err))
			}

			// 配置IPv6地址
			ipv6Cmd := fmt.Sprintf("qm set %d --ipconfig1 ip6='%s/64',gw6='2001:db8:1::1'", vmid, vmInternalIPv6)
			_, err = p.sshClient.Execute(ipv6Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置虚拟机IPv6失败", zap.Int("vmid", vmid), zap.Error(err))
			}
		}
	} else {
		// 容器：根据模式配置网络接口
		if ipv6Only {
			// ipv6_only模式：只配置IPv6，net0为IPv6（如onlyv6脚本）
			net0Cmd := fmt.Sprintf("pct set %d --net0 name=eth0,ip6='%s/64',bridge=vmbr1,gw6='2001:db8:1::1'", vmid, vmInternalIPv6)
			_, err := p.sshClient.Execute(net0Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置容器IPv6-only接口失败", zap.Int("vmid", vmid), zap.Error(err))
			}
		} else {
			// nat_ipv4_ipv6模式：net0为IPv4，net1为IPv6（如常规脚本）
			user_ip := fmt.Sprintf("172.16.1.%d", vmid)

			// 配置net0为IPv4接口
			net0Cmd := fmt.Sprintf("pct set %d --net0 name=eth0,ip=%s/24,bridge=vmbr1,gw=172.16.1.1", vmid, user_ip)
			_, err := p.sshClient.Execute(net0Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置容器IPv4接口失败", zap.Int("vmid", vmid), zap.Error(err))
			}

			// 配置net1为IPv6接口
			net1Cmd := fmt.Sprintf("pct set %d --net1 name=eth1,ip6='%s/64',bridge=vmbr1,gw6='2001:db8:1::1'", vmid, vmInternalIPv6)
			_, err = p.sshClient.Execute(net1Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置容器IPv6接口失败", zap.Int("vmid", vmid), zap.Error(err))
			}
		}

		// 配置DNS
		var dnsCmd string
		if ipv6Only {
			// IPv6-only模式只配置IPv6 DNS
			dnsCmd = fmt.Sprintf("pct set %d --nameserver 2001:4860:4860::8888 --nameserver 2001:4860:4860::8844", vmid)
		} else {
			// 混合模式配置IPv4+IPv6 DNS
			dnsCmd = fmt.Sprintf("pct set %d --nameserver 8.8.8.8,2001:4860:4860::8888 --nameserver 8.8.4.4,2001:4860:4860::8844", vmid)
		}
		_, err := p.sshClient.Execute(dnsCmd)
		if err != nil {
			global.APP_LOG.Warn("配置容器DNS失败", zap.Int("vmid", vmid), zap.Error(err))
		}
	}

	// 获取可用的外部IPv6地址
	hostExternalIPv6, err := p.getAvailableVmbr1IPv6(ctx)
	if err != nil {
		return fmt.Errorf("没有可用的IPv6地址用于NAT映射: %w", err)
	}

	// 设置NAT映射
	if err := p.setupNATMapping(ctx, vmInternalIPv6, hostExternalIPv6); err != nil {
		return fmt.Errorf("设置IPv6 NAT映射失败: %w", err)
	}

	global.APP_LOG.Info("IPv6 NAT映射配置完成",
		zap.Int("vmid", vmid),
		zap.String("internal", vmInternalIPv6),
		zap.String("external", hostExternalIPv6),
		zap.Bool("ipv6Only", ipv6Only))

	return nil
} // configureIPv6WithDirectMapping 使用直接映射方式配置IPv6（参考脚本中的直接分配逻辑）
func (p *ProxmoxProvider) configureIPv6WithDirectMapping(ctx context.Context, vmid int, config provider.InstanceConfig, instanceType string, ipv6Info *IPv6Info, ipv6Only bool) error {
	if ipv6Info.IPv6AddressPrefix == "" || ipv6Info.HostIPv6Address == "" {
		return fmt.Errorf("IPv6地址信息不完整")
	}

	// 生成虚拟机的外部IPv6地址
	vmExternalIPv6 := fmt.Sprintf("%s%d", ipv6Info.IPv6AddressPrefix, vmid)

	// 根据实例类型配置IPv6
	if instanceType == "vm" {
		if ipv6Only {
			// ipv6_only模式：只配置IPv6，net0为IPv6
			net0Cmd := fmt.Sprintf("qm set %d --net0 virtio,bridge=vmbr2,firewall=0", vmid)
			_, err := p.sshClient.Execute(net0Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置虚拟机IPv6-only net0接口失败", zap.Int("vmid", vmid), zap.Error(err))
			}

			// 配置IPv6地址
			ipv6Cmd := fmt.Sprintf("qm set %d --ipconfig0 ip6='%s/128',gw6='%s'", vmid, vmExternalIPv6, ipv6Info.HostIPv6Address)
			_, err = p.sshClient.Execute(ipv6Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置虚拟机IPv6失败", zap.Int("vmid", vmid), zap.Error(err))
			}
		} else {
			// dedicated_ipv4_ipv6模式：IPv6在net1接口
			netCmd := fmt.Sprintf("qm set %d --net1 virtio,bridge=vmbr2,firewall=0", vmid)
			_, err := p.sshClient.Execute(netCmd)
			if err != nil {
				global.APP_LOG.Warn("添加虚拟机net1接口失败", zap.Int("vmid", vmid), zap.Error(err))
			}

			// 配置IPv6地址
			ipv6Cmd := fmt.Sprintf("qm set %d --ipconfig1 ip6='%s/128',gw6='%s'", vmid, vmExternalIPv6, ipv6Info.HostIPv6Address)
			_, err = p.sshClient.Execute(ipv6Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置虚拟机IPv6失败", zap.Int("vmid", vmid), zap.Error(err))
			}
		}
	} else {
		// 容器：根据模式配置网络接口
		if ipv6Only {
			// ipv6_only模式：只配置IPv6，net0为IPv6（如onlyv6脚本）
			net0Cmd := fmt.Sprintf("pct set %d --net0 name=eth0,ip6='%s/128',bridge=vmbr2,gw6='%s'", vmid, vmExternalIPv6, ipv6Info.HostIPv6Address)
			_, err := p.sshClient.Execute(net0Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置容器IPv6-only接口失败", zap.Int("vmid", vmid), zap.Error(err))
			}
		} else {
			// dedicated_ipv4_ipv6模式：net0为IPv4，net1为IPv6（如常规脚本）
			user_ip := fmt.Sprintf("172.16.1.%d", vmid)

			// 配置net0为IPv4接口
			net0Cmd := fmt.Sprintf("pct set %d --net0 name=eth0,ip=%s/24,bridge=vmbr1,gw=172.16.1.1", vmid, user_ip)
			_, err := p.sshClient.Execute(net0Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置容器IPv4接口失败", zap.Int("vmid", vmid), zap.Error(err))
			}

			// 配置net1为IPv6接口
			net1Cmd := fmt.Sprintf("pct set %d --net1 name=eth1,ip6='%s/128',bridge=vmbr2,gw6='%s'", vmid, vmExternalIPv6, ipv6Info.HostIPv6Address)
			_, err = p.sshClient.Execute(net1Cmd)
			if err != nil {
				global.APP_LOG.Warn("配置容器IPv6接口失败", zap.Int("vmid", vmid), zap.Error(err))
			}
		}

		// 配置DNS
		var dnsCmd string
		if ipv6Only {
			// IPv6-only模式只配置IPv6 DNS
			dnsCmd = fmt.Sprintf("pct set %d --nameserver 2001:4860:4860::8888 --nameserver 2001:4860:4860::8844", vmid)
		} else {
			// 混合模式配置IPv4+IPv6 DNS
			dnsCmd = fmt.Sprintf("pct set %d --nameserver 8.8.8.8,2001:4860:4860::8888 --nameserver 8.8.4.4,2001:4860:4860::8844", vmid)
		}
		_, err := p.sshClient.Execute(dnsCmd)
		if err != nil {
			global.APP_LOG.Warn("配置容器DNS失败", zap.Int("vmid", vmid), zap.Error(err))
		}
	}

	global.APP_LOG.Info("IPv6直接映射配置完成",
		zap.Int("vmid", vmid),
		zap.String("ipv6", vmExternalIPv6),
		zap.String("gateway", ipv6Info.HostIPv6Address),
		zap.Bool("ipv6Only", ipv6Only))

	return nil
}

// getAvailableVmbr1IPv6 获取可用的vmbr1 IPv6地址（参考脚本中的get_available_vmbr1_ipv6函数）
func (p *ProxmoxProvider) getAvailableVmbr1IPv6(ctx context.Context) (string, error) {
	appendedFile := "/usr/local/bin/pve_appended_content.txt"
	usedIPsFile := "/usr/local/bin/pve_used_vmbr1_ips.txt"

	// 读取可用的IPv6地址
	output, err := p.sshClient.Execute(fmt.Sprintf("cat '%s' 2>/dev/null || true", appendedFile))
	if err != nil || strings.TrimSpace(output) == "" {
		return "", fmt.Errorf("没有可用的IPv6地址")
	}

	availableIPs := strings.Split(strings.TrimSpace(output), "\n")

	// 读取已使用的IPv6地址
	usedOutput, _ := p.sshClient.Execute(fmt.Sprintf("cat '%s' 2>/dev/null || true", usedIPsFile))
	usedIPs := make(map[string]bool)
	if usedOutput != "" {
		for _, ip := range strings.Split(strings.TrimSpace(usedOutput), "\n") {
			usedIPs[strings.TrimSpace(ip)] = true
		}
	}

	// 查找第一个可用的IPv6地址
	for _, ip := range availableIPs {
		ip = strings.TrimSpace(ip)
		if ip != "" && !usedIPs[ip] {
			// 标记为已使用
			_, err := p.sshClient.Execute(fmt.Sprintf("echo '%s' >> '%s'", ip, usedIPsFile))
			if err != nil {
				global.APP_LOG.Warn("标记IPv6地址为已使用失败", zap.String("ip", ip), zap.Error(err))
			}
			return ip, nil
		}
	}

	return "", fmt.Errorf("没有可用的IPv6地址")
}

// setupNATMapping 设置IPv6 NAT映射（参考脚本中的setup_nat_mapping函数）
func (p *ProxmoxProvider) setupNATMapping(ctx context.Context, vmInternalIPv6, hostExternalIPv6 string) error {
	rulesFile := "/usr/local/bin/ipv6_nat_rules.sh"

	// 确保规则文件存在
	_, err := p.sshClient.Execute(fmt.Sprintf("touch '%s'", rulesFile))
	if err != nil {
		return fmt.Errorf("创建IPv6 NAT规则文件失败: %w", err)
	}

	// ip6tables规则
	dnatRule := fmt.Sprintf("ip6tables -t nat -A PREROUTING -d '%s' -j DNAT --to-destination '%s'", hostExternalIPv6, vmInternalIPv6)
	snatRule := fmt.Sprintf("ip6tables -t nat -A POSTROUTING -s '%s' -j SNAT --to-source '%s'", vmInternalIPv6, hostExternalIPv6)

	// 执行规则
	_, err = p.sshClient.Execute(dnatRule)
	if err != nil {
		global.APP_LOG.Warn("添加IPv6 DNAT规则失败", zap.Error(err))
	}

	_, err = p.sshClient.Execute(snatRule)
	if err != nil {
		global.APP_LOG.Warn("添加IPv6 SNAT规则失败", zap.Error(err))
	}

	// 将规则写入文件以便持久化
	rulesContent := fmt.Sprintf("%s\n%s\n", dnatRule, snatRule)
	_, err = p.sshClient.Execute(fmt.Sprintf("echo '%s' >> '%s'", rulesContent, rulesFile))
	if err != nil {
		global.APP_LOG.Warn("保存IPv6 NAT规则到文件失败", zap.Error(err))
	}

	// 重启相关服务
	_, _ = p.sshClient.Execute("systemctl daemon-reload")
	_, _ = p.sshClient.Execute("systemctl restart ipv6nat.service 2>/dev/null || true")

	global.APP_LOG.Info("IPv6 NAT映射规则配置完成",
		zap.String("internal", vmInternalIPv6),
		zap.String("external", hostExternalIPv6))

	return nil
}

// GetInstanceIPv6 获取实例的内网IPv6地址 (公开方法)
func (p *ProxmoxProvider) GetInstanceIPv6(ctx context.Context, instanceName string) (string, error) {
	// 先查找实例的VMID和类型
	vmid, instanceType, err := p.findVMIDByNameOrID(ctx, instanceName)
	if err != nil {
		return "", fmt.Errorf("failed to find instance %s: %w", instanceName, err)
	}

	return p.getInstanceIPv6ByVMID(ctx, vmid, instanceType)
}

// GetInstanceIPv4 获取实例的内网IPv4地址 (公开方法)
func (p *ProxmoxProvider) GetInstanceIPv4(ctx context.Context, instanceName string) (string, error) {
	// 复用已有的getInstanceIPAddress方法来获取内网IPv4地址
	vmid, instanceType, err := p.findVMIDByNameOrID(ctx, instanceName)
	if err != nil {
		return "", fmt.Errorf("failed to find instance %s: %w", instanceName, err)
	}

	return p.getInstanceIPAddress(ctx, vmid, instanceType)
}

// GetInstancePublicIPv6 获取实例的公网IPv6地址
func (p *ProxmoxProvider) GetInstancePublicIPv6(ctx context.Context, instanceName string) (string, error) {
	// 先查找实例的VMID和类型
	vmid, instanceType, err := p.findVMIDByNameOrID(ctx, instanceName)
	if err != nil {
		return "", fmt.Errorf("failed to find instance %s: %w", instanceName, err)
	}

	// 尝试从保存的IPv6文件中读取公网IPv6地址
	publicIPv6Cmd := fmt.Sprintf("cat %s_v6 2>/dev/null | tail -1", instanceName)
	publicIPv6Output, err := p.sshClient.Execute(publicIPv6Cmd)
	if err == nil {
		publicIPv6 := strings.TrimSpace(publicIPv6Output)
		if publicIPv6 != "" && !p.isPrivateIPv6(publicIPv6) {
			global.APP_LOG.Info("从文件获取到公网IPv6地址",
				zap.String("instanceName", instanceName),
				zap.String("publicIPv6", publicIPv6))
			return publicIPv6, nil
		}
	}

	// 如果文件中没有，尝试获取实例配置中的IPv6地址
	return p.getInstancePublicIPv6ByVMID(ctx, vmid, instanceType)
}

// getInstanceIPv6ByVMID 根据VMID获取实例内网IPv6地址
func (p *ProxmoxProvider) getInstanceIPv6ByVMID(ctx context.Context, vmid string, instanceType string) (string, error) {
	var cmd string

	if instanceType == "container" {
		// 对于容器，尝试从配置中获取IPv6地址
		// 支持 net0, net1 等多个网络接口的IPv6配置
		cmd = fmt.Sprintf("pct config %s | grep -E 'net[0-9]+:.*ip6=' | sed -n 's/.*ip6=\\([^/,[:space:]]*\\).*/\\1/p' | head -1", vmid)
		output, err := p.sshClient.Execute(cmd)
		if err == nil && strings.TrimSpace(output) != "" {
			ipv6 := strings.TrimSpace(output)
			if ipv6 != "auto" && ipv6 != "dhcp" {
				return ipv6, nil
			}
		}

		// 如果没有静态IPv6，尝试从容器内部获取
		cmd = fmt.Sprintf("pct exec %s -- ip -6 addr show | grep 'inet6.*global' | awk '{print $2}' | cut -d'/' -f1 | head -1 || true", vmid)
	} else {
		// 对于虚拟机，尝试从配置中获取IPv6地址
		// 支持 ipconfig0, ipconfig1 等多个网络接口的IPv6配置
		cmd = fmt.Sprintf("qm config %s | grep -E 'ipconfig[0-9]+:.*ip6=' | sed -n 's/.*ip6=\\([^/,[:space:]]*\\).*/\\1/p' | head -1", vmid)
		output, err := p.sshClient.Execute(cmd)
		if err == nil && strings.TrimSpace(output) != "" {
			ipv6 := strings.TrimSpace(output)
			if ipv6 != "auto" && ipv6 != "dhcp" {
				return ipv6, nil
			}
		}

		// 如果没有静态IPv6配置，尝试通过guest agent获取IPv6
		cmd = fmt.Sprintf("qm guest cmd %s network-get-interfaces 2>/dev/null | grep -o '\"ip-address\":[[:space:]]*\"[^\"]*:' | sed 's/.*\"\\([^\"]*\\)\".*/\\1/' | head -1 || true", vmid)
		output, err = p.sshClient.Execute(cmd)
		if err == nil && strings.TrimSpace(output) != "" {
			return strings.TrimSpace(output), nil
		}

		// 最后尝试从虚拟机内部获取IPv6地址
		cmd = fmt.Sprintf("qm guest exec %s -- ip -6 addr show | grep 'inet6.*global' | awk '{print $2}' | cut -d'/' -f1 | head -1 2>/dev/null || true", vmid)
	}

	output, err := p.sshClient.Execute(cmd)
	if err != nil {
		return "", err
	}

	ipv6 := strings.TrimSpace(output)
	if ipv6 == "" {
		return "", fmt.Errorf("no IPv6 address found for %s %s", instanceType, vmid)
	}

	return ipv6, nil
}

// getInstancePublicIPv6ByVMID 根据VMID获取实例公网IPv6地址
func (p *ProxmoxProvider) getInstancePublicIPv6ByVMID(ctx context.Context, vmid string, instanceType string) (string, error) {
	// 首先尝试直接从配置中获取IPv6地址（通常这就是公网IPv6地址）
	ipv6Address, err := p.getInstanceIPv6ByVMID(ctx, vmid, instanceType)
	if err == nil && ipv6Address != "" && !p.isPrivateIPv6(ipv6Address) {
		// 如果获取到的IPv6地址不是私有地址，则认为它是公网地址
		return ipv6Address, nil
	}

	// 获取IPv6信息进行进一步判断
	ipv6Info, err := p.getIPv6Info(ctx)
	if err != nil {
		return "", fmt.Errorf("获取IPv6信息失败: %w", err)
	}

	if ipv6Info.HasAppendedAddresses {
		// NAT映射模式，从映射文件中查找外部IPv6地址
		return p.getNATMappedIPv6(ctx, vmid)
	} else {
		// 直接分配模式，优先返回从配置中获取的IPv6地址
		if ipv6Address != "" {
			return ipv6Address, nil
		}

		// 如果配置中没有，尝试计算外部IPv6地址
		vmidInt, err := strconv.Atoi(vmid)
		if err == nil && vmidInt > 0 && ipv6Info.IPv6AddressPrefix != "" {
			publicIPv6 := fmt.Sprintf("%s%d", ipv6Info.IPv6AddressPrefix, vmidInt)
			return publicIPv6, nil
		}
	}

	return "", fmt.Errorf("无法获取实例公网IPv6地址")
}

// getNATMappedIPv6 获取NAT映射的外部IPv6地址
func (p *ProxmoxProvider) getNATMappedIPv6(ctx context.Context, vmid string) (string, error) {
	// 从IPv6 NAT规则文件中查找映射
	cmd := fmt.Sprintf("grep -E 'DNAT.*2001:db8:1::%s' /usr/local/bin/ipv6_nat_rules.sh 2>/dev/null | grep -oP '\\-d\\s+\\K[^\\s]+' | head -1 || true", vmid)
	output, err := p.sshClient.Execute(cmd)
	if err == nil && strings.TrimSpace(output) != "" {
		return strings.TrimSpace(output), nil
	}

	// 如果没有找到，从ip6tables规则中查找
	cmd = fmt.Sprintf("ip6tables -t nat -L PREROUTING -n | grep 'DNAT.*2001:db8:1::%s' | awk '{print $4}' | head -1 || true", vmid)
	output, err = p.sshClient.Execute(cmd)
	if err == nil && strings.TrimSpace(output) != "" {
		return strings.TrimSpace(output), nil
	}

	return "", fmt.Errorf("未找到IPv6 NAT映射")
}

// isPrivateIPv6 检查是否为私有IPv6地址
func (p *ProxmoxProvider) isPrivateIPv6(address string) bool {
	if address == "" || !strings.Contains(address, ":") {
		return true
	}

	// 私有IPv6地址范围检查
	privateRanges := []string{
		"fe80:",        // 链路本地地址
		"fc00:",        // 唯一本地地址
		"fd00:",        // 唯一本地地址
		"2001:db8:",    // 文档用途（注意：只有2001:db8:才是私有的）
		"::1",          // 回环地址
		"::ffff:",      // IPv4映射地址
		"fd42:",        // Docker等使用的私有地址
		"2001:db8:1::", // 我们在NAT映射中使用的内部地址
	}

	for _, prefix := range privateRanges {
		if strings.HasPrefix(address, prefix) {
			return true
		}
	}
	return false
}
