package incus

import (
	"context"
	"encoding/json"
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

func (i *IncusProvider) sshListInstances() ([]provider.Instance, error) {
	// 使用 JSON 格式获取完整的实例信息，包括 IP 地址
	output, err := i.sshClient.Execute("incus list --format json")
	if err != nil {
		return nil, fmt.Errorf("执行 incus list 命令失败: %w", err)
	}

	// 解析 JSON 输出
	var incusInstances []map[string]interface{}
	if err := json.Unmarshal([]byte(output), &incusInstances); err != nil {
		return nil, fmt.Errorf("解析 incus list JSON 输出失败: %w", err)
	}

	var instances []provider.Instance
	for _, inst := range incusInstances {
		name, _ := inst["name"].(string)
		status, _ := inst["status"].(string)
		instanceType, _ := inst["type"].(string)

		instance := provider.Instance{
			ID:     name,
			Name:   name,
			Status: strings.ToLower(status),
			Type:   instanceType,
		}

		// 原有逻辑：遍历所有网络接口提取网络信息
		if state, ok := inst["state"].(map[string]interface{}); ok {
			if network, ok := state["network"].(map[string]interface{}); ok {
				// 遍历网络接口，通常是 eth0, eth1 等
				for ifaceName, ifaceData := range network {
					if ifaceMap, ok := ifaceData.(map[string]interface{}); ok {
						if addresses, ok := ifaceMap["addresses"].([]interface{}); ok {
							for _, addr := range addresses {
								if addrMap, ok := addr.(map[string]interface{}); ok {
									family, _ := addrMap["family"].(string)
									scope, _ := addrMap["scope"].(string)
									address, _ := addrMap["address"].(string)

									// IPv4 地址
									if family == "inet" {
										if scope == "global" || scope == "link" {
											// 内网 IPv4 地址
											if instance.PrivateIP == "" {
												instance.PrivateIP = address
												instance.IP = address // 向后兼容
												global.APP_LOG.Debug("获取到内网IPv4地址",
													zap.String("instance", name),
													zap.String("interface", ifaceName),
													zap.String("ip", address))
											}
										}
									}

									// IPv6 地址
									if family == "inet6" && scope == "global" {
										// 全局 IPv6 地址
										if instance.IPv6Address == "" {
											instance.IPv6Address = address
											global.APP_LOG.Debug("获取到IPv6地址",
												zap.String("instance", name),
												zap.String("interface", ifaceName),
												zap.String("ipv6", address))
										}
									}
								}
							}
						}
					}
				}

				// 补充逻辑1：如果原有逻辑没有获取到内网IPv4，尝试从 eth0 明确获取
				if instance.PrivateIP == "" {
					if eth0, ok := network["eth0"].(map[string]interface{}); ok {
						if addresses, ok := eth0["addresses"].([]interface{}); ok {
							for _, addr := range addresses {
								if addrMap, ok := addr.(map[string]interface{}); ok {
									family, _ := addrMap["family"].(string)
									scope, _ := addrMap["scope"].(string)
									address, _ := addrMap["address"].(string)

									if family == "inet" && scope == "global" {
										instance.PrivateIP = address
										instance.IP = address
										global.APP_LOG.Debug("从eth0补充获取到内网IPv4地址",
											zap.String("instance", name),
											zap.String("ip", address))
										break
									}
								}
							}
						}
					}
				}

				// 补充逻辑2：如果原有逻辑获取到的IPv6是ULA地址，尝试从 eth1 获取公网IPv6
				if instance.IPv6Address != "" && strings.HasPrefix(instance.IPv6Address, "fd") {
					// 当前IPv6是ULA地址，尝试从eth1获取公网IPv6
					if eth1, ok := network["eth1"].(map[string]interface{}); ok {
						if addresses, ok := eth1["addresses"].([]interface{}); ok {
							for _, addr := range addresses {
								if addrMap, ok := addr.(map[string]interface{}); ok {
									family, _ := addrMap["family"].(string)
									scope, _ := addrMap["scope"].(string)
									address, _ := addrMap["address"].(string)

									if family == "inet6" && scope == "global" && !strings.HasPrefix(address, "fd") {
										instance.IPv6Address = address
										global.APP_LOG.Debug("从eth1替换为公网IPv6地址",
											zap.String("instance", name),
											zap.String("ipv6", address))
										break
									}
								}
							}
						}
					}
				} else if instance.IPv6Address == "" {
					// 如果原有逻辑没有获取到任何IPv6，尝试从eth1获取
					if eth1, ok := network["eth1"].(map[string]interface{}); ok {
						if addresses, ok := eth1["addresses"].([]interface{}); ok {
							for _, addr := range addresses {
								if addrMap, ok := addr.(map[string]interface{}); ok {
									family, _ := addrMap["family"].(string)
									scope, _ := addrMap["scope"].(string)
									address, _ := addrMap["address"].(string)

									if family == "inet6" && scope == "global" {
										// 优先使用非ULA地址
										if !strings.HasPrefix(address, "fd") {
											instance.IPv6Address = address
											global.APP_LOG.Debug("从eth1补充获取到公网IPv6地址",
												zap.String("instance", name),
												zap.String("ipv6", address))
											break
										} else if instance.IPv6Address == "" {
											// 如果没有公网IPv6，至少保存ULA地址
											instance.IPv6Address = address
										}
									}
								}
							}
						}
					}
				}
			}
		}

		// 补充逻辑3：如果 state.network 中仍然没有获取到 IPv6，尝试从 devices 配置中获取
		if instance.IPv6Address == "" {
			if devices, ok := inst["devices"].(map[string]interface{}); ok {
				if eth1, ok := devices["eth1"].(map[string]interface{}); ok {
					if ipv6Addr, ok := eth1["ipv6.address"].(string); ok && ipv6Addr != "" {
						instance.IPv6Address = ipv6Addr
						global.APP_LOG.Debug("从devices配置获取到IPv6地址",
							zap.String("instance", name),
							zap.String("ipv6", ipv6Addr))
					}
				}
			}
		}

		instances = append(instances, instance)
	}

	global.APP_LOG.Debug("通过 SSH 成功获取 Incus 实例列表",
		zap.Int("count", len(instances)))
	return instances, nil
}

func (i *IncusProvider) sshCreateInstance(ctx context.Context, config provider.InstanceConfig) error {
	return i.sshCreateInstanceWithProgress(ctx, config, nil)
}

func (i *IncusProvider) sshCreateInstanceWithProgress(ctx context.Context, config provider.InstanceConfig, progressCallback provider.ProgressCallback) error {
	// 获取节点hostname用于日志
	hostname := "unknown"
	if output, err := i.sshClient.Execute("hostname"); err == nil {
		hostname = utils.CleanCommandOutput(output)
	}

	// 预检：确保Incus CLI可用，避免后续命令以 127 失败且错误不明确
	if _, err := i.sshClient.Execute("command -v incus >/dev/null 2>&1"); err != nil {
		return fmt.Errorf("Incus命令不可用，请确认provider节点已安装incus并在PATH中: %w", err)
	}

	global.APP_LOG.Debug("开始在Incus节点上创建实例（使用SSH）",
		zap.String("hostname", hostname),
		zap.String("host", utils.TruncateString(i.config.Host, 32)),
		zap.String("instance_name", config.Name),
		zap.String("instance_type", config.InstanceType))

	// 进度更新辅助函数
	updateProgress := func(percentage int, message string) {
		if progressCallback != nil {
			progressCallback(percentage, message)
		}
		global.APP_LOG.Debug("Incus实例创建进度",
			zap.String("instance", config.Name),
			zap.Int("percentage", percentage),
			zap.String("message", message))
	}

	updateProgress(5, "验证实例配置...")
	if err := i.validateInstanceConfig(config); err != nil {
		return fmt.Errorf("实例配置验证失败: %w", err)
	}

	// 如果是虚拟机，先检查VM支持
	if config.InstanceType == "vm" {
		updateProgress(10, "检查虚拟机支持...")
		if err := i.checkVMSupport(); err != nil {
			return fmt.Errorf("虚拟机支持检查失败: %w", err)
		}
	} else {
		updateProgress(10, "检查实例是否已存在...")
		if exists, err := i.instanceExists(config.Name); err != nil {
			return fmt.Errorf("检查实例是否存在失败: %w", err)
		} else if exists {
			return fmt.Errorf("实例 %s 已存在", config.Name)
		}
	}

	updateProgress(15, "处理镜像下载和导入...")
	if config.CopyMode && config.CopySourceName != "" {
		if err := i.validateCopyModeSource(config); err != nil {
			return err
		}
		// 复制模式：跳过镜像下载，直接复制源容器
		updateProgress(30, "复制源容器...")
		copyCmd := fmt.Sprintf("incus copy %s %s", shellSingleQuote(config.CopySourceName), shellSingleQuote(config.Name))
		global.APP_LOG.Debug("执行Incus容器复制命令", zap.String("command", copyCmd))
		output, err := i.sshClient.Execute(copyCmd)
		if err != nil {
			errMsg := output
			if errMsg == "" {
				errMsg = err.Error()
			}
			global.APP_LOG.Error("Incus容器复制命令失败",
				zap.String("command", copyCmd),
				zap.String("output", utils.TruncateString(output, 2000)),
				zap.Error(err))
			return fmt.Errorf("复制容器失败: %s", errMsg)
		}
	} else {
		if err := i.handleImageDownloadAndImport(ctx, &config, progressCallback); err != nil {
			return fmt.Errorf("镜像处理失败: %w", err)
		}
	}

	// 确保SSH脚本可用
	if err := i.ensureSSHScriptsAvailable(i.config.Country); err != nil {
		global.APP_LOG.Warn("SSH脚本检查失败，但继续创建实例", zap.Error(err))
	}

	updateProgress(30, "准备实例创建命令...")
	var err error
	if !config.CopyMode {
		var cmd string
		cmd, err = i.buildCreateCommand(config)
		if err != nil {
			return fmt.Errorf("构建创建命令失败: %w", err)
		}

		updateProgress(35, "创建Incus实例...")
		if err := i.executeCreateCommand(cmd); err != nil {
			lowerErr := strings.ToLower(err.Error())
			if strings.Contains(lowerErr, "failed to find image") || strings.Contains(lowerErr, "image not found") || strings.Contains(lowerErr, "no such image") {
				fallbackAlias := config.Image
				if strings.ContainsAny(fallbackAlias, ":/") || !strings.HasPrefix(fallbackAlias, "oneclickvirt_") {
					fallbackAlias = i.spiritlhlLocalAlias(config.Image, config.InstanceType)
				}
				if fallbackAlias != "" {
					global.APP_LOG.Warn("Incus创建时镜像不存在，尝试spiritlhl远程源兜底并重试",
						zap.String("image", utils.TruncateString(config.Image, 100)),
						zap.String("fallbackAlias", utils.TruncateString(fallbackAlias, 100)))
					if copyErr := i.copySpiritlhlImageToLocal(config.Image, fallbackAlias, config.InstanceType); copyErr == nil {
						retryCmd := strings.Replace(cmd, shellSingleQuote(config.Image), shellSingleQuote(fallbackAlias), 1)
						if retryErr := i.executeCreateCommand(retryCmd); retryErr == nil {
							global.APP_LOG.Info("Incus使用spiritlhl本地缓存镜像重试创建成功",
								zap.String("instance", config.Name),
								zap.String("alias", utils.TruncateString(fallbackAlias, 100)))
							config.Image = fallbackAlias
						} else {
							global.APP_LOG.Warn("Incus使用spiritlhl本地缓存镜像重试创建仍失败", zap.Error(retryErr))
							if i.shouldCleanupCachedImageOnCreateFailure("", err) {
								i.cleanupCachedImageOnFailure(config.Image, config.InstanceType)
							}
							return fmt.Errorf("执行创建命令失败: %w", err)
						}
					} else {
						global.APP_LOG.Warn("Incus创建时spiritlhl镜像兜底失败", zap.Error(copyErr))
						if i.shouldCleanupCachedImageOnCreateFailure("", err) {
							i.cleanupCachedImageOnFailure(config.Image, config.InstanceType)
						}
						return fmt.Errorf("执行创建命令失败: %w", err)
					}
				} else {
					if i.shouldCleanupCachedImageOnCreateFailure("", err) {
						i.cleanupCachedImageOnFailure(config.Image, config.InstanceType)
					}
					return fmt.Errorf("执行创建命令失败: %w", err)
				}
			} else {
				// 只在明确属于镜像问题时清理缓存。存储池、网络、权限等错误不能删掉健康镜像，
				// 否则会造成后续重复下载/重复导入/别名丢失。
				if i.shouldCleanupCachedImageOnCreateFailure("", err) {
					i.cleanupCachedImageOnFailure(config.Image, config.InstanceType)
				}
				return fmt.Errorf("执行创建命令失败: %w", err)
			}
		}

		// 如果是虚拟机，需要额外的配置
		if config.InstanceType == "vm" {
			updateProgress(40, "配置虚拟机设置...")
			if err := i.configureVMSettings(ctx, config.Name); err != nil {
				global.APP_LOG.Warn("配置虚拟机设置失败，但继续", zap.Error(err))
			}
		}
	} // end if !config.CopyMode

	updateProgress(45, "配置实例安全设置...")
	// 配置安全设置
	if err := i.configureInstanceSecurity(ctx, config); err != nil {
		global.APP_LOG.Warn("配置实例安全设置失败，但继续", zap.Error(err))
	}

	// 配置GPU直通（仅 LXD/Incus 容器，需要在启动前附加设备）
	if config.GpuEnabled {
		if err := i.configureInstanceGPU(ctx, config); err != nil {
			global.APP_LOG.Warn("配置GPU直通失败，但继续", zap.Error(err))
		}
	}

	updateProgress(50, "启动实例...")
	// 启动实例
	time.Sleep(6 * time.Second)
	err = i.sshStartInstance(config.Name)
	if err != nil {
		return fmt.Errorf("启动实例失败: %w", err)
	}

	updateProgress(55, "等待实例就绪...")
	if err := i.waitForInstanceState(config.Name, "RUNNING", 30); err != nil {
		global.APP_LOG.Warn("等待实例就绪超时，但继续配置流程",
			zap.String("instance", config.Name),
			zap.Error(err))
	}

	// 验证实例可以执行命令（容器和虚拟机都需要）
	if config.InstanceType == "vm" {
		waitTimeout := incusExecReadyTimeout(config.InstanceType)
		updateProgress(58, fmt.Sprintf("等待虚拟机Agent启动（最多%d秒）...", waitTimeout))
		if err := i.waitForInstanceExecReady(config.Name, waitTimeout); err != nil {
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
		waitTimeout := incusExecReadyTimeout(config.InstanceType)
		updateProgress(58, fmt.Sprintf("等待容器启动（最多%d秒）...", waitTimeout))
		if err := i.waitForInstanceExecReady(config.Name, waitTimeout); err != nil {
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

	updateProgress(60, "配置实例资源限制...")
	if err := i.configureInstanceLimits(ctx, config); err != nil {
		global.APP_LOG.Warn("配置资源限制失败", zap.Error(err))
	}

	updateProgress(65, "配置实例存储...")
	if err := i.configureInstanceStorage(ctx, config); err != nil {
		global.APP_LOG.Warn("配置存储失败", zap.Error(err))
	}

	updateProgress(70, "配置实例网络...")
	if err := i.configureInstanceNetworkSettings(ctx, config); err != nil {
		global.APP_LOG.Warn("配置网络失败", zap.Error(err))
	}

	updateProgress(75, "配置实例系统...")
	// 配置实例系统
	if err := i.configureInstanceSystem(ctx, config); err != nil {
		// 系统配置失败不应该阻止实例创建，记录错误即可
		global.APP_LOG.Warn("配置实例系统失败", zap.Error(err))
	}

	waitTimeout := incusExecReadyTimeout(config.InstanceType)
	updateProgress(80, fmt.Sprintf("等待实例完全启动（最多%d秒）...", waitTimeout))
	if err := i.waitForInstanceExecReady(config.Name, waitTimeout); err != nil {
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
	if err := global.APP_DB.Where("id = ?", i.config.ID).First(&providerRecord).Error; err != nil {
		global.APP_LOG.Warn("查找provider记录失败，跳过pmacct初始化",
			zap.Uint("provider_id", i.config.ID),
			zap.Error(err))
	} else if err := global.APP_DB.Where("name = ? AND provider_id = ?", config.Name, i.config.ID).First(&instance).Error; err != nil {
		global.APP_LOG.Warn("查找实例记录失败，跳过pmacct初始化",
			zap.String("instance_name", config.Name),
			zap.Uint("provider_id", i.config.ID),
			zap.Error(err))
	} else {
		instanceID = instance.ID

		// 获取并更新实例的PrivateIP（确保pmacct配置使用正确的内网IP）
		updateProgress(83, "获取实例内网IP...")
		ctx2, cancel2 := context.WithTimeout(ctx, 30*time.Second)
		defer cancel2()
		if privateIP, err := i.GetInstanceIPv4(ctx2, config.Name); err == nil && privateIP != "" {
			// 更新数据库中的PrivateIP
			if err := global.APP_DB.Model(&instance).Update("private_ip", privateIP).Error; err == nil {
				global.APP_LOG.Debug("已更新Incus实例内网IP",
					zap.String("instanceName", config.Name),
					zap.String("privateIP", privateIP))
			}
		} else {
			global.APP_LOG.Warn("获取Incus实例内网IP失败，pmacct可能使用公网IP",
				zap.String("instanceName", config.Name),
				zap.Error(err))
		}

		// 获取并更新实例的网络接口信息（对于容器类型）
		if config.InstanceType != "vm" {
			updateProgress(84, "获取网络接口信息...")
			ctx3, cancel3 := context.WithTimeout(ctx, 15*time.Second)
			defer cancel3()

			// 获取IPv4的veth接口
			if vethV4, err := i.GetVethInterfaceName(ctx3, config.Name); err == nil && vethV4 != "" {
				if err := global.APP_DB.Model(&instance).Update("pmacct_interface_v4", vethV4).Error; err == nil {
					global.APP_LOG.Debug("已更新Incus实例IPv4网络接口",
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
				ctx4, cancel4 := context.WithTimeout(ctx, 15*time.Second)
				defer cancel4()
				if vethV6, err := i.GetVethInterfaceNameV6(ctx4, config.Name); err == nil && vethV6 != "" {
					if err := global.APP_DB.Model(&instance).Update("pmacct_interface_v6", vethV6).Error; err == nil {
						global.APP_LOG.Debug("已更新Incus实例IPv6网络接口",
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
			updateProgress(85, "初始化流量监控...")
			pmacctService := pmacct.NewService()
			if pmacctErr := pmacctService.InitializePmacctForInstance(instanceID); pmacctErr != nil {
				global.APP_LOG.Warn("Incus实例创建后初始化 pmacct 监控失败",
					zap.Uint("instanceId", instanceID),
					zap.String("instanceName", config.Name),
					zap.Error(pmacctErr))
			} else {
				global.APP_LOG.Info("Incus实例创建后 pmacct 监控初始化成功",
					zap.Uint("instanceId", instanceID),
					zap.String("instanceName", config.Name))
			}
			// 触发流量数据同步
			updateProgress(90, "同步流量数据...")
			syncTrigger := traffic.NewSyncTriggerService()
			syncTrigger.TriggerInstanceTrafficSync(instanceID, "Incus实例创建后同步")
		} else {
			global.APP_LOG.Debug("Provider未启用流量统计，跳过Incus实例pmacct监控初始化",
				zap.String("providerName", i.config.Name),
				zap.String("instanceName", config.Name))
		}
	}
	// 最后设置SSH密码 - Agent已在启动后等待完成
	updateProgress(98, "配置SSH密码...")
	if err := i.configureInstanceSSHPassword(ctx, config); err != nil {
		// SSH密码设置失败也不应该阻止实例创建，记录错误即可
		global.APP_LOG.Warn("配置SSH密码失败", zap.Error(err))
	}
	updateProgress(100, "Incus实例创建完成")
	instanceTypeText := "容器"
	if config.InstanceType == "vm" {
		instanceTypeText = "虚拟机"
	}
	global.APP_LOG.Info("通过 SSH 成功创建 Incus "+instanceTypeText,
		zap.String("name", config.Name),
		zap.String("type", config.InstanceType))
	return nil
}

func (i *IncusProvider) validateCopyModeSource(config provider.InstanceConfig) error {
	if config.InstanceType != "container" {
		return fmt.Errorf("复制模式仅支持容器实例")
	}
	if !utils.IsValidLXDInstanceName(config.CopySourceName) {
		return fmt.Errorf("源容器名称格式无效: %s", config.CopySourceName)
	}
	output, err := i.sshClient.Execute(fmt.Sprintf("incus list %s --format csv -c n,s,t", shellSingleQuote(config.CopySourceName)))
	if err != nil {
		return fmt.Errorf("检查源容器失败: %w", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		parts := strings.Split(line, ",")
		if len(parts) < 3 || strings.TrimSpace(parts[0]) != config.CopySourceName {
			continue
		}
		status := strings.TrimSpace(parts[1])
		instanceType := strings.TrimSpace(parts[2])
		if !strings.EqualFold(status, "STOPPED") {
			return fmt.Errorf("源容器 %s 必须处于 STOPPED 状态，当前状态: %s", config.CopySourceName, status)
		}
		if !strings.EqualFold(instanceType, "CONTAINER") && !strings.EqualFold(instanceType, "container") {
			return fmt.Errorf("源实例 %s 不是容器类型，当前类型: %s", config.CopySourceName, instanceType)
		}
		return nil
	}
	return fmt.Errorf("源容器 %s 不存在", config.CopySourceName)
}

// configureInstanceLimits 配置实例资源限制
