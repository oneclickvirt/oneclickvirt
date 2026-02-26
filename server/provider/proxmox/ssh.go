package proxmox

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/provider"
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

	// 等待实例真正启动
	maxWaitTime := 120 * time.Second
	checkInterval := 3 * time.Second
	startTime := time.Now()

	for {
		// 检查是否超时
		if time.Since(startTime) > maxWaitTime {
			return fmt.Errorf("等待实例启动超时 (120秒)")
		}

		// 等待一段时间后再检查
		time.Sleep(checkInterval)

		// 检查实例状态
		statusOutput, err := p.sshClient.Execute(statusCommand)
		if err == nil && strings.Contains(statusOutput, "status: running") {
			// 实例已经启动
			global.APP_LOG.Info("Proxmox实例已成功启动",
				zap.String("id", utils.TruncateString(id, 50)),
				zap.String("vmid", vmid),
				zap.String("type", instanceType),
				zap.Duration("wait_time", time.Since(startTime)))

			// 对于VM类型，智能检测QEMU Guest Agent（可选，不影响主流程）
			if instanceType == "vm" {
				// 快速检测2次，判断是否支持Agent
				agentSupported := false
				for i := 0; i < 2; i++ {
					agentCmd := fmt.Sprintf("qm agent %s ping 2>/dev/null", vmid)
					_, err := p.sshClient.Execute(agentCmd)
					if err == nil {
						agentSupported = true
						global.APP_LOG.Info("QEMU Guest Agent已就绪",
							zap.String("vmid", vmid))
						break
					}
					time.Sleep(2 * time.Second)
				}

				// 如果未检测到，进行短时等待
				if !agentSupported {
					agentWaitTime := 12 * time.Second
					agentStartTime := time.Now()
					for time.Since(agentStartTime) < agentWaitTime {
						agentCmd := fmt.Sprintf("qm agent %s ping 2>/dev/null", vmid)
						_, err := p.sshClient.Execute(agentCmd)
						if err == nil {
							global.APP_LOG.Info("QEMU Guest Agent已就绪",
								zap.String("vmid", vmid),
								zap.Duration("elapsed", time.Since(agentStartTime)))
							break
						}
						time.Sleep(3 * time.Second)
					}
				}
			}

			// 额外等待确保系统稳定
			time.Sleep(3 * time.Second)
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

// getInstanceIPAddress 获取实例IP地址
func (p *ProxmoxProvider) getInstanceIPAddress(ctx context.Context, vmid string, instanceType string) (string, error) {
	var cmd string

	if instanceType == "container" {
		// 对于容器，首先尝试从配置中获取静态IP
		cmd = fmt.Sprintf("pct config %s | grep -oP 'ip=\\K[0-9.]+' || true", vmid)
		output, err := p.sshClient.Execute(cmd)
		if err == nil && utils.CleanCommandOutput(output) != "" {
			return utils.CleanCommandOutput(output), nil
		}

		// 如果没有静态IP，尝试从容器内部获取动态IP
		cmd = fmt.Sprintf("pct exec %s -- hostname -I | awk '{print $1}' || true", vmid)
	} else {
		// 对于虚拟机，首先尝试从配置中获取静态IP
		cmd = fmt.Sprintf("qm config %s | grep -oP 'ip=\\K[0-9.]+' || true", vmid)
		output, err := p.sshClient.Execute(cmd)
		if err == nil && utils.CleanCommandOutput(output) != "" {
			return utils.CleanCommandOutput(output), nil
		}

		// 如果没有静态IP配置，尝试通过guest agent获取IP
		cmd = fmt.Sprintf("qm guest cmd %s network-get-interfaces 2>/dev/null | grep -oP '\"ip-address\":\\s*\"\\K[^\"]+' | grep -E '^(172\\.|192\\.|10\\.)' | head -1 || true", vmid)
		output, err = p.sshClient.Execute(cmd)
		if err == nil && utils.CleanCommandOutput(output) != "" {
			return utils.CleanCommandOutput(output), nil
		}

		// 最后尝试从网络配置推断IP地址 (如果使用标准内网配置)
		// 使用VMID到IP的映射函数
		vmidInt, err := strconv.Atoi(vmid)
		if err == nil && vmidInt >= MinVMID && vmidInt <= MaxVMID {
			inferredIP := VMIDToInternalIP(vmidInt)
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

	ip := utils.CleanCommandOutput(output)
	if ip == "" {
		return "", fmt.Errorf("no IP address found for %s %s", instanceType, vmid)
	}

	return ip, nil
}
