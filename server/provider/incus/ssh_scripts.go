package incus

import (
	"context"
	"fmt"
	"path/filepath"

	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

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
	if config.InstanceType == "vm" {
		// 虚拟机安全配置
		if err := i.setInstanceConfig(ctx, config.Name, "security.secureboot", "false"); err != nil {
			global.APP_LOG.Warn("设置SecureBoot失败", zap.Error(err))
		}
	} else {
		// 容器安全配置
		if err := i.setInstanceConfig(ctx, config.Name, "security.nesting", "true"); err != nil {
			global.APP_LOG.Warn("设置容器嵌套失败", zap.Error(err))
		}

		// CPU优先级配置
		if err := i.setInstanceConfig(ctx, config.Name, "limits.cpu.priority", "0"); err != nil {
			global.APP_LOG.Warn("设置CPU优先级失败", zap.Error(err))
		}

		// 内存交换配置
		if err := i.setInstanceConfig(ctx, config.Name, "limits.memory.swap", "true"); err != nil {
			global.APP_LOG.Warn("设置内存交换失败", zap.Error(err))
		}

		if err := i.setInstanceConfig(ctx, config.Name, "limits.memory.swap.priority", "1"); err != nil {
			global.APP_LOG.Warn("设置内存交换优先级失败", zap.Error(err))
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
			global.APP_LOG.Warn("Incus API设置实例配置失败", zap.Error(err))

			// 检查是否可以回退到SSH
			if !i.shouldFallbackToSSH() {
				return fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
			}
			global.APP_LOG.Debug("回退到SSH执行 - 设置实例配置",
				zap.String("instance", instanceName),
				zap.String("key", key))
		}
	}

	// 如果执行规则不允许使用SSH，则返回错误
	if !i.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	// SSH方式设置配置
	cmd := fmt.Sprintf("incus config set %s %s %s", instanceName, key, value)
	_, err := i.sshClient.Execute(cmd)
	if err != nil {
		return fmt.Errorf("SSH设置实例配置失败: %w", err)
	}

	global.APP_LOG.Debug("Incus SSH设置实例配置成功",
		zap.String("instance", instanceName),
		zap.String("key", key),
		zap.String("value", value))
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
			global.APP_LOG.Warn("Incus API设置实例设备配置失败", zap.Error(err))

			// 检查是否可以回退到SSH
			if !i.shouldFallbackToSSH() {
				return fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
			}
			global.APP_LOG.Debug("回退到SSH执行 - 设置实例设备配置",
				zap.String("instance", instanceName),
				zap.String("device", deviceName),
				zap.String("key", key))
		}
	}

	// 如果执行规则不允许使用SSH，则返回错误
	if !i.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	// SSH方式设置设备配置
	cmd := fmt.Sprintf("incus config device set %s %s %s %s", instanceName, deviceName, key, value)
	_, err := i.sshClient.Execute(cmd)
	if err != nil {
		return fmt.Errorf("SSH设置实例设备配置失败: %w", err)
	}

	global.APP_LOG.Debug("Incus SSH设置实例设备配置成功",
		zap.String("instance", instanceName),
		zap.String("device", deviceName),
		zap.String("key", key),
		zap.String("value", value))
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
		chmodCmd := fmt.Sprintf("chmod +x %s", scriptPath)
		if _, err := i.sshClient.Execute(chmodCmd); err != nil {
			global.APP_LOG.Error("设置SSH脚本执行权限失败",
				zap.String("script", script),
				zap.Error(err))
			return fmt.Errorf("设置SSH脚本 %s 执行权限失败: %w", script, err)
		}

		// 使用dos2unix处理脚本格式（如果可用）
		dos2unixCmd := fmt.Sprintf("command -v dos2unix >/dev/null 2>&1 && dos2unix %s || true", scriptPath)
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
			testCmd := fmt.Sprintf("curl -s -I --max-time 5 '%s' | head -n 1 | grep -q '200'", cdnURL)
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
		testCmd := fmt.Sprintf("curl -s -I --max-time 5 '%s' | head -n 1 | grep -q '200'", cdnURL)
		if _, err := i.sshClient.Execute(testCmd); err == nil {
			return cdnURL
		}
	}
	return ""
}
