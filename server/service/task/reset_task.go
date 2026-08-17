package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/constant"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	systemModel "oneclickvirt/model/system"
	userModel "oneclickvirt/model/user"
	traffic_monitor "oneclickvirt/service/admin/traffic_monitor"
	ipv6PoolService "oneclickvirt/service/ipv6pool"
	provider2 "oneclickvirt/service/provider"
	"oneclickvirt/service/resources"
	"oneclickvirt/utils"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PortMappingRequest 端口映射创建请求
type PortMappingRequest struct {
	InstanceID    uint
	ProviderID    uint
	HostPort      int
	GuestPort     int
	Protocol      string
	Description   string
	IsSSH         bool
	IsAutomatic   bool
	PortType      string
	MappingMethod string
	IPv6Enabled   bool
}

// ResetTaskContext 重置任务上下文
type ResetTaskContext struct {
	Instance               providerModel.Instance
	Provider               providerModel.Provider
	SystemImage            systemModel.SystemImage
	OldPortMappings        []providerModel.Port
	OldInstanceID          uint
	OldInstanceName        string
	OldProviderInstanceID  string
	OriginalUserID         uint
	OriginalStatus         string // 实例重置前的原始状态（用于正确释放配额）
	OriginalExpiresAt      *time.Time
	OriginalIsManualExpiry bool // 实例重置前的手动过期时间设置
	OriginalMaxTraffic     uint64
	NewInstanceID          uint
	NewProviderInstanceID  string
	NewPassword            string
	NewPrivateIP           string
	OldAllocatedIPv6       string
	NewAllocatedIPv6       string
	NewIPv6Metadata        ipv6PoolService.IPv6AllocationMetadata
	OldEgressBindingID     uint
}

func applyIPv6AllocationMetadata(metadata map[string]string, allocation ipv6PoolService.IPv6AllocationMetadata) {
	if metadata == nil || strings.TrimSpace(allocation.Address) == "" {
		return
	}
	metadata["static_ipv6"] = allocation.Address
	if strings.TrimSpace(allocation.CIDR) == "" {
		return
	}
	metadata["static_ipv6_cidr"] = allocation.CIDR
	metadata["static_ipv6_gateway"] = allocation.Gateway
	metadata["static_ipv6_bridge"] = allocation.Bridge
	metadata["static_ipv6_tunnel_id"] = fmt.Sprintf("%d", allocation.TunnelID)
	metadata["static_ipv6_tunnel_interface"] = allocation.TunnelInterface
	metadata["static_ipv6_network"] = allocation.CIDR
}

// validateResetRoutedIPv6 verifies the binding that will be moved during a
// reset before the old guest is removed. Routed-only VM backends cannot turn a
// legacy standalone pool address into a reachable guest route, so discovering
// that only after deletion would leave the user without their original guest.
func validateResetRoutedIPv6(providerType, networkType, allocatedIPv6 string, allocation ipv6PoolService.IPv6AllocationMetadata) error {
	if !ipv6PoolService.RequiresRoutedStaticIPv6(providerType) {
		return nil
	}
	if !utils.NetworkTypeHasIPv6(networkType) && strings.TrimSpace(allocatedIPv6) == "" {
		return nil
	}
	if strings.TrimSpace(allocatedIPv6) == "" {
		return fmt.Errorf("Provider类型 %s 的IPv6实例缺少可迁移的隧道路由IPv6地址池绑定，无法安全重建", providerType)
	}
	if strings.TrimSpace(allocation.Address) != strings.TrimSpace(allocatedIPv6) {
		return fmt.Errorf("重建实例IPv6地址与隧道路由元数据不一致")
	}
	if strings.TrimSpace(allocation.CIDR) == "" || strings.TrimSpace(allocation.Gateway) == "" ||
		strings.TrimSpace(allocation.Bridge) == "" || allocation.TunnelID == 0 || strings.TrimSpace(allocation.TunnelInterface) == "" {
		return fmt.Errorf("Provider类型 %s 的IPv6地址池缺少隧道路由前缀、网关、网桥或接口信息，无法安全重建", providerType)
	}
	return nil
}

func resetReplacementInstance(resetCtx *ResetTaskContext) providerModel.Instance {
	return providerModel.Instance{
		UUID:            resetCtx.Instance.UUID,
		Name:            resetCtx.OldInstanceName,
		Provider:        resetCtx.Provider.Name,
		ProviderID:      resetCtx.Provider.ID,
		Image:           resetCtx.Instance.Image,
		InstanceType:    resetCtx.Instance.InstanceType,
		CPU:             resetCtx.Instance.CPU,
		Memory:          resetCtx.Instance.Memory,
		Disk:            resetCtx.Instance.Disk,
		Bandwidth:       resetCtx.Instance.Bandwidth,
		UserID:          resetCtx.OriginalUserID,
		Status:          "creating",
		OSType:          resetCtx.Instance.OSType,
		NetworkType:     resetCtx.Instance.NetworkType,
		ExpiresAt:       resetCtx.OriginalExpiresAt,
		IsManualExpiry:  resetCtx.OriginalIsManualExpiry,
		PublicIP:        resetCtx.Provider.Endpoint,
		MaxTraffic:      int64(resetCtx.OriginalMaxTraffic),
		ProviderVMID:    resetCtx.OldInstanceName,
		EgressProfileID: resetCtx.Instance.EgressProfileID,
	}
}

func transferResetEgressBindingInTx(tx *gorm.DB, bindingID, providerID, oldInstanceID, newInstanceID uint) error {
	if bindingID == 0 {
		return nil
	}
	if tx == nil || providerID == 0 || oldInstanceID == 0 || newInstanceID == 0 {
		return fmt.Errorf("透明出口绑定迁移参数无效")
	}
	result := tx.Model(&monitoringModel.EgressDesiredBinding{}).
		Where("id = ? AND provider_id = ? AND instance_id = ? AND pending_delete = ?",
			bindingID, providerID, oldInstanceID, false).
		Update("instance_id", newInstanceID)
	if result.Error != nil {
		return fmt.Errorf("迁移重建实例透明出口绑定失败: %v", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("透明出口绑定在重建期间发生并发变化")
	}
	return nil
}

// executeResetTask 执行实例重置任务
// 直接复用删除和创建逻辑，避免代码重复和资源管理错误
func (s *TaskService) executeResetTask(ctx context.Context, task *adminModel.Task) error {
	// 解析任务数据
	var taskReq adminModel.InstanceOperationTaskRequest
	if err := json.Unmarshal([]byte(task.TaskData), &taskReq); err != nil {
		return fmt.Errorf("解析任务数据失败: %v", err)
	}

	var resetCtx ResetTaskContext

	// 当任务context被取消时（超时/强制停止），确保新实例不会卡在creating状态
	// 使用独立的background context执行清理，避免被取消的ctx影响
	defer func() {
		if ctx.Err() != nil && resetCtx.NewInstanceID != 0 {
			bgCtx := context.Background()
			result := global.APP_DB.WithContext(bgCtx).
				Model(&providerModel.Instance{}).
				Where("id = ? AND status = ?", resetCtx.NewInstanceID, "creating").
				Updates(map[string]interface{}{
					"status":     "stopped",
					"updated_at": time.Now(),
				})
			if result.Error != nil {
				global.APP_LOG.Error("重置任务context取消后清理新实例状态失败",
					zap.Uint("taskId", task.ID),
					zap.Uint("newInstanceId", resetCtx.NewInstanceID),
					zap.Error(result.Error))
			} else if result.RowsAffected > 0 {
				global.APP_LOG.Warn("重置任务因context取消而中断，已将新实例状态从creating恢复为stopped",
					zap.Uint("taskId", task.ID),
					zap.Uint("newInstanceId", resetCtx.NewInstanceID))
			}
		}
	}()
	// 阶段1: 准备阶段 - 收集必要信息
	if err := s.resetTask_Prepare(ctx, task, &taskReq, &resetCtx); err != nil {
		return err
	}

	// 阶段2: 执行Provider删除（复用删除逻辑）
	if err := s.resetTask_DeleteOldInstance(ctx, task, &resetCtx); err != nil {
		return err
	}

	// 阶段3: 清理旧实例数据库记录和资源
	if err := s.resetTask_CleanupOldInstance(ctx, task, &resetCtx); err != nil {
		return err
	}

	// 阶段4: 创建新实例（复用创建逻辑）
	if err := s.resetTask_CreateNewInstance(ctx, task, &resetCtx); err != nil {
		return err
	}

	// 阶段5: 设置密码
	if err := s.resetTask_SetPassword(ctx, task, &resetCtx); err != nil {
		// 密码设置失败不影响重置流程，但不能记录固定默认口令。
		global.APP_LOG.Warn("重置系统：密码设置失败，未记录新密码", zap.Error(err))
	}

	// 阶段6: 更新实例信息
	if err := s.resetTask_UpdateInstanceInfo(ctx, task, &resetCtx); err != nil {
		return err
	}

	// 阶段7: 恢复端口映射（使用端口映射服务）
	if err := s.resetTask_RestorePortMappings(ctx, task, &resetCtx); err != nil {
		// 端口映射失败不影响重置流程
		global.APP_LOG.Warn("重置系统：端口映射恢复部分失败", zap.Error(err))
	}

	// 阶段8: 重新初始化监控
	if err := s.resetTask_ReinitializeMonitoring(ctx, task, &resetCtx); err != nil {
		// 监控初始化失败不影响重置流程
		global.APP_LOG.Warn("重置系统：监控初始化失败", zap.Error(err))
	}

	s.updateTaskProgress(task.ID, 100, "step.resetCompleted")

	global.APP_LOG.Info("用户实例重置成功",
		zap.Uint("taskId", task.ID),
		zap.Uint("oldInstanceId", resetCtx.OldInstanceID),
		zap.Uint("newInstanceId", resetCtx.NewInstanceID),
		zap.String("instanceName", resetCtx.OldInstanceName),
		zap.Uint("userId", task.UserID))

	return nil
}

// resetTask_Prepare 阶段1: 准备阶段 - 查询必要信息
func (s *TaskService) resetTask_Prepare(ctx context.Context, task *adminModel.Task, taskReq *adminModel.InstanceOperationTaskRequest, resetCtx *ResetTaskContext) error {
	s.updateTaskProgress(task.ID, 5, "step.preparingReset")

	// 解析taskData获取originalStatus（实例重置前的原始状态）
	var taskData map[string]interface{}
	if err := json.Unmarshal([]byte(task.TaskData), &taskData); err == nil {
		if originalStatus, ok := taskData["originalStatus"].(string); ok {
			resetCtx.OriginalStatus = originalStatus
			global.APP_LOG.Debug("从任务数据中解析到原始状态",
				zap.String("originalStatus", originalStatus))
		}
	}

	// 确定重置使用的镜像名称（用户可能选择了不同的镜像）
	resetImageName := ""
	if ri, ok := taskData["resetImage"].(string); ok && ri != "" {
		resetImageName = ri
	}

	// 使用单个短事务查询所有需要的数据
	err := s.dbService.ExecuteQuery(ctx, func() error {
		// 1. 查询实例
		if err := global.APP_DB.First(&resetCtx.Instance, taskReq.InstanceId).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("实例不存在")
			}
			return fmt.Errorf("获取实例信息失败: %v", err)
		}

		// 验证实例所有权
		if resetCtx.Instance.UserID != task.UserID {
			return fmt.Errorf("无权限操作此实例")
		}

		// 2. 查询Provider
		if err := global.APP_DB.First(&resetCtx.Provider, resetCtx.Instance.ProviderID).Error; err != nil {
			return fmt.Errorf("获取Provider配置失败: %v", err)
		}

		var ipv6Binding providerModel.ProviderIPv6Pool
		bindingResult := global.APP_DB.Where("provider_id = ? AND instance_id = ? AND is_allocated = ? AND deleted_at IS NULL",
			resetCtx.Provider.ID, resetCtx.Instance.ID, true).First(&ipv6Binding)
		if bindingResult.Error == nil {
			resetCtx.OldAllocatedIPv6 = ipv6Binding.Address
		} else if !errors.Is(bindingResult.Error, gorm.ErrRecordNotFound) {
			return fmt.Errorf("获取实例IPv6地址池绑定失败: %v", bindingResult.Error)
		}

		var egressBinding monitoringModel.EgressDesiredBinding
		egressResult := global.APP_DB.Where("provider_id = ? AND instance_id = ? AND pending_delete = ?",
			resetCtx.Provider.ID, resetCtx.Instance.ID, false).First(&egressBinding)
		if egressResult.Error == nil {
			if strings.TrimSpace(resetCtx.Instance.UUID) == "" {
				return fmt.Errorf("实例缺少稳定UUID，无法安全迁移透明出口绑定")
			}
			if configured := strings.TrimSpace(resetCtx.Instance.EgressProfileID); configured != "" && configured != egressBinding.ProfileID {
				return fmt.Errorf("实例出口配置与控制端期望状态不一致")
			}
			resetCtx.Instance.EgressProfileID = egressBinding.ProfileID
			resetCtx.OldEgressBindingID = egressBinding.ID
		} else if !errors.Is(egressResult.Error, gorm.ErrRecordNotFound) {
			return fmt.Errorf("获取实例透明出口绑定失败: %v", egressResult.Error)
		} else if strings.TrimSpace(resetCtx.Instance.EgressProfileID) != "" {
			return fmt.Errorf("实例已配置透明出口，但控制端期望状态缺失")
		}

		// 重新确定resetImageName（需要在查询实例后获取默认值）
		if resetImageName == "" {
			resetImageName = resetCtx.Instance.Image
		}

		// 3. 查询系统镜像（使用用户选择的镜像或当前实例的镜像）
		imageProviderTypes := utils.SystemImageProviderTypeCandidates(resetCtx.Provider.Type)
		if len(imageProviderTypes) == 0 {
			return fmt.Errorf("Provider类型无效，无法查询系统镜像")
		}
		if err := global.APP_DB.Where("name = ? AND provider_type IN ? AND instance_type = ? AND architecture = ?",
			resetImageName, imageProviderTypes, resetCtx.Instance.InstanceType, resetCtx.Provider.Architecture).
			Order(clause.Expr{SQL: "CASE WHEN provider_type = ? THEN 0 ELSE 1 END", Vars: []interface{}{imageProviderTypes[0]}}).
			First(&resetCtx.SystemImage).Error; err != nil {
			return fmt.Errorf("获取系统镜像信息失败: %v", err)
		}

		// 更新实例镜像名称为用户选择的镜像（可能与原镜像不同）
		resetCtx.Instance.Image = resetImageName
		resetCtx.Instance.OSType = resetCtx.SystemImage.OSType

		// 4. 查询端口映射（包含status='active'的）
		if err := global.APP_DB.Where("instance_id = ? AND status = ?", resetCtx.Instance.ID, "active").
			Find(&resetCtx.OldPortMappings).Error; err != nil {
			global.APP_LOG.Warn("获取旧端口映射失败", zap.Error(err))
		}

		return nil
	})

	if err != nil {
		return err
	}
	if utils.NetworkTypeHasIPv6(resetCtx.Instance.NetworkType) && !ipv6PoolService.SupportsStaticIPv6(resetCtx.Provider.Type) {
		return fmt.Errorf("Provider类型 %s 当前不支持实例IPv6网络配置，无法安全重建", resetCtx.Provider.Type)
	}
	if resetCtx.OldAllocatedIPv6 != "" && !ipv6PoolService.SupportsStaticIPv6(resetCtx.Provider.Type) {
		return fmt.Errorf("Provider类型 %s 无法在重建时恢复控制面分配的静态IPv6地址", resetCtx.Provider.Type)
	}
	if ipv6PoolService.RequiresRoutedStaticIPv6(resetCtx.Provider.Type) &&
		(utils.NetworkTypeHasIPv6(resetCtx.Instance.NetworkType) || resetCtx.OldAllocatedIPv6 != "") {
		// This is one controller-side join before any destructive provider call.
		// The binding is locked and transferred later in the short replacement
		// transaction, so this validation never holds a DB transaction over SSH.
		allocation, metadataErr := ipv6PoolService.NewService().GetAllocationMetadata(resetCtx.Provider.ID, resetCtx.Instance.ID)
		if metadataErr != nil {
			return fmt.Errorf("读取重建实例IPv6隧道路由元数据失败: %w", metadataErr)
		}
		if err := validateResetRoutedIPv6(resetCtx.Provider.Type, resetCtx.Instance.NetworkType, resetCtx.OldAllocatedIPv6, allocation); err != nil {
			return err
		}
	}

	// 如果无法从taskData解析originalStatus，则使用当前状态作为兜底
	if resetCtx.OriginalStatus == "" {
		resetCtx.OriginalStatus = resetCtx.Instance.Status
		global.APP_LOG.Warn("无法从任务数据解析原始状态，使用当前状态作为兜底",
			zap.String("currentStatus", resetCtx.Instance.Status))
	}

	// 保存必要信息
	resetCtx.OldInstanceID = resetCtx.Instance.ID
	resetCtx.OldInstanceName = resetCtx.Instance.Name
	resetCtx.OldProviderInstanceID = providerInstanceIdentifier(resetCtx.Instance)
	if resetCtx.OldProviderInstanceID == "" {
		resetCtx.OldProviderInstanceID = resetCtx.OldInstanceName
	}
	resetCtx.OriginalUserID = resetCtx.Instance.UserID
	resetCtx.OriginalExpiresAt = resetCtx.Instance.ExpiresAt
	resetCtx.OriginalIsManualExpiry = resetCtx.Instance.IsManualExpiry
	resetCtx.OriginalMaxTraffic = uint64(resetCtx.Instance.MaxTraffic)

	global.APP_LOG.Info("准备阶段完成",
		zap.Uint("taskId", task.ID),
		zap.Uint("instanceId", resetCtx.OldInstanceID),
		zap.String("instanceName", resetCtx.OldInstanceName),
		zap.String("providerInstanceId", resetCtx.OldProviderInstanceID),
		zap.Int("portMappings", len(resetCtx.OldPortMappings)))

	return nil
}

// resetTask_DeleteOldInstance 阶段2: 删除Provider上的旧实例（复用删除逻辑）
func (s *TaskService) resetTask_DeleteOldInstance(ctx context.Context, task *adminModel.Task, resetCtx *ResetTaskContext) error {
	s.updateTaskProgress(task.ID, 15, "step.deletingOldInstance")

	providerApiService := &provider2.ProviderApiService{}

	// 直接调用Provider删除API
	if err := providerApiService.DeleteInstanceByProviderID(ctx, resetCtx.Provider.ID, resetCtx.OldProviderInstanceID); err != nil {
		// 如果实例不存在，继续流程
		errStr := err.Error()
		if contains(errStr, "not found") || contains(errStr, "no such") {
			global.APP_LOG.Info("实例已不存在，继续重置流程",
				zap.String("instanceName", resetCtx.OldInstanceName))
		} else {
			return fmt.Errorf("删除旧实例失败: %v", err)
		}
	}

	// 等待删除完成
	time.Sleep(10 * time.Second)

	global.APP_LOG.Info("旧实例删除完成",
		zap.String("instanceName", resetCtx.OldInstanceName))

	return nil
}

// resetTask_CleanupOldInstance 阶段3: 清理旧实例数据库记录和资源（复用删除逻辑）
func (s *TaskService) resetTask_CleanupOldInstance(ctx context.Context, task *adminModel.Task, resetCtx *ResetTaskContext) error {
	s.updateTaskProgress(task.ID, 25, "step.cleaningOldData")

	// 清理pmacct监控（事务外操作）
	trafficMonitorManager := traffic_monitor.GetManager()
	cleanupCtx, cleanupCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cleanupCancel()

	if err := trafficMonitorManager.DetachMonitor(cleanupCtx, resetCtx.OldInstanceID); err != nil {
		global.APP_LOG.Warn("清理pmacct监控失败", zap.Error(err))
	}

	// The database replacement is deliberately deferred to the create stage so
	// old/new records, resource accounting and address/egress bindings can move
	// in one atomic transaction. No remote I/O is performed in that transaction.
	global.APP_LOG.Info("旧实例宿主资源清理完成，等待原子替换数据库记录",
		zap.Uint("instanceId", resetCtx.OldInstanceID))

	return nil
}

// resetTask_CreateNewInstance 阶段4: 创建新实例（复用创建逻辑）
func (s *TaskService) resetTask_CreateNewInstance(ctx context.Context, task *adminModel.Task, resetCtx *ResetTaskContext) error {
	s.updateTaskProgress(task.ID, 40, "step.creatingNewInstance")

	userLevel := 0
	if resetCtx.OriginalUserID != 0 {
		var user userModel.User
		if err := global.APP_DB.First(&user, resetCtx.OriginalUserID).Error; err != nil {
			return fmt.Errorf("获取用户信息失败: %v", err)
		}
		userLevel = user.Level
	} else {
		global.APP_LOG.Debug("实例无用户归属，使用管理员重建默认用户等级",
			zap.Uint("taskId", task.ID),
			zap.Uint("instanceId", resetCtx.OldInstanceID))
	}

	// 在事务中创建新实例记录、迁移IPv6绑定并分配配额。
	// 远程Provider调用始终在事务提交后执行。
	var newInstance providerModel.Instance
	allocatedIPv6 := ""
	allocatedIPv6Metadata := ipv6PoolService.IPv6AllocationMetadata{}
	err := s.dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		allocatedIPv6 = ""
		allocatedIPv6Metadata = ipv6PoolService.IPv6AllocationMetadata{}
		portMappingService := resources.PortMappingService{}
		if err := portMappingService.DeleteInstancePortMappingsInTx(tx, resetCtx.OldInstanceID); err != nil {
			return fmt.Errorf("删除旧实例端口映射失败: %v", err)
		}

		resourceService := &resources.ResourceService{}
		if err := resourceService.ReleaseResourcesInTx(tx, resetCtx.Provider.ID, resetCtx.Instance.InstanceType,
			resetCtx.Instance.CPU, resetCtx.Instance.Memory, resetCtx.Instance.Disk); err != nil {
			return fmt.Errorf("释放旧实例Provider资源失败: %v", err)
		}

		quotaService := resources.NewQuotaService()
		resourceUsage := resources.ResourceUsage{
			CPU:       resetCtx.Instance.CPU,
			Memory:    resetCtx.Instance.Memory,
			Disk:      resetCtx.Instance.Disk,
			Bandwidth: resetCtx.Instance.Bandwidth,
		}
		if resetCtx.OriginalUserID != 0 {
			if constant.IsTransitionalStatus(resetCtx.OriginalStatus) {
				if err := quotaService.ReleasePendingQuota(tx, resetCtx.OriginalUserID, resourceUsage); err != nil {
					return fmt.Errorf("释放旧实例待确认配额失败: %v", err)
				}
			} else if err := quotaService.ReleaseUsedQuota(tx, resetCtx.OriginalUserID, resourceUsage); err != nil {
				return fmt.Errorf("释放旧实例已使用配额失败: %v", err)
			}
		}

		deletedName := fmt.Sprintf("%s_deleted_%d", resetCtx.Instance.Name, time.Now().UnixNano())
		if err := tx.Model(&providerModel.Instance{}).Where("id = ?", resetCtx.OldInstanceID).
			Updates(map[string]interface{}{"name": deletedName, "uuid": uuid.New().String()}).Error; err != nil {
			return fmt.Errorf("重命名旧实例失败: %v", err)
		}
		if err := tx.Where("id = ?", resetCtx.OldInstanceID).Delete(&providerModel.Instance{}).Error; err != nil {
			return fmt.Errorf("删除旧实例记录失败: %v", err)
		}

		// 创建新实例记录
		newInstance = resetReplacementInstance(resetCtx)

		if err := tx.Create(&newInstance).Error; err != nil {
			return fmt.Errorf("创建新实例记录失败: %v", err)
		}

		var transferErr error
		allocatedIPv6, transferErr = ipv6PoolService.NewService().TransferIPv6BindingWithDB(
			tx, resetCtx.Provider.ID, resetCtx.OldInstanceID, newInstance.ID,
		)
		if transferErr != nil {
			return transferErr
		}
		if resetCtx.OldAllocatedIPv6 != "" && allocatedIPv6 == "" {
			return fmt.Errorf("旧实例IPv6地址池绑定在重建期间丢失")
		}
		if allocatedIPv6 != "" {
			if err := tx.Model(&newInstance).Update("public_ipv6", allocatedIPv6).Error; err != nil {
				return fmt.Errorf("保存重建实例IPv6地址失败: %v", err)
			}
			newInstance.PublicIPv6 = allocatedIPv6
			metadata, metadataErr := ipv6PoolService.NewServiceWithDB(tx).GetAllocationMetadata(resetCtx.Provider.ID, newInstance.ID)
			if metadataErr != nil {
				return fmt.Errorf("读取重建实例IPv6路由元数据失败: %w", metadataErr)
			}
			if metadata.Address != "" && metadata.Address != allocatedIPv6 {
				return fmt.Errorf("重建实例IPv6地址与路由元数据不一致")
			}
			if metadata.Address == "" {
				// Native pools have no routed parent metadata, but the create
				// request must still retain the controller-selected address.
				metadata.Address = allocatedIPv6
			}
			allocatedIPv6Metadata = metadata
		}
		if err := transferResetEgressBindingInTx(tx, resetCtx.OldEgressBindingID, resetCtx.Provider.ID,
			resetCtx.OldInstanceID, newInstance.ID); err != nil {
			return err
		}

		// 分配待确认配额
		if resetCtx.OriginalUserID != 0 {
			if err := quotaService.AllocatePendingQuota(tx, resetCtx.OriginalUserID, resourceUsage); err != nil {
				return fmt.Errorf("分配待确认配额失败: %v", err)
			}
		} else {
			global.APP_LOG.Debug("实例无用户归属，跳过待确认配额分配",
				zap.Uint("taskId", task.ID),
				zap.Uint("newInstanceId", newInstance.ID))
		}

		// 分配Provider资源
		if err := resourceService.AllocateResourcesInTx(tx, resetCtx.Provider.ID, resetCtx.Instance.InstanceType,
			resetCtx.Instance.CPU, resetCtx.Instance.Memory, resetCtx.Instance.Disk); err != nil {
			return fmt.Errorf("分配Provider资源失败: %v", err)
		}

		return nil
	})

	if err != nil {
		return err
	}
	resetCtx.NewInstanceID = newInstance.ID
	resetCtx.NewProviderInstanceID = newInstance.ProviderVMID
	resetCtx.NewAllocatedIPv6 = allocatedIPv6
	resetCtx.NewIPv6Metadata = allocatedIPv6Metadata

	global.APP_LOG.Info("新实例记录创建完成",
		zap.Uint("newInstanceId", resetCtx.NewInstanceID),
		zap.String("instanceName", resetCtx.OldInstanceName),
		zap.String("providerInstanceId", resetCtx.NewProviderInstanceID))

	s.updateTaskProgress(task.ID, 50, "step.callingProviderCreate")

	// 准备创建请求（使用与正常创建完全相同的逻辑）
	createReq := provider2.CreateInstanceRequest{
		InstanceConfig: providerModel.ProviderInstanceConfig{
			Name:         resetCtx.OldInstanceName,
			Image:        resetCtx.Instance.Image,
			InstanceType: resetCtx.Instance.InstanceType,
			CPU:          fmt.Sprintf("%d", resetCtx.Instance.CPU),
			Memory:       fmt.Sprintf("%dm", resetCtx.Instance.Memory),
			Disk:         fmt.Sprintf("%dm", resetCtx.Instance.Disk),
			Env:          map[string]string{"RESET_OPERATION": "true"},
			Metadata: map[string]string{
				"user_level":               fmt.Sprintf("%d", userLevel),
				"bandwidth_spec":           fmt.Sprintf("%d", resetCtx.Instance.Bandwidth),
				"ipv4_port_mapping_method": resetCtx.Provider.IPv4PortMappingMethod,
				"ipv6_port_mapping_method": resetCtx.Provider.IPv6PortMappingMethod,
				"network_type":             resetCtx.Instance.NetworkType, // 继承原实例的网络类型（而非Provider默认类型）
				"instance_id":              fmt.Sprintf("%d", resetCtx.NewInstanceID),
				"provider_id":              fmt.Sprintf("%d", resetCtx.Provider.ID),
				"reset_from_instance_id":   fmt.Sprintf("%d", resetCtx.OldInstanceID),
			},
		},
		SystemImageID: resetCtx.SystemImage.ID,
	}
	applyIPv6AllocationMetadata(createReq.InstanceConfig.Metadata, resetCtx.NewIPv6Metadata)
	if utils.SupportsLXDContainerOptions(resetCtx.Provider.Type, resetCtx.Instance.InstanceType) {
		createReq.InstanceConfig.Privileged = boolPtr(resetCtx.Provider.ContainerPrivileged)
		createReq.InstanceConfig.AllowNesting = boolPtr(resetCtx.Provider.ContainerAllowNesting)
		createReq.InstanceConfig.EnableLXCFS = boolPtr(resetCtx.Provider.ContainerEnableLXCFS)
		createReq.InstanceConfig.CPUAllowance = stringPtr(resetCtx.Provider.ContainerCPUAllowance)
		createReq.InstanceConfig.MemorySwap = boolPtr(resetCtx.Provider.ContainerMemorySwap)
		createReq.InstanceConfig.MaxProcesses = intPtr(resetCtx.Provider.ContainerMaxProcesses)
		createReq.InstanceConfig.DiskIOLimit = stringPtr(resetCtx.Provider.ContainerDiskIOLimit)
		createReq.InstanceConfig.GpuEnabled = resetCtx.Provider.GpuEnabled
		createReq.InstanceConfig.GpuDeviceIds = resetCtx.Provider.GpuDeviceIds
	}
	if strings.EqualFold(resetCtx.Instance.InstanceType, "vm") {
		createReq.InstanceConfig.ReadIOLimit = stringPtr(resetCtx.Provider.VMReadIOLimit)
		createReq.InstanceConfig.WriteIOLimit = stringPtr(resetCtx.Provider.VMWriteIOLimit)
	} else {
		createReq.InstanceConfig.ReadIOLimit = stringPtr(resetCtx.Provider.ContainerReadIOLimit)
		createReq.InstanceConfig.WriteIOLimit = stringPtr(resetCtx.Provider.ContainerWriteIOLimit)
	}

	// 容器类Provider（docker/podman/containerd）端口映射特殊处理
	// 这些Provider通过 -p 标志在创建时绑定端口，需要将端口信息写入创建请求
	if utils.UsesContainerRuntimePorts(resetCtx.Provider.Type, resetCtx.Instance.InstanceType) && len(resetCtx.OldPortMappings) > 0 {
		var ports []string
		for _, oldPort := range resetCtx.OldPortMappings {
			if oldPort.Protocol == "both" {
				ports = append(ports,
					fmt.Sprintf("0.0.0.0:%d:%d/tcp", oldPort.HostPort, oldPort.GuestPort),
					fmt.Sprintf("0.0.0.0:%d:%d/udp", oldPort.HostPort, oldPort.GuestPort))
			} else {
				ports = append(ports,
					fmt.Sprintf("0.0.0.0:%d:%d/%s", oldPort.HostPort, oldPort.GuestPort, oldPort.Protocol))
			}
		}
		createReq.InstanceConfig.Ports = ports
	}

	// VM-only providers may consume positional ports: [sshPort, startPort, endPort].
	if utils.UsesVMPositionalPorts(resetCtx.Provider.Type, resetCtx.Instance.InstanceType) && len(resetCtx.OldPortMappings) > 0 {
		var sshPort, startPort, endPort int
		for _, oldPort := range resetCtx.OldPortMappings {
			if oldPort.IsSSH {
				sshPort = oldPort.HostPort
			} else {
				if startPort == 0 || oldPort.HostPort < startPort {
					startPort = oldPort.HostPort
				}
				if oldPort.HostPort > endPort {
					endPort = oldPort.HostPort
				}
			}
		}
		// 如果没有找到非SSH端口，使用SSH端口作为起始
		if startPort == 0 {
			startPort = sshPort
		}
		if endPort == 0 {
			endPort = startPort
		}
		createReq.InstanceConfig.Ports = []string{
			fmt.Sprintf("%d", sshPort),
			fmt.Sprintf("%d", startPort),
			fmt.Sprintf("%d", endPort),
		}
	}

	// 调用Provider创建实例（根据Provider的ExecutionRule配置自动选择API或SSH）
	providerApiService := &provider2.ProviderApiService{}
	if err := providerApiService.CreateInstanceByProviderID(ctx, resetCtx.Provider.ID, createReq); err != nil {
		// 创建失败，更新实例状态为failed，但不回滚数据库（保留记录供排查）
		// 使用独立的background context，避免ctx已被取消时无法更新状态
		s.dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
			return tx.Model(&providerModel.Instance{}).Where("id = ?", resetCtx.NewInstanceID).
				Update("status", "failed").Error
		})
		return fmt.Errorf("Provider创建实例失败: %v", err)
	}

	// 等待实例启动：QEMU/KubeVirt虚拟机启动慢，需要更长等待
	instanceStartWait := 15 * time.Second
	switch resetCtx.Provider.Type {
	case "qemu", "vmware", "virtualbox", "multipass", "vagrant":
		instanceStartWait = 120 * time.Second
	case "proxmox":
		instanceStartWait = 60 * time.Second
	}
	time.Sleep(instanceStartWait)

	// 确保实例运行
	if prov, _, err := providerApiService.GetProviderByID(resetCtx.Provider.ID); err == nil {
		if instance, err := prov.GetInstance(ctx, resetCtx.NewProviderInstanceID); err == nil {
			if instance.Status != "running" {
				global.APP_LOG.Debug("实例未运行，尝试启动",
					zap.String("instanceName", resetCtx.NewProviderInstanceID),
					zap.String("status", instance.Status))
				if err := prov.StartInstance(ctx, resetCtx.NewProviderInstanceID); err != nil {
					global.APP_LOG.Warn("启动实例失败", zap.Error(err))
				} else {
					time.Sleep(10 * time.Second)
				}
			}
		}
	}

	global.APP_LOG.Info("新实例创建完成",
		zap.Uint("newInstanceId", resetCtx.NewInstanceID),
		zap.String("instanceName", resetCtx.OldInstanceName))

	return nil
}

// resetTask_SetPassword 阶段5: 设置新密码
func (s *TaskService) resetTask_SetPassword(ctx context.Context, task *adminModel.Task, resetCtx *ResetTaskContext) error {
	s.updateTaskProgress(task.ID, 70, "step.settingPassword")

	// 生成新密码
	if strings.TrimSpace(resetCtx.NewPassword) == "" {
		resetCtx.NewPassword = utils.GenerateStrongPassword(12)
	}

	// 获取内网IP
	providerApiService := &provider2.ProviderApiService{}
	prov, _, err := providerApiService.GetProviderByID(resetCtx.Provider.ID)
	if err == nil {
		resetCtx.NewPrivateIP = getInstancePrivateIP(ctx, prov, resetCtx.Provider.Type, resetCtx.NewProviderInstanceID)
	}

	// 设置密码（带重试）
	providerService := provider2.GetProviderService()
	maxRetries := 3
	var lastErr error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		if attempt > 1 {
			time.Sleep(time.Duration(attempt*3) * time.Second)
		}

		err := providerService.SetInstancePassword(ctx, resetCtx.Provider.ID, resetCtx.NewProviderInstanceID, resetCtx.NewPassword)
		if err != nil {
			lastErr = err
			global.APP_LOG.Warn("设置密码失败，准备重试",
				zap.Int("attempt", attempt),
				zap.Error(err))
			continue
		}

		global.APP_LOG.Debug("密码设置成功",
			zap.Uint("instanceId", resetCtx.NewInstanceID),
			zap.Int("attempt", attempt))
		return nil
	}

	// 所有重试失败时不写入固定默认口令，避免误导用户或暴露弱凭据。
	global.APP_LOG.Warn("设置密码失败，未记录新密码",
		zap.Error(lastErr))
	resetCtx.NewPassword = ""

	return lastErr
}

// resetTask_UpdateInstanceInfo 阶段6: 更新实例信息并确认配额
func (s *TaskService) resetTask_UpdateInstanceInfo(ctx context.Context, task *adminModel.Task, resetCtx *ResetTaskContext) error {
	s.updateTaskProgress(task.ID, 80, "step.updatingInstanceInfo")

	// 使用短事务更新实例信息和确认配额
	err := s.dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		updates := map[string]interface{}{
			"status":   "running",
			"username": "root",
		}
		if resetCtx.NewPassword != "" {
			updates["password"] = resetCtx.NewPassword
		}

		if resetCtx.NewPrivateIP != "" {
			updates["private_ip"] = resetCtx.NewPrivateIP
		}

		if err := tx.Model(&providerModel.Instance{}).Where("id = ?", resetCtx.NewInstanceID).
			Updates(updates).Error; err != nil {
			return fmt.Errorf("更新实例信息失败: %v", err)
		}

		// 确认待确认配额（将 pending_quota 转为 used_quota）
		quotaService := resources.NewQuotaService()
		resourceUsage := resources.ResourceUsage{
			CPU:       resetCtx.Instance.CPU,
			Memory:    resetCtx.Instance.Memory,
			Disk:      resetCtx.Instance.Disk,
			Bandwidth: resetCtx.Instance.Bandwidth,
		}

		if resetCtx.OriginalUserID != 0 {
			if err := quotaService.ConfirmPendingQuota(tx, resetCtx.OriginalUserID, resourceUsage); err != nil {
				return fmt.Errorf("确认配额失败: %v", err)
			}
		} else {
			global.APP_LOG.Debug("实例无用户归属，跳过待确认配额确认",
				zap.Uint("taskId", task.ID),
				zap.Uint("newInstanceId", resetCtx.NewInstanceID))
		}

		return nil
	})

	if err != nil {
		return err
	}

	global.APP_LOG.Info("实例信息已更新并确认配额",
		zap.Uint("instanceId", resetCtx.NewInstanceID))

	return nil
}
