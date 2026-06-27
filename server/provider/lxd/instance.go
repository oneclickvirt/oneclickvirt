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
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

func isLXDConfigUnsupportedError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := strings.ToLower(err.Error())
	return strings.Contains(errMsg, "unknown key") ||
		strings.Contains(errMsg, "invalid config") ||
		strings.Contains(errMsg, "not supported") ||
		strings.Contains(errMsg, "cgroup controller is missing")
}

// configureInstanceStorage 配置实例存储
func (l *LXDProvider) configureInstanceStorage(ctx context.Context, config provider.InstanceConfig) error {
	// 参考: https://github.com/oneclickvirt/lxd/blob/main/scripts/buildct.sh
	// 磁盘大小在创建实例后通过 device set 设置（不再使用 -d 标志，避免 profile 缺少 root 设备时失败）

	// 获取 sshClient（带锁保护）
	l.mu.RLock()
	client := l.sshClient
	l.mu.RUnlock()

	// 设置 root 磁盘大小（在创建时不使用 -d 标志的情况下，后置设置）
	if config.Disk != "" {
		diskFormatted := convertDiskFormat(config.Disk)
		if client != nil {
			// 优先用新语法设置磁盘大小
			setSizeCmd := fmt.Sprintf("lxc config device set %s root size=%s", shellSingleQuote(config.Name), shellSingleQuote(diskFormatted))
			if _, err := client.Execute(setSizeCmd); err != nil {
				// 兼容旧语法
				legacySizeCmd := fmt.Sprintf("lxc config device set %s root size %s", shellSingleQuote(config.Name), shellSingleQuote(diskFormatted))
				if _, legacyErr := client.Execute(legacySizeCmd); legacyErr != nil {
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

		// 设置 limits.max（磁盘配额）
		setMaxCmd := fmt.Sprintf("lxc config device set %s root limits.max=%s", shellSingleQuote(config.Name), shellSingleQuote(diskFormatted))
		if client == nil {
			global.APP_LOG.Warn("SSH client不可用，跳过设置磁盘limits.max",
				zap.String("instance", config.Name))
		} else if _, err := client.Execute(setMaxCmd); err != nil {
			legacyCmd := fmt.Sprintf("lxc config device set %s root limits.max %s", shellSingleQuote(config.Name), shellSingleQuote(diskFormatted))
			if _, legacyErr := client.Execute(legacyCmd); legacyErr != nil {
				global.APP_LOG.Warn("设置磁盘limits.max失败",
					zap.String("command", legacyCmd),
					zap.Error(legacyErr))
			} else {
				global.APP_LOG.Debug("已通过legacy语法设置磁盘limits.max限制",
					zap.String("instance", config.Name),
					zap.String("limits.max", diskFormatted))
			}
		} else {
			global.APP_LOG.Debug("已设置磁盘limits.max限制",
				zap.String("instance", config.Name),
				zap.String("limits.max", diskFormatted))
		}
	}

	readLimit, writeLimit := lxdResolveIOLimits(config)
	// 配置IO限制。旧的 containerDiskIoLimit 仅作为容器回退值；新读/写字段适用于容器和 VM。
	// copy模式下容器继承来自profile的root设备，lxc config device set 需要容器有显式root设备
	// 先确保root设备存在（若不存在则添加），再设置限制
	if readLimit != "" || writeLimit != "" {
		if client != nil && config.CopyMode {
			// 检查容器是否有显式root设备
			checkCmd := fmt.Sprintf("lxc config device list %s", shellSingleQuote(config.Name))
			output, err := client.Execute(checkCmd)
			if err != nil || !strings.Contains(output, "root") {
				// root设备继承自profile，无法直接 device set；先 add 一个显式root设备
				// 使用真实存在的存储池；配置无效时自动从远端 storage list 纠正，
				// 不再把 local/空值硬切到 default，避免 default 池不存在时失败。
				poolName := l.resolveStoragePoolForInstance()
				var addErr error
				if poolName != "" {
					addRootCmd := fmt.Sprintf("lxc config device add %s root disk path=/ pool=%s", shellSingleQuote(config.Name), shellSingleQuote(poolName))
					_, addErr = client.Execute(addRootCmd)
				} else {
					addErr = fmt.Errorf("no usable LXD storage pool detected")
				}
				if addErr != nil {
					// 不指定 pool 重试（让 LXD 使用默认池）
					addRootCmd2 := fmt.Sprintf("lxc config device add %s root disk path=/", shellSingleQuote(config.Name))
					if _, addErr2 := client.Execute(addRootCmd2); addErr2 != nil {
						global.APP_LOG.Warn("copy模式下添加显式root设备失败，跳过IO限制设置",
							zap.String("instance", config.Name),
							zap.String("pool", poolName),
							zap.Error(addErr2))
						return nil
					}
				}
				global.APP_LOG.Debug("copy模式下已添加显式root设备",
					zap.String("instance", config.Name),
					zap.String("pool", poolName))
			}
		}

		if readLimit != "" {
			if err := l.setInstanceDeviceConfig(ctx, config.Name, "root", "limits.read", readLimit); err != nil {
				global.APP_LOG.Warn("设置读取IO速率限制失败",
					zap.String("instance", config.Name),
					zap.String("limit", readLimit),
					zap.Error(err))
			}
		}
		if writeLimit != "" {
			if err := l.setInstanceDeviceConfig(ctx, config.Name, "root", "limits.write", writeLimit); err != nil {
				global.APP_LOG.Warn("设置写入IO速率限制失败",
					zap.String("instance", config.Name),
					zap.String("limit", writeLimit),
					zap.Error(err))
			}
		}
	}

	return nil
}

func lxdResolveIOLimits(config provider.InstanceConfig) (string, string) {
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

// configureInstanceGPU 配置实例GPU直通（仅 LXD/Incus 容器，VM不支持通过此方式直通）
// GPU驱动需要用户在宿主机上手动安装，本函数仅负责附加GPU设备
func (l *LXDProvider) configureInstanceGPU(ctx context.Context, config provider.InstanceConfig) error {
	if !config.GpuEnabled {
		return nil
	}
	if config.InstanceType == "vm" {
		global.APP_LOG.Warn("GPU直通当前不支持虚拟机实例，跳过", zap.String("instance", config.Name))
		return nil
	}

	ids := strings.Split(config.GpuDeviceIds, ",")
	if config.GpuDeviceIds == "" {
		// 附加所有GPU（不指定ID）
		cmd := fmt.Sprintf("lxc config device add %s gpu gpu", shellSingleQuote(config.Name))
		if _, err := l.sshClient.Execute(cmd); err != nil {
			return fmt.Errorf("附加GPU设备失败: %w", err)
		}
		global.APP_LOG.Info("已为实例附加所有GPU设备", zap.String("instance", config.Name))
	} else {
		for i, rawID := range ids {
			id := strings.TrimSpace(rawID)
			if id == "" {
				continue
			}
			deviceName := fmt.Sprintf("gpu%d", i)
			cmd := fmt.Sprintf("lxc config device add %s %s gpu id=%s", shellSingleQuote(config.Name), shellSingleQuote(deviceName), shellSingleQuote(id))
			if _, err := l.sshClient.Execute(cmd); err != nil {
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

// configureInstanceSecurity 配置实例安全设置
func (l *LXDProvider) configureInstanceSecurity(ctx context.Context, config provider.InstanceConfig) error {
	swapValue := "true"
	if config.MemorySwap != nil && !*config.MemorySwap {
		swapValue = "false"
	}

	if config.InstanceType == "vm" {
		// 虚拟机安全配置
		if err := l.setInstanceConfig(ctx, config.Name, "security.secureboot", "false"); err != nil {
			global.APP_LOG.Warn("设置SecureBoot失败", zap.Error(err))
		}

		if err := l.setInstanceConfig(ctx, config.Name, "limits.cpu.priority", "0"); err != nil {
			if isLXDConfigUnsupportedError(err) {
				global.APP_LOG.Warn("设置CPU优先级失败，当前节点不支持该配置，已跳过", zap.Error(err))
			} else {
				global.APP_LOG.Warn("设置CPU优先级失败", zap.Error(err))
			}
		}

		swapEnabled := true
		if err := l.setInstanceConfig(ctx, config.Name, "limits.memory.swap", swapValue); err != nil {
			if isLXDConfigUnsupportedError(err) {
				swapEnabled = false
				global.APP_LOG.Warn("设置内存交换失败，当前节点不支持该配置，已跳过", zap.Error(err))
			} else {
				swapEnabled = false
				global.APP_LOG.Warn("设置内存交换失败", zap.Error(err))
			}
		}

		if swapEnabled && swapValue == "true" {
			if err := l.setInstanceConfig(ctx, config.Name, "limits.memory.swap.priority", "1"); err != nil {
				if isLXDConfigUnsupportedError(err) {
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
		if err := l.setInstanceConfig(ctx, config.Name, "security.nesting", nestingValue); err != nil {
			if isLXDConfigUnsupportedError(err) {
				global.APP_LOG.Warn("设置容器嵌套失败，当前节点不支持该配置，已跳过", zap.Error(err))
			} else {
				global.APP_LOG.Warn("设置容器嵌套失败", zap.Error(err))
			}
		}

		// CPU优先级配置
		if err := l.setInstanceConfig(ctx, config.Name, "limits.cpu.priority", "0"); err != nil {
			if isLXDConfigUnsupportedError(err) {
				global.APP_LOG.Warn("设置CPU优先级失败，当前节点不支持该配置，已跳过", zap.Error(err))
			} else {
				global.APP_LOG.Warn("设置CPU优先级失败", zap.Error(err))
			}
		}

		// 内存交换配置
		swapEnabled := true
		if err := l.setInstanceConfig(ctx, config.Name, "limits.memory.swap", swapValue); err != nil {
			if isLXDConfigUnsupportedError(err) {
				swapEnabled = false
				global.APP_LOG.Warn("设置内存交换失败，当前节点不支持该配置，已跳过", zap.Error(err))
			} else {
				swapEnabled = false
				global.APP_LOG.Warn("设置内存交换失败", zap.Error(err))
			}
		}

		if swapEnabled && swapValue == "true" {
			if err := l.setInstanceConfig(ctx, config.Name, "limits.memory.swap.priority", "1"); err != nil {
				if isLXDConfigUnsupportedError(err) {
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

		if config.CPUAllowance != nil && *config.CPUAllowance != "" && *config.CPUAllowance != "100%" {
			if err := l.setInstanceConfig(ctx, config.Name, "limits.cpu.allowance", *config.CPUAllowance); err != nil {
				global.APP_LOG.Warn("设置CPU配额限制失败", zap.Error(err))
			}
		} else {
			if err := l.setInstanceConfig(ctx, config.Name, "limits.cpu.allowance", "50%"); err != nil {
				global.APP_LOG.Debug("设置CPU配额限制(50%)失败，继续执行", zap.Error(err))
			}
			if err := l.setInstanceConfig(ctx, config.Name, "limits.cpu.allowance", "25ms/100ms"); err != nil {
				global.APP_LOG.Debug("设置CPU配额限制(25ms/100ms)失败，继续执行", zap.Error(err))
			}
		}

		if config.MaxProcesses != nil && *config.MaxProcesses > 0 {
			if err := l.setInstanceConfig(ctx, config.Name, "limits.processes", fmt.Sprintf("%d", *config.MaxProcesses)); err != nil {
				global.APP_LOG.Warn("设置最大进程数失败", zap.Error(err))
			}
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
			if fallbackErr := l.ensureSSHBeforeFallback(err, "设置实例配置"); fallbackErr != nil {
				return fallbackErr
			}
		}
	}

	// 如果执行规则不允许使用SSH，则返回错误
	if !l.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	// SSH方式设置配置
	cmd := fmt.Sprintf("lxc config set %s %s %s", shellSingleQuote(instanceName), shellSingleQuote(key), shellSingleQuote(value))
	l.mu.RLock()
	client := l.sshClient
	l.mu.RUnlock()
	if client == nil {
		return fmt.Errorf("SSH client不可用，无法设置实例配置")
	}

	// SSH方式设置配置（优先 key=value 新语法，兼容回退到旧语法）
	cmdNew := fmt.Sprintf("lxc config set %s %s=%s", shellSingleQuote(instanceName), shellSingleQuote(key), shellSingleQuote(value))
	_, newErr := client.Execute(cmdNew)
	if newErr == nil {
		global.APP_LOG.Debug("LXD SSH设置实例配置成功",
			zap.String("instance", instanceName),
			zap.String("key", key),
			zap.String("value", value),
			zap.String("syntax", "key=value"))
		return nil
	}

	_, err := client.Execute(cmd)
	if err != nil {
		return fmt.Errorf("SSH设置实例配置失败: new syntax error=%v, legacy syntax error=%w", newErr, err)
	}

	global.APP_LOG.Debug("LXD SSH设置实例配置成功",
		zap.String("instance", instanceName),
		zap.String("key", key),
		zap.String("value", value),
		zap.String("syntax", "legacy"))
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
			if fallbackErr := l.ensureSSHBeforeFallback(err, "设置实例设备配置"); fallbackErr != nil {
				return fallbackErr
			}
		}
	}

	// 如果执行规则不允许使用SSH，则返回错误
	if !l.shouldUseSSH() {
		return fmt.Errorf("执行规则不允许使用SSH")
	}

	l.mu.RLock()
	client := l.sshClient
	l.mu.RUnlock()
	if client == nil {
		return fmt.Errorf("SSH client不可用，无法设置实例设备配置")
	}

	// SSH方式设置设备配置（优先 key=value 新语法，兼容回退到旧语法）
	cmdNew := fmt.Sprintf("lxc config device set %s %s %s=%s", shellSingleQuote(instanceName), shellSingleQuote(deviceName), shellSingleQuote(key), shellSingleQuote(value))
	_, newErr := client.Execute(cmdNew)
	if newErr == nil {
		global.APP_LOG.Debug("LXD SSH设置实例设备配置成功",
			zap.String("instance", instanceName),
			zap.String("device", deviceName),
			zap.String("key", key),
			zap.String("value", value),
			zap.String("syntax", "key=value"))
		return nil
	}

	cmdLegacy := fmt.Sprintf("lxc config device set %s %s %s %s", shellSingleQuote(instanceName), shellSingleQuote(deviceName), shellSingleQuote(key), shellSingleQuote(value))
	_, legacyErr := client.Execute(cmdLegacy)
	if legacyErr == nil {
		global.APP_LOG.Debug("LXD SSH设置实例设备配置成功",
			zap.String("instance", instanceName),
			zap.String("device", deviceName),
			zap.String("key", key),
			zap.String("value", value),
			zap.String("syntax", "legacy"))
		return nil
	}

	// 检查是否为 "Device not found" 错误，如果是则尝试添加设备
	// 两种语法都失败了，使用新语法错误信息（通常更有帮助）
	errMsg := newErr.Error()
	if strings.Contains(strings.ToLower(errMsg), "device not found") ||
		strings.Contains(strings.ToLower(errMsg), "not found") {
		global.APP_LOG.Debug("设备不存在，尝试自动添加设备",
			zap.String("instance", instanceName),
			zap.String("device", deviceName))

		// 尝试添加 root 设备（disk 类型）
		if deviceName == "root" {
			addCmd := fmt.Sprintf("lxc config device add %s root disk path=/ size=1GB", shellSingleQuote(instanceName))
			if _, addErr := client.Execute(addCmd); addErr != nil {
				// 不指定 size 重试
				addCmd2 := fmt.Sprintf("lxc config device add %s root disk path=/", shellSingleQuote(instanceName))
				if _, addErr2 := client.Execute(addCmd2); addErr2 != nil {
					return fmt.Errorf("添加root设备失败: add(size)=%v, add(no size)=%w", addErr, addErr2)
				}
			}

			// 添加成功后重试设置配置
			_, retryErr := client.Execute(cmdNew)
			if retryErr == nil {
				return nil
			}
			_, retryErr = client.Execute(cmdLegacy)
			if retryErr == nil {
				return nil
			}
			return fmt.Errorf("添加root设备后设置配置仍然失败: new syntax=%v, legacy=%w", newErr, legacyErr)
		}

		return fmt.Errorf("SSH设置实例设备配置失败: device '%s' not found and auto-add only supported for 'root': new syntax error=%v, legacy syntax error=%w", deviceName, newErr, legacyErr)
	}

	return fmt.Errorf("SSH设置实例设备配置失败: new syntax error=%v, legacy syntax error=%w", newErr, legacyErr)
}

// waitForInstanceReady 等待实例就绪
func (l *LXDProvider) waitForInstanceReady(ctx context.Context, instanceName string) error {
	global.APP_LOG.Debug("等待LXD实例就绪", zap.String("instance", instanceName))

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
		cmd := fmt.Sprintf("lxc info %s | grep \"Status:\" | awk '{print $2}'", shellSingleQuote(instanceName))
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
			global.APP_LOG.Debug("LXD实例已就绪", zap.String("instance", instanceName))
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
	global.APP_LOG.Debug("开始配置LXD实例系统",
		zap.String("instance", config.Name),
		zap.String("type", config.InstanceType))
	if config.InstanceType != "vm" {
		_ = l.setInstanceConfig(ctx, config.Name, "boot.autostart", "true")
		_ = l.setInstanceConfig(ctx, config.Name, "boot.autostart.priority", "50")
		_ = l.setInstanceConfig(ctx, config.Name, "boot.autostart.delay", "10")
	}
	global.APP_LOG.Debug("实例系统配置完成",
		zap.String("instanceName", config.Name))
	return nil
}

// checkVMSupport 检查LXD是否支持虚拟机（参考官方buildvm.sh的check_vm_support函数）
func (l *LXDProvider) checkVMSupport() error {
	global.APP_LOG.Debug("检查LXD虚拟机支持...")

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

	kvmOutput, kvmErr := l.sshClient.Execute("if [ -e /dev/kvm ] && [ -r /dev/kvm ] && [ -w /dev/kvm ]; then echo kvm; else echo qemu; fi")
	if kvmErr != nil {
		global.APP_LOG.Warn("LXD KVM可用性检测失败，将继续使用LXD/QEMU默认策略",
			zap.Error(kvmErr))
	} else if strings.TrimSpace(kvmOutput) != "kvm" {
		global.APP_LOG.Warn("LXD Provider未检测到可用KVM，将依赖QEMU软件模拟/TCG启动VM",
			zap.String("virtType", "qemu"),
			zap.String("kvmCheck", strings.TrimSpace(kvmOutput)))
	} else {
		global.APP_LOG.Debug("LXD Provider检测到KVM硬件加速可用", zap.String("virtType", "kvm"))
	}

	global.APP_LOG.Debug("已确认LXD支持虚拟机 - qemu驱动可用")
	return nil
}

// configureVMSettings 配置虚拟机特有设置（参考官方buildvm.sh的configure_limits函数）
func (l *LXDProvider) configureVMSettings(ctx context.Context, instanceName string) error {
	global.APP_LOG.Debug("配置虚拟机特有设置", zap.String("instance", instanceName))

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
	global.APP_LOG.Debug("开始配置实例SSH密码",
		zap.String("instanceName", config.Name))

	// 生成随机密码
	password := l.generateRandomPassword()

	// 根据系统类型选择脚本
	var scriptName string
	// 检测系统类型（轻量级命令，直接执行即可）
	output, err := l.sshClient.Execute(fmt.Sprintf("lxc exec %s -- cat /etc/os-release 2>/dev/null | grep ^ID= | cut -d= -f2 | tr -d '\"'", shellSingleQuote(config.Name)))
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
		// 复制脚本到实例（宿主机文件操作，直接执行即可）
		copyCmd := fmt.Sprintf("lxc file push %s %s/root/", shellSingleQuote(scriptPath), shellSingleQuote(config.Name))
		_, err = l.sshClient.Execute(copyCmd)
		if err != nil {
			global.APP_LOG.Warn("复制SSH脚本到实例失败，仅设置密码", zap.Error(err))
		} else {
			// 设置脚本权限并执行 - 使用临时脚本方式以确保 agent 模式下稳定执行
			sshExecScript := utils.BuildTempScript(utils.TempScriptConfig{
				PrimaryCmd: fmt.Sprintf(
					"lxc exec %s -- chmod +x /root/%s && lxc exec %s -- /root/%s %s",
					shellSingleQuote(config.Name), scriptName, shellSingleQuote(config.Name), scriptName, shellSingleQuote(password),
				),
				TimeoutSeconds: 60,
				SuccessMarker:  "PASSWORD_OK",
			})
			_, scriptErr := l.sshClient.ExecuteViaTempScript(sshExecScript, nil, 180*time.Second)
			if scriptErr != nil {
				global.APP_LOG.Warn("执行SSH配置脚本失败，将使用直接设置密码",
					zap.String("instanceName", config.Name),
					zap.String("scriptName", scriptName),
					zap.Error(scriptErr))
			}
			time.Sleep(3 * time.Second)
		}
	}

	// 使用统一的 LXD 密码设置流程：自动恢复 STOPPED/FROZEN 状态，等待 exec 就绪，
	// 并在 sh/bash/历史 stdin 管道方式之间回退，避免初次开设后实例没有可用密码。
	if err = l.setLXDInstancePasswordWithRetry(config.Name, password, "sh"); err != nil {
		global.APP_LOG.Error("设置实例密码失败",
			zap.String("instanceName", config.Name),
			zap.Error(err))
		return fmt.Errorf("设置实例密码失败: %w", err)
	}

	// 清理历史记录 - 非阻塞式，如果失败不影响整体流程
	_, err = l.sshClient.Execute(fmt.Sprintf("lxc exec %s -- bash -c 'history -c 2>/dev/null || true'", shellSingleQuote(config.Name)))
	if err != nil {
		global.APP_LOG.Warn("清理历史记录失败",
			zap.String("instanceName", config.Name),
			zap.Error(err))
	}

	global.APP_LOG.Debug("实例SSH密码设置完成",
		zap.String("instanceName", config.Name))

	// 保存密码到实例配置中（用于后续获取）
	if err = l.setInstanceConfig(ctx, config.Name, "user.password", password); err != nil {
		global.APP_LOG.Warn("保存密码到实例配置失败", zap.Error(err))
	}

	// 更新数据库中的密码记录，确保数据库与实际密码一致
	err = global.APP_DB.Model(&providerModel.Instance{}).
		Where("name = ? AND provider_id = ?", config.Name, l.config.ID).
		Update("password", password).Error
	if err != nil {
		global.APP_LOG.Warn("更新实例密码到数据库失败",
			zap.String("instanceName", config.Name),
			zap.Error(err))
	} else {
		global.APP_LOG.Debug("实例密码已同步到数据库",
			zap.String("instanceName", config.Name))
	}
	return nil
}

// waitForInstanceExecReady 等待实例可以执行命令（容器直接可用，虚拟机需要等待Agent）
func (l *LXDProvider) waitForInstanceExecReady(instanceName string, timeoutSeconds int) error {
	global.APP_LOG.Debug("开始等待实例可执行命令",
		zap.String("instanceName", instanceName),
		zap.Int("timeout", timeoutSeconds))

	// 防御性检查：SSH 客户端可能因连接丢失而为 nil
	if l.sshClient == nil {
		return fmt.Errorf("SSH客户端不可用，无法等待实例就绪: %s", instanceName)
	}

	initialDelay := 12 * time.Second
	if timeoutSeconds <= 30 {
		initialDelay = 3 * time.Second
	} else if timeoutSeconds <= 90 {
		initialDelay = 6 * time.Second
	}
	time.Sleep(initialDelay)
	loopCount := 0
	for elapsed := 0; elapsed < timeoutSeconds; elapsed += 5 {
		// 每两轮循环（10秒）尝试启动实例，避免实例因故障停止导致一直干等待
		if loopCount > 0 && loopCount%2 == 0 {
			startCmd := fmt.Sprintf("lxc start %s", shellSingleQuote(instanceName))
			startOutput, startErr := l.sshClient.Execute(startCmd)
			// "already running" 不是错误，而是实例已在运行的正常状态
			startText := startOutput
			if startErr != nil {
				startText += "\n" + startErr.Error()
			}
			if startErr == nil || lxdAlreadyRunningMessage(startText) {
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
		cmd := fmt.Sprintf("lxc exec %s -- echo 'agent-ready' 2>/dev/null", shellSingleQuote(instanceName))
		output, err := l.sshClient.Execute(cmd)
		if err == nil && strings.Contains(output, "agent-ready") {
			global.APP_LOG.Debug("实例可执行命令",
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

func lxdExecReadyTimeout(instanceType string) int {
	if strings.EqualFold(strings.TrimSpace(instanceType), "vm") {
		return 300
	}
	return 30
}
