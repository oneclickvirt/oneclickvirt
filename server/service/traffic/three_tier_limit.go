package traffic

import (
	"context"
	"fmt"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/model/provider"
	"oneclickvirt/service/taskgate"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// ThreeTierLimitService 三层级流量限制服务
// 实现实例级、用户级、Provider级的独立流量限制
type ThreeTierLimitService struct {
	service *Service
}

// NewThreeTierLimitService 创建三层级流量限制服务
func NewThreeTierLimitService() *ThreeTierLimitService {
	return &ThreeTierLimitService{
		service: NewService(),
	}
}

// TrafficLimitLevel 流量限制层级
type TrafficLimitLevel string

const (
	LimitLevelInstance TrafficLimitLevel = "instance" // 实例层级
	LimitLevelUser     TrafficLimitLevel = "user"     // 用户层级
	LimitLevelProvider TrafficLimitLevel = "provider" // Provider层级
)

// CheckAllTrafficLimits 检查所有三层级的流量限制
// 按优先级顺序检查: Provider > User > Instance
func (s *ThreeTierLimitService) CheckAllTrafficLimits(ctx context.Context) error {
	global.APP_LOG.Debug("开始三层级流量限制检查")

	// 第一层：检查Provider层级（最高优先级）
	if err := s.CheckAllProvidersTrafficLimit(ctx); err != nil {
		global.APP_LOG.Warn("Provider层级流量检查失败", zap.Error(err))
	}

	// 第二层：检查用户层级
	if err := s.CheckAllUsersTrafficLimit(ctx); err != nil {
		global.APP_LOG.Warn("用户层级流量检查失败", zap.Error(err))
	}

	// 第三层：检查实例层级（最低优先级）
	if err := s.CheckAllInstancesTrafficLimit(ctx); err != nil {
		global.APP_LOG.Warn("实例层级流量检查失败", zap.Error(err))
	}

	if err := s.RecoverTrafficStoppedInstances(ctx); err != nil {
		global.APP_LOG.Warn("恢复流量策略自动停机实例失败", zap.Error(err))
	}

	global.APP_LOG.Debug("三层级流量限制检查完成")
	return nil
}

// ============ 辅助方法 ============

// createStopTask 创建停止实例的任务
func (s *ThreeTierLimitService) createStopTask(userID, instanceID, providerID uint, message string) error {
	if err := s.createStopTaskTx(global.APP_DB, userID, instanceID, providerID, message); err != nil {
		return err
	}
	if global.APP_SCHEDULER != nil {
		global.APP_SCHEDULER.TriggerTaskProcessing()
	}
	return nil
}

// createStopTaskTx 在指定的DB/事务中创建停止任务（供事务内调用）
func (s *ThreeTierLimitService) createStopTaskTx(db *gorm.DB, userID, instanceID, providerID uint, message string) error {
	if err := taskgate.EnsureAccepting(); err != nil {
		return err
	}

	taskData := fmt.Sprintf(`{"instanceId":%d,"providerId":%d}`, instanceID, providerID)

	task := &adminModel.Task{
		TaskType:         "stop",
		Status:           "pending",
		Progress:         0,
		StatusMessage:    message,
		TaskData:         taskData,
		UserID:           userID,
		ProviderID:       &providerID,
		InstanceID:       &instanceID,
		TimeoutDuration:  600,
		IsForceStoppable: true,
		CanForceStop:     false,
	}

	return db.Create(task).Error
}

// batchCreateStopTasks 批量创建停止任务（用户层级限流）
func (s *ThreeTierLimitService) batchCreateStopTasks(userID uint, instances []provider.Instance, message string) error {
	if err := taskgate.EnsureAccepting(); err != nil {
		return err
	}

	if len(instances) == 0 {
		return nil
	}

	tasks := make([]*adminModel.Task, 0, len(instances))
	for _, instance := range instances {
		taskData := fmt.Sprintf(`{"instanceId":%d,"providerId":%d}`, instance.ID, instance.ProviderID)

		task := &adminModel.Task{
			TaskType:         "stop",
			Status:           "pending",
			Progress:         0,
			StatusMessage:    message,
			TaskData:         taskData,
			UserID:           userID,
			ProviderID:       &instance.ProviderID,
			InstanceID:       &instance.ID,
			TimeoutDuration:  600,
			IsForceStoppable: true,
			CanForceStop:     false,
		}
		tasks = append(tasks, task)
	}

	// 批量插入任务
	if err := global.APP_DB.CreateInBatches(tasks, 100).Error; err != nil {
		return err
	}

	// 触发调度器立即处理任务
	if global.APP_SCHEDULER != nil {
		global.APP_SCHEDULER.TriggerTaskProcessing()
	}

	return nil
}

// batchCreateStopTasksForProvider 批量创建停止任务（Provider层级限流）
func (s *ThreeTierLimitService) batchCreateStopTasksForProvider(providerID uint, instances []provider.Instance, message string) error {
	if err := taskgate.EnsureAccepting(); err != nil {
		return err
	}

	if len(instances) == 0 {
		return nil
	}

	tasks := make([]*adminModel.Task, 0, len(instances))
	for _, instance := range instances {
		taskData := fmt.Sprintf(`{"instanceId":%d,"providerId":%d}`, instance.ID, providerID)

		task := &adminModel.Task{
			TaskType:         "stop",
			Status:           "pending",
			Progress:         0,
			StatusMessage:    message,
			TaskData:         taskData,
			UserID:           instance.UserID,
			ProviderID:       &providerID,
			InstanceID:       &instance.ID,
			TimeoutDuration:  600,
			IsForceStoppable: true,
			CanForceStop:     false,
		}
		tasks = append(tasks, task)
	}

	// 批量插入任务
	if err := global.APP_DB.CreateInBatches(tasks, 100).Error; err != nil {
		return err
	}

	// 触发调度器立即处理任务
	if global.APP_SCHEDULER != nil {
		global.APP_SCHEDULER.TriggerTaskProcessing()
	}

	return nil
}

// batchCreateStartTasks 批量创建启动任务（流量限制解除后的自动恢复）。
func (s *ThreeTierLimitService) batchCreateStartTasks(instances []provider.Instance, message string) error {
	if err := taskgate.EnsureAccepting(); err != nil {
		return err
	}

	if len(instances) == 0 {
		return nil
	}

	tasks := make([]*adminModel.Task, 0, len(instances))
	for _, instance := range instances {
		taskData := fmt.Sprintf(`{"instanceId":%d,"providerId":%d}`, instance.ID, instance.ProviderID)
		task := &adminModel.Task{
			TaskType:         "start",
			Status:           "pending",
			Progress:         0,
			StatusMessage:    message,
			TaskData:         taskData,
			UserID:           instance.UserID,
			ProviderID:       &instance.ProviderID,
			InstanceID:       &instance.ID,
			TimeoutDuration:  600,
			IsForceStoppable: true,
			CanForceStop:     false,
		}
		tasks = append(tasks, task)
	}

	if err := global.APP_DB.CreateInBatches(tasks, 100).Error; err != nil {
		return err
	}

	if global.APP_SCHEDULER != nil {
		global.APP_SCHEDULER.TriggerTaskProcessing()
	}

	return nil
}
