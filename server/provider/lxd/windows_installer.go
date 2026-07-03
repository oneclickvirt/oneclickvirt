package lxd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

func isLXDWindowsInstallerURL(imageURL string) bool {
	cleanURL := strings.ToLower(strings.TrimSpace(strings.Split(imageURL, "?")[0]))
	cleanURL = strings.TrimSuffix(cleanURL, "/download")
	return strings.Contains(cleanURL, ".iso") && strings.Contains(cleanURL, "windows") && !strings.Contains(cleanURL, "windows-virtio")
}

func (l *LXDProvider) shouldUseWindowsInstallerSSH(ctx context.Context, config *provider.InstanceConfig) bool {
	if config == nil || config.InstanceType != "vm" || config.CopyMode {
		return false
	}
	if isLXDWindowsInstallerURL(config.ImageURL) {
		return true
	}
	if config.ImageURL != "" {
		return false
	}
	systemConfig := &provider.InstanceConfig{
		Image:        config.Image,
		InstanceType: config.InstanceType,
	}
	if err := l.queryAndSetSystemImage(ctx, systemConfig); err != nil {
		return false
	}
	config.ImageURL = systemConfig.ImageURL
	config.UseCDN = systemConfig.UseCDN
	return isLXDWindowsInstallerURL(config.ImageURL)
}

func (l *LXDProvider) createWindowsInstallerVM(ctx context.Context, config provider.InstanceConfig, progressCallback provider.ProgressCallback) error {
	updateProgress := func(percentage int, message string) {
		if progressCallback != nil {
			progressCallback(percentage, message)
		}
		global.APP_LOG.Debug("LXD Windows安装型VM创建进度",
			zap.String("instance", config.Name),
			zap.Int("percentage", percentage),
			zap.String("message", message))
	}

	if exists, err := l.instanceExists(config.Name); err != nil {
		return fmt.Errorf("检查实例是否存在失败: %w", err)
	} else if exists {
		return fmt.Errorf("实例 %s 已存在", config.Name)
	}

	updateProgress(18, "下载Windows安装镜像...")
	isoPath, err := l.downloadImageToRemote(config.ImageURL, config.Image, l.config.Country, l.getCurrentArchitecture(), config.InstanceType, config.UseCDN)
	if err != nil {
		return fmt.Errorf("下载Windows ISO失败: %w", err)
	}

	updateProgress(28, "修补Windows安装镜像...")
	repackedISO, err := l.repackWindowsInstallerISO(isoPath)
	if err != nil {
		return err
	}

	updateProgress(42, "创建空白Windows虚拟机...")
	configParams := []string{"security.secureboot=false"}
	if config.CPU != "" {
		configParams = append(configParams, fmt.Sprintf("limits.cpu=%s", config.CPU))
	}
	if config.Memory != "" {
		configParams = append(configParams, fmt.Sprintf("limits.memory=%s", convertMemoryFormat(config.Memory)))
	}
	cmd := fmt.Sprintf("lxc init %s --empty --vm -s %s", shellSingleQuote(config.Name), shellSingleQuote(lxdStoragePoolArg(l.resolveStoragePoolForInstance())))
	for _, param := range configParams {
		cmd += fmt.Sprintf(" -c %s", shellSingleQuote(param))
	}
	if output, err := l.executeCreateCommand(cmd, config.InstanceType); err != nil {
		return fmt.Errorf("创建Windows空白虚拟机失败: %s: %w", utils.TruncateString(output, 500), err)
	}

	if config.Disk != "" {
		updateProgress(50, "配置Windows虚拟机磁盘...")
		if _, err := l.sshClient.Execute(fmt.Sprintf("lxc config device override %s root size=%s", shellSingleQuote(config.Name), shellSingleQuote(convertDiskFormat(config.Disk)))); err != nil {
			global.APP_LOG.Warn("配置Windows VM磁盘大小失败，将继续",
				zap.String("instance", config.Name),
				zap.Error(err))
		}
	}

	updateProgress(58, "挂载Windows安装盘...")
	addISOCommand := fmt.Sprintf("lxc config device add %s install disk source=%s boot.priority=10", shellSingleQuote(config.Name), shellSingleQuote(repackedISO))
	if _, err := l.sshClient.Execute(addISOCommand); err != nil {
		return fmt.Errorf("挂载Windows安装盘失败: %w", err)
	}

	updateProgress(68, "启动Windows安装型虚拟机...")
	if err := l.sshStartInstance(ctx, config.Name); err != nil {
		return fmt.Errorf("启动Windows安装型虚拟机失败: %w", err)
	}
	if err := l.waitForWindowsInstallerState(config.Name, "RUNNING", 60); err != nil {
		global.APP_LOG.Warn("等待Windows安装型虚拟机启动超时，但继续完成任务",
			zap.String("instance", config.Name),
			zap.Error(err))
	}

	updateProgress(100, "Windows安装型虚拟机创建完成")
	return nil
}

func (l *LXDProvider) repackWindowsInstallerISO(isoPath string) (string, error) {
	repackedPath := strings.TrimSuffix(isoPath, ".iso") + ".lxd.iso"
	if l.isRemoteFileValid(repackedPath) {
		return repackedPath, nil
	}
	script := fmt.Sprintf(`set -e
src=%s
dst=%s
if ! command -v distrobuilder >/dev/null 2>&1; then
  echo "distrobuilder not found; install distrobuilder on the LXD node" >&2
  exit 127
fi
tmp="${dst}.tmp"
rm -f "$tmp"
distrobuilder repack-windows "$src" "$tmp"
mv "$tmp" "$dst"
`, shellSingleQuote(isoPath), shellSingleQuote(repackedPath))
	output, err := l.sshClient.ExecuteViaTempScript(script, nil, 6*time.Hour)
	if err != nil {
		return "", fmt.Errorf("修补Windows安装镜像失败: %s: %w", utils.TruncateString(output, 500), err)
	}
	return repackedPath, nil
}

func (l *LXDProvider) waitForWindowsInstallerState(name, expectedState string, timeoutSeconds int) error {
	for elapsed := 0; elapsed < timeoutSeconds; elapsed += 3 {
		cmd := fmt.Sprintf("lxc info %s | awk -F': ' '/^Status:/{print $2; exit}'", shellSingleQuote(name))
		output, err := l.sshClient.Execute(cmd)
		if err == nil && strings.EqualFold(strings.TrimSpace(output), expectedState) {
			return nil
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("等待实例状态超时: %s", expectedState)
}
