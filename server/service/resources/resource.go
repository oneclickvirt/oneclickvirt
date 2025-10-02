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

	result := &resource.ResourceCheckResult{
		Allowed: true,
	}

	// 检查Provider是否支持指定类型
	if req.InstanceType == "container" && !provider.ContainerEnabled {
		result.Allowed = false
		result.Reason = "该节点不支持容器类型"
		return result, nil
	}

	if req.InstanceType == "vm" && !provider.VirtualMachineEnabled {
		result.Allowed = false
		result.Reason = "该节点不支持虚拟机类型"
		return result, nil
	}

	// 计算可用资源
	availableCPU := provider.NodeCPUCores - provider.UsedCPUCores
	availableMemory := provider.NodeMemoryTotal - provider.UsedMemory
	availableDisk := provider.NodeDiskTotal - provider.UsedDisk

	result.AvailableCPU = availableCPU
	result.AvailableMemory = availableMemory
	result.AvailableDisk = availableDisk

	// 对于容器，CPU可以超开（共享CPU），只检查实例数量限制
	if req.InstanceType == "container" {
		// 检查容器数量限制
		if provider.MaxContainerInstances > 0 && provider.ContainerCount >= provider.MaxContainerInstances {
			result.Allowed = false
			result.Reason = fmt.Sprintf("容器数量已达上限：%d/%d", provider.ContainerCount, provider.MaxContainerInstances)
			return result, nil
		}
		// 容器不进行CPU核心数限制
	} else {
		// 对于虚拟机，严格检查CPU核心数（独享）
		if req.CPU > availableCPU {
			result.Allowed = false
			result.Reason = fmt.Sprintf("CPU资源不足：需要 %d 核，可用 %d 核", req.CPU, availableCPU)
			return result, nil
		}

		// 检查虚拟机数量限制
		if provider.MaxVMInstances > 0 && provider.VMCount >= provider.MaxVMInstances {
			result.Allowed = false
			result.Reason = fmt.Sprintf("虚拟机数量已达上限：%d/%d", provider.VMCount, provider.MaxVMInstances)
			return result, nil
		}
	}

	// 内存严格校验（不可超开）
	if req.Memory > availableMemory {
		result.Allowed = false
		result.Reason = fmt.Sprintf("内存资源不足：需要 %d MB，可用 %d MB", req.Memory, availableMemory)
		return result, nil
	}

	// 磁盘严格校验（不可超开）
	if req.Disk > availableDisk {
		result.Allowed = false
		result.Reason = fmt.Sprintf("磁盘资源不足：需要 %d MB，可用 %d MB", req.Disk, availableDisk)
		return result, nil
	}

	return result, nil
}

// AllocateResourcesInTx 在事务中分配资源（不创建新事务，使用悲观锁）
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

	// 更新资源占用
	updates := map[string]interface{}{
		"used_memory": provider.UsedMemory + memory,
		"used_disk":   provider.UsedDisk + disk,
		"updated_at":  time.Now(),
	}

	// 只有虚拟机才占用CPU核心
	if instanceType == "vm" {
		updates["used_cpu_cores"] = provider.UsedCPUCores + cpu
		updates["vm_count"] = provider.VMCount + 1
	} else {
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

	// 更新资源占用
	updates := map[string]interface{}{
		"used_memory": provider.UsedMemory - memory,
		"used_disk":   provider.UsedDisk - disk,
		"updated_at":  time.Now(),
	}

	// 只有虚拟机才释放CPU核心
	if instanceType == "vm" {
		updates["used_cpu_cores"] = provider.UsedCPUCores - cpu
		updates["vm_count"] = provider.VMCount - 1
	} else {
		updates["container_count"] = provider.ContainerCount - 1
	}

	// 确保值不为负数
	if updates["used_memory"].(int64) < 0 {
		updates["used_memory"] = int64(0)
	}
	if updates["used_disk"].(int64) < 0 {
		updates["used_disk"] = int64(0)
	}
	if instanceType == "vm" && updates["used_cpu_cores"].(int) < 0 {
		updates["used_cpu_cores"] = 0
	}
	if (instanceType == "vm" && updates["vm_count"].(int) < 0) || (instanceType == "container" && updates["container_count"].(int) < 0) {
		if instanceType == "vm" {
			updates["vm_count"] = 0
		} else {
			updates["container_count"] = 0
		}
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

		// 统计虚拟机资源
		err := tx.Model(&providerModel.Instance{}).
			Where("provider_id = ? AND instance_type = ? AND status NOT IN (?)",
				providerID, "vm", []string{"deleted", "deleting"}).
			Select("COUNT(*) as vm_count, COALESCE(SUM(cpu), 0) as used_cpu_cores, COALESCE(SUM(memory), 0) as used_memory, COALESCE(SUM(disk), 0) as used_disk").
			Scan(&stats).Error
		if err != nil {
			return fmt.Errorf("统计虚拟机资源失败: %v", err)
		}

		vmCPU := stats.UsedCPUCores
		vmMemory := stats.UsedMemory
		vmDisk := stats.UsedDisk
		vmCount := stats.VMCount

		// 统计容器资源
		err = tx.Model(&providerModel.Instance{}).
			Where("provider_id = ? AND instance_type = ? AND status NOT IN (?)",
				providerID, "container", []string{"deleted", "deleting"}).
			Select("COUNT(*) as container_count, COALESCE(SUM(memory), 0) as used_memory, COALESCE(SUM(disk), 0) as used_disk").
			Scan(&stats).Error
		if err != nil {
			return fmt.Errorf("统计容器资源失败: %v", err)
		}

		containerMemory := stats.UsedMemory
		containerDisk := stats.UsedDisk
		containerCount := stats.ContainerCount

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
		maxInstances := 0
		if provider.MaxContainerInstances > 0 {
			maxInstances += provider.MaxContainerInstances
		}
		if provider.MaxVMInstances > 0 {
			maxInstances += provider.MaxVMInstances
		}
		// 如果没有设置任何限制，使用默认值
		if maxInstances == 0 {
			maxInstances = 3 // 默认值为3个实例
		}

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
			"resource_synced_at":  "NOW()",
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
