package task

import (
	"errors"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/service/cache"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

func (s *TaskService) invalidateTaskInstanceCaches(taskID uint) {
	if global.APP_DB == nil {
		return
	}

	var task adminModel.Task
	err := global.APP_DB.Select("id, user_id, instance_id").
		Where("id = ?", taskID).
		First(&task).Error
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) && global.APP_LOG != nil {
			global.APP_LOG.Debug("查询任务缓存失效信息失败",
				zap.Uint("taskID", taskID),
				zap.Error(err))
		}
		return
	}

	cacheService := cache.GetUserCacheService()
	if task.UserID > 0 {
		cacheService.InvalidateUserCache(task.UserID)
	}
	if task.InstanceID != nil && *task.InstanceID > 0 {
		cacheService.InvalidateInstanceCache(*task.InstanceID)
	}
}
