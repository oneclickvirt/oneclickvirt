package lxd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	"oneclickvirt/service/traffic"
	"oneclickvirt/service/vnstat"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

func (l *LXDProvider) sshListInstances(ctx context.Context) ([]provider.Instance, error) {
	// 原有逻辑：使用 CSV 格式获取基本实例信息（兼容性最好）
	output, err := l.sshClient.Execute("lxc list --format csv -c n,s,t")
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	var instances []provider.Instance

	for _, line := range lines {
		if line == "" {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) < 3 {
			continue
		}

		instance := provider.Instance{
			ID:     fields[0],
			Name:   fields[0],
			Status: strings.ToLower(fields[1]),
			Type:   fields[2],
		}
		instances = append(instances, instance)
	}

	// 补充逻辑：尝试通过 JSON 格式获取 IP 地址信息（如果支持的话）
	l.enrichInstancesWithIPAddresses(&instances)

	global.APP_LOG.Info("通过SSH成功获取LXD实例列表", zap.Int("count", len(instances)))
	return instances, nil
}

// enrichInstancesWithIPAddresses 补充获取实例的IP地址信息
func (l *LXDProvider) enrichInstancesWithIPAddresses(instances *[]provider.Instance) {
	// 尝试使用 JSON 格式获取详细信息（包含 IP 地址）
	output, err := l.sshClient.Execute("lxc list --format json")
	if err != nil {
		// JSON 格式不支持，跳过IP地址获取
		global.APP_LOG.Debug("lxc list --format json 不支持，跳过IP地址获取",
			zap.Error(err))
		return
	}

	// 解析 JSON 输出
	var lxdInstances []map[string]interface{}
	if err := json.Unmarshal([]byte(output), &lxdInstances); err != nil {
		global.APP_LOG.Debug("解析 lxc list JSON 输出失败",
			zap.Error(err))
		return
	}

	// 构建实例名称到JSON数据的映射
	instanceMap := make(map[string]map[string]interface{})
	for _, inst := range lxdInstances {
		if name, ok := inst["name"].(string); ok {
			instanceMap[name] = inst
		}
	}

	// 遍历实例列表，补充 IP 地址信息
	for idx := range *instances {
		instance := &(*instances)[idx]
		inst, exists := instanceMap[instance.Name]
		if !exists {
			continue
		}

		// 从 state.network 提取网络信息
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
											if instance.PrivateIP == "" {
												instance.PrivateIP = address
												instance.IP = address
												global.APP_LOG.Debug("获取到内网IPv4地址",
													zap.String("instance", instance.Name),
													zap.String("interface", ifaceName),
													zap.String("ip", address))
											}
										}
									}

									// IPv6 地址
									if family == "inet6" && scope == "global" {
										if instance.IPv6Address == "" {
											instance.IPv6Address = address
											global.APP_LOG.Debug("获取到IPv6地址",
												zap.String("instance", instance.Name),
												zap.String("interface", ifaceName),
												zap.String("ipv6", address))
										}
									}
								}
							}
						}
					}
				}

				// 补充逻辑1：如果没有获取到内网IPv4，尝试从 eth0 明确获取
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
											zap.String("instance", instance.Name),
											zap.String("ip", address))
										break
									}
								}
							}
						}
					}
				}

				// 补充逻辑2：处理IPv6地址，优先使用公网IPv6
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
											zap.String("instance", instance.Name),
											zap.String("ipv6", address))
										break
									}
								}
							}
						}
					}
				} else if instance.IPv6Address == "" {
					// 如果没有获取到任何IPv6，尝试从eth1获取
					if eth1, ok := network["eth1"].(map[string]interface{}); ok {
						if addresses, ok := eth1["addresses"].([]interface{}); ok {
							for _, addr := range addresses {
								if addrMap, ok := addr.(map[string]interface{}); ok {
									family, _ := addrMap["family"].(string)
									scope, _ := addrMap["scope"].(string)
									address, _ := addrMap["address"].(string)

									if family == "inet6" && scope == "global" {
										if !strings.HasPrefix(address, "fd") {
											instance.IPv6Address = address
											global.APP_LOG.Debug("从eth1补充获取到公网IPv6地址",
												zap.String("instance", instance.Name),
												zap.String("ipv6", address))
											break
										} else if instance.IPv6Address == "" {
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
							zap.String("instance", instance.Name),
							zap.String("ipv6", ipv6Addr))
					}
				}
			}
		}
	}
}

func (l *LXDProvider) sshCreateInstance(ctx context.Context, config provider.InstanceConfig) error {
	return l.sshCreateInstanceWithProgress(ctx, config, nil)
}

func (l *LXDProvider) sshCreateInstanceWithProgress(ctx context.Context, config provider.InstanceConfig, progressCallback provider.ProgressCallback) error {
	// 进度更新辅助函数
	updateProgress := func(percentage int, message string) {
		if progressCallback != nil {
			progressCallback(percentage, message)
		}
		global.APP_LOG.Info("LXD实例创建进度",
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
	}

	// 在创建之前，处理镜像下载和导入
	updateProgress(15, "处理镜像下载和导入...")
	if err := l.handleImageDownloadAndImport(ctx, &config); err != nil {
		return fmt.Errorf("镜像处理失败: %w", err)
	}

	// 确保SSH脚本可用
	updateProgress(25, "检查SSH脚本可用性...")
	if err := l.ensureSSHScriptsAvailable(l.config.Country); err != nil {
		global.APP_LOG.Warn("确保SSH脚本可用失败，但继续创建实例", zap.Error(err))
	}

	updateProgress(30, "初始化实例...")
	// 根据实例类型使用正确的命令格式（参考官方buildvm.sh）
	// 注意：始终应用资源参数，资源限制配置只影响Provider层面的资源预算计算
	var cmd string
	if config.InstanceType == "vm" {
		// 虚拟机创建命令格式：lxc init image_name vm_name --vm -c limits.cpu=X -c limits.memory=XMiB -d root,size=XGiB
		cmd = fmt.Sprintf("lxc init %s %s --vm", config.Image, config.Name)

		// 添加资源配置参数
		if config.CPU != "" {
			cmd += fmt.Sprintf(" -c limits.cpu=%s", config.CPU)
		}
		if config.Memory != "" {
			// 转换内存格式为LXD支持的MiB格式
			memoryFormatted := convertMemoryFormat(config.Memory)
			cmd += fmt.Sprintf(" -c limits.memory=%s", memoryFormatted)
		}
		if config.Disk != "" {
			// 转换磁盘格式为LXD支持的GiB格式
			diskFormatted := convertDiskFormat(config.Disk)
			cmd += fmt.Sprintf(" -d root,size=%s", diskFormatted)
		}
	} else {
		// 容器创建命令格式
		cmd = fmt.Sprintf("lxc init %s %s", config.Image, config.Name)

		// 添加资源限制
		if config.CPU != "" || config.Memory != "" {
			limits := []string{}
			if config.CPU != "" {
				limits = append(limits, fmt.Sprintf("limits.cpu=%s", config.CPU))
			}
			if config.Memory != "" {
				memoryFormatted := convertMemoryFormat(config.Memory)
				limits = append(limits, fmt.Sprintf("limits.memory=%s", memoryFormatted))
			}
			if len(limits) > 0 {
				cmd += " -c " + strings.Join(limits, " -c ")
			}
		}

		// 添加磁盘配置
		if config.Disk != "" {
			diskFormatted := convertDiskFormat(config.Disk)
			cmd += fmt.Sprintf(" -d root,size=%s", diskFormatted)
		}
	}

	// 创建实例
	global.APP_LOG.Debug("执行LXD实例创建命令", zap.String("command", cmd))
	_, err := l.sshClient.Execute(cmd)
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

	updateProgress(45, "配置实例存储...")
	// 配置存储（如果需要）
	if err := l.configureInstanceStorage(ctx, config); err != nil {
		global.APP_LOG.Warn("配置实例存储失败，但继续", zap.Error(err))
	}

	updateProgress(50, "配置实例安全设置...")
	// 配置安全设置
	if err := l.configureInstanceSecurity(ctx, config); err != nil {
		global.APP_LOG.Warn("配置实例安全设置失败，但继续", zap.Error(err))
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
	// 查找实例ID用于vnstat初始化
	var instanceID uint
	var instance providerModel.Instance
	// 通过provider名称查找provider记录
	var providerRecord providerModel.Provider
	if err := global.APP_DB.Where("name = ?", l.config.Name).First(&providerRecord).Error; err != nil {
		global.APP_LOG.Warn("查找provider记录失败，跳过vnstat初始化",
			zap.String("provider_name", l.config.Name),
			zap.Error(err))
	} else if err := global.APP_DB.Where("name = ? AND provider_id = ?", config.Name, providerRecord.ID).First(&instance).Error; err != nil {
		global.APP_LOG.Warn("查找实例记录失败，跳过vnstat初始化",
			zap.String("instance_name", config.Name),
			zap.Uint("provider_id", providerRecord.ID),
			zap.Error(err))
	} else {
		instanceID = instance.ID

		// 初始化vnstat监控
		updateProgress(80, "初始化vnstat监控...")
		vnstatService := vnstat.NewService()
		if vnstatErr := vnstatService.InitializeVnStatForInstance(instanceID); vnstatErr != nil {
			global.APP_LOG.Warn("LXD实例创建后初始化vnStat监控失败",
				zap.Uint("instanceId", instanceID),
				zap.String("instanceName", config.Name),
				zap.Error(vnstatErr))
		} else {
			global.APP_LOG.Info("LXD实例创建后vnStat监控初始化成功",
				zap.Uint("instanceId", instanceID),
				zap.String("instanceName", config.Name))
		}

		// 触发流量数据同步
		updateProgress(85, "同步流量数据...")
		syncTrigger := traffic.NewSyncTriggerService()
		syncTrigger.TriggerInstanceTrafficSync(instanceID, "LXD实例创建后同步")
	}
	updateProgress(90, "等待Agent启动...")
	if err := l.waitForVMAgentReady(config.Name, 120); err != nil {
		global.APP_LOG.Warn("等待Agent启动超时，尝试直接设置SSH密码",
			zap.String("instanceName", config.Name),
			zap.Error(err))
	} else {
		global.APP_LOG.Info("Agent已启动，可以设置SSH密码",
			zap.String("instanceName", config.Name))
	}
	// 最后设置SSH密码 - 在所有其他配置完成后
	updateProgress(95, "配置SSH密码...")
	if err := l.configureInstanceSSHPassword(ctx, config); err != nil {
		// SSH密码设置失败也不应该阻止实例创建，记录错误即可
		global.APP_LOG.Warn("配置SSH密码失败", zap.Error(err))
	}

	updateProgress(100, "LXD实例创建完成")
	global.APP_LOG.Info("LXD实例创建成功", zap.String("name", config.Name))
	return nil
}

func (l *LXDProvider) sshStartInstance(ctx context.Context, id string) error {
	_, err := l.sshClient.Execute(fmt.Sprintf("lxc start %s", id))
	if err != nil {
		return fmt.Errorf("failed to start instance: %w", err)
	}

	global.APP_LOG.Info("通过SSH成功启动LXD实例", zap.String("id", utils.TruncateString(id, 50)))
	return nil
}

func (l *LXDProvider) sshStopInstance(ctx context.Context, id string) error {
	_, err := l.sshClient.Execute(fmt.Sprintf("lxc stop %s", id))
	if err != nil {
		return fmt.Errorf("failed to stop instance: %w", err)
	}

	global.APP_LOG.Info("通过SSH成功停止LXD实例", zap.String("id", utils.TruncateString(id, 50)))
	return nil
}

func (l *LXDProvider) sshRestartInstance(ctx context.Context, id string) error {
	_, err := l.sshClient.Execute(fmt.Sprintf("lxc restart %s", id))
	if err != nil {
		return fmt.Errorf("failed to restart instance: %w", err)
	}

	global.APP_LOG.Info("通过SSH成功重启LXD实例", zap.String("id", utils.TruncateString(id, 50)))
	return nil
}

func (l *LXDProvider) sshDeleteInstance(ctx context.Context, id string) error {
	output, err := l.sshClient.Execute(fmt.Sprintf("lxc delete %s --force", id))
	if err != nil {
		// 检查是否是实例不存在的错误
		if strings.Contains(output, "Instance not found") || strings.Contains(output, "not found") {
			global.APP_LOG.Info("实例已不存在，视为删除成功", zap.String("id", utils.TruncateString(id, 50)))
			return nil // 实例不存在，视为删除成功
		}
		return fmt.Errorf("failed to delete instance: %w", err)
	}

	global.APP_LOG.Info("通过SSH成功删除LXD实例", zap.String("id", utils.TruncateString(id, 50)))
	return nil
}

func (l *LXDProvider) sshListImages(ctx context.Context) ([]provider.Image, error) {
	output, err := l.sshClient.Execute("lxc image list --format csv -c l,f,s,u")
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

	global.APP_LOG.Info("通过SSH成功获取LXD镜像列表", zap.Int("count", len(images)))
	return images, nil
}

func (l *LXDProvider) sshPullImage(ctx context.Context, image string) error {
	_, err := l.sshClient.Execute(fmt.Sprintf("lxc image copy images:%s local:", image))
	if err != nil {
		return fmt.Errorf("failed to pull image: %w", err)
	}

	global.APP_LOG.Info("通过SSH成功拉取LXD镜像", zap.String("image", utils.TruncateString(image, 100)))
	return nil
}

func (l *LXDProvider) sshDeleteImage(ctx context.Context, id string) error {
	_, err := l.sshClient.Execute(fmt.Sprintf("lxc image delete %s", id))
	if err != nil {
		return fmt.Errorf("failed to delete image: %w", err)
	}

	global.APP_LOG.Info("通过SSH成功删除LXD镜像", zap.String("id", utils.TruncateString(id, 50)))
	return nil
}

// sshSetInstancePassword 通过SSH设置实例密码
func (l *LXDProvider) sshSetInstancePassword(ctx context.Context, instanceID, password string) error {
	// 首先尝试简单的状态检查命令
	simpleCheckCmd := fmt.Sprintf("lxc list | grep %s", instanceID)
	output, err := l.sshClient.Execute(simpleCheckCmd)
	if err != nil {
		global.APP_LOG.Error("检查LXD实例状态失败",
			zap.String("instanceID", instanceID),
			zap.Error(err))
		return fmt.Errorf("检查实例状态失败: %w", err)
	}

	// 检查实例是否存在且运行
	if !strings.Contains(output, instanceID) {
		return fmt.Errorf("实例 %s 不存在", instanceID)
	}

	if !strings.Contains(output, "RUNNING") {
		return fmt.Errorf("实例 %s 未运行，无法设置密码", instanceID)
	}

	// 设置密码 - 使用lxc exec命令，增加错误处理
	setPasswordCmd := fmt.Sprintf("lxc exec %s -- bash -c 'echo \"root:%s\" | chpasswd'", instanceID, password)
	_, err = l.sshClient.Execute(setPasswordCmd)
	if err != nil {
		global.APP_LOG.Error("设置LXD实例密码失败",
			zap.String("instanceID", instanceID),
			zap.Error(err))
		return fmt.Errorf("设置实例密码失败: %w", err)
	}

	global.APP_LOG.Info("LXD实例密码设置成功(SSH)",
		zap.String("instanceID", utils.TruncateString(instanceID, 12)))

	return nil
}

// configureInstanceNetworkSettings 配置实例网络设置
func (l *LXDProvider) configureInstanceNetworkSettings(ctx context.Context, config provider.InstanceConfig) error {
	// 解析网络配置
	networkConfig := l.parseNetworkConfigFromInstanceConfig(config)

	// 配置网络
	if err := l.configureInstanceNetwork(ctx, config, networkConfig); err != nil {
		return fmt.Errorf("配置实例网络失败: %w", err)
	}

	return nil
}
