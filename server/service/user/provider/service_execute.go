package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/constant"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	systemModel "oneclickvirt/model/system"
	userModel "oneclickvirt/model/user"
	"oneclickvirt/provider"
	providerService "oneclickvirt/service/provider"
	"oneclickvirt/service/resources"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// executeProviderCreation 阶段2: Provider创建实例 (30% -> 60%)，根据ExecutionRule自动选择API或SSH
func (s *Service) executeProviderCreation(ctx context.Context, task *adminModel.Task, instance *providerModel.Instance) error {
	global.APP_LOG.Debug("开始Provider创建实例阶段", zap.Uint("taskId", task.ID))

	// 检查上下文状态
	if ctx.Err() != nil {
		global.APP_LOG.Warn("Provider创建实例开始时上下文已取消", zap.Uint("taskId", task.ID), zap.Error(ctx.Err()))
		return ctx.Err()
	}

	// 解析任务数据获取创建实例所需的参数
	var taskReq adminModel.CreateInstanceTaskRequest

	if err := json.Unmarshal([]byte(task.TaskData), &taskReq); err != nil {
		err := fmt.Errorf("解析任务数据失败: %v", err)
		global.APP_LOG.Error("解析任务数据失败", zap.Uint("taskId", task.ID), zap.Error(err))
		return err
	}
	var redemptionTaskReq adminModel.CreateRedemptionInstanceTaskRequest
	redemptionTaskJSONErr := json.Unmarshal([]byte(task.TaskData), &redemptionTaskReq)
	isCopyMode := redemptionTaskJSONErr == nil && redemptionTaskReq.CreationMode == "copy" && redemptionTaskReq.SourceContainer != ""

	// 直接从数据库获取Provider配置（使用ProviderID而不是Name）
	// 可用性口径：标准节点看 active/partial，agent 节点仅看在线状态
	var dbProvider providerModel.Provider
	if err := global.APP_DB.Where("id = ?", instance.ProviderID).First(&dbProvider).Error; err != nil {
		err := fmt.Errorf("Provider ID %d 不存在或不可用", instance.ProviderID)
		global.APP_LOG.Error("Provider不存在", zap.Uint("taskId", task.ID), zap.Uint("providerId", instance.ProviderID), zap.Error(err))
		return err
	}
	providerAvailable := (dbProvider.ConnectionType == "agent" && dbProvider.AgentStatus == "online") ||
		(dbProvider.ConnectionType != "agent" && (dbProvider.Status == "active" || dbProvider.Status == "partial"))
	if !providerAvailable {
		err := fmt.Errorf("Provider ID %d 不存在或不可用", instance.ProviderID)
		global.APP_LOG.Error("Provider不可用", zap.Uint("taskId", task.ID), zap.Uint("providerId", instance.ProviderID), zap.Error(err))
		return err
	}

	// 复制副本避免共享状态，立即创建Provider字段的本地副本
	localProviderID := dbProvider.ID
	localProviderName := dbProvider.Name
	localProviderType := dbProvider.Type
	localProviderIsFrozen := dbProvider.IsFrozen
	localProviderExpiresAt := dbProvider.ExpiresAt
	localProviderIPv4PortMappingMethod := dbProvider.IPv4PortMappingMethod
	localProviderIPv6PortMappingMethod := dbProvider.IPv6PortMappingMethod
	localProviderNetworkType := dbProvider.NetworkType

	// 检查Provider是否过期或冻结
	if localProviderIsFrozen {
		err := fmt.Errorf("Provider ID %d 已被冻结", localProviderID)
		global.APP_LOG.Error("Provider已冻结", zap.Uint("taskId", task.ID), zap.Uint("providerId", localProviderID))
		return err
	}

	if localProviderExpiresAt != nil && localProviderExpiresAt.Before(time.Now()) {
		err := fmt.Errorf("Provider ID %d 已过期", localProviderID)
		global.APP_LOG.Error("Provider已过期", zap.Uint("taskId", task.ID), zap.Uint("providerId", localProviderID), zap.Time("expiresAt", *localProviderExpiresAt))
		return err
	}

	// 实现实际的Provider操作逻辑（根据ExecutionRule使用API或SSH）
	// 首先尝试从ProviderService获取已连接的Provider实例（使用ID）
	providerSvc := providerService.GetProviderService()
	providerInstance, exists := providerSvc.GetProviderByID(instance.ProviderID)

	if !exists || !providerInstance.IsConnected() {
		// 缓存中不存在或连接已失效时，强制重载，避免命中 stale provider 导致 "not connected"
		global.APP_LOG.Debug("Provider未连接或连接失效，尝试重载",
			zap.Uint("providerId", localProviderID),
			zap.String("name", localProviderName),
			zap.Bool("exists", exists))

		if err := providerSvc.ReloadProvider(localProviderID); err != nil {
			global.APP_LOG.Warn("Provider重载失败，回退到动态加载",
				zap.Uint("providerId", localProviderID),
				zap.String("name", localProviderName),
				zap.Error(err))
			if loadErr := providerSvc.LoadProvider(dbProvider); loadErr != nil {
				global.APP_LOG.Error("动态加载Provider失败",
					zap.Uint("providerId", localProviderID),
					zap.String("name", localProviderName),
					zap.Error(loadErr))
				return fmt.Errorf("Provider ID %d 连接失败: %v", localProviderID, loadErr)
			}
		}

		// 重新获取Provider实例并确认连接状态
		providerInstance, exists = providerSvc.GetProviderByID(instance.ProviderID)
		if dbProvider.ConnectionType == "agent" && (!exists || !providerInstance.IsConnected()) {
			// Agent 连接可能在重载后短时间内重连，等待 Agent 重新建立 WebSocket 连接。
			// 与 AgentShellExecutor.getConn() 的 60 秒超时保持一致。
			agentWaitDeadline := time.Now().Add(60 * time.Second)
			for i := 0; ; i++ {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				providerInstance, exists = providerSvc.GetProviderByID(instance.ProviderID)
				if exists && providerInstance.IsConnected() {
					global.APP_LOG.Debug("Agent 连接已恢复",
						zap.Uint("providerId", localProviderID),
						zap.Int("waitIterations", i+1))
					break
				}
				if time.Now().After(agentWaitDeadline) {
					break
				}
				// 前 10 秒每 500ms 检查一次，之后每 2 秒检查一次
				delay := 500 * time.Millisecond
				if i >= 20 {
					delay = 2 * time.Second
				}
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
		if !exists || !providerInstance.IsConnected() {
			err := fmt.Errorf("Provider ID %d 连接后仍然不可用", localProviderID)
			global.APP_LOG.Error("Provider连接后仍然不可用",
				zap.Uint("taskId", task.ID),
				zap.Uint("providerId", localProviderID),
				zap.Bool("exists", exists))
			return err
		}
	}

	imageName := ""
	imageURL := ""
	useCDN := false
	cpuValue := ""
	memoryValue := ""
	diskValue := ""
	bandwidthSpeedMbps := 0
	userLevel := 0

	if isCopyMode {
		if localProviderType != "lxd" && localProviderType != "incus" {
			return fmt.Errorf("复制模式仅支持 LXD/Incus 类型的节点")
		}
		if instance.InstanceType != "container" {
			return fmt.Errorf("复制模式仅支持容器实例")
		}
		if !utils.IsValidLXDInstanceName(redemptionTaskReq.SourceContainer) {
			return fmt.Errorf("源容器名称格式无效")
		}
		imageName = "copy:" + redemptionTaskReq.SourceContainer
		copyCPU, copyMemory, copyDisk, err := s.detectCopySourceResources(ctx, providerInstance, localProviderType, redemptionTaskReq.SourceContainer)
		if err != nil {
			return fmt.Errorf("复制模式获取源容器资源失败: %w", err)
		}
		if err := validateCopyResourceIsolation(dbProvider, copyCPU, copyMemory, copyDisk); err != nil {
			return err
		}
		resourceService := &resources.ResourceService{}
		if err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
			if err := resourceService.AllocateResourcesInTx(tx, localProviderID, "container", copyCPU, copyMemory, copyDisk); err != nil {
				return err
			}
			return tx.Model(instance).Updates(map[string]interface{}{
				"cpu":    copyCPU,
				"memory": copyMemory,
				"disk":   copyDisk,
			}).Error
		}); err != nil {
			return fmt.Errorf("复制模式分配节点资源失败: %w", err)
		}
		instance.CPU = copyCPU
		instance.Memory = copyMemory
		instance.Disk = copyDisk
	} else {
		// 获取镜像名称
		var systemImage systemModel.SystemImage
		if err := global.APP_DB.Where("id = ?", taskReq.ImageId).First(&systemImage).Error; err != nil {
			err := fmt.Errorf("获取镜像信息失败: %v", err)
			global.APP_LOG.Error("获取镜像信息失败", zap.Uint("taskId", task.ID), zap.Uint("imageId", taskReq.ImageId), zap.Error(err))
			return err
		}

		// 将规格ID转换为实际数值
		cpuSpec, err := constant.GetCPUSpecByID(taskReq.CPUId)
		if err != nil {
			err := fmt.Errorf("获取CPU规格失败: %v", err)
			global.APP_LOG.Error("获取CPU规格失败", zap.Uint("taskId", task.ID), zap.String("cpuId", taskReq.CPUId), zap.Error(err))
			return err
		}

		memorySpec, err := constant.GetMemorySpecByID(taskReq.MemoryId)
		if err != nil {
			err := fmt.Errorf("获取内存规格失败: %v", err)
			global.APP_LOG.Error("获取内存规格失败", zap.Uint("taskId", task.ID), zap.String("memoryId", taskReq.MemoryId), zap.Error(err))
			return err
		}

		diskSpec, err := constant.GetDiskSpecByID(taskReq.DiskId)
		if err != nil {
			err := fmt.Errorf("获取磁盘规格失败: %v", err)
			global.APP_LOG.Error("获取磁盘规格失败", zap.Uint("taskId", task.ID), zap.String("diskId", taskReq.DiskId), zap.Error(err))
			return err
		}

		bandwidthSpec, err := constant.GetBandwidthSpecByID(taskReq.BandwidthId)
		if err != nil {
			err := fmt.Errorf("获取带宽规格失败: %v", err)
			global.APP_LOG.Error("获取带宽规格失败", zap.Uint("taskId", task.ID), zap.String("bandwidthId", taskReq.BandwidthId), zap.Error(err))
			return err
		}

		// 获取用户等级信息，用于带宽限制配置
		var user userModel.User
		if err := global.APP_DB.First(&user, task.UserID).Error; err != nil {
			err := fmt.Errorf("获取用户信息失败: %v", err)
			global.APP_LOG.Error("获取用户信息失败", zap.Uint("taskId", task.ID), zap.Uint("userID", task.UserID), zap.Error(err))
			return err
		}

		imageName = systemImage.Name
		imageURL = systemImage.URL
		useCDN = systemImage.UseCDN
		cpuValue = fmt.Sprintf("%d", cpuSpec.Cores)
		memoryValue = fmt.Sprintf("%dm", memorySpec.SizeMB)
		diskValue = fmt.Sprintf("%dm", diskSpec.SizeMB)
		bandwidthSpeedMbps = bandwidthSpec.SpeedMbps
		userLevel = user.Level

		global.APP_LOG.Debug("规格ID转换为实际数值",
			zap.Uint("taskId", task.ID),
			zap.String("cpuId", taskReq.CPUId), zap.Int("cpuCores", cpuSpec.Cores),
			zap.String("memoryId", taskReq.MemoryId), zap.Int("memorySizeMB", memorySpec.SizeMB),
			zap.String("diskId", taskReq.DiskId), zap.Int("diskSizeMB", diskSpec.SizeMB),
			zap.String("bandwidthId", taskReq.BandwidthId), zap.Int("bandwidthSpeedMbps", bandwidthSpec.SpeedMbps),
			zap.Int("userLevel", user.Level))
	}

	// 构建实例配置，使用实际数值而非ID
	instanceConfig := provider.InstanceConfig{
		Name:         instance.Name,
		Image:        imageName,
		CPU:          cpuValue,
		Memory:       memoryValue,
		Disk:         diskValue,
		InstanceType: instance.InstanceType,
		ImageURL:     imageURL, // 镜像URL用于下载
		UseCDN:       useCDN,   // 传递CDN加速配置（仅GitHub链接启用）
		Metadata: map[string]string{
			"user_level":               fmt.Sprintf("%d", userLevel),          // 用户等级，用于带宽限制配置
			"bandwidth_spec":           fmt.Sprintf("%d", bandwidthSpeedMbps), // 用户选择的带宽规格
			"ipv4_port_mapping_method": localProviderIPv4PortMappingMethod,    // IPv4端口映射方式（从Provider配置获取）
			"ipv6_port_mapping_method": localProviderIPv6PortMappingMethod,    // IPv6端口映射方式（从Provider配置获取）
			"network_type":             localProviderNetworkType,              // 网络配置类型：nat_ipv4, nat_ipv4_ipv6, dedicated_ipv4, dedicated_ipv4_ipv6, ipv6_only
			"instance_id":              fmt.Sprintf("%d", instance.ID),        // 实例ID，用于端口分配
			"provider_id":              fmt.Sprintf("%d", localProviderID),    // Provider ID，用于端口区间分配
		},
		// 容器特殊配置选项（从Provider继承，仅用于LXD/Incus容器）
		Privileged:   boolPtr(dbProvider.ContainerPrivileged),
		AllowNesting: boolPtr(dbProvider.ContainerAllowNesting),
		EnableLXCFS:  boolPtr(dbProvider.ContainerEnableLXCFS),
		CPUAllowance: stringPtr(dbProvider.ContainerCPUAllowance),
		MemorySwap:   boolPtr(dbProvider.ContainerMemorySwap),
		MaxProcesses: intPtr(dbProvider.ContainerMaxProcesses),
		DiskIOLimit:  stringPtr(dbProvider.ContainerDiskIOLimit),
	}

	// GPU 直通仅支持 LXD/Incus 的容器实例，其他 Provider 类型不支持
	isLxdIncusProvider := localProviderType == "lxd" || localProviderType == "incus"
	if isLxdIncusProvider && instance.InstanceType != "vm" {
		// 优先使用任务请求中的GPU配置（用户主动选择），没有则回退到Provider默认配置
		instanceConfig.GpuEnabled = dbProvider.GpuEnabled
		instanceConfig.GpuDeviceIds = dbProvider.GpuDeviceIds
	}

	// 复制模式处理（仅 LXD/Incus，通过 CreateRedemptionInstanceTaskRequest 传入）
	if isCopyMode {
		instanceConfig.CopyMode = true
		instanceConfig.CopySourceName = redemptionTaskReq.SourceContainer
		// 复制模式下，GPU配置也从任务请求中获取（覆盖Provider默认值）
		if isLxdIncusProvider {
			instanceConfig.GpuEnabled = redemptionTaskReq.GpuEnabled
			instanceConfig.GpuDeviceIds = redemptionTaskReq.GpuDeviceIds
		}
	} else if redemptionTaskJSONErr == nil && redemptionTaskReq.GpuEnabled {
		// 标准模式下，如果兑换码任务请求中指定了GPU配置，覆盖Provider默认值
		if isLxdIncusProvider && instance.InstanceType != "vm" {
			instanceConfig.GpuEnabled = redemptionTaskReq.GpuEnabled
			instanceConfig.GpuDeviceIds = redemptionTaskReq.GpuDeviceIds
		}
	} else if taskReq.GpuEnabled {
		// 标准模式下（普通用户创建），如果任务请求中指定了GPU配置，覆盖Provider默认值
		if isLxdIncusProvider && instance.InstanceType != "vm" {
			instanceConfig.GpuEnabled = taskReq.GpuEnabled
			instanceConfig.GpuDeviceIds = taskReq.GpuDeviceIds
		}
	}

	// 预分配端口映射（所有Provider类型都需要）
	portMappingService := &resources.PortMappingService{}

	// 对于 dedicated_ipv4/dedicated_ipv4_ipv6 类型，尝试从IP池分配地址
	// 如果池中有可用地址，则预设给实例并通过metadata传递给创建逻辑
	if localProviderNetworkType == "dedicated_ipv4" || localProviderNetworkType == "dedicated_ipv4_ipv6" {
		var allocatedIP string
		allocErr := global.APP_DB.Transaction(func(tx *gorm.DB) error {
			var entry struct {
				ID      uint
				Address string
			}
			rawSQL := `SELECT id, address FROM provider_ipv4_pools
			           WHERE provider_id = ? AND is_allocated = 0 AND deleted_at IS NULL
			           ORDER BY id ASC LIMIT 1 FOR UPDATE`
			if err := tx.Raw(rawSQL, localProviderID).Scan(&entry).Error; err != nil {
				return fmt.Errorf("查询可用IPv4地址失败: %w", err)
			}
			if entry.ID == 0 {
				return fmt.Errorf("地址池已耗尽，没有可用的IPv4地址")
			}
			if err := tx.Exec(
				`UPDATE provider_ipv4_pools SET is_allocated = 1, instance_id = ?, updated_at = NOW() WHERE id = ? AND is_allocated = 0`,
				instance.ID, entry.ID,
			).Error; err != nil {
				return fmt.Errorf("分配IPv4地址失败: %w", err)
			}
			allocatedIP = entry.Address
			return nil
		})
		if allocErr == nil && allocatedIP != "" {
			instanceConfig.Metadata["static_ipv4"] = allocatedIP
			// 预先写入公网IP（方便未启动实例时展示，finalize阶段会校验更新）
			if dbErr := global.APP_DB.Model(instance).Update("public_ip", allocatedIP).Error; dbErr != nil {
				global.APP_LOG.Warn("预设实例public_ip失败",
					zap.Uint("taskId", task.ID),
					zap.Uint("instanceId", instance.ID),
					zap.Error(dbErr))
			}
			global.APP_LOG.Debug("从 IPv4 池分配地址成功",
				zap.Uint("taskId", task.ID),
				zap.Uint("instanceId", instance.ID),
				zap.String("allocatedIP", allocatedIP))
		} else if allocErr != nil {
			// 池未配置或已耗尽：记录警告但不阻止实例创建（网络侧 DHCP 仍可工作）
			global.APP_LOG.Warn("未能从 IPv4 池分配地址（池未配置或已耗尽），继续创建",
				zap.Uint("taskId", task.ID),
				zap.Uint("instanceId", instance.ID),
				zap.Error(allocErr))
		}
	}

	// 预先创建端口映射记录，用于统一的端口管理
	if err := portMappingService.CreateDefaultPortMappings(instance.ID, localProviderID); err != nil {
		global.APP_LOG.Warn("预分配端口映射失败",
			zap.Uint("taskId", task.ID),
			zap.Uint("instanceId", instance.ID),
			zap.Error(err))
		// 对于容器类Provider（docker/podman/containerd），端口映射通过 -p 标志在容器创建时建立，
		// 预分配失败意味着容器将无任何端口映射，继续创建会产生无法访问的僵尸实例，必须立即终止任务。
		if localProviderType == "docker" || localProviderType == "podman" || localProviderType == "containerd" {
			return fmt.Errorf("容器类Provider端口映射预分配失败（podman/containerd/docker 的端口映射在容器创建时绑定，无法事后追加），无法继续创建实例: %v", err)
		}
	} else {
		// 获取已分配的端口映射
		portMappings, err := portMappingService.GetInstancePortMappings(instance.ID)
		if err != nil {
			global.APP_LOG.Warn("获取端口映射失败",
				zap.Uint("taskId", task.ID),
				zap.Uint("instanceId", instance.ID),
				zap.Error(err))
		} else {
			// 对于容器类Provider（docker/podman/containerd），将端口映射信息写入实例配置，
			// 作为 -p 参数传给容器运行时。LXD/Incus/Proxmox 通过其他机制管理端口，无需此步骤。
			if localProviderType == "docker" || localProviderType == "podman" || localProviderType == "containerd" {
				// 将端口映射信息添加到实例配置中
				var ports []string
				for _, port := range portMappings {
					// 格式: "0.0.0.0:公网端口:容器端口/协议"
					// 如果协议是 both，需要创建两个端口映射（tcp 和 udp）
					if port.Protocol == "both" {
						tcpMapping := fmt.Sprintf("0.0.0.0:%d:%d/tcp", port.HostPort, port.GuestPort)
						udpMapping := fmt.Sprintf("0.0.0.0:%d:%d/udp", port.HostPort, port.GuestPort)
						ports = append(ports, tcpMapping, udpMapping)
					} else {
						portMapping := fmt.Sprintf("0.0.0.0:%d:%d/%s", port.HostPort, port.GuestPort, port.Protocol)
						ports = append(ports, portMapping)
					}
				}
				instanceConfig.Ports = ports

				global.APP_LOG.Debug("容器端口映射预分配成功",
					zap.Uint("taskId", task.ID),
					zap.Uint("instanceId", instance.ID),
					zap.String("providerType", localProviderType),
					zap.Int("portCount", len(ports)),
					zap.Strings("ports", ports))
			} else if localProviderType == "qemu" || localProviderType == "kubevirt" {
				// QEMU/KubeVirt通过shell脚本的位置参数传递端口：[sshPort, startPort, endPort]
				var sshPort, startPort, endPort int
				for _, port := range portMappings {
					if port.IsSSH {
						sshPort = port.HostPort
					} else {
						if startPort == 0 || port.HostPort < startPort {
							startPort = port.HostPort
						}
						if port.HostPort > endPort {
							endPort = port.HostPort
						}
					}
				}
				if startPort == 0 {
					startPort = sshPort
				}
				if endPort == 0 {
					endPort = startPort
				}
				instanceConfig.Ports = []string{
					fmt.Sprintf("%d", sshPort),
					fmt.Sprintf("%d", startPort),
					fmt.Sprintf("%d", endPort),
				}

				global.APP_LOG.Debug("QEMU/KubeVirt端口映射预分配成功",
					zap.Uint("taskId", task.ID),
					zap.Uint("instanceId", instance.ID),
					zap.String("providerType", localProviderType),
					zap.Int("sshPort", sshPort),
					zap.Int("startPort", startPort),
					zap.Int("endPort", endPort))
			} else {
				// 对于LXD等其他Provider，端口映射信息已保存在数据库中，将在实例创建时读取
				global.APP_LOG.Debug("端口映射预分配成功",
					zap.Uint("taskId", task.ID),
					zap.Uint("instanceId", instance.ID),
					zap.String("providerType", localProviderType),
					zap.Int("portCount", len(portMappings)))
			}
		}
	}

	// 调用Provider创建实例（API或SSH，取决于Provider的ExecutionRule配置）
	// 创建进度回调函数，与任务系统集成
	progressCallback := func(percentage int, message string) {
		// 将Provider内部进度（0-100）映射到任务进度（30-70）
		// Provider进度占用40%的总进度空间
		adjustedPercentage := 30 + (percentage * 40 / 100)
		s.updateTaskProgress(task.ID, adjustedPercentage, message)
	}

	global.APP_LOG.Debug("准备调用Provider创建实例方法",
		zap.Uint("taskId", task.ID),
		zap.String("instanceName", instance.Name),
		zap.String("providerName", localProviderName),
		zap.String("providerType", localProviderType))

	// 使用带进度的创建方法
	global.APP_LOG.Debug("开始调用CreateInstanceWithProgress",
		zap.Uint("taskId", task.ID),
		zap.String("instanceName", instance.Name))

	if err := providerInstance.CreateInstanceWithProgress(ctx, instanceConfig, progressCallback); err != nil {
		err := fmt.Errorf("Provider创建实例失败: %v", err)
		global.APP_LOG.Error("Provider创建实例失败", zap.Uint("taskId", task.ID), zap.Error(err))
		return err
	}

	global.APP_LOG.Info("Provider创建实例成功", zap.Uint("taskId", task.ID), zap.String("instanceName", instance.Name))

	// 更新进度到70%
	s.updateTaskProgress(task.ID, 70, "step.providerCreateSuccess")

	return nil
}

func (s *Service) detectCopySourceResources(ctx context.Context, providerInstance provider.Provider, providerType, sourceName string) (int, int64, int64, error) {
	cli := "lxc"
	if providerType == "incus" {
		cli = "incus"
	}
	quotedSourceName := shellSingleQuote(sourceName)
	cmd := fmt.Sprintf(`cpu="$(%s config get %s limits.cpu 2>/dev/null || true)"; memory="$(%s config get %s limits.memory 2>/dev/null || true)"; disk="$(%s config device get %s root size 2>/dev/null || true)"; if [ -z "$disk" ]; then disk="$(%s config device get %s root limits.max 2>/dev/null || true)"; fi; printf 'cpu=%%s\nmemory=%%s\ndisk=%%s\n' "$cpu" "$memory" "$disk"`,
		cli, quotedSourceName, cli, quotedSourceName, cli, quotedSourceName, cli, quotedSourceName)
	output, err := providerInstance.ExecuteSSHCommand(ctx, cmd)
	if err != nil {
		return 0, 0, 0, err
	}
	var cpu int
	var memory, disk int64
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "cpu":
			cpu = parseCopyCPUValue(value)
		case "memory":
			memory = parseCopySizeToMB(value)
		case "disk":
			disk = parseCopySizeToMB(value)
		}
	}
	return cpu, memory, disk, nil
}

func validateCopyResourceIsolation(provider providerModel.Provider, cpu int, memory, disk int64) error {
	// 复制模式：源容器可能未设置资源限制，此时跳过该资源的校验（不阻塞流程），
	// 仅当源容器设置了限制且超过节点可用容量时才报错。
	if provider.ContainerLimitCPU && cpu <= 0 {
		global.APP_LOG.Warn("复制模式源容器未设置 limits.cpu，跳过 CPU 资源隔离校验",
			zap.String("provider", provider.Name))
	}
	if provider.ContainerLimitMemory && memory <= 0 {
		global.APP_LOG.Warn("复制模式源容器未设置 limits.memory，跳过内存资源隔离校验",
			zap.String("provider", provider.Name))
	}
	if provider.ContainerLimitDisk && disk <= 0 {
		global.APP_LOG.Warn("复制模式源容器未设置 root 磁盘 size/limits.max，跳过磁盘资源隔离校验",
			zap.String("provider", provider.Name))
	}
	if provider.ContainerLimitCPU && cpu > 0 && provider.NodeCPUCores > 0 && cpu > provider.NodeCPUCores-provider.UsedCPUCores {
		return fmt.Errorf("节点CPU资源不足：需要 %d 核，当前可用 %d 核", cpu, provider.NodeCPUCores-provider.UsedCPUCores)
	}
	if provider.ContainerLimitMemory && memory > 0 && provider.NodeMemoryTotal > 0 && memory > provider.NodeMemoryTotal-provider.UsedMemory {
		return fmt.Errorf("节点内存资源不足：需要 %d MB，当前可用 %d MB", memory, provider.NodeMemoryTotal-provider.UsedMemory)
	}
	if provider.ContainerLimitDisk && disk > 0 && provider.NodeDiskTotal > 0 && disk > provider.NodeDiskTotal-provider.UsedDisk {
		return fmt.Errorf("节点磁盘资源不足：需要 %d MB，当前可用 %d MB", disk, provider.NodeDiskTotal-provider.UsedDisk)
	}
	return nil
}

func parseCopyCPUValue(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if n, err := strconv.Atoi(raw); err == nil && n > 0 {
		return n
	}
	total := 0
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if start, end, ok := strings.Cut(part, "-"); ok {
			a, aErr := strconv.Atoi(strings.TrimSpace(start))
			b, bErr := strconv.Atoi(strings.TrimSpace(end))
			if aErr == nil && bErr == nil && b >= a {
				total += b - a + 1
			}
			continue
		}
		if _, err := strconv.Atoi(part); err == nil {
			total++
		}
	}
	return total
}

func parseCopySizeToMB(raw string) int64 {
	value := strings.TrimSpace(strings.ToLower(raw))
	if value == "" {
		return 0
	}
	value = strings.TrimSuffix(value, "bytes")
	value = strings.TrimSuffix(value, "byte")
	multiplier := float64(1)
	suffixes := []struct {
		suffix string
		mul    float64
	}{
		{"tib", 1024 * 1024},
		{"tb", 1000 * 1000},
		{"gib", 1024},
		{"gb", 1000},
		{"mib", 1},
		{"mb", 1},
		{"kib", 1.0 / 1024},
		{"kb", 1.0 / 1000},
		{"ti", 1024 * 1024},
		{"t", 1024 * 1024},
		{"gi", 1024},
		{"g", 1024},
		{"mi", 1},
		{"m", 1},
	}
	for _, item := range suffixes {
		if strings.HasSuffix(value, item.suffix) {
			value = strings.TrimSpace(strings.TrimSuffix(value, item.suffix))
			multiplier = item.mul
			break
		}
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || number <= 0 {
		return 0
	}
	return int64(math.Ceil(number * multiplier))
}
