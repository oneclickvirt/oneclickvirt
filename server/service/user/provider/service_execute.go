package provider

import (
	"context"
	"encoding/json"
	"fmt"
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

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// executeProviderCreation 阶段2: Provider API调用 (30% -> 60%)
func (s *Service) executeProviderCreation(ctx context.Context, task *adminModel.Task, instance *providerModel.Instance) error {
	global.APP_LOG.Debug("开始Provider API调用阶段", zap.Uint("taskId", task.ID))

	// 检查上下文状态
	if ctx.Err() != nil {
		global.APP_LOG.Warn("Provider API调用开始时上下文已取消", zap.Uint("taskId", task.ID), zap.Error(ctx.Err()))
		return ctx.Err()
	}

	// 解析任务数据获取创建实例所需的参数
	var taskReq adminModel.CreateInstanceTaskRequest

	if err := json.Unmarshal([]byte(task.TaskData), &taskReq); err != nil {
		err := fmt.Errorf("解析任务数据失败: %v", err)
		global.APP_LOG.Error("解析任务数据失败", zap.Uint("taskId", task.ID), zap.Error(err))
		return err
	}

	// 直接从数据库获取Provider配置（使用ProviderID而不是Name）
	// 允许 active 和 partial 状态的Provider执行任务（与GetAvailableProviders保持一致）
	var dbProvider providerModel.Provider
	if err := global.APP_DB.Where("id = ? AND (status = ? OR status = ?)", instance.ProviderID, "active", "partial").First(&dbProvider).Error; err != nil {
		err := fmt.Errorf("Provider ID %d 不存在或不可用", instance.ProviderID)
		global.APP_LOG.Error("Provider不存在", zap.Uint("taskId", task.ID), zap.Uint("providerId", instance.ProviderID), zap.Error(err))
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

	// 实现实际的Provider API调用逻辑
	// 首先尝试从ProviderService获取已连接的Provider实例（使用ID）
	providerSvc := providerService.GetProviderService()
	providerInstance, exists := providerSvc.GetProviderByID(instance.ProviderID)

	if !exists {
		// 如果Provider未连接，尝试动态加载
		global.APP_LOG.Debug("Provider未连接，尝试动态加载", zap.Uint("providerId", localProviderID), zap.String("name", localProviderName))
		if err := providerSvc.LoadProvider(dbProvider); err != nil {
			global.APP_LOG.Error("动态加载Provider失败", zap.Uint("providerId", localProviderID), zap.String("name", localProviderName), zap.Error(err))
			err := fmt.Errorf("Provider ID %d 连接失败: %v", localProviderID, err)
			return err
		}

		// 重新获取Provider实例
		providerInstance, exists = providerSvc.GetProviderByID(instance.ProviderID)
		if !exists {
			err := fmt.Errorf("Provider ID %d 连接后仍然不可用", localProviderID)
			global.APP_LOG.Error("Provider连接后仍然不可用", zap.Uint("taskId", task.ID), zap.Uint("providerId", localProviderID))
			return err
		}
	}

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

	global.APP_LOG.Debug("规格ID转换为实际数值",
		zap.Uint("taskId", task.ID),
		zap.String("cpuId", taskReq.CPUId), zap.Int("cpuCores", cpuSpec.Cores),
		zap.String("memoryId", taskReq.MemoryId), zap.Int("memorySizeMB", memorySpec.SizeMB),
		zap.String("diskId", taskReq.DiskId), zap.Int("diskSizeMB", diskSpec.SizeMB),
		zap.String("bandwidthId", taskReq.BandwidthId), zap.Int("bandwidthSpeedMbps", bandwidthSpec.SpeedMbps),
		zap.Int("userLevel", user.Level))

	// 构建实例配置，使用实际数值而非ID
	instanceConfig := provider.InstanceConfig{
		Name:         instance.Name,
		Image:        systemImage.Name,
		CPU:          fmt.Sprintf("%d", cpuSpec.Cores),      // 使用实际核心数
		Memory:       fmt.Sprintf("%dm", memorySpec.SizeMB), // 使用实际内存大小（MB格式）
		Disk:         fmt.Sprintf("%dm", diskSpec.SizeMB),   // 使用实际磁盘大小（MB格式）
		InstanceType: instance.InstanceType,
		ImageURL:     systemImage.URL, // 镜像URL用于下载
		Metadata: map[string]string{
			"user_level":               fmt.Sprintf("%d", user.Level),              // 用户等级，用于带宽限制配置
			"bandwidth_spec":           fmt.Sprintf("%d", bandwidthSpec.SpeedMbps), // 用户选择的带宽规格
			"ipv4_port_mapping_method": localProviderIPv4PortMappingMethod,         // IPv4端口映射方式（从Provider配置获取）
			"ipv6_port_mapping_method": localProviderIPv6PortMappingMethod,         // IPv6端口映射方式（从Provider配置获取）
			"network_type":             localProviderNetworkType,                   // 网络配置类型：nat_ipv4, nat_ipv4_ipv6, dedicated_ipv4, dedicated_ipv4_ipv6, ipv6_only
			"instance_id":              fmt.Sprintf("%d", instance.ID),             // 实例ID，用于端口分配
			"provider_id":              fmt.Sprintf("%d", localProviderID),         // Provider ID，用于端口区间分配
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
	} else {
		// 获取已分配的端口映射
		portMappings, err := portMappingService.GetInstancePortMappings(instance.ID)
		if err != nil {
			global.APP_LOG.Warn("获取端口映射失败",
				zap.Uint("taskId", task.ID),
				zap.Uint("instanceId", instance.ID),
				zap.Error(err))
		} else {
			// 对于Docker容器，将端口映射信息添加到实例配置中
			if localProviderType == "docker" {
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

				global.APP_LOG.Debug("Docker容器端口映射预分配成功",
					zap.Uint("taskId", task.ID),
					zap.Uint("instanceId", instance.ID),
					zap.Int("portCount", len(ports)),
					zap.Strings("ports", ports))
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

	// 调用Provider API创建实例
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
		err := fmt.Errorf("Provider API创建实例失败: %v", err)
		global.APP_LOG.Error("Provider API创建实例失败", zap.Uint("taskId", task.ID), zap.Error(err))
		return err
	}

	global.APP_LOG.Debug("Provider API调用成功", zap.Uint("taskId", task.ID), zap.String("instanceName", instance.Name))

	// 更新进度到70%
	s.updateTaskProgress(task.ID, 70, "Provider API调用成功")

	return nil
}
