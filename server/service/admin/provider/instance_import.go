package provider

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"sync"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/common"
	providerModel "oneclickvirt/model/provider"
	systemModel "oneclickvirt/model/system"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var instanceImportLocks sync.Map

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

type duplicateDiscoveredInstance struct {
	Instance provider.DiscoveredInstance
	Reason   string
}

// ImportDiscoveredInstances 导入发现的实例
func (s *Service) ImportDiscoveredInstances(ctx context.Context, options ImportOptions) (*ImportResult, error) {
	lockValue, _ := instanceImportLocks.LoadOrStore(options.ProviderID, make(chan struct{}, 1))
	providerLock := lockValue.(chan struct{})
	select {
	case providerLock <- struct{}{}:
		defer func() { <-providerLock }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

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
		// 仅导入指定身份。UUID是首选；兼容远端ID和名称，重名时按稳定
		// 排序选择一个，其余重复资源在后续批次去重中记为 skipped。
		var selectionErr error
		instancesToImport, selectionErr = selectDiscoveredInstances(discoveryResult.DiscoveredInstances, options.InstanceUUIDs)
		if selectionErr != nil {
			return nil, common.NewError(common.CodeValidationError, selectionErr.Error())
		}
	}

	result.TotalAttempted = len(instancesToImport)

	if result.TotalAttempted == 0 {
		if len(options.InstanceUUIDs) == 0 {
			return result, nil
		}
		return nil, common.NewError(common.CodeValidationError, "指定的实例未在当前发现结果中找到")
	}

	// 发现器、远端脏数据或兼容 API 可能返回重复行。按稳定顺序仅保留一个
	// 实例；名称、远端身份、私网地址、IPv6 或宿主机端口任一冲突时，
	// 后续行只记为 skipped，避免数据库唯一约束让整批事务回滚。
	var duplicates []duplicateDiscoveredInstance
	instancesToImport, duplicates = deduplicateDiscoveredInstances(instancesToImport)
	for _, duplicate := range duplicates {
		result.SkippedCount++
		result.ImportedDetails = append(result.ImportedDetails, ImportedInstanceInfo{
			UUID: duplicate.Instance.UUID, Name: duplicate.Instance.Name,
			Status: "skipped", Error: duplicate.Reason,
		})
	}

	// 4. 检查已存在的实例（避免重复导入）
	var existingInstances []providerModel.Instance
	if err := global.APP_DB.Where("provider_id = ?", options.ProviderID).
		Select("id", "uuid", "name", "provider_vm_id", "private_ip", "ipv6_address", "ssh_port", "port_range_start", "port_range_end").
		Find(&existingInstances).Error; err != nil {
		return nil, fmt.Errorf("查询已有实例失败: %w", err)
	}
	// UUID is globally unique, including soft-deleted history rows. Query the
	// exact database constraint scope before starting the transaction: active or
	// cross-Provider owners remain deterministic skips, while a tombstone owned
	// by this Provider can be restored if the remote instance still exists.
	existingByUUID := make(map[string]providerModel.Instance)
	candidateUUIDs := make([]string, 0, len(instancesToImport))
	for _, discovered := range instancesToImport {
		if normalized := normalizeImportedInstanceUUID(discovered.UUID); normalized != "" {
			candidateUUIDs = append(candidateUUIDs, normalized)
		}
	}
	if len(candidateUUIDs) > 0 {
		var uuidOwners []providerModel.Instance
		if err := global.APP_DB.Unscoped().
			Select("id", "uuid", "name", "provider_id", "deleted_at").
			Where("LOWER(uuid) IN ?", candidateUUIDs).
			Find(&uuidOwners).Error; err != nil {
			return nil, fmt.Errorf("查询已有实例UUID失败: %w", err)
		}
		for _, owner := range uuidOwners {
			existingByUUID[normalizeImportedInstanceUUID(owner.UUID)] = owner
		}
	}

	// 5. 获取已占用的端口范围（用于检测冲突）
	occupiedPorts := make(map[int]bool)
	instancesWithPortRows := make(map[uint]bool)
	var existingPorts []providerModel.Port
	if err := global.APP_DB.Where("provider_id = ?", options.ProviderID).
		Select("instance_id", "host_port", "host_port_end").Find(&existingPorts).Error; err != nil {
		return nil, fmt.Errorf("查询已有端口映射失败: %w", err)
	}
	for _, port := range existingPorts {
		instancesWithPortRows[port.InstanceID] = true
		end := port.HostPortEnd
		if end < port.HostPort {
			end = port.HostPort
		}
		for value := port.HostPort; value <= end && value <= 65535; value++ {
			if validDiscoveredPort(value) {
				occupiedPorts[value] = true
			}
		}
	}
	for _, inst := range existingInstances {
		// 22 通常是实例自身独立 IP 上的客体端口，不是 Provider 宿主机端口。
		// 只有非 22 的 SSH 转发端口或显式 Port 行才参与全局占用判断。
		if inst.SSHPort > 0 && inst.SSHPort != 22 {
			occupiedPorts[inst.SSHPort] = true
		}
		// Legacy rows may only carry an allocated range and have no normalized
		// Port rows. Preserve that reservation without treating gaps between
		// modern sparse mappings as occupied.
		if !instancesWithPortRows[inst.ID] {
			for port := inst.PortRangeStart; port <= inst.PortRangeEnd && port <= 65535; port++ {
				if port > 0 {
					occupiedPorts[port] = true
				}
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
			Where("user_type IN ?", []string{"admin", "super_admin"}).
			Select("id").
			First(&adminUser).Error; err != nil {
			return nil, common.NewError(common.CodeValidationError, "未找到可接收导入实例的管理员用户")
		} else {
			adminUserID = adminUser.ID
		}
	} else {
		var adminUser struct {
			ID uint
		}
		if err := global.APP_DB.Table("users").Where("id = ?", adminUserID).Select("id").First(&adminUser).Error; err != nil {
			return nil, common.NewError(common.CodeValidationError, "指定的实例所有者不存在")
		}
	}

	// 7. 批量导入实例（使用事务）
	err = global.APP_DB.Transaction(func(tx *gorm.DB) error {
		for importIndex, discovered := range instancesToImport {
			normalizedUUID := normalizeImportedInstanceUUID(discovered.UUID)
			var historicalOwner *providerModel.Instance
			if owner, exists := existingByUUID[normalizedUUID]; exists {
				if canRestoreHistoricalImportedInstance(owner, options.ProviderID) {
					ownerCopy := owner
					historicalOwner = &ownerCopy
				} else {
					result.SkippedCount++
					reason := fmt.Sprintf("实例UUID已由实例 %s(ID:%d) 占用，已保留现有实例", owner.Name, owner.ID)
					if owner.DeletedAt.Valid {
						reason = fmt.Sprintf("实例UUID已由其他Provider的历史实例 %s(ID:%d) 占用，已跳过重复资源", owner.Name, owner.ID)
					}
					result.ImportedDetails = append(result.ImportedDetails, ImportedInstanceInfo{
						UUID: discovered.UUID, Name: discovered.Name, InstanceID: owner.ID, Status: "skipped", Error: reason,
					})
					continue
				}
			}
			// 检查是否已存在
			if hasMatchingDBInstance(providerInfo.Type, discovered, existingInstances) {
				result.SkippedCount++
				result.ImportedDetails = append(result.ImportedDetails, ImportedInstanceInfo{
					UUID:   discovered.UUID,
					Name:   discovered.Name,
					Status: "skipped",
					Error:  "实例已存在",
				})
				continue
			}
			if hasConflictingInstanceName(providerInfo.Type, discovered, existingInstances) {
				result.SkippedCount++
				result.ImportedDetails = append(result.ImportedDetails, ImportedInstanceInfo{
					UUID: discovered.UUID, Name: discovered.Name, Status: "skipped", Error: "同一Provider已存在同名实例，已保留现有实例",
				})
				continue
			}
			if reason := conflictingExistingInstanceResource(discovered, existingInstances); reason != "" {
				result.SkippedCount++
				result.ImportedDetails = append(result.ImportedDetails, ImportedInstanceInfo{
					UUID: discovered.UUID, Name: discovered.Name, Status: "skipped", Error: reason,
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
			conflictPortSet := make(map[int]bool)
			if discovered.SSHPort > 0 && discovered.SSHPort != 22 && occupiedPorts[discovered.SSHPort] {
				hasPortConflict = true
				conflictPortSet[discovered.SSHPort] = true
			}
			seenMappingHosts := make(map[int]provider.DiscoveredPortMapping)
			for _, mapping := range discovered.PortMappings {
				port := mapping.HostPort
				if occupiedPorts[port] {
					hasPortConflict = true
					conflictPortSet[port] = true
				}
				if previous, exists := seenMappingHosts[port]; exists && (previous.GuestPort != mapping.GuestPort || previous.Protocol != mapping.Protocol) {
					hasPortConflict = true
					conflictPortSet[port] = true
				} else {
					seenMappingHosts[port] = mapping
				}
			}
			for _, port := range discovered.ExtraPorts {
				if validDiscoveredPort(port) && occupiedPorts[port] {
					hasPortConflict = true
					conflictPortSet[port] = true
				}
			}
			conflictPorts := sortedPortSet(conflictPortSet)

			if hasPortConflict {
				result.PortConflicts++
				result.SkippedCount++
				importDetail.Status = "skipped"
				importDetail.Error = fmt.Sprintf("宿主机端口与已选/已纳管实例重复，已保留先出现的实例: %v", conflictPorts)
				importDetail.HasPortConflict = options.MarkConflicts
				importDetail.ConflictDetail = fmt.Sprintf("端口冲突: %v", conflictPorts)
				result.ImportedDetails = append(result.ImportedDetails, importDetail)
				continue
			}

			// 序列化原始数据
			rawData, rawDataErr := marshalSanitizedDiscoveredData(discovered.RawData)
			if rawDataErr != nil {
				result.FailedCount++
				importDetail.Status = "failed"
				importDetail.Error = "发现数据无法安全序列化"
				result.Errors = append(result.Errors, fmt.Sprintf("导入实例 %s 失败: %v", discovered.Name, rawDataErr))
				result.ImportedDetails = append(result.ImportedDetails, importDetail)
				continue
			}
			supportsAccelerators := utils.SupportsLXDContainerOptions(providerInfo.Type, discovered.InstanceType)
			gpuEnabled := discovered.GpuEnabled && supportsAccelerators
			gpuDeviceIDs := ""
			npuEnabled := discovered.NpuEnabled && supportsAccelerators
			npuDeviceIDs := ""
			if supportsAccelerators {
				gpuDeviceIDs = discovered.GpuDeviceIds
				npuDeviceIDs = discovered.NpuDeviceIds
			}

			// 根据Provider的资源限制配置，决定导入的实例是否计入资源占用
			// 对于不限制的资源类型，将对应值置零，避免影响资源使用量计算
			importCPU := discovered.CPU
			importMemory := discovered.Memory
			importDisk := discovered.Disk
			isContainer := discovered.InstanceType == "container"
			if isContainer {
				if !providerInfo.ContainerLimitCPU {
					importCPU = 0
				}
				if !providerInfo.ContainerLimitMemory {
					importMemory = 0
				}
				if !providerInfo.ContainerLimitDisk {
					importDisk = 0
				}
			} else {
				// VM instance
				if !providerInfo.VMLimitCPU {
					importCPU = 0
				}
				if !providerInfo.VMLimitMemory {
					importMemory = 0
				}
				if !providerInfo.VMLimitDisk {
					importDisk = 0
				}
			}

			instance := providerModel.Instance{
				UUID:         normalizeImportedInstanceUUID(discovered.UUID),
				Name:         discovered.Name,
				Provider:     providerInfo.Name,
				ProviderID:   options.ProviderID,
				Status:       discovered.Status,
				Image:        discovered.Image,
				InstanceType: discovered.InstanceType,
				CPU:          importCPU,
				Memory:       importMemory,
				Disk:         importDisk,
				PrivateIP:    discovered.PrivateIP,
				PublicIP:     discovered.PublicIP,
				IPv6Address:  discovered.IPv6Address,
				SSHPort:      discovered.SSHPort,
				OSType:       discovered.OSType,
				UserID:       adminUserID,
				ProviderVMID: discovered.ProviderInstanceID,
				// 导入相关字段
				IsImported:         true,
				ImportedAt:         &now,
				HasPortConflict:    false,
				PortConflictDetail: "",
				DiscoveredData:     rawData,
				// GPU/NPU配置
				GpuEnabled:   gpuEnabled,
				GpuDeviceIds: gpuDeviceIDs,
				NpuEnabled:   npuEnabled,
				NpuDeviceIds: npuDeviceIDs,
				NetworkType:  providerInfo.NetworkType, // 继承Provider的网络类型
			}

			// Isolate every discovered instance with a savepoint. A concurrent
			// name/UUID/port winner or a malformed auxiliary record must reject
			// only this item, never roll back unrelated imports in the same batch.
			savepoint := fmt.Sprintf("instance_import_%d", importIndex)
			if savepointErr := tx.SavePoint(savepoint).Error; savepointErr != nil {
				return fmt.Errorf("创建实例导入保存点失败: %w", savepointErr)
			}
			var persistResult *gorm.DB
			if historicalOwner != nil {
				// The remote instance still exists but its controller row was soft
				// deleted. Reuse that globally unique UUID/ID and refresh it as a new
				// import. The savepoint restores the tombstone if any auxiliary write
				// fails later in this item.
				instance.ID = historicalOwner.ID
				persistResult = restoreHistoricalImportedInstance(tx, &instance, historicalOwner.ID)
			} else {
				persistResult = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&instance)
			}
			if persistResult.Error != nil {
				if rollbackErr := rollbackImportedInstance(tx, savepoint); rollbackErr != nil {
					return rollbackErr
				}
				if isDuplicateImportResourceError(persistResult.Error) {
					result.SkippedCount++
					importDetail.Status = "skipped"
					importDetail.Error = "数据库中已有相同名称或实例UUID，已保留先写入的实例"
				} else {
					result.FailedCount++
					importDetail.Status = "failed"
					importDetail.Error = fmt.Sprintf("写入实例记录失败: %v", persistResult.Error)
					result.Errors = append(result.Errors, fmt.Sprintf("导入实例 %s 失败: %v", discovered.Name, persistResult.Error))
				}
				result.ImportedDetails = append(result.ImportedDetails, importDetail)
				continue
			} else if persistResult.RowsAffected == 0 || instance.ID == 0 {
				// A different controller process may have imported the same UUID/name
				// or restored the same tombstone after the preflight query. The database
				// keeps the first winner while the rest of this batch continues normally.
				result.SkippedCount++
				importDetail.Status = "skipped"
				importDetail.Error = "数据库中已有相同名称或实例UUID，已保留先写入的实例"
				result.ImportedDetails = append(result.ImportedDetails, importDetail)
				continue
			} else {
				// 创建端口映射记录
				if err := s.createImportedPortMappings(tx, &discovered, instance.ID, options.ProviderID, conflictPortSet); err != nil {
					if rollbackErr := rollbackImportedInstance(tx, savepoint); rollbackErr != nil {
						return rollbackErr
					}
					importDetail.Error = fmt.Sprintf("关联端口资源写入失败: %v", err)
					if isDuplicateImportResourceError(err) {
						result.SkippedCount++
						importDetail.Status = "skipped"
						importDetail.Error = "关联端口已由另一实例占用，已保留先写入的实例"
					} else {
						result.FailedCount++
						importDetail.Status = "failed"
						result.Errors = append(result.Errors, fmt.Sprintf("导入实例 %s 失败: %v", discovered.Name, err))
					}
					result.ImportedDetails = append(result.ImportedDetails, importDetail)
					continue
				}

				// 为导入的实例自动生成 ORI 前缀兑换码（一对一绑定，状态直接为 pending_use）
				const oriCharset = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
				var oriCode string
				var redemptionErr error
				for oriAttempt := 0; oriAttempt < 10; oriAttempt++ {
					buf := make([]byte, 13)
					for j := range buf {
						n, randErr := rand.Int(rand.Reader, big.NewInt(int64(len(oriCharset))))
						if randErr != nil {
							redemptionErr = fmt.Errorf("生成ORI兑换码随机值失败: %w", randErr)
							break
						}
						buf[j] = oriCharset[n.Int64()]
					}
					if redemptionErr != nil {
						break
					}
					candidate := "ORI" + string(buf)
					var existingOriCode systemModel.RedemptionCode
					lookupErr := tx.Where("code = ?", candidate).First(&existingOriCode).Error
					if errors.Is(lookupErr, gorm.ErrRecordNotFound) {
						oriCode = candidate
						break
					} else if lookupErr != nil {
						redemptionErr = fmt.Errorf("检查ORI兑换码唯一性失败: %w", lookupErr)
						break
					}
				}
				if redemptionErr == nil && oriCode != "" {
					instanceType := discovered.InstanceType
					if instanceType == "" {
						instanceType = "container"
					}
					oriRedemptionCode := systemModel.RedemptionCode{
						Code:         oriCode,
						Status:       systemModel.RedemptionStatusPendingUse,
						ProviderID:   options.ProviderID,
						ProviderName: providerInfo.Name,
						InstanceType: instanceType,
						InstanceID:   &instance.ID,
						CreatedBy:    adminUserID,
						Remark:       "节点导入自动生成",
						GpuEnabled:   gpuEnabled,
						GpuDeviceIds: gpuDeviceIDs,
					}
					if createCodeErr := tx.Create(&oriRedemptionCode).Error; createCodeErr != nil {
						redemptionErr = fmt.Errorf("创建ORI兑换码失败: %w", createCodeErr)
					} else {
						global.APP_LOG.Debug("为导入实例自动生成ORI兑换码",
							zap.Uint("instanceId", instance.ID),
							zap.Uint("redemptionCodeID", oriRedemptionCode.ID))
					}
				} else if redemptionErr == nil {
					redemptionErr = fmt.Errorf("无法生成唯一ORI兑换码")
				}
				if redemptionErr != nil {
					if rollbackErr := rollbackImportedInstance(tx, savepoint); rollbackErr != nil {
						return rollbackErr
					}
					result.FailedCount++
					importDetail.Status = "failed"
					importDetail.Error = redemptionErr.Error()
					result.Errors = append(result.Errors, fmt.Sprintf("导入实例 %s 失败: %v", discovered.Name, redemptionErr))
					result.ImportedDetails = append(result.ImportedDetails, importDetail)
					continue
				}

				result.SuccessCount++
				importDetail.InstanceID = instance.ID
				// 立即纳入本次导入的重复检查，防止异常的重复发现行
				// 在同一事务中创建两条记录。
				existingInstances = append(existingInstances, instance)
				existingByUUID[normalizeImportedInstanceUUID(instance.UUID)] = instance

				// 标记端口为已占用
				if discovered.SSHPort > 0 && !conflictPortSet[discovered.SSHPort] {
					occupiedPorts[discovered.SSHPort] = true
				}
				for _, mapping := range discovered.PortMappings {
					if !conflictPortSet[mapping.HostPort] {
						occupiedPorts[mapping.HostPort] = true
					}
				}
				for _, port := range discovered.ExtraPorts {
					if validDiscoveredPort(port) {
						occupiedPorts[port] = true
					}
				}

				global.APP_LOG.Debug("实例导入成功",
					zap.String("name", discovered.Name),
					zap.Uint("instanceId", instance.ID),
					zap.Bool("restoredHistoricalRecord", historicalOwner != nil),
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
			if err := tx.Model(&providerModel.Instance{}).
				Where("provider_id = ?", options.ProviderID).
				Select("COALESCE(SUM(cpu), 0) as used_cpu, COALESCE(SUM(memory), 0) as used_memory, COALESCE(SUM(disk), 0) as used_disk").
				Scan(&currentUsage).Error; err != nil {
				return fmt.Errorf("统计Provider资源使用量失败: %w", err)
			}

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

// createImportedPortMappings 为导入的实例创建端口映射记录
func (s *Service) createImportedPortMappings(tx *gorm.DB, discovered *provider.DiscoveredInstance, instanceID, providerID uint, skipHostPorts map[int]bool) error {
	if len(discovered.PortMappings) == 0 && discovered.SSHPort <= 0 {
		return nil
	}

	var ports []providerModel.Port
	var portRangeStart, portRangeEnd int

	if len(discovered.PortMappings) > 0 {
		seenHostPorts := make(map[int]bool)
		for _, pm := range discovered.PortMappings {
			if skipHostPorts[pm.HostPort] || seenHostPorts[pm.HostPort] || !validDiscoveredPort(pm.HostPort) || !validDiscoveredPort(pm.GuestPort) {
				continue
			}
			seenHostPorts[pm.HostPort] = true
			protocol := pm.Protocol
			if protocol == "" {
				protocol = "both"
			}
			port := providerModel.Port{
				InstanceID:  instanceID,
				ProviderID:  providerID,
				HostPort:    pm.HostPort,
				GuestPort:   pm.GuestPort,
				Protocol:    protocol,
				Status:      "active",
				IsSSH:       pm.IsSSH,
				IsAutomatic: true,
				PortType:    "range_mapped",
			}
			if pm.IsSSH {
				port.Description = "SSH"
			} else {
				port.Description = fmt.Sprintf("端口%d", pm.HostPort)
			}
			ports = append(ports, port)

			// 计算端口范围
			if portRangeStart == 0 || pm.HostPort < portRangeStart {
				portRangeStart = pm.HostPort
			}
			if pm.HostPort > portRangeEnd {
				portRangeEnd = pm.HostPort
			}
		}
	} else if discovered.SSHPort > 0 && discovered.SSHPort != 22 && !skipHostPorts[discovered.SSHPort] {
		// 只有SSH端口信息，没有完整的端口映射
		ports = append(ports, providerModel.Port{
			InstanceID:  instanceID,
			ProviderID:  providerID,
			HostPort:    discovered.SSHPort,
			GuestPort:   22,
			Protocol:    "both",
			Status:      "active",
			IsSSH:       true,
			IsAutomatic: true,
			PortType:    "range_mapped",
			Description: "SSH",
		})
		portRangeStart = discovered.SSHPort
		portRangeEnd = discovered.SSHPort
	}

	if len(ports) > 0 {
		if err := tx.CreateInBatches(ports, 100).Error; err != nil {
			return fmt.Errorf("批量创建端口映射失败: %w", err)
		}
	}

	// 更新实例的端口范围和SSH端口
	updates := map[string]interface{}{}
	if portRangeStart > 0 {
		updates["port_range_start"] = portRangeStart
	}
	if portRangeEnd > 0 {
		updates["port_range_end"] = portRangeEnd
	}
	if discovered.SSHPort > 0 {
		updates["ssh_port"] = discovered.SSHPort
	}
	if len(updates) > 0 {
		if err := tx.Model(&providerModel.Instance{}).Where("id = ?", instanceID).Updates(updates).Error; err != nil {
			return fmt.Errorf("更新实例端口范围失败: %w", err)
		}
	}

	return nil
}

func selectDiscoveredInstances(instances []provider.DiscoveredInstance, selectors []string) ([]provider.DiscoveredInstance, error) {
	selected := make([]provider.DiscoveredInstance, 0, len(selectors))
	seen := make(map[string]bool)
	for _, rawSelector := range selectors {
		selector := strings.TrimSpace(rawSelector)
		if selector == "" {
			return nil, fmt.Errorf("instanceUuids 不能包含空值")
		}
		matches := make([]provider.DiscoveredInstance, 0, 1)
		for _, instance := range instances {
			if selector == instance.UUID || selector == instance.ProviderInstanceID || selector == instance.Name {
				matches = append(matches, instance)
			}
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("未发现指定实例: %s", selector)
		}
		// 非纯净节点可能存在重名行。发现结果已经稳定排序，兼容名称选择器时
		// 确定性选择第一条，剩余重复项由批次去重逻辑记录为 skipped。
		key := matches[0].UUID
		if key == "" {
			key = matches[0].ProviderInstanceID + "\x00" + matches[0].Name
		}
		if !seen[key] {
			seen[key] = true
			selected = append(selected, matches[0])
		}
	}
	return selected, nil
}

func normalizeImportedInstanceUUID(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func canRestoreHistoricalImportedInstance(owner providerModel.Instance, providerID uint) bool {
	return owner.DeletedAt.Valid && owner.ProviderID == providerID
}

func restoreHistoricalImportedInstance(tx *gorm.DB, instance *providerModel.Instance, historicalID uint) *gorm.DB {
	return tx.Unscoped().Model(&providerModel.Instance{}).
		Where("id = ? AND deleted_at IS NOT NULL", historicalID).
		Select("*").
		Omit("id", "created_at").
		Updates(instance)
}

func rollbackImportedInstance(tx *gorm.DB, savepoint string) error {
	if err := tx.RollbackTo(savepoint).Error; err != nil {
		return fmt.Errorf("回滚失败的实例导入项失败: %w", err)
	}
	return nil
}

func isDuplicateImportResourceError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "duplicate") || strings.Contains(lower, "unique constraint") || strings.Contains(lower, "unique key")
}

func deduplicateDiscoveredInstances(instances []provider.DiscoveredInstance) ([]provider.DiscoveredInstance, []duplicateDiscoveredInstance) {
	ordered := append([]provider.DiscoveredInstance(nil), instances...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return discoveredInstanceCanonicalKey(ordered[i]) < discoveredInstanceCanonicalKey(ordered[j])
	})
	owners := make(map[string]string)
	kept := make([]provider.DiscoveredInstance, 0, len(ordered))
	duplicates := make([]duplicateDiscoveredInstance, 0)
	for _, instance := range ordered {
		keys := discoveredInstanceConflictKeys(instance)
		var reason string
		for _, key := range keys {
			if owner, exists := owners[key]; exists {
				reason = fmt.Sprintf("发现资源重复（%s），已保留实例 %s", strings.ReplaceAll(key, "\x00", ":"), owner)
				break
			}
		}
		if reason != "" {
			duplicates = append(duplicates, duplicateDiscoveredInstance{Instance: instance, Reason: reason})
			continue
		}
		owner := instance.Name
		if owner == "" {
			owner = instance.ProviderInstanceID
		}
		for _, key := range keys {
			owners[key] = owner
		}
		kept = append(kept, instance)
	}
	return kept, duplicates
}

func discoveredInstanceConflictKeys(instance provider.DiscoveredInstance) []string {
	keys := make([]string, 0, 8+len(instance.PortMappings))
	appendKey := func(kind, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			keys = append(keys, kind+"\x00"+strings.ToLower(value))
		}
	}
	appendKey("uuid", instance.UUID)
	appendKey("remote-id", instance.ProviderInstanceID)
	appendKey("name", instance.Name)
	appendKey("private-ip", instance.PrivateIP)
	appendKey("ipv6", instance.IPv6Address)
	portSet := make(map[int]struct{})
	if validDiscoveredPort(instance.SSHPort) && instance.SSHPort != 22 {
		portSet[instance.SSHPort] = struct{}{}
	}
	for _, mapping := range instance.PortMappings {
		if validDiscoveredPort(mapping.HostPort) {
			portSet[mapping.HostPort] = struct{}{}
		}
	}
	for _, port := range instance.ExtraPorts {
		if validDiscoveredPort(port) {
			portSet[port] = struct{}{}
		}
	}
	ports := make([]int, 0, len(portSet))
	for port := range portSet {
		ports = append(ports, port)
	}
	sort.Ints(ports)
	for _, port := range ports {
		keys = append(keys, fmt.Sprintf("host-port\x00%d", port))
	}
	return keys
}

func hasConflictingInstanceName(providerType string, discovered provider.DiscoveredInstance, existing []providerModel.Instance) bool {
	for _, instance := range existing {
		if strings.EqualFold(strings.TrimSpace(instance.Name), strings.TrimSpace(discovered.Name)) && !discoveredInstanceMatchesDB(providerType, discovered, instance) {
			return true
		}
	}
	return false
}

func conflictingExistingInstanceResource(discovered provider.DiscoveredInstance, existing []providerModel.Instance) string {
	for _, instance := range existing {
		if discovered.PrivateIP != "" && instance.PrivateIP != "" && discovered.PrivateIP == instance.PrivateIP {
			return fmt.Sprintf("私网IP与已纳管实例 %s 重复，已保留现有实例", instance.Name)
		}
		if discovered.IPv6Address != "" && instance.IPv6Address != "" && strings.EqualFold(discovered.IPv6Address, instance.IPv6Address) {
			return fmt.Sprintf("IPv6地址与已纳管实例 %s 重复，已保留现有实例", instance.Name)
		}
	}
	return ""
}

func sortedPortSet(values map[int]bool) []int {
	ports := make([]int, 0, len(values))
	for port := range values {
		ports = append(ports, port)
	}
	sort.Ints(ports)
	return ports
}
