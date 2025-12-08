package traffic

import (
	"fmt"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/monitoring"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// ClearService 流量清空服务
type ClearService struct{}

// NewClearService 创建流量清空服务
func NewClearService() *ClearService {
	return &ClearService{}
}

// ClearUserTrafficRecords 清空用户的所有流量历史记录
// 这会删除 pmacct_traffic_records 表中该用户的所有记录
// 这是管理员功能，用于清空用户累积的流量统计，重新计数
func (s *ClearService) ClearUserTrafficRecords(userID uint) (int64, error) {
	var deletedCount int64

	// 在事务中执行删除操作
	err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
		// 删除该用户的所有流量记录（硬删除）
		result := tx.Unscoped().Where("user_id = ?", userID).Delete(&monitoring.PmacctTrafficRecord{})
		if result.Error != nil {
			return fmt.Errorf("删除用户流量记录失败: %w", result.Error)
		}

		deletedCount = result.RowsAffected

		// 更新该用户所有实例的last_sync时间为当前时间（包含软删除的实例）
		// 这样下次采集时会从当前时间开始，避免重复采集已删除的历史数据
		// 注意：需要使用 Unscoped 来包含软删除的实例
		var instanceIDs []uint
		if err := global.APP_DB.Unscoped().Table("instances").Where("user_id = ?", userID).Pluck("id", &instanceIDs).Error; err != nil {
			return fmt.Errorf("获取用户实例列表失败: %w", err)
		}
		if len(instanceIDs) > 0 {
			if err := tx.Model(&monitoring.PmacctMonitor{}).
				Where("instance_id IN ?", instanceIDs).
				Update("last_sync", time.Now()).Error; err != nil {
				return fmt.Errorf("更新实例同步时间失败: %w", err)
			}
		}

		return nil
	})

	if err != nil {
		global.APP_LOG.Error("清空用户流量记录失败",
			zap.Uint("userID", userID),
			zap.Error(err))
		return 0, err
	}

	global.APP_LOG.Info("成功清空用户流量记录",
		zap.Uint("userID", userID),
		zap.Int64("deletedCount", deletedCount))

	return deletedCount, nil
}

// ClearInstanceTrafficRecords 清空实例的所有流量历史记录
func (s *ClearService) ClearInstanceTrafficRecords(instanceID uint) (int64, error) {
	var deletedCount int64

	err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
		// 删除该实例的所有流量记录（硬删除）
		result := tx.Unscoped().Where("instance_id = ?", instanceID).Delete(&monitoring.PmacctTrafficRecord{})
		if result.Error != nil {
			return fmt.Errorf("删除实例流量记录失败: %w", result.Error)
		}

		deletedCount = result.RowsAffected

		// 更新实例的last_sync时间
		if err := tx.Model(&monitoring.PmacctMonitor{}).
			Where("instance_id = ?", instanceID).
			Update("last_sync", time.Now()).Error; err != nil {
			return fmt.Errorf("更新实例同步时间失败: %w", err)
		}

		return nil
	})

	if err != nil {
		global.APP_LOG.Error("清空实例流量记录失败",
			zap.Uint("instanceID", instanceID),
			zap.Error(err))
		return 0, err
	}

	global.APP_LOG.Info("成功清空实例流量记录",
		zap.Uint("instanceID", instanceID),
		zap.Int64("deletedCount", deletedCount))

	return deletedCount, nil
}

// ClearProviderTrafficRecords 清空Provider的所有流量历史记录
func (s *ClearService) ClearProviderTrafficRecords(providerID uint) (int64, error) {
	var deletedCount int64

	err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
		// 删除该Provider的所有流量记录（硬删除）
		result := tx.Unscoped().Where("provider_id = ?", providerID).Delete(&monitoring.PmacctTrafficRecord{})
		if result.Error != nil {
			return fmt.Errorf("删除Provider流量记录失败: %w", result.Error)
		}

		deletedCount = result.RowsAffected

		// 更新该Provider所有实例的last_sync时间
		if err := tx.Model(&monitoring.PmacctMonitor{}).
			Where("provider_id = ?", providerID).
			Update("last_sync", time.Now()).Error; err != nil {
			return fmt.Errorf("更新Provider实例同步时间失败: %w", err)
		}

		return nil
	})

	if err != nil {
		global.APP_LOG.Error("清空Provider流量记录失败",
			zap.Uint("providerID", providerID),
			zap.Error(err))
		return 0, err
	}

	global.APP_LOG.Info("成功清空Provider流量记录",
		zap.Uint("providerID", providerID),
		zap.Int64("deletedCount", deletedCount))

	return deletedCount, nil
}

// ClearOldTrafficRecords 清理旧的流量记录（保留指定天数）
func (s *ClearService) ClearOldTrafficRecords(retentionDays int) (int64, error) {
	cutoffDate := time.Now().AddDate(0, 0, -retentionDays)

	result := global.APP_DB.Where("stamp_inserted < ?", cutoffDate).
		Delete(&monitoring.PmacctTrafficRecord{})

	if result.Error != nil {
		global.APP_LOG.Error("清理旧流量记录失败",
			zap.Int("retentionDays", retentionDays),
			zap.Time("cutoffDate", cutoffDate),
			zap.Error(result.Error))
		return 0, result.Error
	}

	global.APP_LOG.Info("成功清理旧流量记录",
		zap.Int("retentionDays", retentionDays),
		zap.Time("cutoffDate", cutoffDate),
		zap.Int64("deletedCount", result.RowsAffected))

	return result.RowsAffected, nil
}
