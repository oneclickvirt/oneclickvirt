package incus

import (
	"context"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// validateInstanceConfig 验证实例配置
func (i *IncusProvider) validateInstanceConfig(config provider.InstanceConfig) error {
	if config.Name == "" {
		return fmt.Errorf("实例名称不能为空")
	}

	if !utils.IsValidLXDInstanceName(config.Name) {
		return fmt.Errorf("实例名称格式无效: %s", config.Name)
	}

	if config.Memory != "" {
		// 检查内存格式是否有效
		if convertMemoryFormat(config.Memory) == config.Memory && !strings.HasSuffix(config.Memory, "iB") {
			// 如果convertMemoryFormat没有转换且不以iB结尾，可能是无效格式
			global.APP_LOG.Warn("内存格式可能无效", zap.String("memory", config.Memory))
		}
	}

	return nil
}

// instanceExists 检查实例是否已存在
func (i *IncusProvider) instanceExists(name string) (bool, error) {
	cmd := fmt.Sprintf("incus list %s --format csv", name)
	output, err := i.sshClient.Execute(cmd)
	if err != nil {
		return false, fmt.Errorf("检查实例是否存在失败: %w", err)
	}

	return strings.TrimSpace(output) != "", nil
}

// buildCreateCommand 构建创建命令
func (i *IncusProvider) buildCreateCommand(config provider.InstanceConfig) (string, error) {
	var cmd string

	global.APP_LOG.Info("开始构建创建命令",
		zap.String("instance_name", config.Name),
		zap.String("image", config.Image),
		zap.String("instance_type", config.InstanceType),
		zap.String("cpu", config.CPU),
		zap.String("memory", config.Memory),
		zap.String("disk", config.Disk))

	// 根据实例类型构建基础命令
	if config.InstanceType == "vm" {
		cmd = fmt.Sprintf("incus init %s %s --vm", config.Image, config.Name)
	} else {
		cmd = fmt.Sprintf("incus init %s %s", config.Image, config.Name)
	}

	// 基础配置参数
	// 始终应用资源参数，资源限制配置只影响Provider层面的资源预算计算
	configParams := []string{}

	if config.CPU != "" {
		configParams = append(configParams, fmt.Sprintf("limits.cpu=%s", config.CPU))
	}

	if config.Memory != "" {
		memoryFormatted := convertMemoryFormat(config.Memory)
		configParams = append(configParams, fmt.Sprintf("limits.memory=%s", memoryFormatted))
	}

	// 实例类型特定的配置
	if config.InstanceType == "vm" {
		// 虚拟机特定配置
		configParams = append(configParams, "security.secureboot=false")
		configParams = append(configParams, "limits.memory.swap=true")
		configParams = append(configParams, "limits.cpu.priority=0")
	} else {
		// 容器特定配置 - 应用容器特殊配置选项
		// 1. 特权模式配置（Privileged）
		if config.Privileged != nil {
			if *config.Privileged {
				configParams = append(configParams, "security.privileged=true")
			} else {
				configParams = append(configParams, "security.privileged=false")
			}
		}

		// 2. 容器嵌套配置（Allow Nesting）
		if config.AllowNesting != nil {
			if *config.AllowNesting {
				configParams = append(configParams, "security.nesting=true")
			} else {
				configParams = append(configParams, "security.nesting=false")
			}
		} else {
			// 默认启用嵌套（保持原有行为）
			configParams = append(configParams, "security.nesting=true")
		}

		// 3. CPU限制配置（CPU Allowance vs limits.cpu）
		if config.CPUAllowance != nil && *config.CPUAllowance != "" && *config.CPUAllowance != "100%" {
			configParams = append(configParams, fmt.Sprintf("limits.cpu.allowance=%s", *config.CPUAllowance))
			configParams = append(configParams, "limits.cpu.priority=0")
		} else {
			configParams = append(configParams, "limits.cpu.priority=0")
			configParams = append(configParams, "limits.cpu.allowance=50%")
			configParams = append(configParams, "limits.cpu.allowance=25ms/100ms")
		}

		// 4. 内存交换配置（Memory Swap）
		if config.MemorySwap != nil {
			if *config.MemorySwap {
				configParams = append(configParams, "limits.memory.swap=true")
				configParams = append(configParams, "limits.memory.swap.priority=1")
			} else {
				configParams = append(configParams, "limits.memory.swap=false")
			}
		} else {
			// 默认启用swap（保持原有行为）
			configParams = append(configParams, "limits.memory.swap=true")
			configParams = append(configParams, "limits.memory.swap.priority=1")
		}

		// 5. 最大进程数配置（Max Processes）
		if config.MaxProcesses != nil && *config.MaxProcesses > 0 {
			configParams = append(configParams, fmt.Sprintf("limits.processes=%d", *config.MaxProcesses))
		}

		// 磁盘IO限制将在实例创建后通过device命令设置
		if config.DiskIOLimit != nil && *config.DiskIOLimit != "" {
			if config.Metadata == nil {
				config.Metadata = make(map[string]string)
			}
			config.Metadata["disk_io_limit"] = *config.DiskIOLimit
		}
	}

	// 配置参数到命令
	for _, param := range configParams {
		cmd += fmt.Sprintf(" -c %s", param)
	}

	// 如果有磁盘大小配置
	if config.Disk != "" {
		diskFormatted := convertDiskFormat(config.Disk)
		cmd += fmt.Sprintf(" -d root,size=%s", diskFormatted)
	}

	global.APP_LOG.Info("构建的完整创建命令",
		zap.String("full_command", cmd),
		zap.Strings("config_params", configParams))

	return cmd, nil
}

// executeCreateCommand 执行创建命令
func (i *IncusProvider) executeCreateCommand(cmd string) error {
	// 输出完整的创建命令用于调试
	global.APP_LOG.Info("准备执行实例创建命令",
		zap.String("full_command", cmd))

	output, err := i.sshClient.Execute(cmd)
	if err != nil {
		// 尝试获取更详细的错误信息
		instanceName := ""
		cmdParts := strings.Fields(cmd)
		if len(cmdParts) >= 3 {
			instanceName = cmdParts[2]
		}

		global.APP_LOG.Error("实例创建命令执行失败",
			zap.String("command", cmd),
			zap.String("output", output),
			zap.String("instanceName", instanceName),
			zap.Error(err))

		// 如果实例已经存在，提供更友好的错误信息
		if strings.Contains(err.Error(), "already exists") || strings.Contains(output, "already exists") {
			return fmt.Errorf("实例 %s 已存在", instanceName)
		}

		return fmt.Errorf("创建实例失败: %w", err)
	}

	global.APP_LOG.Info("实例创建命令执行成功", zap.String("output", output))
	return nil
}

// waitForInstanceState 等待实例达到指定状态
func (i *IncusProvider) waitForInstanceState(name, expectedState string, timeoutSeconds int) error {
	for elapsed := 0; elapsed < timeoutSeconds; elapsed += 3 {
		cmd := fmt.Sprintf("incus info %s | grep \"Status:\" | awk '{print $2}'", name)
		output, err := i.sshClient.Execute(cmd)
		if err != nil {
			global.APP_LOG.Debug("获取实例状态失败",
				zap.String("name", name),
				zap.Error(err))
			time.Sleep(3 * time.Second)
			continue
		}

		currentState := strings.TrimSpace(output)
		if currentState == expectedState {
			global.APP_LOG.Info("实例达到期望状态",
				zap.String("name", name),
				zap.String("state", expectedState))
			return nil
		}

		global.APP_LOG.Debug("等待实例状态变化",
			zap.String("name", name),
			zap.String("currentState", currentState),
			zap.String("expectedState", expectedState),
			zap.Int("elapsed", elapsed))

		time.Sleep(3 * time.Second)
	}

	return fmt.Errorf("等待实例状态超时: %s", expectedState)
}

// checkVMSupport 检查Incus是否支持虚拟机
func (i *IncusProvider) checkVMSupport() error {
	global.APP_LOG.Info("检查Incus虚拟机支持...")

	// 检查incus命令是否可用
	_, err := i.sshClient.Execute("command -v incus")
	if err != nil {
		return fmt.Errorf("Incus未安装或不在PATH中: %w", err)
	}

	// 获取Incus驱动信息
	output, err := i.sshClient.Execute("incus info | grep -i 'driver:'")
	if err != nil {
		return fmt.Errorf("无法获取Incus驱动信息: %w", err)
	}

	global.APP_LOG.Debug("Incus可用驱动", zap.String("drivers", output))

	// 检查是否支持qemu驱动（虚拟机所需）
	if !strings.Contains(strings.ToLower(output), "qemu") {
		return fmt.Errorf("Incus不支持虚拟机 (未找到qemu驱动)，此系统仅支持容器")
	}

	global.APP_LOG.Info("已确认Incus支持虚拟机 - qemu驱动可用")
	return nil
}

// configureVMSettings 配置虚拟机特有设置
func (i *IncusProvider) configureVMSettings(ctx context.Context, instanceName string) error {
	global.APP_LOG.Info("配置虚拟机特有设置", zap.String("instance", instanceName))

	// 禁用安全启动（虚拟机常用配置）
	if err := i.setInstanceConfig(ctx, instanceName, "security.secureboot", "false"); err != nil {
		global.APP_LOG.Warn("禁用安全启动失败，但继续",
			zap.String("instance", instanceName),
			zap.Error(err))
	}

	return nil
}

// configureInstanceSSHPassword 专门用于设置实例的SSH密码
func (i *IncusProvider) configureInstanceSSHPassword(ctx context.Context, config provider.InstanceConfig) error {
	global.APP_LOG.Info("开始配置实例SSH密码",
		zap.String("instanceName", config.Name))

	// 生成随机密码
	password := i.generateRandomPassword()

	// 根据系统类型选择脚本
	var scriptName string
	// 检测系统类型
	output, err := i.sshClient.Execute(fmt.Sprintf("incus exec %s -- cat /etc/os-release 2>/dev/null | grep ^ID= | cut -d= -f2 | tr -d '\"'", config.Name))
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

	scriptPath := fmt.Sprintf("/usr/local/bin/%s", scriptName)
	// 检查脚本是否存在
	if !i.isRemoteFileValid(scriptPath) {
		global.APP_LOG.Warn("SSH脚本不存在，仅设置密码不配置SSH",
			zap.String("scriptPath", scriptPath))
		// 即使脚本不存在，也要设置密码
	} else {
		time.Sleep(3 * time.Second)
		// 复制脚本到实例
		copyCmd := fmt.Sprintf("incus file push %s %s/root/", scriptPath, config.Name)
		_, err = i.sshClient.Execute(copyCmd)
		if err != nil {
			global.APP_LOG.Warn("复制SSH脚本到实例失败，仅设置密码", zap.Error(err))
		} else {
			// 设置脚本权限
			_, err = i.sshClient.Execute(fmt.Sprintf("incus exec %s -- chmod +x /root/%s", config.Name, scriptName))
			if err != nil {
				global.APP_LOG.Warn("设置脚本权限失败", zap.Error(err))
			} else {
				// 执行脚本配置SSH和密码
				execCmd := fmt.Sprintf("incus exec %s -- /root/%s %s", config.Name, scriptName, password)
				_, err = i.sshClient.Execute(execCmd)
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

	// 直接使用incus exec设置密码
	directPasswordCmd := fmt.Sprintf("incus exec %s -- bash -c 'echo \"root:%s\" | chpasswd'", config.Name, password)
	_, err = i.sshClient.Execute(directPasswordCmd)
	if err != nil {
		global.APP_LOG.Error("设置实例密码失败",
			zap.String("instanceName", config.Name),
			zap.Error(err))
		return fmt.Errorf("设置实例密码失败: %w", err)
	}

	// 清理历史记录 - 非阻塞式，如果失败不影响整体流程
	_, err = i.sshClient.Execute(fmt.Sprintf("incus exec %s -- bash -c 'history -c 2>/dev/null || true'", config.Name))
	if err != nil {
		global.APP_LOG.Warn("清理历史记录失败",
			zap.String("instanceName", config.Name),
			zap.Error(err))
	}

	global.APP_LOG.Info("实例SSH密码设置完成",
		zap.String("instanceName", config.Name),
		zap.String("rootPassword", password))

	// 保存密码到实例配置中（用于后续获取）
	if err = i.setInstanceConfig(ctx, config.Name, "user.password", password); err != nil {
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
func (i *IncusProvider) waitForInstanceExecReady(instanceName string, timeoutSeconds int) error {
	global.APP_LOG.Info("开始等待实例可执行命令",
		zap.String("instanceName", instanceName),
		zap.Int("timeout", timeoutSeconds))
	time.Sleep(12 * time.Second)
	loopCount := 0
	for elapsed := 0; elapsed < timeoutSeconds; elapsed += 5 {
		// 每两轮循环（10秒）尝试启动实例，避免实例因故障停止导致一直干等待
		if loopCount > 0 && loopCount%2 == 0 {
			startCmd := fmt.Sprintf("incus start %s", instanceName)
			startOutput, startErr := i.sshClient.Execute(startCmd)
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
		cmd := fmt.Sprintf("incus exec %s -- echo 'agent-ready' 2>/dev/null", instanceName)
		output, err := i.sshClient.Execute(cmd)
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
