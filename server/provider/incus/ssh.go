package incus

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
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

	global.APP_LOG.Info("通过 SSH 成功获取 Incus 实例列表",
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
		hostname = strings.TrimSpace(output)
	}

	global.APP_LOG.Info("开始在Incus节点上创建实例（使用SSH）",
		zap.String("hostname", hostname),
		zap.String("host", utils.TruncateString(i.config.Host, 32)),
		zap.String("instance_name", config.Name),
		zap.String("instance_type", config.InstanceType))

	// 进度更新辅助函数
	updateProgress := func(percentage int, message string) {
		if progressCallback != nil {
			progressCallback(percentage, message)
		}
		global.APP_LOG.Info("Incus实例创建进度",
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
	if err := i.handleImageDownloadAndImport(ctx, &config); err != nil {
		return fmt.Errorf("镜像处理失败: %w", err)
	}

	// 确保SSH脚本可用
	updateProgress(25, "检查SSH脚本可用性...")
	if err := i.ensureSSHScriptsAvailable(i.config.Country); err != nil {
		global.APP_LOG.Warn("SSH脚本检查失败，但继续创建实例", zap.Error(err))
	}

	updateProgress(30, "准备实例创建命令...")
	cmd, err := i.buildCreateCommand(config)
	if err != nil {
		return fmt.Errorf("构建创建命令失败: %w", err)
	}

	updateProgress(35, "创建Incus实例...")
	if err := i.executeCreateCommand(cmd); err != nil {
		return fmt.Errorf("执行创建命令失败: %w", err)
	}

	// 如果是虚拟机，需要额外的配置
	if config.InstanceType == "vm" {
		updateProgress(40, "配置虚拟机设置...")
		if err := i.configureVMSettings(ctx, config.Name); err != nil {
			global.APP_LOG.Warn("配置虚拟机设置失败，但继续", zap.Error(err))
		}
	}

	updateProgress(45, "配置实例安全设置...")
	// 配置安全设置
	if err := i.configureInstanceSecurity(ctx, config); err != nil {
		global.APP_LOG.Warn("配置实例安全设置失败，但继续", zap.Error(err))
	}

	updateProgress(50, "启动实例...")
	// 启动实例
	_, err = i.sshClient.Execute(fmt.Sprintf("incus start %s", config.Name))
	if err != nil {
		return fmt.Errorf("启动实例失败: %w", err)
	}

	updateProgress(55, "等待实例就绪...")
	if err := i.waitForInstanceState(config.Name, "RUNNING", 30); err != nil {
		global.APP_LOG.Warn("等待实例就绪超时，但继续配置流程",
			zap.String("instance", config.Name),
			zap.Error(err))
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

	updateProgress(80, "等待实例完全启动...")
	// 查找实例ID用于pmacct初始化
	var instanceID uint
	var instance providerModel.Instance
	// 通过provider名称查找provider记录
	var providerRecord providerModel.Provider
	if err := global.APP_DB.Where("name = ?", i.config.Name).First(&providerRecord).Error; err != nil {
		global.APP_LOG.Warn("查找provider记录失败，跳过pmacct初始化",
			zap.String("provider_name", i.config.Name),
			zap.Error(err))
	} else if err := global.APP_DB.Where("name = ? AND provider_id = ?", config.Name, providerRecord.ID).First(&instance).Error; err != nil {
		global.APP_LOG.Warn("查找实例记录失败，跳过pmacct初始化",
			zap.String("instance_name", config.Name),
			zap.Uint("provider_id", providerRecord.ID),
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
				global.APP_LOG.Info("已更新Incus实例内网IP",
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
					global.APP_LOG.Info("已更新Incus实例IPv4网络接口",
						zap.String("instanceName", config.Name),
						zap.String("interfaceV4", vethV4))
				}
			} else {
				global.APP_LOG.Debug("未获取到IPv4网络接口",
					zap.String("instanceName", config.Name),
					zap.Error(err))
			}

			// 获取IPv6的veth接口（尝试检查实例是否有公网IPv6）
			// 先尝试获取公网IPv6地址来判断是否需要获取eth1接口
			ctx4, cancel4 := context.WithTimeout(ctx, 15*time.Second)
			defer cancel4()
			if publicIPv6, err := i.GetInstancePublicIPv6(ctx4, config.Name); err == nil && publicIPv6 != "" {
				// 实例有公网IPv6，获取对应的veth接口
				if vethV6, err := i.GetVethInterfaceNameV6(ctx4, config.Name); err == nil && vethV6 != "" {
					if err := global.APP_DB.Model(&instance).Update("pmacct_interface_v6", vethV6).Error; err == nil {
						global.APP_LOG.Info("已更新Incus实例IPv6网络接口",
							zap.String("instanceName", config.Name),
							zap.String("interfaceV6", vethV6))
					}
				} else {
					global.APP_LOG.Debug("未获取到IPv6网络接口或使用与IPv4相同的接口",
						zap.String("instanceName", config.Name))
				}
			} else {
				global.APP_LOG.Debug("实例没有公网IPv6地址，跳过IPv6网络接口获取",
					zap.String("instanceName", config.Name))
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
	updateProgress(95, "等待Agent启动...")
	if err := i.waitForVMAgentReady(config.Name, 120); err != nil {
		global.APP_LOG.Warn("等待Agent启动超时，尝试直接设置SSH密码",
			zap.String("instanceName", config.Name),
			zap.Error(err))
	} else {
		global.APP_LOG.Info("Agent已启动，可以设置SSH密码",
			zap.String("instanceName", config.Name))
	}
	// 最后设置SSH密码 - 在所有其他配置完成后
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

// configureInstanceLimits 配置实例资源限制
func (i *IncusProvider) configureInstanceLimits(ctx context.Context, config provider.InstanceConfig) error {
	var errors []string

	// 配置CPU优先级
	if config.CPU != "" {
		if err := i.setInstanceConfig(ctx, config.Name, "limits.cpu.priority", "0"); err != nil {
			errors = append(errors, fmt.Sprintf("设置CPU优先级失败: %v", err))
		}
	}

	// 配置内存交换
	if err := i.setInstanceConfig(ctx, config.Name, "limits.memory.swap", "true"); err != nil {
		errors = append(errors, fmt.Sprintf("设置内存交换失败: %v", err))
	}

	// 如果是容器，配置额外的限制
	if config.InstanceType != "vm" {
		// 配置IO限制（移除了limits.read.iops和limits.write.iops，Incus不支持这些选项）
		ioConfigs := map[string]string{
			"limits.read":  "500MB",
			"limits.write": "500MB",
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
		}{
			{"limits.cpu.allowance", "50%"},
			{"limits.cpu.allowance", "25ms/100ms"},
		}

		for _, cpuConfig := range cpuConfigs {
			if err := i.setInstanceConfig(ctx, config.Name, cpuConfig.key, cpuConfig.value); err != nil {
				global.APP_LOG.Debug("CPU限制配置失败，继续执行",
					zap.String("key", cpuConfig.key),
					zap.String("value", cpuConfig.value),
					zap.Error(err))
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
	// 如果是容器，配置IO限制（类似LXD的做法）
	if config.InstanceType != "vm" {
		// 设置读写限制
		if err := i.setInstanceDeviceConfig(ctx, config.Name, "root", "limits.read", "500MB"); err != nil {
			global.APP_LOG.Warn("设置读取限制失败", zap.Error(err))
		}

		if err := i.setInstanceDeviceConfig(ctx, config.Name, "root", "limits.write", "500MB"); err != nil {
			global.APP_LOG.Warn("设置写入限制失败", zap.Error(err))
		}

		// 设置IOPS限制
		if err := i.setInstanceDeviceConfig(ctx, config.Name, "root", "limits.read", "5000iops"); err != nil {
			global.APP_LOG.Warn("设置读取IOPS限制失败", zap.Error(err))
		}

		if err := i.setInstanceDeviceConfig(ctx, config.Name, "root", "limits.write", "5000iops"); err != nil {
			global.APP_LOG.Warn("设置写入IOPS限制失败", zap.Error(err))
		}
	}

	global.APP_LOG.Info("实例存储配置完成",
		zap.String("instance", config.Name),
		zap.String("instanceType", config.InstanceType))

	return nil
}

func (i *IncusProvider) sshStartInstance(id string) error {
	// 先检查实例状态，如果已经在运行则跳过启动
	output, err := i.sshClient.Execute(fmt.Sprintf("incus info %s | grep \"Status:\" | awk '{print $2}'", id))
	if err == nil && strings.TrimSpace(output) == "RUNNING" {
		global.APP_LOG.Info("Incus 实例已在运行，跳过启动", zap.String("id", id))
		return nil
	}

	// 执行启动命令
	_, err = i.sshClient.Execute(fmt.Sprintf("incus start %s", id))
	if err != nil {
		// 如果错误信息提示实例已在运行，则不视为错误
		if strings.Contains(err.Error(), "already running") ||
			strings.Contains(err.Error(), "The instance is already running") {
			global.APP_LOG.Info("Incus 实例已在运行", zap.String("id", id))
			return nil
		}
		return fmt.Errorf("failed to start instance: %w", err)
	}

	global.APP_LOG.Info("已发送启动命令，等待实例启动", zap.String("id", id))

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
		statusOutput, err := i.sshClient.Execute(fmt.Sprintf("incus info %s | grep \"Status:\" | awk '{print $2}'", id))
		if err == nil {
			status := strings.TrimSpace(statusOutput)
			if status == "RUNNING" || status == "Running" {
				// 实例已经启动，再等待额外的时间确保系统完全就绪
				time.Sleep(3 * time.Second)
				global.APP_LOG.Info("Incus实例已成功启动并就绪",
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

func (i *IncusProvider) sshStopInstance(id string) error {
	_, err := i.sshClient.Execute(fmt.Sprintf("incus stop %s", id))
	if err != nil {
		return fmt.Errorf("failed to stop instance: %w", err)
	}

	global.APP_LOG.Info("通过 SSH 成功停止 Incus 实例", zap.String("id", id))
	return nil
}

func (i *IncusProvider) sshRestartInstance(id string) error {
	_, err := i.sshClient.Execute(fmt.Sprintf("incus restart %s", id))
	if err != nil {
		return fmt.Errorf("failed to restart instance: %w", err)
	}
	global.APP_LOG.Info("通过 SSH 成功重启 Incus 实例", zap.String("id", id))
	return nil
}

func (i *IncusProvider) sshDeleteInstance(id string) error {
	// 获取节点hostname用于日志
	hostname := "unknown"
	if output, err := i.sshClient.Execute("hostname"); err == nil {
		hostname = strings.TrimSpace(output)
	}

	global.APP_LOG.Info("开始在Incus节点上删除实例（使用SSH）",
		zap.String("hostname", hostname),
		zap.String("host", utils.TruncateString(i.config.Host, 32)),
		zap.String("instance_id", id))

	output, err := i.sshClient.Execute(fmt.Sprintf("incus delete %s --force", id))
	if err != nil {
		// 检查是否是实例不存在的错误
		if strings.Contains(output, "Instance not found") || strings.Contains(output, "not found") {
			global.APP_LOG.Info("实例已不存在，视为删除成功", zap.String("id", id))
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

	global.APP_LOG.Info("通过 SSH 成功获取 Incus 镜像列表", zap.Int("count", len(images)))
	return images, nil
}

func (i *IncusProvider) sshPullImage(image string) error {
	_, err := i.sshClient.Execute(fmt.Sprintf("incus image copy images:%s local:", image))
	if err != nil {
		return fmt.Errorf("failed to pull image: %w", err)
	}

	global.APP_LOG.Info("通过 SSH 成功拉取 Incus 镜像", zap.String("image", image))
	return nil
}

func (i *IncusProvider) sshDeleteImage(id string) error {
	_, err := i.sshClient.Execute(fmt.Sprintf("incus image delete %s", id))
	if err != nil {
		return fmt.Errorf("failed to delete image: %w", err)
	}

	global.APP_LOG.Info("通过 SSH 成功删除 Incus 镜像", zap.String("id", id))
	return nil
}

// configureInstanceSystem 配置实例系统
func (i *IncusProvider) configureInstanceSystem(ctx context.Context, config provider.InstanceConfig) error {
	global.APP_LOG.Info("开始配置LXD实例系统",
		zap.String("instance", config.Name),
		zap.String("type", config.InstanceType))
	if config.InstanceType != "vm" {
		_ = i.setInstanceConfig(ctx, config.Name, "boot.autostart", "true")
		_ = i.setInstanceConfig(ctx, config.Name, "boot.autostart.priority", "50")
		_ = i.setInstanceConfig(ctx, config.Name, "boot.autostart.delay", "10")
	}
	global.APP_LOG.Info("实例系统配置完成",
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
			global.APP_LOG.Info("回退到SSH执行 - 设置实例配置",
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
			global.APP_LOG.Info("回退到SSH执行 - 设置实例设备配置",
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
			global.APP_LOG.Info("SSH脚本文件不存在或无效",
				zap.String("scriptPath", scriptPath))
			break
		}
	}

	if allExist {
		global.APP_LOG.Info("SSH脚本文件都已存在且有效")
		return nil
	}

	// 下载缺失的脚本
	global.APP_LOG.Info("开始下载SSH脚本文件")

	for _, script := range scripts {
		scriptPath := filepath.Join(scriptsDir, script)

		// 如果脚本已存在且有效，跳过
		if i.isRemoteFileValid(scriptPath) {
			global.APP_LOG.Info("SSH脚本已存在，跳过下载",
				zap.String("script", script))
			continue
		}

		// 构建下载URL - 使用Incus仓库路径
		baseURL := "https://raw.githubusercontent.com/oneclickvirt/incus/main/scripts/" + script
		downloadURL := i.getSSHScriptDownloadURL(baseURL, providerCountry)

		global.APP_LOG.Info("开始下载SSH脚本",
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

		global.APP_LOG.Info("SSH脚本下载并设置完成",
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
				global.APP_LOG.Info("使用CDN下载SSH脚本",
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
