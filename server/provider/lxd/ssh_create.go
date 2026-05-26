package lxd

import (
	"context"
	"fmt"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	"oneclickvirt/service/pmacct"
	"oneclickvirt/service/traffic"

	"go.uber.org/zap"
)

func (l *LXDProvider) sshCreateInstance(ctx context.Context, config provider.InstanceConfig) error {
	return l.sshCreateInstanceWithProgress(ctx, config, nil)
}

func (l *LXDProvider) sshCreateInstanceWithProgress(ctx context.Context, config provider.InstanceConfig, progressCallback provider.ProgressCallback) error {
	// 进度更新辅助函数
	updateProgress := func(percentage int, message string) {
		if progressCallback != nil {
			progressCallback(percentage, message)
		}
		global.APP_LOG.Debug("LXD实例创建进度",
			zap.String("instance", config.Name),
			zap.Int("percentage", percentage),
			zap.String("message", message))
	}

	updateProgress(5, "开始创建LXD实例...")

	// 如果是虚拟机，先检查VM支持
	if config.InstanceType == "vm" {
		updateProgress(10, "检查虚拟机支持...")
		if err := l.checkVMSupport(); err != nil {
			return fmt.Errorf("虚拟机支持检查失败: %w", err)
		}
	} else {
		updateProgress(10, "检查实例是否已存在...")
		if exists, err := l.instanceExists(config.Name); err != nil {
			return fmt.Errorf("检查实例是否存在失败: %w", err)
		} else if exists {
			return fmt.Errorf("实例 %s 已存在", config.Name)
		}
	}

	// 在创建之前，处理镜像下载和导入
	updateProgress(15, "处理镜像下载和导入...")
	if config.CopyMode && config.CopySourceName != "" {
		if err := l.validateCopyModeSource(config); err != nil {
			return err
		}
		// 复制模式：跳过镜像下载，直接复制源容器
		updateProgress(30, "复制源容器...")
		copyCmd := fmt.Sprintf("lxc copy %s %s", config.CopySourceName, config.Name)
		global.APP_LOG.Debug("执行LXD容器复制命令", zap.String("command", copyCmd))
		if _, err := l.sshClient.Execute(copyCmd); err != nil {
			return fmt.Errorf("复制容器失败: %w", err)
		}
	} else {
		if err := l.handleImageDownloadAndImport(ctx, &config); err != nil {
			return fmt.Errorf("镜像处理失败: %w", err)
		}
	}

	// 确保SSH脚本可用
	if err := l.ensureSSHScriptsAvailable(l.config.Country); err != nil {
		global.APP_LOG.Warn("确保SSH脚本可用失败，但继续创建实例", zap.Error(err))
	}

	updateProgress(30, "初始化实例...")
	var err error
	if !config.CopyMode {
		// 根据实例类型使用正确的命令格式（参考官方buildvm.sh）
		// 始终应用资源参数，资源限制配置只影响Provider层面的资源预算计算
		var cmd string
		configParams := []string{}

		if config.InstanceType == "vm" {
			// 虚拟机创建命令格式：lxc init image_name vm_name --vm -c limits.cpu=X -c limits.memory=XMiB -d root,size=XGiB
			cmd = fmt.Sprintf("lxc init %s %s --vm", config.Image, config.Name)

			// 资源配置参数
			if config.CPU != "" {
				configParams = append(configParams, fmt.Sprintf("limits.cpu=%s", config.CPU))
			}
			if config.Memory != "" {
				// 转换内存格式为LXD支持的MiB格式
				memoryFormatted := convertMemoryFormat(config.Memory)
				configParams = append(configParams, fmt.Sprintf("limits.memory=%s", memoryFormatted))
			}

			// 虚拟机通用配置
			configParams = append(configParams, "security.secureboot=false")
			configParams = append(configParams, "limits.memory.swap=true")
			// 虚拟机CPU优先级配置
			configParams = append(configParams, "limits.cpu.priority=0")
		} else {
			// 容器创建命令格式
			cmd = fmt.Sprintf("lxc init %s %s", config.Image, config.Name)

			// 基础资源配置
			if config.CPU != "" {
				configParams = append(configParams, fmt.Sprintf("limits.cpu=%s", config.CPU))
			}
			if config.Memory != "" {
				memoryFormatted := convertMemoryFormat(config.Memory)
				configParams = append(configParams, fmt.Sprintf("limits.memory=%s", memoryFormatted))
			}

			// 容器特殊配置选项
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
			// limits.cpu.allowance 与 limits.cpu 互斥，优先使用 allowance
			if config.CPUAllowance != nil && *config.CPUAllowance != "" && *config.CPUAllowance != "100%" {
				// CPU限制格式：20% 或 50%，100%等同于不限制
				configParams = append(configParams, fmt.Sprintf("limits.cpu.allowance=%s", *config.CPUAllowance))
				configParams = append(configParams, "limits.cpu.priority=0")
			} else {
				// 使用标准的CPU核心数限制（已在上面设置）
				configParams = append(configParams, "limits.cpu.priority=0")
				// 设置默认的CPU调度策略（参考官方脚本）
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

			// LXCFS和磁盘IO在init阶段不设置，在实例启动后通过lxc config device命令设置
		}

		// 添加所有配置参数到命令
		for _, param := range configParams {
			cmd += fmt.Sprintf(" -c %s", param)
		}

		// 磁盘配置
		if config.Disk != "" {
			diskFormatted := convertDiskFormat(config.Disk)
			cmd += fmt.Sprintf(" -d root,size=%s", diskFormatted)
		}

		// 创建实例
		global.APP_LOG.Debug("执行LXD实例创建命令", zap.String("command", cmd))
		_, err = l.sshClient.Execute(cmd)
		if err != nil {
			return fmt.Errorf("failed to create instance: %w", err)
		}

		// 如果是虚拟机，需要额外的配置
		if config.InstanceType == "vm" {
			updateProgress(40, "配置虚拟机设置...")
			if err := l.configureVMSettings(ctx, config.Name); err != nil {
				global.APP_LOG.Warn("配置虚拟机设置失败，但继续", zap.Error(err))
			}
		}
	} // end if !config.CopyMode

	updateProgress(45, "配置实例存储...")
	// 配置存储（包括磁盘大小限制和IO限制）
	if err := l.configureInstanceStorage(ctx, config); err != nil {
		global.APP_LOG.Warn("配置实例存储失败，但继续", zap.Error(err))
	}

	// 如果用户指定了自定义IO限制，在存储配置后应用
	if config.InstanceType != "vm" && config.DiskIOLimit != nil && *config.DiskIOLimit != "" {
		// 解析格式："10MB"（带宽）或 "100iops"（IOPS）
		limit := *config.DiskIOLimit
		_, _ = l.sshClient.Execute(fmt.Sprintf("lxc config device set %s root limits.read %s", config.Name, limit))
		_, _ = l.sshClient.Execute(fmt.Sprintf("lxc config device set %s root limits.write %s", config.Name, limit))
		global.APP_LOG.Debug("已应用自定义磁盘IO限制", zap.String("limit", limit))
	}

	updateProgress(50, "配置实例安全设置...")
	// 配置安全设置
	time.Sleep(6 * time.Second)
	if err := l.configureInstanceSecurity(ctx, config); err != nil {
		global.APP_LOG.Warn("配置实例安全设置失败，但继续", zap.Error(err))
	}

	// 配置GPU直通（仅 LXD/Incus 容器，需要在启动前附加设备）
	if config.GpuEnabled {
		if err := l.configureInstanceGPU(ctx, config); err != nil {
			global.APP_LOG.Warn("配置GPU直通失败，但继续", zap.Error(err))
		}
	}

	updateProgress(55, "启动实例...")
	// 启动实例
	_, err = l.sshClient.Execute(fmt.Sprintf("lxc start %s", config.Name))
	if err != nil {
		return fmt.Errorf("failed to start instance: %w", err)
	}

	updateProgress(60, "等待实例就绪...")
	// 等待实例就绪
	if err := l.waitForInstanceReady(ctx, config.Name); err != nil {
		global.APP_LOG.Warn("等待实例就绪失败，但继续", zap.Error(err))
	}

	// 验证实例可以执行命令（容器和虚拟机都需要）
	if config.InstanceType == "vm" {
		updateProgress(63, "等待虚拟机Agent启动...")
		if err := l.waitForInstanceExecReady(config.Name, 120); err != nil {
			global.APP_LOG.Error("等待虚拟机Agent启动超时",
				zap.String("instanceName", config.Name),
				zap.Error(err))
			return fmt.Errorf("虚拟机Agent启动超时，无法继续配置: %w", err)
		} else {
			global.APP_LOG.Debug("虚拟机Agent已启动",
				zap.String("instanceName", config.Name))
		}
	} else {
		updateProgress(63, "等待容器启动...")
		if err := l.waitForInstanceExecReady(config.Name, 120); err != nil {
			global.APP_LOG.Warn("等待容器启动超时",
				zap.String("instanceName", config.Name),
				zap.Error(err))
			// 容器超时只是警告，继续尝试
		} else {
			global.APP_LOG.Debug("容器已启动",
				zap.String("instanceName", config.Name))
		}
	}

	updateProgress(65, "配置实例网络...")
	if err := l.configureInstanceNetworkSettings(ctx, config); err != nil {
		global.APP_LOG.Warn("配置网络失败", zap.Error(err))
	}

	updateProgress(70, "配置实例系统...")
	// 配置实例系统
	if err := l.configureInstanceSystem(ctx, config); err != nil {
		// 系统配置失败不应该阻止实例创建，记录错误即可
		global.APP_LOG.Warn("配置实例系统失败", zap.Error(err))
	}

	updateProgress(75, "等待实例完全启动...")
	if err := l.waitForInstanceExecReady(config.Name, 120); err != nil {
		global.APP_LOG.Warn("等待容器启动超时",
			zap.String("instanceName", config.Name),
			zap.Error(err))
		// 容器超时只是警告，继续尝试
	} else {
		global.APP_LOG.Debug("容器已启动",
			zap.String("instanceName", config.Name))
	}
	// 查找实例ID用于pmacct初始化
	var instanceID uint
	var instance providerModel.Instance
	// 通过provider名称查找provider记录
	var providerRecord providerModel.Provider
	if err := global.APP_DB.Where("name = ?", l.config.Name).First(&providerRecord).Error; err != nil {
		global.APP_LOG.Warn("查找provider记录失败，跳过pmacct初始化",
			zap.String("provider_name", l.config.Name),
			zap.Error(err))
	} else if err := global.APP_DB.Where("name = ? AND provider_id = ?", config.Name, providerRecord.ID).First(&instance).Error; err != nil {
		global.APP_LOG.Warn("查找实例记录失败，跳过pmacct初始化",
			zap.String("instance_name", config.Name),
			zap.Uint("provider_id", providerRecord.ID),
			zap.Error(err))
	} else {
		instanceID = instance.ID

		// 获取并更新实例的PrivateIP（确保pmacct配置使用正确的内网IP）
		updateProgress(78, "获取实例内网IP...")
		if privateIP, err := l.GetInstanceIPv4(ctx, config.Name); err == nil && privateIP != "" {
			// 更新数据库中的PrivateIP
			if err := global.APP_DB.Model(&instance).Update("private_ip", privateIP).Error; err == nil {
				global.APP_LOG.Debug("已更新LXD实例内网IP",
					zap.String("instanceName", config.Name),
					zap.String("privateIP", privateIP))
			}
		} else {
			global.APP_LOG.Warn("获取LXD实例内网IP失败，pmacct可能使用公网IP",
				zap.String("instanceName", config.Name),
				zap.Error(err))
		}

		// 获取并更新实例的网络接口信息（对于容器类型）
		if config.InstanceType != "vm" {
			updateProgress(79, "获取网络接口信息...")

			// 获取IPv4的veth接口
			if vethV4, err := l.GetVethInterfaceName(config.Name); err == nil && vethV4 != "" {
				if err := global.APP_DB.Model(&instance).Update("pmacct_interface_v4", vethV4).Error; err == nil {
					global.APP_LOG.Debug("已更新LXD实例IPv4网络接口",
						zap.String("instanceName", config.Name),
						zap.String("interfaceV4", vethV4))
				}
			} else {
				global.APP_LOG.Debug("未获取到IPv4网络接口",
					zap.String("instanceName", config.Name),
					zap.Error(err))
			}

			// 仅当网络类型包含IPv6时才检测V6的veth接口
			if instance.NetworkType == "nat_ipv4_ipv6" || instance.NetworkType == "dedicated_ipv4_ipv6" || instance.NetworkType == "ipv6_only" {
				if vethV6, err := l.GetVethInterfaceNameV6(config.Name); err == nil && vethV6 != "" {
					if err := global.APP_DB.Model(&instance).Update("pmacct_interface_v6", vethV6).Error; err == nil {
						global.APP_LOG.Debug("已更新LXD实例IPv6网络接口",
							zap.String("instanceName", config.Name),
							zap.String("interfaceV6", vethV6))
					}
				} else {
					global.APP_LOG.Debug("未获取到IPv6网络接口",
						zap.String("instanceName", config.Name),
						zap.Error(err))
				}
			}
		}

		// 检查provider是否启用了流量统计
		if providerRecord.EnableTrafficControl {
			// 初始化流量监控
			updateProgress(80, "初始化流量监控...")
			pmacctService := pmacct.NewService()
			if pmacctErr := pmacctService.InitializePmacctForInstance(instanceID); pmacctErr != nil {
				global.APP_LOG.Warn("LXD实例创建后初始化 pmacct 监控失败",
					zap.Uint("instanceId", instanceID),
					zap.String("instanceName", config.Name),
					zap.Error(pmacctErr))
			} else {
				global.APP_LOG.Debug("LXD实例创建后 pmacct 监控初始化成功",
					zap.Uint("instanceId", instanceID),
					zap.String("instanceName", config.Name))
			}

			// 触发流量数据同步
			updateProgress(85, "同步流量数据...")
			syncTrigger := traffic.NewSyncTriggerService()
			syncTrigger.TriggerInstanceTrafficSync(instanceID, "LXD实例创建后同步")
		} else {
			global.APP_LOG.Debug("Provider未启用流量统计，跳过LXD实例pmacct监控初始化",
				zap.String("providerName", l.config.Name),
				zap.String("instanceName", config.Name))
		}
	}
	// 最后设置SSH密码 - Agent已在启动后等待完成
	updateProgress(95, "配置SSH密码...")
	if err := l.configureInstanceSSHPassword(ctx, config); err != nil {
		// SSH密码设置失败也不应该阻止实例创建，记录错误即可
		global.APP_LOG.Warn("配置SSH密码失败", zap.Error(err))
	}

	updateProgress(100, "LXD实例创建完成")
	global.APP_LOG.Info("LXD实例创建成功", zap.String("name", config.Name))
	return nil
}
