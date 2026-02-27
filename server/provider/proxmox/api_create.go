package proxmox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"

	"go.uber.org/zap"
)

// apiCreateContainer 通过API创建LXC容器
func (p *ProxmoxProvider) apiCreateContainer(ctx context.Context, vmid int, config provider.InstanceConfig, updateProgress func(int, string)) error {
	updateProgress(50, "通过API创建LXC容器...")

	// 获取系统镜像配置
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

	// 确保镜像文件存在（通过SSH下载）
	checkCmd := fmt.Sprintf("[ -f %s ] && echo 'exists' || echo 'missing'", localImagePath)
	output, err := p.sshClient.Execute(checkCmd)
	if err != nil {
		return fmt.Errorf("检查镜像文件失败: %v", err)
	}

	if strings.TrimSpace(output) == "missing" {
		updateProgress(30, "下载容器镜像...")
		_, err = p.sshClient.Execute("mkdir -p /var/lib/vz/template/cache")
		if err != nil {
			return fmt.Errorf("创建缓存目录失败: %v", err)
		}

		downloadCmd := fmt.Sprintf("curl -L -o %s %s", localImagePath, systemConfig.ImageURL)
		_, err = p.sshClient.Execute(downloadCmd)
		if err != nil {
			return fmt.Errorf("下载镜像失败: %v", err)
		}
	}

	// 获取存储配置
	var providerRecord providerModel.Provider
	if err := global.APP_DB.Where("name = ?", p.config.Name).First(&providerRecord).Error; err != nil {
		global.APP_LOG.Warn("获取Provider记录失败，使用默认存储", zap.Error(err))
	}

	storage := providerRecord.StoragePool
	if storage == "" {
		storage = "local"
	}

	// 转换参数格式
	cpuFormatted := convertCPUFormat(config.CPU)
	memoryFormatted := convertMemoryFormat(config.Memory)
	diskFormatted := convertDiskFormat(config.Disk)

	// 构造API请求创建容器
	url := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/lxc", p.config.Host, p.node)

	payload := map[string]interface{}{
		"vmid":         vmid,
		"ostemplate":   localImagePath,
		"cores":        cpuFormatted,
		"memory":       memoryFormatted,
		"swap":         "128",
		"rootfs":       fmt.Sprintf("%s:%s", storage, diskFormatted),
		"onboot":       "1",
		"features":     "nesting=1",
		"hostname":     config.Name,
		"unprivileged": "1",
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("序列化请求失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(jsonData)))
	if err != nil {
		return fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	p.setAPIAuth(req)

	resp, err := p.apiClient.Do(req)
	if err != nil {
		return fmt.Errorf("执行API请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var respData map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&respData)
		return fmt.Errorf("创建容器失败: status %d, response: %v", resp.StatusCode, respData)
	}

	updateProgress(70, "配置容器网络...")

	// 解析网络配置获取带宽限制
	networkConfig := p.parseNetworkConfigFromInstanceConfig(config)

	// 配置网络（使用VMID到IP的映射函数，包含带宽限制）
	userIP := VMIDToInternalIP(vmid)
	netConfigURL := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/lxc/%d/config", p.config.Host, p.node, vmid)

	// 构建网络配置字符串，包含 rate 参数
	netConfigStr := fmt.Sprintf("name=eth0,ip=%s/24,bridge=vmbr1,gw=%s", userIP, InternalGateway)
	if networkConfig.OutSpeed > 0 {
		// Proxmox rate 参数单位为 MB/s，配置中的 OutSpeed 单位为 Mbps，需要转换：MB/s = Mbps ÷ 8
		rateMBps := networkConfig.OutSpeed / 8
		if rateMBps < 1 {
			rateMBps = 1 // 最小1MB/s
		}
		netConfigStr = fmt.Sprintf("%s,rate=%d", netConfigStr, rateMBps)
	}

	netPayload := map[string]interface{}{
		"net0": netConfigStr,
	}

	netJsonData, _ := json.Marshal(netPayload)
	netReq, _ := http.NewRequestWithContext(ctx, "PUT", netConfigURL, strings.NewReader(string(netJsonData)))
	netReq.Header.Set("Content-Type", "application/json")
	p.setAPIAuth(netReq)

	netResp, err := p.apiClient.Do(netReq)
	if err != nil {
		global.APP_LOG.Warn("配置容器网络失败", zap.Error(err))
	} else {
		netResp.Body.Close()
	}

	updateProgress(80, "启动容器...")

	global.APP_LOG.Info("通过API成功创建LXC容器",
		zap.Int("vmid", vmid),
		zap.String("name", config.Name))

	return nil
}

// apiCreateVM 通过API创建QEMU虚拟机
func (p *ProxmoxProvider) apiCreateVM(ctx context.Context, vmid int, config provider.InstanceConfig, updateProgress func(int, string)) error {
	updateProgress(50, "通过API创建QEMU虚拟机...")

	// 获取系统镜像配置
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

	// 确保镜像文件存在（通过SSH下载）
	checkCmd := fmt.Sprintf("[ -f %s ] && echo 'exists' || echo 'missing'", localImagePath)
	output, err := p.sshClient.Execute(checkCmd)
	if err != nil {
		return fmt.Errorf("检查镜像文件失败: %v", err)
	}

	if strings.TrimSpace(output) == "missing" {
		updateProgress(30, "下载虚拟机镜像...")
		_, err = p.sshClient.Execute("mkdir -p /root/qcow")
		if err != nil {
			return fmt.Errorf("创建目录失败: %v", err)
		}

		downloadCmd := fmt.Sprintf("curl -L -o %s %s", localImagePath, systemConfig.ImageURL)
		_, err = p.sshClient.Execute(downloadCmd)
		if err != nil {
			return fmt.Errorf("下载镜像失败: %v", err)
		}
	}

	// 检测系统架构和KVM支持
	archCmd := "uname -m"
	archOutput, _ := p.sshClient.Execute(archCmd)
	systemArch := strings.TrimSpace(archOutput)

	kvmFlag := 1
	cpuType := "host"
	kvmCheckCmd := "[ -e /dev/kvm ] && [ -r /dev/kvm ] && [ -w /dev/kvm ] && echo 'kvm_available' || echo 'kvm_unavailable'"
	kvmOutput, _ := p.sshClient.Execute(kvmCheckCmd)
	if strings.TrimSpace(kvmOutput) != "kvm_available" {
		kvmFlag = 0
		switch systemArch {
		case "aarch64", "armv7l", "armv8", "armv8l":
			cpuType = "max"
		case "i386", "i686", "x86":
			cpuType = "qemu32"
		default:
			cpuType = "qemu64"
		}
	}

	// 获取存储配置
	var providerRecord providerModel.Provider
	if err := global.APP_DB.Where("name = ?", p.config.Name).First(&providerRecord).Error; err != nil {
		global.APP_LOG.Warn("获取Provider记录失败，使用默认存储", zap.Error(err))
	}

	storage := providerRecord.StoragePool
	if storage == "" {
		storage = "local"
	}

	// 转换参数格式
	cpuFormatted := convertCPUFormat(config.CPU)
	memoryFormatted := convertMemoryFormat(config.Memory)

	// 获取IPv6配置信息
	ipv6Info, err := p.getIPv6Info(ctx)
	if err != nil {
		global.APP_LOG.Warn("获取IPv6信息失败，使用默认网络配置", zap.Error(err))
		ipv6Info = &IPv6Info{HasAppendedAddresses: false}
	}

	var net1Bridge string
	if ipv6Info.HasAppendedAddresses {
		net1Bridge = "vmbr1"
	} else {
		net1Bridge = "vmbr2"
	}

	// 获取网络配置用于带宽限制
	networkConfig := p.parseNetworkConfigFromInstanceConfig(config)

	// 通过API创建虚拟机
	url := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/qemu", p.config.Host, p.node)

	// 根据 PVE 版本决定是否使用 fstrim_cloned_disks 参数
	agentParam := "1"
	if p.supportsCloneFstrim() {
		agentParam = "1,fstrim_cloned_disks=1"
	}

	// 构建网络配置字符串，包含 rate 参数
	net0Config := "virtio,bridge=vmbr1,firewall=0"
	if networkConfig.OutSpeed > 0 {
		// Proxmox rate 参数单位为 MB/s，配置中的 OutSpeed 单位为 Mbps，需要转换：MB/s = Mbps ÷ 8
		rateMBps := networkConfig.OutSpeed / 8
		if rateMBps < 1 {
			rateMBps = 1 // 最小1MB/s
		}
		net0Config = fmt.Sprintf("%s,rate=%d", net0Config, rateMBps)
	}

	net1Config := fmt.Sprintf("virtio,bridge=%s,firewall=0", net1Bridge)

	payload := map[string]interface{}{
		"vmid":    vmid,
		"name":    config.Name,
		"agent":   agentParam,
		"scsihw":  "virtio-scsi-single",
		"serial0": "socket",
		"cores":   cpuFormatted,
		"sockets": "1",
		"cpu":     cpuType,
		"net0":    net0Config,
		"net1":    net1Config,
		"ostype":  "l26",
		"kvm":     fmt.Sprintf("%d", kvmFlag),
		"memory":  memoryFormatted,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("序列化请求失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(jsonData)))
	if err != nil {
		return fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	p.setAPIAuth(req)

	resp, err := p.apiClient.Do(req)
	if err != nil {
		return fmt.Errorf("执行API请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var respData map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&respData)
		return fmt.Errorf("创建虚拟机失败: status %d, response: %v", resp.StatusCode, respData)
	}

	updateProgress(60, "导入磁盘镜像...")

	// 导入磁盘镜像（需要通过SSH执行，因为没有直接的API）
	if systemArch == "aarch64" || systemArch == "armv7l" || systemArch == "armv8" || systemArch == "armv8l" {
		_, err = p.sshClient.Execute(fmt.Sprintf("qm set %d --bios ovmf", vmid))
		if err != nil {
			global.APP_LOG.Warn("设置ARM BIOS失败", zap.Error(err))
		}
	}

	importCmd := fmt.Sprintf("qm importdisk %d %s %s", vmid, localImagePath, storage)
	_, err = p.sshClient.Execute(importCmd)
	if err != nil {
		return fmt.Errorf("导入磁盘失败: %w", err)
	}

	updateProgress(70, "配置虚拟机磁盘和启动...")

	// 配置磁盘和启动（通过SSH，因为这些配置复杂且没有直接的简单API）
	// 这部分使用SSH来完成剩余的配置
	time.Sleep(3 * time.Second)

	// 查找并设置磁盘
	findDiskCmd := fmt.Sprintf("pvesm list %s | awk -v vmid=\"%d\" '$5 == vmid && $1 ~ /\\.raw$/ {print $1}' | tail -n 1", storage, vmid)
	diskOutput, _ := p.sshClient.Execute(findDiskCmd)
	volid := strings.TrimSpace(diskOutput)

	if volid == "" {
		findDiskCmd = fmt.Sprintf("pvesm list %s | awk -v vmid=\"%d\" '$5 == vmid {print $1}' | tail -n 1", storage, vmid)
		diskOutput, _ = p.sshClient.Execute(findDiskCmd)
		volid = strings.TrimSpace(diskOutput)
	}

	if volid != "" {
		_, _ = p.sshClient.Execute(fmt.Sprintf("qm set %d --scsihw virtio-scsi-pci --scsi0 %s", vmid, volid))
	}

	_, _ = p.sshClient.Execute(fmt.Sprintf("qm set %d --bootdisk scsi0", vmid))
	_, _ = p.sshClient.Execute(fmt.Sprintf("qm set %d --boot order=scsi0", vmid))

	// 配置云初始化
	if systemArch == "aarch64" || systemArch == "armv7l" || systemArch == "armv8" || systemArch == "armv8l" {
		_, _ = p.sshClient.Execute(fmt.Sprintf("qm set %d --scsi1 %s:cloudinit", vmid, storage))
	} else {
		_, _ = p.sshClient.Execute(fmt.Sprintf("qm set %d --ide1 %s:cloudinit", vmid, storage))
	}

	// 调整磁盘大小
	// Proxmox 不支持缩小磁盘，所以需要先检查当前磁盘大小，只在需要扩大时才resize
	diskFormatted := convertDiskFormat(config.Disk)
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
					global.APP_LOG.Debug("磁盘无需调整",
						zap.Int("vmid", vmid),
						zap.Int("current_gb", currentGB),
						zap.Int("target_gb", targetDiskGB))
				}
			}

			if shouldResize {
				resizeCmd := fmt.Sprintf("qm resize %d scsi0 %sG", vmid, diskFormatted)
				_, err := p.sshClient.Execute(resizeCmd)
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

	// 配置IP（使用VMID到IP的映射函数）
	userIP := VMIDToInternalIP(vmid)
	_, _ = p.sshClient.Execute(fmt.Sprintf("qm set %d --ipconfig0 ip=%s/24,gw=%s", vmid, userIP, InternalGateway))

	updateProgress(80, "虚拟机配置完成...")

	// 启动虚拟机（通过API创建后不会自动启动）
	updateProgress(85, "启动虚拟机...")
	startCmd := fmt.Sprintf("qm start %d", vmid)
	_, err = p.sshClient.Execute(startCmd)
	if err != nil {
		global.APP_LOG.Warn("启动虚拟机失败", zap.Int("vmid", vmid), zap.Error(err))
		// 不返回错误，继续流程
	} else {
		updateProgress(90, "等待虚拟机启动...")

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
				global.APP_LOG.Debug("虚拟机已启动",
					zap.Int("vmid", vmid),
					zap.Duration("elapsed", time.Since(startTime)))
				break
			}
			time.Sleep(checkInterval)
		}

		if !vmRunning {
			global.APP_LOG.Warn("虚拟机启动超时",
				zap.Int("vmid", vmid),
				zap.Duration("elapsed", time.Since(startTime)))
		}

		updateProgress(95, "检测Guest Agent...")

		// 智能等待QEMU Guest Agent就绪
		if vmRunning {
			// 先快速检测3次，判断镜像是否支持Guest Agent
			agentSupported := false
			for i := 0; i < 3; i++ {
				agentCmd := fmt.Sprintf("qm agent %d ping 2>/dev/null", vmid)
				_, err := p.sshClient.Execute(agentCmd)
				if err == nil {
					agentSupported = true
					global.APP_LOG.Debug("检测到QEMU Guest Agent已安装并就绪",
						zap.Int("vmid", vmid))
					break
				}
				time.Sleep(2 * time.Second)
			}

			// 如果快速检测失败，进行较短的等待
			if !agentSupported {
				global.APP_LOG.Debug("镜像可能未安装QEMU Guest Agent，进行短时等待...",
					zap.Int("vmid", vmid))

				agentWaitTime := 15 * time.Second
				agentStartTime := time.Now()

				for time.Since(agentStartTime) < agentWaitTime {
					agentCmd := fmt.Sprintf("qm agent %d ping 2>/dev/null", vmid)
					_, err := p.sshClient.Execute(agentCmd)
					if err == nil {
						global.APP_LOG.Debug("QEMU Guest Agent已就绪",
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
	}

	updateProgress(100, "虚拟机创建完成")

	global.APP_LOG.Info("通过API成功创建QEMU虚拟机",
		zap.Int("vmid", vmid),
		zap.String("name", config.Name))

	return nil
}
