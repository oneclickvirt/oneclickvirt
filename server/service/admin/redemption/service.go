package redemption

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"oneclickvirt/constant"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	systemModel "oneclickvirt/model/system"
	userModel "oneclickvirt/model/user"
	"oneclickvirt/service/database"
	"oneclickvirt/service/interfaces"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Service 兑换码管理服务
type Service struct {
	taskService interfaces.TaskServiceInterface
}

// NewService 创建兑换码管理服务
func NewService(taskService interfaces.TaskServiceInterface) *Service {
	return &Service{taskService: taskService}
}

// GetList 获取兑换码列表（分页+筛选）
func (s *Service) GetList(req adminModel.RedemptionCodeListRequest) ([]adminModel.RedemptionCodeResponse, int64, error) {
	var codes []systemModel.RedemptionCode
	var total int64

	query := global.APP_DB.Model(&systemModel.RedemptionCode{})

	if req.Code != "" {
		query = query.Where("code LIKE ?", "%"+req.Code+"%")
	}
	if req.Status != "" {
		query = query.Where("status = ?", req.Status)
	}
	if req.ProviderID != 0 {
		query = query.Where("provider_id = ?", req.ProviderID)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (req.Page - 1) * req.PageSize
	if err := query.Order("created_at DESC").Offset(offset).Limit(req.PageSize).Find(&codes).Error; err != nil {
		return nil, 0, err
	}

	// 批量查询创建者用户名，避免 N+1
	creatorIDSet := make(map[uint]bool)
	for _, c := range codes {
		if c.CreatedBy != 0 {
			creatorIDSet[c.CreatedBy] = true
		}
	}
	creatorIDs := make([]uint, 0, len(creatorIDSet))
	for id := range creatorIDSet {
		creatorIDs = append(creatorIDs, id)
	}

	userMap := make(map[uint]string)
	if len(creatorIDs) > 0 {
		var users []userModel.User
		global.APP_DB.Select("id, username").Where("id IN ?", creatorIDs).Limit(500).Find(&users)
		for _, u := range users {
			userMap[u.ID] = u.Username
		}
	}

	result := make([]adminModel.RedemptionCodeResponse, 0, len(codes))
	for _, c := range codes {
		result = append(result, adminModel.RedemptionCodeResponse{
			RedemptionCode: c,
			CreatedByUser:  userMap[c.CreatedBy],
		})
	}

	return result, total, nil
}

// BatchCreate 批量创建兑换码：生成 Code -> 插入 DB (status=pending_create) -> 创建 create_redemption_instance 任务
func (s *Service) BatchCreate(req adminModel.BatchCreateRedemptionCodesRequest, adminID uint) error {
	dbService := database.GetDatabaseService()

	// 验证 Provider 存在
	var provider providerModel.Provider
	if err := global.APP_DB.Where("id = ? AND status IN (?)", req.ProviderID, []string{"active", "partial"}).First(&provider).Error; err != nil {
		return fmt.Errorf("节点不存在或不可用")
	}

	// 验证规格 ID 并计算本次批量创建所需的总资源量
	cpuSpec, err := constant.GetCPUSpecByID(req.CPUId)
	if err != nil {
		return fmt.Errorf("无效的CPU规格: %v", err)
	}
	memorySpec, err := constant.GetMemorySpecByID(req.MemoryId)
	if err != nil {
		return fmt.Errorf("无效的内存规格: %v", err)
	}
	diskSpec, err := constant.GetDiskSpecByID(req.DiskId)
	if err != nil {
		return fmt.Errorf("无效的磁盘规格: %v", err)
	}

	// 根据实例类型和节点的超分配配置，决定哪些资源项需要做容量检查
	// ContainerLimitCPU/Memory/Disk 和 VMLimitCPU/Memory/Disk 为 true 时表示该资源不允许超开
	isContainer := req.InstanceType == "container"
	checkCPU := (isContainer && provider.ContainerLimitCPU) || (!isContainer && provider.VMLimitCPU)
	checkMemory := (isContainer && provider.ContainerLimitMemory) || (!isContainer && provider.VMLimitMemory)
	checkDisk := (isContainer && provider.ContainerLimitDisk) || (!isContainer && provider.VMLimitDisk)

	requiredCPU := cpuSpec.Cores * req.Count
	requiredMemoryMB := int64(memorySpec.SizeMB) * int64(req.Count)
	requiredDiskMB := int64(diskSpec.SizeMB) * int64(req.Count)

	if checkCPU && provider.NodeCPUCores > 0 {
		availCPU := provider.NodeCPUCores - provider.UsedCPUCores
		if requiredCPU > availCPU {
			return fmt.Errorf("节点CPU资源不足：需要 %d 核，当前可用 %d 核", requiredCPU, availCPU)
		}
	}
	if checkMemory && provider.NodeMemoryTotal > 0 {
		availMemMB := provider.NodeMemoryTotal - provider.UsedMemory
		if requiredMemoryMB > availMemMB {
			return fmt.Errorf("节点内存资源不足：需要 %d MB，当前可用 %d MB", requiredMemoryMB, availMemMB)
		}
	}
	if checkDisk && provider.NodeDiskTotal > 0 {
		availDiskMB := provider.NodeDiskTotal - provider.UsedDisk
		if requiredDiskMB > availDiskMB {
			return fmt.Errorf("节点磁盘资源不足：需要 %d MB，当前可用 %d MB", requiredDiskMB, availDiskMB)
		}
	}

	for i := 0; i < req.Count; i++ {
		code, err := s.generateUniqueCode()
		if err != nil {
			return fmt.Errorf("生成兑换码失败: %v", err)
		}

		// 构造任务数据（字段名与 CreateInstanceTaskRequest 的 JSON 标签一致，便于 executeProviderCreation 复用）
		taskDataReq := adminModel.CreateRedemptionInstanceTaskRequest{
			ProviderId:  req.ProviderID,
			ImageId:     req.ImageId,
			CPUId:       req.CPUId,
			MemoryId:    req.MemoryId,
			DiskId:      req.DiskId,
			BandwidthId: req.BandwidthId,
		}

		var redemptionCode systemModel.RedemptionCode

		err = dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
			redemptionCode = systemModel.RedemptionCode{
				Code:         code,
				Status:       systemModel.RedemptionStatusPendingCreate,
				ProviderID:   req.ProviderID,
				ProviderName: provider.Name,
				InstanceType: req.InstanceType,
				ImageId:      req.ImageId,
				CPUId:        req.CPUId,
				MemoryId:     req.MemoryId,
				DiskId:       req.DiskId,
				BandwidthId:  req.BandwidthId,
				CreatedBy:    adminID,
				Remark:       req.Remark,
			}
			if err := tx.Create(&redemptionCode).Error; err != nil {
				return fmt.Errorf("创建兑换码记录失败: %v", err)
			}

			// 将 RedemptionCodeID 写入任务数据
			taskDataReq.RedemptionCodeID = redemptionCode.ID
			taskDataJSON, err := json.Marshal(taskDataReq)
			if err != nil {
				return fmt.Errorf("序列化任务数据失败: %v", err)
			}

			// 使用管理员 ID 创建任务（避免 executeProviderCreation 中用户查询失败）
			task, err := s.taskService.CreateTask(adminID, &req.ProviderID, nil, "create_redemption_instance", string(taskDataJSON), 0)
			if err != nil {
				return fmt.Errorf("创建任务失败: %v", err)
			}

			// 更新兑换码状态为 creating，记录 TaskID
			taskID := task.ID
			if err := tx.Model(&redemptionCode).Updates(map[string]interface{}{
				"status":  systemModel.RedemptionStatusCreating,
				"task_id": taskID,
			}).Error; err != nil {
				return fmt.Errorf("更新兑换码状态失败: %v", err)
			}

			return nil
		})

		if err != nil {
			global.APP_LOG.Error("创建兑换码失败", zap.Int("index", i), zap.Error(err))
			return err
		}
	}

	return nil
}

// BatchDelete 批量删除兑换码（硬删除），同时清理对应实例
// - pending_use: 创建实例删除任务 + 硬删除兑换码
// - pending_create / creating: 取消任务 + 硬删除兑换码
// - used: 仅硬删除兑换码（实例已属于用户）
// - deleting: 跳过（已在处理中）
func (s *Service) BatchDelete(ids []uint, adminID uint) error {
	if len(ids) == 0 {
		return fmt.Errorf("请选择要删除的兑换码")
	}

	var codes []systemModel.RedemptionCode
	if err := global.APP_DB.Where("id IN ?", ids).Find(&codes).Error; err != nil {
		return fmt.Errorf("查询兑换码失败: %v", err)
	}

	dbService := database.GetDatabaseService()

	for _, code := range codes {
		codeID := code.ID

		switch code.Status {
		case systemModel.RedemptionStatusDeleting:
			continue

		case systemModel.RedemptionStatusPendingUse:
			if code.InstanceID != nil {
				var instance providerModel.Instance
				if err := global.APP_DB.First(&instance, *code.InstanceID).Error; err == nil {
					taskData := map[string]interface{}{
						"instanceId":     instance.ID,
						"providerId":     instance.ProviderID,
						"adminOperation": true,
					}
					taskDataJSON, err := json.Marshal(taskData)
					if err == nil {
						if _, tErr := s.taskService.CreateTask(adminID, &instance.ProviderID, &instance.ID, "delete", string(taskDataJSON), 0); tErr != nil {
							global.APP_LOG.Error("创建实例删除任务失败",
								zap.Uint("codeId", codeID),
								zap.Uint("instanceId", instance.ID),
								zap.Error(tErr))
						} else {
							global.APP_DB.Model(&instance).Update("status", "deleting")
						}
					}
				}
			}
			if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
				return tx.Unscoped().Delete(&systemModel.RedemptionCode{}, codeID).Error
			}); err != nil {
				global.APP_LOG.Error("硬删除兑换码失败", zap.Uint("codeId", codeID), zap.Error(err))
			}

		case systemModel.RedemptionStatusPendingCreate, systemModel.RedemptionStatusCreating:
			if code.TaskID != nil {
				taskID := *code.TaskID
				var t adminModel.Task
				if err := global.APP_DB.Where("id = ? AND status IN ('pending','running')", taskID).First(&t).Error; err == nil {
					global.APP_DB.Model(&t).Updates(map[string]interface{}{
						"status":        "cancelled",
						"cancel_reason": "兑换码被管理员删除",
						"completed_at":  time.Now(),
					})
				}
			}
			if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
				return tx.Unscoped().Delete(&systemModel.RedemptionCode{}, codeID).Error
			}); err != nil {
				global.APP_LOG.Error("硬删除兑换码失败", zap.Uint("codeId", codeID), zap.Error(err))
			}

		default:
			// used 及未知状态：仅删除兑换码记录（实例已属于用户，不影响）
			if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
				return tx.Unscoped().Delete(&systemModel.RedemptionCode{}, codeID).Error
			}); err != nil {
				global.APP_LOG.Error("硬删除兑换码失败", zap.Uint("codeId", codeID), zap.Error(err))
			}
		}
	}

	return nil
}

// ExportByIDs 导出指定 ID 的兑换码字符串
func (s *Service) ExportByIDs(ids []uint) ([]string, error) {
	var codes []systemModel.RedemptionCode
	query := global.APP_DB.Model(&systemModel.RedemptionCode{})
	if len(ids) > 0 {
		query = query.Where("id IN ?", ids)
	}
	if err := query.Find(&codes).Error; err != nil {
		return nil, err
	}

	result := make([]string, 0, len(codes))
	for _, c := range codes {
		result = append(result, c.Code)
	}
	return result, nil
}

// generateUniqueCode 生成唯一的 16 位大写字母数字兑换码
func (s *Service) generateUniqueCode() (string, error) {
	const charset = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	const codeLen = 16
	const maxAttempts = 20

	for attempt := 0; attempt < maxAttempts; attempt++ {
		buf := make([]byte, codeLen)
		for i := range buf {
			n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
			if err != nil {
				return "", err
			}
			buf[i] = charset[n.Int64()]
		}
		code := string(buf)

		// ORI 前缀保留给节点导入自动生成的兑换码，普通兑换码不允许使用该前缀
		if strings.HasPrefix(code, "ORI") {
			continue
		}

		var existing systemModel.RedemptionCode
		if err := global.APP_DB.Where("code = ?", code).First(&existing).Error; err != nil {
			return code, nil
		}
	}
	return "", fmt.Errorf("无法生成唯一兑换码，请重试")
}
