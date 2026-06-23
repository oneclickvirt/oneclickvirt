package containerd

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// sshListInstances 列出所有实例
func (c *ContainerdProvider) sshListInstances(ctx context.Context) ([]provider.Instance, error) {
	output, err := c.sshClient.ExecuteWithLogging(cliName+" ps -a --format 'table {{.Names}}\\t{{.Status}}\\t{{.Image}}\\t{{.ID}}\\t{{.CreatedAt}}'", "CONTAINERD_LIST")
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) <= 1 {
		return []provider.Instance{}, nil
	}

	var instances []provider.Instance
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		status := "unknown"
		statusField := strings.ToLower(fields[1])
		if strings.Contains(statusField, "up") {
			status = "running"
		} else if strings.Contains(statusField, "exited") {
			status = "stopped"
		}

		instance := provider.Instance{
			ID:     fields[3],
			Name:   fields[0],
			Status: status,
			Image:  fields[2],
		}
		instances = append(instances, instance)
	}

	c.enrichInstancesWithNetworkInfo(&instances)

	global.APP_LOG.Info("获取Containerd容器实例列表成功", zap.Int("count", len(instances)))
	return instances, nil
}

// enrichInstancesWithNetworkInfo 补充获取实例的网络信息
func (c *ContainerdProvider) enrichInstancesWithNetworkInfo(instances *[]provider.Instance) {
	for idx := range *instances {
		instance := &(*instances)[idx]
		if instance.Status != "running" {
			continue
		}

		cmd := fmt.Sprintf("%s inspect %s --format '{{range $net, $config := .NetworkSettings.Networks}}{{$config.IPAddress}}{{end}}'", cliName, shellSingleQuote(instance.Name))
		output, err := c.sshClient.Execute(cmd)
		if err == nil {
			ipAddress := utils.CleanCommandOutput(output)
			if ipAddress != "" && ipAddress != "<no value>" {
				instance.PrivateIP = ipAddress
				instance.IP = ipAddress
			}
		}

		vethCmd := fmt.Sprintf(`
CONTAINER_NAME=%s
CONTAINER_PID=$(%s inspect -f '{{.State.Pid}}' "$CONTAINER_NAME" 2>/dev/null)
if [ -z "$CONTAINER_PID" ] || [ "$CONTAINER_PID" = "0" ]; then
    exit 1
fi
HOST_VETH_IFINDEX=$(nsenter -t $CONTAINER_PID -n ip link show eth0 2>/dev/null | head -n1 | sed -n 's/.*@if\([0-9]\+\).*/\1/p')
if [ -z "$HOST_VETH_IFINDEX" ]; then
    exit 1
fi
VETH_NAME=$(ip -o link show 2>/dev/null | awk -v idx="$HOST_VETH_IFINDEX" -F': ' '$1 == idx {print $2}' | cut -d'@' -f1)
if [ -n "$VETH_NAME" ]; then
    echo "$VETH_NAME"
fi
`, shellSingleQuote(instance.Name), cliName)
		vethOutput, err := c.sshClient.Execute(vethCmd)
		if err == nil {
			vethInterface := utils.CleanCommandOutput(vethOutput)
			if vethInterface != "" {
				if instance.Metadata == nil {
					instance.Metadata = make(map[string]string)
				}
				instance.Metadata["network_interface"] = vethInterface
			}
		}

		if instance.PrivateIP == "" {
			fallbackCmd := fmt.Sprintf("%s inspect %s --format '{{.NetworkSettings.IPAddress}}'", cliName, shellSingleQuote(instance.Name))
			fallbackOutput, fallbackErr := c.sshClient.Execute(fallbackCmd)
			if fallbackErr == nil {
				ipAddress := strings.TrimSpace(fallbackOutput)
				if ipAddress != "" && ipAddress != "<no value>" {
					instance.PrivateIP = ipAddress
					instance.IP = ipAddress
				}
			}
		}

		checkIPv6Cmd := fmt.Sprintf("%s inspect %s --format '{{range $net, $config := .NetworkSettings.Networks}}{{$net}}{{println}}{{end}}'", cliName, shellSingleQuote(instance.Name))
		networksOutput, err := c.sshClient.Execute(checkIPv6Cmd)
		if err == nil && strings.Contains(networksOutput, ipv6Network) {
			cmd = fmt.Sprintf("%s inspect %s --format '{{range $net, $config := .NetworkSettings.Networks}}{{if $config.GlobalIPv6Address}}{{$config.GlobalIPv6Address}}{{end}}{{end}}'", cliName, shellSingleQuote(instance.Name))
			output, err = c.sshClient.Execute(cmd)
			if err == nil {
				ipv6Address := strings.TrimSpace(output)
				if ipv6Address != "" && ipv6Address != "<no value>" {
					instance.IPv6Address = ipv6Address
				}
			}
		}
	}
}

// sshCreateInstance 创建实例
func (c *ContainerdProvider) sshCreateInstance(ctx context.Context, config provider.InstanceConfig) error {
	return c.sshCreateInstanceWithProgress(ctx, config, nil)
}

// sshCreateInstanceWithProgress 创建实例并报告进度
func (c *ContainerdProvider) sshCreateInstanceWithProgress(ctx context.Context, config provider.InstanceConfig, progressCallback provider.ProgressCallback) error {
	updateProgress := func(percentage int, message string) {
		if progressCallback != nil {
			progressCallback(percentage, message)
		}
		global.APP_LOG.Debug("Containerd实例创建进度",
			zap.String("instance", config.Name),
			zap.Int("percentage", percentage),
			zap.String("message", message))
	}

	updateProgress(10, "开始创建Containerd实例...")

	// 预检：确保 Containerd CLI 可用，避免后续命令以 127 失败且错误不明确
	if _, err := c.sshClient.Execute(fmt.Sprintf("command -v %s >/dev/null 2>&1", cliName)); err != nil {
		return fmt.Errorf("%s 命令不可用，请确认 provider 节点已安装并在 PATH 中: %w", cliName, err)
	}

	// 确保SSH脚本文件可用（非致命错误，SSH脚本仅用于后续密码配置）
	updateProgress(15, "确保SSH脚本可用...")
	if err := c.ensureSSHScriptsAvailable(c.config.Country); err != nil {
		global.APP_LOG.Warn("确保SSH脚本可用失败，但继续创建实例",
			zap.String("name", utils.TruncateString(config.Name, 32)),
			zap.Error(err))
	}

	updateProgress(20, "处理Containerd镜像...")
	imageNameWithPrefix := "oneclickvirt_" + config.Image
	// 标记是否使用了 registry 回退拉取（原始镜像无持久进程，需附加 keep-alive 命令）
	registryFallback := false

	if config.CopyMode && config.CopySourceName != "" {
		if !utils.IsValidContainerRuntimeName(config.CopySourceName) {
			return fmt.Errorf("源容器名称格式无效: %s", config.CopySourceName)
		}
		updateProgress(25, "从源容器创建临时镜像...")
		if _, err := c.sshClient.Execute(fmt.Sprintf("%s inspect %s >/dev/null 2>&1", cliName, shellSingleQuote(config.CopySourceName))); err != nil {
			return fmt.Errorf("源容器 %s 不存在或不可访问: %w", config.CopySourceName, err)
		}
		copyImageName := "oneclickvirt_copy_" + strings.ToLower(strings.NewReplacer("_", "-", ".", "-", "/", "-").Replace(config.Name))
		commitCmd := fmt.Sprintf("%s commit %s %s", cliName, shellSingleQuote(config.CopySourceName), shellSingleQuote(copyImageName))
		if out, err := c.sshClient.ExecuteWithTimeout(commitCmd, 10*time.Minute); err != nil {
			return fmt.Errorf("从源容器创建临时镜像失败: %w; output: %s", err, utils.TruncateString(out, 300))
		}
		imageNameWithPrefix = copyImageName
		defer c.sshClient.Execute(fmt.Sprintf("%s rmi -f %s >/dev/null 2>&1 || true", cliName, shellSingleQuote(copyImageName)))
	} else {
		imageExistsResult := c.imageExists(imageNameWithPrefix)
		if !imageExistsResult {
			if config.ImageURL != "" {
				imageURL := config.ImageURL
				imageName := config.Image
				useCDN := config.UseCDN
				_, sfErr, _ := c.imageImportGroup.Do(imageNameWithPrefix, func() (interface{}, error) {
					if c.imageExists(imageNameWithPrefix) {
						return nil, nil
					}

					updateProgress(30, "下载镜像到远程服务器...")
					remotePath, err := c.downloadImageToRemote(imageURL, imageName, c.config.Country, c.config.Architecture, useCDN)
					if err != nil {
						return nil, fmt.Errorf("下载镜像失败: %w", err)
					}

					updateProgress(50, "加载镜像到Containerd...")
					if err := c.loadImageToContainerd(remotePath, imageNameWithPrefix); err != nil {
						global.APP_LOG.Warn("Containerd镜像加载失败，尝试重新下载",
							zap.String("image", utils.TruncateString(imageNameWithPrefix, 64)),
							zap.Error(err))

						c.cleanupRemoteImage(imageName, imageURL, c.config.Architecture)
						c.cleanupContainerdImage(imageNameWithPrefix)

						updateProgress(40, "重新下载镜像...")
						remotePath, err = c.downloadImageToRemote(imageURL, imageName, c.config.Country, c.config.Architecture, useCDN)
						if err != nil {
							return nil, fmt.Errorf("重新下载镜像失败: %w", err)
						}

						updateProgress(55, "重新加载镜像到Containerd...")
						if err := c.loadImageToContainerd(remotePath, imageNameWithPrefix); err != nil {
							return nil, fmt.Errorf("重新加载镜像失败: %w", err)
						}
					}

					updateProgress(60, "清理临时文件...")
					c.cleanupRemoteImage(imageName, imageURL, c.config.Architecture)
					return nil, nil
				})
				if sfErr != nil {
					return sfErr
				}
			} else {
				// 镜像不存在且没有下载URL，尝试从 registry 拉取原始镜像并打标
				updateProgress(25, "从 registry 拉取原始镜像作为回退...")
				global.APP_LOG.Info("Containerd镜像不存在且无下载URL，尝试从 registry 拉取原始镜像",
					zap.String("rawImage", utils.TruncateString(config.Image, 64)),
					zap.String("targetImage", utils.TruncateString(imageNameWithPrefix, 64)))

				pullErr := c.sshPullImage(ctx, config.Image)
				if pullErr != nil {
					global.APP_LOG.Error("从 registry 拉取镜像也失败",
						zap.String("rawImage", utils.TruncateString(config.Image, 64)),
						zap.Error(pullErr))
					return fmt.Errorf("镜像 %s 不存在，且没有提供下载URL；从 registry 拉取也失败: %w", imageNameWithPrefix, pullErr)
				}

				tagCmd := fmt.Sprintf("%s tag %s %s", cliName, shellSingleQuote(config.Image), shellSingleQuote(containerdRuntimeImageRef(imageNameWithPrefix)))
				if out, tagErr := c.sshClient.Execute(tagCmd); tagErr != nil {
					global.APP_LOG.Error("Containerd镜像打标失败",
						zap.String("rawImage", utils.TruncateString(config.Image, 64)),
						zap.String("targetImage", utils.TruncateString(imageNameWithPrefix, 64)),
						zap.String("output", utils.TruncateString(out, 500)),
						zap.Error(tagErr))
					return fmt.Errorf("registry镜像打标失败: %w; output: %s", tagErr, out)
				}
				registryFallback = true
				updateProgress(55, "原始镜像拉取并打标完成")
			}
		} else {
			updateProgress(60, "Containerd镜像已存在，跳过下载...")
		}
	}

	if err := c.ensureContainerdRuntimeImageRef(imageNameWithPrefix); err != nil {
		return fmt.Errorf("Containerd镜像运行引用不可用: %w", err)
	}

	updateProgress(70, "清理同名残留容器...")
	cleanupCmd := fmt.Sprintf("%s ps -a --filter %s -q | xargs -r %s rm -f", cliName, containerNameFilter(config.Name), cliName)
	c.sshClient.Execute(cleanupCmd)

	updateProgress(72, "构建nerdctl run命令...")
	cmd := fmt.Sprintf("%s run -d --name %s", cliName, shellSingleQuote(config.Name))

	networkType := c.config.NetworkType
	if config.Metadata != nil {
		if metaNetworkType, ok := config.Metadata["network_type"]; ok {
			networkType = metaNetworkType
		}
	}

	hasIPv6 := networkType == "nat_ipv4_ipv6" || networkType == "dedicated_ipv4_ipv6" || networkType == "ipv6_only"
	if hasIPv6 && c.checkIPv6NetworkAvailable() {
		cmd += fmt.Sprintf(" --network=%s", shellSingleQuote(ipv6Network))
	} else {
		cmd += fmt.Sprintf(" --network=%s", shellSingleQuote(ipv4Network))
	}

	if networkType == "dedicated_ipv4" || networkType == "dedicated_ipv4_ipv6" {
		if config.Metadata != nil {
			if staticIPv4, ok := config.Metadata["static_ipv4"]; ok && staticIPv4 != "" {
				if err := c.ensureIPv4OnHostInterface(staticIPv4); err != nil {
					global.APP_LOG.Warn("独立IPv4宿主机接口绑定检查失败，继续执行",
						zap.String("instance", config.Name),
						zap.String("ipv4", staticIPv4),
						zap.Error(err))
				}
			}
		}
	}

	if config.CPU != "" {
		cmd += fmt.Sprintf(" --cpus=%s", config.CPU)
	}

	if config.Memory != "" {
		cmd += fmt.Sprintf(" --memory=%s", config.Memory)
	}

	updateProgress(75, "配置存储限制...")
	if config.Disk != "" && config.Disk != "0" {
		supportsDiskLimit, storageDriver, err := c.checkStorageDriver()
		if err != nil {
			global.APP_LOG.Warn("检查存储驱动失败，跳过硬盘大小限制",
				zap.String("name", utils.TruncateString(config.Name, 32)),
				zap.Error(err))
		} else if supportsDiskLimit {
			diskSize := strings.ToLower(config.Disk)
			var finalDiskSize string
			if strings.HasSuffix(diskSize, "mb") || strings.HasSuffix(diskSize, "m") {
				mbValue := strings.TrimSuffix(strings.TrimSuffix(diskSize, "mb"), "m")
				if mb, err := strconv.Atoi(mbValue); err == nil {
					gb := (mb + 1023) / 1024
					if gb < 1 {
						gb = 1
					}
					finalDiskSize = fmt.Sprintf("%dG", gb)
				} else {
					finalDiskSize = "1G"
				}
			} else if strings.HasSuffix(diskSize, "gb") || strings.HasSuffix(diskSize, "g") {
				finalDiskSize = config.Disk
				if !strings.HasSuffix(diskSize, "g") {
					finalDiskSize = strings.TrimSuffix(config.Disk, "b")
				}
			} else {
				if mb, err := strconv.Atoi(config.Disk); err == nil {
					gb := (mb + 1023) / 1024
					if gb < 1 {
						gb = 1
					}
					finalDiskSize = fmt.Sprintf("%dG", gb)
				} else {
					finalDiskSize = "1G"
				}
			}
			cmd += fmt.Sprintf(" --storage-opt size=%s", finalDiskSize)
			global.APP_LOG.Debug("已启用硬盘大小限制",
				zap.String("name", utils.TruncateString(config.Name, 32)),
				zap.String("storage_driver", storageDriver))
		}
	}

	updateProgress(80, "配置端口映射...")
	for _, port := range config.Ports {
		portMapping := port
		if strings.HasPrefix(portMapping, "0.0.0.0:") {
			if strings.HasSuffix(portMapping, "/both") {
				baseMapping := strings.TrimSuffix(portMapping, "/both")
				cmd += fmt.Sprintf(" -p %s", shellSingleQuote(baseMapping+"/tcp"))
				cmd += fmt.Sprintf(" -p %s", shellSingleQuote(baseMapping+"/udp"))
			} else {
				cmd += fmt.Sprintf(" -p %s", shellSingleQuote(portMapping))
			}
		} else if strings.Contains(portMapping, ":") {
			protocol := ""
			baseMapping := portMapping
			if strings.Contains(portMapping, "/") {
				parts := strings.Split(portMapping, "/")
				baseMapping = parts[0]
				if len(parts) > 1 {
					protocol = "/" + parts[1]
				}
			}
			portParts := strings.Split(baseMapping, ":")
			if len(portParts) >= 2 {
				hostPort := portParts[len(portParts)-2]
				guestPort := portParts[len(portParts)-1]
				if protocol == "/both" {
					cmd += fmt.Sprintf(" -p %s", shellSingleQuote(fmt.Sprintf("0.0.0.0:%s:%s/tcp", hostPort, guestPort)))
					cmd += fmt.Sprintf(" -p %s", shellSingleQuote(fmt.Sprintf("0.0.0.0:%s:%s/udp", hostPort, guestPort)))
				} else {
					cmd += fmt.Sprintf(" -p %s", shellSingleQuote(fmt.Sprintf("0.0.0.0:%s:%s%s", hostPort, guestPort, protocol)))
				}
			}
		} else {
			cmd += fmt.Sprintf(" -p %s", shellSingleQuote(fmt.Sprintf("0.0.0.0:%s:%s", portMapping, portMapping)))
		}
	}

	updateProgress(85, "配置LXCFS卷挂载...")
	lxcfsAvailable, lxcfsVolumes, lxcfsReason, err := c.checkLXCFS()
	if err != nil {
		global.APP_LOG.Warn("检查LXCFS状态失败",
			zap.String("name", utils.TruncateString(config.Name, 32)),
			zap.Error(err))
	} else if lxcfsAvailable && len(lxcfsVolumes) > 0 {
		for _, volume := range lxcfsVolumes {
			cmd += " " + volume
		}
		global.APP_LOG.Debug("已启用LXCFS卷挂载",
			zap.String("name", utils.TruncateString(config.Name, 32)),
			zap.String("reason", lxcfsReason))
	}

	updateProgress(90, "配置容器能力和环境变量...")
	gpuOptStr := ""
	if config.GpuEnabled {
		if strings.TrimSpace(config.GpuDeviceIds) != "" {
			gpuOptStr = fmt.Sprintf(" --gpus %s", shellSingleQuote("device="+strings.TrimSpace(config.GpuDeviceIds)))
		} else {
			gpuOptStr = " --gpus all"
		}
		cmd += gpuOptStr
	}
	// Containerd(nerdctl)仅需基本能力，不需要NET_ADMIN/NET_RAW
	cmd += " --cap-add=MKNOD"

	for key, value := range config.Env {
		cmd += fmt.Sprintf(" -e %s", shellSingleQuote(key+"="+value))
	}

	// --pull=never: 确保使用本地已加载的镜像，不尝试远程拉取
	runImageRef := containerdRuntimeImageRef(imageNameWithPrefix)
	cmd += fmt.Sprintf(" --pull=never %s", shellSingleQuote(runImageRef))
	global.APP_LOG.Debug("Containerd使用本地镜像引用创建容器",
		zap.String("name", utils.TruncateString(config.Name, 32)),
		zap.String("image", utils.TruncateString(imageNameWithPrefix, 64)),
		zap.String("runImageRef", utils.TruncateString(runImageRef, 80)))

	// 若使用 registry 回退拉取的原始镜像（无持久进程），追加 keep-alive 命令
	if registryFallback {
		cmd += " sh -c 'trap : TERM INT; tail -f /dev/null & wait'"
		global.APP_LOG.Debug("使用 registry 回退镜像，附加 keep-alive 命令",
			zap.String("name", utils.TruncateString(config.Name, 32)))
	}

	updateProgress(95, "执行Containerd创建命令...")
	global.APP_LOG.Debug("开始执行Containerd创建命令",
		zap.String("name", utils.TruncateString(config.Name, 32)))

	effectiveCmd := cmd
	output, err := c.sshClient.Execute(effectiveCmd)
	if err != nil && gpuOptStr != "" {
		global.APP_LOG.Warn("Containerd GPU参数创建失败，自动回退为无GPU创建",
			zap.String("name", utils.TruncateString(config.Name, 32)),
			zap.String("output", utils.TruncateString(output, 300)),
			zap.Error(err))
		_, _ = c.sshClient.Execute(fmt.Sprintf("%s rm -f %s 2>/dev/null || true", cliName, shellSingleQuote(config.Name)))
		effectiveCmd = strings.Replace(cmd, gpuOptStr, "", 1)
		output, err = c.sshClient.Execute(effectiveCmd)
	}
	if err != nil {
		global.APP_LOG.Error("Containerd创建容器失败",
			zap.String("name", utils.TruncateString(config.Name, 32)),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		if strings.TrimSpace(output) != "" {
			return fmt.Errorf("failed to create container: %w; output: %s", err, output)
		}
		return fmt.Errorf("failed to create container: %w", err)
	}

	updateProgress(96, "等待容器完全启动...")
	maxWaitTime := 30 * time.Second
	checkInterval := 6 * time.Second
	startTime := time.Now()
	isRunning := false

	for {
		if time.Since(startTime) > maxWaitTime {
			global.APP_LOG.Warn("等待容器启动超时，但继续执行",
				zap.String("name", utils.TruncateString(config.Name, 32)))
			break
		}
		time.Sleep(checkInterval)
		statusOutput, err := c.sshClient.Execute(fmt.Sprintf("%s inspect %s --format '{{.State.Status}}'", cliName, shellSingleQuote(config.Name)))
		if err == nil {
			status := strings.ToLower(strings.TrimSpace(statusOutput))
			if status == "running" {
				isRunning = true
				break
			}
		}
	}

	if !isRunning {
		global.APP_LOG.Warn("无法确认容器运行状态，继续执行后续操作",
			zap.String("name", utils.TruncateString(config.Name, 32)))
	}

	// 确保iptables路由规则存在
	c.ensureContainerNetworkRouting()

	updateProgress(97, "配置SSH密码...")
	if err := c.configureInstanceSSHPassword(ctx, config); err != nil {
		global.APP_LOG.Warn("配置SSH密码失败", zap.Error(err))
	}

	updateProgress(97, "获取实例内网IP...")
	if privateIP, err := c.getContainerPrivateIP(config.Name); err == nil && privateIP != "" {
		var instance providerModel.Instance
		if err := global.APP_DB.Where("name = ? AND provider_id = ?", config.Name, c.config.ID).First(&instance).Error; err == nil {
			global.APP_DB.Model(&instance).Update("private_ip", privateIP)
		}
	}

	updateProgress(98, "初始化流量监控...")
	if err := c.initializePmacctMonitoring(ctx, config); err != nil {
		global.APP_LOG.Warn("初始化流量监控失败", zap.Error(err))
	}

	updateProgress(100, "Containerd实例创建完成")
	global.APP_LOG.Info("Containerd容器实例创建成功", zap.String("name", utils.TruncateString(config.Name, 32)))
	return nil
}

// ensureContainerNetworkRouting 确保宿主机上的iptables路由规则存在
func (c *ContainerdProvider) ensureContainerNetworkRouting() {
	rules := []string{
		fmt.Sprintf("iptables -t nat -C POSTROUTING -s %s ! -d %s -j MASQUERADE 2>/dev/null || iptables -t nat -A POSTROUTING -s %s ! -d %s -j MASQUERADE", ipv4Subnet, ipv4Subnet, ipv4Subnet, ipv4Subnet),
		fmt.Sprintf("iptables -C FORWARD -s %s -j ACCEPT 2>/dev/null || iptables -A FORWARD -s %s -j ACCEPT", ipv4Subnet, ipv4Subnet),
		fmt.Sprintf("iptables -C FORWARD -d %s -j ACCEPT 2>/dev/null || iptables -A FORWARD -d %s -j ACCEPT", ipv4Subnet, ipv4Subnet),
	}
	for _, rule := range rules {
		if _, err := c.sshClient.Execute(rule); err != nil {
			global.APP_LOG.Warn("iptables路由规则设置失败（非致命）",
				zap.String("subnet", ipv4Subnet),
				zap.Error(err))
		}
	}
}
