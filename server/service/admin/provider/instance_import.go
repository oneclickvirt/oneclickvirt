package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// ImportOptions 导入选项
type ImportOptions struct {
	ProviderID      uint     `json:"providerId"`      // Provider ID
	InstanceUUIDs   []string `json:"instanceUuids"`   // 要导入的实例UUID列表，为空表示全部导入
	AdminUserID     uint     `json:"adminUserId"`     // 管理员用户ID，导入的实例将分配给该用户
	AutoAdjustQuota bool     `json:"autoAdjustQuota"` // 是否自动调整quota
	MarkConflicts   bool     `json:"markConflicts"`   // 是否标记端口冲突
}

// ImportResult 导入结果
type ImportResult struct {
	ProviderID      uint                   `json:"providerId"`
	ProviderName    string                 `json:"providerName"`
	TotalAttempted  int                    `json:"totalAttempted"`   // 尝试导入的实例数
	SuccessCount    int                    `json:"successCount"`     // 成功导入数
	SkippedCount    int                    `json:"skippedCount"`     // 跳过数（已存在）
	FailedCount     int                    `json:"failedCount"`      // 失败数
	PortConflicts   int                    `json:"portConflicts"`    // 端口冲突数
	QuotaAdjusted   bool                   `json:"quotaAdjusted"`    // 是否调整了quota
	ImportedDetails []ImportedInstanceInfo `json:"importedDetails"`  // 导入详情
	Errors          []string               `json:"errors,omitempty"` // 错误列表
	ImportedAt      time.Time              `json:"importedAt"`
}

// ImportedInstanceInfo 导入的实例信息
type ImportedInstanceInfo struct {
	UUID            string `json:"uuid"`
	Name            string `json:"name"`
	InstanceID      uint   `json:"instanceId"`
	Status          string `json:"status"` // success, skipped, failed
	HasPortConflict bool   `json:"hasPortConflict"`
	ConflictDetail  string `json:"conflictDetail,omitempty"`
	Error           string `json:"error,omitempty"`
}

// ImportDiscoveredInstances 导入发现的实例
func (s *Service) ImportDiscoveredInstances(ctx context.Context, options ImportOptions) (*ImportResult, error) {
	global.APP_LOG.Info("开始导入实例",
		zap.Uint("providerId", options.ProviderID),
		zap.Int("uuidCount", len(options.InstanceUUIDs)),
		zap.Uint("adminUserId", options.AdminUserID),
		zap.Bool("autoAdjustQuota", options.AutoAdjustQuota))

	result := &ImportResult{
		ProviderID:      options.ProviderID,
		ImportedAt:      time.Now(),
		ImportedDetails: []ImportedInstanceInfo{},
		Errors:          []string{},
	}

	// 1. 获取Provider信息
	var providerInfo providerModel.Provider
	if err := global.APP_DB.First(&providerInfo, options.ProviderID).Error; err != nil {
		return nil, fmt.Errorf("获取Provider信息失败: %w", err)
	}
	result.ProviderName = providerInfo.Name

	// 2. 发现实例
	discoveryResult, err := s.DiscoverProviderInstances(ctx, options.ProviderID)
	if err != nil {
		return nil, fmt.Errorf("发现实例失败: %w", err)
	}

	// 3. 筛选要导入的实例
	var instancesToImport []provider.DiscoveredInstance
	if len(options.InstanceUUIDs) == 0 {
		// 导入所有新发现的实例
		instancesToImport = discoveryResult.DiscoveredInstances
	} else {
		// 仅导入指定UUID的实例
		uuidMap := make(map[string]bool)
		for _, uuid := range options.InstanceUUIDs {
			uuidMap[uuid] = true
		}
		for _, inst := range discoveryResult.DiscoveredInstances {
			if uuidMap[inst.UUID] || uuidMap[inst.Name] {
				instancesToImport = append(instancesToImport, inst)
			}
		}
	}

	result.TotalAttempted = len(instancesToImport)

	if result.TotalAttempted == 0 {
		global.APP_LOG.Info("没有需要导入的实例")
		return result, nil
	}

	// 4. 检查已存在的实例（避免重复导入）
	var existingInstances []providerModel.Instance
	if err := global.APP_DB.Where("provider_id = ?", options.ProviderID).
		Select("uuid", "name", "ssh_port", "port_range_start", "port_range_end").
		Find(&existingInstances).Error; err != nil {
		return nil, fmt.Errorf("查询已有实例失败: %w", err)
	}

	existingUUIDs := make(map[string]bool)
	existingNames := make(map[string]bool)
	for _, inst := range existingInstances {
		existingUUIDs[inst.UUID] = true
		existingNames[inst.Name] = true
	}

	// 5. 获取已占用的端口范围（用于检测冲突）
	occupiedPorts := make(map[int]bool)
	for _, inst := range existingInstances {
		if inst.SSHPort > 0 {
			occupiedPorts[inst.SSHPort] = true
		}
		for port := inst.PortRangeStart; port <= inst.PortRangeEnd; port++ {
			if port > 0 {
				occupiedPorts[port] = true
			}
		}
	}

	// 6. 获取管理员用户ID
	adminUserID := options.AdminUserID
	if adminUserID == 0 {
		// 查找第一个管理员用户
		var adminUser struct {
			ID uint
		}
		if err := global.APP_DB.Table("users").
			Where("is_admin = ?", true).
			Select("id").
			First(&adminUser).Error; err != nil {
			global.APP_LOG.Warn("未找到管理员用户，将使用用户ID 1", zap.Error(err))
			adminUserID = 1
		} else {
			adminUserID = adminUser.ID
		}
	}

	// 7. 批量导入实例（使用事务）
	err = global.APP_DB.Transaction(func(tx *gorm.DB) error {
		var totalCPU, totalMemory, totalDisk int64

		for _, discovered := range instancesToImport {
			// 检查是否已存在
			if existingUUIDs[discovered.UUID] || existingNames[discovered.Name] {
				result.SkippedCount++
				result.ImportedDetails = append(result.ImportedDetails, ImportedInstanceInfo{
					UUID:   discovered.UUID,
					Name:   discovered.Name,
					Status: "skipped",
					Error:  "实例已存在",
				})
				continue
			}

			// 创建Instance记录
			now := time.Now()
			importDetail := ImportedInstanceInfo{
				UUID:   discovered.UUID,
				Name:   discovered.Name,
				Status: "success",
			}

			// 检测端口冲突
			hasPortConflict := false
			conflictPorts := []int{}
			if discovered.SSHPort > 0 && occupiedPorts[discovered.SSHPort] {
				hasPortConflict = true
				conflictPorts = append(conflictPorts, discovered.SSHPort)
			}
			for _, port := range discovered.ExtraPorts {
				if occupiedPorts[port] {
					hasPortConflict = true
					conflictPorts = append(conflictPorts, port)
				}
			}

			var conflictDetail string
			if hasPortConflict {
				result.PortConflicts++
				conflictBytes, _ := json.Marshal(map[string]interface{}{
					"conflictPorts": conflictPorts,
					"sshPort":       discovered.SSHPort,
					"extraPorts":    discovered.ExtraPorts,
				})
				conflictDetail = string(conflictBytes)
				importDetail.HasPortConflict = true
				importDetail.ConflictDetail = fmt.Sprintf("端口冲突: %v", conflictPorts)
			}

			// 序列化原始数据
			rawDataBytes, _ := json.Marshal(discovered.RawData)

			instance := providerModel.Instance{
				UUID:         discovered.UUID,
				Name:         discovered.Name,
				Provider:     providerInfo.Name,
				ProviderID:   options.ProviderID,
				Status:       discovered.Status,
				Image:        discovered.Image,
				InstanceType: discovered.InstanceType,
				CPU:          discovered.CPU,
				Memory:       discovered.Memory,
				Disk:         discovered.Disk,
				PrivateIP:    discovered.PrivateIP,
				PublicIP:     discovered.PublicIP,
				IPv6Address:  discovered.IPv6Address,
				SSHPort:      discovered.SSHPort,
				OSType:       discovered.OSType,
				UserID:       adminUserID,
				// 导入相关字段
				IsImported:         true,
				ImportedAt:         &now,
				HasPortConflict:    hasPortConflict,
				PortConflictDetail: conflictDetail,
				DiscoveredData:     string(rawDataBytes),
			}

			if err := tx.Create(&instance).Error; err != nil {
				result.FailedCount++
				importDetail.Status = "failed"
				importDetail.Error = err.Error()
				result.Errors = append(result.Errors, fmt.Sprintf("导入实例 %s 失败: %v", discovered.Name, err))
				global.APP_LOG.Error("创建实例记录失败",
					zap.String("name", discovered.Name),
					zap.Error(err))
			} else {
				result.SuccessCount++
				importDetail.InstanceID = instance.ID

				// 累计资源占用
				totalCPU += int64(discovered.CPU)
				totalMemory += discovered.Memory
				totalDisk += discovered.Disk

				// 标记端口为已占用
				if discovered.SSHPort > 0 {
					occupiedPorts[discovered.SSHPort] = true
				}
				for _, port := range discovered.ExtraPorts {
					occupiedPorts[port] = true
				}

				global.APP_LOG.Info("实例导入成功",
					zap.String("name", discovered.Name),
					zap.Uint("instanceId", instance.ID),
					zap.Bool("hasPortConflict", hasPortConflict))
			}

			result.ImportedDetails = append(result.ImportedDetails, importDetail)
		}

		// 8. 自动调整Provider quota（如果启用）
		if options.AutoAdjustQuota && result.SuccessCount > 0 {
			// 计算当前使用量
			var currentUsage struct {
				UsedCPU    int64
				UsedMemory int64
				UsedDisk   int64
			}
			tx.Model(&providerModel.Instance{}).
				Where("provider_id = ?", options.ProviderID).
				Select("COALESCE(SUM(cpu), 0) as used_cpu, COALESCE(SUM(memory), 0) as used_memory, COALESCE(SUM(disk), 0) as used_disk").
				Scan(&currentUsage)

			// 更新Provider的quota和使用量
			updates := map[string]interface{}{
				"used_cpu_cores": currentUsage.UsedCPU,
				"used_memory":    currentUsage.UsedMemory,
				"used_disk":      currentUsage.UsedDisk,
			}

			// 如果当前使用量超过了quota，自动提升quota
			if currentUsage.UsedCPU > int64(providerInfo.NodeCPUCores) {
				updates["node_cpu_cores"] = int(currentUsage.UsedCPU)
			}
			if currentUsage.UsedMemory > providerInfo.NodeMemoryTotal {
				updates["node_memory_total"] = currentUsage.UsedMemory
			}
			if currentUsage.UsedDisk > providerInfo.NodeDiskTotal {
				updates["node_disk_total"] = currentUsage.UsedDisk
			}

			if err := tx.Model(&providerModel.Provider{}).
				Where("id = ?", options.ProviderID).
				Updates(updates).Error; err != nil {
				global.APP_LOG.Error("更新Provider资源配额失败", zap.Error(err))
				return err
			}

			result.QuotaAdjusted = true
			global.APP_LOG.Info("Provider资源配额已自动调整",
				zap.Uint("providerId", options.ProviderID),
				zap.Int64("usedCPU", currentUsage.UsedCPU),
				zap.Int64("usedMemory", currentUsage.UsedMemory),
				zap.Int64("usedDisk", currentUsage.UsedDisk))
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("导入事务失败: %w", err)
	}

	global.APP_LOG.Info("实例导入完成",
		zap.Uint("providerId", options.ProviderID),
		zap.Int("attempted", result.TotalAttempted),
		zap.Int("success", result.SuccessCount),
		zap.Int("skipped", result.SkippedCount),
		zap.Int("failed", result.FailedCount),
		zap.Int("portConflicts", result.PortConflicts),
		zap.Bool("quotaAdjusted", result.QuotaAdjusted))

	return result, nil
}

// RemoveImportedInstancesMark 移除实例的导入标记（用于将导入的实例转为正常管理）
func (s *Service) RemoveImportedInstancesMark(ctx context.Context, instanceIDs []uint) error {
	if len(instanceIDs) == 0 {
		return nil
	}

	err := global.APP_DB.Model(&providerModel.Instance{}).
		Where("id IN ?", instanceIDs).
		Updates(map[string]interface{}{
			"is_imported":          false,
			"has_port_conflict":    false,
			"port_conflict_detail": "",
		}).Error

	if err != nil {
		return fmt.Errorf("移除导入标记失败: %w", err)
	}

	global.APP_LOG.Info("已移除实例导入标记", zap.Int("count", len(instanceIDs)))
	return nil
}
