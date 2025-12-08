package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"oneclickvirt/constant"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	resourceModel "oneclickvirt/model/resource"
	systemModel "oneclickvirt/model/system"
	"oneclickvirt/service/auth"
	"oneclickvirt/service/resources"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// validateProviderImageCompatibility 验证Provider和Image的兼容性
func (s *Service) validateProviderImageCompatibility(provider *providerModel.Provider, image *systemModel.SystemImage) error {
	// 验证Provider类型是否支持该镜像
	supportedProviders := strings.Split(image.ProviderType, ",")
	providerSupported := false
	for _, supportedType := range supportedProviders {
		if strings.TrimSpace(supportedType) == provider.Type {
			providerSupported = true
			break
		}
	}

	if !providerSupported {
		return fmt.Errorf("所选镜像不支持Provider类型 %s，支持的类型: %s", provider.Type, image.ProviderType)
	}

	// 验证架构兼容性
	if provider.Architecture != "" && image.Architecture != "" && provider.Architecture != image.Architecture {
		return fmt.Errorf("架构不匹配：Provider架构为 %s，镜像架构为 %s", provider.Architecture, image.Architecture)
	}

	// 验证实例类型支持
	if image.InstanceType == "vm" && !provider.VirtualMachineEnabled {
		return errors.New("该Provider不支持虚拟机实例")
	}

	if image.InstanceType == "container" && !provider.ContainerEnabled {
		return errors.New("该Provider不支持容器实例")
	}

	return nil
}

// validateUserSpecPermissions 验证用户等级限制和资源规格权限
//
// 功能说明：
// 1. 获取用户全局等级限制（从 global.APP_CONFIG.Quota.LevelLimits）
// 2. 获取Provider节点等级限制（从 provider.LevelLimits）
// 3. 合并两者限制，取最小值作为最终限制
// 4. 验证所选规格（CPU、内存、磁盘、带宽）是否超过限制
//
// - 此函数在事务外执行，可以快速失败
// - 不验证实例数量限制（在事务内验证以防并发问题）
// - 管理员不受限制
func (s *Service) validateUserSpecPermissions(userID uint, providerID uint, cpuSpec *constant.CPUSpec, memorySpec *constant.MemorySpec, diskSpec *constant.DiskSpec, bandwidthSpec *constant.BandwidthSpec) error {
	// 获取用户权限信息
	permissionService := auth.PermissionService{}
	effective, err := permissionService.GetUserEffectivePermission(userID)
	if err != nil {
		return fmt.Errorf("获取用户权限失败: %v", err)
	}

	// 管理员可以使用所有规格
	if effective.EffectiveType == "admin" {
		return nil
	}

	// 获取用户全局等级限制
	levelLimits, exists := global.APP_CONFIG.Quota.LevelLimits[effective.EffectiveLevel]
	if !exists {
		return fmt.Errorf("用户等级 %d 没有配置资源限制", effective.EffectiveLevel)
	}

	// 如果指定了Provider，获取并合并Provider的节点等级限制（取最小值）
	if providerID > 0 {
		var provider providerModel.Provider
		if err := global.APP_DB.First(&provider, providerID).Error; err == nil && provider.LevelLimits != "" {
			// 解析Provider的LevelLimits JSON
			var allProviderLimits map[string]map[string]interface{}
			if err := json.Unmarshal([]byte(provider.LevelLimits), &allProviderLimits); err == nil {
				// 获取用户等级对应的Provider限制
				levelKey := fmt.Sprintf("%d", effective.EffectiveLevel)
				if providerLimits, ok := allProviderLimits[levelKey]; ok {
					// 合并Provider限制：如果Provider有限制且更严格，则使用Provider的限制
					if providerMaxResources, ok := providerLimits["max-resources"].(map[string]interface{}); ok {
						// 更新levelLimits为合并后的限制（取最小值）
						for key, value := range levelLimits.MaxResources {
							if providerValue, exists := providerMaxResources[key]; exists {
								// 比较并取最小值
								var currentVal, providerVal float64
								switch v := value.(type) {
								case float64:
									currentVal = v
								case int:
									currentVal = float64(v)
								}
								switch v := providerValue.(type) {
								case float64:
									providerVal = v
								case int:
									providerVal = float64(v)
								}
								if providerVal > 0 && providerVal < currentVal {
									levelLimits.MaxResources[key] = providerVal
								}
							}
						}
					}
				}
			}
		}
	}

	// 从配置中获取当前等级的最大资源限制
	maxResources, ok := levelLimits.MaxResources["cpu"]
	if ok {
		var maxCPU int
		switch v := maxResources.(type) {
		case float64:
			maxCPU = int(v)
		case int:
			maxCPU = v
		}

		if cpuSpec.Cores > maxCPU {
			return fmt.Errorf("您的等级不足以使用CPU规格 %s（需要 %d 核，您的等级 %d 最多支持 %d 核）",
				cpuSpec.Name, cpuSpec.Cores, effective.EffectiveLevel, maxCPU)
		}
	}

	// 内存规格验证
	maxResources, ok = levelLimits.MaxResources["memory"]
	if ok {
		var maxMemory int
		switch v := maxResources.(type) {
		case float64:
			maxMemory = int(v)
		case int:
			maxMemory = v
		}

		if memorySpec.SizeMB > maxMemory {
			return fmt.Errorf("您的等级不足以使用内存规格 %s（需要 %d MB，您的等级 %d 最多支持 %d MB）",
				memorySpec.Name, memorySpec.SizeMB, effective.EffectiveLevel, maxMemory)
		}
	}

	// 磁盘规格验证
	maxResources, ok = levelLimits.MaxResources["disk"]
	if ok {
		var maxDisk int
		switch v := maxResources.(type) {
		case float64:
			maxDisk = int(v)
		case int:
			maxDisk = v
		}

		if diskSpec.SizeMB > maxDisk {
			return fmt.Errorf("您的等级不足以使用磁盘规格 %s（需要 %d MB，您的等级 %d 最多支持 %d MB）",
				diskSpec.Name, diskSpec.SizeMB, effective.EffectiveLevel, maxDisk)
		}
	}

	// 带宽规格验证
	maxResources, ok = levelLimits.MaxResources["bandwidth"]
	if ok {
		var maxBandwidth int
		switch v := maxResources.(type) {
		case float64:
			maxBandwidth = int(v)
		case int:
			maxBandwidth = v
		}

		if bandwidthSpec.SpeedMbps > maxBandwidth {
			return fmt.Errorf("您的等级不足以使用带宽规格 %s（需要 %d Mbps，您的等级 %d 最多支持 %d Mbps）",
				bandwidthSpec.Name, bandwidthSpec.SpeedMbps, effective.EffectiveLevel, maxBandwidth)
		}
	}

	return nil
}

// validateInstanceMinimumRequirements 验证实例的最低硬件要求（统一验证）
func (s *Service) validateInstanceMinimumRequirements(image *systemModel.SystemImage, memorySpec *constant.MemorySpec, diskSpec *constant.DiskSpec, provider *providerModel.Provider) error {
	if image == nil {
		return fmt.Errorf("镜像信息不能为空")
	}

	// 使用镜像自身的最低硬件要求
	minMemoryMB := image.MinMemoryMB
	minDiskMB := image.MinDiskMB

	// 验证镜像是否设置了最低要求
	if minMemoryMB <= 0 || minDiskMB <= 0 {
		return fmt.Errorf("镜像未设置最低硬件要求，请联系管理员")
	}

	// 验证内存要求
	if memorySpec.SizeMB < minMemoryMB {
		instanceTypeDesc := "虚拟机"
		if image.InstanceType == "container" {
			instanceTypeDesc = "容器"
		}
		return fmt.Errorf("%s镜像 %s 最少需要%dMB内存，当前选择%dMB不足",
			instanceTypeDesc, image.Name, minMemoryMB, memorySpec.SizeMB)
	}

	// 验证磁盘要求
	if diskSpec.SizeMB < minDiskMB {
		instanceTypeDesc := "虚拟机"
		if image.InstanceType == "container" {
			instanceTypeDesc = "容器"
		}
		return fmt.Errorf("%s镜像 %s 最少需要%dMB硬盘，当前选择%dMB不足",
			instanceTypeDesc, image.Name, minDiskMB, diskSpec.SizeMB)
	}

	global.APP_LOG.Info("实例最低硬件要求验证通过",
		zap.String("imageName", image.Name),
		zap.String("instanceType", image.InstanceType),
		zap.String("providerType", provider.Type),
		zap.Int("requiredMemoryMB", minMemoryMB),
		zap.Int("requiredDiskMB", minDiskMB),
		zap.Int("selectedMemoryMB", memorySpec.SizeMB),
		zap.Int("selectedDiskMB", diskSpec.SizeMB))

	return nil
}

// validateCreateTaskPermissionsInTx 在事务中验证任务创建权限（三重验证）
// 保持事务和行锁直到验证完成，防止并发创建导致超出配额
func (s *Service) validateCreateTaskPermissionsInTx(tx *gorm.DB, userID uint, providerID uint, instanceType string,
	cpu int, memory int64, disk int64, bandwidth int) error {

	// 1. 用户配额验证（在同一事务中，保持行锁）
	quotaService := resources.NewQuotaService()
	quotaReq := resources.ResourceRequest{
		UserID:       userID,
		CPU:          cpu,
		Memory:       memory,
		Disk:         disk,
		Bandwidth:    bandwidth,
		InstanceType: instanceType,
		ProviderID:   providerID, //  Provider ID 用于节点级限制检查
	}

	// 直接调用事务内的验证方法，保持行锁
	quotaResult, err := quotaService.ValidateInTransaction(tx, quotaReq)
	if err != nil {
		return fmt.Errorf("用户配额验证失败: %v", err)
	}

	if !quotaResult.Allowed {
		return fmt.Errorf("用户配额不足: %s", quotaResult.Reason)
	}

	// 2. Provider资源验证（在同一事务中）
	resourceService := &resources.ResourceService{}
	resourceReq := resourceModel.ResourceCheckRequest{
		ProviderID:   providerID,
		InstanceType: instanceType,
		CPU:          cpu,
		Memory:       memory,
		Disk:         disk,
	}

	resourceResult, err := resourceService.CheckProviderResourcesWithTx(tx, resourceReq)
	if err != nil {
		return fmt.Errorf("Provider资源检查失败: %v", err)
	}

	if !resourceResult.Allowed {
		return fmt.Errorf("Provider资源不足: %s", resourceResult.Reason)
	}

	// 3. Provider并发任务数验证（在同一事务中）
	var provider providerModel.Provider
	if err := tx.First(&provider, providerID).Error; err != nil {
		return fmt.Errorf("查询Provider失败: %v", err)
	}

	// 检查Provider的并发任务限制
	if err := s.validateProviderConcurrencyLimitInTx(tx, providerID, provider.MaxConcurrentTasks, provider.AllowConcurrentTasks); err != nil {
		return fmt.Errorf("Provider并发限制验证失败: %v", err)
	}

	global.APP_LOG.Info("事务内任务创建三重验证通过",
		zap.Uint("userID", userID),
		zap.Uint("providerID", providerID),
		zap.String("instanceType", instanceType),
		zap.Int("cpu", cpu),
		zap.Int64("memory", memory),
		zap.Int64("disk", disk),
		zap.Int("bandwidth", bandwidth))

	return nil
}

// validateCreateTaskPermissions 验证任务创建权限（三重验证）
func (s *Service) validateCreateTaskPermissions(userID uint, providerID uint, instanceType string,
	cpu int, memory int64, disk int64, bandwidth int) error {

	// 1. 用户配额验证
	quotaService := resources.NewQuotaService()
	quotaReq := resources.ResourceRequest{
		UserID:       userID,
		CPU:          cpu,
		Memory:       memory,
		Disk:         disk,
		Bandwidth:    bandwidth,
		InstanceType: instanceType,
		ProviderID:   providerID, //  Provider ID 用于节点级限制检查
	}

	quotaResult, err := quotaService.ValidateInstanceCreation(quotaReq)
	if err != nil {
		return fmt.Errorf("用户配额验证失败: %v", err)
	}

	if !quotaResult.Allowed {
		return fmt.Errorf("用户配额不足: %s", quotaResult.Reason)
	}

	// 2. Provider资源验证
	resourceService := &resources.ResourceService{}
	resourceReq := resourceModel.ResourceCheckRequest{
		ProviderID:   providerID,
		InstanceType: instanceType,
		CPU:          cpu,
		Memory:       memory,
		Disk:         disk,
	}

	resourceResult, err := resourceService.CheckProviderResources(resourceReq)
	if err != nil {
		return fmt.Errorf("Provider资源检查失败: %v", err)
	}

	if !resourceResult.Allowed {
		return fmt.Errorf("Provider资源不足: %s", resourceResult.Reason)
	}

	// 3. Provider并发任务数验证
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, providerID).Error; err != nil {
		return fmt.Errorf("查询Provider失败: %v", err)
	}

	// 检查Provider的并发任务限制
	if err := s.validateProviderConcurrencyLimit(providerID, provider.MaxConcurrentTasks, provider.AllowConcurrentTasks); err != nil {
		return fmt.Errorf("Provider并发限制验证失败: %v", err)
	}

	global.APP_LOG.Info("任务创建三重验证通过",
		zap.Uint("userID", userID),
		zap.Uint("providerID", providerID),
		zap.String("instanceType", instanceType),
		zap.Int("cpu", cpu),
		zap.Int64("memory", memory),
		zap.Int64("disk", disk),
		zap.Int("bandwidth", bandwidth))

	return nil
}

// validateProviderConcurrencyLimitInTx 在事务中验证Provider并发任务限制
func (s *Service) validateProviderConcurrencyLimitInTx(tx *gorm.DB, providerID uint, maxConcurrentTasks int, allowConcurrentTasks bool) error {
	// 分别统计running和pending任务数
	var runningTaskCount int64
	var pendingTaskCount int64

	err := tx.Model(&adminModel.Task{}).
		Where("provider_id = ? AND status = 'running'", providerID).
		Count(&runningTaskCount).Error
	if err != nil {
		return fmt.Errorf("查询Provider当前running任务数失败: %v", err)
	}

	err = tx.Model(&adminModel.Task{}).
		Where("provider_id = ? AND status = 'pending'", providerID).
		Count(&pendingTaskCount).Error
	if err != nil {
		return fmt.Errorf("查询Provider当前pending任务数失败: %v", err)
	}

	// 确定最大允许并发执行任务数
	var maxRunningTasks int
	if allowConcurrentTasks {
		maxRunningTasks = maxConcurrentTasks
		if maxRunningTasks <= 0 {
			maxRunningTasks = 1 // 默认值
		}
	} else {
		maxRunningTasks = 1 // 串行模式只允许1个运行中的任务
	}

	// pending任务可以排队，可无限制排队

	global.APP_LOG.Info("事务内Provider并发验证通过",
		zap.Uint("providerID", providerID),
		zap.Int64("runningTasks", runningTaskCount),
		zap.Int64("pendingTasks", pendingTaskCount),
		zap.Int("maxRunningTasks", maxRunningTasks),
		zap.Bool("allowConcurrent", allowConcurrentTasks))

	return nil
}

// validateProviderConcurrencyLimit 验证Provider并发任务限制
func (s *Service) validateProviderConcurrencyLimit(providerID uint, maxConcurrentTasks int, allowConcurrentTasks bool) error {
	// 分别统计running和pending任务数
	var runningTaskCount int64
	var pendingTaskCount int64

	err := global.APP_DB.Model(&adminModel.Task{}).
		Where("provider_id = ? AND status = 'running'", providerID).
		Count(&runningTaskCount).Error
	if err != nil {
		return fmt.Errorf("查询Provider当前running任务数失败: %v", err)
	}

	err = global.APP_DB.Model(&adminModel.Task{}).
		Where("provider_id = ? AND status = 'pending'", providerID).
		Count(&pendingTaskCount).Error
	if err != nil {
		return fmt.Errorf("查询Provider当前pending任务数失败: %v", err)
	}

	// 确定最大允许并发执行任务数
	var maxRunningTasks int
	if allowConcurrentTasks {
		maxRunningTasks = maxConcurrentTasks
		if maxRunningTasks <= 0 {
			maxRunningTasks = 1 // 默认值
		}
	} else {
		maxRunningTasks = 1 // 串行模式只允许1个运行中的任务
	}

	// pending任务可以排队，可无限制排队

	global.APP_LOG.Info("Provider并发验证通过",
		zap.Uint("providerID", providerID),
		zap.Int64("runningTasks", runningTaskCount),
		zap.Int64("pendingTasks", pendingTaskCount),
		zap.Int("maxRunningTasks", maxRunningTasks),
		zap.Bool("allowConcurrent", allowConcurrentTasks))

	return nil
}
