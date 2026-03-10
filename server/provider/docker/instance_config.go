package docker

import (
	"context"
	"fmt"
	"path/filepath"
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

// checkStorageDriver 检查Docker存储驱动并判断是否支持硬盘大小限制
func (d *DockerProvider) checkStorageDriver() (bool, string, error) {
	// 首先尝试从缓存文件读取存储驱动信息
	cacheCmd := fmt.Sprintf("cat %s 2>/dev/null || echo ''", d.runtime.StorageDriverFile)
	cacheOutput, _ := d.sshClient.Execute(cacheCmd)
	storageDriver := strings.TrimSpace(cacheOutput)

	// 如果缓存文件不存在或为空，则通过docker info命令获取
	if storageDriver == "" {
		infoCmd := fmt.Sprintf("%s info --format '{{.Driver}}' 2>/dev/null || %s info | grep 'Storage Driver:' | awk '{print $3}'", d.runtime.CLI, d.runtime.CLI)
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

	global.APP_LOG.Debug("Docker存储驱动检测结果",
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
	global.APP_LOG.Debug("LXCFS检测结果",
		zap.String("provider", d.config.Name),
		zap.String("reason", reason),
		zap.Strings("available_files", availableFiles))

	return true, availableVolumes, reason, nil
}

// ensureContainerRunning 确保容器处于运行状态；若已退出则重启并等待
func (d *DockerProvider) ensureContainerRunning(containerName string) error {
	checkCmd := fmt.Sprintf("%s inspect %s --format '{{.State.Status}}'", d.runtime.CLI, containerName)
	output, err := d.sshClient.Execute(checkCmd)
	if err != nil {
		return fmt.Errorf("检查容器状态失败: %w", err)
	}
	status := strings.ToLower(strings.TrimSpace(output))
	if status == "running" {
		return nil
	}
	// 容器可能因 OOM 等原因退出，尝试重启
	global.APP_LOG.Warn("容器未处于运行状态，尝试重启",
		zap.String("containerName", containerName),
		zap.String("status", status))
	restartCmd := fmt.Sprintf("%s restart %s", d.runtime.CLI, containerName)
	if _, restartErr := d.sshClient.Execute(restartCmd); restartErr != nil {
		return fmt.Errorf("重启容器失败: %w", restartErr)
	}
	// 等待容器重新运行，最多 30 秒
	for i := 0; i < 15; i++ {
		time.Sleep(2 * time.Second)
		out, err2 := d.sshClient.Execute(checkCmd)
		if err2 == nil && strings.ToLower(strings.TrimSpace(out)) == "running" {
			global.APP_LOG.Debug("容器重启后已恢复运行",
				zap.String("containerName", containerName))
			return nil
		}
	}
	return fmt.Errorf("等待容器重启超时")
}

// setContainerPasswordWithRetry 使用多种 shell 回退方式设置容器 root 密码，带重试
func (d *DockerProvider) setContainerPasswordWithRetry(containerName, password, preferShell string) error {
	shells := []string{preferShell}
	if preferShell != "sh" {
		shells = append(shells, "sh")
	}

	maxRetries := 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		for _, shell := range shells {
			cmd := fmt.Sprintf("%s exec %s %s -c 'echo \"root:%s\" | chpasswd'",
				d.runtime.CLI, containerName, shell, password)
			_, err := d.sshClient.Execute(cmd)
			if err == nil {
				global.APP_LOG.Info("容器密码设置成功",
					zap.String("containerName", containerName),
					zap.String("shell", shell),
					zap.Int("attempt", attempt))
				return nil
			}
			global.APP_LOG.Warn("使用 chpasswd 设置密码失败，尝试备用方式",
				zap.String("containerName", containerName),
				zap.String("shell", shell),
				zap.Int("attempt", attempt),
				zap.Error(err))
		}
		if attempt < maxRetries {
			// 等待后重试；若容器退出则重启
			time.Sleep(5 * time.Second)
			if err := d.ensureContainerRunning(containerName); err != nil {
				global.APP_LOG.Warn("重试前确认容器运行状态失败",
					zap.String("containerName", containerName),
					zap.Error(err))
			}
		}
	}
	return fmt.Errorf("所有 shell 方式均无法设置容器密码: %s", containerName)
}

// configureInstanceSSHPassword 专门用于设置Docker容器的SSH密码
func (d *DockerProvider) configureInstanceSSHPassword(ctx context.Context, config provider.InstanceConfig) error {
	global.APP_LOG.Debug("开始配置Docker容器SSH密码",
		zap.String("instanceName", config.Name))

	// 生成随机密码
	password := d.generateRandomPassword()

	// 检测系统类型选择 shell 和脚本
	shellType := "bash"
	scriptName := "ssh_bash.sh"
	output, err := d.sshClient.Execute(fmt.Sprintf("%s exec %s cat /etc/os-release 2>/dev/null | grep ^ID= | cut -d= -f2 | tr -d '\"'", d.runtime.CLI, config.Name))
	if err == nil {
		osType := utils.CleanCommandOutput(strings.ToLower(output))
		if osType == "alpine" || osType == "openwrt" {
			shellType = "sh"
			scriptName = "ssh_sh.sh"
		}
	}

	scriptPath := filepath.Join("/usr/local/bin", scriptName)
	if d.isRemoteFileValid(scriptPath) {
		time.Sleep(3 * time.Second)
		copyCmd := fmt.Sprintf("%s cp %s %s:/root/", d.runtime.CLI, scriptPath, config.Name)
		_, copyErr := d.sshClient.Execute(copyCmd)
		if copyErr != nil {
			global.APP_LOG.Warn("复制SSH脚本到容器失败",
				zap.String("instanceName", config.Name),
				zap.String("scriptPath", scriptPath),
				zap.Error(copyErr))
		} else {
			d.sshClient.Execute(fmt.Sprintf("%s exec %s %s -c 'chmod +x /root/%s'", d.runtime.CLI, config.Name, shellType, scriptName))
			// 使用 interactionless=true 减少脚本交互内存占用
			execCmd := fmt.Sprintf("%s exec %s %s -c 'interactionless=true %s /root/%s %s'",
				d.runtime.CLI, config.Name, shellType, shellType, scriptName, password)
			_, execErr := d.sshClient.Execute(execCmd)
			if execErr != nil {
				global.APP_LOG.Warn("执行SSH配置脚本失败，将使用直接设置密码",
					zap.String("instanceName", config.Name),
					zap.String("scriptName", scriptName),
					zap.Error(execErr))
			}
			// 脚本运行后稍等，让容器内部稳定（OOM 后容器可能需要恢复）
			time.Sleep(5 * time.Second)
		}
	} else {
		global.APP_LOG.Warn("SSH脚本不存在，仅设置密码不配置SSH",
			zap.String("scriptPath", scriptPath))
	}

	// 确保容器仍在运行（OOM / cgroup v2 可能导致容器退出）
	if ensureErr := d.ensureContainerRunning(config.Name); ensureErr != nil {
		global.APP_LOG.Error("配置SSH密码前确认容器运行状态失败",
			zap.String("instanceName", config.Name),
			zap.Error(ensureErr))
		return fmt.Errorf("配置SSH密码前确认容器运行状态失败: %w", ensureErr)
	}

	// 直接通过 chpasswd 设置密码（多 shell 回退 + 重试）
	if err := d.setContainerPasswordWithRetry(config.Name, password, shellType); err != nil {
		global.APP_LOG.Error("设置容器密码失败",
			zap.String("instanceName", config.Name),
			zap.Error(err))
		return fmt.Errorf("设置容器密码失败: %w", err)
	}

	global.APP_LOG.Info("Docker容器SSH密码配置成功",
		zap.String("instanceName", config.Name))

	// 更新数据库中的密码记录，确保数据库与实际密码一致
	if dbErr := global.APP_DB.Model(&providerModel.Instance{}).
		Where("name = ?", config.Name).
		Update("password", password).Error; dbErr != nil {
		global.APP_LOG.Warn("更新实例密码到数据库失败",
			zap.String("instanceName", config.Name),
			zap.Error(dbErr))
	} else {
		global.APP_LOG.Debug("实例密码已同步到数据库",
			zap.String("instanceName", config.Name))
	}

	return nil
}

// getContainerPrivateIP 获取容器的内网IP地址
func (d *DockerProvider) getContainerPrivateIP(containerName string) (string, error) {
	cmd := fmt.Sprintf("%s inspect %s --format '{{range $net, $config := .NetworkSettings.Networks}}{{$config.IPAddress}}{{end}}'", d.runtime.CLI, containerName)
	output, err := d.sshClient.Execute(cmd)
	if err == nil {
		ipAddress := utils.CleanCommandOutput(output)
		if ipAddress != "" && ipAddress != "<no value>" {
			return ipAddress, nil
		}
	}

	// 尝试使用默认网络字段
	cmd = fmt.Sprintf("%s inspect %s --format '{{.NetworkSettings.IPAddress}}'", d.runtime.CLI, containerName)
	output, err = d.sshClient.Execute(cmd)
	if err == nil {
		ipAddress := utils.CleanCommandOutput(output)
		if ipAddress != "" && ipAddress != "<no value>" {
			return ipAddress, nil
		}
	}

	// 终极回退：直接在容器内执行 hostname -I（适用于 podman/containerd 等 inspect 格式差异）
	hostCmd := fmt.Sprintf("%s exec %s hostname -I 2>/dev/null", d.runtime.CLI, containerName)
	hostOutput, hostErr := d.sshClient.Execute(hostCmd)
	if hostErr == nil {
		// hostname -I 可能返回多个 IP，取第一个
		ips := strings.Fields(strings.TrimSpace(hostOutput))
		if len(ips) > 0 && ips[0] != "" {
			return ips[0], nil
		}
	}

	if err != nil {
		return "", fmt.Errorf("failed to get container IP: %w", err)
	}
	return "", fmt.Errorf("container IP is empty")
}

// initializePmacctMonitoring 初始化流量监控
func (d *DockerProvider) initializePmacctMonitoring(ctx context.Context, config provider.InstanceConfig) error {
	// 查找provider记录
	var providerRecord providerModel.Provider
	if err := global.APP_DB.Where("name = ?", d.config.Name).First(&providerRecord).Error; err != nil {
		global.APP_LOG.Error("查找provider记录失败，跳过pmacct初始化",
			zap.String("provider_name", d.config.Name),
			zap.Error(err))
		return fmt.Errorf("查找provider记录失败: %w", err)
	}

	// 查找实例ID
	var instanceID uint
	var instance providerModel.Instance
	if err := global.APP_DB.Where("name = ? AND provider_id = ?", config.Name, providerRecord.ID).First(&instance).Error; err != nil {
		global.APP_LOG.Error("查找实例记录失败，跳过pmacct初始化",
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

	global.APP_LOG.Debug("开始初始化Docker容器pmacct监控",
		zap.String("instanceName", config.Name))

	// 初始化流量监控
	pmacctService := pmacct.NewService()
	if pmacctErr := pmacctService.InitializePmacctForInstance(instanceID); pmacctErr != nil {
		global.APP_LOG.Error("Docker容器创建后初始化 pmacct 监控失败",
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
