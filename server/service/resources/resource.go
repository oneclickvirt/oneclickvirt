package resources

import (
	"context"
	"errors"
	"fmt"
	"oneclickvirt/service/database"
	"time"

	"oneclickvirt/global"
	dashboardModel "oneclickvirt/model/dashboard"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/model/resource"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// ResourceService 资源管理服务 - 使用数据库级锁，移除应用级锁
type ResourceService struct {
	// 移除mutex，完全依赖数据库悲观锁
}

// SyncProviderResourcesAsync 异步同步Provider资源
func (s *ResourceService) SyncProviderResourcesAsync(providerID uint) {
	global.APP_LOG.Debug("启动异步资源同步", zap.Uint("providerId", providerID))

	go func() {
		if err := s.SyncProviderResources(providerID); err != nil {
			global.APP_LOG.Warn("异步资源同步失败",
				zap.Uint("providerID", providerID),
				zap.String("error", utils.TruncateString(err.Error(), 200)))
		} else {
			global.APP_LOG.Debug("异步资源同步成功", zap.Uint("providerId", providerID))
		}
	}()
}

// CheckProviderResources 检查Provider资源是否充足
func (s *ResourceService) CheckProviderResources(req resource.ResourceCheckRequest) (*resource.ResourceCheckResult, error) {
	global.APP_LOG.Debug("开始检查Provider资源",
		zap.Uint("providerId", req.ProviderID),
		zap.String("instanceType", req.InstanceType),
		zap.Int("cpu", req.CPU),
		zap.Int64("memory", req.Memory),
		zap.Int64("disk", req.Disk))

	result, err := s.checkResourcesInTransaction(req)

	if err != nil {
		global.APP_LOG.Error("检查Provider资源失败",
			zap.Uint("providerId", req.ProviderID),
			zap.String("error", utils.TruncateString(err.Error(), 200)))
	} else if result != nil {
		global.APP_LOG.Debug("资源检查完成",
			zap.Uint("providerId", req.ProviderID),
			zap.Bool("allowed", result.Allowed),
			zap.String("reason", utils.TruncateString(result.Reason, 100)))
	}

	return result, err
}

// CheckProviderResourcesWithTx 在指定事务中检查Provider资源是否充足
func (s *ResourceService) CheckProviderResourcesWithTx(tx *gorm.DB, req resource.ResourceCheckRequest) (*resource.ResourceCheckResult, error) {
	global.APP_LOG.Debug("在事务中开始检查Provider资源",
		zap.Uint("providerId", req.ProviderID),
		zap.String("instanceType", req.InstanceType),
		zap.Int("cpu", req.CPU),
		zap.Int64("memory", req.Memory),
		zap.Int64("disk", req.Disk))

	var provider providerModel.Provider
	if err := tx.First(&provider, req.ProviderID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			global.APP_LOG.Warn("Provider不存在", zap.Uint("providerId", req.ProviderID))
		} else {
			global.APP_LOG.Error("查询Provider失败",
				zap.Uint("providerId", req.ProviderID),
				zap.String("error", utils.TruncateString(err.Error(), 200)))
		}
		return nil, fmt.Errorf("Provider不存在: %v", err)
	}

	result := s.checkProviderResourceAvailability(&provider, req)

	if result.Allowed {
		global.APP_LOG.Debug("事务中资源检查通过",
			zap.Uint("providerId", req.ProviderID))
	} else {
		global.APP_LOG.Info("事务中资源检查未通过",
			zap.Uint("providerId", req.ProviderID),
			zap.String("reason", utils.TruncateString(result.Reason, 100)))
	}

	return result, nil
}

// checkResourcesInTransaction 在事务中检查资源
func (s *ResourceService) checkResourcesInTransaction(req resource.ResourceCheckRequest) (*resource.ResourceCheckResult, error) {
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, req.ProviderID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			global.APP_LOG.Warn("Provider不存在", zap.Uint("providerId", req.ProviderID))
		} else {
			global.APP_LOG.Error("查询Provider失败",
				zap.Uint("providerId", req.ProviderID),
				zap.String("error", utils.TruncateString(err.Error(), 200)))
		}
		return nil, fmt.Errorf("Provider不存在: %v", err)
	}

	result := s.checkProviderResourceAvailability(&provider, req)
	return result, nil
}

// checkProviderResourceAvailability 检查Provider资源可用性
func (s *ResourceService) checkProviderResourceAvailability(provider *providerModel.Provider, req resource.ResourceCheckRequest) *resource.ResourceCheckResult {
	result := &resource.ResourceCheckResult{
		Allowed: true,
	}

	// 检查Provider是否支持指定类型
	if req.InstanceType == "container" && !provider.ContainerEnabled {
		result.Allowed = false
		result.Reason = "该节点不支持容器类型"
		return result
	}

	if req.InstanceType == "vm" && !provider.VirtualMachineEnabled {
		result.Allowed = false
		result.Reason = "该节点不支持虚拟机类型"
		return result
	}

	// 计算可用资源（考虑Provider的资源限制配置）
	// 如果资源类型配置为不限制（false），则不计入总量，允许超分配
	availableCPU := provider.NodeCPUCores - provider.UsedCPUCores
	availableMemory := provider.NodeMemoryTotal - provider.UsedMemory
	availableDisk := provider.NodeDiskTotal - provider.UsedDisk

	result.AvailableCPU = availableCPU
	result.AvailableMemory = availableMemory
	result.AvailableDisk = availableDisk

	// 根据实例类型检查资源
	if req.InstanceType == "container" {
		// 检查容器数量限制
		if provider.MaxContainerInstances > 0 && provider.ContainerCount >= provider.MaxContainerInstances {
			result.Allowed = false
			result.Reason = fmt.Sprintf("容器数量已达上限：%d/%d", provider.ContainerCount, provider.MaxContainerInstances)
			return result
		}

		// 容器CPU检查：如果ContainerLimitCPU为false，允许超分配（不检查）
		if provider.ContainerLimitCPU && req.CPU > availableCPU {
			result.Allowed = false
			result.Reason = fmt.Sprintf("CPU资源不足：需要 %d 核，可用 %d 核", req.CPU, availableCPU)
			return result
		}

		// 容器内存检查：如果ContainerLimitMemory为false，允许超分配（不检查）
		if provider.ContainerLimitMemory && req.Memory > availableMemory {
			result.Allowed = false
			result.Reason = fmt.Sprintf("内存资源不足：需要 %d MB，可用 %d MB", req.Memory, availableMemory)
			return result
		}

		// 容器磁盘检查：如果ContainerLimitDisk为false，允许超分配（不检查）
		if provider.ContainerLimitDisk && req.Disk > availableDisk {
			result.Allowed = false
			result.Reason = fmt.Sprintf("磁盘资源不足：需要 %d MB，可用 %d MB", req.Disk, availableDisk)
			return result
		}
	} else {
		// 虚拟机数量限制
		if provider.MaxVMInstances > 0 && provider.VMCount >= provider.MaxVMInstances {
			result.Allowed = false
			result.Reason = fmt.Sprintf("虚拟机数量已达上限：%d/%d", provider.VMCount, provider.MaxVMInstances)
			return result
		}

		// 虚拟机CPU检查：如果VMLimitCPU为false，允许超分配（不检查）
		if provider.VMLimitCPU && req.CPU > availableCPU {
			result.Allowed = false
			result.Reason = fmt.Sprintf("CPU资源不足：需要 %d 核，可用 %d 核", req.CPU, availableCPU)
			return result
		}

		// 虚拟机内存检查：如果VMLimitMemory为false，允许超分配（不检查）
		if provider.VMLimitMemory && req.Memory > availableMemory {
			result.Allowed = false
			result.Reason = fmt.Sprintf("内存资源不足：需要 %d MB，可用 %d MB", req.Memory, availableMemory)
			return result
		}

		// 虚拟机磁盘检查：如果VMLimitDisk为false，允许超分配（不检查）
		if provider.VMLimitDisk && req.Disk > availableDisk {
			result.Allowed = false
			result.Reason = fmt.Sprintf("磁盘资源不足：需要 %d MB，可用 %d MB", req.Disk, availableDisk)
			return result
		}
	}

	return result
}

// AllocateResourcesInTx 在事务中分配资源（不创建新事务，使用悲观锁）
// 根据Provider的资源限制配置决定是否扣减资源
func (s *ResourceService) AllocateResourcesInTx(tx *gorm.DB, providerID uint, instanceType string, cpu int, memory, disk int64) error {
	global.APP_LOG.Info("开始分配资源",
		zap.Uint("providerId", providerID),
		zap.String("instanceType", instanceType),
		zap.Int("cpu", cpu),
		zap.Int64("memory", memory),
		zap.Int64("disk", disk))

	var provider providerModel.Provider
	// 使用悲观锁锁定Provider记录
	if err := tx.Set("gorm:query_option", "FOR UPDATE").First(&provider, providerID).Error; err != nil {
		global.APP_LOG.Error("锁定Provider失败",
			zap.Uint("providerId", providerID),
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return fmt.Errorf("Provider不存在或无法锁定: %v", err)
	}

	// 更新资源占用（根据资源限制配置决定是否扣减）
	updates := map[string]interface{}{
		"updated_at": time.Now(),
	}

	// 根据实例类型和资源限制配置更新资源占用
	if instanceType == "vm" {
		// 虚拟机：根据VMLimitXXX配置决定是否扣减资源
		if provider.VMLimitCPU {
			updates["used_cpu_cores"] = provider.UsedCPUCores + cpu
			global.APP_LOG.Debug("扣减VM CPU资源", zap.Int("cpu", cpu))
		} else {
			global.APP_LOG.Debug("VM CPU不计入总量（允许超分配）", zap.Int("cpu", cpu))
		}

		if provider.VMLimitMemory {
			updates["used_memory"] = provider.UsedMemory + memory
			global.APP_LOG.Debug("扣减VM内存资源", zap.Int64("memory", memory))
		} else {
			global.APP_LOG.Debug("VM内存不计入总量（允许超分配）", zap.Int64("memory", memory))
		}

		if provider.VMLimitDisk {
			updates["used_disk"] = provider.UsedDisk + disk
			global.APP_LOG.Debug("扣减VM磁盘资源", zap.Int64("disk", disk))
		} else {
			global.APP_LOG.Debug("VM磁盘不计入总量（允许超分配）", zap.Int64("disk", disk))
		}

		updates["vm_count"] = provider.VMCount + 1
	} else {
		// 容器：根据ContainerLimitXXX配置决定是否扣减资源
		if provider.ContainerLimitCPU {
			updates["used_cpu_cores"] = provider.UsedCPUCores + cpu
			global.APP_LOG.Debug("扣减容器CPU资源", zap.Int("cpu", cpu))
		} else {
			global.APP_LOG.Debug("容器CPU不计入总量（允许超分配）", zap.Int("cpu", cpu))
		}

		if provider.ContainerLimitMemory {
			updates["used_memory"] = provider.UsedMemory + memory
			global.APP_LOG.Debug("扣减容器内存资源", zap.Int64("memory", memory))
		} else {
			global.APP_LOG.Debug("容器内存不计入总量（允许超分配）", zap.Int64("memory", memory))
		}

		if provider.ContainerLimitDisk {
			updates["used_disk"] = provider.UsedDisk + disk
			global.APP_LOG.Debug("扣减容器磁盘资源", zap.Int64("disk", disk))
		} else {
			global.APP_LOG.Debug("容器磁盘不计入总量（允许超分配）", zap.Int64("disk", disk))
		}

		updates["container_count"] = provider.ContainerCount + 1
	}

	if err := tx.Model(&provider).Updates(updates).Error; err != nil {
		global.APP_LOG.Error("更新资源占用失败",
			zap.Uint("providerId", providerID),
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return err
	}

	global.APP_LOG.Info("资源分配成功",
		zap.Uint("providerId", providerID),
		zap.String("instanceType", instanceType),
		zap.Int("cpu", cpu),
		zap.Int64("memory", memory),
		zap.Int64("disk", disk))

	return nil
}

// AllocateResources 分配资源（创建实例时调用）- 保持向后兼容
func (s *ResourceService) AllocateResources(providerID uint, instanceType string, cpu int, memory, disk int64) error {
	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return s.AllocateResourcesInTx(tx, providerID, instanceType, cpu, memory, disk)
	})
}

// ReleaseResourcesInTx 在事务中释放资源
// 根据Provider的资源限制配置决定是否回收资源
func (s *ResourceService) ReleaseResourcesInTx(tx *gorm.DB, providerID uint, instanceType string, cpu int, memory, disk int64) error {
	global.APP_LOG.Info("开始释放资源",
		zap.Uint("providerId", providerID),
		zap.String("instanceType", instanceType),
		zap.Int("cpu", cpu),
		zap.Int64("memory", memory),
		zap.Int64("disk", disk))

	var provider providerModel.Provider
	// 使用悲观锁锁定Provider记录
	if err := tx.Set("gorm:query_option", "FOR UPDATE").First(&provider, providerID).Error; err != nil {
		global.APP_LOG.Error("锁定Provider失败",
			zap.Uint("providerId", providerID),
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return fmt.Errorf("Provider不存在或无法锁定: %v", err)
	}

	// 更新资源占用（根据资源限制配置决定是否回收）
	updates := map[string]interface{}{
		"updated_at": time.Now(),
	}

	// 根据实例类型和资源限制配置更新资源占用
	if instanceType == "vm" {
		// 虚拟机：根据VMLimitXXX配置决定是否回收资源
		if provider.VMLimitCPU {
			newCPU := provider.UsedCPUCores - cpu
			if newCPU < 0 {
				newCPU = 0
			}
			updates["used_cpu_cores"] = newCPU
			global.APP_LOG.Debug("回收VM CPU资源", zap.Int("cpu", cpu))
		} else {
			global.APP_LOG.Debug("VM CPU未计入总量，无需回收", zap.Int("cpu", cpu))
		}

		if provider.VMLimitMemory {
			newMemory := provider.UsedMemory - memory
			if newMemory < 0 {
				newMemory = 0
			}
			updates["used_memory"] = newMemory
			global.APP_LOG.Debug("回收VM内存资源", zap.Int64("memory", memory))
		} else {
			global.APP_LOG.Debug("VM内存未计入总量，无需回收", zap.Int64("memory", memory))
		}

		if provider.VMLimitDisk {
			newDisk := provider.UsedDisk - disk
			if newDisk < 0 {
				newDisk = 0
			}
			updates["used_disk"] = newDisk
			global.APP_LOG.Debug("回收VM磁盘资源", zap.Int64("disk", disk))
		} else {
			global.APP_LOG.Debug("VM磁盘未计入总量，无需回收", zap.Int64("disk", disk))
		}

		newVMCount := provider.VMCount - 1
		if newVMCount < 0 {
			newVMCount = 0
		}
		updates["vm_count"] = newVMCount
	} else {
		// 容器：根据ContainerLimitXXX配置决定是否回收资源
		if provider.ContainerLimitCPU {
			newCPU := provider.UsedCPUCores - cpu
			if newCPU < 0 {
				newCPU = 0
			}
			updates["used_cpu_cores"] = newCPU
			global.APP_LOG.Debug("回收容器CPU资源", zap.Int("cpu", cpu))
		} else {
			global.APP_LOG.Debug("容器CPU未计入总量，无需回收", zap.Int("cpu", cpu))
		}

		if provider.ContainerLimitMemory {
			newMemory := provider.UsedMemory - memory
			if newMemory < 0 {
				newMemory = 0
			}
			updates["used_memory"] = newMemory
			global.APP_LOG.Debug("回收容器内存资源", zap.Int64("memory", memory))
		} else {
			global.APP_LOG.Debug("容器内存未计入总量，无需回收", zap.Int64("memory", memory))
		}

		if provider.ContainerLimitDisk {
			newDisk := provider.UsedDisk - disk
			if newDisk < 0 {
				newDisk = 0
			}
			updates["used_disk"] = newDisk
			global.APP_LOG.Debug("回收容器磁盘资源", zap.Int64("disk", disk))
		} else {
			global.APP_LOG.Debug("容器磁盘未计入总量，无需回收", zap.Int64("disk", disk))
		}

		newContainerCount := provider.ContainerCount - 1
		if newContainerCount < 0 {
			newContainerCount = 0
		}
		updates["container_count"] = newContainerCount
	}

	if err := tx.Model(&provider).Updates(updates).Error; err != nil {
		global.APP_LOG.Error("更新资源占用失败",
			zap.Uint("providerId", providerID),
			zap.String("error", utils.TruncateString(err.Error(), 200)))
		return err
	}

	global.APP_LOG.Info("资源释放成功",
		zap.Uint("providerId", providerID),
		zap.String("instanceType", instanceType),
		zap.Int("cpu", cpu),
		zap.Int64("memory", memory),
		zap.Int64("disk", disk))

	return nil
}

// ReleaseResources 释放资源（删除实例时调用）
func (s *ResourceService) ReleaseResources(providerID uint, instanceType string, cpu int, memory, disk int64) error {
	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return s.ReleaseResourcesInTx(tx, providerID, instanceType, cpu, memory, disk)
	})
}

// SyncProviderResources 同步Provider资源使用情况（基于实际实例计算）
func (s *ResourceService) SyncProviderResources(providerID uint) error {
	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		var provider providerModel.Provider
		if err := tx.First(&provider, providerID).Error; err != nil {
			return fmt.Errorf("Provider不存在: %v", err)
		}

		// 统计当前实例资源使用
		var stats dashboardModel.ResourceUsageStats

		// 统计虚拟机资源（排除deleted、deleting、failed状态）
		err := tx.Model(&providerModel.Instance{}).
			Where("provider_id = ? AND instance_type = ? AND status NOT IN (?)",
				providerID, "vm", []string{"deleted", "deleting", "failed"}).
			Select("COUNT(*) as vm_count, COALESCE(SUM(cpu), 0) as used_cpu_cores, COALESCE(SUM(memory), 0) as used_memory, COALESCE(SUM(disk), 0) as used_disk").
			Scan(&stats).Error
		if err != nil {
			return fmt.Errorf("统计虚拟机资源失败: %v", err)
		}

		vmCPU := stats.UsedCPUCores
		vmMemory := stats.UsedMemory
		vmDisk := stats.UsedDisk
		vmCount := stats.VMCount

		// 统计容器资源（排除deleted、deleting、failed状态）
		err = tx.Model(&providerModel.Instance{}).
			Where("provider_id = ? AND instance_type = ? AND status NOT IN (?)",
				providerID, "container", []string{"deleted", "deleting", "failed"}).
			Select("COUNT(*) as container_count, COALESCE(SUM(memory), 0) as used_memory, COALESCE(SUM(disk), 0) as used_disk").
			Scan(&stats).Error
		if err != nil {
			return fmt.Errorf("统计容器资源失败: %v", err)
		}

		containerMemory := stats.UsedMemory
		containerDisk := stats.UsedDisk
		containerCount := stats.ContainerCount

		// 设置缓存过期时间（5分钟后）
		cacheExpiry := time.Now().Add(5 * time.Minute)

		// 更新Provider资源统计
		totalInstances := int(vmCount + containerCount)
		availableCPU := provider.NodeCPUCores - int(vmCPU)
		if availableCPU < 0 {
			availableCPU = 0
		}
		availableMemory := provider.NodeMemoryTotal - (vmMemory + containerMemory)
		if availableMemory < 0 {
			availableMemory = 0
		}

		// 计算最大实例数限制（基于容器和虚拟机的单独限制）
		// 0 表示无限制，不应该被处理成有限制的情况
		maxInstances := 0
		hasLimit := false
		if provider.MaxContainerInstances > 0 {
			maxInstances += provider.MaxContainerInstances
			hasLimit = true
		}
		if provider.MaxVMInstances > 0 {
			maxInstances += provider.MaxVMInstances
			hasLimit = true
		}
		// 如果没有设置任何限制（都为0，表示无限制），使用默认值仅用于显示
		if !hasLimit {
			maxInstances = 999999 // 使用一个很大的数字表示无限制，
		}

		now := time.Now()
		updates := map[string]interface{}{
			"used_cpu_cores":      int(vmCPU), // 只有虚拟机占用CPU核心
			"used_memory":         vmMemory + containerMemory,
			"used_disk":           vmDisk + containerDisk,
			"vm_count":            int(vmCount),
			"container_count":     int(containerCount),
			"available_cpu_cores": availableCPU,
			"available_memory":    availableMemory,
			"used_instances":      totalInstances,
			"resource_synced":     true,
			"resource_synced_at":  &now,
			"count_cache_expiry":  &cacheExpiry, // 设置缓存过期时间
		}

		return tx.Model(&provider).Updates(updates).Error
	})
}

// GetProviderResourceStatus 获取Provider资源状态
func (s *ResourceService) GetProviderResourceStatus(providerID uint) (map[string]interface{}, error) {
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, providerID).Error; err != nil {
		return nil, fmt.Errorf("Provider不存在: %v", err)
	}

	status := map[string]interface{}{
		"providerID":            provider.ID,
		"name":                  provider.Name,
		"type":                  provider.Type,
		"architecture":          provider.Architecture,
		"containerEnabled":      provider.ContainerEnabled,
		"vmEnabled":             provider.VirtualMachineEnabled,
		"maxContainerInstances": provider.MaxContainerInstances,
		"maxVMInstances":        provider.MaxVMInstances,
		"resources": map[string]interface{}{
			"cpu": map[string]interface{}{
				"total":     provider.NodeCPUCores,
				"used":      provider.UsedCPUCores,
				"available": provider.NodeCPUCores - provider.UsedCPUCores,
			},
			"memory": map[string]interface{}{
				"total":     provider.NodeMemoryTotal,
				"used":      provider.UsedMemory,
				"available": provider.NodeMemoryTotal - provider.UsedMemory,
			},
			"disk": map[string]interface{}{
				"total":     provider.NodeDiskTotal,
				"used":      provider.UsedDisk,
				"available": provider.NodeDiskTotal - provider.UsedDisk,
			},
		},
		"instances": map[string]interface{}{
			"containers": provider.ContainerCount,
			"vms":        provider.VMCount,
			"total":      provider.ContainerCount + provider.VMCount,
		},
		"resourceSynced":   provider.ResourceSynced,
		"resourceSyncedAt": provider.ResourceSyncedAt,
	}

	return status, nil
}

// ValidateInstanceTypeSupport 验证Provider是否支持指定的实例类型
func (s *ResourceService) ValidateInstanceTypeSupport(providerID uint, instanceType string) error {
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, providerID).Error; err != nil {
		return fmt.Errorf("Provider不存在: %v", err)
	}

	switch instanceType {
	case "container":
		if !provider.ContainerEnabled {
			return errors.New("该节点不支持容器类型")
		}
	case "vm":
		if !provider.VirtualMachineEnabled {
			return errors.New("该节点不支持虚拟机类型")
		}
	default:
		return fmt.Errorf("不支持的实例类型: %s", instanceType)
	}

	return nil
}
