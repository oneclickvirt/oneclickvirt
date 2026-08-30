package proxmox

import (
	"context"
	"crypto/md5"
	"fmt"
	"strings"
	"time"

	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"oneclickvirt/global"
)

func isProxmoxInstallerImageURL(imageURL string) bool {
	cleanURL := strings.ToLower(strings.TrimSpace(strings.Split(imageURL, "?")[0]))
	cleanURL = strings.TrimSuffix(cleanURL, "/download")
	return strings.Contains(cleanURL, ".iso") && !strings.HasSuffix(cleanURL, ".qcow2")
}

func (p *ProxmoxProvider) shouldForceSSHForInstaller(ctx context.Context, config *provider.InstanceConfig) bool {
	if config == nil || config.InstanceType != "vm" || config.CopyMode {
		return false
	}
	if isProxmoxInstallerImageURL(config.ImageURL) {
		return true
	}
	if config.ImageURL != "" {
		return false
	}
	systemConfig := &provider.InstanceConfig{
		Image:        config.Image,
		InstanceType: config.InstanceType,
	}
	if err := p.queryAndSetSystemImage(ctx, systemConfig); err != nil {
		return false
	}
	config.ImageURL = systemConfig.ImageURL
	config.UseCDN = systemConfig.UseCDN
	return isProxmoxInstallerImageURL(config.ImageURL)
}

func (p *ProxmoxProvider) createInstallerVM(ctx context.Context, vmid int, config provider.InstanceConfig, imageURL string, useCDN bool, updateProgress func(int, string)) error {
	updateProgress(20, "准备安装镜像...")
	isoPath, isoFileName, err := p.prepareInstallerISO(imageURL, config.Image, useCDN)
	if err != nil {
		return err
	}

	updateProgress(35, "获取系统架构和KVM支持...")
	systemArchOutput, err := p.sshClient.Execute("uname -m")
	if err != nil {
		return fmt.Errorf("获取系统架构失败: %v", err)
	}
	systemArch := strings.TrimSpace(systemArchOutput)
	kvmFlag := "--kvm 1"
	cpuType := "host"
	kvmOutput, _ := p.sshClient.Execute("[ -e /dev/kvm ] && [ -r /dev/kvm ] && [ -w /dev/kvm ] && echo 'kvm_available' || echo 'kvm_unavailable'")
	if strings.TrimSpace(kvmOutput) != "kvm_available" {
		kvmFlag = "--kvm 0"
		p.kvmUnavailable = true
		switch systemArch {
		case "aarch64", "armv7l", "armv8", "armv8l":
			cpuType = "max"
		case "i386", "i686", "x86":
			cpuType = "qemu32"
		default:
			cpuType = "qemu64"
		}
	}

	kvmAvailable := !p.kvmUnavailable
	if err := global.APP_DB.Model(&providerModel.Provider{}).Where("id = ?", p.config.ID).Update("pve_kvm_available", &kvmAvailable).Error; err != nil {
		global.APP_LOG.Warn("更新KVM可用性状态失败", zap.Error(err))
	}

	updateProgress(45, "创建安装型虚拟机...")
	cpuFormatted := convertCPUFormat(config.CPU)
	if cpuFormatted == "" {
		cpuFormatted = "1"
	}
	memoryFormatted := convertMemoryFormat(config.Memory)
	if memoryFormatted == "" {
		memoryFormatted = "4096"
	}
	diskFormatted := convertDiskFormat(config.Disk)
	if diskFormatted == "" {
		diskFormatted = "40"
	}

	var providerRecord providerModel.Provider
	if err := global.APP_DB.Where("id = ?", p.config.ID).First(&providerRecord).Error; err != nil {
		global.APP_LOG.Warn("获取Provider记录失败，使用默认存储", zap.Error(err))
	}
	storage := providerRecord.StoragePool
	if storage == "" {
		storage = "local"
	}

	ostype := proxmoxInstallerOSType(imageURL)
	networkConfig := p.parseNetworkConfigFromInstanceConfig(config)
	if err := p.preflightIPv6Create(ctx, config, networkConfig.NetworkType); err != nil {
		return err
	}
	net0Config := fmt.Sprintf("e1000,bridge=%s,firewall=0", p.getBridgeName("nat"))
	createCmd := fmt.Sprintf(
		"qm create %d --agent 0 --cores %s --sockets 1 --cpu %s --memory %s --net0 %s --ostype %s --name %s %s",
		vmid, cpuFormatted, cpuType, memoryFormatted, shellSingleQuote(net0Config), shellSingleQuote(ostype), shellSingleQuote(config.Name), kvmFlag,
	)
	if _, err := p.sshClient.Execute(createCmd); err != nil {
		return fmt.Errorf("创建安装型虚拟机失败: %v", err)
	}

	updateProgress(55, "配置安装盘和空白磁盘...")
	diskArg := fmt.Sprintf("%s:%s", storage, diskFormatted)
	if _, err := p.sshClient.Execute(fmt.Sprintf("qm set %d --sata0 %s", vmid, shellSingleQuote(diskArg))); err != nil {
		return fmt.Errorf("创建空白系统盘失败: %v", err)
	}
	cdromArg := fmt.Sprintf("local:iso/%s,media=cdrom", isoFileName)
	if _, err := p.sshClient.Execute(fmt.Sprintf("qm set %d --ide2 %s", vmid, shellSingleQuote(cdromArg))); err != nil {
		return fmt.Errorf("挂载安装ISO失败: %v", err)
	}
	if _, err := p.sshClient.Execute(fmt.Sprintf("qm set %d --boot order=ide2", vmid)); err != nil {
		return fmt.Errorf("设置安装盘启动失败: %v", err)
	}

	updateProgress(80, "启动安装型虚拟机...")
	if _, err := p.sshClient.Execute(fmt.Sprintf("qm start %d", vmid)); err != nil {
		return fmt.Errorf("启动安装型虚拟机失败: %v", err)
	}

	updateProgress(90, "等待安装器启动...")
	maxWaitTime := p.waitScale(90 * time.Second)
	checkInterval := p.waitScale(3 * time.Second)
	startTime := time.Now()
	for time.Since(startTime) < maxWaitTime {
		statusOutput, err := p.sshClient.Execute(fmt.Sprintf("qm status %d", vmid))
		if err == nil && strings.Contains(statusOutput, "status: running") {
			updateProgress(100, "安装型虚拟机创建完成")
			global.APP_LOG.Info("Proxmox安装型虚拟机创建成功",
				zap.Int("vmid", vmid),
				zap.String("imageURL", utils.TruncateString(imageURL, 100)),
				zap.String("isoPath", isoPath))
			return nil
		}
		time.Sleep(checkInterval)
	}

	statusOutput, statusErr := p.sshClient.Execute(fmt.Sprintf("qm status %d", vmid))
	if statusErr != nil {
		return fmt.Errorf("等待安装型虚拟机 %d 启动超时（最后状态查询失败: %v）", vmid, statusErr)
	}
	return fmt.Errorf("等待安装型虚拟机 %d 启动超时（最后状态: %s）", vmid, strings.TrimSpace(statusOutput))
}

func (p *ProxmoxProvider) prepareInstallerISO(imageURL, imageName string, useCDN bool) (string, string, error) {
	isoDir := "/var/lib/vz/template/iso"
	if _, err := p.sshClient.Execute(fmt.Sprintf("mkdir -p %s", shellSingleQuote(isoDir))); err != nil {
		return "", "", fmt.Errorf("创建PVE ISO目录失败: %v", err)
	}

	archiveFileName, finalISOFileName := proxmoxInstallerFileNames(imageName, imageURL, p.config.Architecture)
	archivePath := isoDir + "/" + archiveFileName
	finalISOPath := isoDir + "/" + finalISOFileName

	if p.isRemoteFileValid(finalISOPath) {
		return finalISOPath, finalISOFileName, nil
	}

	if !p.isRemoteFileValid(archivePath) {
		downloadURL := p.getDownloadURL(imageURL, useCDN)
		tmpPath := archivePath + ".tmp"
		output, err := p.downloadRemoteFileWithFallback(downloadURL, imageURL, tmpPath, archivePath, 4*time.Hour)
		if err != nil {
			p.sshClient.Execute(fmt.Sprintf("rm -f %s", shellSingleQuote(tmpPath)))
			return "", "", fmt.Errorf("下载安装镜像失败: %s: %w", utils.TruncateString(output, 300), err)
		}
	}

	if archivePath == finalISOPath {
		return finalISOPath, finalISOFileName, nil
	}

	extractScript := fmt.Sprintf(`set -e
archive=%s
dst=%s
work="${archive}.extract"
sevenzip="$(command -v 7z || command -v 7za || true)"
if [ -z "$sevenzip" ]; then
  echo "7z/7za not found; install p7zip-full on the Proxmox node" >&2
  exit 127
fi
rm -rf "$work"
mkdir -p "$work"
"$sevenzip" x -y "$archive" "-o$work"
iso="$(find "$work" -type f -iname '*.iso' | sort | head -n 1)"
if [ -z "$iso" ]; then
  echo "no ISO file found after extracting $archive" >&2
  exit 1
fi
tmp="${dst}.tmp"
mv "$iso" "$tmp"
mv "$tmp" "$dst"
rm -rf "$work"
`, shellSingleQuote(archivePath), shellSingleQuote(finalISOPath))
	output, err := p.sshClient.ExecuteViaTempScript(extractScript, nil, 2*time.Hour)
	if err != nil {
		return "", "", fmt.Errorf("解压安装镜像失败: %s: %w", utils.TruncateString(output, 300), err)
	}
	return finalISOPath, finalISOFileName, nil
}

func proxmoxInstallerFileNames(imageName, imageURL, architecture string) (string, string) {
	cleanURL := strings.TrimSpace(strings.Split(imageURL, "?")[0])
	cleanURL = strings.TrimSuffix(cleanURL, "/download")
	lowerURL := strings.ToLower(cleanURL)
	extension := ".iso"
	if strings.HasSuffix(lowerURL, ".iso.7z") {
		extension = ".iso.7z"
	}

	combined := fmt.Sprintf("%s_%s_%s", imageName, imageURL, architecture)
	hash := fmt.Sprintf("%x", md5.Sum([]byte(combined)))[:8]
	safeName := strings.NewReplacer("/", "_", ":", "_", " ", "_").Replace(imageName)
	if safeName == "" {
		safeName = "installer"
	}

	archiveName := fmt.Sprintf("%s_%s%s", safeName, hash, extension)
	finalName := strings.TrimSuffix(archiveName, ".7z")
	return archiveName, finalName
}

func proxmoxInstallerOSType(imageURL string) string {
	cleanURL := strings.ToLower(imageURL)
	switch {
	case strings.Contains(cleanURL, "windows"):
		if strings.Contains(cleanURL, "windows_11") || strings.Contains(cleanURL, "windows-11") {
			return "win11"
		}
		return "win10"
	case strings.Contains(cleanURL, "android"):
		return "l26"
	default:
		return "other"
	}
}
