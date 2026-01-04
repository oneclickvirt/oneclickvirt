package lxd

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"

	"go.uber.org/zap"
)

// configureInstanceStorage 配置实例存储
func (l *LXDProvider) configureInstanceStorage(ctx context.Context, config provider.InstanceConfig) error {
	// 如果是容器，配置IO限制
	if config.InstanceType != "vm" {
		// 设置读写限制
		if err := l.setInstanceDeviceConfig(ctx, config.Name, "root", "limits.read", "500MB"); err != nil {
			global.APP_LOG.Warn("设置读取限制失败", zap.Error(err))
		}

		if err := l.setInstanceDeviceConfig(ctx, config.Name, "root", "limits.write", "500MB"); err != nil {
			global.APP_LOG.Warn("设置写入限制失败", zap.Error(err))
		}

		// 设置IOPS限制
		if err := l.setInstanceDeviceConfig(ctx, config.Name, "root", "limits.read", "5000iops"); err != nil {
			global.APP_LOG.Warn("设置读取IOPS限制失败", zap.Error(err))
		}

		if err := l.setInstanceDeviceConfig(ctx, config.Name, "root", "limits.write", "5000iops"); err != nil {
			global.APP_LOG.Warn("设置写入IOPS限制失败", zap.Error(err))
		}
	}

	return nil
}

// configureInstanceSecurity 配置实例安全设置
func (l *LXDProvider) configureInstanceSecurity(ctx context.Context, config provider.InstanceConfig) error {
	if config.InstanceType == "vm" {
		// 虚拟机安全配置
		if err := l.setInstanceConfig(ctx, config.Name, "security.secureboot", "false"); err != nil {
			global.APP_LOG.Warn("设置SecureBoot失败", zap.Error(err))
		}
	} else {
		// 容器安全配置
		if err := l.setInstanceConfig(ctx, config.Name, "security.nesting", "true"); err != nil {
			global.APP_LOG.Warn("设置容器嵌套失败", zap.Error(err))
		}

		// CPU优先级配置
		if err := l.setInstanceConfig(ctx, config.Name, "limits.cpu.priority", "0"); err != nil {
			global.APP_LOG.Warn("设置CPU优先级失败", zap.Error(err))
		}

		// 内存交换配置
		if err := l.setInstanceConfig(ctx, config.Name, "limits.memory.swap", "true"); err != nil {
			global.APP_LOG.Warn("设置内存交换失败", zap.Error(err))
		}

		if err := l.setInstanceConfig(ctx, config.Name, "limits.memory.swap.priority", "1"); err != nil {
			global.APP_LOG.Warn("设置内存交换优先级失败", zap.Error(err))
		}
	}

	return nil
}

// setInstanceConfig 通用的实例配置设置方法，根据执行规则选择API或SSH
func (l *LXDProvider) setInstanceConfig(ctx context.Context, instanceName string, key string, value string) error {
	// 根据执行规则判断使用哪种方式
	if l.shouldUseAPI() {
		if err := l.apiSetInstanceConfig(ctx, instanceName, key, value); err == nil {
			global.APP_LOG.Debug("LXD API设置实例配置成功",
				zap.String("instance", instanceName),
				zap.String("key", key),
				zap.String("value", value))
			return nil
		} else {
			global.APP_LOG.Warn("LXD API设置实例配置失败", zap.Error(err))

			// 检查是否可以回退到SSH
			if !l.shouldFallbackToSSH() {
				return fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
			}
			global.APP_LOG.Info("回退到SSH执行 - 设置实例配置",
				zap.String("instance", instanceName),
				zap.String("key", key))
		}
	}

	// 如果执行规则不允许使用SSH，则返回错误
	if !l.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	// SSH方式设置配置
	cmd := fmt.Sprintf("lxc config set %s %s %s", instanceName, key, value)
	_, err := l.sshClient.Execute(cmd)
	if err != nil {
		return fmt.Errorf("SSH设置实例配置失败: %w", err)
	}

	global.APP_LOG.Debug("LXD SSH设置实例配置成功",
		zap.String("instance", instanceName),
		zap.String("key", key),
		zap.String("value", value))
	return nil
}

// setInstanceDeviceConfig 通用的实例设备配置设置方法，根据执行规则选择API或SSH
func (l *LXDProvider) setInstanceDeviceConfig(ctx context.Context, instanceName string, deviceName string, key string, value string) error {
	// 根据执行规则判断使用哪种方式
	if l.shouldUseAPI() {
		if err := l.apiSetInstanceDeviceConfig(ctx, instanceName, deviceName, key, value); err == nil {
			global.APP_LOG.Debug("LXD API设置实例设备配置成功",
				zap.String("instance", instanceName),
				zap.String("device", deviceName),
				zap.String("key", key),
				zap.String("value", value))
			return nil
		} else {
			global.APP_LOG.Warn("LXD API设置实例设备配置失败", zap.Error(err))

			// 检查是否可以回退到SSH
			if !l.shouldFallbackToSSH() {
				return fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
			}
			global.APP_LOG.Info("回退到SSH执行 - 设置实例设备配置",
				zap.String("instance", instanceName),
				zap.String("device", deviceName),
				zap.String("key", key))
		}
	}

	// 如果执行规则不允许使用SSH，则返回错误
	if !l.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	// SSH方式设置设备配置
	cmd := fmt.Sprintf("lxc config device set %s %s %s %s", instanceName, deviceName, key, value)
	_, err := l.sshClient.Execute(cmd)
	if err != nil {
		return fmt.Errorf("SSH设置实例设备配置失败: %w", err)
	}

	global.APP_LOG.Debug("LXD SSH设置实例设备配置成功",
		zap.String("instance", instanceName),
		zap.String("device", deviceName),
		zap.String("key", key),
		zap.String("value", value))
	return nil
}

// waitForInstanceReady 等待实例就绪
func (l *LXDProvider) waitForInstanceReady(ctx context.Context, instanceName string) error {
	global.APP_LOG.Info("等待LXD实例就绪", zap.String("instance", instanceName))

	timeout := 50 * time.Second
	interval := 3 * time.Second
	startTime := time.Now()

	// 使用Timer避免time.After泄漏
	timer := time.NewTimer(interval)
	defer timer.Stop()

	for {
		if time.Since(startTime) > timeout {
			return fmt.Errorf("等待实例就绪超时: %s", instanceName)
		}

		// 检查实例状态
		cmd := fmt.Sprintf("lxc info %s | grep \"Status:\" | awk '{print $2}'", instanceName)
		output, err := l.sshClient.Execute(cmd)
		if err != nil {
			global.APP_LOG.Debug("获取实例状态失败",
				zap.String("instance", instanceName),
				zap.Error(err))
			timer.Reset(interval)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				continue
			}
		}

		status := strings.TrimSpace(output)
		global.APP_LOG.Debug("实例状态检查",
			zap.String("instance", instanceName),
			zap.String("status", status))

		if strings.ToLower(status) == "running" {
			global.APP_LOG.Info("LXD实例已就绪", zap.String("instance", instanceName))
			return nil
		}

		timer.Reset(interval)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			// 继续等待
		}
	}
}

// configureInstanceSystem 配置实例系统
func (l *LXDProvider) configureInstanceSystem(ctx context.Context, config provider.InstanceConfig) error {
	global.APP_LOG.Info("开始配置LXD实例系统",
		zap.String("instance", config.Name),
		zap.String("type", config.InstanceType))
	if config.InstanceType != "vm" {
		_ = l.setInstanceConfig(ctx, config.Name, "boot.autostart", "true")
		_ = l.setInstanceConfig(ctx, config.Name, "boot.autostart.priority", "50")
		_ = l.setInstanceConfig(ctx, config.Name, "boot.autostart.delay", "10")
	}
	global.APP_LOG.Info("实例系统配置完成",
		zap.String("instanceName", config.Name))
	return nil
}

// checkVMSupport 检查LXD是否支持虚拟机（参考官方buildvm.sh的check_vm_support函数）
func (l *LXDProvider) checkVMSupport() error {
	global.APP_LOG.Info("检查LXD虚拟机支持...")

	// 检查lxc命令是否可用
	_, err := l.sshClient.Execute("command -v lxc")
	if err != nil {
		return fmt.Errorf("LXD未安装或不在PATH中: %w", err)
	}

	// 获取LXD驱动信息
	output, err := l.sshClient.Execute("lxc info | grep -i 'driver:'")
	if err != nil {
		return fmt.Errorf("无法获取LXD驱动信息: %w", err)
	}

	global.APP_LOG.Debug("LXD可用驱动", zap.String("drivers", output))

	// 检查是否支持qemu驱动（虚拟机所需）
	if !strings.Contains(strings.ToLower(output), "qemu") {
		return fmt.Errorf("LXD不支持虚拟机 (未找到qemu驱动)，此系统仅支持容器")
	}

	global.APP_LOG.Info("已确认LXD支持虚拟机 - qemu驱动可用")
	return nil
}

// configureVMSettings 配置虚拟机特有设置（参考官方buildvm.sh的configure_limits函数）
func (l *LXDProvider) configureVMSettings(ctx context.Context, instanceName string) error {
	global.APP_LOG.Info("配置虚拟机特有设置", zap.String("instance", instanceName))

	// 禁用安全启动（虚拟机常用配置）
	if err := l.setInstanceConfig(ctx, instanceName, "security.secureboot", "false"); err != nil {
		global.APP_LOG.Warn("禁用安全启动失败，但继续",
			zap.String("instance", instanceName),
			zap.Error(err))
	}

	return nil
}

// configureInstanceSSHPassword 专门用于设置实例的SSH密码
func (l *LXDProvider) configureInstanceSSHPassword(ctx context.Context, config provider.InstanceConfig) error {
	global.APP_LOG.Info("开始配置实例SSH密码",
		zap.String("instanceName", config.Name))

	// 生成随机密码
	password := l.generateRandomPassword()

	// 根据系统类型选择脚本
	var scriptName string
	// 检测系统类型
	output, err := l.sshClient.Execute(fmt.Sprintf("lxc exec %s -- cat /etc/os-release 2>/dev/null | grep ^ID= | cut -d= -f2 | tr -d '\"'", config.Name))
	if err == nil {
		osType := strings.TrimSpace(strings.ToLower(output))
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
	if !l.isRemoteFileValid(scriptPath) {
		global.APP_LOG.Warn("SSH脚本不存在，仅设置密码不配置SSH",
			zap.String("scriptPath", scriptPath))
		// 即使脚本不存在，也要设置密码
	} else {
		time.Sleep(3 * time.Second)
		// 复制脚本到实例
		copyCmd := fmt.Sprintf("lxc file push %s %s/root/", scriptPath, config.Name)
		_, err = l.sshClient.Execute(copyCmd)
		if err != nil {
			global.APP_LOG.Warn("复制SSH脚本到实例失败，仅设置密码", zap.Error(err))
		} else {
			// 设置脚本权限
			_, err = l.sshClient.Execute(fmt.Sprintf("lxc exec %s -- chmod +x /root/%s", config.Name, scriptName))
			if err != nil {
				global.APP_LOG.Warn("设置脚本权限失败", zap.Error(err))
			} else {
				// 执行脚本配置SSH和密码
				execCmd := fmt.Sprintf("lxc exec %s -- /root/%s %s", config.Name, scriptName, password)
				_, err = l.sshClient.Execute(execCmd)
				if err != nil {
					global.APP_LOG.Warn("执行SSH配置脚本失败，将使用直接设置密码",
						zap.String("instanceName", config.Name),
						zap.String("scriptName", scriptName),
						zap.Error(err))
				}
				time.Sleep(3 * time.Second)
			}
		}
	}

	// 直接使用lxc exec设置密码
	directPasswordCmd := fmt.Sprintf("lxc exec %s -- bash -c 'echo \"root:%s\" | chpasswd'", config.Name, password)
	_, err = l.sshClient.Execute(directPasswordCmd)
	if err != nil {
		global.APP_LOG.Error("设置实例密码失败",
			zap.String("instanceName", config.Name),
			zap.Error(err))
		return fmt.Errorf("设置实例密码失败: %w", err)
	}

	// 清理历史记录 - 非阻塞式，如果失败不影响整体流程
	_, err = l.sshClient.Execute(fmt.Sprintf("lxc exec %s -- bash -c 'history -c 2>/dev/null || true'", config.Name))
	if err != nil {
		global.APP_LOG.Warn("清理历史记录失败",
			zap.String("instanceName", config.Name),
			zap.Error(err))
	}

	global.APP_LOG.Info("实例SSH密码设置完成",
		zap.String("instanceName", config.Name),
		zap.String("rootPassword", password))

	// 保存密码到实例配置中（用于后续获取）
	if err = l.setInstanceConfig(ctx, config.Name, "user.password", password); err != nil {
		global.APP_LOG.Warn("保存密码到实例配置失败", zap.Error(err))
	}

	// 更新数据库中的密码记录，确保数据库与实际密码一致
	err = global.APP_DB.Model(&providerModel.Instance{}).
		Where("name = ?", config.Name).
		Update("password", password).Error
	if err != nil {
		global.APP_LOG.Warn("更新实例密码到数据库失败",
			zap.String("instanceName", config.Name),
			zap.Error(err))
	} else {
		global.APP_LOG.Info("实例密码已同步到数据库",
			zap.String("instanceName", config.Name))
	}
	return nil
}

// waitForInstanceExecReady 等待实例可以执行命令（容器直接可用，虚拟机需要等待Agent）
func (l *LXDProvider) waitForInstanceExecReady(instanceName string, timeoutSeconds int) error {
	global.APP_LOG.Info("开始等待实例可执行命令",
		zap.String("instanceName", instanceName),
		zap.Int("timeout", timeoutSeconds))
	time.Sleep(12 * time.Second)
	loopCount := 0
	for elapsed := 0; elapsed < timeoutSeconds; elapsed += 5 {
		// 每两轮循环（10秒）尝试启动实例，避免实例因故障停止导致一直干等待
		if loopCount > 0 && loopCount%2 == 0 {
			startCmd := fmt.Sprintf("lxc start %s", instanceName)
			startOutput, startErr := l.sshClient.Execute(startCmd)
			// "already running" 不是错误，而是实例已在运行的正常状态
			if startErr == nil || strings.Contains(startOutput, "already running") {
				global.APP_LOG.Debug("实例已启动或正在运行",
					zap.String("instanceName", instanceName),
					zap.Int("loopCount", loopCount))
			} else {
				global.APP_LOG.Warn("启动实例失败",
					zap.String("instanceName", instanceName),
					zap.String("output", startOutput),
					zap.Error(startErr))
			}
		}

		// 尝试执行一个简单的命令来检测VM agent是否就绪
		cmd := fmt.Sprintf("lxc exec %s -- echo 'agent-ready' 2>/dev/null", instanceName)
		output, err := l.sshClient.Execute(cmd)
		if err == nil && strings.Contains(output, "agent-ready") {
			global.APP_LOG.Info("实例可执行命令",
				zap.String("instanceName", instanceName),
				zap.Int("elapsed", elapsed))
			time.Sleep(12 * time.Second)
			return nil
		}
		global.APP_LOG.Debug("等待实例就绪",
			zap.String("instanceName", instanceName),
			zap.Int("elapsed", elapsed),
			zap.Int("timeout", timeoutSeconds),
			zap.Error(err))
		loopCount++
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("等待实例可执行命令超时 (%d秒)", timeoutSeconds)
}
