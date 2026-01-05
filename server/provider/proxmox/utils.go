package proxmox

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	"oneclickvirt/service/pmacct"
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
	if cdnURL := utils.GetCDNURL(p.sshClient, originalURL, "Proxmox"); cdnURL != "" {
		return cdnURL
	}
	return originalURL
}

// convertMemoryFormat 转换内存格式为Proxmox VE支持的格式
// Proxmox VE pct/qm create 命令要求 memory 参数为纯数字（以MB为单位）
func convertMemoryFormat(memory string) string {
	if memory == "" {
		return ""
	}

	// 如果已经是纯数字，直接返回
	if utils.IsNumeric(memory) {
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
	if utils.IsNumeric(disk) {
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
		if utils.IsNumeric(numStr) {
			return numStr
		}
	} else if strings.HasSuffix(disk, "G") {
		numStr := strings.TrimSuffix(disk, "G")
		if utils.IsNumeric(numStr) {
			return numStr
		}
	} else if strings.HasSuffix(disk, "g") {
		numStr := strings.TrimSuffix(disk, "g")
		if utils.IsNumeric(numStr) {
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
	if utils.IsNumeric(cpu) || utils.IsFloat(cpu) {
		return cpu
	}

	// 处理可能的后缀（虽然CPU通常不会有后缀，但为了一致性）
	if strings.HasSuffix(cpu, "cores") || strings.HasSuffix(cpu, "core") {
		return strings.TrimSuffix(strings.TrimSuffix(cpu, "cores"), "core")
	}

	// 默认返回原值
	return cpu
}

// checkVMCTStatus 检查VM/CT状态
func (p *ProxmoxProvider) checkVMCTStatus(ctx context.Context, id string, instanceType string) error {
	maxAttempts := 5
	for i := 1; i <= maxAttempts; i++ {
		var cmd string
		switch instanceType {
		case "vm":
			cmd = fmt.Sprintf("qm status %s 2>/dev/null | grep -w 'status:' | awk '{print $2}'", id)
		case "container":
			cmd = fmt.Sprintf("pct status %s 2>/dev/null | grep -w 'status:' | awk '{print $2}'", id)
		default:
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

// cleanupPmacctMonitoring 清理实例的pmacct监控（通过instanceID）
func (p *ProxmoxProvider) cleanupPmacctMonitoring(ctx context.Context, vmid string) error {
	// 创建pmacct服务实例
	pmacctService := pmacct.NewService()

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
		global.APP_LOG.Info("找到实例记录，开始清理pmacct监控",
			zap.String("vmid", vmid),
			zap.Uint("instance_id", instanceID))

		// 使用现有的CleanupPmacctData方法进行清理
		if err := pmacctService.CleanupPmacctData(instanceID); err != nil {
			global.APP_LOG.Error("通过pmacct服务清理数据失败",
				zap.String("vmid", vmid),
				zap.Uint("instance_id", instanceID),
				zap.Error(err))
			return err
		}

		global.APP_LOG.Info("pmacct监控清理完成",
			zap.String("vmid", vmid),
			zap.Uint("instance_id", instanceID))
	} else {
		global.APP_LOG.Warn("未找到对应的实例记录，跳过pmacct清理",
			zap.String("vmid", vmid))
	}

	return nil
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
		"2001:db8:",    // 文档用途（只有2001:db8:才是私有的）
		"::1",          // 回环地址
		"::ffff:",      // IPv4映射地址
		"fd42:",        // Docker等使用的私有地址
		"2001:db8:1::", // 在NAT映射中使用的内部地址
	}

	for _, prefix := range privateRanges {
		if strings.HasPrefix(address, prefix) {
			return true
		}
	}
	return false
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

// detectContainerPackageManager 检测容器包管理器类型
func (p *ProxmoxProvider) detectContainerPackageManager(vmid int) string {
	// 定义包管理器检测命令列表
	packageManagers := []struct {
		name    string
		command string
	}{
		{"apk", "command -v apk"},
		{"opkg", "command -v opkg"},
		{"pacman", "command -v pacman"},
		{"apt-get", "command -v apt-get"},
		{"apt", "command -v apt"},
		{"dnf", "command -v dnf"},
		{"yum", "command -v yum"},
		{"zypper", "command -v zypper"},
	}

	// 尝试检测每个包管理器
	for _, pm := range packageManagers {
		checkCmd := fmt.Sprintf("pct exec %d -- sh -c \"%s >/dev/null 2>&1 && echo 'found'\"", vmid, pm.command)
		output, err := p.sshClient.Execute(checkCmd)
		if err == nil && strings.TrimSpace(output) == "found" {
			global.APP_LOG.Info("检测到包管理器", zap.Int("vmid", vmid), zap.String("packageManager", pm.name))
			return pm.name
		}
	}

	// 如果没有检测到任何包管理器，返回unknown
	global.APP_LOG.Warn("未检测到任何已知的包管理器", zap.Int("vmid", vmid))
	return "unknown"
}

// configureContainerDNS 配置容器DNS
func (p *ProxmoxProvider) configureContainerDNS(vmid int) {
	dnsCommands := []string{
		"sh -c \"if [ -f /etc/resolv.conf ]; then cp /etc/resolv.conf /etc/resolv.conf.bak; fi\"",
		"sh -c \"echo 'nameserver 8.8.8.8' | tee -a /etc/resolv.conf >/dev/null\"",
		"sh -c \"echo 'nameserver 8.8.4.4' | tee -a /etc/resolv.conf >/dev/null\"",
	}

	for _, cmd := range dnsCommands {
		fullCmd := fmt.Sprintf("pct exec %d -- %s", vmid, cmd)
		_, err := p.sshClient.Execute(fullCmd)
		if err != nil {
			global.APP_LOG.Warn("配置DNS失败", zap.Int("vmid", vmid), zap.Error(err))
		}
	}
}

// configureAlpineSSH 配置Alpine容器SSH
func (p *ProxmoxProvider) configureAlpineSSH(vmid int) {
	commands := []string{
		// 更新包管理器
		"apk update",
		// 安装必要软件
		"apk add --no-cache openssh-server",
		"apk add --no-cache sshpass",
		"apk add --no-cache openssh-keygen",
		"apk add --no-cache bash",
		"apk add --no-cache curl",
		"apk add --no-cache wget",
		// 生成SSH密钥
		"sh -c \"cd /etc/ssh && ssh-keygen -A\"",
		// 配置sshd_config - 使用chattr解锁
		"sh -c \"chattr -i /etc/ssh/sshd_config 2>/dev/null || true\"",
		"sed -i '/^#PermitRootLogin\\|PermitRootLogin/c PermitRootLogin yes' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?PasswordAuthentication.*/PasswordAuthentication yes/g' /etc/ssh/sshd_config",
		"sed -i 's/#ListenAddress 0.0.0.0/ListenAddress 0.0.0.0/' /etc/ssh/sshd_config",
		"sed -i 's/#ListenAddress ::/ListenAddress ::/' /etc/ssh/sshd_config",
		"sed -i '/^#AddressFamily\\|AddressFamily/c AddressFamily any' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?\\(Port\\).*/\\1 22/' /etc/ssh/sshd_config",
		"sed -i '/^#UsePAM\\|UsePAM/c #UsePAM no' /etc/ssh/sshd_config",
		// 配置cloud-init
		"sed -E -i 's/preserve_hostname:[[:space:]]*false/preserve_hostname: true/g' /etc/cloud/cloud.cfg 2>/dev/null || true",
		"sed -E -i 's/disable_root:[[:space:]]*true/disable_root: false/g' /etc/cloud/cloud.cfg 2>/dev/null || true",
		"sed -E -i 's/ssh_pwauth:[[:space:]]*false/ssh_pwauth:   true/g' /etc/cloud/cloud.cfg 2>/dev/null || true",
		// 启动SSH服务
		"/usr/sbin/sshd",
		"rc-update add sshd default",
		// 锁定配置文件
		"sh -c \"chattr +i /etc/ssh/sshd_config 2>/dev/null || true\"",
	}

	p.executeContainerCommands(vmid, commands, "Alpine")
}

// configureOpenWrtSSH 配置OpenWrt容器SSH
func (p *ProxmoxProvider) configureOpenWrtSSH(vmid int) {
	commands := []string{
		// 更新包管理器
		"opkg update",
		// 安装必要软件
		"opkg install openssh-server",
		"opkg install bash",
		"opkg install openssh-keygen",
		"opkg install shadow-chpasswd",
		"opkg install chattr",
		// 生成SSH密钥
		"sh -c \"cd /etc/ssh && ssh-keygen -A\"",
		// 配置sshd_config
		"sh -c \"chattr -i /etc/ssh/sshd_config 2>/dev/null || true\"",
		"sed -i 's/^#\\?Port.*/Port 22/g' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?PermitRootLogin.*/PermitRootLogin yes/g' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?PasswordAuthentication.*/PasswordAuthentication yes/g' /etc/ssh/sshd_config",
		"sed -i 's/#ListenAddress 0.0.0.0/ListenAddress 0.0.0.0/' /etc/ssh/sshd_config",
		"sed -i 's/#ListenAddress ::/ListenAddress ::/' /etc/ssh/sshd_config",
		"sed -i 's/#AddressFamily any/AddressFamily any/' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?PubkeyAuthentication.*/PubkeyAuthentication no/g' /etc/ssh/sshd_config",
		"sed -i '/^AuthorizedKeysFile/s/^/#/' /etc/ssh/sshd_config",
		// 锁定配置文件
		"sh -c \"chattr +i /etc/ssh/sshd_config 2>/dev/null || true\"",
		// 启动SSH服务
		"/etc/init.d/sshd enable",
		"/etc/init.d/sshd start",
	}

	p.executeContainerCommands(vmid, commands, "OpenWrt")
}

// configureArchSSH 配置Arch容器SSH
func (p *ProxmoxProvider) configureArchSSH(vmid int) {
	commands := []string{
		// 初始化GPG密钥
		"sh -c \"rm -rf /etc/pacman.d/gnupg/\"",
		"pacman-key --init",
		"pacman-key --populate archlinux",
		// 更新系统
		"pacman -Syyuu --noconfirm",
		// 安装必要软件
		"pacman -Sy --needed --noconfirm openssh",
		"pacman -Sy --needed --noconfirm bash",
		// 配置sshd_config
		"sh -c \"chattr -i /etc/ssh/sshd_config 2>/dev/null || true\"",
		"sed -i 's/^#\\?Port.*/Port 22/g' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?PermitRootLogin.*/PermitRootLogin yes/g' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?PasswordAuthentication.*/PasswordAuthentication yes/g' /etc/ssh/sshd_config",
		"sed -i 's/#ListenAddress 0.0.0.0/ListenAddress 0.0.0.0/' /etc/ssh/sshd_config",
		"sed -i 's/#ListenAddress ::/ListenAddress ::/' /etc/ssh/sshd_config",
		"sed -i 's/#AddressFamily any/AddressFamily any/' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?PubkeyAuthentication.*/PubkeyAuthentication no/g' /etc/ssh/sshd_config",
		"sed -i '/^AuthorizedKeysFile/s/^/#/' /etc/ssh/sshd_config",
		// 锁定配置文件
		"sh -c \"chattr +i /etc/ssh/sshd_config 2>/dev/null || true\"",
		// 启动SSH服务
		"systemctl enable sshd",
		"systemctl start sshd",
	}

	p.executeContainerCommands(vmid, commands, "Arch")
}

// configureDebianBasedSSH 配置Debian/Ubuntu等基于APT的系统SSH
func (p *ProxmoxProvider) configureDebianBasedSSH(vmid int) {
	commands := []string{
		// 检查并修复APT
		"sh -c \"apt-get update 2>&1 | tee /tmp/apt_fix.txt\"",
		"sh -c \"if grep -q 'NO_PUBKEY' /tmp/apt_fix.txt; then public_keys=$(grep -oE 'NO_PUBKEY [0-9A-F]+' /tmp/apt_fix.txt | awk '{ print $2 }' | paste -sd ' '); apt-key adv --keyserver keyserver.ubuntu.com --recv-keys $public_keys; apt-get update; fi\"",
		// 修复损坏的包
		"apt-get --fix-broken install -y",
		// 更新包列表
		"apt-get update -y",
		// 安装必要软件
		"apt-get install -y openssh-server sshpass curl",
		// 生成SSH密钥
		"ssh-keygen -A",
		// 配置sshd_config
		"sh -c \"chattr -i /etc/ssh/sshd_config 2>/dev/null || true\"",
		"sed -i 's/^#\\?Port.*/Port 22/g' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?PermitRootLogin.*/PermitRootLogin yes/g' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?PasswordAuthentication.*/PasswordAuthentication yes/g' /etc/ssh/sshd_config",
		"sed -i 's/#ListenAddress 0.0.0.0/ListenAddress 0.0.0.0/' /etc/ssh/sshd_config",
		"sed -i 's/#ListenAddress ::/ListenAddress ::/' /etc/ssh/sshd_config",
		"sed -i 's/#AddressFamily any/AddressFamily any/' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?PubkeyAuthentication.*/PubkeyAuthentication no/g' /etc/ssh/sshd_config",
		"sed -i '/^#UsePAM\\|UsePAM/c #UsePAM no' /etc/ssh/sshd_config",
		"sed -i '/^AuthorizedKeysFile/s/^/#/' /etc/ssh/sshd_config",
		"sed -i 's/^#[[:space:]]*KbdInteractiveAuthentication.*\\|^KbdInteractiveAuthentication.*/KbdInteractiveAuthentication yes/' /etc/ssh/sshd_config",
		// 处理sshd_config.d目录中的配置文件
		"sh -c \"if [ -d /etc/ssh/sshd_config.d ]; then for file in /etc/ssh/sshd_config.d/*; do if [ -f \\\"$file\\\" ] && grep -q 'PasswordAuthentication no' \\\"$file\\\"; then sed -i 's/PasswordAuthentication no/PasswordAuthentication yes/g' \\\"$file\\\"; fi; done; fi\"",
		// 锁定配置文件
		"sh -c \"chattr +i /etc/ssh/sshd_config 2>/dev/null || true\"",
		// 启动SSH服务
		"systemctl enable ssh 2>/dev/null || systemctl enable sshd 2>/dev/null || true",
		"systemctl start ssh 2>/dev/null || systemctl start sshd 2>/dev/null || service ssh start || service sshd start",
		// 配置IPv6优先级
		"sed -i 's/.*precedence ::ffff:0:0\\/96.*/precedence ::ffff:0:0\\/96  100/g' /etc/gai.conf",
		// 设置motd
		"sh -c \"if [ -f /etc/motd ]; then echo '' > /etc/motd; echo 'Related repo https://github.com/oneclickvirt/pve' >> /etc/motd; echo '--by https://t.me/spiritlhl' >> /etc/motd; fi\"",
	}

	p.executeContainerCommands(vmid, commands, "Debian-based")
}

// configureRHELBasedSSH 配置RHEL/CentOS/Fedora等基于YUM/DNF的系统SSH
func (p *ProxmoxProvider) configureRHELBasedSSH(vmid int) {
	// 检测使用yum还是dnf
	checkDnfCmd := fmt.Sprintf("pct exec %d -- sh -c \"command -v dnf >/dev/null 2>&1 && echo 'dnf' || echo 'yum'\"", vmid)
	output, _ := p.sshClient.Execute(checkDnfCmd)
	pkgCmd := strings.TrimSpace(output)
	if pkgCmd == "" {
		pkgCmd = "yum" // 默认使用yum
	}

	commands := []string{
		// 更新包管理器
		fmt.Sprintf("%s -y update", pkgCmd),
		// 安装必要软件
		fmt.Sprintf("%s -y install openssh-server curl", pkgCmd),
		// 生成SSH密钥
		"ssh-keygen -A",
		// 停止防火墙服务
		"service iptables stop 2>/dev/null || true",
		"chkconfig iptables off 2>/dev/null || true",
		// 禁用SELinux
		"sh -c \"if [ -f /etc/sysconfig/selinux ]; then sed -i.bak '/^SELINUX=/cSELINUX=disabled' /etc/sysconfig/selinux; fi\"",
		"sh -c \"if [ -f /etc/selinux/config ]; then sed -i.bak '/^SELINUX=/cSELINUX=disabled' /etc/selinux/config; fi\"",
		"setenforce 0 2>/dev/null || true",
		// 配置sshd_config
		"sh -c \"chattr -i /etc/ssh/sshd_config 2>/dev/null || true\"",
		"sed -i 's/^#\\?Port.*/Port 22/g' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?PermitRootLogin.*/PermitRootLogin yes/g' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?PasswordAuthentication.*/PasswordAuthentication yes/g' /etc/ssh/sshd_config",
		"sed -i 's/#ListenAddress 0.0.0.0/ListenAddress 0.0.0.0/' /etc/ssh/sshd_config",
		"sed -i 's/#ListenAddress ::/ListenAddress ::/' /etc/ssh/sshd_config",
		"sed -i 's/#AddressFamily any/AddressFamily any/' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?PubkeyAuthentication.*/PubkeyAuthentication no/g' /etc/ssh/sshd_config",
		"sed -i '/^#UsePAM\\|UsePAM/c #UsePAM no' /etc/ssh/sshd_config",
		"sed -i '/^AuthorizedKeysFile/s/^/#/' /etc/ssh/sshd_config",
		"sed -i 's/^#[[:space:]]*KbdInteractiveAuthentication.*\\|^KbdInteractiveAuthentication.*/KbdInteractiveAuthentication yes/' /etc/ssh/sshd_config",
		// 处理sshd_config.d目录中的配置文件
		"sh -c \"if [ -d /etc/ssh/sshd_config.d ]; then for file in /etc/ssh/sshd_config.d/*; do if [ -f \\\"$file\\\" ] && grep -q 'PasswordAuthentication no' \\\"$file\\\"; then sed -i 's/PasswordAuthentication no/PasswordAuthentication yes/g' \\\"$file\\\"; fi; done; fi\"",
		// 锁定配置文件
		"sh -c \"chattr +i /etc/ssh/sshd_config 2>/dev/null || true\"",
		// 启动SSH服务
		"systemctl enable sshd 2>/dev/null || service sshd enable 2>/dev/null || true",
		"systemctl start sshd 2>/dev/null || service sshd start",
		// 配置IPv6优先级
		"sed -i 's/.*precedence ::ffff:0:0\\/96.*/precedence ::ffff:0:0\\/96  100/g' /etc/gai.conf",
		// 设置motd
		"sh -c \"if [ -f /etc/motd ]; then echo '' > /etc/motd; echo 'Related repo https://github.com/oneclickvirt/pve' >> /etc/motd; echo '--by https://t.me/spiritlhl' >> /etc/motd; fi\"",
	}

	p.executeContainerCommands(vmid, commands, "RHEL-based")
}

// configureOpenSUSESSH 配置openSUSE系统SSH
func (p *ProxmoxProvider) configureOpenSUSESSH(vmid int) {
	commands := []string{
		// 更新包管理器
		"zypper update -y",
		// 安装必要软件
		"zypper install -y openssh-server curl",
		// 生成SSH密钥
		"ssh-keygen -A",
		// 配置sshd_config
		"sh -c \"chattr -i /etc/ssh/sshd_config 2>/dev/null || true\"",
		"sed -i 's/^#\\?Port.*/Port 22/g' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?PermitRootLogin.*/PermitRootLogin yes/g' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?PasswordAuthentication.*/PasswordAuthentication yes/g' /etc/ssh/sshd_config",
		"sed -i 's/#ListenAddress 0.0.0.0/ListenAddress 0.0.0.0/' /etc/ssh/sshd_config",
		"sed -i 's/#ListenAddress ::/ListenAddress ::/' /etc/ssh/sshd_config",
		"sed -i 's/#AddressFamily any/AddressFamily any/' /etc/ssh/sshd_config",
		"sed -i 's/^#\\?PubkeyAuthentication.*/PubkeyAuthentication no/g' /etc/ssh/sshd_config",
		"sed -i '/^#UsePAM\\|UsePAM/c #UsePAM no' /etc/ssh/sshd_config",
		"sed -i '/^AuthorizedKeysFile/s/^/#/' /etc/ssh/sshd_config",
		"sed -i 's/^#[[:space:]]*KbdInteractiveAuthentication.*\\|^KbdInteractiveAuthentication.*/KbdInteractiveAuthentication yes/' /etc/ssh/sshd_config",
		// 处理sshd_config.d目录中的配置文件
		"sh -c \"if [ -d /etc/ssh/sshd_config.d ]; then for file in /etc/ssh/sshd_config.d/*; do if [ -f \\\"$file\\\" ] && grep -q 'PasswordAuthentication no' \\\"$file\\\"; then sed -i 's/PasswordAuthentication no/PasswordAuthentication yes/g' \\\"$file\\\"; fi; done; fi\"",
		// 锁定配置文件
		"sh -c \"chattr +i /etc/ssh/sshd_config 2>/dev/null || true\"",
		// 启动SSH服务
		"systemctl enable sshd",
		"systemctl start sshd",
		// 配置IPv6优先级
		"sed -i 's/.*precedence ::ffff:0:0\\/96.*/precedence ::ffff:0:0\\/96  100/g' /etc/gai.conf",
		// 设置motd
		"sh -c \"if [ -f /etc/motd ]; then echo '' > /etc/motd; echo 'Related repo https://github.com/oneclickvirt/pve' >> /etc/motd; echo '--by https://t.me/spiritlhl' >> /etc/motd; fi\"",
	}

	p.executeContainerCommands(vmid, commands, "openSUSE")
}
