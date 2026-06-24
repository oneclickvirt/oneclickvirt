package traffic

import (
	"context"
	"fmt"

	"oneclickvirt/global"
	"oneclickvirt/model/provider"

	"go.uber.org/zap"
)

// RecoverTrafficStoppedInstances 在自然月重置或流量限制解除后恢复由流量策略自动停机的实例。
func (s *ThreeTierLimitService) RecoverTrafficStoppedInstances(ctx context.Context) error {
	const batchSize = 200
	activeTaskTypes := []string{"start", "stop", "restart", "reset", "rebuild", "delete", "reset-password"}
	activeTaskStatuses := []string{"pending", "processing", "running", "cancelling"}

	var instances []provider.Instance
	err := global.APP_DB.Table("instances").
		Select("instances.id, instances.user_id, instances.provider_id").
		Joins("LEFT JOIN users ON users.id = instances.user_id").
		Joins("LEFT JOIN providers ON providers.id = instances.provider_id").
		Where("instances.deleted_at IS NULL").
		Where("instances.status = ? AND instances.traffic_stopped = ? AND instances.traffic_limited = ?", "stopped", true, false).
		Where("COALESCE(users.traffic_limited, ?) = ? AND COALESCE(providers.traffic_limited, ?) = ?", false, false, false, false).
		Where(`NOT EXISTS (
			SELECT 1 FROM tasks
			WHERE tasks.instance_id = instances.id
			  AND tasks.task_type IN ?
			  AND tasks.status IN ?
		)`, activeTaskTypes, activeTaskStatuses).
		Order("instances.id ASC").
		Limit(batchSize).
		Find(&instances).Error
	if err != nil {
		return fmt.Errorf("查询流量自动停机实例失败: %w", err)
	}
	if len(instances) == 0 {
		return nil
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if err := s.batchCreateStartTasks(instances, "流量限制已解除，自动恢复因流量策略停机的实例"); err != nil {
		return fmt.Errorf("创建流量自动恢复启动任务失败: %w", err)
	}

	instanceIDs := make([]uint, 0, len(instances))
	for _, instance := range instances {
		instanceIDs = append(instanceIDs, instance.ID)
	}

	if err := global.APP_DB.Model(&provider.Instance{}).
		Where("id IN ?", instanceIDs).
		Updates(map[string]interface{}{
			"status":             "starting",
			"traffic_stopped":    false,
			"traffic_stopped_at": nil,
		}).Error; err != nil {
		return fmt.Errorf("更新流量自动恢复实例状态失败: %w", err)
	}

	global.APP_LOG.Info("已提交流量限制解除后的实例自动恢复任务",
		zap.Int("instanceCount", len(instances)))

	return nil
}
