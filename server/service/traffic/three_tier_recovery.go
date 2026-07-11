package traffic

import (
	"context"
	"fmt"

	"oneclickvirt/global"
	"oneclickvirt/model/provider"

	"go.uber.org/zap"
)

// RecoverTrafficStoppedInstances 在流量周期重置或流量限制解除后恢复由流量策略自动停机的实例。
func (s *ThreeTierLimitService) RecoverTrafficStoppedInstances(ctx context.Context) error {
	const batchSize = 200
	activeTaskTypes := []string{"start", "stop", "restart", "reset", "rebuild", "delete", "reset-password"}
	activeTaskStatuses := []string{"pending", "processing", "running", "cancelling"}
	totalRecovered := 0

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

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
			break
		}

		if err := s.batchCreateStartTasks(instances, "流量限制已解除，自动恢复因流量策略停机的实例"); err != nil {
			return fmt.Errorf("创建流量自动恢复启动任务失败: %w", err)
		}

		totalRecovered += len(instances)
	}

	if totalRecovered > 0 {
		global.APP_LOG.Info("已提交流量限制解除后的实例自动恢复任务",
			zap.Int("instanceCount", totalRecovered))
	}

	return nil
}
