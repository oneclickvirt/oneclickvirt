package health

import (
	"fmt"
	"strings"

	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

// DetectStoragePoolPath 根据provider类型自动检测存储池路径
func (phc *ProviderHealthChecker) DetectStoragePoolPath(client *ssh.Client, providerType, storagePoolName string) (string, error) {
	switch strings.ToLower(providerType) {
	case "proxmox", "pve":
		return phc.detectProxmoxStoragePath(client, storagePoolName)
	case "lxd":
		return phc.detectLXDStoragePath(client, storagePoolName)
	case "incus":
		return phc.detectIncusStoragePath(client, storagePoolName)
	case "docker":
		return phc.detectDockerStoragePath(client)
	default:
		// 默认返回根目录
		if phc.logger != nil {
			phc.logger.Warn("未知的provider类型，使用根目录作为存储路径",
				zap.String("providerType", providerType))
		}
		return "/", nil
	}
}

// detectProxmoxStoragePath 检测Proxmox VE存储池路径
func (phc *ProviderHealthChecker) detectProxmoxStoragePath(client *ssh.Client, storagePoolName string) (string, error) {
	if storagePoolName == "" {
		storagePoolName = "local"
	}

	// 使用pvesm命令查询存储池路径
	cmd := fmt.Sprintf("pvesm path %s: 2>/dev/null | head -1", storagePoolName)
	output, err := phc.executeSSHCommand(client, cmd)
	if err == nil && strings.TrimSpace(output) != "" {
		path := strings.TrimSpace(output)
		// pvesm path返回的是完整路径，我们需要提取挂载点
		// 例如: /var/lib/vz/images/100/vm-100-disk-0.raw -> /var/lib/vz
		if idx := strings.Index(path, "/images/"); idx != -1 {
			path = path[:idx]
		}
		if phc.logger != nil {
			phc.logger.Info("检测到Proxmox存储池路径",
				zap.String("storagePool", storagePoolName),
				zap.String("path", path))
		}
		return path, nil
	}

	// 如果pvesm命令失败，尝试从配置文件读取
	cmd = fmt.Sprintf("grep -A 10 \"^%s:\" /etc/pve/storage.cfg 2>/dev/null | grep -E '^\\s+path' | awk '{print $2}' | head -1", storagePoolName)
	output, err = phc.executeSSHCommand(client, cmd)
	if err == nil && strings.TrimSpace(output) != "" {
		path := strings.TrimSpace(output)
		if phc.logger != nil {
			phc.logger.Info("从配置文件检测到Proxmox存储池路径",
				zap.String("storagePool", storagePoolName),
				zap.String("path", path))
		}
		return path, nil
	}

	// 默认路径
	defaultPaths := map[string]string{
		"local":     "/var/lib/vz",
		"local-lvm": "/dev/pve",
	}
	if defaultPath, ok := defaultPaths[storagePoolName]; ok {
		if phc.logger != nil {
			phc.logger.Info("使用Proxmox默认存储池路径",
				zap.String("storagePool", storagePoolName),
				zap.String("path", defaultPath))
		}
		return defaultPath, nil
	}

	return "/var/lib/vz", nil
}

// detectLXDStoragePath 检测LXD存储池路径
func (phc *ProviderHealthChecker) detectLXDStoragePath(client *ssh.Client, storagePoolName string) (string, error) {
	if storagePoolName == "" {
		storagePoolName = "default"
	}

	// 使用lxc storage info命令查询存储池路径
	cmd := fmt.Sprintf("lxc storage info %s 2>/dev/null | grep -E '^\\s+source:' | awk '{print $2}'", storagePoolName)
	output, err := phc.executeSSHCommand(client, cmd)
	if err == nil && strings.TrimSpace(output) != "" {
		path := strings.TrimSpace(output)
		if phc.logger != nil {
			phc.logger.Info("检测到LXD存储池路径",
				zap.String("storagePool", storagePoolName),
				zap.String("path", path))
		}
		return path, nil
	}

	// 尝试从配置目录获取
	cmd = "ls -d /var/lib/lxd/storage-pools/* 2>/dev/null | head -1"
	output, err = phc.executeSSHCommand(client, cmd)
	if err == nil && strings.TrimSpace(output) != "" {
		path := strings.TrimSpace(output)
		if phc.logger != nil {
			phc.logger.Info("从目录检测到LXD存储池路径",
				zap.String("path", path))
		}
		return path, nil
	}

	// 默认路径
	defaultPath := "/var/lib/lxd/storage-pools/default"
	if phc.logger != nil {
		phc.logger.Info("使用LXD默认存储池路径",
			zap.String("path", defaultPath))
	}
	return defaultPath, nil
}

// detectIncusStoragePath 检测Incus存储池路径
func (phc *ProviderHealthChecker) detectIncusStoragePath(client *ssh.Client, storagePoolName string) (string, error) {
	if storagePoolName == "" {
		storagePoolName = "default"
	}

	// 使用incus storage info命令查询存储池路径
	cmd := fmt.Sprintf("incus storage info %s 2>/dev/null | grep -E '^\\s+source:' | awk '{print $2}'", storagePoolName)
	output, err := phc.executeSSHCommand(client, cmd)
	if err == nil && strings.TrimSpace(output) != "" {
		path := strings.TrimSpace(output)
		if phc.logger != nil {
			phc.logger.Info("检测到Incus存储池路径",
				zap.String("storagePool", storagePoolName),
				zap.String("path", path))
		}
		return path, nil
	}

	// 尝试从配置目录获取
	cmd = "ls -d /var/lib/incus/storage-pools/* 2>/dev/null | head -1"
	output, err = phc.executeSSHCommand(client, cmd)
	if err == nil && strings.TrimSpace(output) != "" {
		path := strings.TrimSpace(output)
		if phc.logger != nil {
			phc.logger.Info("从目录检测到Incus存储池路径",
				zap.String("path", path))
		}
		return path, nil
	}

	// 默认路径
	defaultPath := "/var/lib/incus/storage-pools/default"
	if phc.logger != nil {
		phc.logger.Info("使用Incus默认存储池路径",
			zap.String("path", defaultPath))
	}
	return defaultPath, nil
}

// detectDockerStoragePath 检测Docker存储路径
func (phc *ProviderHealthChecker) detectDockerStoragePath(client *ssh.Client) (string, error) {
	// 尝试从docker info获取数据根目录
	cmd := "docker info 2>/dev/null | grep -E 'Docker Root Dir:|Data Root:' | awk -F': ' '{print $2}' | head -1"
	output, err := phc.executeSSHCommand(client, cmd)
	if err == nil && strings.TrimSpace(output) != "" {
		path := strings.TrimSpace(output)
		if phc.logger != nil {
			phc.logger.Info("检测到Docker存储路径",
				zap.String("path", path))
		}
		return path, nil
	}

	// 尝试从配置文件读取
	cmd = "grep -E '\"data-root\"|\"graph\"' /etc/docker/daemon.json 2>/dev/null | awk -F'\"' '{print $4}' | head -1"
	output, err = phc.executeSSHCommand(client, cmd)
	if err == nil && strings.TrimSpace(output) != "" {
		path := strings.TrimSpace(output)
		if phc.logger != nil {
			phc.logger.Info("从配置文件检测到Docker存储路径",
				zap.String("path", path))
		}
		return path, nil
	}

	// 默认路径
	defaultPath := "/var/lib/docker"
	if phc.logger != nil {
		phc.logger.Info("使用Docker默认存储路径",
			zap.String("path", defaultPath))
	}
	return defaultPath, nil
}

// getDiskInfoByPath 根据指定路径获取磁盘信息
func (phc *ProviderHealthChecker) getDiskInfoByPath(client *ssh.Client, path string) (total int64, free int64, err error) {
	// 如果没有指定路径，使用根目录
	if path == "" {
		path = "/"
	}

	// 获取磁盘信息 - 使用指定路径
	diskCmd := fmt.Sprintf("df -h %s 2>/dev/null | tail -1", path)
	diskInfo, err := phc.executeSSHCommand(client, diskCmd)
	if err != nil {
		if phc.logger != nil {
			phc.logger.Warn("df -h命令失败", zap.String("path", path), zap.Error(err))
		}
		return 0, 0, err
	}

	if phc.logger != nil {
		phc.logger.Debug("df -h命令输出", zap.String("path", path), zap.String("output", diskInfo))
	}

	// 解析df输出，格式：Filesystem Size Used Avail Use% Mounted on
	// 示例：/dev/sda1        25G   17G  7.2G  70% /
	fields := strings.Fields(strings.TrimSpace(diskInfo))
	if len(fields) >= 4 {
		// 第二个字段(index 1)是总空间Size，第四个字段(index 3)是可用空间Avail
		if totalSize := phc.parseDiskSize(fields[1]); totalSize > 0 {
			total = totalSize
		}
		if freeSize := phc.parseDiskSize(fields[3]); freeSize > 0 {
			free = freeSize
		}
		return total, free, nil
	}

	return 0, 0, fmt.Errorf("failed to parse disk info output")
}
