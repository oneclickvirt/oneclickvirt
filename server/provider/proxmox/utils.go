package proxmox

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	"oneclickvirt/service/vnstat"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// getDownloadURL 确定下载URL (支持CDN)
func (p *ProxmoxProvider) getDownloadURL(originalURL string, useCDN bool) string {
	// 如果不使用CDN，直接返回原始URL
	if !useCDN {
		global.APP_LOG.Info("镜像配置不使用CDN，使用原始URL",
			zap.String("originalURL", utils.TruncateString(originalURL, 100)))
		return originalURL
	}

	// 尝试使用CDN
	if cdnURL := p.getCDNURL(originalURL); cdnURL != "" {
		return cdnURL
	}
	return originalURL
}

// getCDNURL 获取CDN URL - 测试CDN可用性
func (p *ProxmoxProvider) getCDNURL(originalURL string) string {
	cdnEndpoints := utils.GetCDNEndpoints()

	// 使用已知存在的测试文件来检测CDN可用性
	testURL := "https://raw.githubusercontent.com/spiritLHLS/ecs/main/back/test"

	// 测试每个CDN端点，找到第一个可用的就使用
	for _, endpoint := range cdnEndpoints {
		cdnTestURL := endpoint + testURL
		// 测试CDN可用性 - 检查是否包含 "success" 字符串
		testCmd := fmt.Sprintf("curl -sL -k --max-time 6 '%s' 2>/dev/null | grep -q 'success' && echo 'ok' || echo 'failed'", cdnTestURL)
		result, err := p.sshClient.Execute(testCmd)
		if err == nil && strings.TrimSpace(result) == "ok" {
			cdnURL := endpoint + originalURL
			global.APP_LOG.Info("找到可用CDN，使用CDN下载Proxmox镜像",
				zap.String("originalURL", utils.TruncateString(originalURL, 100)),
				zap.String("cdnURL", utils.TruncateString(cdnURL, 100)),
				zap.String("cdnEndpoint", endpoint))
			return cdnURL
		}
		// 短暂延迟避免过于频繁的请求
		p.sshClient.Execute("sleep 0.5")
	}

	global.APP_LOG.Info("未找到可用CDN，使用原始URL",
		zap.String("originalURL", utils.TruncateString(originalURL, 100)))
	return ""
}

// handleImageDownloadAndImport 处理镜像下载和导入的通用逻辑
func (p *ProxmoxProvider) handleImageDownloadAndImport(ctx context.Context, config *provider.InstanceConfig) error {
	// 为镜像名称添加前缀
	originalImageName := config.Image
	imageNameWithPrefix := "oneclickvirt_" + config.Image

	// 根据实例类型确定镜像类型和存储路径
	var imageTypeStr string
	var targetDir string
	if config.InstanceType == "container" {
		imageTypeStr = "容器"
		targetDir = "/var/lib/vz/template/cache" // LXC容器模板路径
	} else {
		imageTypeStr = "虚拟机"
		targetDir = "/var/lib/vz/template/iso" // ISO镜像路径
	}

	// 如果有镜像URL，先下载镜像到指定路径
	if config.ImageURL != "" {
		global.APP_LOG.Info("ProxmoxVE"+imageTypeStr+"镜像将通过sshPullImage直接下载到远程服务器",
			zap.String("imageURL", config.ImageURL),
			zap.String("type", config.InstanceType),
			zap.String("targetDir", targetDir))

		// 设置镜像路径为目标位置
		imageName := filepath.Base(config.ImageURL)
		config.ImagePath = fmt.Sprintf("%s/%s", targetDir, imageName)

		global.APP_LOG.Info("ProxmoxVE"+imageTypeStr+"镜像路径设置",
			zap.String("imagePath", config.ImagePath),
			zap.String("type", config.InstanceType))

		// 实际下载镜像到指定路径
		if _, err := p.sshPullImageToPath(ctx, config.ImageURL, config.ImagePath); err != nil {
			return fmt.Errorf("下载%s镜像失败: %w", imageTypeStr, err)
		}

		// 生成基于URL、架构和实例类型的唯一别名，避免重复
		config.Image = imageNameWithPrefix + "_" + config.InstanceType + "_" + p.generateImageAlias(config.ImageURL, originalImageName, p.config.Architecture)[len(originalImageName)+1:]
	} else {
		config.Image = imageNameWithPrefix + "_" + config.InstanceType
	}

	// 如果有镜像文件路径，确保文件存在即可（ProxmoxVE暂时只需要保证文件存在）
	if config.ImagePath != "" {
		global.APP_LOG.Info("ProxmoxVE"+imageTypeStr+"镜像文件已准备",
			zap.String("imagePath", config.ImagePath),
			zap.String("imageName", config.Image),
			zap.String("type", config.InstanceType))

		// 检查文件是否存在
		if _, err := os.Stat(config.ImagePath); err != nil {
			return fmt.Errorf("ProxmoxVE%s镜像文件不存在: %w", imageTypeStr, err)
		}

		global.APP_LOG.Info("ProxmoxVE"+imageTypeStr+"镜像文件验证成功",
			zap.String("imagePath", config.ImagePath),
			zap.String("imageName", config.Image),
			zap.String("type", config.InstanceType))
	}

	return nil
}

// generateImageAlias 生成基于URL、镜像名和架构的唯一别名
func (p *ProxmoxProvider) generateImageAlias(imageURL, imageName, architecture string) string {
	// 使用URL和架构的哈希值来生成唯一标识
	hashInput := fmt.Sprintf("%s_%s", imageURL, architecture)
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(hashInput)))
	// 取前8位哈希值，组合镜像名和架构
	return fmt.Sprintf("%s-%s-%s", imageName, architecture, hash[:8])
}

// convertMemoryFormat 转换内存格式为Proxmox VE支持的格式
// Proxmox VE pct/qm create 命令要求 memory 参数为纯数字（以MB为单位）
func convertMemoryFormat(memory string) string {
	if memory == "" {
		return ""
	}

	// 如果已经是纯数字，直接返回
	if isNumeric(memory) {
		return memory
	}

	// 处理MB格式：512m, 512M, 512MB -> 512
	if strings.HasSuffix(memory, "MB") {
		return strings.TrimSuffix(memory, "MB")
	} else if strings.HasSuffix(memory, "m") || strings.HasSuffix(memory, "M") {
		return memory[:len(memory)-1]
	}

	// 处理GB格式：1g, 1G, 1GB -> 1024
	if strings.HasSuffix(memory, "GB") {
		numStr := strings.TrimSuffix(memory, "GB")
		if num, err := strconv.Atoi(numStr); err == nil {
			return strconv.Itoa(num * 1024)
		}
	} else if strings.HasSuffix(memory, "g") || strings.HasSuffix(memory, "G") {
		numStr := memory[:len(memory)-1]
		if num, err := strconv.Atoi(numStr); err == nil {
			return strconv.Itoa(num * 1024)
		}
	}

	// 默认返回原值
	return memory
}

// convertDiskFormat 转换磁盘格式为Proxmox VE支持的格式
// Proxmox VE rootfs 参数要求格式如: storage:10 (数字表示GB)
func convertDiskFormat(disk string) string {
	if disk == "" {
		return ""
	}

	// 如果已经是纯数字，假设是GB，直接返回
	if isNumeric(disk) {
		return disk
	}

	// 处理MB格式：转换为GB
	if strings.HasSuffix(disk, "MB") {
		numStr := strings.TrimSuffix(disk, "MB")
		if num, err := strconv.Atoi(numStr); err == nil {
			// 转换MB到GB（向上取整）
			gb := (num + 1023) / 1024 // 向上取整
			if gb < 1 {
				gb = 1 // 最小1GB
			}
			return strconv.Itoa(gb)
		}
	} else if strings.HasSuffix(disk, "m") {
		numStr := strings.TrimSuffix(disk, "m")
		if num, err := strconv.Atoi(numStr); err == nil {
			// 转换MB到GB（向上取整）
			gb := (num + 1023) / 1024 // 向上取整
			if gb < 1 {
				gb = 1 // 最小1GB
			}
			return strconv.Itoa(gb)
		}
	} else if strings.HasSuffix(disk, "M") {
		numStr := strings.TrimSuffix(disk, "M")
		if num, err := strconv.Atoi(numStr); err == nil {
			// 转换MB到GB（向上取整）
			gb := (num + 1023) / 1024 // 向上取整
			if gb < 1 {
				gb = 1 // 最小1GB
			}
			return strconv.Itoa(gb)
		}
	}

	// 处理GB格式：去掉单位，只保留数字
	if strings.HasSuffix(disk, "GB") {
		numStr := strings.TrimSuffix(disk, "GB")
		if isNumeric(numStr) {
			return numStr
		}
	} else if strings.HasSuffix(disk, "G") {
		numStr := strings.TrimSuffix(disk, "G")
		if isNumeric(numStr) {
			return numStr
		}
	} else if strings.HasSuffix(disk, "g") {
		numStr := strings.TrimSuffix(disk, "g")
		if isNumeric(numStr) {
			return numStr
		}
	}

	// 如果无法解析，默认返回 "1" (1GB)
	return "1"
}

// convertCPUFormat 转换CPU格式为Proxmox VE支持的格式
// Proxmox VE cores 参数要求为纯数字或小数
func convertCPUFormat(cpu string) string {
	if cpu == "" {
		return ""
	}

	// 检查是否已经是数字格式（包括小数）
	if isNumeric(cpu) || isFloat(cpu) {
		return cpu
	}

	// 处理可能的后缀（虽然CPU通常不会有后缀，但为了一致性）
	if strings.HasSuffix(cpu, "cores") || strings.HasSuffix(cpu, "core") {
		return strings.TrimSuffix(strings.TrimSuffix(cpu, "cores"), "core")
	}

	// 默认返回原值
	return cpu
}

// isNumeric 检查字符串是否为纯数字
func isNumeric(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}

// isFloat 检查字符串是否为浮点数
func isFloat(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

// checkVMCTStatus 检查VM/CT状态
func (p *ProxmoxProvider) checkVMCTStatus(ctx context.Context, id string, instanceType string) error {
	maxAttempts := 5
	for i := 1; i <= maxAttempts; i++ {
		var cmd string
		if instanceType == "vm" {
			cmd = fmt.Sprintf("qm status %s 2>/dev/null | grep -w 'status:' | awk '{print $2}'", id)
		} else if instanceType == "container" {
			cmd = fmt.Sprintf("pct status %s 2>/dev/null | grep -w 'status:' | awk '{print $2}'", id)
		} else {
			return fmt.Errorf("unknown instance type: %s", instanceType)
		}

		output, err := p.sshClient.Execute(cmd)
		if err == nil && strings.TrimSpace(output) == "stopped" {
			return nil
		}

		global.APP_LOG.Debug("等待实例停止",
			zap.String("id", id),
			zap.String("type", instanceType),
			zap.Int("attempt", i),
			zap.String("status", strings.TrimSpace(output)))

		// 等待1秒后重试
		_, _ = p.sshClient.Execute("sleep 1")
	}
	return fmt.Errorf("实例 %s 未能在预期时间内停止", id)
}

// safeRemove 安全删除文件/路径
func (p *ProxmoxProvider) safeRemove(ctx context.Context, path string) error {
	if path == "" {
		return nil
	}

	// 检查路径是否存在
	checkCmd := fmt.Sprintf("[ -e '%s' ]", path)
	_, err := p.sshClient.Execute(checkCmd)
	if err != nil {
		// 路径不存在，无需删除
		return nil
	}

	global.APP_LOG.Info("删除路径", zap.String("path", path))
	removeCmd := fmt.Sprintf("rm -rf '%s'", path)
	_, err = p.sshClient.Execute(removeCmd)
	return err
}

// cleanupIPv6NATRules 清理IPv6 NAT映射规则
func (p *ProxmoxProvider) cleanupIPv6NATRules(ctx context.Context, vmctid string) error {
	appendedFile := "/usr/local/bin/pve_appended_content.txt"
	rulesFile := "/usr/local/bin/ipv6_nat_rules.sh"
	usedIPsFile := "/usr/local/bin/pve_used_vmbr1_ips.txt"

	// 检查appended_file是否存在且非空
	checkCmd := fmt.Sprintf("[ -s '%s' ]", appendedFile)
	_, err := p.sshClient.Execute(checkCmd)
	if err != nil {
		// 文件不存在或为空，跳过IPv6清理
		return nil
	}

	global.APP_LOG.Info("清理IPv6 NAT规则", zap.String("vmctid", vmctid))
	vmInternalIPv6 := fmt.Sprintf("2001:db8:1::%s", vmctid)

	// 查找外部IPv6地址
	if _, err := p.sshClient.Execute(fmt.Sprintf("[ -f '%s' ]", rulesFile)); err == nil {
		// 获取外部IPv6地址
		getExternalIPCmd := fmt.Sprintf("grep -oP 'DNAT --to-destination %s' '%s' | head -1 | grep -oP '(?<=-d )[^ ]+' || true", vmInternalIPv6, rulesFile)
		hostExternalIPv6, _ := p.sshClient.Execute(getExternalIPCmd)
		hostExternalIPv6 = strings.TrimSpace(hostExternalIPv6)

		if hostExternalIPv6 != "" {
			global.APP_LOG.Info("删除IPv6 NAT规则",
				zap.String("internal", vmInternalIPv6),
				zap.String("external", hostExternalIPv6))

			// 删除ip6tables规则
			_, _ = p.sshClient.Execute(fmt.Sprintf("ip6tables -t nat -D PREROUTING -d '%s' -j DNAT --to-destination '%s' 2>/dev/null || true", hostExternalIPv6, vmInternalIPv6))
			_, _ = p.sshClient.Execute(fmt.Sprintf("ip6tables -t nat -D POSTROUTING -s '%s' -j SNAT --to-source '%s' 2>/dev/null || true", vmInternalIPv6, hostExternalIPv6))

			// 从规则文件中删除相关行
			_, _ = p.sshClient.Execute(fmt.Sprintf("sed -i '/DNAT --to-destination %s/d' '%s' 2>/dev/null || true", vmInternalIPv6, rulesFile))
			_, _ = p.sshClient.Execute(fmt.Sprintf("sed -i '/SNAT --to-source %s/d' '%s' 2>/dev/null || true", hostExternalIPv6, rulesFile))

			// 从已使用IP文件中删除
			if _, err := p.sshClient.Execute(fmt.Sprintf("[ -f '%s' ]", usedIPsFile)); err == nil {
				_, _ = p.sshClient.Execute(fmt.Sprintf("sed -i '/^%s$/d' '%s' 2>/dev/null || true", hostExternalIPv6, usedIPsFile))
				global.APP_LOG.Info("释放IPv6地址", zap.String("ipv6", hostExternalIPv6))
			}

			// 重启服务
			_, _ = p.sshClient.Execute("systemctl daemon-reload")
			_, _ = p.sshClient.Execute("systemctl restart ipv6nat.service")
		}
	}

	return nil
}

// cleanupVMFiles 清理VM相关文件
func (p *ProxmoxProvider) cleanupVMFiles(ctx context.Context, vmid string) error {
	global.APP_LOG.Info("清理VM文件", zap.String("vmid", vmid))

	// 获取所有存储名称并清理相关卷
	storageListCmd := "pvesm status | awk 'NR > 1 {print $1}'"
	storageOutput, err := p.sshClient.Execute(storageListCmd)
	if err != nil {
		return fmt.Errorf("获取存储列表失败: %w", err)
	}

	storages := strings.Split(strings.TrimSpace(storageOutput), "\n")
	for _, storage := range storages {
		storage = strings.TrimSpace(storage)
		if storage == "" {
			continue
		}

		// 列出存储中与该VM相关的卷
		listVolCmd := fmt.Sprintf("pvesm list '%s' | awk -v vmid='%s' '$5 == vmid {print $1}'", storage, vmid)
		volOutput, err := p.sshClient.Execute(listVolCmd)
		if err != nil {
			global.APP_LOG.Warn("列出存储卷失败", zap.String("storage", storage), zap.Error(err))
			continue
		}

		vols := strings.Split(strings.TrimSpace(volOutput), "\n")
		for _, volid := range vols {
			volid = strings.TrimSpace(volid)
			if volid == "" {
				continue
			}

			// 获取卷路径并删除
			pathCmd := fmt.Sprintf("pvesm path '%s' 2>/dev/null || true", volid)
			volPath, _ := p.sshClient.Execute(pathCmd)
			volPath = strings.TrimSpace(volPath)

			if volPath != "" {
				if err := p.safeRemove(ctx, volPath); err != nil {
					global.APP_LOG.Warn("删除卷路径失败",
						zap.String("volid", volid),
						zap.String("path", volPath),
						zap.Error(err))
				}
			} else {
				global.APP_LOG.Warn("无法解析卷路径",
					zap.String("volid", volid),
					zap.String("storage", storage))
			}
		}
	}

	// 删除VM目录
	vmDir := fmt.Sprintf("/root/vm%s", vmid)
	return p.safeRemove(ctx, vmDir)
}

// cleanupCTFiles 清理CT相关文件
func (p *ProxmoxProvider) cleanupCTFiles(ctx context.Context, ctid string) error {
	global.APP_LOG.Info("清理CT文件", zap.String("ctid", ctid))

	// 获取所有存储名称并清理相关卷
	storageListCmd := "pvesm status | awk 'NR > 1 {print $1}'"
	storageOutput, err := p.sshClient.Execute(storageListCmd)
	if err != nil {
		return fmt.Errorf("获取存储列表失败: %w", err)
	}

	storages := strings.Split(strings.TrimSpace(storageOutput), "\n")
	for _, storage := range storages {
		storage = strings.TrimSpace(storage)
		if storage == "" {
			continue
		}

		// 列出存储中与该CT相关的卷
		listVolCmd := fmt.Sprintf("pvesm list '%s' | awk -v ctid='%s' '$5 == ctid {print $1}'", storage, ctid)
		volOutput, err := p.sshClient.Execute(listVolCmd)
		if err != nil {
			global.APP_LOG.Warn("列出存储卷失败", zap.String("storage", storage), zap.Error(err))
			continue
		}

		vols := strings.Split(strings.TrimSpace(volOutput), "\n")
		for _, volid := range vols {
			volid = strings.TrimSpace(volid)
			if volid == "" {
				continue
			}

			// 获取卷路径并删除
			pathCmd := fmt.Sprintf("pvesm path '%s' 2>/dev/null || true", volid)
			volPath, _ := p.sshClient.Execute(pathCmd)
			volPath = strings.TrimSpace(volPath)

			if volPath != "" {
				if err := p.safeRemove(ctx, volPath); err != nil {
					global.APP_LOG.Warn("删除卷路径失败",
						zap.String("volid", volid),
						zap.String("path", volPath),
						zap.Error(err))
				}
			} else {
				global.APP_LOG.Warn("无法解析卷路径",
					zap.String("volid", volid),
					zap.String("storage", storage))
			}
		}
	}

	// 删除CT目录
	ctDir := fmt.Sprintf("/root/ct%s", ctid)
	return p.safeRemove(ctx, ctDir)
}

// updateIPTablesRules 更新iptables规则
func (p *ProxmoxProvider) updateIPTablesRules(ctx context.Context, ipAddress string) error {
	if ipAddress == "" {
		return nil
	}

	rulesFile := "/etc/iptables/rules.v4"

	// 检查rules文件是否存在
	if _, err := p.sshClient.Execute(fmt.Sprintf("[ -f '%s' ]", rulesFile)); err != nil {
		global.APP_LOG.Warn("iptables规则文件不存在", zap.String("file", rulesFile))
		return nil
	}

	global.APP_LOG.Info("删除iptables规则", zap.String("ip", ipAddress))

	// 从rules文件中删除包含该IP的规则
	removeCmd := fmt.Sprintf("sed -i '/%s:/d' '%s'", ipAddress, rulesFile)
	_, err := p.sshClient.Execute(removeCmd)
	return err
}

// rebuildIPTablesRules 重建iptables规则
func (p *ProxmoxProvider) rebuildIPTablesRules(ctx context.Context) error {
	rulesFile := "/etc/iptables/rules.v4"

	// 检查rules文件是否存在
	if _, err := p.sshClient.Execute(fmt.Sprintf("[ -f '%s' ]", rulesFile)); err != nil {
		global.APP_LOG.Warn("iptables规则文件不存在，跳过重建", zap.String("file", rulesFile))
		return nil
	}

	global.APP_LOG.Info("重建iptables规则")

	// 应用规则文件
	restoreCmd := fmt.Sprintf("cat '%s' | iptables-restore", rulesFile)
	_, err := p.sshClient.Execute(restoreCmd)
	return err
}

// restartNDPResponder 重启ndpresponder服务
func (p *ProxmoxProvider) restartNDPResponder(ctx context.Context) error {
	ndpBinary := "/usr/local/bin/ndpresponder"

	// 检查ndpresponder是否存在
	if _, err := p.sshClient.Execute(fmt.Sprintf("[ -f '%s' ]", ndpBinary)); err != nil {
		// ndpresponder不存在，跳过重启
		return nil
	}

	global.APP_LOG.Info("重启ndpresponder服务")
	_, err := p.sshClient.Execute("systemctl restart ndpresponder.service")
	return err
}

// cleanupVnStatMonitoring 清理实例的vnstat监控（通过instanceID）
func (p *ProxmoxProvider) cleanupVnStatMonitoring(ctx context.Context, vmid string) error {
	// 创建vnstat服务实例
	vnstatService := vnstat.NewService()

	// 尝试通过VMID查找对应的实例记录
	var instance providerModel.Instance
	var instanceID uint

	// 方法1: 通过实例名称匹配（如果VMID就是实例名称）
	err := global.APP_DB.Where("name = ?", vmid).First(&instance).Error
	if err == nil {
		instanceID = instance.ID
	} else {
		// 方法2: 查找所有实例，通过VMID字段匹配（如果有的话）
		var instances []providerModel.Instance
		if err := global.APP_DB.Find(&instances).Error; err == nil {
			for _, inst := range instances {
				// 假设VMID存储在某个字段中，或者可以从实例配置中解析
				// 这里先简单地通过名称匹配
				if inst.Name == vmid {
					instanceID = inst.ID
					break
				}
			}
		}
	}

	if instanceID > 0 {
		global.APP_LOG.Info("找到实例记录，开始清理vnstat监控",
			zap.String("vmid", vmid),
			zap.Uint("instance_id", instanceID))

		// 使用现有的CleanupVnStatData方法进行清理
		if err := vnstatService.CleanupVnStatData(instanceID); err != nil {
			global.APP_LOG.Error("通过vnstat服务清理数据失败",
				zap.String("vmid", vmid),
				zap.Uint("instance_id", instanceID),
				zap.Error(err))
			return err
		}

		global.APP_LOG.Info("vnstat监控清理完成",
			zap.String("vmid", vmid),
			zap.Uint("instance_id", instanceID))
	} else {
		global.APP_LOG.Warn("未找到对应的实例记录，跳过vnstat清理",
			zap.String("vmid", vmid))
	}

	return nil
}
