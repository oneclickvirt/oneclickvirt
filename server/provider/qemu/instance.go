package qemu

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/provider/firewall"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

func (p *QEMUProvider) ListInstances(ctx context.Context) ([]provider.Instance, error) {
	if !p.connected {
		return nil, fmt.Errorf("not connected")
	}
	return p.sshListInstances(ctx)
}

func (p *QEMUProvider) CreateInstance(ctx context.Context, config provider.InstanceConfig) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}
	if strings.ToLower(strings.TrimSpace(config.InstanceType)) == "container" {
		return p.sshCreateLXCContainer(ctx, config, nil)
	}
	return p.sshCreateInstance(ctx, config, nil)
}

func (p *QEMUProvider) CreateInstanceWithProgress(ctx context.Context, config provider.InstanceConfig, progressCallback provider.ProgressCallback) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}
	if strings.ToLower(strings.TrimSpace(config.InstanceType)) == "container" {
		return p.sshCreateLXCContainer(ctx, config, progressCallback)
	}
	return p.sshCreateInstance(ctx, config, progressCallback)
}

func (p *QEMUProvider) GetInstance(ctx context.Context, id string) (*provider.Instance, error) {
	if !p.connected {
		return nil, fmt.Errorf("not connected")
	}
	instances, err := p.sshListInstances(ctx)
	if err != nil {
		return nil, err
	}
	for _, inst := range instances {
		if inst.Name == id || inst.ID == id {
			return &inst, nil
		}
	}
	return nil, fmt.Errorf("instance %s not found", id)
}

// sshListInstances 通过 libvirt 列出 QEMU/KVM 虚拟机和 libvirt-lxc 容器。
func (p *QEMUProvider) sshListInstances(ctx context.Context) ([]provider.Instance, error) {
	var instances []provider.Instance
	seen := map[string]struct{}{}

	vmOutput, vmErr := p.sshClient.Execute("virsh -c qemu:///system list --all --name 2>/dev/null | grep -v '^$'")
	if vmErr != nil {
		global.APP_LOG.Debug("列出QEMU虚拟机失败", zap.Error(vmErr))
	}
	for _, name := range strings.Split(strings.TrimSpace(vmOutput), "\n") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		inst, err := p.getInstanceInfo(ctx, name, "vm")
		if err != nil {
			global.APP_LOG.Warn("获取QEMU虚拟机信息失败", zap.String("name", name), zap.Error(err))
			continue
		}
		seen[name] = struct{}{}
		instances = append(instances, *inst)
	}

	lxcOutput, lxcErr := p.sshClient.Execute("virsh -c lxc:/// list --all --name 2>/dev/null | grep -v '^$'")
	if lxcErr != nil {
		global.APP_LOG.Debug("列出libvirt-lxc容器失败", zap.Error(lxcErr))
	}
	for _, name := range strings.Split(strings.TrimSpace(lxcOutput), "\n") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		inst, err := p.getInstanceInfo(ctx, name, "container")
		if err != nil {
			global.APP_LOG.Warn("获取libvirt-lxc容器信息失败", zap.String("name", name), zap.Error(err))
			continue
		}
		instances = append(instances, *inst)
	}

	if vmErr != nil && lxcErr != nil {
		return nil, fmt.Errorf("failed to list QEMU VMs and LXC containers: %w", vmErr)
	}
	return instances, nil
}

// getInstanceInfo 获取单个 libvirt domain 详细信息。
func (p *QEMUProvider) getInstanceInfo(ctx context.Context, name string, instanceType string) (*provider.Instance, error) {
	uri := "qemu:///system"
	if instanceType == "container" {
		uri = "lxc:///"
	}
	output, err := p.sshClient.Execute(fmt.Sprintf("virsh -c %s dominfo %s 2>/dev/null", shellSingleQuote(uri), shellSingleQuote(name)))
	if err != nil {
		return nil, fmt.Errorf("failed to get libvirt domain info: %w", err)
	}

	inst := &provider.Instance{
		Name:   name,
		ID:     name,
		Type:   instanceType,
		Status: "unknown",
	}

	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "State":
			inst.Status = mapVirshStatus(value)
		case "CPU(s)":
			inst.CPU = value
		case "Max memory":
			if memKB, err := parseKiBValue(value); err == nil {
				inst.Memory = fmt.Sprintf("%d MB", memKB/1024)
			}
		case "UUID":
			inst.ID = value
		}
	}

	if ip := p.getVMIPAddress(ctx, name); ip != "" {
		inst.IP = ip
		inst.PrivateIP = ip
	}

	return inst, nil
}

// sshCreateInstance 直接通过 SSH 命令创建 QEMU 虚拟机（不依赖外部 shell 脚本）
func (p *QEMUProvider) sshCreateInstance(ctx context.Context, config provider.InstanceConfig, progressCallback provider.ProgressCallback) error {
	updateProgress := func(pct int, msg string) {
		if progressCallback != nil {
			progressCallback(pct, msg)
		}
		global.APP_LOG.Debug(msg, zap.String("instance", config.Name), zap.Int("progress", pct))
	}

	updateProgress(5, "开始创建QEMU虚拟机")

	// 预检：确保 QEMU/libvirt 关键命令可用，避免后续命令以 127 失败且错误不明确
	if _, err := p.sshClient.Execute("command -v virsh >/dev/null 2>&1"); err != nil {
		return fmt.Errorf("virsh 命令不可用，请确认 provider 节点已安装 libvirt 并在 PATH 中: %w", err)
	}
	if _, err := p.sshClient.Execute("command -v qemu-img >/dev/null 2>&1"); err != nil {
		return fmt.Errorf("qemu-img 命令不可用，请确认 provider 节点已安装 qemu-utils 并在 PATH 中: %w", err)
	}
	if _, err := p.sshClient.Execute("command -v virt-install >/dev/null 2>&1"); err != nil {
		return fmt.Errorf("virt-install 命令不可用，请确认 provider 节点已安装 virtinst/virt-install 并在 PATH 中: %w", err)
	}
	// libvirt 在不同发行版上服务名可能是 libvirtd 或 virtqemud，允许已由 socket 激活；至少确认 virsh 能连接。
	if _, err := p.sshClient.Execute("virsh uri >/dev/null 2>&1"); err != nil {
		return fmt.Errorf("libvirt 连接不可用，请确认 libvirtd/virtqemud 已启动且当前用户有权限访问: %w", err)
	}

	// 解析资源配置
	cpu, _ := strconv.Atoi(config.CPU)
	if cpu <= 0 {
		cpu = 1
	}
	memoryMB := parseConfigMB(config.Memory)
	if memoryMB <= 0 {
		memoryMB = 512
	}
	diskGB := parseConfigGB(config.Disk)
	if diskGB <= 0 {
		diskGB = 10
	}

	password := "password"
	if pw, ok := config.Metadata["password"]; ok && pw != "" {
		password = pw
	}

	sshPort := 0
	startPort := 0
	endPort := 0
	if len(config.Ports) >= 1 {
		sshPort, _ = strconv.Atoi(config.Ports[0])
	}
	if len(config.Ports) >= 2 {
		startPort, _ = strconv.Atoi(config.Ports[1])
	}
	if len(config.Ports) >= 3 {
		endPort, _ = strconv.Atoi(config.Ports[2])
	}

	system := config.Image
	if system == "" {
		system = "debian12"
	}

	updateProgress(10, "确保镜像存在")

	// 确保目录存在
	p.sshClient.Execute(fmt.Sprintf("mkdir -p %s %s", shellSingleQuote(ImageDir), shellSingleQuote(VMLogDir)))

	// 确保基础镜像存在，如果不存在则从 seed 数据库中的 URL 下载
	baseImage := fmt.Sprintf("%s/%s.qcow2", ImageDir, system)
	output, err := p.sshClient.Execute(fmt.Sprintf("test -f %s && test -s %s && echo 'ok'", shellSingleQuote(baseImage), shellSingleQuote(baseImage)))
	if err != nil || strings.TrimSpace(output) != "ok" {
		if config.ImageURL == "" {
			return fmt.Errorf("base image not found and no image URL configured for image=%s", system)
		}
		global.APP_LOG.Info("QEMU基础镜像不存在，开始自动下载",
			zap.String("system", system),
			zap.String("imageURL", config.ImageURL))
		updateProgress(11, fmt.Sprintf("下载基础镜像 %s", system))

		// 确定下载URL：对GitHub链接尝试CDN加速
		downloadURL := config.ImageURL
		if config.UseCDN && (strings.Contains(config.ImageURL, "github.com/") || strings.Contains(config.ImageURL, "raw.githubusercontent.com/")) {
			cdnURL := utils.GetCDNURL(p.sshClient, config.ImageURL, "QEMU")
			if cdnURL != "" {
				downloadURL = cdnURL
			}
		}

		tmpPath := baseImage + ".tmp"
		runDownload := func(rawURL string) (string, error) {
			downloadScript := utils.BuildRemoteDownloadScript(rawURL, tmpPath, baseImage)
			return p.sshClient.ExecuteViaTempScript(downloadScript, nil, 30*time.Minute)
		}
		dlOutput, dlErr := runDownload(downloadURL)
		if dlErr != nil {
			// CDN下载失败，回退到原始URL重试
			if downloadURL != config.ImageURL {
				global.APP_LOG.Warn("CDN下载失败，回退到原始URL",
					zap.String("cdnURL", utils.TruncateString(downloadURL, 200)),
					zap.String("output", utils.TruncateString(dlOutput, 200)))
				p.sshClient.Execute(fmt.Sprintf("rm -f %s", shellSingleQuote(tmpPath)))
				dlOutput, dlErr = runDownload(config.ImageURL)
			}
			if dlErr != nil {
				p.sshClient.Execute(fmt.Sprintf("rm -f %s", shellSingleQuote(tmpPath)))
				return fmt.Errorf("failed to download base image for image=%s imageURL=%s downloadURL=%s: %s",
					system, utils.TruncateString(config.ImageURL, 300), utils.TruncateString(downloadURL, 300), utils.TruncateString(dlOutput, 200))
			}
		}

		global.APP_LOG.Info("QEMU基础镜像下载成功",
			zap.String("system", system),
			zap.String("url", utils.TruncateString(downloadURL, 200)))
	}

	updateProgress(15, "确保 libvirt default 网络就绪")

	// 确保 libvirt default 网络活跃并随宿主机自启动。
	p.sshClient.Execute("virsh net-start default 2>/dev/null || true")
	p.sshClient.Execute("virsh net-autostart default 2>/dev/null || true")

	// 获取网桥名称
	bridgeOutput, _ := p.sshClient.Execute("virsh net-dumpxml default 2>/dev/null | grep '<bridge' | grep -oP 'name=\"\\K[^\"]+' || echo virbr0")
	bridgeName := strings.TrimSpace(bridgeOutput)
	if bridgeName == "" {
		bridgeName = "virbr0"
	}

	updateProgress(20, "生成 MAC 地址和分配 IP")

	// 生成 MAC 地址
	macOutput, err := p.sshClient.Execute("printf '52:54:%02x:%02x:%02x:%02x\\n' $((RANDOM%256)) $((RANDOM%256)) $((RANDOM%256)) $((RANDOM%256))")
	if err != nil {
		return fmt.Errorf("failed to generate MAC address: %w", err)
	}
	vmMAC := strings.TrimSpace(macOutput)

	// 分配静态 IP（加锁保证并发安全，从 192.168.122.2 ~ 192.168.122.254 中找空闲的）
	p.ipMu.Lock()
	vmIP, err := p.allocateIP()
	if err != nil {
		p.ipMu.Unlock()
		return fmt.Errorf("failed to allocate IP: %w", err)
	}
	// 立即写入 DHCP 预留，防止并发任务分配到相同IP
	p.setupDHCPReservation(config.Name, vmMAC, vmIP)
	p.ipMu.Unlock()

	global.APP_LOG.Info("VM 网络配置",
		zap.String("name", config.Name),
		zap.String("mac", vmMAC),
		zap.String("ip", vmIP))

	updateProgress(30, "创建 VM 磁盘")

	// 创建差量磁盘
	artifactName := qemuSafeFileComponent(config.Name)
	vmDisk := fmt.Sprintf("%s/vm-%s.qcow2", ImageDir, artifactName)
	diskCmd := fmt.Sprintf("qemu-img create -f qcow2 -b %s -F qcow2 %s %dG 2>&1", shellSingleQuote(baseImage), shellSingleQuote(vmDisk), diskGB)
	output, err = p.sshClient.Execute(diskCmd)
	if err != nil {
		return fmt.Errorf("failed to create VM disk: %s, %w", utils.TruncateString(output, 200), err)
	}

	updateProgress(40, "创建 cloud-init ISO")

	// 创建 cloud-init 配置和 ISO
	ciISO, err := p.createCloudInitISO(config.Name, password)
	if err != nil {
		// 清理磁盘
		p.sshClient.Execute(fmt.Sprintf("rm -f %s", shellSingleQuote(vmDisk)))
		return fmt.Errorf("failed to create cloud-init ISO: %w", err)
	}

	updateProgress(50, "配置端口转发")

	// 配置防火墙端口转发
	fwMgr := firewall.NewManager(p.sshClient, NFTTableName, InternalSubnet)
	if _, err := fwMgr.DetectBackend(FWBackendFile); err != nil {
		// 清理磁盘和 cloud-init ISO
		p.sshClient.Execute(fmt.Sprintf("rm -f %s %s", shellSingleQuote(vmDisk), shellSingleQuote(ciISO)))
		return fmt.Errorf("防火墙后端检测失败: %w", err)
	}
	if err := fwMgr.InitTable(); err != nil {
		p.sshClient.Execute(fmt.Sprintf("rm -f %s %s", shellSingleQuote(vmDisk), shellSingleQuote(ciISO)))
		return fmt.Errorf("防火墙初始化失败: %w", err)
	}
	if sshPort > 0 {
		if err := fwMgr.AddDNAT(config.Name, vmIP, sshPort, startPort, endPort); err != nil {
			// 端口转发失败是致命错误 —— VM 创建后无法被外部访问
			p.sshClient.Execute(fmt.Sprintf("rm -f %s %s", shellSingleQuote(vmDisk), shellSingleQuote(ciISO)))
			return fmt.Errorf("端口转发规则添加失败: %w", err)
		}
	}
	fwMgr.SaveRules()

	updateProgress(65, "部署虚拟机")

	// 检测 KVM 加速
	virtType := "qemu"
	kvmOutput, err := p.sshClient.Execute("test -w /dev/kvm && echo kvm")
	if err == nil && strings.TrimSpace(kvmOutput) == "kvm" {
		virtType = "kvm"
		global.APP_LOG.Debug("QEMU Provider检测到KVM硬件加速可用", zap.String("virtType", virtType))
	} else {
		global.APP_LOG.Warn("QEMU Provider未检测到可用KVM，自动使用QEMU软件模拟",
			zap.String("virtType", virtType),
			zap.String("image", system),
			zap.String("imageURL", utils.TruncateString(config.ImageURL, 300)))
	}

	// 获取 os-variant
	osVariant := p.getOSVariant(system)

	// 构建 virt-install 命令
	virtCmd := fmt.Sprintf(
		`virt-install --name %s --memory %d --vcpus %d --virt-type %s --import `+
			`--disk %s --disk %s --network %s --os-variant %s --sysinfo %s `+
			`--graphics none --serial pty --console %s --noautoconsole 2>&1`,
		shellSingleQuote(config.Name), memoryMB, cpu, shellSingleQuote(virtType),
		shellSingleQuote(fmt.Sprintf("path=%s,format=qcow2,bus=virtio,cache=none", vmDisk)),
		shellSingleQuote(fmt.Sprintf("path=%s,format=raw,bus=virtio,readonly=on", ciISO)),
		shellSingleQuote(fmt.Sprintf("network=default,mac=%s,model=virtio", vmMAC)),
		shellSingleQuote(osVariant),
		shellSingleQuote("type=smbios,system.serial=ds=nocloud"),
		shellSingleQuote("pty,target_type=serial"))

	output, err = p.sshClient.Execute(virtCmd)
	virtRC := err

	// 如果失败，用 detect=on,require=off 重试
	if virtRC != nil {
		global.APP_LOG.Warn("virt-install 失败，用通用 os-variant 重试",
			zap.String("output", utils.TruncateString(output, 500)))
		p.sshClient.Execute(fmt.Sprintf("virsh undefine %s --remove-all-storage 2>/dev/null || virsh undefine %s 2>/dev/null || true", shellSingleQuote(config.Name), shellSingleQuote(config.Name)))
		// 重建磁盘
		p.sshClient.Execute(fmt.Sprintf("test -f %s || qemu-img create -f qcow2 -b %s -F qcow2 %s %dG 2>/dev/null", shellSingleQuote(vmDisk), shellSingleQuote(baseImage), shellSingleQuote(vmDisk), diskGB))
		retryCmd := fmt.Sprintf(
			`virt-install --name %s --memory %d --vcpus %d --virt-type %s --import `+
				`--disk %s --disk %s --network %s --os-variant %s --sysinfo %s `+
				`--graphics none --serial pty --console %s --noautoconsole 2>&1`,
			shellSingleQuote(config.Name), memoryMB, cpu, shellSingleQuote(virtType),
			shellSingleQuote(fmt.Sprintf("path=%s,format=qcow2,bus=virtio,cache=none", vmDisk)),
			shellSingleQuote(fmt.Sprintf("path=%s,format=raw,bus=virtio,readonly=on", ciISO)),
			shellSingleQuote(fmt.Sprintf("network=default,mac=%s,model=virtio", vmMAC)),
			shellSingleQuote("detect=on,require=off"),
			shellSingleQuote("type=smbios,system.serial=ds=nocloud"),
			shellSingleQuote("pty,target_type=serial"))
		output, err = p.sshClient.Execute(retryCmd)
		virtRC = err
	}

	// 如果 KVM 模式失败，降级到 TCG
	if virtRC != nil && virtType == "kvm" {
		global.APP_LOG.Warn("KVM 模式失败，降级到 TCG",
			zap.String("output", utils.TruncateString(output, 500)))
		p.sshClient.Execute(fmt.Sprintf("virsh undefine %s --remove-all-storage 2>/dev/null || virsh undefine %s 2>/dev/null || true", shellSingleQuote(config.Name), shellSingleQuote(config.Name)))
		p.sshClient.Execute(fmt.Sprintf("test -f %s || qemu-img create -f qcow2 -b %s -F qcow2 %s %dG 2>/dev/null", shellSingleQuote(vmDisk), shellSingleQuote(baseImage), shellSingleQuote(vmDisk), diskGB))
		tcgCmd := fmt.Sprintf(
			`virt-install --name %s --memory %d --vcpus %d --virt-type qemu --import `+
				`--disk %s --disk %s --network %s --os-variant %s --sysinfo %s `+
				`--graphics none --serial pty --console %s --noautoconsole 2>&1`,
			shellSingleQuote(config.Name), memoryMB, cpu,
			shellSingleQuote(fmt.Sprintf("path=%s,format=qcow2,bus=virtio,cache=none", vmDisk)),
			shellSingleQuote(fmt.Sprintf("path=%s,format=raw,bus=virtio,readonly=on", ciISO)),
			shellSingleQuote(fmt.Sprintf("network=default,mac=%s,model=virtio", vmMAC)),
			shellSingleQuote("detect=on,require=off"),
			shellSingleQuote("type=smbios,system.serial=ds=nocloud"),
			shellSingleQuote("pty,target_type=serial"))
		output, err = p.sshClient.Execute(tcgCmd)
		virtRC = err
		virtType = "qemu"
	}

	// 清理临时文件
	p.sshClient.Execute(fmt.Sprintf("rm -f %s %s 2>/dev/null || true", shellSingleQuote(qemuCloudInitUserDataPath(config.Name)), shellSingleQuote(qemuCloudInitMetaDataPath(config.Name))))

	if virtRC != nil {
		// 部署失败，清理
		global.APP_LOG.Error("QEMU 虚拟机部署失败",
			zap.String("name", config.Name),
			zap.String("output", utils.TruncateString(output, 1000)),
			zap.Error(virtRC))
		p.sshClient.Execute(fmt.Sprintf("virsh undefine %s 2>/dev/null || true", shellSingleQuote(config.Name)))
		p.sshClient.Execute(fmt.Sprintf("rm -f %s %s 2>/dev/null || true", shellSingleQuote(vmDisk), shellSingleQuote(ciISO)))
		return fmt.Errorf("failed to create VM using virt-type=%s image=%s imageURL=%s: %w (virt-install output: %s)",
			virtType, system, utils.TruncateString(config.ImageURL, 300), virtRC, utils.TruncateString(output, 500))
	}

	// 设置开机自启
	p.sshClient.Execute(fmt.Sprintf("virsh autostart %s 2>/dev/null || true", shellSingleQuote(config.Name)))

	updateProgress(80, "等待虚拟机启动")

	// 等待VM启动
	startWaitSeconds := 60
	if virtType == "qemu" {
		startWaitSeconds = 300
	}
	var vmStarted bool
	for i := 0; i < startWaitSeconds/2; i++ {
		statusOutput, err := p.sshClient.Execute(fmt.Sprintf("virsh domstate %s 2>/dev/null", shellSingleQuote(config.Name)))
		if err == nil && strings.Contains(strings.TrimSpace(statusOutput), "running") {
			vmStarted = true
			break
		}
		if err := sleepWithContext(ctx, 2*time.Second); err != nil {
			global.APP_LOG.Warn("QEMU虚拟机创建等待启动被取消，开始清理远端资源",
				zap.String("name", utils.TruncateString(config.Name, 32)),
				zap.Error(err))
			updateProgress(90, "创建已取消，正在清理QEMU资源")
			if cleanupErr := p.sshDeleteInstance(context.Background(), config.Name); cleanupErr != nil {
				global.APP_LOG.Error("QEMU虚拟机取消后清理失败",
					zap.String("name", utils.TruncateString(config.Name, 32)),
					zap.Error(cleanupErr))
			}
			return fmt.Errorf("waiting for QEMU VM start cancelled: %w", err)
		}
	}
	if !vmStarted {
		global.APP_LOG.Warn("QEMU虚拟机创建等待启动超时，开始清理远端资源",
			zap.String("name", utils.TruncateString(config.Name, 32)),
			zap.String("virtType", virtType),
			zap.Int("waitSeconds", startWaitSeconds))
		updateProgress(90, "启动超时，正在清理QEMU资源")
		if cleanupErr := p.sshDeleteInstance(context.Background(), config.Name); cleanupErr != nil {
			global.APP_LOG.Error("QEMU虚拟机启动超时后清理失败",
				zap.String("name", utils.TruncateString(config.Name, 32)),
				zap.Error(cleanupErr))
		}
		return fmt.Errorf("VM %q did not start within %d seconds (virt-type=%s image=%s imageURL=%s)",
			config.Name, startWaitSeconds, virtType, system, utils.TruncateString(config.ImageURL, 300))
	}

	p.applyLibvirtIOLimits(ctx, "qemu:///system", config.Name, "vda", config)

	// cloud-init ISO 仅首次启动时需要，启动成功后分离并删除以节省磁盘空间
	p.sshClient.Execute(fmt.Sprintf("virsh detach-disk %s sdb --persistent 2>/dev/null || true", shellSingleQuote(config.Name)))
	p.sshClient.Execute(fmt.Sprintf("rm -f %s 2>/dev/null || true", shellSingleQuote(ciISO)))

	updateProgress(95, "虚拟机创建完成")

	// 写入 vmlog（不写入密码）
	logLine := fmt.Sprintf("%s %d %s %d %d %d %d %d %s %s",
		config.Name, sshPort, "***", cpu, memoryMB, diskGB, startPort, endPort, system, vmIP)
	logCmd := fmt.Sprintf("printf '%%s\\n' %s >> /root/vmlog", shellSingleQuote(logLine))
	p.sshClient.Execute(logCmd)

	updateProgress(100, "QEMU虚拟机创建完成")
	return nil
}

// allocateIP 从 192.168.122.2~254 中分配可用的静态 IP
func (p *QEMUProvider) allocateIP() (string, error) {
	output, err := p.sshClient.Execute("virsh net-dumpxml default 2>/dev/null | grep '<host ' | grep -oP \"ip='[^']+\" | cut -d\"'\" -f2 | sort -t. -k4 -n")
	usedIPs := ""
	if err == nil {
		usedIPs = output
	}

	for i := 2; i <= 254; i++ {
		candidate := fmt.Sprintf("192.168.122.%d", i)
		if !strings.Contains(usedIPs, candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no available IP in 192.168.122.0/24")
}

// setupDHCPReservation 在 libvirt default 网络中设置 DHCP 固定分配
func (p *QEMUProvider) setupDHCPReservation(vmName, vmMAC, vmIP string) {
	// 先删除旧记录
	currentHostXML := fmt.Sprintf("<host mac='%s' name='%s' ip='%s' />", vmMAC, vmName, vmIP)
	p.sshClient.Execute(fmt.Sprintf(
		"virsh net-update default delete ip-dhcp-host %s --live --config 2>/dev/null || true",
		shellSingleQuote(currentHostXML)))

	// 删除可能存在的同名旧记录
	oldMAC, _ := p.sshClient.Execute(fmt.Sprintf(
		"virsh net-dumpxml default 2>/dev/null | grep -F %s | grep -oP \"mac='[^']+\" | cut -d\"'\" -f2",
		shellSingleQuote("name='"+vmName+"'")))
	oldMAC = strings.TrimSpace(oldMAC)
	if oldMAC != "" {
		oldIP, _ := p.sshClient.Execute(fmt.Sprintf(
			"virsh net-dumpxml default 2>/dev/null | grep -F %s | grep -oP \"ip='[^']+\" | cut -d\"'\" -f2",
			shellSingleQuote("name='"+vmName+"'")))
		oldIP = strings.TrimSpace(oldIP)
		if oldIP != "" {
			oldHostXML := fmt.Sprintf("<host mac='%s' name='%s' ip='%s' />", oldMAC, vmName, oldIP)
			p.sshClient.Execute(fmt.Sprintf(
				"virsh net-update default delete ip-dhcp-host %s --live --config 2>/dev/null || "+
					"virsh net-update default delete ip-dhcp-host %s --config 2>/dev/null || true",
				shellSingleQuote(oldHostXML), shellSingleQuote(oldHostXML)))
		}
	}

	// 添加新记录
	p.sshClient.Execute(fmt.Sprintf(
		"virsh net-update default add ip-dhcp-host %s --live --config 2>/dev/null || "+
			"virsh net-update default add ip-dhcp-host %s --config 2>/dev/null || true",
		shellSingleQuote(currentHostXML), shellSingleQuote(currentHostXML)))
}

// createCloudInitISO 创建 cloud-init ISO
func (p *QEMUProvider) createCloudInitISO(vmName, password string) (string, error) {
	artifactName := qemuSafeFileComponent(vmName)
	ciISO := fmt.Sprintf("%s/vm-%s-cloudinit.iso", ImageDir, artifactName)
	userDataPath := qemuCloudInitUserDataPath(vmName)
	metaDataPath := qemuCloudInitMetaDataPath(vmName)
	userPassword := strings.ReplaceAll(strings.ReplaceAll(password, "\r", ""), "\n", "")

	// 创建 user-data
	userDataCmd := fmt.Sprintf(`cat > %s << 'CIEOF'
#cloud-config
hostname: %s
locale: en_US.UTF-8
disable_root: false
ssh_pwauth: true
chpasswd:
  expire: false
  list:
    - %s
write_files:
  - path: /etc/ssh/sshd_config.d/99-qemu.conf
    content: |
      PermitRootLogin yes
      PasswordAuthentication yes
      PubkeyAuthentication yes
runcmd:
  - systemctl enable --now serial-getty@ttyS0.service 2>/dev/null || true
  - printf 'root:%%s\n' %s | chpasswd
  - |
    if [ -f /etc/ssh/sshd_config ]; then
      sed -i 's/^#*PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config
      sed -i 's/^#*PasswordAuthentication.*/PasswordAuthentication yes/' /etc/ssh/sshd_config
    fi
  - systemctl restart ssh 2>/dev/null || systemctl restart sshd 2>/dev/null || true
final_message: "cloud-init done after $UPTIME seconds"
CIEOF`, shellSingleQuote(userDataPath), yamlDoubleQuote(qemuSafeFileComponent(vmName)), yamlDoubleQuote("root:"+userPassword), shellSingleQuote(userPassword))
	if _, err := p.sshClient.Execute(userDataCmd); err != nil {
		return "", fmt.Errorf("failed to create user-data: %w", err)
	}

	// 创建 meta-data
	metaDataCmd := fmt.Sprintf(`cat > %s << 'METAEOF'
instance-id: %s
local-hostname: %s
METAEOF`, shellSingleQuote(metaDataPath), qemuSafeFileComponent(vmName), qemuSafeFileComponent(vmName))
	if _, err := p.sshClient.Execute(metaDataCmd); err != nil {
		return "", fmt.Errorf("failed to create meta-data: %w", err)
	}

	// 创建 ISO（优先 cloud-localds，回退到 genisoimage/mkisofs）
	isoCmd := fmt.Sprintf(
		`if command -v cloud-localds >/dev/null 2>&1; then
  cloud-localds %s %s %s
elif command -v genisoimage >/dev/null 2>&1; then
  ci_dir=%s && mkdir -p "$ci_dir"
  cp %s "$ci_dir/user-data"
  cp %s "$ci_dir/meta-data"
  genisoimage -output %s -volid cidata -joliet -rock "$ci_dir" 2>/dev/null
  rm -rf "$ci_dir"
elif command -v mkisofs >/dev/null 2>&1; then
  ci_dir=%s && mkdir -p "$ci_dir"
  cp %s "$ci_dir/user-data"
  cp %s "$ci_dir/meta-data"
  mkisofs -output %s -volid cidata -joliet -rock "$ci_dir" 2>/dev/null
  rm -rf "$ci_dir"
else
  echo "ERROR: no ISO creation tool found" >&2 && exit 1
fi`,
		shellSingleQuote(ciISO), shellSingleQuote(userDataPath), shellSingleQuote(metaDataPath),
		shellSingleQuote("/tmp/qemu-ci-"+artifactName), shellSingleQuote(userDataPath), shellSingleQuote(metaDataPath), shellSingleQuote(ciISO),
		shellSingleQuote("/tmp/qemu-ci-"+artifactName), shellSingleQuote(userDataPath), shellSingleQuote(metaDataPath), shellSingleQuote(ciISO))

	output, err := p.sshClient.Execute(isoCmd)
	if err != nil {
		return "", fmt.Errorf("failed to create cloud-init ISO: %s, %w", utils.TruncateString(output, 200), err)
	}

	// 验证 ISO 存在
	checkOutput, err := p.sshClient.Execute(fmt.Sprintf("test -s %s && echo ok", shellSingleQuote(ciISO)))
	if err != nil || strings.TrimSpace(checkOutput) != "ok" {
		return "", fmt.Errorf("cloud-init ISO was not created: %s", ciISO)
	}

	return ciISO, nil
}

// getOSVariant 根据系统名返回 virt-install --os-variant 值
func (p *QEMUProvider) getOSVariant(system string) string {
	system = strings.ToLower(system)
	switch {
	case strings.HasPrefix(system, "debian10"):
		return "debian10"
	case strings.HasPrefix(system, "debian11"):
		return "debian11"
	case strings.HasPrefix(system, "debian12"):
		return "debian12"
	case strings.HasPrefix(system, "debian13"):
		return "debian12"
	case strings.HasPrefix(system, "ubuntu18"):
		return "ubuntu18.04"
	case strings.HasPrefix(system, "ubuntu20"):
		return "ubuntu20.04"
	case strings.HasPrefix(system, "ubuntu22"):
		return "ubuntu22.04"
	case strings.HasPrefix(system, "ubuntu24"):
		return "ubuntu24.04"
	case strings.HasPrefix(system, "almalinux8"), strings.HasPrefix(system, "alma8"):
		return "almalinux8"
	case strings.HasPrefix(system, "almalinux9"), strings.HasPrefix(system, "alma9"):
		return "almalinux9"
	case strings.HasPrefix(system, "rocky8"):
		return "rhel8.0"
	case strings.HasPrefix(system, "rocky9"):
		return "rhel9.0"
	case strings.HasPrefix(system, "openeuler"):
		return "rhel8.0"
	default:
		return "debian12"
	}
}

// getVMIPAddress 获取虚拟机IP地址
func (p *QEMUProvider) getVMIPAddress(ctx context.Context, name string) string {
	// 优先从 DHCP 预留获取
	output, err := p.sshClient.Execute(fmt.Sprintf(
		"virsh net-dumpxml default 2>/dev/null | grep -F %s | grep -oP \"ip='[^']+\" | cut -d\"'\" -f2",
		shellSingleQuote("name='"+name+"'")))
	if err == nil {
		ip := strings.TrimSpace(output)
		if ip != "" {
			return ip
		}
	}

	// 尝试通过 virsh domifaddr 获取
	output, err = p.sshClient.Execute(fmt.Sprintf("virsh domifaddr %s 2>/dev/null | grep -oP '192\\.168\\.122\\.\\d+'", shellSingleQuote(name)))
	if err == nil {
		ip := strings.TrimSpace(output)
		if ip != "" {
			return ip
		}
	}

	// 从 vmlog 获取
	output, err = p.sshClient.Execute(fmt.Sprintf("grep -F %s /root/vmlog 2>/dev/null | tail -1 | awk '{print $10}'", shellSingleQuote(name+" ")))
	if err == nil {
		ip := strings.TrimSpace(output)
		if ip != "" && strings.HasPrefix(ip, "192.168.122.") {
			return ip
		}
	}

	return ""
}

func qemuSafeFileComponent(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), ".")
	if out == "" {
		return "vm"
	}
	return out
}

func qemuCloudInitUserDataPath(vmName string) string {
	return fmt.Sprintf("/tmp/qemu-cloudinit-%s.yaml", qemuSafeFileComponent(vmName))
}

func qemuCloudInitMetaDataPath(vmName string) string {
	return fmt.Sprintf("/tmp/qemu-cloudinit-%s-meta.yaml", qemuSafeFileComponent(vmName))
}

func yamlDoubleQuote(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return "\"" + s + "\""
}

// mapVirshStatus 将virsh状态映射到统一状态
func mapVirshStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	switch {
	case strings.Contains(status, "running"):
		return "running"
	case strings.Contains(status, "shut off"), strings.Contains(status, "shutoff"):
		return "stopped"
	case strings.Contains(status, "paused"):
		return "paused"
	case strings.Contains(status, "crashed"):
		return "error"
	case strings.Contains(status, "in shutdown"):
		return "stopping"
	default:
		return "unknown"
	}
}

// parseKiBValue 解析 "1048576 KiB" 格式的值为KB数值
func parseKiBValue(value string) (int64, error) {
	parts := strings.Fields(value)
	if len(parts) == 0 {
		return 0, fmt.Errorf("empty value")
	}
	return strconv.ParseInt(parts[0], 10, 64)
}

// parseConfigMB 解析实例配置中的内存/磁盘字符串为MB数值
// 支持格式: "512m", "512M", "512MB", "1g", "1G", "1GB", "512"（纯数字视为MB）
func parseConfigMB(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	lower := strings.ToLower(s)
	switch {
	case strings.HasSuffix(lower, "mb"):
		n, _ := strconv.Atoi(strings.TrimSuffix(lower, "mb"))
		return n
	case strings.HasSuffix(lower, "m"):
		n, _ := strconv.Atoi(strings.TrimSuffix(lower, "m"))
		return n
	case strings.HasSuffix(lower, "gb"):
		n, _ := strconv.Atoi(strings.TrimSuffix(lower, "gb"))
		return n * 1024
	case strings.HasSuffix(lower, "g"):
		n, _ := strconv.Atoi(strings.TrimSuffix(lower, "g"))
		return n * 1024
	default:
		n, _ := strconv.Atoi(s)
		return n
	}
}

// parseConfigGB 解析实例配置中的磁盘字符串为GB数值（向上取整）
// 支持格式: "10240m", "10g", "10G", "10" （纯数字视为MB）
func parseConfigGB(s string) int {
	mb := parseConfigMB(s)
	if mb <= 0 {
		return 0
	}
	gb := (mb + 1023) / 1024
	if gb < 1 {
		gb = 1
	}
	return gb
}
