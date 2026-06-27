package lxd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	"oneclickvirt/service/pmacct"
	"oneclickvirt/service/traffic"
	"oneclickvirt/utils"

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

	// 预检：确保LXD CLI可用，避免后续命令以 127 失败且错误不明确
	if _, err := l.sshClient.Execute("command -v lxc >/dev/null 2>&1"); err != nil {
		return fmt.Errorf("LXD命令不可用，请确认provider节点已安装lxc并在PATH中: %w", err)
	}

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
		copyCmd := fmt.Sprintf("lxc copy %s %s", shellSingleQuote(config.CopySourceName), shellSingleQuote(config.Name))
		global.APP_LOG.Debug("执行LXD容器复制命令", zap.String("command", copyCmd))
		output, err := l.sshClient.Execute(copyCmd)
		if err != nil {
			errMsg := output
			if errMsg == "" {
				errMsg = err.Error()
			}
			global.APP_LOG.Error("LXD容器复制命令失败",
				zap.String("command", copyCmd),
				zap.String("output", utils.TruncateString(output, 2000)),
				zap.Error(err))
			return fmt.Errorf("复制容器失败: %s", errMsg)
		}
	} else {
		if err := l.handleImageDownloadAndImport(ctx, &config, progressCallback); err != nil {
			return fmt.Errorf("镜像处理失败 [%s]: %w", l.formatImageContext(config, ""), err)
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
			cmd = fmt.Sprintf("lxc init %s %s --vm", shellSingleQuote(config.Image), shellSingleQuote(config.Name))

			// 资源配置参数
			if config.CPU != "" {
				configParams = append(configParams, fmt.Sprintf("limits.cpu=%s", config.CPU))
			}
			if config.Memory != "" {
				// 转换内存格式为LXD支持的MiB格式
				memoryFormatted := convertMemoryFormat(config.Memory)
				configParams = append(configParams, fmt.Sprintf("limits.memory=%s", memoryFormatted))
			}

			// 虚拟机附加配置统一在后置配置阶段处理，避免初始化命令因节点能力差异失败
		} else {
			// 容器创建命令格式
			cmd = fmt.Sprintf("lxc init %s %s", shellSingleQuote(config.Image), shellSingleQuote(config.Name))

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

			// 容器特有配置统一在后置配置阶段处理，避免初始化命令因节点能力差异失败
			// LXCFS、CPU/内存swap、nesting、磁盘IO均在实例创建后再设置（带回退）
		}

		// 添加所有配置参数到命令
		for _, param := range configParams {
			cmd += fmt.Sprintf(" -c %s", shellSingleQuote(param))
		}

		// 磁盘配置统一在后置阶段处理（configureInstanceStorage），
		// 避免 "-d root,size=..." 覆盖标志在 profile 缺少 root 设备时失败
		// （部分 LXD 安装的 default profile 不包含 root 设备，或使用非 default 存储池）

		// 创建实例
		global.APP_LOG.Debug("执行LXD实例创建命令", zap.String("command", cmd))
		output, err := l.executeCreateCommand(cmd, config.InstanceType)
		if err != nil {
			createErrText := strings.ToLower(output + "\n" + err.Error())
			if strings.Contains(createErrText, "failed to find image") || strings.Contains(createErrText, "image not found") || strings.Contains(createErrText, "no such image") {
				fallbackAlias := config.Image
				if strings.ContainsAny(fallbackAlias, ":/") || !strings.HasPrefix(fallbackAlias, "oneclickvirt_") {
					fallbackAlias = l.spiritlhlLocalAlias(config.Image, config.InstanceType)
				}
				if fallbackAlias != "" {
					global.APP_LOG.Warn("LXD创建时镜像不存在，尝试spiritlhl远程源兜底并重试",
						zap.String("image", utils.TruncateString(config.Image, 100)),
						zap.String("fallbackAlias", utils.TruncateString(fallbackAlias, 100)))
					if copyErr := l.copySpiritlhlImageToLocal(config.Image, fallbackAlias, config.InstanceType); copyErr == nil {
						retryCmd := strings.Replace(cmd, shellSingleQuote(config.Image), shellSingleQuote(fallbackAlias), 1)
						output2, retryErr := l.executeCreateCommand(retryCmd, config.InstanceType)
						if retryErr == nil {
							global.APP_LOG.Info("LXD使用spiritlhl本地缓存镜像重试创建成功",
								zap.String("instance", config.Name),
								zap.String("alias", utils.TruncateString(fallbackAlias, 100)))
							config.Image = fallbackAlias
							output = output2
							goto lxdCreateSucceeded
						}
						global.APP_LOG.Warn("LXD使用spiritlhl本地缓存镜像重试创建仍失败", zap.String("output", utils.TruncateString(output2, 1000)), zap.Error(retryErr))
					} else {
						global.APP_LOG.Warn("LXD创建时spiritlhl镜像兜底失败", zap.Error(copyErr))
					}
				}
			}

			// 保留完整输出（stdout+stderr）以便排查架构不匹配、镜像损坏等问题
			errMsg := output
			if errMsg == "" {
				errMsg = err.Error()
			}
			global.APP_LOG.Error("LXD实例创建命令失败",
				zap.String("command", cmd),
				zap.String("output", utils.TruncateString(output, 2000)),
				zap.Error(err))

			// 只在明确属于镜像问题时清理缓存。存储池、网络、权限等错误不能删掉健康镜像，
			// 否则会造成后续重复下载/重复导入/别名丢失。
			if l.shouldCleanupCachedImageOnCreateFailure(output, err) {
				l.cleanupCachedImageOnFailure(config.Image, config.InstanceType)
			}

			// 返回包含 LXD 原始错误输出的消息，帮助排查问题（如存储池不存在、镜像别名无效等）
			return fmt.Errorf("failed to create instance [%s]: %s (lxc output: %s)", l.formatImageContext(config, ""), errMsg, utils.TruncateString(output, 500))
		}

	lxdCreateSucceeded:
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
	err = l.sshStartInstance(ctx, config.Name)
	if err != nil {
		return fmt.Errorf("failed to start instance [%s]: %w", l.formatImageContext(config, ""), err)
	}

	updateProgress(60, "等待实例就绪...")
	// 等待实例就绪
	if err := l.waitForInstanceReady(ctx, config.Name); err != nil {
		global.APP_LOG.Warn("等待实例就绪失败，但继续", zap.Error(err))
	}

	// 验证实例可以执行命令（容器和虚拟机都需要）
	if config.InstanceType == "vm" {
		waitTimeout := lxdExecReadyTimeout(config.InstanceType)
		updateProgress(63, fmt.Sprintf("等待虚拟机Agent启动（最多%d秒）...", waitTimeout))
		if err := l.waitForInstanceExecReady(config.Name, waitTimeout); err != nil {
			global.APP_LOG.Error("等待虚拟机Agent启动超时",
				zap.String("instanceName", config.Name),
				zap.Int("timeoutSeconds", waitTimeout),
				zap.Error(err))
			return fmt.Errorf("虚拟机Agent启动超时，无法继续配置: %w", err)
		} else {
			global.APP_LOG.Debug("虚拟机Agent已启动",
				zap.String("instanceName", config.Name))
		}
	} else {
		waitTimeout := lxdExecReadyTimeout(config.InstanceType)
		updateProgress(63, fmt.Sprintf("等待容器启动（最多%d秒）...", waitTimeout))
		if err := l.waitForInstanceExecReady(config.Name, waitTimeout); err != nil {
			global.APP_LOG.Warn("等待容器启动超时",
				zap.String("instanceName", config.Name),
				zap.Int("timeoutSeconds", waitTimeout),
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

	waitTimeout := lxdExecReadyTimeout(config.InstanceType)
	updateProgress(75, fmt.Sprintf("等待实例完全启动（最多%d秒）...", waitTimeout))
	if err := l.waitForInstanceExecReady(config.Name, waitTimeout); err != nil {
		global.APP_LOG.Warn("等待实例完全启动超时，继续后续流程",
			zap.String("instanceName", config.Name),
			zap.String("instanceType", config.InstanceType),
			zap.Int("timeoutSeconds", waitTimeout),
			zap.Error(err))
	} else {
		global.APP_LOG.Debug("实例已启动",
			zap.String("instanceName", config.Name))
	}
	// 查找实例ID用于pmacct初始化
	var instanceID uint
	var instance providerModel.Instance
	var providerRecord providerModel.Provider
	if err := global.APP_DB.Where("id = ?", l.config.ID).First(&providerRecord).Error; err != nil {
		global.APP_LOG.Warn("查找provider记录失败，跳过pmacct初始化",
			zap.Uint("provider_id", l.config.ID),
			zap.Error(err))
	} else if err := global.APP_DB.Where("name = ? AND provider_id = ?", config.Name, l.config.ID).First(&instance).Error; err != nil {
		global.APP_LOG.Warn("查找实例记录失败，跳过pmacct初始化",
			zap.String("instance_name", config.Name),
			zap.Uint("provider_id", l.config.ID),
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

func (l *LXDProvider) executeCreateCommand(cmd, instanceType string) (string, error) {
	timeout := 5 * time.Minute
	if strings.EqualFold(strings.TrimSpace(instanceType), "vm") {
		timeout = 15 * time.Minute
	}
	return l.sshClient.ExecuteWithTimeout(cmd, timeout)
}
