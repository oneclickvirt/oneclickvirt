package proxmox

import (
	"context"
	"fmt"
	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	"oneclickvirt/utils"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
)

func (p *ProxmoxProvider) sshCreateInstance(ctx context.Context, config provider.InstanceConfig) error {
	return p.sshCreateInstanceWithProgress(ctx, config, nil)
}

func (p *ProxmoxProvider) sshCreateInstanceWithProgress(ctx context.Context, config provider.InstanceConfig, progressCallback provider.ProgressCallback) error {
	global.APP_LOG.Debug("开始在Proxmox节点上创建实例（使用SSH）",
		zap.String("node", p.node),
		zap.String("host", utils.TruncateString(p.config.Host, 32)),
		zap.String("instance_name", config.Name),
		zap.String("instance_type", config.InstanceType))
	// 进度更新辅助函数
	updateProgress := func(percentage int, message string) {
		if progressCallback != nil {
			progressCallback(percentage, message)
		}
		global.APP_LOG.Debug("Proxmox实例创建进度",
			zap.String("instance", config.Name),
			zap.Int("percentage", percentage),
			zap.String("message", message))
	}

	updateProgress(10, "开始创建Proxmox实例...")

	// 预检：确保 Proxmox 关键命令可用，避免后续命令以 127 失败且错误不明确
	if _, err := p.sshClient.Execute("command -v qm >/dev/null 2>&1"); err != nil {
		return fmt.Errorf("qm 命令不可用，请确认 provider 节点已安装 Proxmox VE 组件并在 PATH 中: %w", err)
	}
	if _, err := p.sshClient.Execute("command -v pct >/dev/null 2>&1"); err != nil {
		return fmt.Errorf("pct 命令不可用，请确认 provider 节点已安装 Proxmox VE 组件并在 PATH 中: %w", err)
	}

	// 获取下一个可用的VMID
	vmid, err := p.getNextVMID(ctx, config.InstanceType)
	if err != nil {
		return fmt.Errorf("获取VMID失败: %w", err)
	}
	// 确保创建完成（成功或失败）后释放 pendingVMIDs 中的占位，
	// 避免其他并发创建请求永久跳过该 ID。
	defer p.releasePendingVMID(vmid)

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

	// 初始化流量监控（仅当 provider 启用流量统计时才生效）
	updateProgress(95, "配置实例网络监控...")
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

	// 检查镜像是否已存在，不存在则下载（用 singleflight 防止同一镜像并发重复下载）
	checkCmd := fmt.Sprintf("[ -f %s ] && echo 'exists' || echo 'missing'", localImagePath)
	output, err := p.sshClient.Execute(checkCmd)
	if err != nil {
		return fmt.Errorf("检查镜像文件失败: %v", err)
	}

	if strings.TrimSpace(output) == "missing" {
		_, sfErr, _ := p.imageImportGroup.Do(localImagePath, func() (interface{}, error) {
			// 等待期间可能已由并发协程下载完毕，再次检查
			checkAgain, _ := p.sshClient.Execute(checkCmd)
			if strings.TrimSpace(checkAgain) == "exists" {
				return nil, nil
			}

			updateProgress(20, "下载容器镜像...")
			// 创建缓存目录
			_, err = p.sshClient.Execute("mkdir -p /var/lib/vz/template/cache")
			if err != nil {
				return nil, fmt.Errorf("创建缓存目录失败: %v", err)
			}

			// 确定下载URL（支持CDN）
			downloadURL := p.getDownloadURL(systemConfig.ImageURL, config.UseCDN)
			global.APP_LOG.Debug("下载容器镜像",
				zap.String("downloadURL", utils.TruncateString(downloadURL, 100)),
				zap.Bool("useCDN", config.UseCDN))

			// 下载镜像文件（先下载到临时文件，再 mv，避免并发写冲突）
			tmpPath := localImagePath + ".tmp"
			downloadCmd := fmt.Sprintf("curl -L -o %s %s", tmpPath, downloadURL)
			_, err = p.sshClient.Execute(downloadCmd)
			if err != nil {
				p.sshClient.Execute(fmt.Sprintf("rm -f %s", tmpPath))
				return nil, fmt.Errorf("下载镜像失败: %v", err)
			}
			_, err = p.sshClient.Execute(fmt.Sprintf("mv %s %s", tmpPath, localImagePath))
			if err != nil {
				p.sshClient.Execute(fmt.Sprintf("rm -f %s", tmpPath))
				return nil, fmt.Errorf("移动镜像文件失败: %v", err)
			}
			global.APP_LOG.Debug("容器镜像下载完成",
				zap.String("image_path", localImagePath),
				zap.String("url", downloadURL))
			return nil, nil
		})
		if sfErr != nil {
			return sfErr
		}
	}

	updateProgress(50, "创建LXC容器...")

	// 获取存储盘配置 - 从数据库查询Provider记录
	var providerRecord providerModel.Provider
	if err := global.APP_DB.Where("id = ?", p.config.ID).First(&providerRecord).Error; err != nil {
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

	global.APP_LOG.Debug("转换参数格式",
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

	global.APP_LOG.Debug("执行容器创建命令", zap.String("command", createCmd))

	_, err = p.sshClient.Execute(createCmd)
	if err != nil {
		return fmt.Errorf("创建容器失败: %w", err)
	}

	updateProgress(70, "配置容器网络...")

	// 配置网络（使用VMID到IP的映射函数，充分利用IP地址空间）
	// 使用 Proxmox 原生的 rate 参数限制带宽
	networkConfig := p.parseNetworkConfigFromInstanceConfig(config)
	userIP := p.vmidToInternalIP(vmid)
	netConfigStr := fmt.Sprintf("name=eth0,ip=%s/24,bridge=%s,gw=%s", userIP, p.getBridgeName("nat"), p.getInternalGateway())

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
	time.Sleep(p.waitScale(3 * time.Second))
	// 启动容器
	_, err = p.sshClient.Execute(fmt.Sprintf("pct start %d", vmid))
	if err != nil {
		global.APP_LOG.Warn("容器启动失败", zap.Int("vmid", vmid), zap.Error(err))
	}

	// 等待容器启动
	time.Sleep(p.waitScale(5 * time.Second))

	updateProgress(85, "配置容器SSH...")

	// 配置SSH
	p.configureContainerSSH(ctx, vmid)

	return nil
}

// createVM 创建QEMU虚拟机
