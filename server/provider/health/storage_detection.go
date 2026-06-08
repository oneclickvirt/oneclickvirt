package health

import (
	"fmt"
	"oneclickvirt/utils"
	"strings"

	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

func storageShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// DetectStoragePoolPath 根据provider类型自动检测存储池路径
func (phc *ProviderHealthChecker) DetectStoragePoolPath(client *ssh.Client, providerType, storagePoolName string) (string, error) {
	switch strings.ToLower(providerType) {
	case "proxmox", "pve":
		return phc.detectProxmoxStoragePath(client, storagePoolName)
	case "lxd":
		return phc.detectLXDStoragePath(client, storagePoolName)
	case "incus":
		return phc.detectIncusStoragePath(client, storagePoolName)
	case "docker", "orbstack":
		return phc.detectDockerStoragePath(client)
	case "podman", "containerd", "nerdctl":
		// podman/containerd 的镜像存储通常在 /var/lib/containers 或 /var/lib/containerd
		return phc.detectContainerStoragePath(client, providerType)
	case "qemu":
		return "/var/lib/libvirt/images", nil
	case "kubevirt":
		return "/var/lib/libvirt/images", nil
	case "vmware":
		return "/var/lib/oneclickvirt/vmware", nil
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
	if err == nil && utils.CleanCommandOutput(output) != "" {
		path := utils.CleanCommandOutput(output)
		// pvesm path返回的是完整路径，需要提取挂载点
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
	if err == nil && utils.CleanCommandOutput(output) != "" {
		path := utils.CleanCommandOutput(output)
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
	if storagePoolName == "" || storagePoolName == "local" {
		storagePoolName = phc.DetectLXDStoragePoolName(client)
	}

	if storagePoolName != "" {
		// 使用 lxc storage info 命令查询存储池路径。pool 名称必须先来自真实存在的池，避免把 default/local 占位符当成有效池。
		cmd := fmt.Sprintf("lxc storage info %s 2>/dev/null | grep -E '^\\s+source:' | awk '{print $2}'", storageShellQuote(storagePoolName))
		output, err := phc.executeSSHCommand(client, cmd)
		if err == nil && utils.CleanCommandOutput(output) != "" {
			path := utils.CleanCommandOutput(output)
			if phc.logger != nil {
				phc.logger.Info("检测到LXD存储池路径",
					zap.String("storagePool", storagePoolName),
					zap.String("path", path))
			}
			return path, nil
		}
	}

	// 尝试从配置目录获取
	cmd := "ls -d /var/lib/lxd/storage-pools/* 2>/dev/null | head -1"
	output, err := phc.executeSSHCommand(client, cmd)
	if err == nil && utils.CleanCommandOutput(output) != "" {
		path := utils.CleanCommandOutput(output)
		if phc.logger != nil {
			phc.logger.Info("从目录检测到LXD存储池路径",
				zap.String("path", path))
		}
		return path, nil
	}

	return "", fmt.Errorf("no usable LXD storage pool path found")
}

// detectIncusStoragePath 检测Incus存储池路径
func (phc *ProviderHealthChecker) detectIncusStoragePath(client *ssh.Client, storagePoolName string) (string, error) {
	if storagePoolName == "" || storagePoolName == "local" {
		storagePoolName = phc.DetectIncusStoragePoolName(client)
	}

	if storagePoolName != "" {
		// 使用 incus storage info 命令查询存储池路径。pool 名称必须先来自真实存在的池，避免把 default/local 占位符当成有效池。
		cmd := fmt.Sprintf("incus storage info %s 2>/dev/null | grep -E '^\\s+source:' | awk '{print $2}'", storageShellQuote(storagePoolName))
		output, err := phc.executeSSHCommand(client, cmd)
		if err == nil && utils.CleanCommandOutput(output) != "" {
			path := utils.CleanCommandOutput(output)
			if phc.logger != nil {
				phc.logger.Info("检测到Incus存储池路径",
					zap.String("storagePool", storagePoolName),
					zap.String("path", path))
			}
			return path, nil
		}
	}

	// 尝试从配置目录获取
	cmd := "ls -d /var/lib/incus/storage-pools/* 2>/dev/null | head -1"
	output, err := phc.executeSSHCommand(client, cmd)
	if err == nil && utils.CleanCommandOutput(output) != "" {
		path := utils.CleanCommandOutput(output)
		if phc.logger != nil {
			phc.logger.Info("从目录检测到Incus存储池路径",
				zap.String("path", path))
		}
		return path, nil
	}

	return "", fmt.Errorf("no usable Incus storage pool path found")
}

// detectDockerStoragePath 检测Docker存储路径
func (phc *ProviderHealthChecker) detectDockerStoragePath(client *ssh.Client) (string, error) {
	// 尝试从docker info获取数据根目录
	cmd := "docker info 2>/dev/null | grep -E 'Docker Root Dir:|Data Root:' | awk -F': ' '{print $2}' | head -1"
	output, err := phc.executeSSHCommand(client, cmd)
	if err == nil && utils.CleanCommandOutput(output) != "" {
		path := utils.CleanCommandOutput(output)
		if phc.logger != nil {
			phc.logger.Info("检测到Docker存储路径",
				zap.String("path", path))
		}
		return path, nil
	}

	// 尝试从配置文件读取
	cmd = "grep -E '\"data-root\"|\"graph\"' /etc/docker/daemon.json 2>/dev/null | awk -F'\"' '{print $4}' | head -1"
	output, err = phc.executeSSHCommand(client, cmd)
	if err == nil && utils.CleanCommandOutput(output) != "" {
		path := utils.CleanCommandOutput(output)
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

// detectContainerStoragePath 检测 Podman / Containerd 的存储路径
func (phc *ProviderHealthChecker) detectContainerStoragePath(client *ssh.Client, providerType string) (string, error) {
	switch strings.ToLower(providerType) {
	case "podman":
		// podman info 显示 graphRoot
		cmd := "podman info 2>/dev/null | grep -E 'graphRoot:|GraphRoot:' | awk -F': ' '{print $2}' | head -1"
		output, err := phc.executeSSHCommand(client, cmd)
		if err == nil && utils.CleanCommandOutput(output) != "" {
			path := utils.CleanCommandOutput(output)
			if phc.logger != nil {
				phc.logger.Info("检测到Podman存储路径", zap.String("path", path))
			}
			return path, nil
		}
		// 默认路径（rootful）
		defaultPath := "/var/lib/containers"
		if phc.logger != nil {
			phc.logger.Info("使用Podman默认存储路径", zap.String("path", defaultPath))
		}
		return defaultPath, nil
	case "containerd", "nerdctl":
		// nerdctl/containerd 数据根目录
		cmd := "nerdctl info 2>/dev/null | grep -E 'Docker Root Dir:|Data Root:' | awk -F': ' '{print $2}' | head -1"
		output, err := phc.executeSSHCommand(client, cmd)
		if err == nil && utils.CleanCommandOutput(output) != "" {
			path := utils.CleanCommandOutput(output)
			if phc.logger != nil {
				phc.logger.Info("检测到containerd(nerdctl)存储路径", zap.String("path", path))
			}
			return path, nil
		}
		// 通过 containerd 配置查找
		cmd = "grep -E 'root\\s*=' /etc/containerd/config.toml 2>/dev/null | awk -F'\"' '{print $2}' | head -1"
		output, err = phc.executeSSHCommand(client, cmd)
		if err == nil && utils.CleanCommandOutput(output) != "" {
			path := utils.CleanCommandOutput(output)
			if phc.logger != nil {
				phc.logger.Info("从配置文件检测到containerd存储路径", zap.String("path", path))
			}
			return path, nil
		}
		defaultPath := "/var/lib/containerd"
		if phc.logger != nil {
			phc.logger.Info("使用containerd默认存储路径", zap.String("path", defaultPath))
		}
		return defaultPath, nil
	}
	return "/", nil
}

// ── LXD/Incus 存储池名称检测 ──────────────────────────────────────────────

// DetectLXDStoragePoolName 检测 LXD 存储池名称（返回第一个真实可用池；未检测到则返回空字符串）
func (phc *ProviderHealthChecker) DetectLXDStoragePoolName(client *ssh.Client) string {
	// 获取所有存储池列表，取第一个
	cmd := "{ lxc storage list --format csv -c n 2>/dev/null || lxc storage list --format csv 2>/dev/null | cut -d, -f1; } | awk 'NF {print; exit}'"
	output, err := phc.executeSSHCommand(client, cmd)
	if err == nil && utils.CleanCommandOutput(output) != "" {
		name := utils.CleanCommandOutput(output)
		if phc.logger != nil {
			phc.logger.Info("检测到LXD存储池名称", zap.String("pool", name))
		}
		return name
	}
	return ""
}

// DetectIncusStoragePoolName 检测 Incus 存储池名称（返回第一个真实可用池；未检测到则返回空字符串）
func (phc *ProviderHealthChecker) DetectIncusStoragePoolName(client *ssh.Client) string {
	cmd := "{ incus storage list --format csv -c n 2>/dev/null || incus storage list --format csv 2>/dev/null | cut -d, -f1; } | awk 'NF {print; exit}'"
	output, err := phc.executeSSHCommand(client, cmd)
	if err == nil && utils.CleanCommandOutput(output) != "" {
		name := utils.CleanCommandOutput(output)
		if phc.logger != nil {
			phc.logger.Info("检测到Incus存储池名称", zap.String("pool", name))
		}
		return name
	}
	return ""
}

// DetectStoragePoolName 统一入口：根据 provider 类型检测存储池名称
func (phc *ProviderHealthChecker) DetectStoragePoolName(client *ssh.Client, providerType string) string {
	switch strings.ToLower(providerType) {
	case "lxd":
		return phc.DetectLXDStoragePoolName(client)
	case "incus":
		return phc.DetectIncusStoragePoolName(client)
	default:
		return ""
	}
}

// ── LXD/Incus Profile Root 设备检测与修复 ──────────────────────────────────

// EnsureProfileHasRootDevice 确保 LXD/Incus 的 default profile 包含 root 磁盘设备
// 返回 true 表示进行了修复（添加了 root 设备）
func (phc *ProviderHealthChecker) EnsureProfileHasRootDevice(client *ssh.Client, providerType string, preferredPool ...string) (fixed bool) {
	switch strings.ToLower(providerType) {
	case "lxd":
		return phc.ensureLXDProfileRootDevice(client, preferredPool...)
	case "incus":
		return phc.ensureIncusProfileRootDevice(client, preferredPool...)
	}
	return false
}

func (phc *ProviderHealthChecker) ensureLXDProfileRootDevice(client *ssh.Client, preferredPool ...string) bool {
	// 检查 default profile 是否有 root 设备
	checkCmd := "lxc profile show default 2>/dev/null | grep -A3 'root:' | head -4"
	output, err := phc.executeSSHCommand(client, checkCmd)
	if err == nil && strings.Contains(output, "type: disk") {
		return false // root 设备已存在
	}

	// 检测存储池名称。优先使用上游已验证可用的池，避免多池环境下误回落到第一个池。
	poolName := ""
	if len(preferredPool) > 0 {
		poolName = strings.TrimSpace(preferredPool[0])
	}
	if poolName == "" {
		poolName = phc.DetectLXDStoragePoolName(client)
	}
	if poolName == "" {
		if phc.logger != nil {
			phc.logger.Warn("无法添加root设备到default profile：未检测到可用LXD存储池")
		}
		return false
	}

	// 添加 root 设备到 default profile
	addCmd := fmt.Sprintf("lxc profile device add default root disk path=/ pool=%s 2>/dev/null", storageShellQuote(poolName))
	_, err = phc.executeSSHCommand(client, addCmd)
	if err != nil {
		// 不指定 pool 重试
		addCmd2 := "lxc profile device add default root disk path=/ 2>/dev/null"
		_, err = phc.executeSSHCommand(client, addCmd2)
		if err != nil {
			if phc.logger != nil {
				phc.logger.Warn("无法添加root设备到default profile",
					zap.String("pool", poolName),
					zap.Error(err))
			}
			return false
		}
	}

	if phc.logger != nil {
		phc.logger.Info("已自动添加root设备到default profile",
			zap.String("pool", poolName))
	}
	return true
}

func (phc *ProviderHealthChecker) ensureIncusProfileRootDevice(client *ssh.Client, preferredPool ...string) bool {
	checkCmd := "incus profile show default 2>/dev/null | grep -A3 'root:' | head -4"
	output, err := phc.executeSSHCommand(client, checkCmd)
	if err == nil && strings.Contains(output, "type: disk") {
		return false
	}

	poolName := ""
	if len(preferredPool) > 0 {
		poolName = strings.TrimSpace(preferredPool[0])
	}
	if poolName == "" {
		poolName = phc.DetectIncusStoragePoolName(client)
	}
	if poolName == "" {
		if phc.logger != nil {
			phc.logger.Warn("无法添加root设备到default profile：未检测到可用Incus存储池")
		}
		return false
	}

	addCmd := fmt.Sprintf("incus profile device add default root disk path=/ pool=%s 2>/dev/null", storageShellQuote(poolName))
	_, err = phc.executeSSHCommand(client, addCmd)
	if err != nil {
		addCmd2 := "incus profile device add default root disk path=/ 2>/dev/null"
		_, err = phc.executeSSHCommand(client, addCmd2)
		if err != nil {
			if phc.logger != nil {
				phc.logger.Warn("无法添加root设备到default profile",
					zap.String("pool", poolName),
					zap.Error(err))
			}
			return false
		}
	}

	if phc.logger != nil {
		phc.logger.Info("已自动添加root设备到default profile",
			zap.String("pool", poolName))
	}
	return true
}

// ── 存储池存在性检查与自动对齐修复 ────────────────────────────────────────

// storagePoolExists 检查 LXD/Incus 存储池是否存在
func (phc *ProviderHealthChecker) storagePoolExists(client *ssh.Client, providerType, poolName string) bool {
	poolName = strings.TrimSpace(poolName)
	if poolName == "" {
		return false
	}
	var cmd string
	if strings.ToLower(providerType) == "incus" {
		cmd = fmt.Sprintf("incus storage info %s >/dev/null 2>&1 && echo 'yes' || echo 'no'", storageShellQuote(poolName))
	} else {
		cmd = fmt.Sprintf("lxc storage info %s >/dev/null 2>&1 && echo 'yes' || echo 'no'", storageShellQuote(poolName))
	}
	output, err := phc.executeSSHCommand(client, cmd)
	if err != nil {
		return false
	}
	return strings.TrimSpace(output) == "yes"
}

// ResolveStoragePoolNameForProvider 验证并解析 LXD/Incus 实际可用的存储池。
// 规则：用户/数据库已有配置真实存在时保留；为空、local/default 占位符不存在、或配置指向不存在的池时，回退到远端第一个真实存在的池。
func (phc *ProviderHealthChecker) ResolveStoragePoolNameForProvider(client *ssh.Client, providerType, configuredPool string) string {
	pt := strings.ToLower(providerType)
	if pt != "lxd" && pt != "incus" {
		return strings.TrimSpace(configuredPool)
	}

	configuredPool = strings.TrimSpace(configuredPool)
	if configuredPool != "" && phc.storagePoolExists(client, providerType, configuredPool) {
		if phc.logger != nil {
			phc.logger.Info("保留已配置且可用的存储池",
				zap.String("providerType", providerType),
				zap.String("pool", configuredPool))
		}
		return configuredPool
	}

	detectedPool := phc.detectFirstStoragePool(client, providerType)
	if detectedPool != "" {
		if phc.logger != nil {
			phc.logger.Warn("存储池配置不可用，自动切换到远端真实存在的存储池",
				zap.String("providerType", providerType),
				zap.String("configuredPool", configuredPool),
				zap.String("detectedPool", detectedPool))
		}
		return detectedPool
	}

	if phc.logger != nil {
		phc.logger.Error("未检测到任何可用的 LXD/Incus 存储池",
			zap.String("providerType", providerType),
			zap.String("configuredPool", configuredPool))
	}
	return ""
}

// detectFirstStoragePool 检测 LXD/Incus 第一个可用存储池
// 用于 profile 引用不存在的池时，回退到实际存在的池
func (phc *ProviderHealthChecker) detectFirstStoragePool(client *ssh.Client, providerType string) string {
	return phc.DetectStoragePoolName(client, providerType)
}

// getProfileRootPool 获取 default profile 中 root 设备引用的存储池名称
func (phc *ProviderHealthChecker) getProfileRootPool(client *ssh.Client, providerType string) string {
	var cmd string
	if strings.ToLower(providerType) == "incus" {
		cmd = "incus profile device get default root pool 2>/dev/null"
	} else {
		cmd = "lxc profile device get default root pool 2>/dev/null"
	}
	output, err := phc.executeSSHCommand(client, cmd)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(output)
}

// fixProfileRootPool 修复 profile root 设备的 pool 指向
func (phc *ProviderHealthChecker) fixProfileRootPool(client *ssh.Client, providerType, poolName string) {
	var cmd string
	if strings.ToLower(providerType) == "incus" {
		cmd = fmt.Sprintf("incus profile device set default root pool=%s 2>/dev/null || { incus profile device remove default root 2>/dev/null; incus profile device add default root disk path=/ pool=%s 2>/dev/null; }", storageShellQuote(poolName), storageShellQuote(poolName))
	} else {
		cmd = fmt.Sprintf("lxc profile device set default root pool=%s 2>/dev/null || { lxc profile device remove default root 2>/dev/null; lxc profile device add default root disk path=/ pool=%s 2>/dev/null; }", storageShellQuote(poolName), storageShellQuote(poolName))
	}
	phc.executeSSHCommand(client, cmd)
}

// detectLXDEnvironment 检测 LXD 环境：判断是否为 snap 安装、lxc 命令路径
// 返回 (isSnap, lxcPath, lxdPath)
func (phc *ProviderHealthChecker) detectLXDEnvironment(client *ssh.Client) (isSnap bool, lxcPath, lxdPath string) {
	// 检查 snap lxc
	output, err := phc.executeSSHCommand(client, "which /snap/bin/lxc 2>/dev/null || true")
	if err == nil && strings.TrimSpace(output) != "" {
		isSnap = true
		lxcPath = "/snap/bin/lxc"
		lxdPath = "/snap/bin/lxd"
		return
	}
	// 检查普通 lxc
	output, err = phc.executeSSHCommand(client, "which lxc 2>/dev/null || true")
	if err == nil {
		lxcPath = strings.TrimSpace(output)
	}
	output, err = phc.executeSSHCommand(client, "which lxd 2>/dev/null || true")
	if err == nil {
		lxdPath = strings.TrimSpace(output)
	}
	return
}

// EnsureProviderStorageReady LXD/Incus 存储环境完整就绪检查：
// 1. 检测是否有存储池 → 无池则记录错误（不自动创建 dir，应由管理员通过脚本创建 btrfs/lvm/zfs 池）
// 2. profile 引用的 pool 不存在但有其他可用池 → 自动对齐修复
// 3. profile 无 root 设备 → 自动添加（使用现有池或默认 "default"）
func (phc *ProviderHealthChecker) EnsureProviderStorageReady(client *ssh.Client, providerType string, preferredPool ...string) (fixed bool) {
	pt := strings.ToLower(providerType)
	if pt != "lxd" && pt != "incus" {
		return false
	}
	configuredPool := ""
	if len(preferredPool) > 0 {
		configuredPool = preferredPool[0]
	}

	// 步骤 1：检测存储池
	poolName := phc.ResolveStoragePoolNameForProvider(client, providerType, configuredPool)
	if poolName == "" {
		// 没有任何存储池 — 严重问题，记录明确错误引导管理员
		isSnap, lxcPath, _ := phc.detectLXDEnvironment(client)
		if phc.logger != nil {
			actionHint := "请通过 oneclickvirt/lxd 安装脚本创建存储池（推荐 btrfs/lvm/zfs），不要手动创建 dir 池"
			if isSnap {
				actionHint += fmt.Sprintf("；检测到 snap LXD (%s)，请确保 Provider SSH 环境可访问 snap 命令", lxcPath)
			}
			actionHint += "；创建后请在面板触发 Provider 健康检查/资源同步"
			phc.logger.Error("Provider 无任何 LXD/Incus 存储池，无法创建实例",
				zap.String("providerType", providerType),
				zap.Bool("isSnap", isSnap),
				zap.String("lxcPath", lxcPath),
				zap.String("action", actionHint))
		}
		return false
	}

	// 步骤 2：检查 profile root 设备引用的 pool 是否存在
	profilePoolName := phc.getProfileRootPool(client, providerType)
	if profilePoolName != "" && profilePoolName != poolName && !phc.storagePoolExists(client, providerType, profilePoolName) {
		// profile 引用的 pool (如 "default") 不存在，但存在另一个可用池 (如 "local")
		if phc.logger != nil {
			phc.logger.Warn("profile 引用的存储池不存在，自动对齐到现有池",
				zap.String("providerType", providerType),
				zap.String("profilePool", profilePoolName),
				zap.String("actualPool", poolName))
		}
		phc.fixProfileRootPool(client, providerType, poolName)
		fixed = true
	}

	// 步骤 3：确保 profile 有 root 设备
	if deviceFixed := phc.EnsureProfileHasRootDevice(client, providerType, poolName); deviceFixed {
		fixed = true
	}

	return fixed
}
