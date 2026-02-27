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

// configureInstanceSSHPassword 专门用于设置Docker容器的SSH密码
func (d *DockerProvider) configureInstanceSSHPassword(ctx context.Context, config provider.InstanceConfig) error {
	global.APP_LOG.Debug("开始配置Docker容器SSH密码",
		zap.String("instanceName", config.Name))

	// 生成随机密码
	password := d.generateRandomPassword()

	// 根据系统类型选择脚本
	var scriptName string
	// 检测系统类型
	output, err := d.sshClient.Execute(fmt.Sprintf("docker exec %s cat /etc/os-release 2>/dev/null | grep ^ID= | cut -d= -f2 | tr -d '\"'", config.Name))
	if err == nil {
		osType := utils.CleanCommandOutput(strings.ToLower(output))
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
		global.APP_LOG.Debug("实例密码已同步到数据库",
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

	ipAddress := utils.CleanCommandOutput(output)
	if ipAddress == "" || ipAddress == "<no value>" {
		// 尝试使用默认网络
		cmd = fmt.Sprintf("docker inspect %s --format '{{.NetworkSettings.IPAddress}}'", containerName)
		output, err = d.sshClient.Execute(cmd)
		if err != nil {
			return "", fmt.Errorf("failed to get container IP from default network: %w", err)
		}
		ipAddress = utils.CleanCommandOutput(output)
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
