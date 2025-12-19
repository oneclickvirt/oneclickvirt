package proxmox

import (
	"context"
	"fmt"
	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	"oneclickvirt/utils"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

func (p *ProxmoxProvider) sshCreateInstance(ctx context.Context, config provider.InstanceConfig) error {
	return p.sshCreateInstanceWithProgress(ctx, config, nil)
}

func (p *ProxmoxProvider) sshCreateInstanceWithProgress(ctx context.Context, config provider.InstanceConfig, progressCallback provider.ProgressCallback) error {
	global.APP_LOG.Info("开始在Proxmox节点上创建实例（使用SSH）",
		zap.String("node", p.node),
		zap.String("host", utils.TruncateString(p.config.Host, 32)),
		zap.String("instance_name", config.Name),
		zap.String("instance_type", config.InstanceType))
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

	// 虚拟机和容器的带宽限制已在创建时通过 rate 参数配置

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

	// 初始化pmacct流量监控
	updateProgress(95, "初始化pmacct流量监控...")
	if err := p.initializePmacctMonitoring(ctx, vmid, config.Name); err != nil {
		global.APP_LOG.Warn("初始化流量监控失败",
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

	// 配置网络（使用VMID到IP的映射函数，充分利用IP地址空间）
	// 使用 Proxmox 原生的 rate 参数限制带宽
	networkConfig := p.parseNetworkConfigFromInstanceConfig(config)
	userIP := VMIDToInternalIP(vmid)
	netConfigStr := fmt.Sprintf("name=eth0,ip=%s/24,bridge=vmbr1,gw=%s", userIP, InternalGateway)

	// 先尝试带rate参数的配置
	if networkConfig.OutSpeed > 0 {
		// Proxmox rate 参数单位为 MB/s，配置中的 OutSpeed 单位为 Mbps，需要转换：MB/s = Mbps ÷ 8
		rateMBps := networkConfig.OutSpeed / 8
		if rateMBps < 1 {
			rateMBps = 1 // 最小1MB/s
		}
		netConfigStrWithRate := fmt.Sprintf("%s,rate=%d", netConfigStr, rateMBps)
		netCmd := fmt.Sprintf("pct set %d --net0 %s", vmid, netConfigStrWithRate)
		_, err = p.sshClient.Execute(netCmd)
		if err != nil {
			// 带rate参数失败，fallback到不带rate的配置
			global.APP_LOG.Warn("容器网络配置（带rate）失败，尝试不带rate的配置",
				zap.Int("vmid", vmid),
				zap.Int("rateMBps", rateMBps),
				zap.Error(err))

			netCmd = fmt.Sprintf("pct set %d --net0 %s", vmid, netConfigStr)
			_, err = p.sshClient.Execute(netCmd)
			if err != nil {
				global.APP_LOG.Warn("容器网络配置失败", zap.Int("vmid", vmid), zap.Error(err))
			}
		}
	} else {
		// 不需要rate限速，直接配置
		netCmd := fmt.Sprintf("pct set %d --net0 %s", vmid, netConfigStr)
		_, err = p.sshClient.Execute(netCmd)
		if err != nil {
			global.APP_LOG.Warn("容器网络配置失败", zap.Int("vmid", vmid), zap.Error(err))
		}
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

	// 获取网络类型配置
	networkConfig := p.parseNetworkConfigFromInstanceConfig(config)
	hasIPv6 := networkConfig.NetworkType == "nat_ipv4_ipv6" ||
		networkConfig.NetworkType == "dedicated_ipv4_ipv6" ||
		networkConfig.NetworkType == "ipv6_only"

	// 根据NetworkType选择第二个网络桥接
	// 仅在配置了IPv6时才使用vmbr2，纯IPv4模式只使用vmbr1
	var net1Bridge string
	if hasIPv6 {
		// 检查是否真正配置了IPv6
		ipv6Info, err := p.getIPv6Info(ctx)
		if err != nil {
			global.APP_LOG.Warn("获取IPv6信息失败",
				zap.Error(err),
				zap.String("networkType", networkConfig.NetworkType))
		}

		// 如果有appended addresses或基础IPv6配置，使用vmbr2
		if ipv6Info != nil && (ipv6Info.HasAppendedAddresses ||
			(ipv6Info.HostIPv6Address != "" && ipv6Info.IPv6Gateway != "")) {
			net1Bridge = "vmbr2"
			global.APP_LOG.Info("检测到IPv6环境，使用vmbr2",
				zap.Bool("hasAppendedAddresses", ipv6Info.HasAppendedAddresses),
				zap.String("hostIPv6", ipv6Info.HostIPv6Address))
		} else {
			// 没有任何IPv6配置，使用单网络接口
			net1Bridge = ""
			global.APP_LOG.Warn("未检测到IPv6环境，将使用单网络接口（仅vmbr1）",
				zap.String("networkType", networkConfig.NetworkType))
		}
	} else {
		// 纯IPv4模式，只使用vmbr1
		net1Bridge = ""
		global.APP_LOG.Info("使用IPv4-only配置，不创建vmbr2接口",
			zap.String("networkType", networkConfig.NetworkType))
	}

	// 创建虚拟机
	var createCmd string
	// 根据 PVE 版本决定是否使用 fstrim_cloned_disks 参数
	agentParam := "1"
	if p.supportsCloneFstrim() {
		agentParam = "1,fstrim_cloned_disks=1"
	}

	// 构建网络配置字符串，包含 rate 参数
	net0Config := "virtio,bridge=vmbr1,firewall=0"
	net0ConfigWithRate := net0Config
	useRateLimit := false

	if networkConfig.OutSpeed > 0 {
		// Proxmox rate 参数单位为 MB/s，配置中的 OutSpeed 单位为 Mbps，需要转换：MB/s = Mbps ÷ 8
		rateMBps := networkConfig.OutSpeed / 8
		if rateMBps < 1 {
			rateMBps = 1 // 最小1MB/s
		}
		net0ConfigWithRate = fmt.Sprintf("%s,rate=%d", net0Config, rateMBps)
		useRateLimit = true
	}

	if net1Bridge != "" {
		// 双网络接口模式（IPv6）
		if useRateLimit {
			createCmd = fmt.Sprintf(
				"qm create %d --agent %s --scsihw virtio-scsi-single --serial0 socket --cores %s --sockets 1 --cpu %s --net0 %s --net1 virtio,bridge=%s,firewall=0 --ostype l26 %s",
				vmid, agentParam, cpuFormatted, cpuType, net0ConfigWithRate, net1Bridge, kvmFlag,
			)
		} else {
			createCmd = fmt.Sprintf(
				"qm create %d --agent %s --scsihw virtio-scsi-single --serial0 socket --cores %s --sockets 1 --cpu %s --net0 %s --net1 virtio,bridge=%s,firewall=0 --ostype l26 %s",
				vmid, agentParam, cpuFormatted, cpuType, net0Config, net1Bridge, kvmFlag,
			)
		}
	} else {
		// 单网络接口模式（纯IPv4或IPv6环境缺失）
		if useRateLimit {
			createCmd = fmt.Sprintf(
				"qm create %d --agent %s --scsihw virtio-scsi-single --serial0 socket --cores %s --sockets 1 --cpu %s --net0 %s --ostype l26 %s",
				vmid, agentParam, cpuFormatted, cpuType, net0ConfigWithRate, kvmFlag,
			)
		} else {
			createCmd = fmt.Sprintf(
				"qm create %d --agent %s --scsihw virtio-scsi-single --serial0 socket --cores %s --sockets 1 --cpu %s --net0 %s --ostype l26 %s",
				vmid, agentParam, cpuFormatted, cpuType, net0Config, kvmFlag,
			)
		}
	}

	_, err = p.sshClient.Execute(createCmd)
	if err != nil && useRateLimit {
		// 带rate参数创建失败，尝试不带rate重试
		global.APP_LOG.Warn("虚拟机创建（带rate）失败，尝试不带rate的创建",
			zap.Int("vmid", vmid),
			zap.Error(err))

		// 重新构建不带rate的命令
		if net1Bridge != "" {
			createCmd = fmt.Sprintf(
				"qm create %d --agent %s --scsihw virtio-scsi-single --serial0 socket --cores %s --sockets 1 --cpu %s --net0 %s --net1 virtio,bridge=%s,firewall=0 --ostype l26 %s",
				vmid, agentParam, cpuFormatted, cpuType, net0Config, net1Bridge, kvmFlag,
			)
		} else {
			createCmd = fmt.Sprintf(
				"qm create %d --agent %s --scsihw virtio-scsi-single --serial0 socket --cores %s --sockets 1 --cpu %s --net0 %s --ostype l26 %s",
				vmid, agentParam, cpuFormatted, cpuType, net0Config, kvmFlag,
			)
		}
		_, err = p.sshClient.Execute(createCmd)
	}

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

	// 配置网络（使用VMID到IP的映射函数，充分利用IP地址空间）
	userIP := VMIDToInternalIP(vmid)
	_, err = p.sshClient.Execute(fmt.Sprintf("qm set %d --ipconfig0 ip=%s/24,gw=%s", vmid, userIP, InternalGateway))
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

	updateProgress(96, "等待虚拟机启动...")

	// 等待虚拟机状态变为running
	maxWaitTime := 90 * time.Second
	checkInterval := 3 * time.Second
	startTime := time.Now()
	vmRunning := false

	for time.Since(startTime) < maxWaitTime {
		statusCmd := fmt.Sprintf("qm status %d", vmid)
		statusOutput, err := p.sshClient.Execute(statusCmd)
		if err == nil && strings.Contains(statusOutput, "status: running") {
			vmRunning = true
			global.APP_LOG.Info("虚拟机已启动",
				zap.Int("vmid", vmid),
				zap.Duration("elapsed", time.Since(startTime)))
			break
		}
		time.Sleep(checkInterval)
	}

	if !vmRunning {
		global.APP_LOG.Warn("虚拟机启动超时，但继续创建流程",
			zap.Int("vmid", vmid),
			zap.Duration("elapsed", time.Since(startTime)))
	}

	updateProgress(98, "检测Guest Agent...")

	// 智能等待QEMU Guest Agent就绪（可选，失败不影响创建流程）
	if vmRunning {
		// 先快速检测3次，判断镜像是否支持Guest Agent
		agentSupported := false
		for i := 0; i < 3; i++ {
			agentCmd := fmt.Sprintf("qm agent %d ping 2>/dev/null", vmid)
			_, err := p.sshClient.Execute(agentCmd)
			if err == nil {
				agentSupported = true
				global.APP_LOG.Info("检测到QEMU Guest Agent已安装并就绪",
					zap.Int("vmid", vmid))
				break
			}
			time.Sleep(2 * time.Second)
		}

		// 如果快速检测失败，说明镜像可能没有安装Agent，进行较短的等待
		if !agentSupported {
			global.APP_LOG.Info("镜像可能未安装QEMU Guest Agent，进行短时等待...",
				zap.Int("vmid", vmid))

			// 只再等待15秒，给系统更多启动时间
			agentWaitTime := 15 * time.Second
			agentStartTime := time.Now()

			for time.Since(agentStartTime) < agentWaitTime {
				agentCmd := fmt.Sprintf("qm agent %d ping 2>/dev/null", vmid)
				_, err := p.sshClient.Execute(agentCmd)
				if err == nil {
					global.APP_LOG.Info("QEMU Guest Agent已就绪",
						zap.Int("vmid", vmid),
						zap.Duration("elapsed", time.Since(agentStartTime)))
					agentSupported = true
					break
				}
				time.Sleep(3 * time.Second)
			}

			if !agentSupported {
				global.APP_LOG.Warn("镜像未安装QEMU Guest Agent或Agent启动较慢",
					zap.Int("vmid", vmid),
					zap.String("建议", "如需使用Agent功能，请在镜像中安装qemu-guest-agent软件包"))
			}
		}
	}

	updateProgress(100, "虚拟机创建完成")
	global.APP_LOG.Info("虚拟机创建成功",
		zap.Int("vmid", vmid),
		zap.String("image", config.Image),
		zap.String("storage", storage),
		zap.String("cpu_type", cpuType))

	return nil
}
