package incus

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

func (i *IncusProvider) configureInstanceLimits(ctx context.Context, config provider.InstanceConfig) error {
	var errors []string
	swapValue := "true"
	if config.MemorySwap != nil && !*config.MemorySwap {
		swapValue = "false"
	}

	// 配置CPU优先级
	if config.CPU != "" {
		if err := i.setInstanceConfig(ctx, config.Name, "limits.cpu.priority", "0"); err != nil {
			errors = append(errors, fmt.Sprintf("设置CPU优先级失败: %v", err))
		}
	}

	// 配置内存交换
	if err := i.setInstanceConfig(ctx, config.Name, "limits.memory.swap", swapValue); err != nil {
		errors = append(errors, fmt.Sprintf("设置内存交换失败: %v", err))
	}

	// 如果是容器，配置额外的限制
	if config.InstanceType != "vm" {
		readLimit, writeLimit := incusResolveIOLimits(config)
		ioConfigs := map[string]string{}
		if readLimit != "" {
			ioConfigs["limits.read"] = readLimit
		}
		if writeLimit != "" {
			ioConfigs["limits.write"] = writeLimit
		}

		for key, value := range ioConfigs {
			if err := i.setInstanceDeviceConfig(ctx, config.Name, "root", key, value); err != nil {
				global.APP_LOG.Debug("IO限制配置失败，继续执行",
					zap.String("device", "root"),
					zap.String("key", key),
					zap.String("value", value),
					zap.Error(err))
			}
		}

		// 配置CPU限制
		cpuConfigs := []struct {
			key   string
			value string
		}{}

		if config.CPUAllowance != nil && *config.CPUAllowance != "" && *config.CPUAllowance != "100%" {
			cpuConfigs = append(cpuConfigs, struct {
				key   string
				value string
			}{"limits.cpu.allowance", *config.CPUAllowance})
		} else {
			cpuConfigs = append(cpuConfigs,
				struct {
					key   string
					value string
				}{"limits.cpu.allowance", "50%"},
				struct {
					key   string
					value string
				}{"limits.cpu.allowance", "25ms/100ms"},
			)
		}

		for _, cpuConfig := range cpuConfigs {
			if err := i.setInstanceConfig(ctx, config.Name, cpuConfig.key, cpuConfig.value); err != nil {
				global.APP_LOG.Debug("CPU限制配置失败，继续执行",
					zap.String("key", cpuConfig.key),
					zap.String("value", cpuConfig.value),
					zap.Error(err))
			}
		}

		if config.MaxProcesses != nil && *config.MaxProcesses > 0 {
			if err := i.setInstanceConfig(ctx, config.Name, "limits.processes", fmt.Sprintf("%d", *config.MaxProcesses)); err != nil {
				global.APP_LOG.Warn("设置最大进程数失败", zap.Error(err))
			}
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("配置实例限制时发生错误: %s", strings.Join(errors, "; "))
	}

	return nil
}

// configureInstanceNetworkSettings 配置实例网络设置
func (i *IncusProvider) configureInstanceNetworkSettings(ctx context.Context, config provider.InstanceConfig) error {
	// 启动实例以配置网络
	if err := i.sshStartInstance(config.Name); err != nil {
		return fmt.Errorf("启动实例失败: %w", err)
	}
	// 解析网络配置
	networkConfig := i.parseNetworkConfigFromInstanceConfig(config)
	// 配置网络
	if err := i.configureInstanceNetwork(ctx, config, networkConfig); err != nil {
		return fmt.Errorf("配置实例网络失败: %w", err)
	}
	return nil
}

// configureInstanceStorage 配置实例存储
func (i *IncusProvider) configureInstanceStorage(ctx context.Context, config provider.InstanceConfig) error {
	// 参考: https://github.com/oneclickvirt/incus/blob/main/scripts/buildct.sh
	// 磁盘大小在创建实例后通过 device set 设置（不再使用 -d 标志，避免 profile 缺少 root 设备时失败）

	// 设置 root 磁盘大小
	if config.Disk != "" {
		diskFormatted := convertDiskFormat(config.Disk)
		// 优先用新语法设置磁盘大小
		setSizeCmd := fmt.Sprintf("incus config device set %s root size=%s", shellSingleQuote(config.Name), shellSingleQuote(diskFormatted))
		if _, err := i.sshClient.Execute(setSizeCmd); err != nil {
			// 兼容旧语法
			legacySizeCmd := fmt.Sprintf("incus config device set %s root size %s", shellSingleQuote(config.Name), shellSingleQuote(diskFormatted))
			if _, legacyErr := i.sshClient.Execute(legacySizeCmd); legacyErr != nil {
				global.APP_LOG.Warn("设置磁盘大小失败（可能 root 设备继承自 profile）",
					zap.String("instance", config.Name),
					zap.String("size", diskFormatted),
					zap.Error(legacyErr))
			} else {
				global.APP_LOG.Debug("已通过 legacy 语法设置磁盘大小",
					zap.String("instance", config.Name),
					zap.String("size", diskFormatted))
			}
		} else {
			global.APP_LOG.Debug("已设置磁盘大小",
				zap.String("instance", config.Name),
				zap.String("size", diskFormatted))
		}
	}

	readLimit, writeLimit := incusResolveIOLimits(config)
	if readLimit != "" {
		if err := i.setInstanceDeviceConfig(ctx, config.Name, "root", "limits.read", readLimit); err != nil {
			global.APP_LOG.Warn("设置读取IO速率限制失败",
				zap.String("instance", config.Name),
				zap.String("limit", readLimit),
				zap.Error(err))
		}
	}
	if writeLimit != "" {
		if err := i.setInstanceDeviceConfig(ctx, config.Name, "root", "limits.write", writeLimit); err != nil {
			global.APP_LOG.Warn("设置写入IO速率限制失败",
				zap.String("instance", config.Name),
				zap.String("limit", writeLimit),
				zap.Error(err))
		}
	}

	global.APP_LOG.Debug("实例存储配置完成",
		zap.String("instance", config.Name),
		zap.String("instanceType", config.InstanceType))

	return nil
}

func incusResolveIOLimits(config provider.InstanceConfig) (string, string) {
	readLimit := ""
	writeLimit := ""
	if config.ReadIOLimit != nil {
		readLimit = strings.TrimSpace(*config.ReadIOLimit)
	}
	if config.WriteIOLimit != nil {
		writeLimit = strings.TrimSpace(*config.WriteIOLimit)
	}
	if config.InstanceType != "vm" && config.DiskIOLimit != nil {
		legacyLimit := strings.TrimSpace(*config.DiskIOLimit)
		if readLimit == "" {
			readLimit = legacyLimit
		}
		if writeLimit == "" {
			writeLimit = legacyLimit
		}
	}
	return readLimit, writeLimit
}

func (i *IncusProvider) sshStartInstance(id string) error {
	// 先检查实例状态，如果已经在运行则跳过启动
	if i.sshInstanceRunning(id) {
		global.APP_LOG.Debug("Incus 实例已在运行，跳过启动", zap.String("id", id))
		return nil
	}
	if err := i.ensureVMCloudInitTemplates(id); err != nil {
		global.APP_LOG.Warn("Incus VM cloud-init模板预检查失败，将继续尝试启动",
			zap.String("id", id),
			zap.Error(err))
	}

	startCmd := fmt.Sprintf("incus start %s", shellSingleQuote(id))
	var startErr error
	var startOutput string
	maxAttempts := 3
	repairedCloudInitTemplates := false
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		startOutput, startErr = i.sshClient.Execute(startCmd)
		if startErr == nil {
			break
		}

		// 如果错误信息提示实例已在运行，则不视为错误
		errMsg := startOutput + "\n" + startErr.Error()
		if incusAlreadyRunningMessage(errMsg) || i.sshInstanceRunning(id) {
			global.APP_LOG.Debug("Incus 实例已在运行", zap.String("id", id))
			return nil
		}

		if incusStartNeedsCloudInitTemplateRepair(errMsg) && !repairedCloudInitTemplates {
			if repairErr := i.ensureVMCloudInitTemplates(id); repairErr != nil {
				global.APP_LOG.Warn("Incus VM cloud-init模板自动修复失败",
					zap.String("id", id),
					zap.Error(repairErr))
			} else {
				repairedCloudInitTemplates = true
				if maxAttempts < 4 {
					maxAttempts = 4
				}
				global.APP_LOG.Info("Incus VM cloud-init模板已自动修复，准备重试启动",
					zap.String("id", id))
			}
		}

		if attempt < maxAttempts {
			global.APP_LOG.Warn("Incus启动实例首次失败，准备重试",
				zap.String("id", id),
				zap.String("output", utils.TruncateString(startOutput, 500)),
				zap.Error(startErr))
			time.Sleep(time.Duration(attempt*3) * time.Second)
		}
	}

	if startErr != nil {
		if i.sshInstanceRunning(id) {
			global.APP_LOG.Debug("Incus实例启动命令失败后状态已变为运行，继续流程", zap.String("id", id))
			return nil
		}
		diagOutput, diagErr := i.collectStartDiagnostics(id)
		details := []string{}
		if trimmed := strings.TrimSpace(startOutput); trimmed != "" {
			details = append(details, "start output: "+utils.TruncateString(trimmed, 8000))
		}
		if cleanDiag := strings.TrimSpace(diagOutput); cleanDiag != "" {
			details = append(details, "diagnostics: "+utils.TruncateString(cleanDiag, 8000))
		}
		if diagErr != nil {
			details = append(details, fmt.Sprintf("获取实例诊断日志失败: %v", diagErr))
		}
		if len(details) == 0 {
			return fmt.Errorf("failed to start instance: %w", startErr)
		}
		return fmt.Errorf("failed to start instance: %w; %s", startErr, strings.Join(details, "; "))
	}

	global.APP_LOG.Debug("已发送启动命令，等待实例启动", zap.String("id", id))

	// 等待实例真正启动 - 最多等待60秒
	maxWaitTime := 90 * time.Second
	checkInterval := 10 * time.Second
	startTime := time.Now()

	for {
		// 检查是否超时
		if time.Since(startTime) > maxWaitTime {
			return fmt.Errorf("等待实例启动超时 (90秒)")
		}

		// 等待一段时间后再检查
		time.Sleep(checkInterval)

		// 检查实例状态
		statusOutput, err := i.sshClient.Execute(fmt.Sprintf("incus info %s | grep \"Status:\" | awk '{print $2}'", shellSingleQuote(id)))
		if err == nil {
			status := strings.TrimSpace(statusOutput)
			if status == "RUNNING" || status == "Running" {
				// 实例已经启动，再等待额外的时间确保系统完全就绪
				time.Sleep(3 * time.Second)
				global.APP_LOG.Debug("Incus实例已成功启动并就绪",
					zap.String("id", id),
					zap.Duration("wait_time", time.Since(startTime)))
				return nil
			}
		}

		global.APP_LOG.Debug("等待实例启动",
			zap.String("id", id),
			zap.Duration("elapsed", time.Since(startTime)))
	}
}

func incusAlreadyRunningMessage(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "already running") || strings.Contains(lower, "instance is already running")
}

func (i *IncusProvider) sshInstanceRunning(id string) bool {
	statusOutput, err := i.sshClient.Execute(fmt.Sprintf("incus info %s | awk -F': ' '/^Status:/{print $2; exit}'", shellSingleQuote(id)))
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(statusOutput), "running")
}

func (i *IncusProvider) sshInstanceStopped(id string) bool {
	statusOutput, err := i.sshClient.Execute(fmt.Sprintf("incus info %s | awk -F': ' '/^Status:/{print $2; exit}'", shellSingleQuote(id)))
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(statusOutput), "stopped")
}

func (i *IncusProvider) collectStartDiagnostics(id string) (string, error) {
	commands := []struct {
		name string
		cmd  string
	}{
		{"incus info --show-log", fmt.Sprintf("incus info %s --show-log", shellSingleQuote(id))},
		{"incus info", fmt.Sprintf("incus info %s", shellSingleQuote(id))},
		{"incus config show --expanded", fmt.Sprintf("incus config show %s --expanded", shellSingleQuote(id))},
		{"incus list", fmt.Sprintf("incus list %s --format csv -c n,s,t,4,6", shellSingleQuote(id))},
	}
	var parts []string
	var errs []string
	for _, command := range commands {
		output, err := i.sshClient.Execute(command.cmd)
		if trimmed := strings.TrimSpace(output); trimmed != "" {
			parts = append(parts, fmt.Sprintf("[%s]\n%s", command.name, trimmed))
		}
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", command.name, err))
		}
	}
	if len(errs) > 0 {
		return strings.Join(parts, "\n\n"), fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return strings.Join(parts, "\n\n"), nil
}

func (i *IncusProvider) sshStopInstance(id string) error {
	output, err := i.sshClient.Execute(fmt.Sprintf("incus stop %s", shellSingleQuote(id)))
	if err != nil {
		if i.sshInstanceStopped(id) {
			return nil
		}
		return fmt.Errorf("failed to stop instance: %w; output: %s", err, utils.TruncateString(strings.TrimSpace(output), 8000))
	}

	global.APP_LOG.Info("通过 SSH 成功停止 Incus 实例", zap.String("id", id))
	return nil
}

func (i *IncusProvider) sshRestartInstance(id string) error {
	output, err := i.sshClient.Execute(fmt.Sprintf("incus restart %s", shellSingleQuote(id)))
	if err != nil {
		return fmt.Errorf("failed to restart instance: %w; output: %s", err, utils.TruncateString(strings.TrimSpace(output), 8000))
	}
	global.APP_LOG.Info("通过 SSH 成功重启 Incus 实例", zap.String("id", id))
	return nil
}

func (i *IncusProvider) sshDeleteInstance(id string) error {
	// 获取节点hostname用于日志
	hostname := "unknown"
	if output, err := i.sshClient.Execute("hostname"); err == nil {
		hostname = utils.CleanCommandOutput(output)
	}

	global.APP_LOG.Debug("开始在Incus节点上删除实例（使用SSH）",
		zap.String("hostname", hostname),
		zap.String("host", utils.TruncateString(i.config.Host, 32)),
		zap.String("instance_id", id))

	output, err := i.sshClient.Execute(fmt.Sprintf("incus delete %s --force", shellSingleQuote(id)))
	if err != nil {
		// 检查是否是实例不存在的错误
		if strings.Contains(output, "Instance not found") || strings.Contains(output, "not found") {
			global.APP_LOG.Debug("实例已不存在，视为删除成功", zap.String("id", id))
			return nil // 实例不存在，视为删除成功
		}
		return fmt.Errorf("failed to delete instance: %w", err)
	}

	global.APP_LOG.Info("通过 SSH 成功删除 Incus 实例", zap.String("id", id))
	return nil
}

func (i *IncusProvider) sshListImages() ([]provider.Image, error) {
	output, err := i.sshClient.Execute("incus image list --format csv -c l,f,s,u")
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	var images []provider.Image

	for _, line := range lines {
		if line == "" {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) < 4 {
			continue
		}

		image := provider.Image{
			ID:   fields[1][:12], // fingerprint
			Name: fields[0],      // alias
			Tag:  "latest",
			Size: fields[2], // size
		}
		images = append(images, image)
	}

	global.APP_LOG.Debug("通过 SSH 成功获取 Incus 镜像列表", zap.Int("count", len(images)))
	return images, nil
}

func (i *IncusProvider) sshPullImage(image string) error {
	_, err := i.sshClient.Execute(fmt.Sprintf("incus image copy %s local:", shellSingleQuote("images:"+image)))
	if err != nil {
		return fmt.Errorf("failed to pull image: %w", err)
	}

	global.APP_LOG.Info("通过 SSH 成功拉取 Incus 镜像", zap.String("image", image))
	return nil
}

func (i *IncusProvider) sshDeleteImage(id string) error {
	_, err := i.sshClient.Execute(fmt.Sprintf("incus image delete %s", shellSingleQuote(id)))
	if err != nil {
		return fmt.Errorf("failed to delete image: %w", err)
	}

	global.APP_LOG.Info("通过 SSH 成功删除 Incus 镜像", zap.String("id", id))
	return nil
}
