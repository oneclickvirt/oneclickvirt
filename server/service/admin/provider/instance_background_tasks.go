package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/model/common"
	providerModel "oneclickvirt/model/provider"
	runtimeProvider "oneclickvirt/service/provider"
	"oneclickvirt/service/task"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

var providerTaskCreateMu sync.Mutex

const (
	taskTypeProviderInstanceSync  = "provider-instance-sync"
	taskTypeProviderOrphanCleanup = "provider-orphan-cleanup"
	taskTypeProviderHealthCheck   = "provider-health-check"
	taskTypeProviderIOLimitSync   = "provider-io-limit-sync"
	taskTypeProviderRuntimeReload = "provider-runtime-reload"
	taskTypeProviderDelete        = "provider-delete"
)

type InstanceSyncTaskOptions struct {
	AutoImport      bool `json:"autoImport"`
	AutoAdjustQuota bool `json:"autoAdjustQuota"`
	AdminUserID     uint `json:"adminUserId"`
}

type providerTaskPayload struct {
	InstanceSyncTaskOptions
	ForceRefresh bool `json:"forceRefresh,omitempty"`
	ForceDelete  bool `json:"forceDelete,omitempty"`
}

func init() {
	task.RegisterExternalTaskHandler(taskTypeProviderInstanceSync, executeProviderInstanceSyncTask)
	task.RegisterExternalTaskHandler(taskTypeProviderOrphanCleanup, executeProviderOrphanCleanupTask)
	task.RegisterExternalTaskHandler(taskTypeProviderHealthCheck, executeProviderHealthCheckTask)
	task.RegisterExternalTaskHandler(taskTypeProviderIOLimitSync, executeProviderIOLimitSyncTask)
	task.RegisterExternalTaskHandler(taskTypeProviderRuntimeReload, executeProviderRuntimeReloadTask)
	task.RegisterExternalTaskHandler(taskTypeProviderDelete, executeProviderDeleteTask)
}

func (s *Service) CreateInstanceSyncTask(providerID, requestedBy uint, options InstanceSyncTaskOptions) (*adminModel.Task, error) {
	var provider providerModel.Provider
	if err := global.APP_DB.Select("id", "instance_discovery_enabled", "discovery_owner_user_id", "discovery_auto_adjust").
		First(&provider, providerID).Error; err != nil {
		return nil, fmt.Errorf("读取Provider实例同步配置失败: %w", err)
	}
	if !provider.InstanceDiscoveryEnabled {
		var importedCount int64
		if err := global.APP_DB.Model(&providerModel.Instance{}).
			Where("provider_id = ? AND (is_imported = ? OR imported_at IS NOT NULL)", providerID, true).Count(&importedCount).Error; err != nil {
			return nil, fmt.Errorf("检查历史导入实例失败: %w", err)
		}
		if importedCount == 0 {
			return nil, common.NewError(common.CodeValidationError, "仅录入为非纯净节点的Provider可以同步实例")
		}
		// 升级兼容：旧版本没有持久能力字段，以已有导入实例为证据一次性回填。
		if err := global.APP_DB.Model(&providerModel.Provider{}).Where("id = ?", providerID).
			Update("instance_discovery_enabled", true).Error; err != nil {
			return nil, fmt.Errorf("回填实例同步能力失败: %w", err)
		}
	}
	if options.AdminUserID == 0 {
		options.AdminUserID = provider.DiscoveryOwnerUserID
	}
	if options.AdminUserID == 0 {
		options.AdminUserID = requestedBy
	}
	if requestedBy == 0 {
		requestedBy = options.AdminUserID
	}
	if options.AdminUserID == 0 || requestedBy == 0 {
		return nil, common.NewError(common.CodeValidationError, "实例同步任务缺少有效管理员用户")
	}
	return createUniqueProviderTask(providerID, requestedBy, taskTypeProviderInstanceSync, providerTaskPayload{InstanceSyncTaskOptions: options}, 1800)
}

func (s *Service) CreateConfiguredInstanceSyncTask(providerID, requestedBy uint) (*adminModel.Task, error) {
	var provider providerModel.Provider
	if err := global.APP_DB.Select("id", "instance_discovery_enabled", "discovery_owner_user_id", "discovery_auto_adjust").
		First(&provider, providerID).Error; err != nil {
		return nil, fmt.Errorf("读取Provider实例同步配置失败: %w", err)
	}
	return s.CreateInstanceSyncTask(providerID, requestedBy, InstanceSyncTaskOptions{
		// 管理员显式点击“同步实例”即授权导入；DiscoveryAutoImport 只控制
		// 录入/Agent首次连接时是否自动导入。
		AutoImport:      true,
		AutoAdjustQuota: provider.DiscoveryAutoAdjust,
		AdminUserID:     provider.DiscoveryOwnerUserID,
	})
}

func (s *Service) CreateOrphanCleanupTask(providerID, requestedBy uint) (*adminModel.Task, error) {
	return createUniqueProviderTask(providerID, requestedBy, taskTypeProviderOrphanCleanup, providerTaskPayload{}, 3600)
}

func (s *Service) CreateHealthCheckTask(providerID, requestedBy uint, forceRefresh bool) (*adminModel.Task, error) {
	var count int64
	if err := global.APP_DB.Model(&providerModel.Provider{}).Where("id = ?", providerID).Count(&count).Error; err != nil {
		return nil, fmt.Errorf("检查Provider失败: %w", err)
	}
	if count == 0 {
		return nil, common.NewError(common.CodeNotFound, "Provider不存在")
	}
	return createUniqueProviderTask(providerID, requestedBy, taskTypeProviderHealthCheck, providerTaskPayload{ForceRefresh: forceRefresh}, 900)
}

func (s *Service) CreateIOLimitSyncTask(providerID, requestedBy uint) (*adminModel.Task, error) {
	return createUniqueProviderTask(providerID, requestedBy, taskTypeProviderIOLimitSync, providerTaskPayload{}, 600)
}

// CreateRuntimeReloadTask refreshes the in-memory Provider connection after a
// configuration update. A pending task reads the latest DB row when it starts,
// so it can be reused. If one is already processing/running, a new pending task is
// queued behind it to guarantee rapid successive edits converge to the newest
// configuration without blocking the HTTP request on SSH.
func (s *Service) CreateRuntimeReloadTask(providerID, requestedBy uint) (*adminModel.Task, error) {
	return createConvergentProviderTask(providerID, requestedBy, taskTypeProviderRuntimeReload, providerTaskPayload{}, 300)
}

// CreateDeleteTask queues both cascade and database-only deletion. Cascade
// deletion may perform one remote delete per instance and must never execute in
// the HTTP request that submitted it.
func (s *Service) CreateDeleteTask(providerID, requestedBy uint, forceDelete bool) (*adminModel.Task, error) {
	var count int64
	if err := global.APP_DB.Model(&providerModel.Provider{}).Where("id = ?", providerID).Count(&count).Error; err != nil {
		return nil, fmt.Errorf("检查Provider失败: %w", err)
	}
	if count == 0 {
		return nil, common.NewError(common.CodeNotFound, "Provider不存在")
	}
	return createUniqueProviderTask(providerID, requestedBy, taskTypeProviderDelete, providerTaskPayload{ForceDelete: forceDelete}, 7200)
}

func (s *Service) CreateTrafficMonitorToggleTask(providerID uint, enabled bool) (*adminModel.Task, error) {
	providerTaskCreateMu.Lock()
	defer providerTaskCreateMu.Unlock()
	var provider providerModel.Provider
	if err := global.APP_DB.Select("id", "owner_admin_id").First(&provider, providerID).Error; err != nil {
		return nil, err
	}
	userID, err := resolveProviderTaskUserID(provider.OwnerAdminID)
	if err != nil {
		return nil, err
	}
	operation := "disable"
	taskType := "disable_all"
	adminTaskType := "traffic-monitor-disable"
	if enabled {
		operation = "enable"
		taskType = "enable_all"
		adminTaskType = "traffic-monitor-enable"
	}
	var activeCount int64
	if err := global.APP_DB.Model(&adminModel.Task{}).
		Where("provider_id = ? AND task_type = ? AND status IN ?", providerID, adminTaskType,
			[]string{"pending", "processing", "running", "cancelling"}).Count(&activeCount).Error; err != nil {
		return nil, err
	}
	if activeCount > 0 {
		return nil, common.NewError(common.CodeConflict, "该节点已有流量监控后台任务")
	}
	trafficTask := adminModel.TrafficMonitorTask{
		ProviderID: providerID,
		TaskType:   taskType,
		Status:     "pending",
		Progress:   0,
		Message:    "任务已创建，等待执行",
	}
	if err := global.APP_DB.Create(&trafficTask).Error; err != nil {
		return nil, err
	}
	created, err := task.CreateTrafficMonitorAdminTask(providerID, trafficTask.ID, operation, userID)
	if err != nil {
		_ = global.APP_DB.Model(&trafficTask).Updates(map[string]interface{}{"status": "failed", "message": err.Error()}).Error
		return nil, err
	}
	_ = global.APP_DB.Model(&trafficTask).Update("admin_task_id", created.ID).Error
	return created, nil
}

func createUniqueProviderTask(providerID, requestedBy uint, taskType string, payload providerTaskPayload, timeout int) (*adminModel.Task, error) {
	providerTaskCreateMu.Lock()
	defer providerTaskCreateMu.Unlock()
	if providerID == 0 || requestedBy == 0 {
		return nil, common.NewError(common.CodeValidationError, "后台任务参数无效")
	}
	var count int64
	if err := global.APP_DB.Model(&adminModel.Task{}).
		Where("provider_id = ? AND task_type = ? AND status IN ?", providerID, taskType, []string{"pending", "processing", "running", "cancelling"}).
		Count(&count).Error; err != nil {
		return nil, fmt.Errorf("检查重复后台任务失败: %w", err)
	}
	if count > 0 {
		return nil, common.NewError(common.CodeConflict, "该节点已有同类后台任务，请在任务列表查看进度")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化后台任务参数失败: %w", err)
	}
	providerIDCopy := providerID
	taskService := task.GetTaskService()
	created, err := taskService.CreateTask(requestedBy, &providerIDCopy, nil, taskType, string(data), timeout)
	if err != nil {
		return nil, err
	}
	// 只持久化 pending 并非阻塞触发调度；不要在 HTTP 请求中等待工作池
	// 入队（队列满时 StartTask 最长会等待 30 秒）。
	if global.APP_SCHEDULER != nil {
		global.APP_SCHEDULER.TriggerTaskProcessing()
	}
	return created, nil
}

func createConvergentProviderTask(providerID, requestedBy uint, taskType string, payload providerTaskPayload, timeout int) (*adminModel.Task, error) {
	providerTaskCreateMu.Lock()
	defer providerTaskCreateMu.Unlock()
	if providerID == 0 || requestedBy == 0 {
		return nil, common.NewError(common.CodeValidationError, "后台任务参数无效")
	}
	var queued adminModel.Task
	// Only a not-yet-started task is safe to reuse: it will read the latest DB
	// row when it eventually runs. A processing/running task may already have
	// captured the previous configuration, so a new pending task must be queued
	// behind it to converge rapid successive edits.
	err := global.APP_DB.Where("provider_id = ? AND task_type = ? AND status = ?", providerID, taskType,
		convergentTaskReusableStatus()).Order("id DESC").First(&queued).Error
	if err == nil {
		return &queued, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("检查待执行后台任务失败: %w", err)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化后台任务参数失败: %w", err)
	}
	providerIDCopy := providerID
	created, err := task.GetTaskService().CreateTask(requestedBy, &providerIDCopy, nil, taskType, string(data), timeout)
	if err != nil {
		return nil, err
	}
	if global.APP_SCHEDULER != nil {
		global.APP_SCHEDULER.TriggerTaskProcessing()
	}
	return created, nil
}

func convergentTaskReusableStatus() string {
	return "pending"
}

func resolveProviderTaskUserID(preferred uint) (uint, error) {
	if preferred > 0 {
		return preferred, nil
	}
	var user struct{ ID uint }
	if err := global.APP_DB.Table("users").Select("id").
		Where("user_type IN ?", []string{"admin", "super_admin"}).Order("id ASC").First(&user).Error; err != nil {
		return 0, fmt.Errorf("未找到可记录后台任务的管理员: %w", err)
	}
	return user.ID, nil
}

func executeProviderInstanceSyncTask(ctx context.Context, adminTask *adminModel.Task) error {
	providerID, err := providerIDFromTask(adminTask)
	if err != nil {
		return err
	}
	var payload providerTaskPayload
	if err := json.Unmarshal([]byte(adminTask.TaskData), &payload); err != nil {
		return fmt.Errorf("解析实例同步任务失败: %w", err)
	}
	utils.UpdateTaskProgress(adminTask.ID, 5, "开始扫描非纯净节点实例")
	service := NewService()

	if !payload.AutoImport {
		result, err := discoverWithRetry(ctx, service, providerID, adminTask.ID)
		if err != nil {
			return err
		}
		utils.UpdateTaskProgress(adminTask.ID, 100, fmt.Sprintf("扫描完成：远端 %d，新增 %d，已纳管 %d；当前配置未启用自动导入", result.TotalCount, result.NewInstances, result.AlreadyManaged))
		return nil
	}

	utils.UpdateTaskProgress(adminTask.ID, 20, "正在扫描并导入新增实例")
	result, err := service.ImportDiscoveredInstances(ctx, ImportOptions{
		ProviderID:      providerID,
		AdminUserID:     payload.AdminUserID,
		AutoAdjustQuota: payload.AutoAdjustQuota,
		MarkConflicts:   true,
	})
	if err != nil {
		return err
	}
	message := fmt.Sprintf("实例同步完成：导入 %d，跳过 %d，失败 %d", result.SuccessCount, result.SkippedCount, result.FailedCount)
	utils.UpdateTaskProgress(adminTask.ID, 100, message)
	if result.FailedCount > 0 {
		return errors.New(strings.Join(result.Errors, "; "))
	}
	return nil
}

func discoverWithRetry(ctx context.Context, service *Service, providerID, taskID uint) (*DiscoveryResult, error) {
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		result, err := service.DiscoverProviderInstances(ctx, providerID)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if attempt == 3 {
			break
		}
		utils.UpdateTaskProgress(taskID, 5+attempt*5, fmt.Sprintf("实例扫描失败，准备第 %d 次重试", attempt+1))
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	return nil, fmt.Errorf("实例扫描失败: %w", lastErr)
}

func executeProviderOrphanCleanupTask(ctx context.Context, adminTask *adminModel.Task) error {
	providerID, err := providerIDFromTask(adminTask)
	if err != nil {
		return err
	}
	utils.UpdateTaskProgress(adminTask.ID, 5, "开始扫描远端孤儿实例")
	result, err := NewService().CleanupOrphanInstances(ctx, providerID)
	if err != nil {
		return err
	}
	utils.UpdateTaskProgress(adminTask.ID, 100, fmt.Sprintf("孤儿清理完成：发现 %d，删除 %d，失败 %d", result.TotalOrphans, result.DeletedCount, result.FailedCount))
	if result.FailedCount > 0 {
		return fmt.Errorf("有 %d 个远端孤儿实例删除失败，请查看任务日志", result.FailedCount)
	}
	return nil
}

func executeProviderHealthCheckTask(ctx context.Context, adminTask *adminModel.Task) error {
	providerID, err := providerIDFromTask(adminTask)
	if err != nil {
		return err
	}
	var payload providerTaskPayload
	if err := json.Unmarshal([]byte(adminTask.TaskData), &payload); err != nil {
		return fmt.Errorf("解析健康检查任务失败: %w", err)
	}
	utils.UpdateTaskProgress(adminTask.ID, 10, "开始检测节点连接并同步资源")
	if err := NewService().CheckProviderHealthWithOptionsContext(ctx, providerID, payload.ForceRefresh); err != nil {
		return err
	}
	utils.UpdateTaskProgress(adminTask.ID, 100, "节点健康检查与资源同步完成")
	return nil
}

func executeProviderIOLimitSyncTask(ctx context.Context, adminTask *adminModel.Task) error {
	providerID, err := providerIDFromTask(adminTask)
	if err != nil {
		return err
	}
	utils.UpdateTaskProgress(adminTask.ID, 10, "开始同步实例IO限速")
	if err := NewService().syncProviderIOLimitsWithContext(ctx, providerID); err != nil {
		return err
	}
	utils.UpdateTaskProgress(adminTask.ID, 100, "实例IO限速同步完成")
	return nil
}

func executeProviderRuntimeReloadTask(ctx context.Context, adminTask *adminModel.Task) error {
	providerID, err := providerIDFromTask(adminTask)
	if err != nil {
		return err
	}
	utils.UpdateTaskProgress(adminTask.ID, 10, "开始刷新节点运行时连接")
	if err := runtimeProvider.GetProviderService().ReloadProviderContext(ctx, providerID); err != nil {
		return fmt.Errorf("刷新节点运行时连接失败: %w", err)
	}
	utils.UpdateTaskProgress(adminTask.ID, 100, "节点运行时连接已刷新")
	return nil
}

func executeProviderDeleteTask(ctx context.Context, adminTask *adminModel.Task) error {
	providerID, err := providerIDFromTask(adminTask)
	if err != nil {
		return err
	}
	var payload providerTaskPayload
	if strings.TrimSpace(adminTask.TaskData) != "" {
		if err := json.Unmarshal([]byte(adminTask.TaskData), &payload); err != nil {
			return fmt.Errorf("解析Provider删除任务参数失败: %w", err)
		}
	}

	var provider providerModel.Provider
	if err := global.APP_DB.Select("id", "name", "status").First(&provider, providerID).Error; err != nil {
		return fmt.Errorf("读取待删除Provider失败: %w", err)
	}
	originalStatus := provider.Status
	if err := global.APP_DB.Model(&providerModel.Provider{}).Where("id = ?", providerID).Update("status", "deleting").Error; err != nil {
		return fmt.Errorf("标记Provider删除状态失败: %w", err)
	}
	deletionSucceeded := false
	defer func() {
		if deletionSucceeded {
			return
		}
		// A failed/cancelled delete must not strand the node in a state that
		// rejects every later operation. Do not recreate a Provider that was
		// actually removed before a late cleanup error.
		_ = global.APP_DB.Model(&providerModel.Provider{}).
			Where("id = ? AND status = ?", providerID, "deleting").
			Update("status", originalStatus).Error
	}()

	utils.UpdateTaskProgress(adminTask.ID, 5, "等待节点上的其他任务结束")
	if err := waitForOtherProviderTasks(ctx, providerID, adminTask.ID); err != nil {
		return err
	}
	mode := "级联删除远端实例并清理节点"
	if payload.ForceDelete {
		mode = "仅清理节点数据库记录"
	}
	utils.UpdateTaskProgress(adminTask.ID, 15, mode)
	if err := NewService().deleteProviderWithTaskContext(ctx, providerID, payload.ForceDelete, adminTask.ID); err != nil {
		return err
	}
	deletionSucceeded = true
	utils.UpdateTaskProgress(adminTask.ID, 100, fmt.Sprintf("节点 %s 删除完成", provider.Name))
	return nil
}

func waitForOtherProviderTasks(ctx context.Context, providerID, currentTaskID uint) error {
	// A task already marked processing may still be sitting in this same
	// Provider pool behind the exclusive delete task. Waiting for it would
	// deadlock a single-worker pool, so cancel queued work first through the
	// normal task service (which also releases reservations/transitional state).
	var queued []adminModel.Task
	if err := global.APP_DB.Select("id").
		Where("provider_id = ? AND id <> ? AND status IN ?", providerID, currentTaskID,
			[]string{"pending", "processing"}).
		Find(&queued).Error; err != nil {
		return fmt.Errorf("查询节点排队任务失败: %w", err)
	}
	for _, queuedTask := range queued {
		if err := task.GetTaskService().CancelTaskByAdmin(queuedTask.ID, "Provider deletion is pending"); err != nil {
			// The worker may have moved processing -> running between the query
			// and cancellation. The loop below then waits for its real terminal
			// state, so only log this race instead of failing deletion immediately.
			global.APP_LOG.Debug("取消Provider排队任务时状态已变化",
				zap.Uint("providerID", providerID), zap.Uint("taskID", queuedTask.ID), zap.Error(err))
		}
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		var activeCount int64
		if err := global.APP_DB.Model(&adminModel.Task{}).
			Where("provider_id = ? AND id <> ? AND status IN ?", providerID, currentTaskID,
				[]string{"processing", "running", "cancelling"}).
			Count(&activeCount).Error; err != nil {
			return fmt.Errorf("检查节点活跃任务失败: %w", err)
		}
		if activeCount == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("等待节点其他任务结束失败: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func providerIDFromTask(adminTask *adminModel.Task) (uint, error) {
	if adminTask.ProviderID == nil || *adminTask.ProviderID == 0 {
		return 0, fmt.Errorf("任务缺少ProviderID")
	}
	var exists int64
	if err := global.APP_DB.Model(&providerModel.Provider{}).Where("id = ?", *adminTask.ProviderID).Count(&exists).Error; err != nil {
		return 0, err
	}
	if exists == 0 {
		return 0, gorm.ErrRecordNotFound
	}
	return *adminTask.ProviderID, nil
}
