package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"oneclickvirt/constant"
	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	resourceModel "oneclickvirt/model/resource"
	systemModel "oneclickvirt/model/system"
	userModel "oneclickvirt/model/user"
	"oneclickvirt/service/images"
	"oneclickvirt/service/resources"

	"go.uber.org/zap"
)

// GetAvailableProviders 获取可用节点列表
func (s *Service) GetAvailableProviders(userID uint) ([]userModel.AvailableProviderResponse, error) {
	var dbProviders []providerModel.Provider

	// 获取允许申领且未冻结的Provider，包括部分在线的服务器
	err := global.APP_DB.Where("(status = ? OR status = ?) AND allow_claim = ? AND is_frozen = ?",
		"active", "partial", true, false).
		Limit(1000). // 限制最多1000条，防止单次查询过大
		Find(&dbProviders).Error
	if err != nil {
		return nil, err
	}

	global.APP_LOG.Info("开始处理Provider列表",
		zap.Int("totalProviders", len(dbProviders)),
		zap.Uint("userID", userID))

	// 批量查询所有Provider的活跃预留资源
	var providerIDs []uint
	for _, provider := range dbProviders {
		providerIDs = append(providerIDs, provider.ID)
	}

	var allReservations []resourceModel.ResourceReservation
	if len(providerIDs) > 0 {
		global.APP_DB.Where("provider_id IN ? AND expires_at > ?", providerIDs, time.Now()).
			Find(&allReservations)
	}

	// 按provider_id分组预留资源
	reservationsByProvider := make(map[uint][]resourceModel.ResourceReservation)
	for _, reservation := range allReservations {
		reservationsByProvider[reservation.ProviderID] = append(
			reservationsByProvider[reservation.ProviderID], reservation)
	}

	var providers []userModel.AvailableProviderResponse
	skippedCount := 0

	for _, provider := range dbProviders {
		// 只在资源信息完全缺失时才进行同步，避免阻塞用户请求
		if !provider.ResourceSynced && provider.NodeCPUCores == 0 && provider.NodeMemoryTotal == 0 && provider.NodeDiskTotal == 0 {
			global.APP_LOG.Info("节点资源信息缺失，跳过该节点",
				zap.String("provider", provider.Name),
				zap.Uint("id", provider.ID))

			// 跳过没有有效资源数据的Provider，不返回给用户
			skippedCount++
			continue
		}

		// 对于有可用资源的服务器，添加到返回列表
		if provider.ContainerEnabled || provider.VirtualMachineEnabled {
			// 检查是否有有效的资源数据，如果没有则跳过
			if provider.NodeCPUCores == 0 || provider.NodeMemoryTotal == 0 || provider.NodeDiskTotal == 0 {
				global.APP_LOG.Warn("节点资源数据不完整，跳过该节点",
					zap.String("provider", provider.Name),
					zap.Uint("id", provider.ID),
					zap.Int("cpu", provider.NodeCPUCores),
					zap.Int64("memory", provider.NodeMemoryTotal),
					zap.Int64("disk", provider.NodeDiskTotal))
				skippedCount++
				continue
			}

			// 从预加载的map中获取该Provider的活跃预留资源
			activeReservations := reservationsByProvider[provider.ID]

			// 计算预留资源占用
			reservedCPU := 0
			reservedMemory := int64(0)
			reservedDisk := int64(0)
			reservedContainers := 0
			reservedVMs := 0

			for _, reservation := range activeReservations {
				if reservation.InstanceType == "vm" {
					reservedCPU += reservation.CPU
					reservedVMs++
				} else {
					reservedContainers++
				}
				reservedMemory += reservation.Memory
				reservedDisk += reservation.Disk
			}

			// 使用真实的资源数据
			nodeCPU := provider.NodeCPUCores
			nodeMemory := provider.NodeMemoryTotal
			nodeDisk := provider.NodeDiskTotal

			// 计算实际使用的资源 = 已分配的 + 预留的
			actualUsedCPU := provider.UsedCPUCores + reservedCPU
			actualUsedMemory := provider.UsedMemory + reservedMemory
			actualUsedDisk := provider.UsedDisk + reservedDisk
			actualUsedContainers := provider.ContainerCount + reservedContainers
			actualUsedVMs := provider.VMCount + reservedVMs

			// 计算可用资源
			availableCPU := nodeCPU - actualUsedCPU
			availableMemory := nodeMemory - actualUsedMemory
			availableDisk := nodeDisk - actualUsedDisk

			// 确保不出现负数
			if availableCPU < 0 {
				availableCPU = 0
			}
			if availableMemory < 0 {
				availableMemory = 0
			}
			if availableDisk < 0 {
				availableDisk = 0
			}

			// 计算资源使用率
			cpuUsage := float64(0)
			memoryUsage := float64(0)
			if nodeCPU > 0 {
				cpuUsage = float64(actualUsedCPU) / float64(nodeCPU) * 100
			}
			if nodeMemory > 0 {
				memoryUsage = float64(actualUsedMemory) / float64(nodeMemory) * 100
			}

			// 计算可用实例槽位 - 基于容器和虚拟机的单独限制
			availableContainerSlots := -1 // -1 表示不限制
			availableVMSlots := -1        // -1 表示不限制

			if provider.MaxContainerInstances > 0 {
				availableContainerSlots = provider.MaxContainerInstances - actualUsedContainers
				if availableContainerSlots < 0 {
					availableContainerSlots = 0
				}
			}

			if provider.MaxVMInstances > 0 {
				availableVMSlots = provider.MaxVMInstances - actualUsedVMs
				if availableVMSlots < 0 {
					availableVMSlots = 0
				}
			}

			providerResp := userModel.AvailableProviderResponse{
				ID:                      provider.ID,
				Name:                    provider.Name,
				Type:                    provider.Type,
				Region:                  provider.Region,
				Country:                 provider.Country,
				CountryCode:             provider.CountryCode,
				City:                    provider.City,
				Status:                  provider.Status,
				CPU:                     nodeCPU,
				Memory:                  int(nodeMemory), // 返回MB单位
				Disk:                    int(nodeDisk),   // 返回MB单位
				AvailableContainerSlots: availableContainerSlots,
				AvailableVMSlots:        availableVMSlots,
				MaxContainerInstances:   provider.MaxContainerInstances,
				MaxVMInstances:          provider.MaxVMInstances,
				CPUUsage:                cpuUsage,
				MemoryUsage:             memoryUsage,
				ContainerEnabled:        provider.ContainerEnabled,
				VmEnabled:               provider.VirtualMachineEnabled,
			}
			providers = append(providers, providerResp)
		}
	}

	global.APP_LOG.Info("Provider列表处理完成",
		zap.Int("totalProviders", len(dbProviders)),
		zap.Int("availableProviders", len(providers)),
		zap.Int("skippedProviders", skippedCount),
		zap.Uint("userID", userID))

	return providers, nil
}

// GetSystemImages 获取系统镜像列表
func (s *Service) GetSystemImages(userID uint, req userModel.SystemImagesRequest) ([]userModel.SystemImageResponse, error) {
	var images []systemModel.SystemImage

	// 从数据库获取镜像
	query := global.APP_DB.Where("status = ?", "active")

	if err := query.Order("os_type ASC, name ASC").Find(&images).Error; err != nil {
		return nil, err
	}

	var response []userModel.SystemImageResponse
	for _, img := range images {
		response = append(response, userModel.SystemImageResponse{
			ID:           img.ID,
			Name:         img.Name,
			DisplayName:  img.Name,
			Version:      img.OSVersion,
			Architecture: img.Architecture,
			OsType:       img.OSType,
			ProviderType: img.ProviderType,
			InstanceType: img.InstanceType,
			ImageURL:     img.URL,
			Description:  img.Description,
			IsActive:     img.Status == "active",
			MinMemoryMB:  img.MinMemoryMB,
			MinDiskMB:    img.MinDiskMB,
			UseCDN:       img.UseCDN,
		})
	}

	return response, nil
}

// GetInstanceConfig 获取实例配置选项 - 根据用户配额和节点限制动态过滤
func (s *Service) GetInstanceConfig(userID uint, providerID uint) (*userModel.InstanceConfigResponse, error) {
	// 获取用户配额信息
	quotaService := resources.NewQuotaService()
	quotaInfo, err := quotaService.GetUserQuotaInfo(userID)
	if err != nil {
		return nil, fmt.Errorf("获取用户配额信息失败: %v", err)
	}

	// 计算用户剩余的全局配额
	remainingGlobalCPU := quotaInfo.MaxQuota.CPU - quotaInfo.CurrentResources.CPU
	remainingGlobalMemory := quotaInfo.MaxQuota.Memory - quotaInfo.CurrentResources.Memory
	remainingGlobalDisk := quotaInfo.MaxQuota.Disk - quotaInfo.CurrentResources.Disk
	remainingGlobalBandwidth := quotaInfo.MaxQuota.Bandwidth

	// 获取节点的等级限制（如果指定了 providerID）
	var providerLevelLimits map[string]interface{}
	if providerID > 0 {
		var provider providerModel.Provider
		if err := global.APP_DB.First(&provider, providerID).Error; err == nil && provider.LevelLimits != "" {
			// 解析节点的 levelLimits JSON
			var allLevelLimits map[string]map[string]interface{}
			if err := json.Unmarshal([]byte(provider.LevelLimits), &allLevelLimits); err == nil {
				// 获取用户等级对应的限制
				var user userModel.User
				if err := global.APP_DB.First(&user, userID).Error; err == nil {
					levelKey := fmt.Sprintf("%d", user.Level)
					if limits, ok := allLevelLimits[levelKey]; ok {
						providerLevelLimits = limits
					}
				}
			}
		}
	}

	// 计算最终可用的配额（取剩余全局配额和节点限制的最小值）
	finalMaxCPU := remainingGlobalCPU
	finalMaxMemory := remainingGlobalMemory
	finalMaxDisk := remainingGlobalDisk
	finalMaxBandwidth := remainingGlobalBandwidth

	if providerLevelLimits != nil {
		// 如果有节点限制，取最小值
		if maxResources, ok := providerLevelLimits["max-resources"].(map[string]interface{}); ok {
			if cpu, ok := maxResources["cpu"].(float64); ok && int(cpu) < finalMaxCPU {
				finalMaxCPU = int(cpu)
			}
			if memory, ok := maxResources["memory"].(float64); ok && int64(memory) < finalMaxMemory {
				finalMaxMemory = int64(memory)
			}
			if disk, ok := maxResources["disk"].(float64); ok && int64(disk) < finalMaxDisk {
				finalMaxDisk = int64(disk)
			}
			if bandwidth, ok := maxResources["bandwidth"].(float64); ok && int(bandwidth) < finalMaxBandwidth {
				finalMaxBandwidth = int(bandwidth)
			}
		}
	}

	// 获取所有预定义规格
	allCPUSpecs := constant.PredefinedCPUSpecs
	allMemorySpecs := constant.PredefinedMemorySpecs
	allDiskSpecs := constant.PredefinedDiskSpecs
	allBandwidthSpecs := constant.PredefinedBandwidthSpecs

	// 根据最终可用配额动态过滤规格
	var availableCPUSpecs []constant.CPUSpec
	for _, spec := range allCPUSpecs {
		if spec.Cores <= finalMaxCPU {
			availableCPUSpecs = append(availableCPUSpecs, spec)
		}
	}

	var availableMemorySpecs []constant.MemorySpec
	for _, spec := range allMemorySpecs {
		if int64(spec.SizeMB) <= finalMaxMemory {
			availableMemorySpecs = append(availableMemorySpecs, spec)
		}
	}

	var availableDiskSpecs []constant.DiskSpec
	for _, spec := range allDiskSpecs {
		if int64(spec.SizeMB) <= finalMaxDisk {
			availableDiskSpecs = append(availableDiskSpecs, spec)
		}
	}

	var availableBandwidthSpecs []constant.BandwidthSpec
	for _, spec := range allBandwidthSpecs {
		if spec.SpeedMbps <= finalMaxBandwidth {
			availableBandwidthSpecs = append(availableBandwidthSpecs, spec)
		}
	}

	// 获取可用镜像（从数据库）
	images, err := s.GetSystemImages(userID, userModel.SystemImagesRequest{})
	if err != nil {
		return nil, fmt.Errorf("获取镜像列表失败: %v", err)
	}

	// 返回所有可用的磁盘规格，让前端根据选择的镜像类型动态过滤
	filteredDiskSpecs := availableDiskSpecs

	// 转换为前端期望的格式
	cpuOptions := make([]userModel.CPUSpecResponse, len(availableCPUSpecs))
	for i, spec := range availableCPUSpecs {
		cpuOptions[i] = userModel.CPUSpecResponse{
			ID:    spec.ID,
			Cores: spec.Cores,
			Name:  spec.Name,
		}
	}

	memoryOptions := make([]userModel.MemorySpecResponse, len(availableMemorySpecs))
	for i, spec := range availableMemorySpecs {
		memoryOptions[i] = userModel.MemorySpecResponse{
			ID:     spec.ID,
			SizeMB: spec.SizeMB,
			Name:   spec.Name,
		}
	}

	diskOptions := make([]userModel.DiskSpecResponse, len(filteredDiskSpecs))
	for i, spec := range filteredDiskSpecs {
		diskOptions[i] = userModel.DiskSpecResponse{
			ID:     spec.ID,
			SizeMB: spec.SizeMB,
			Name:   spec.Name,
		}
	}

	bandwidthOptions := make([]userModel.BandwidthSpecResponse, len(availableBandwidthSpecs))
	for i, spec := range availableBandwidthSpecs {
		bandwidthOptions[i] = userModel.BandwidthSpecResponse{
			ID:        spec.ID,
			SpeedMbps: spec.SpeedMbps,
			Name:      spec.Name,
		}
	}

	return &userModel.InstanceConfigResponse{
		Images:         images,
		CPUSpecs:       cpuOptions,
		MemorySpecs:    memoryOptions,
		DiskSpecs:      diskOptions,
		BandwidthSpecs: bandwidthOptions,
	}, nil
}

// GetFilteredSystemImages 根据Provider和实例类型获取过滤后的系统镜像列表
func (s *Service) GetFilteredSystemImages(userID uint, providerID uint, instanceType string) ([]userModel.SystemImageResponse, error) {
	// 验证Provider是否存在
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, providerID).Error; err != nil {
		return nil, errors.New("Provider不存在")
	}

	// 验证Provider是否支持该实例类型
	resourceService := &resources.ResourceService{}
	if err := resourceService.ValidateInstanceTypeSupport(providerID, instanceType); err != nil {
		return nil, err
	}

	// 使用镜像服务获取过滤后的镜像
	imageService := &images.ImageService{}
	images, err := imageService.GetFilteredImages(providerID, instanceType)
	if err != nil {
		return nil, err
	}

	var response []userModel.SystemImageResponse
	for _, img := range images {
		response = append(response, userModel.SystemImageResponse{
			ID:           img.ID,
			Name:         img.Name,
			DisplayName:  img.Name,
			Version:      img.OSVersion,
			Architecture: img.Architecture,
			OsType:       img.OSType,
			ProviderType: img.ProviderType,
			InstanceType: img.InstanceType,
			ImageURL:     img.URL,
			Description:  img.Description,
			IsActive:     img.Status == "active",
			MinMemoryMB:  img.MinMemoryMB,
			MinDiskMB:    img.MinDiskMB,
			UseCDN:       img.UseCDN,
		})
	}

	return response, nil
}

// GetProviderCapabilities 获取Provider能力
func (s *Service) GetProviderCapabilities(userID uint, providerID uint) (map[string]interface{}, error) {
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, providerID).Error; err != nil {
		return nil, errors.New("Provider不存在")
	}

	// 构建支持的实例类型列表
	var supportedTypes []string
	if provider.ContainerEnabled {
		supportedTypes = append(supportedTypes, "container")
	}
	if provider.VirtualMachineEnabled {
		supportedTypes = append(supportedTypes, "vm")
	}

	capabilities := map[string]interface{}{
		"containerEnabled": provider.ContainerEnabled,
		"vmEnabled":        provider.VirtualMachineEnabled,
		"supportedTypes":   supportedTypes,
		"maxCpu":           provider.NodeCPUCores,
		"maxMemory":        provider.NodeMemoryTotal,
		"maxDisk":          provider.NodeDiskTotal,
		"region":           provider.Region,
		"country":          provider.Country,
		"city":             provider.City,
	}

	return capabilities, nil
}

// GetInstanceTypePermissions 获取实例类型权限
func (s *Service) GetInstanceTypePermissions(userID uint) (map[string]interface{}, error) {
	// 获取用户信息
	var user userModel.User
	if err := global.APP_DB.First(&user, userID).Error; err != nil {
		return nil, errors.New("用户不存在")
	}

	// 从配置获取实例类型权限
	permissions := global.APP_CONFIG.Quota.InstanceTypePermissions

	// 检查用户等级是否允许创建容器和虚拟机
	canCreateContainer := user.Level >= permissions.MinLevelForContainer
	canCreateVM := user.Level >= permissions.MinLevelForVM
	canDeleteContainer := user.Level >= permissions.MinLevelForDeleteContainer
	canDeleteVM := user.Level >= permissions.MinLevelForDeleteVM
	canResetContainer := user.Level >= permissions.MinLevelForResetContainer
	canResetVM := user.Level >= permissions.MinLevelForResetVM

	result := map[string]interface{}{
		"userLevel":                  user.Level,
		"canCreateContainer":         canCreateContainer,
		"canCreateVM":                canCreateVM,
		"canDeleteContainer":         canDeleteContainer,
		"canDeleteVM":                canDeleteVM,
		"canResetContainer":          canResetContainer,
		"canResetVM":                 canResetVM,
		"minLevelForContainer":       permissions.MinLevelForContainer,
		"minLevelForVM":              permissions.MinLevelForVM,
		"minLevelForDeleteContainer": permissions.MinLevelForDeleteContainer,
		"minLevelForDeleteVM":        permissions.MinLevelForDeleteVM,
		"minLevelForResetContainer":  permissions.MinLevelForResetContainer,
		"minLevelForResetVM":         permissions.MinLevelForResetVM,
	}

	return result, nil
}
