package incus

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

func isIncusConfigUnsupportedError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := strings.ToLower(err.Error())
	return strings.Contains(errMsg, "unknown key") ||
		strings.Contains(errMsg, "invalid config") ||
		strings.Contains(errMsg, "not supported") ||
		strings.Contains(errMsg, "cgroup controller is missing")
}

// configureInstanceSystem 配置实例系统
func (i *IncusProvider) configureInstanceSystem(ctx context.Context, config provider.InstanceConfig) error {
	global.APP_LOG.Debug("开始配置LXD实例系统",
		zap.String("instance", config.Name),
		zap.String("type", config.InstanceType))
	if config.InstanceType != "vm" {
		_ = i.setInstanceConfig(ctx, config.Name, "boot.autostart", "true")
		_ = i.setInstanceConfig(ctx, config.Name, "boot.autostart.priority", "50")
		_ = i.setInstanceConfig(ctx, config.Name, "boot.autostart.delay", "10")
	}
	global.APP_LOG.Debug("实例系统配置完成",
		zap.String("instanceName", config.Name))
	return nil
}

// configureInstanceSecurity 配置实例安全设置
func (i *IncusProvider) configureInstanceSecurity(ctx context.Context, config provider.InstanceConfig) error {
	swapValue := "true"
	if config.MemorySwap != nil && !*config.MemorySwap {
		swapValue = "false"
	}

	if config.InstanceType == "vm" {
		// 虚拟机安全配置
		if err := i.setInstanceConfig(ctx, config.Name, "security.secureboot", "false"); err != nil {
			global.APP_LOG.Warn("设置SecureBoot失败", zap.Error(err))
		}

		if err := i.setInstanceConfig(ctx, config.Name, "limits.cpu.priority", "0"); err != nil {
			if isIncusConfigUnsupportedError(err) {
				global.APP_LOG.Warn("设置CPU优先级失败，当前节点不支持该配置，已跳过", zap.Error(err))
			} else {
				global.APP_LOG.Warn("设置CPU优先级失败", zap.Error(err))
			}
		}

		swapEnabled := true
		if err := i.setInstanceConfig(ctx, config.Name, "limits.memory.swap", swapValue); err != nil {
			if isIncusConfigUnsupportedError(err) {
				swapEnabled = false
				global.APP_LOG.Warn("设置内存交换失败，当前节点不支持该配置，已跳过", zap.Error(err))
			} else {
				swapEnabled = false
				global.APP_LOG.Warn("设置内存交换失败", zap.Error(err))
			}
		}

		if swapEnabled && swapValue == "true" {
			if err := i.setInstanceConfig(ctx, config.Name, "limits.memory.swap.priority", "1"); err != nil {
				if isIncusConfigUnsupportedError(err) {
					global.APP_LOG.Warn("设置内存交换优先级失败，当前节点不支持该配置，已跳过", zap.Error(err))
				} else {
					global.APP_LOG.Warn("设置内存交换优先级失败", zap.Error(err))
				}
			}
		}
	} else {
		nestingValue := "true"
		if config.AllowNesting != nil && !*config.AllowNesting {
			nestingValue = "false"
		}

		// 容器安全配置
		if err := i.setInstanceConfig(ctx, config.Name, "security.nesting", nestingValue); err != nil {
			if isIncusConfigUnsupportedError(err) {
				global.APP_LOG.Warn("设置容器嵌套失败，当前节点不支持该配置，已跳过", zap.Error(err))
			} else {
				global.APP_LOG.Warn("设置容器嵌套失败", zap.Error(err))
			}
		}

		// CPU优先级配置
		if err := i.setInstanceConfig(ctx, config.Name, "limits.cpu.priority", "0"); err != nil {
			if isIncusConfigUnsupportedError(err) {
				global.APP_LOG.Warn("设置CPU优先级失败，当前节点不支持该配置，已跳过", zap.Error(err))
			} else {
				global.APP_LOG.Warn("设置CPU优先级失败", zap.Error(err))
			}
		}

		// 内存交换配置
		swapEnabled := true
		if err := i.setInstanceConfig(ctx, config.Name, "limits.memory.swap", swapValue); err != nil {
			if isIncusConfigUnsupportedError(err) {
				swapEnabled = false
				global.APP_LOG.Warn("设置内存交换失败，当前节点不支持该配置，已跳过", zap.Error(err))
			} else {
				swapEnabled = false
				global.APP_LOG.Warn("设置内存交换失败", zap.Error(err))
			}
		}

		if swapEnabled && swapValue == "true" {
			if err := i.setInstanceConfig(ctx, config.Name, "limits.memory.swap.priority", "1"); err != nil {
				if isIncusConfigUnsupportedError(err) {
					global.APP_LOG.Warn("设置内存交换优先级失败，当前节点不支持该配置，已跳过", zap.Error(err))
				} else {
					global.APP_LOG.Warn("设置内存交换优先级失败", zap.Error(err))
				}
			}
		} else {
			global.APP_LOG.Debug("已跳过内存交换优先级设置",
				zap.String("instance", config.Name),
				zap.String("reason", "swap not enabled"))
		}
	}

	return nil
}

// configureInstanceGPU 配置实例GPU直通（仅容器，VM不支持通过此方式直通）
// GPU驱动需要用户在宿主机上手动安装，本函数仅负责附加GPU设备
func (i *IncusProvider) configureInstanceGPU(ctx context.Context, config provider.InstanceConfig) error {
	if !config.GpuEnabled {
		return nil
	}
	if config.InstanceType == "vm" {
		global.APP_LOG.Warn("GPU直通当前不支持虚拟机实例，跳过", zap.String("instance", config.Name))
		return nil
	}

	ids := strings.Split(config.GpuDeviceIds, ",")
	if config.GpuDeviceIds == "" {
		cmd := fmt.Sprintf("incus config device add %s gpu gpu", shellSingleQuote(config.Name))
		if _, err := i.sshClient.Execute(cmd); err != nil {
			return fmt.Errorf("附加GPU设备失败: %w", err)
		}
		global.APP_LOG.Info("已为实例附加所有GPU设备", zap.String("instance", config.Name))
	} else {
		for idx, rawID := range ids {
			id := strings.TrimSpace(rawID)
			if id == "" {
				continue
			}
			deviceName := fmt.Sprintf("gpu%d", idx)
			cmd := fmt.Sprintf("incus config device add %s %s gpu id=%s", shellSingleQuote(config.Name), shellSingleQuote(deviceName), shellSingleQuote(id))
			if _, err := i.sshClient.Execute(cmd); err != nil {
				global.APP_LOG.Warn("附加GPU设备失败，跳过该设备",
					zap.String("instance", config.Name),
					zap.String("gpuId", id),
					zap.Error(err))
			} else {
				global.APP_LOG.Info("已为实例附加GPU设备",
					zap.String("instance", config.Name),
					zap.String("device", deviceName),
					zap.String("gpuId", id))
			}
		}
	}
	return nil
}

// setInstanceConfig 通用的实例配置设置方法，根据执行规则选择API或SSH
func (i *IncusProvider) setInstanceConfig(ctx context.Context, instanceName string, key string, value string) error {
	// 根据执行规则判断使用哪种方式
	if i.shouldUseAPI() {
		if err := i.apiSetInstanceConfig(ctx, instanceName, key, value); err == nil {
			global.APP_LOG.Debug("Incus API设置实例配置成功",
				zap.String("instance", instanceName),
				zap.String("key", key),
				zap.String("value", value))
			return nil
		} else {
			if fallbackErr := i.ensureSSHBeforeFallback(err, "设置实例配置"); fallbackErr != nil {
				return fallbackErr
			}
		}
	}

	// 如果执行规则不允许使用SSH，则返回错误
	if !i.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	// SSH方式设置配置（优先 key=value 新语法，兼容回退到旧语法）
	cmdNew := fmt.Sprintf("incus config set %s %s=%s", shellSingleQuote(instanceName), shellSingleQuote(key), shellSingleQuote(value))
	_, newErr := i.sshClient.Execute(cmdNew)
	if newErr == nil {
		global.APP_LOG.Debug("Incus SSH设置实例配置成功",
			zap.String("instance", instanceName),
			zap.String("key", key),
			zap.String("value", value),
			zap.String("syntax", "key=value"))
		return nil
	}

	cmdLegacy := fmt.Sprintf("incus config set %s %s %s", shellSingleQuote(instanceName), shellSingleQuote(key), shellSingleQuote(value))
	_, legacyErr := i.sshClient.Execute(cmdLegacy)
	if legacyErr != nil {
		return fmt.Errorf("SSH设置实例配置失败: new syntax error=%v, legacy syntax error=%w", newErr, legacyErr)
	}

	global.APP_LOG.Debug("Incus SSH设置实例配置成功",
		zap.String("instance", instanceName),
		zap.String("key", key),
		zap.String("value", value),
		zap.String("syntax", "legacy"))
	return nil
}

// setInstanceDeviceConfig 通用的实例设备配置设置方法，根据执行规则选择API或SSH
func (i *IncusProvider) setInstanceDeviceConfig(ctx context.Context, instanceName string, deviceName string, key string, value string) error {
	// 根据执行规则判断使用哪种方式
	if i.shouldUseAPI() {
		if err := i.apiSetInstanceDeviceConfig(ctx, instanceName, deviceName, key, value); err == nil {
			global.APP_LOG.Debug("Incus API设置实例设备配置成功",
				zap.String("instance", instanceName),
				zap.String("device", deviceName),
				zap.String("key", key),
				zap.String("value", value))
			return nil
		} else {
			if fallbackErr := i.ensureSSHBeforeFallback(err, "设置实例设备配置"); fallbackErr != nil {
				return fallbackErr
			}
		}
	}

	// 如果执行规则不允许使用SSH，则返回错误
	if !i.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	// SSH方式设置设备配置（优先 key=value 新语法，兼容回退到旧语法）
	cmdNew := fmt.Sprintf("incus config device set %s %s %s=%s", shellSingleQuote(instanceName), shellSingleQuote(deviceName), shellSingleQuote(key), shellSingleQuote(value))
	_, newErr := i.sshClient.Execute(cmdNew)
	if newErr == nil {
		global.APP_LOG.Debug("Incus SSH设置实例设备配置成功",
			zap.String("instance", instanceName),
			zap.String("device", deviceName),
			zap.String("key", key),
			zap.String("value", value),
			zap.String("syntax", "key=value"))
		return nil
	}

	cmdLegacy := fmt.Sprintf("incus config device set %s %s %s %s", shellSingleQuote(instanceName), shellSingleQuote(deviceName), shellSingleQuote(key), shellSingleQuote(value))
	_, legacyErr := i.sshClient.Execute(cmdLegacy)
	if legacyErr != nil {
		return fmt.Errorf("SSH设置实例设备配置失败: new syntax error=%v, legacy syntax error=%w", newErr, legacyErr)
	}

	global.APP_LOG.Debug("Incus SSH设置实例设备配置成功",
		zap.String("instance", instanceName),
		zap.String("device", deviceName),
		zap.String("key", key),
		zap.String("value", value),
		zap.String("syntax", "legacy"))
	return nil
}

// ensureSSHScriptsAvailable 确保SSH脚本文件在远程服务器上可用
func (i *IncusProvider) ensureSSHScriptsAvailable(providerCountry string) error {
	scriptsDir := "/usr/local/bin"
	scripts := []string{"ssh_bash.sh", "ssh_sh.sh"}

	// 检查脚本是否都存在
	allExist := true
	for _, script := range scripts {
		scriptPath := filepath.Join(scriptsDir, script)
		if !i.isRemoteFileValid(scriptPath) {
			allExist = false
			global.APP_LOG.Debug("SSH脚本文件不存在或无效",
				zap.String("scriptPath", scriptPath))
			break
		}
	}

	if allExist {
		global.APP_LOG.Debug("SSH脚本文件都已存在且有效")
		return nil
	}

	// 下载缺失的脚本
	global.APP_LOG.Debug("开始下载SSH脚本文件")

	for _, script := range scripts {
		scriptPath := filepath.Join(scriptsDir, script)

		// 如果脚本已存在且有效，跳过
		if i.isRemoteFileValid(scriptPath) {
			global.APP_LOG.Debug("SSH脚本已存在，跳过下载",
				zap.String("script", script))
			continue
		}

		// 构建下载URL - 使用Incus仓库路径
		baseURL := "https://raw.githubusercontent.com/oneclickvirt/incus/main/scripts/" + script
		downloadURL := i.getSSHScriptDownloadURL(baseURL, providerCountry)

		global.APP_LOG.Debug("开始下载SSH脚本",
			zap.String("script", script),
			zap.String("downloadURL", downloadURL),
			zap.String("scriptPath", scriptPath))

		// 下载脚本文件
		if err := i.downloadFileToRemote(downloadURL, scriptPath); err != nil {
			global.APP_LOG.Error("下载SSH脚本失败",
				zap.String("script", script),
				zap.Error(err))
			return fmt.Errorf("下载SSH脚本 %s 失败: %w", script, err)
		}

		// 设置执行权限
		chmodCmd := fmt.Sprintf("chmod +x %s", shellSingleQuote(scriptPath))
		if _, err := i.sshClient.Execute(chmodCmd); err != nil {
			global.APP_LOG.Error("设置SSH脚本执行权限失败",
				zap.String("script", script),
				zap.Error(err))
			return fmt.Errorf("设置SSH脚本 %s 执行权限失败: %w", script, err)
		}

		// 使用dos2unix处理脚本格式（如果可用）
		dos2unixCmd := fmt.Sprintf("command -v dos2unix >/dev/null 2>&1 && dos2unix %s || true", shellSingleQuote(scriptPath))
		i.sshClient.Execute(dos2unixCmd)

		global.APP_LOG.Debug("SSH脚本下载并设置完成",
			zap.String("script", script),
			zap.String("scriptPath", scriptPath))
	}

	global.APP_LOG.Info("所有SSH脚本文件下载完成")
	return nil
}

// getSSHScriptDownloadURL 获取SSH脚本下载URL，支持CDN
func (i *IncusProvider) getSSHScriptDownloadURL(originalURL, providerCountry string) string {
	// 如果是中国地区，尝试使用CDN
	if providerCountry == "CN" || providerCountry == "cn" {
		if cdnURL := i.getSSHScriptCDNURL(originalURL); cdnURL != "" {
			// 测试CDN可用性
			testCmd := fmt.Sprintf("curl -s -I --max-time 5 %s | head -n 1 | grep -q '200'", shellSingleQuote(cdnURL))
			if _, err := i.sshClient.Execute(testCmd); err == nil {
				global.APP_LOG.Debug("使用CDN下载SSH脚本",
					zap.String("cdnURL", cdnURL))
				return cdnURL
			}
		}
	}
	return originalURL
}

// getSSHScriptCDNURL 获取SSH脚本CDN URL
func (i *IncusProvider) getSSHScriptCDNURL(originalURL string) string {
	cdnEndpoints := utils.GetCDNEndpoints()

	// 直接在原始URL前加CDN前缀
	// 原始URL格式: https://raw.githubusercontent.com/oneclickvirt/incus/main/scripts/ssh_bash.sh
	// CDN URL格式: https://cdn0.spiritlhl.top/https://raw.githubusercontent.com/oneclickvirt/incus/main/scripts/ssh_bash.sh
	for _, endpoint := range cdnEndpoints {
		cdnURL := endpoint + originalURL
		// 测试CDN可用性
		testCmd := fmt.Sprintf("curl -s -I --max-time 5 %s | head -n 1 | grep -q '200'", shellSingleQuote(cdnURL))
		if _, err := i.sshClient.Execute(testCmd); err == nil {
			return cdnURL
		}
	}
	return ""
}
