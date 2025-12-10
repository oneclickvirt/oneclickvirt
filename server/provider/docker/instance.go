package docker

import (
	"context"
	"fmt"
	"path/filepath"
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

// sshListInstances 列出所有实例
func (d *DockerProvider) sshListInstances(ctx context.Context) ([]provider.Instance, error) {
	output, err := d.sshClient.ExecuteWithLogging("docker ps -a --format 'table {{.Names}}\\t{{.Status}}\\t{{.Image}}\\t{{.ID}}\\t{{.CreatedAt}}'", "DOCKER_LIST")
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

	// 获取每个实例的网络信息（IP地址和网络接口）
	d.enrichInstancesWithNetworkInfo(&instances)

	global.APP_LOG.Info("获取Docker实例列表成功", zap.Int("count", len(instances)))
	return instances, nil
}

// enrichInstancesWithNetworkInfo 补充获取实例的网络信息（IP地址和网络接口）
func (d *DockerProvider) enrichInstancesWithNetworkInfo(instances *[]provider.Instance) {
	for idx := range *instances {
		instance := &(*instances)[idx]
		// 只处理正在运行的实例
		if instance.Status != "running" {
			continue
		}

		// 1. 获取容器的内网IP地址
		cmd := fmt.Sprintf("docker inspect %s --format '{{range $net, $config := .NetworkSettings.Networks}}{{$config.IPAddress}}{{end}}'", instance.Name)
		output, err := d.sshClient.Execute(cmd)
		if err == nil {
			ipAddress := strings.TrimSpace(output)
			if ipAddress != "" && ipAddress != "<no value>" {
				instance.PrivateIP = ipAddress
				instance.IP = ipAddress // 保持向后兼容
				global.APP_LOG.Debug("获取到Docker实例内网IP地址",
					zap.String("instance", instance.Name),
					zap.String("privateIP", ipAddress))
			}
		}

		// 2. 获取容器对应的宿主机veth接口
		vethCmd := fmt.Sprintf(`
CONTAINER_NAME='%s'
CONTAINER_PID=$(docker inspect -f '{{.State.Pid}}' "$CONTAINER_NAME" 2>/dev/null)
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
`, instance.Name)

		vethOutput, err := d.sshClient.Execute(vethCmd)
		if err == nil {
			vethInterface := strings.TrimSpace(vethOutput)
			if vethInterface != "" {
				if instance.Metadata == nil {
					instance.Metadata = make(map[string]string)
				}
				instance.Metadata["network_interface"] = vethInterface
				global.APP_LOG.Debug("获取到Docker实例veth接口",
					zap.String("instance", instance.Name),
					zap.String("veth", vethInterface))
			}
		}

		// 如果没有获取到PrivateIP，尝试使用旧方法获取
		if instance.PrivateIP == "" {
			cmd := fmt.Sprintf("docker inspect %s --format '{{.NetworkSettings.IPAddress}}'", instance.Name)
			output, err := d.sshClient.Execute(cmd)
			if err == nil {
				ipAddress := strings.TrimSpace(output)
				if ipAddress != "" && ipAddress != "<no value>" {
					instance.PrivateIP = ipAddress
					instance.IP = ipAddress
					global.APP_LOG.Debug("通过默认网络获取到Docker实例IP地址",
						zap.String("instance", instance.Name),
						zap.String("privateIP", ipAddress))
				}
			}
		}

		// 3. 检查容器是否连接到ipv6_net网络，如果是则获取IPv6地址
		checkIPv6Cmd := fmt.Sprintf("docker inspect %s --format '{{range $net, $config := .NetworkSettings.Networks}}{{$net}}{{println}}{{end}}'", instance.Name)
		networksOutput, err := d.sshClient.Execute(checkIPv6Cmd)
		if err == nil && strings.Contains(networksOutput, "ipv6_net") {
			// 容器连接到了ipv6_net，获取IPv6地址
			cmd = fmt.Sprintf("docker inspect %s --format '{{range $net, $config := .NetworkSettings.Networks}}{{if $config.GlobalIPv6Address}}{{$config.GlobalIPv6Address}}{{end}}{{end}}'", instance.Name)
			output, err = d.sshClient.Execute(cmd)
			if err == nil {
				ipv6Address := strings.TrimSpace(output)
				if ipv6Address != "" && ipv6Address != "<no value>" {
					instance.IPv6Address = ipv6Address
					global.APP_LOG.Debug("获取到Docker实例IPv6地址",
						zap.String("instance", instance.Name),
						zap.String("ipv6", ipv6Address))
				}
			}
		}
	}
}

// sshCreateInstance 创建实例
func (d *DockerProvider) sshCreateInstance(ctx context.Context, config provider.InstanceConfig) error {
	return d.sshCreateInstanceWithProgress(ctx, config, nil)
}

// sshCreateInstanceWithProgress 创建实例并报告进度
func (d *DockerProvider) sshCreateInstanceWithProgress(ctx context.Context, config provider.InstanceConfig, progressCallback provider.ProgressCallback) error {
	// 进度更新辅助函数
	updateProgress := func(percentage int, message string) {
		if progressCallback != nil {
			progressCallback(percentage, message)
		}
		global.APP_LOG.Info("Docker实例创建进度",
			zap.String("instance", config.Name),
			zap.Int("percentage", percentage),
			zap.String("message", message))
	}

	updateProgress(10, "开始创建Docker实例...")

	global.APP_LOG.Debug("开始创建Docker实例",
		zap.String("instance", config.Name),
		zap.String("image", config.Image),
		zap.String("providerHost", d.config.Host))

	// 确保SSH脚本文件可用
	updateProgress(15, "确保SSH脚本可用...")
	global.APP_LOG.Debug("准备调用ensureSSHScriptsAvailable",
		zap.String("instance", config.Name),
		zap.String("country", d.config.Country))

	if err := d.ensureSSHScriptsAvailable(d.config.Country); err != nil {
		global.APP_LOG.Error("确保SSH脚本可用失败",
			zap.String("name", utils.TruncateString(config.Name, 32)),
			zap.Error(err))
		return fmt.Errorf("确保SSH脚本可用失败: %w", err)
	}

	global.APP_LOG.Debug("ensureSSHScriptsAvailable成功返回",
		zap.String("instance", config.Name))

	updateProgress(20, "处理Docker镜像...")
	// 为镜像名称添加前缀
	imageNameWithPrefix := "oneclickvirt_" + config.Image

	global.APP_LOG.Debug("准备检查镜像是否存在",
		zap.String("instance", config.Name),
		zap.String("imageNameWithPrefix", imageNameWithPrefix))

	// 首先检查镜像是否存在
	imageExistsResult := d.imageExists(imageNameWithPrefix)
	global.APP_LOG.Debug("imageExists调用完成",
		zap.String("instance", config.Name),
		zap.String("imageNameWithPrefix", imageNameWithPrefix),
		zap.Bool("exists", imageExistsResult))

	if !imageExistsResult {
		// 如果镜像不存在且有镜像URL，先在远程服务器下载镜像
		if config.ImageURL != "" {
			updateProgress(30, "下载镜像到远程服务器...")
			// 在远程服务器上下载镜像
			remotePath, err := d.downloadImageToRemote(config.ImageURL, config.Image, d.config.Country, d.config.Architecture, config.UseCDN)
			if err != nil {
				return fmt.Errorf("下载镜像失败: %w", err)
			}

			updateProgress(50, "加载镜像到Docker...")
			// 在远程服务器上加载镜像到Docker
			if err := d.loadImageToDocker(remotePath, imageNameWithPrefix); err != nil {
				// 加载失败，清理下载的文件并重试
				global.APP_LOG.Warn("Docker镜像加载失败，尝试重新下载",
					zap.String("image", utils.TruncateString(imageNameWithPrefix, 64)),
					zap.Error(err))

				// 清理损坏的镜像文件和Docker镜像
				d.cleanupRemoteImage(config.Image, config.ImageURL, d.config.Architecture)
				d.cleanupDockerImage(imageNameWithPrefix)

				updateProgress(40, "重新下载镜像...")
				// 重新下载
				remotePath, err = d.downloadImageToRemote(config.ImageURL, config.Image, d.config.Country, d.config.Architecture, config.UseCDN)
				if err != nil {
					return fmt.Errorf("重新下载镜像失败: %w", err)
				}

				updateProgress(55, "重新加载镜像到Docker...")
				// 重新加载
				if err := d.loadImageToDocker(remotePath, imageNameWithPrefix); err != nil {
					return fmt.Errorf("重新加载镜像失败: %w", err)
				}
			}

			updateProgress(60, "清理临时文件...")
			// 导入成功后删除文件
			d.cleanupRemoteImage(config.Image, config.ImageURL, d.config.Architecture)
		} else {
			// 镜像不存在且没有URL，返回错误
			global.APP_LOG.Error("Docker镜像不存在且没有下载URL",
				zap.String("image", utils.TruncateString(imageNameWithPrefix, 64)))
			return fmt.Errorf("镜像 %s 不存在，且没有提供下载URL", imageNameWithPrefix)
		}
	} else {
		updateProgress(60, "Docker镜像已存在，跳过下载...")
		global.APP_LOG.Info("Docker镜像已存在，跳过下载",
			zap.String("image", utils.TruncateString(imageNameWithPrefix, 64)))
	}

	updateProgress(70, "清理同名残留容器...")
	// 预先清理任何同名的残留容器（包括停止、失败或创建失败的容器）
	// 这可以避免端口冲突和容器名称冲突
	cleanupCmd := fmt.Sprintf("docker ps -a --filter name=^%s$ -q | xargs -r docker rm -f", config.Name)
	global.APP_LOG.Debug("创建前清理同名容器",
		zap.String("instance", utils.TruncateString(config.Name, 32)),
		zap.String("command", cleanupCmd))

	cleanupOutput, cleanupErr := d.sshClient.Execute(cleanupCmd)
	if cleanupErr != nil {
		global.APP_LOG.Debug("清理同名容器失败（可忽略）",
			zap.String("instance", utils.TruncateString(config.Name, 32)),
			zap.String("output", utils.TruncateString(cleanupOutput, 200)),
			zap.Error(cleanupErr))
	} else if cleanupOutput != "" {
		global.APP_LOG.Info("已清理同名残留容器",
			zap.String("instance", utils.TruncateString(config.Name, 32)),
			zap.String("cleanedContainers", utils.TruncateString(cleanupOutput, 200)))
	}

	updateProgress(72, "构建Docker run命令...")
	// 构建docker run命令
	cmd := fmt.Sprintf("docker run -d --name %s", config.Name)

	// 检查是否启用IPv6网络（支持标准的网络类型值）
	networkType := d.config.NetworkType
	// 优先从实例Metadata中读取网络类型配置
	if config.Metadata != nil {
		if metaNetworkType, ok := config.Metadata["network_type"]; ok {
			networkType = metaNetworkType
			global.APP_LOG.Info("使用实例级别的网络类型配置",
				zap.String("instance", config.Name),
				zap.String("networkType", networkType))
		}
	}

	hasIPv6 := networkType == "nat_ipv4_ipv6" || networkType == "dedicated_ipv4_ipv6" || networkType == "ipv6_only"
	if hasIPv6 && d.checkIPv6NetworkAvailable() {
		cmd += " --network=ipv6_net"
		global.APP_LOG.Info("启用IPv6网络",
			zap.String("name", utils.TruncateString(config.Name, 32)),
			zap.String("provider", d.config.Name))
	} else {
		if hasIPv6 {
			global.APP_LOG.Warn("Provider配置启用IPv6但ipv6_net网络不可用",
				zap.String("name", utils.TruncateString(config.Name, 32)),
				zap.String("provider", d.config.Name))
		}
	}

	// 始终应用CPU限制参数（资源限制配置只影响Provider层面的资源预算计算）
	if config.CPU != "" {
		cmd += fmt.Sprintf(" --cpus=%s", config.CPU)
	}

	// 始终应用内存限制参数（资源限制配置只影响Provider层面的资源预算计算）
	if config.Memory != "" {
		// Docker --memory parameter supports the following units (as per official documentation):
		// - b, k, m, g (with optional 'B' suffix): 1024-based binary units
		// - Examples: 512m, 1g, 2048m, 1GB, 1024MB
		// Reference: https://docs.docker.com/config/containers/resource_constraints/#limit-a-containers-access-to-memory
		// Note: Docker accepts both binary and decimal units, but typically uses 1024-based calculations
		cmd += fmt.Sprintf(" --memory=%s", config.Memory)
	}

	updateProgress(75, "配置存储限制...")
	// 始终检查并应用硬盘限制（资源限制配置只影响Provider层面的资源预算计算）
	if config.Disk != "" && config.Disk != "0" {
		// 检查存储驱动是否支持硬盘大小限制
		supportsDiskLimit, storageDriver, err := d.checkStorageDriver()
		if err != nil {
			global.APP_LOG.Warn("检查存储驱动失败，跳过硬盘大小限制",
				zap.String("name", utils.TruncateString(config.Name, 32)),
				zap.String("disk", config.Disk),
				zap.Error(err))
		} else if supportsDiskLimit {
			// 处理磁盘大小单位转换
			// config.Disk格式可能是："1024MB", "2GB", "512" 等
			diskSize := strings.ToLower(config.Disk)
			var finalDiskSize string

			if strings.HasSuffix(diskSize, "mb") {
				// 如果是MB单位，需要转换为GB（Docker storage-opt一般使用GB）
				mbValue := strings.TrimSuffix(diskSize, "mb")
				if mb, err := strconv.Atoi(mbValue); err == nil {
					// 转换MB到GB，向上取整
					gb := (mb + 1023) / 1024 // 向上取整
					if gb < 1 {
						gb = 1 // 最小1GB
					}
					finalDiskSize = fmt.Sprintf("%dG", gb)
				} else {
					finalDiskSize = "1G" // 解析失败，默认1GB
				}
			} else if strings.HasSuffix(diskSize, "gb") || strings.HasSuffix(diskSize, "g") {
				// 已经是GB单位，直接使用
				finalDiskSize = config.Disk
				if !strings.HasSuffix(diskSize, "g") {
					finalDiskSize = strings.TrimSuffix(config.Disk, "b") // 移除"b"，保留"g"
				}
			} else {
				// 没有单位，假设是MB
				if mb, err := strconv.Atoi(config.Disk); err == nil {
					gb := (mb + 1023) / 1024 // 向上取整
					if gb < 1 {
						gb = 1
					}
					finalDiskSize = fmt.Sprintf("%dG", gb)
				} else {
					finalDiskSize = "1G"
				}
			}

			cmd += fmt.Sprintf(" --storage-opt size=%s", finalDiskSize)
			global.APP_LOG.Info("已启用硬盘大小限制",
				zap.String("name", utils.TruncateString(config.Name, 32)),
				zap.String("original_disk", config.Disk),
				zap.String("final_disk_size", finalDiskSize),
				zap.String("storage_driver", storageDriver))
		} else {
			global.APP_LOG.Warn("当前存储驱动不支持硬盘大小限制，忽略硬盘参数",
				zap.String("name", utils.TruncateString(config.Name, 32)),
				zap.String("storage_driver", storageDriver),
				zap.String("disk", config.Disk))
		}
	}

	updateProgress(80, "配置端口映射...")
	// 端口映射参数 - 只映射IPv4端口
	for _, port := range config.Ports {
		// 保留完整的端口映射格式（包括协议）
		portMapping := port

		// 检查端口映射格式，确保只映射IPv4
		if strings.HasPrefix(portMapping, "0.0.0.0:") {
			// 已经是IPv4格式（可能包含/tcp或/udp协议）
			// 检查是否包含 /both 协议，Docker不支持both，需要拆分
			if strings.HasSuffix(portMapping, "/both") {
				baseMapping := strings.TrimSuffix(portMapping, "/both")
				cmd += fmt.Sprintf(" -p %s/tcp", baseMapping)
				cmd += fmt.Sprintf(" -p %s/udp", baseMapping)
			} else {
				cmd += fmt.Sprintf(" -p %s", portMapping)
			}
		} else if strings.Contains(portMapping, ":") {
			// 如果端口映射中包含冒号但没有IPv4前缀，强制使用0.0.0.0绑定
			// 需要保留协议部分（如果有）
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
				// 重新构建为IPv4-only格式，处理协议
				hostPort := portParts[len(portParts)-2]
				guestPort := portParts[len(portParts)-1]

				// 如果协议是both，需要创建两个端口映射（tcp和udp）
				if protocol == "/both" {
					cmd += fmt.Sprintf(" -p 0.0.0.0:%s:%s/tcp", hostPort, guestPort)
					cmd += fmt.Sprintf(" -p 0.0.0.0:%s:%s/udp", hostPort, guestPort)
				} else {
					cmd += fmt.Sprintf(" -p 0.0.0.0:%s:%s%s", hostPort, guestPort, protocol)
				}
			}
		} else {
			// 如果是简单的端口映射格式（如"8080"），假设内外端口相同，添加IPv4前缀
			cmd += fmt.Sprintf(" -p 0.0.0.0:%s:%s", portMapping, portMapping)
		}
	}

	updateProgress(85, "配置LXCFS卷挂载...")
	// 检查并添加LXCFS卷挂载
	lxcfsAvailable, lxcfsVolumes, lxcfsReason, err := d.checkLXCFS()
	if err != nil {
		global.APP_LOG.Warn("检查LXCFS状态失败",
			zap.String("name", utils.TruncateString(config.Name, 32)),
			zap.Error(err))
	} else if lxcfsAvailable && len(lxcfsVolumes) > 0 {
		// 检测到的LXCFS卷挂载
		for _, volume := range lxcfsVolumes {
			cmd += " " + volume
		}
		global.APP_LOG.Info("已启用LXCFS卷挂载，提供真实的容器内资源视图",
			zap.String("name", utils.TruncateString(config.Name, 32)),
			zap.String("reason", lxcfsReason),
			zap.Int("mount_count", len(lxcfsVolumes)))
	} else {
		global.APP_LOG.Debug("LXCFS不可用，跳过卷挂载",
			zap.String("name", utils.TruncateString(config.Name, 32)),
			zap.String("reason", lxcfsReason))
	}

	updateProgress(90, "配置容器能力和环境变量...")
	// 必要的能力
	cmd += " --cap-add=MKNOD"

	for key, value := range config.Env {
		cmd += fmt.Sprintf(" -e %s=%s", key, value)
	}

	cmd += fmt.Sprintf(" %s", imageNameWithPrefix)

	updateProgress(95, "执行Docker创建命令...")
	global.APP_LOG.Info("开始执行Docker创建命令",
		zap.String("name", utils.TruncateString(config.Name, 32)),
		zap.String("image", utils.TruncateString(imageNameWithPrefix, 64)),
		zap.String("command", utils.TruncateString(cmd, 200)))

	output, err := d.sshClient.Execute(cmd)
	if err != nil {
		global.APP_LOG.Error("Docker创建容器失败",
			zap.String("name", utils.TruncateString(config.Name, 32)),
			zap.String("command", utils.TruncateString(cmd, 200)),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("failed to create container: %w", err)
	}

	// 等待容器完全启动并验证状态
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

		// 检查容器状态
		statusOutput, err := d.sshClient.Execute(fmt.Sprintf("docker inspect %s --format '{{.State.Status}}'", config.Name))
		if err == nil {
			status := strings.ToLower(strings.TrimSpace(statusOutput))
			if status == "running" {
				isRunning = true
				global.APP_LOG.Info("Docker容器已确认运行",
					zap.String("name", utils.TruncateString(config.Name, 32)),
					zap.Duration("wait_time", time.Since(startTime)))
				break
			}
		}

		global.APP_LOG.Debug("等待容器启动",
			zap.String("name", config.Name),
			zap.Duration("elapsed", time.Since(startTime)))
	}

	if !isRunning {
		global.APP_LOG.Warn("无法确认容器运行状态，继续执行后续操作",
			zap.String("name", utils.TruncateString(config.Name, 32)))
	}

	// 配置SSH密码
	updateProgress(97, "配置SSH密码...")
	if err := d.configureInstanceSSHPassword(ctx, config); err != nil {
		// SSH密码设置失败也不应该阻止实例创建，记录错误即可
		global.APP_LOG.Warn("配置SSH密码失败", zap.Error(err))
	}

	// 获取并更新实例的PrivateIP（确保pmacct配置使用正确的内网IP）
	updateProgress(97, "获取实例内网IP...")
	if privateIP, err := d.getContainerPrivateIP(config.Name); err == nil && privateIP != "" {
		// 更新数据库中的PrivateIP
		var providerRecord providerModel.Provider
		var instance providerModel.Instance
		if err := global.APP_DB.Where("name = ?", d.config.Name).First(&providerRecord).Error; err == nil {
			if err := global.APP_DB.Where("name = ? AND provider_id = ?", config.Name, providerRecord.ID).First(&instance).Error; err == nil {
				if err := global.APP_DB.Model(&instance).Update("private_ip", privateIP).Error; err == nil {
					global.APP_LOG.Info("已更新Docker实例内网IP",
						zap.String("instanceName", config.Name),
						zap.String("privateIP", privateIP))
				}
			}
		}
	} else {
		global.APP_LOG.Warn("获取Docker实例内网IP失败，pmacct可能使用公网IP",
			zap.String("instanceName", config.Name),
			zap.Error(err))
	}

	// 初始化流量监控
	updateProgress(98, "初始化流量监控...")
	if err := d.initializePmacctMonitoring(ctx, config); err != nil {
		// pmacct监控初始化失败也不应该阻止实例创建，记录错误即可
		global.APP_LOG.Warn("初始化流量监控失败", zap.Error(err))
	}

	updateProgress(100, "Docker实例创建完成")
	global.APP_LOG.Info("Docker实例创建成功", zap.String("name", utils.TruncateString(config.Name, 32)))
	return nil
}

// sshStartInstance 启动实例
func (d *DockerProvider) sshStartInstance(ctx context.Context, id string) error {
	// 先检查容器状态，如果是Exited状态则使用restart命令
	statusOutput, err := d.sshClient.Execute(fmt.Sprintf("docker inspect %s --format '{{.State.Status}}'", id))
	if err != nil {
		global.APP_LOG.Error("检查Docker容器状态失败",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.Error(err))
		return fmt.Errorf("failed to check container status: %w", err)
	}

	status := strings.ToLower(strings.TrimSpace(statusOutput))
	var startCmd string
	startCmd = fmt.Sprintf("docker restart %s", id)
	if strings.Contains(status, "exited") {
		global.APP_LOG.Info("检测到容器为Exited状态，使用restart命令",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("status", status))
	} else if strings.Contains(status, "running") {
		global.APP_LOG.Info("容器已在运行", zap.String("id", utils.TruncateString(id, 32)))
		return nil
	}

	global.APP_LOG.Info("开始启动Docker实例",
		zap.String("id", utils.TruncateString(id, 32)),
		zap.String("command", startCmd))

	output, err := d.sshClient.Execute(startCmd)
	if err != nil {
		global.APP_LOG.Error("Docker实例启动失败",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("command", startCmd),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("failed to start container: %w", err)
	}

	// 等待容器真正启动 - 最多等待30秒
	maxWaitTime := 30 * time.Second
	checkInterval := 2 * time.Second
	startTime := time.Now()

	for {
		// 检查是否超时
		if time.Since(startTime) > maxWaitTime {
			return fmt.Errorf("等待容器启动超时 (30秒)")
		}

		// 等待一段时间后再检查
		time.Sleep(checkInterval)

		// 检查容器状态
		statusOutput, err := d.sshClient.Execute(fmt.Sprintf("docker inspect %s --format '{{.State.Status}}'", id))
		if err == nil {
			currentStatus := strings.ToLower(strings.TrimSpace(statusOutput))
			if currentStatus == "running" {
				// 容器已经启动，再等待额外的时间确保服务完全就绪
				time.Sleep(2 * time.Second)
				global.APP_LOG.Info("Docker容器已成功启动并就绪",
					zap.String("id", utils.TruncateString(id, 32)),
					zap.Duration("wait_time", time.Since(startTime)))
				return nil
			}
		}

		global.APP_LOG.Debug("等待容器启动",
			zap.String("id", id),
			zap.Duration("elapsed", time.Since(startTime)))
	}
}

// sshStopInstance 停止实例
func (d *DockerProvider) sshStopInstance(ctx context.Context, id string) error {
	stopCmd := fmt.Sprintf("docker stop %s", id)
	global.APP_LOG.Info("开始停止Docker实例",
		zap.String("id", utils.TruncateString(id, 32)),
		zap.String("command", stopCmd))
	output, err := d.sshClient.Execute(stopCmd)
	if err != nil {
		global.APP_LOG.Error("Docker实例停止失败",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("command", stopCmd),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("failed to stop container: %w", err)
	}

	// 等待并验证容器状态
	maxRetries := 10
	retryInterval := 1 * time.Second
	for i := 0; i < maxRetries; i++ {
		statusOutput, err := d.sshClient.Execute(fmt.Sprintf("docker inspect %s --format '{{.State.Status}}'", id))
		if err != nil {
			global.APP_LOG.Warn("检查Docker容器停止状态失败",
				zap.String("id", utils.TruncateString(id, 32)),
				zap.Int("retry", i+1),
				zap.Error(err))
			time.Sleep(retryInterval)
			continue
		}

		status := strings.ToLower(strings.TrimSpace(statusOutput))
		if strings.Contains(status, "exited") {
			global.APP_LOG.Info("Docker实例停止成功并已确认状态",
				zap.String("id", utils.TruncateString(id, 32)),
				zap.String("status", status))
			return nil
		}

		global.APP_LOG.Debug("等待Docker容器停止",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("current_status", status),
			zap.Int("retry", i+1))
		time.Sleep(retryInterval)
	}

	global.APP_LOG.Warn("Docker实例停止命令执行成功但状态验证超时",
		zap.String("id", utils.TruncateString(id, 32)))
	return nil
}

// sshRestartInstance 重启实例
func (d *DockerProvider) sshRestartInstance(ctx context.Context, id string) error {
	restartCmd := fmt.Sprintf("docker restart %s", id)
	global.APP_LOG.Info("开始重启Docker实例",
		zap.String("id", utils.TruncateString(id, 32)),
		zap.String("command", restartCmd))

	output, err := d.sshClient.Execute(restartCmd)
	if err != nil {
		global.APP_LOG.Error("Docker实例重启失败",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("command", restartCmd),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("failed to restart container: %w", err)
	}

	global.APP_LOG.Info("Docker实例重启成功", zap.String("id", utils.TruncateString(id, 32)))
	return nil
}

// sshDeleteInstance 删除实例 - 增强版，多重删除策略
func (d *DockerProvider) sshDeleteInstance(ctx context.Context, id string) error {
	global.APP_LOG.Info("开始删除Docker实例",
		zap.String("id", utils.TruncateString(id, 32)))

	// 预清理：先尝试删除所有同名的已停止容器（Exited状态）
	cleanupCmd := fmt.Sprintf("docker ps -a --filter name=^%s$ --filter status=exited -q | xargs -r docker rm -f", id)
	global.APP_LOG.Debug("清理已停止的同名容器",
		zap.String("id", utils.TruncateString(id, 32)),
		zap.String("command", cleanupCmd))

	cleanupOutput, cleanupErr := d.sshClient.Execute(cleanupCmd)
	if cleanupErr != nil {
		global.APP_LOG.Debug("清理已停止容器失败（可忽略）",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("output", utils.TruncateString(cleanupOutput, 200)),
			zap.Error(cleanupErr))
	} else {
		global.APP_LOG.Debug("已清理已停止的同名容器",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("output", utils.TruncateString(cleanupOutput, 200)))
	}

	// 定义多种删除策略，按优先级顺序执行
	deleteStrategies := []struct {
		name        string
		commands    []string
		description string
	}{
		{
			name: "graceful_stop_and_remove",
			commands: []string{
				fmt.Sprintf("docker stop %s", id),
				fmt.Sprintf("docker rm %s", id),
			},
			description: "优雅停止并删除容器",
		},
		{
			name: "force_remove_running",
			commands: []string{
				fmt.Sprintf("docker rm -f %s", id),
			},
			description: "强制删除正在运行的容器",
		},
		{
			name: "kill_and_remove",
			commands: []string{
				fmt.Sprintf("docker kill %s", id),
				fmt.Sprintf("docker rm %s", id),
			},
			description: "强制杀死进程并删除容器",
		},
		{
			name: "system_prune_targeted",
			commands: []string{
				fmt.Sprintf("docker rm -f %s", id),
				"docker system prune -f --volumes",
			},
			description: "删除容器并清理系统资源",
		},
	}

	maxRetries := 3
	retryDelay := 2 * time.Second

	// 尝试每种删除策略
	for strategyIndex, strategy := range deleteStrategies {
		global.APP_LOG.Info("尝试删除策略",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("strategy", strategy.name),
			zap.String("description", strategy.description),
			zap.Int("strategyIndex", strategyIndex+1),
			zap.Int("totalStrategies", len(deleteStrategies)))

		// 对每种策略进行重试
		for retry := 1; retry <= maxRetries; retry++ {
			success := true
			var lastErr error

			// 执行策略中的所有命令
			for cmdIndex, cmd := range strategy.commands {
				global.APP_LOG.Debug("执行删除命令",
					zap.String("id", utils.TruncateString(id, 32)),
					zap.String("strategy", strategy.name),
					zap.Int("retry", retry),
					zap.Int("cmdIndex", cmdIndex+1),
					zap.String("command", cmd))

				output, err := d.sshClient.Execute(cmd)
				if err != nil {
					// 某些错误是可以接受的
					if d.isAcceptableError(err, output) {
						global.APP_LOG.Debug("命令执行失败但错误可接受",
							zap.String("id", utils.TruncateString(id, 32)),
							zap.String("command", cmd),
							zap.String("output", utils.TruncateString(output, 200)),
							zap.Error(err))
						continue
					}

					global.APP_LOG.Warn("删除命令执行失败",
						zap.String("id", utils.TruncateString(id, 32)),
						zap.String("strategy", strategy.name),
						zap.Int("retry", retry),
						zap.String("command", cmd),
						zap.String("output", utils.TruncateString(output, 200)),
						zap.Error(err))

					lastErr = err
					success = false
					break
				} else {
					global.APP_LOG.Debug("删除命令执行成功",
						zap.String("id", utils.TruncateString(id, 32)),
						zap.String("command", cmd),
						zap.String("output", utils.TruncateString(output, 100)))
				}
			}

			// 如果所有命令都成功执行，验证容器是否真的被删除
			if success {
				if d.verifyContainerDeleted(ctx, id) {
					global.APP_LOG.Info("Docker实例删除成功",
						zap.String("id", utils.TruncateString(id, 32)),
						zap.String("strategy", strategy.name),
						zap.Int("retry", retry))
					return nil
				} else {
					global.APP_LOG.Warn("删除命令执行成功但容器仍存在",
						zap.String("id", utils.TruncateString(id, 32)),
						zap.String("strategy", strategy.name),
						zap.Int("retry", retry))
					success = false
				}
			}

			// 如果失败，等待后重试
			if !success && retry < maxRetries {
				global.APP_LOG.Debug("等待后重试删除",
					zap.String("id", utils.TruncateString(id, 32)),
					zap.String("strategy", strategy.name),
					zap.Int("retry", retry),
					zap.Duration("delay", retryDelay))

				// 使用Timer避免time.After泄漏
				retryTimer := time.NewTimer(retryDelay)
				select {
				case <-ctx.Done():
					retryTimer.Stop()
					return ctx.Err()
				case <-retryTimer.C:
					// 继续重试
				}
			}

			// 如果成功或达到最大重试次数，跳出重试循环
			if success {
				break
			} else if retry == maxRetries {
				global.APP_LOG.Warn("删除策略达到最大重试次数",
					zap.String("id", utils.TruncateString(id, 32)),
					zap.String("strategy", strategy.name),
					zap.Int("maxRetries", maxRetries),
					zap.Error(lastErr))
			}
		}

		// 在策略之间等待一下，让系统稳定
		if strategyIndex < len(deleteStrategies)-1 {
			time.Sleep(1 * time.Second)
		}
	}

	// 最后的强制清理：尝试删除所有同名的已停止容器
	global.APP_LOG.Info("执行最终清理，删除所有同名已停止容器",
		zap.String("id", utils.TruncateString(id, 32)))

	finalCleanupCmd := fmt.Sprintf("docker ps -a --filter name=^%s$ -q | xargs -r docker rm -f", id)
	finalOutput, finalErr := d.sshClient.Execute(finalCleanupCmd)
	if finalErr != nil {
		global.APP_LOG.Debug("最终清理失败（可忽略）",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("output", utils.TruncateString(finalOutput, 200)),
			zap.Error(finalErr))
	} else {
		global.APP_LOG.Debug("最终清理执行完成",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("output", utils.TruncateString(finalOutput, 200)))
	}

	// 最后再次验证容器是否被删除
	if d.verifyContainerDeleted(ctx, id) {
		global.APP_LOG.Info("Docker实例最终确认删除成功", zap.String("id", utils.TruncateString(id, 32)))
		return nil
	}

	// 所有策略都失败了
	global.APP_LOG.Error("所有删除策略都失败，容器可能仍然存在",
		zap.String("id", utils.TruncateString(id, 32)))
	return fmt.Errorf("failed to delete container after trying all strategies: %s", id)
}

// isAcceptableError 检查是否是可以接受的错误（例如容器已经不存在）
func (d *DockerProvider) isAcceptableError(err error, output string) bool {
	errorStr := strings.ToLower(err.Error())
	outputStr := strings.ToLower(output)
	acceptableErrors := []string{
		"no such container",
		"not found",
		"already removed",
		"container not found",
		"no containers to remove",
		"is not running",
		"cannot stop container",
		"no such process",
	}
	for _, acceptableErr := range acceptableErrors {
		if strings.Contains(errorStr, acceptableErr) || strings.Contains(outputStr, acceptableErr) {
			return true
		}
	}
	return false
}

// verifyContainerDeleted 验证容器是否真的被删除（包括已停止的容器）
func (d *DockerProvider) verifyContainerDeleted(ctx context.Context, id string) bool {
	// 方法1：检查运行中的容器
	checkCmd := fmt.Sprintf("docker inspect %s --format '{{.State.Status}}'", id)
	output, err := d.sshClient.Execute(checkCmd)

	if err != nil {
		// 如果命令失败，很可能是容器不存在了
		outputStr := strings.ToLower(output)
		if strings.Contains(outputStr, "no such object") ||
			strings.Contains(outputStr, "no such container") ||
			strings.Contains(outputStr, "not found") {
			// 继续检查是否有已停止的同名容器
			global.APP_LOG.Debug("inspect未找到容器，继续验证",
				zap.String("id", utils.TruncateString(id, 32)))
		} else {
			global.APP_LOG.Warn("验证容器删除时inspect出错",
				zap.String("id", utils.TruncateString(id, 32)),
				zap.String("output", utils.TruncateString(output, 100)),
				zap.Error(err))
			return false
		}
	} else {
		// inspect成功，说明容器还在
		global.APP_LOG.Warn("容器仍然存在",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("status", strings.TrimSpace(output)))
		return false
	}

	// 方法2：通过docker ps -a检查所有状态的容器（包括已停止的）
	// 使用精确匹配的name filter
	listByNameCmd := fmt.Sprintf("docker ps -a --filter name=^%s$ --format '{{.Names}}:{{.Status}}'", id)
	listByNameOutput, listByNameErr := d.sshClient.Execute(listByNameCmd)

	if listByNameErr == nil {
		trimmedOutput := strings.TrimSpace(listByNameOutput)
		if trimmedOutput != "" {
			// 找到了同名容器，即使已停止也算删除失败
			global.APP_LOG.Warn("发现同名容器（可能已停止）",
				zap.String("id", utils.TruncateString(id, 32)),
				zap.String("details", utils.TruncateString(trimmedOutput, 100)))
			return false
		}
	}

	// 方法3：用ID进行filter检查
	listCmd := fmt.Sprintf("docker ps -a --filter id=%s --format '{{.ID}}'", id)
	listOutput, listErr := d.sshClient.Execute(listCmd)

	if listErr == nil && strings.TrimSpace(listOutput) != "" {
		// 通过ID找到了容器
		global.APP_LOG.Warn("通过ID找到容器",
			zap.String("id", utils.TruncateString(id, 32)),
			zap.String("foundId", utils.TruncateString(strings.TrimSpace(listOutput), 32)))
		return false
	}

	// 所有检查都通过，容器已被删除
	return true
}

// checkStorageDriver 检查Docker存储驱动并判断是否支持硬盘大小限制
func (d *DockerProvider) checkStorageDriver() (bool, string, error) {
	// 首先尝试从缓存文件读取存储驱动信息
	cacheCmd := "cat /usr/local/bin/docker_storage_driver 2>/dev/null || echo ''"
	cacheOutput, _ := d.sshClient.Execute(cacheCmd)
	storageDriver := strings.TrimSpace(cacheOutput)

	// 如果缓存文件不存在或为空，则通过docker info命令获取
	if storageDriver == "" {
		infoCmd := "docker info --format '{{.Driver}}' 2>/dev/null || docker info | grep 'Storage Driver:' | awk '{print $3}'"
		output, err := d.sshClient.Execute(infoCmd)
		if err != nil {
			global.APP_LOG.Error("获取Docker存储驱动信息失败",
				zap.String("provider", d.config.Name),
				zap.Error(err))
			return false, "", fmt.Errorf("failed to get storage driver: %w", err)
		}
		storageDriver = strings.TrimSpace(output)
	}

	// 如果仍然为空，默认为overlay2
	if storageDriver == "" {
		storageDriver = "overlay2"
	}

	// 检查是否支持硬盘大小限制
	// 目前只有btrfs存储驱动支持--storage-opt size参数
	supportsDiskLimit := storageDriver == "btrfs"

	global.APP_LOG.Info("Docker存储驱动检测结果",
		zap.String("provider", d.config.Name),
		zap.String("storage_driver", storageDriver),
		zap.Bool("supports_disk_limit", supportsDiskLimit))

	return supportsDiskLimit, storageDriver, nil
}

// checkLXCFS 检查LXCFS服务是否可用并返回可用的挂载路径
func (d *DockerProvider) checkLXCFS() (bool, []string, string, error) {
	// 检查lxcfs服务是否活跃
	statusCmd := "systemctl is-active lxcfs 2>/dev/null"
	statusOutput, err := d.sshClient.Execute(statusCmd)
	if err != nil || strings.TrimSpace(statusOutput) != "active" {
		global.APP_LOG.Debug("LXCFS服务未运行",
			zap.String("provider", d.config.Name),
			zap.String("status", strings.TrimSpace(statusOutput)))
		return false, nil, "LXCFS服务未运行", nil
	}

	// 检查lxcfs proc目录是否存在
	procDirCmd := "[ -d '/var/lib/lxcfs/proc' ] && echo 'exists' || echo 'not_exists'"
	procDirOutput, err := d.sshClient.Execute(procDirCmd)
	if err != nil || strings.TrimSpace(procDirOutput) != "exists" {
		global.APP_LOG.Debug("LXCFS proc目录不存在",
			zap.String("provider", d.config.Name))
		return false, nil, "LXCFS proc目录不存在", nil
	}

	// 定义所有可能的LXCFS挂载文件
	potentialMounts := map[string]string{
		"/var/lib/lxcfs/proc/cpuinfo":   "/proc/cpuinfo",
		"/var/lib/lxcfs/proc/diskstats": "/proc/diskstats",
		"/var/lib/lxcfs/proc/meminfo":   "/proc/meminfo",
		"/var/lib/lxcfs/proc/stat":      "/proc/stat",
		"/var/lib/lxcfs/proc/swaps":     "/proc/swaps",
		"/var/lib/lxcfs/proc/uptime":    "/proc/uptime",
	}

	// 逐个检查文件是否存在，只收集存在的文件
	var availableVolumes []string
	var availableFiles []string

	for hostPath, containerPath := range potentialMounts {
		checkCmd := fmt.Sprintf("[ -f '%s' ] && echo 'exists' || echo 'not_exists'", hostPath)
		output, err := d.sshClient.Execute(checkCmd)
		if err == nil && strings.TrimSpace(output) == "exists" {
			volumeMount := fmt.Sprintf("--volume %s:%s:rw", hostPath, containerPath)
			availableVolumes = append(availableVolumes, volumeMount)
			availableFiles = append(availableFiles, hostPath)
		} else {
			global.APP_LOG.Debug("LXCFS文件不存在，跳过挂载",
				zap.String("provider", d.config.Name),
				zap.String("file", hostPath))
		}
	}

	// 如果没有任何可用的挂载文件，认为LXCFS不可用
	if len(availableVolumes) == 0 {
		return false, nil, "没有可用的LXCFS挂载文件", nil
	}

	reason := fmt.Sprintf("LXCFS可用，找到%d个可挂载文件", len(availableVolumes))
	global.APP_LOG.Info("LXCFS检测结果",
		zap.String("provider", d.config.Name),
		zap.String("reason", reason),
		zap.Strings("available_files", availableFiles))

	return true, availableVolumes, reason, nil
}

// configureInstanceSSHPassword 专门用于设置Docker容器的SSH密码
func (d *DockerProvider) configureInstanceSSHPassword(ctx context.Context, config provider.InstanceConfig) error {
	global.APP_LOG.Info("开始配置Docker容器SSH密码",
		zap.String("instanceName", config.Name))

	// 生成随机密码
	password := d.generateRandomPassword()

	// 根据系统类型选择脚本
	var scriptName string
	// 检测系统类型
	output, err := d.sshClient.Execute(fmt.Sprintf("docker exec %s cat /etc/os-release 2>/dev/null | grep ^ID= | cut -d= -f2 | tr -d '\"'", config.Name))
	if err == nil {
		osType := strings.TrimSpace(strings.ToLower(output))
		if osType == "alpine" || osType == "openwrt" {
			scriptName = "ssh_sh.sh"
		} else {
			scriptName = "ssh_bash.sh"
		}
	} else {
		// 默认使用bash脚本
		scriptName = "ssh_bash.sh"
	}

	scriptPath := filepath.Join("/usr/local/bin", scriptName)
	// 检查脚本是否存在
	if !d.isRemoteFileValid(scriptPath) {
		global.APP_LOG.Warn("SSH脚本不存在，仅设置密码不配置SSH",
			zap.String("scriptPath", scriptPath))
		// 即使脚本不存在，也要设置密码
	} else {
		time.Sleep(3 * time.Second)
		// 复制脚本到容器
		copyCmd := fmt.Sprintf("docker cp %s %s:/root/", scriptPath, config.Name)
		_, err = d.sshClient.Execute(copyCmd)
		if err != nil {
			global.APP_LOG.Warn("复制SSH脚本到容器失败",
				zap.String("instanceName", config.Name),
				zap.String("scriptPath", scriptPath),
				zap.Error(err))
		} else {
			// 设置脚本权限
			_, err = d.sshClient.Execute(fmt.Sprintf("docker exec %s chmod +x /root/%s", config.Name, scriptName))
			if err != nil {
				global.APP_LOG.Warn("设置脚本权限失败", zap.Error(err))
			} else {
				// 执行脚本配置SSH和密码
				execCmd := fmt.Sprintf("docker exec %s /root/%s %s", config.Name, scriptName, password)
				_, execErr := d.sshClient.Execute(execCmd)
				if execErr != nil {
					global.APP_LOG.Warn("执行SSH配置脚本失败，将使用直接设置密码",
						zap.String("instanceName", config.Name),
						zap.String("scriptName", scriptName),
						zap.Error(execErr))
				}
				time.Sleep(3 * time.Second)
			}
		}
	}

	// 直接使用docker exec设置密码
	directPasswordCmd := fmt.Sprintf("docker exec %s bash -c 'echo \"root:%s\" | chpasswd'", config.Name, password)
	_, err = d.sshClient.Execute(directPasswordCmd)
	if err != nil {
		global.APP_LOG.Error("设置容器密码失败",
			zap.String("instanceName", config.Name),
			zap.Error(err))
		return fmt.Errorf("设置容器密码失败: %w", err)
	}

	global.APP_LOG.Info("Docker容器SSH密码配置成功",
		zap.String("instanceName", config.Name))

	// 更新数据库中的密码记录，确保数据库与实际密码一致
	err = global.APP_DB.Model(&providerModel.Instance{}).
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

// getContainerPrivateIP 获取容器的内网IP地址
func (d *DockerProvider) getContainerPrivateIP(containerName string) (string, error) {
	cmd := fmt.Sprintf("docker inspect %s --format '{{range $net, $config := .NetworkSettings.Networks}}{{$config.IPAddress}}{{end}}'", containerName)
	output, err := d.sshClient.Execute(cmd)
	if err != nil {
		return "", fmt.Errorf("failed to get container IP: %w", err)
	}

	ipAddress := strings.TrimSpace(output)
	if ipAddress == "" || ipAddress == "<no value>" {
		// 尝试使用默认网络
		cmd = fmt.Sprintf("docker inspect %s --format '{{.NetworkSettings.IPAddress}}'", containerName)
		output, err = d.sshClient.Execute(cmd)
		if err != nil {
			return "", fmt.Errorf("failed to get container IP from default network: %w", err)
		}
		ipAddress = strings.TrimSpace(output)
	}

	if ipAddress == "" || ipAddress == "<no value>" {
		return "", fmt.Errorf("container IP is empty")
	}

	return ipAddress, nil
}

// initializePmacctMonitoring 初始化流量监控
func (d *DockerProvider) initializePmacctMonitoring(ctx context.Context, config provider.InstanceConfig) error {
	// 查找provider记录
	var providerRecord providerModel.Provider
	if err := global.APP_DB.Where("name = ?", d.config.Name).First(&providerRecord).Error; err != nil {
		global.APP_LOG.Warn("查找provider记录失败，跳过pmacct初始化",
			zap.String("provider_name", d.config.Name),
			zap.Error(err))
		return fmt.Errorf("查找provider记录失败: %w", err)
	}

	// 查找实例ID
	var instanceID uint
	var instance providerModel.Instance
	if err := global.APP_DB.Where("name = ? AND provider_id = ?", config.Name, providerRecord.ID).First(&instance).Error; err != nil {
		global.APP_LOG.Warn("查找实例记录失败，跳过pmacct初始化",
			zap.String("instance_name", config.Name),
			zap.Uint("provider_id", providerRecord.ID),
			zap.Error(err))
		return fmt.Errorf("查找实例记录失败: %w", err)
	}
	instanceID = instance.ID

	// 检查provider是否启用了流量统计
	if !providerRecord.EnableTrafficControl {
		global.APP_LOG.Debug("Provider未启用流量统计，跳过Docker容器pmacct监控初始化",
			zap.String("providerName", d.config.Name),
			zap.String("instanceName", config.Name),
			zap.Uint("instanceId", instanceID))
		return nil
	}

	global.APP_LOG.Info("开始初始化Docker容器pmacct监控",
		zap.String("instanceName", config.Name))

	// 初始化流量监控
	pmacctService := pmacct.NewService()
	if pmacctErr := pmacctService.InitializePmacctForInstance(instanceID); pmacctErr != nil {
		global.APP_LOG.Warn("Docker容器创建后初始化 pmacct 监控失败",
			zap.Uint("instanceId", instanceID),
			zap.String("instanceName", config.Name),
			zap.Error(pmacctErr))
		return fmt.Errorf("初始化 pmacct 监控失败: %w", pmacctErr)
	}

	global.APP_LOG.Info("Docker容器创建后 pmacct 监控初始化成功",
		zap.Uint("instanceId", instanceID),
		zap.String("instanceName", config.Name))

	// 触发流量数据同步
	syncTrigger := traffic.NewSyncTriggerService()
	syncTrigger.TriggerInstanceTrafficSync(instanceID, "Docker容器创建完成后初始化")

	return nil
}
