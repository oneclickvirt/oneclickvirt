package docker

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

type dockerSpecialRuntimeImage struct {
	Kind    string
	Image   string
	Version string
}

func (d *DockerProvider) sshCreateSpecialRuntimeInstance(ctx context.Context, config provider.InstanceConfig, updateProgress func(int, string)) (bool, error) {
	specialImage, ok := detectDockerSpecialRuntimeImage(config)
	if !ok {
		return false, nil
	}
	if d.runtime.ProviderType != "docker" && d.runtime.ProviderType != "orbstack" {
		return true, fmt.Errorf("镜像 %s 需要 Docker 运行时，当前节点类型为 %s", config.Image, d.runtime.ProviderType)
	}
	if !utils.IsValidContainerRuntimeName(config.Name) {
		return true, fmt.Errorf("容器名称格式无效: %s", config.Name)
	}

	networkType := d.config.NetworkType
	if config.Metadata != nil {
		if metaNetworkType, ok := config.Metadata["network_type"]; ok {
			networkType = metaNetworkType
		}
	}
	staticIPv6 := ""
	if config.Metadata != nil {
		staticIPv6 = config.Metadata["static_ipv6"]
	}
	hasIPv6 := utils.NetworkTypeHasIPv6(networkType)
	networkSelection, routedPresent, err := d.routedNetworkSelection(config, networkType)
	if !routedPresent {
		networkSelection, err = utils.ResolveContainerNetwork(
			networkType,
			staticIPv6,
			d.runtime.IPv4Network,
			d.runtime.IPv6Network,
			hasIPv6 && d.checkIPv6NetworkAvailable(),
		)
	}
	if err != nil {
		return true, err
	}
	networkFlags := dockerNetworkOptionFlags(networkSelection)
	if networkSelection.RoutedVeth {
		labelArgs, labelErr := provider.RoutedIPv6RuntimeLabelArgs(networkSelection)
		if labelErr != nil {
			return true, fmt.Errorf("构造隧道路由IPv6运行时标签失败: %w", labelErr)
		}
		networkFlags += " " + labelArgs
	}

	updateProgress(18, "准备特殊运行时镜像...")
	cleanupCmd := fmt.Sprintf("%s rm -f %s 2>/dev/null || true", d.runtime.CLI, shellSingleQuote(config.Name))
	_, _ = d.sshClient.Execute(cleanupCmd)

	switch specialImage.Kind {
	case "windows":
		err = d.createDockerWindowsRuntime(config, specialImage, networkFlags, updateProgress)
	case "android":
		err = d.createDockerAndroidRuntime(config, specialImage, networkFlags, updateProgress)
	case "macos":
		err = d.createDockerMacOSRuntime(config, specialImage, networkFlags, updateProgress)
	default:
		err = fmt.Errorf("不支持的特殊 Docker 运行时镜像类型: %s", specialImage.Kind)
	}
	if err == nil {
		err = d.connectDockerAdditionalNetworks(config.Name, networkSelection)
	}
	if err != nil {
		diagnostics := d.collectCreateDiagnostics(config.Name)
		return true, fmt.Errorf("%w; diagnostics: %s", err, utils.TruncateString(strings.TrimSpace(diagnostics), 8000))
	}

	updateProgress(96, "等待特殊运行时容器启动...")
	if err := d.waitContainerRunning(config.Name, 60*time.Second); err != nil {
		global.APP_LOG.Warn("特殊运行时容器启动状态确认失败，继续执行后续处理",
			zap.String("instance", utils.TruncateString(config.Name, 32)),
			zap.Error(err))
	}

	if privateIP, err := d.getContainerPrivateIP(config.Name); err == nil && privateIP != "" {
		var instance providerModel.Instance
		if err := global.APP_DB.Where("name = ? AND provider_id = ?", config.Name, d.config.ID).First(&instance).Error; err == nil {
			if err := global.APP_DB.Model(&instance).Update("private_ip", privateIP).Error; err == nil {
				global.APP_LOG.Debug("已更新特殊 Docker 实例内网IP",
					zap.String("instanceName", config.Name),
					zap.String("privateIP", privateIP))
			}
		}
	}

	updateProgress(98, "初始化流量监控...")
	if err := d.initializePmacctMonitoring(ctx, config); err != nil {
		global.APP_LOG.Warn("初始化特殊运行时流量监控失败", zap.Error(err))
	}

	updateProgress(100, "特殊Docker实例创建完成")
	global.APP_LOG.Info("特殊Docker运行时实例创建成功",
		zap.String("name", utils.TruncateString(config.Name, 32)),
		zap.String("kind", specialImage.Kind),
		zap.String("image", utils.TruncateString(specialImage.Image, 64)))
	return true, nil
}

func detectDockerSpecialRuntimeImage(config provider.InstanceConfig) (*dockerSpecialRuntimeImage, bool) {
	candidateURL := strings.TrimSpace(config.ImageURL)
	candidateImage := strings.ToLower(strings.TrimSpace(config.Image))
	if strings.HasPrefix(candidateURL, "docker://") {
		ref := strings.TrimPrefix(strings.Split(candidateURL, "?")[0], "docker://")
		tag := "latest"
		if idx := strings.LastIndex(ref, ":"); idx > strings.LastIndex(ref, "/") {
			tag = strings.TrimSpace(ref[idx+1:])
			ref = strings.TrimSpace(ref[:idx])
		}
		switch strings.ToLower(ref) {
		case "spiritlhl/wds":
			return &dockerSpecialRuntimeImage{Kind: "windows", Image: "spiritlhl/wds:" + tag, Version: tag}, true
		case "dockurr/windows":
			return &dockerSpecialRuntimeImage{Kind: "windows", Image: "dockurr/windows:latest", Version: tag}, true
		case "redroid/redroid":
			return &dockerSpecialRuntimeImage{Kind: "android", Image: "redroid/redroid:" + tag, Version: tag}, true
		case "dockurr/macos":
			return &dockerSpecialRuntimeImage{Kind: "macos", Image: "dockurr/macos:latest", Version: tag}, true
		}
	}

	switch {
	case strings.HasPrefix(candidateImage, "windows-"):
		tag := strings.TrimPrefix(candidateImage, "windows-")
		if tag == "" {
			tag = "2022"
		}
		return &dockerSpecialRuntimeImage{Kind: "windows", Image: "spiritlhl/wds:" + tag, Version: tag}, true
	case strings.HasPrefix(candidateImage, "android-"):
		tag := strings.TrimPrefix(candidateImage, "android-")
		if tag == "" {
			tag = "11.0.0-latest"
		}
		return &dockerSpecialRuntimeImage{Kind: "android", Image: "redroid/redroid:" + tag, Version: tag}, true
	case strings.HasPrefix(candidateImage, "macos-"):
		version := strings.TrimPrefix(candidateImage, "macos-")
		if version == "" {
			version = "15"
		}
		return &dockerSpecialRuntimeImage{Kind: "macos", Image: "dockurr/macos:latest", Version: version}, true
	default:
		return nil, false
	}
}

func (d *DockerProvider) createDockerWindowsRuntime(config provider.InstanceConfig, image *dockerSpecialRuntimeImage, networkFlags string, updateProgress func(int, string)) error {
	updateProgress(30, "拉取Windows运行时镜像...")
	if output, err := d.sshClient.ExecuteWithTimeout(fmt.Sprintf("%s pull %s", d.runtime.CLI, shellSingleQuote(image.Image)), 60*time.Minute); err != nil {
		return fmt.Errorf("拉取Windows镜像失败: %w; output: %s", err, utils.TruncateString(output, 1000))
	}

	rdpPort := selectMappedHostPort(config.Ports, 3389, 0)
	if rdpPort == 0 {
		rdpPort = selectFirstMappedHostPort(config.Ports, 33896)
	}

	cmd := fmt.Sprintf("%s run -d --name %s%s --privileged=true --device=/dev/kvm --device=/dev/net/tun --cap-add=NET_ADMIN --cap-add=SYS_ADMIN%s -p %s %s /sbin/init",
		d.runtime.CLI,
		shellSingleQuote(config.Name),
		networkFlags,
		dockerSpecialResourceFlags(config),
		shellSingleQuote(fmt.Sprintf("0.0.0.0:%d:3389/tcp", rdpPort)),
		shellSingleQuote(image.Image))

	updateProgress(80, "创建Windows运行时容器...")
	output, err := d.sshClient.ExecuteWithTimeout(cmd, 30*time.Minute)
	if err != nil {
		return fmt.Errorf("创建Windows容器失败: %w; output: %s", err, utils.TruncateString(output, 2000))
	}

	time.Sleep(3 * time.Second)
	startupCmd := fmt.Sprintf("%s exec %s bash -lc %s", d.runtime.CLI, shellSingleQuote(config.Name), shellSingleQuote("if [ -f /startup.sh ]; then bash /startup.sh; elif [ -f startup.sh ]; then bash startup.sh; fi"))
	if output, err := d.sshClient.ExecuteWithTimeout(startupCmd, 10*time.Minute); err != nil {
		global.APP_LOG.Warn("Windows运行时启动脚本执行失败，容器已创建",
			zap.String("name", utils.TruncateString(config.Name, 32)),
			zap.String("output", utils.TruncateString(output, 1000)),
			zap.Error(err))
	}
	return nil
}

func (d *DockerProvider) createDockerAndroidRuntime(config provider.InstanceConfig, image *dockerSpecialRuntimeImage, networkFlags string, updateProgress func(int, string)) error {
	updateProgress(30, "拉取Android运行时镜像...")
	if output, err := d.sshClient.ExecuteWithTimeout(fmt.Sprintf("%s pull %s", d.runtime.CLI, shellSingleQuote(image.Image)), 60*time.Minute); err != nil {
		return fmt.Errorf("拉取Android镜像失败: %w; output: %s", err, utils.TruncateString(output, 1000))
	}

	adbPort := selectMappedHostPort(config.Ports, 5555, 0)
	if adbPort == 0 {
		adbPort = selectFirstMappedHostPort(config.Ports, 5555)
	}

	cmd := fmt.Sprintf("%s run -d --name %s%s --privileged%s -p %s %s androidboot.redroid_width=720 androidboot.redroid_height=1280 androidboot.redroid_dpi=320",
		d.runtime.CLI,
		shellSingleQuote(config.Name),
		networkFlags,
		dockerSpecialResourceFlags(config),
		shellSingleQuote(fmt.Sprintf("0.0.0.0:%d:5555/tcp", adbPort)),
		shellSingleQuote(image.Image))

	updateProgress(80, "创建Android运行时容器...")
	output, err := d.sshClient.ExecuteWithTimeout(cmd, 30*time.Minute)
	if err != nil {
		return fmt.Errorf("创建Android容器失败: %w; output: %s", err, utils.TruncateString(output, 2000))
	}
	return nil
}

func (d *DockerProvider) createDockerMacOSRuntime(config provider.InstanceConfig, image *dockerSpecialRuntimeImage, networkFlags string, updateProgress func(int, string)) error {
	updateProgress(30, "拉取macOS运行时镜像...")
	if output, err := d.sshClient.ExecuteWithTimeout(fmt.Sprintf("%s pull %s", d.runtime.CLI, shellSingleQuote(image.Image)), 60*time.Minute); err != nil {
		return fmt.Errorf("拉取macOS镜像失败: %w; output: %s", err, utils.TruncateString(output, 1000))
	}

	webPort := selectMappedHostPort(config.Ports, 8006, 0)
	if webPort == 0 {
		webPort = selectFirstMappedHostPort(config.Ports, 8006)
	}
	vncPort := selectMappedHostPort(config.Ports, 5900, 0)
	if vncPort == 0 {
		vncPort = webPort + 1
	}

	envFlags := dockerSpecialMacOSEnvFlags(config, image.Version)
	cmd := fmt.Sprintf("%s run -d --name %s%s --privileged --device=/dev/kvm --device=/dev/net/tun --cap-add=NET_ADMIN%s%s -p %s -p %s %s",
		d.runtime.CLI,
		shellSingleQuote(config.Name),
		networkFlags,
		dockerSpecialResourceFlags(config),
		envFlags,
		shellSingleQuote(fmt.Sprintf("0.0.0.0:%d:8006/tcp", webPort)),
		shellSingleQuote(fmt.Sprintf("0.0.0.0:%d:5900/tcp", vncPort)),
		shellSingleQuote(image.Image))

	updateProgress(80, "创建macOS运行时容器...")
	output, err := d.sshClient.ExecuteWithTimeout(cmd, 30*time.Minute)
	if err != nil {
		return fmt.Errorf("创建macOS容器失败: %w; output: %s", err, utils.TruncateString(output, 2000))
	}
	return nil
}

func dockerSpecialResourceFlags(config provider.InstanceConfig) string {
	flags := ""
	if strings.TrimSpace(config.CPU) != "" {
		flags += fmt.Sprintf(" --cpus=%s", shellSingleQuote(strings.TrimSpace(config.CPU)))
	}
	if strings.TrimSpace(config.Memory) != "" {
		flags += fmt.Sprintf(" --memory=%s", shellSingleQuote(strings.TrimSpace(config.Memory)))
	}
	return flags
}

func dockerSpecialMacOSEnvFlags(config provider.InstanceConfig, version string) string {
	flags := ""
	if version != "" && version != "latest" {
		flags += fmt.Sprintf(" -e %s", shellSingleQuote("VERSION="+version))
	}
	if cpu := dockerSpecialCPUCores(config.CPU); cpu != "" {
		flags += fmt.Sprintf(" -e %s", shellSingleQuote("CPU_CORES="+cpu))
	}
	if memory := strings.TrimSpace(config.Memory); memory != "" {
		flags += fmt.Sprintf(" -e %s", shellSingleQuote("RAM_SIZE="+memory))
	}
	if disk := strings.TrimSpace(config.Disk); disk != "" && disk != "0" {
		flags += fmt.Sprintf(" -e %s", shellSingleQuote("DISK_SIZE="+disk))
	}
	return flags
}

func dockerSpecialCPUCores(cpu string) string {
	value := strings.TrimSpace(cpu)
	if value == "" {
		return ""
	}
	if parsed, err := strconv.ParseFloat(value, 64); err == nil && parsed > 0 {
		if parsed < 1 {
			parsed = 1
		}
		return strconv.Itoa(int(parsed + 0.5))
	}
	return value
}

func selectMappedHostPort(ports []string, guestPort, fallback int) int {
	for _, mapping := range ports {
		host, guest, ok := parseDockerPortMapping(mapping)
		if ok && guest == guestPort && host > 0 {
			return host
		}
	}
	return fallback
}

func selectFirstMappedHostPort(ports []string, fallback int) int {
	for _, mapping := range ports {
		host, _, ok := parseDockerPortMapping(mapping)
		if ok && host > 0 {
			return host
		}
	}
	return fallback
}

func parseDockerPortMapping(mapping string) (int, int, bool) {
	base := strings.TrimSpace(mapping)
	if base == "" {
		return 0, 0, false
	}
	if idx := strings.Index(base, "/"); idx >= 0 {
		base = base[:idx]
	}
	parts := strings.Split(base, ":")
	if len(parts) == 1 {
		port, err := strconv.Atoi(parts[0])
		return port, port, err == nil
	}
	if len(parts) < 2 {
		return 0, 0, false
	}
	hostPort, hostErr := strconv.Atoi(parts[len(parts)-2])
	guestPort, guestErr := strconv.Atoi(parts[len(parts)-1])
	return hostPort, guestPort, hostErr == nil && guestErr == nil
}

func (d *DockerProvider) waitContainerRunning(name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		statusOutput, err := d.sshClient.Execute(fmt.Sprintf("%s inspect %s --format '{{.State.Status}}'", d.runtime.CLI, shellSingleQuote(name)))
		if err == nil && strings.EqualFold(strings.TrimSpace(statusOutput), "running") {
			return nil
		}
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("等待容器 %s 进入 running 状态超时", name)
}
