package resources

import (
	"oneclickvirt/global"
	"oneclickvirt/model/provider"
	"oneclickvirt/model/user"

	"go.uber.org/zap"
)

type SystemStatsService struct{}

// GetInstanceStats 获取实例统计信息
func (s *SystemStatsService) GetInstanceStats() (map[string]interface{}, error) {
	var total, running, stopped int64

	// 统计总实例数
	if err := global.APP_DB.Model(&provider.Instance{}).Where("soft_deleted = ?", false).Count(&total).Error; err != nil {
		return nil, err
	}

	// 统计运行中的实例
	if err := global.APP_DB.Model(&provider.Instance{}).Where("soft_deleted = ? AND status = ?", false, "running").Count(&running).Error; err != nil {
		return nil, err
	}

	// 统计已停止的实例
	if err := global.APP_DB.Model(&provider.Instance{}).Where("soft_deleted = ? AND status = ?", false, "stopped").Count(&stopped).Error; err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"total":   total,
		"running": running,
		"stopped": stopped,
	}, nil
}

// GetUserStats 获取用户统计信息
func (s *SystemStatsService) GetUserStats() (map[string]interface{}, error) {
	var totalUsers, activeUsers, disabledUsers int64

	// 统计总用户数
	if err := global.APP_DB.Model(&user.User{}).Count(&totalUsers).Error; err != nil {
		return nil, err
	}

	// 统计活跃用户数
	if err := global.APP_DB.Model(&user.User{}).Where("status = ?", 1).Count(&activeUsers).Error; err != nil {
		return nil, err
	}

	// 统计禁用用户数
	if err := global.APP_DB.Model(&user.User{}).Where("status = ?", 0).Count(&disabledUsers).Error; err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"total":    totalUsers,
		"active":   activeUsers,
		"disabled": disabledUsers,
	}, nil
}

// CheckUserExists 检查是否存在用户
func (s *SystemStatsService) CheckUserExists() (bool, error) {
	var count int64
	if err := global.APP_DB.Model(&user.User{}).Count(&count).Error; err != nil {
		global.APP_LOG.Warn("检查用户数据失败", zap.Error(err))
		return false, err
	}
	return count > 0, nil
}
