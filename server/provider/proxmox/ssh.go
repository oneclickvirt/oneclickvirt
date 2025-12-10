package proxmox

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	"oneclickvirt/service/pmacct"
	"oneclickvirt/service/traffic"
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
		// IP分配规则: 172.16.1.{VMID}, VMID范围: 10-255
		vmidInt, err := strconv.Atoi(vmid)
		if err == nil && vmidInt >= MinVMID && vmidInt <= MaxVMID {
			inferredIP := fmt.Sprintf("%s.%d", InternalIPPrefix, vmidInt)
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
	// 并发安全保护：VMID分配必须串行化，避免多个goroutine同时分配到相同ID
	// 使用互斥锁确保同一时间只有一个goroutine在分配VMID
	p.mu.Lock()
	defer p.mu.Unlock()

	// 统一VMID范围：10-255（不区分VM和Container类型）
	// 使用全局常量确保一致性
	global.APP_LOG.Info("开始分配VMID",
		zap.String("instanceType", instanceType),
		zap.Int("minVMID", MinVMID),
		zap.Int("maxVMID", MaxVMID),
		zap.Int("maxInstances", MaxInstances))

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

	// 检查是否已达到最大实例数量限制
	if len(usedVMIDs) >= MaxInstances {
		global.APP_LOG.Error("已达到最大实例数量限制",
			zap.Int("currentInstances", len(usedVMIDs)),
			zap.Int("maxInstances", MaxInstances))
		return 0, fmt.Errorf("已达到最大实例数量限制 (%d/%d)，无法创建新实例。请删除不用的实例或联系管理员扩展网络容量", len(usedVMIDs), MaxInstances)
	}

	// 在指定范围内寻找最小的可用VMID
	for vmid := MinVMID; vmid <= MaxVMID; vmid++ {
		if !usedVMIDs[vmid] {
			global.APP_LOG.Info("分配VMID成功",
				zap.String("instanceType", instanceType),
				zap.Int("vmid", vmid),
				zap.Int("totalUsedVMIDs", len(usedVMIDs)),
				zap.Int("remainingSlots", MaxInstances-len(usedVMIDs)))
			return vmid, nil
		}
	}

	// 如果没有可用的VMID，返回错误
	global.APP_LOG.Error("VMID范围内无可用ID",
		zap.Int("minVMID", MinVMID),
		zap.Int("maxVMID", MaxVMID),
		zap.Int("usedCount", len(usedVMIDs)))
	return 0, fmt.Errorf("在范围 %d-%d 内没有可用的VMID（已使用: %d）", MinVMID, MaxVMID, len(usedVMIDs))
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
	// 使用统一的IP分配规则: 172.16.1.{VMID}, VMID范围: 10-255
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

// initializePmacctMonitoring 初始化pmacct流量监控
func (p *ProxmoxProvider) initializePmacctMonitoring(ctx context.Context, vmid int, instanceName string) error {
	// 首先检查实例状态，确保实例正在运行
	vmidStr := fmt.Sprintf("%d", vmid)

	// 查找实例类型
	_, instanceType, err := p.findVMIDByNameOrID(ctx, vmidStr)
	if err != nil {
		global.APP_LOG.Warn("查找实例类型失败，跳过pmacct初始化",
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
			global.APP_LOG.Warn("等待实例运行超时，跳过pmacct初始化",
				zap.String("instance_name", instanceName),
				zap.Int("vmid", vmid))
			return fmt.Errorf("等待实例运行超时")
		}

		statusOutput, err := p.sshClient.Execute(statusCmd)
		if err == nil && strings.Contains(statusOutput, "status: running") {
			isRunning = true
			global.APP_LOG.Info("实例已确认运行，准备初始化pmacct",
				zap.String("instance_name", instanceName),
				zap.Int("vmid", vmid),
				zap.Duration("wait_time", time.Since(startTime)))
			break
		}

		global.APP_LOG.Debug("等待实例启动以初始化pmacct",
			zap.Int("vmid", vmid),
			zap.Duration("elapsed", time.Since(startTime)))

		time.Sleep(checkInterval)
	}

	if !isRunning {
		global.APP_LOG.Warn("实例未运行，跳过pmacct初始化",
			zap.String("instance_name", instanceName),
			zap.Int("vmid", vmid))
		return fmt.Errorf("instance not running")
	}

	// 通过provider名称查找provider记录
	var providerRecord providerModel.Provider
	if err := global.APP_DB.Where("name = ?", p.config.Name).First(&providerRecord).Error; err != nil {
		global.APP_LOG.Warn("查找provider记录失败，跳过pmacct初始化",
			zap.String("provider_name", p.config.Name),
			zap.Error(err))
		return err
	}

	// 查找实例ID用于pmacct初始化
	var instanceID uint
	var instance providerModel.Instance

	if err := global.APP_DB.Where("name = ? AND provider_id = ?", instanceName, providerRecord.ID).First(&instance).Error; err != nil {
		global.APP_LOG.Warn("查找实例记录失败，跳过pmacct初始化",
			zap.String("instance_name", instanceName),
			zap.Uint("provider_id", providerRecord.ID),
			zap.Error(err))
		return err
	}

	instanceID = instance.ID

	// 获取并更新实例的PrivateIP（确保pmacct配置使用正确的内网IP）
	ctx2, cancel2 := context.WithTimeout(ctx, 30*time.Second)
	defer cancel2()
	if privateIP, err := p.GetInstanceIPv4(ctx2, instanceName); err == nil && privateIP != "" {
		// 更新数据库中的PrivateIP
		if err := global.APP_DB.Model(&instance).Update("private_ip", privateIP).Error; err == nil {
			global.APP_LOG.Info("已更新Proxmox实例内网IP",
				zap.String("instanceName", instanceName),
				zap.String("privateIP", privateIP))
		}
	} else {
		global.APP_LOG.Warn("获取Proxmox实例内网IP失败，pmacct可能使用公网IP",
			zap.String("instanceName", instanceName),
			zap.Error(err))
	}

	// 获取并更新实例的IPv4网络接口（用于pmacct流量监控）
	// 使用pmacct Service的检测方法，保持一致性
	pmacctService := pmacct.NewService()
	if interfaceV4, err := pmacctService.DetectProxmoxNetworkInterface(p, instanceName, vmidStr); err == nil && interfaceV4 != "" {
		if err := global.APP_DB.Model(&instance).Update("pmacct_interface_v4", interfaceV4).Error; err == nil {
			global.APP_LOG.Info("已更新Proxmox实例IPv4网络接口",
				zap.String("instanceName", instanceName),
				zap.String("interfaceV4", interfaceV4))
		}
	} else {
		global.APP_LOG.Debug("未获取到IPv4网络接口",
			zap.String("instanceName", instanceName),
			zap.Error(err))
	}

	// 获取并更新实例的IPv6网络接口（如果有IPv6的话）
	// 这里依赖于实例的public_ipv6字段已经在之前被设置
	ctx4, cancel4 := context.WithTimeout(ctx, 15*time.Second)
	defer cancel4()
	if interfaceV6, err := p.GetIPv6NetworkInterface(ctx4, instanceName); err == nil && interfaceV6 != "" {
		if err := global.APP_DB.Model(&instance).Update("pmacct_interface_v6", interfaceV6).Error; err == nil {
			global.APP_LOG.Info("已更新Proxmox实例IPv6网络接口",
				zap.String("instanceName", instanceName),
				zap.String("interfaceV6", interfaceV6))
		}
	} else {
		global.APP_LOG.Debug("未获取到IPv6网络接口或实例无公网IPv6",
			zap.String("instanceName", instanceName))
	}

	// 通过provider名称查找provider记录以检查流量统计配置
	var providerRecordCheck providerModel.Provider
	if err := global.APP_DB.Where("name = ?", p.config.Name).First(&providerRecordCheck).Error; err != nil {
		global.APP_LOG.Warn("查找provider记录失败，跳过pmacct初始化",
			zap.String("provider_name", p.config.Name),
			zap.Error(err))
		return err
	}

	// 检查provider是否启用了流量统计
	if !providerRecordCheck.EnableTrafficControl {
		global.APP_LOG.Debug("Provider未启用流量统计，跳过Proxmox实例pmacct监控初始化",
			zap.String("providerName", p.config.Name),
			zap.String("instanceName", instanceName),
			zap.Int("vmid", vmid))
		return nil
	}

	// 初始化流量监控
	if pmacctErr := pmacctService.InitializePmacctForInstance(instanceID); pmacctErr != nil {
		global.APP_LOG.Warn("Proxmox实例创建后初始化 pmacct 监控失败",
			zap.Uint("instanceId", instanceID),
			zap.String("instanceName", instanceName),
			zap.Int("vmid", vmid),
			zap.Error(pmacctErr))
		return pmacctErr
	}

	global.APP_LOG.Info("Proxmox实例创建后 pmacct 监控初始化成功",
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
	// Prefix each line with '#' so the notes are clearly comments in Proxmox
	notesBuilder.WriteString(fmt.Sprintf("#VMID: %d", vmid))
	notesBuilder.WriteString("\n")
	notesBuilder.WriteString(fmt.Sprintf("#Name: %s", config.Name))
	notesBuilder.WriteString("\n")
	notesBuilder.WriteString(fmt.Sprintf("#CPU: %s", config.CPU))
	notesBuilder.WriteString("\n")
	notesBuilder.WriteString(fmt.Sprintf("#Memory: %s", config.Memory))
	notesBuilder.WriteString("\n")
	notesBuilder.WriteString(fmt.Sprintf("#Disk: %s", config.Disk))
	notesBuilder.WriteString("\n")
	notesBuilder.WriteString(fmt.Sprintf("#Image: %s", config.Image))
	notesBuilder.WriteString("\n")
	notesBuilder.WriteString(fmt.Sprintf("#Type: %s", config.InstanceType))
	notesBuilder.WriteString("\n")
	// 添加网络信息（使用统一的IP分配规则: 172.16.1.{VMID}）
	internalIP := fmt.Sprintf("172.16.1.%d", vmid)
	notesBuilder.WriteString(fmt.Sprintf("#Internal IP: %s", internalIP))
	notesBuilder.WriteString("\n")
	// 添加端口信息（如果有），并尝试解析SSH端口映射
	if len(config.Ports) > 0 {
		notesBuilder.WriteString("#Ports: ")
		for i, port := range config.Ports {
			if i > 0 {
				notesBuilder.WriteString(", ")
			}
			notesBuilder.WriteString(port)
		}
		notesBuilder.WriteString("")

		// 尝试从端口映射中找到宿主机SSH端口（guest 22）
		for _, port := range config.Ports {
			// 常见格式："0.0.0.0:10000:22/tcp" 或 "10000:22/tcp" 或 "10000:22"
			parts := strings.Split(port, ":")
			if len(parts) >= 3 {
				// 取倒数两段: hostPort, guestPart
				hostPort := parts[len(parts)-2]
				guestPart := parts[len(parts)-1]
				guestPort := strings.SplitN(guestPart, "/", 2)[0]
				if guestPort == "22" {
					notesBuilder.WriteString(fmt.Sprintf("#SSH Port: %s", hostPort))
					notesBuilder.WriteString("\n")
					break
				}
			} else if len(parts) == 2 {
				hostPort := parts[0]
				guestPart := parts[1]
				guestPort := strings.SplitN(guestPart, "/", 2)[0]
				if guestPort == "22" {
					notesBuilder.WriteString(fmt.Sprintf("#SSH Port: %s", hostPort))
					notesBuilder.WriteString("\n")
					break
				}
			}
		}
	}

	// 添加创建时间
	notesBuilder.WriteString(fmt.Sprintf("#Created: %s", time.Now().Format("2006-01-02 15:04:05")))
	notesBuilder.WriteString("\n")
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
		return fmt.Errorf("写入临时文件失败: %w", err)
	}

	// 在配置文件末尾追加notes（跳过已存在的注释行）
	appendNotesCmd := fmt.Sprintf("cat %s >> %s", tmpFile, configPath)
	_, err = p.sshClient.Execute(appendNotesCmd)
	if err != nil {
		return fmt.Errorf("追加notes到配置文件失败: %w", err)
	}

	// 删除临时文件
	_, _ = p.sshClient.Execute(fmt.Sprintf("rm -f %s", tmpFile))

	global.APP_LOG.Info("成功更新Proxmox实例notes",
		zap.Int("vmid", vmid),
		zap.String("name", config.Name))

	return nil
}
