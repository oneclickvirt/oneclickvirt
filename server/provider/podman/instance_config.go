package podman

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

// checkStorageDriver 检查Podman存储驱动并判断是否支持硬盘大小限制
func (p *PodmanProvider) checkStorageDriver() (bool, string, error) {
	cacheCmd := fmt.Sprintf("cat %s 2>/dev/null || echo ''", storageDriverFile)
	cacheOutput, _ := p.sshClient.Execute(cacheCmd)
	driver := strings.TrimSpace(cacheOutput)

	if driver == "" {
		infoCmd := fmt.Sprintf("%s info --format '{{.Driver}}' 2>/dev/null || %s info | grep 'Storage Driver:' | awk '{print $3}'", cliName, cliName)
		output, err := p.sshClient.Execute(infoCmd)
		if err != nil {
			return false, "", fmt.Errorf("failed to get storage driver: %w", err)
		}
		driver = strings.TrimSpace(output)
	}

	if driver == "" {
		driver = "overlay"
	}

	supportsDiskLimit := driver == "btrfs"
	return supportsDiskLimit, driver, nil
}

// checkLXCFS 检查LXCFS服务是否可用并返回可用的挂载路径
func (p *PodmanProvider) checkLXCFS() (bool, []string, string, error) {
	statusCmd := "systemctl is-active lxcfs 2>/dev/null"
	statusOutput, err := p.sshClient.Execute(statusCmd)
	if err != nil || strings.TrimSpace(statusOutput) != "active" {
		return false, nil, "LXCFS服务未运行", nil
	}

	procDirCmd := "[ -d '/var/lib/lxcfs/proc' ] && echo 'exists' || echo 'not_exists'"
	procDirOutput, err := p.sshClient.Execute(procDirCmd)
	if err != nil || strings.TrimSpace(procDirOutput) != "exists" {
		return false, nil, "LXCFS proc目录不存在", nil
	}

	potentialMounts := map[string]string{
		"/var/lib/lxcfs/proc/cpuinfo":   "/proc/cpuinfo",
		"/var/lib/lxcfs/proc/diskstats": "/proc/diskstats",
		"/var/lib/lxcfs/proc/meminfo":   "/proc/meminfo",
		"/var/lib/lxcfs/proc/stat":      "/proc/stat",
		"/var/lib/lxcfs/proc/swaps":     "/proc/swaps",
		"/var/lib/lxcfs/proc/uptime":    "/proc/uptime",
	}

	var availableVolumes []string
	for hostPath, containerPath := range potentialMounts {
		checkCmd := fmt.Sprintf("[ -f '%s' ] && echo 'exists' || echo 'not_exists'", hostPath)
		output, err := p.sshClient.Execute(checkCmd)
		if err == nil && strings.TrimSpace(output) == "exists" {
			availableVolumes = append(availableVolumes, fmt.Sprintf("--volume %s:%s:rw", hostPath, containerPath))
		}
	}

	if len(availableVolumes) == 0 {
		return false, nil, "没有可用的LXCFS挂载文件", nil
	}

	reason := fmt.Sprintf("LXCFS可用，找到%d个可挂载文件", len(availableVolumes))
	return true, availableVolumes, reason, nil
}

// ensureContainerRunning 确保容器处于运行状态
func (p *PodmanProvider) ensureContainerRunning(containerName string) error {
	checkCmd := fmt.Sprintf("%s inspect %s --format '{{.State.Status}}'", cliName, containerName)
	output, err := p.sshClient.Execute(checkCmd)
	if err != nil {
		return fmt.Errorf("检查容器状态失败: %w", err)
	}
	status := strings.ToLower(strings.TrimSpace(output))
	if status == "running" {
		return nil
	}
	global.APP_LOG.Warn("容器未处于运行状态，尝试重启",
		zap.String("containerName", containerName),
		zap.String("status", status))
	restartCmd := fmt.Sprintf("%s restart %s", cliName, containerName)
	if _, restartErr := p.sshClient.Execute(restartCmd); restartErr != nil {
		return fmt.Errorf("重启容器失败: %w", restartErr)
	}
	for i := 0; i < 15; i++ {
		time.Sleep(2 * time.Second)
		out, err2 := p.sshClient.Execute(checkCmd)
		if err2 == nil && strings.ToLower(strings.TrimSpace(out)) == "running" {
			return nil
		}
	}
	return fmt.Errorf("等待容器重启超时")
}

// setContainerPasswordWithRetry 使用多种 shell 回退方式设置容器 root 密码
func (p *PodmanProvider) setContainerPasswordWithRetry(containerName, password, preferShell string) error {
	shells := []string{preferShell}
	if preferShell != "sh" {
		shells = append(shells, "sh")
	}

	maxRetries := 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		for _, shell := range shells {
			cmd := fmt.Sprintf("%s exec %s %s -c 'echo \"root:%s\" | chpasswd'",
				cliName, containerName, shell, password)
			_, err := p.sshClient.Execute(cmd)
			if err == nil {
				return nil
			}
			global.APP_LOG.Warn("使用 chpasswd 设置密码失败，尝试备用方式",
				zap.String("containerName", containerName),
				zap.String("shell", shell),
				zap.Int("attempt", attempt),
				zap.Error(err))
		}
		if attempt < maxRetries {
			time.Sleep(5 * time.Second)
			p.ensureContainerRunning(containerName)
		}
	}
	return fmt.Errorf("所有 shell 方式均无法设置容器密码: %s", containerName)
}

// configureInstanceSSHPassword 设置Podman容器的SSH密码
func (p *PodmanProvider) configureInstanceSSHPassword(ctx context.Context, config provider.InstanceConfig) error {
	password := p.generateRandomPassword()

	shellType := "bash"
	scriptName := "ssh_bash.sh"
	output, err := p.sshClient.Execute(fmt.Sprintf("%s exec %s cat /etc/os-release 2>/dev/null | grep ^ID= | cut -d= -f2 | tr -d '\"'", cliName, config.Name))
	if err == nil {
		osType := utils.CleanCommandOutput(strings.ToLower(output))
		if osType == "alpine" || osType == "openwrt" {
			shellType = "sh"
			scriptName = "ssh_sh.sh"
		}
	}

	scriptPath := filepath.Join("/usr/local/bin", scriptName)
	if p.isRemoteFileValid(scriptPath) {
		time.Sleep(3 * time.Second)
		copyCmd := fmt.Sprintf("%s cp %s %s:/root/", cliName, scriptPath, config.Name)
		_, copyErr := p.sshClient.Execute(copyCmd)
		if copyErr == nil {
			p.sshClient.Execute(fmt.Sprintf("%s exec %s %s -c 'chmod +x /root/%s'", cliName, config.Name, shellType, scriptName))
			execCmd := fmt.Sprintf("%s exec %s %s -c 'interactionless=true %s /root/%s %s'",
				cliName, config.Name, shellType, shellType, scriptName, password)
			_, execErr := p.sshClient.Execute(execCmd)
			if execErr != nil {
				global.APP_LOG.Warn("执行SSH配置脚本失败，将使用直接设置密码",
					zap.String("instanceName", config.Name),
					zap.Error(execErr))
			}
			time.Sleep(5 * time.Second)
		}
	}

	if ensureErr := p.ensureContainerRunning(config.Name); ensureErr != nil {
		return fmt.Errorf("配置SSH密码前确认容器运行状态失败: %w", ensureErr)
	}

	if err := p.setContainerPasswordWithRetry(config.Name, password, shellType); err != nil {
		return fmt.Errorf("设置容器密码失败: %w", err)
	}

	global.APP_LOG.Info("Podman容器SSH密码配置成功",
		zap.String("instanceName", config.Name))

	if dbErr := global.APP_DB.Model(&providerModel.Instance{}).
		Where("name = ?", config.Name).
		Update("password", password).Error; dbErr != nil {
		global.APP_LOG.Warn("更新实例密码到数据库失败",
			zap.String("instanceName", config.Name),
			zap.Error(dbErr))
	}

	return nil
}

// getContainerPrivateIP 获取容器的内网IP地址
func (p *PodmanProvider) getContainerPrivateIP(containerName string) (string, error) {
	cmd := fmt.Sprintf("%s inspect %s --format '{{range $net, $config := .NetworkSettings.Networks}}{{$config.IPAddress}}{{end}}'", cliName, containerName)
	output, err := p.sshClient.Execute(cmd)
	if err == nil {
		ipAddress := utils.CleanCommandOutput(output)
		if ipAddress != "" && ipAddress != "<no value>" {
			return ipAddress, nil
		}
	}

	cmd = fmt.Sprintf("%s inspect %s --format '{{.NetworkSettings.IPAddress}}'", cliName, containerName)
	output, err = p.sshClient.Execute(cmd)
	if err == nil {
		ipAddress := utils.CleanCommandOutput(output)
		if ipAddress != "" && ipAddress != "<no value>" {
			return ipAddress, nil
		}
	}

	hostCmd := fmt.Sprintf("%s exec %s hostname -I 2>/dev/null", cliName, containerName)
	hostOutput, hostErr := p.sshClient.Execute(hostCmd)
	if hostErr == nil {
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
func (p *PodmanProvider) initializePmacctMonitoring(ctx context.Context, config provider.InstanceConfig) error {
	var providerRecord providerModel.Provider
	if err := global.APP_DB.Where("name = ?", p.config.Name).First(&providerRecord).Error; err != nil {
		return fmt.Errorf("查找provider记录失败: %w", err)
	}

	var instance providerModel.Instance
	if err := global.APP_DB.Where("name = ? AND provider_id = ?", config.Name, providerRecord.ID).First(&instance).Error; err != nil {
		return fmt.Errorf("查找实例记录失败: %w", err)
	}

	if !providerRecord.EnableTrafficControl {
		return nil
	}

	pmacctService := pmacct.NewService()
	if pmacctErr := pmacctService.InitializePmacctForInstance(instance.ID); pmacctErr != nil {
		return fmt.Errorf("初始化 pmacct 监控失败: %w", pmacctErr)
	}

	syncTrigger := traffic.NewSyncTriggerService()
	syncTrigger.TriggerInstanceTrafficSync(instance.ID, "Podman容器创建完成后初始化")

	return nil
}
