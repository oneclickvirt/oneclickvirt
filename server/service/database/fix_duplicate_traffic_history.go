package database

import (
	"fmt"
	"oneclickvirt/global"

	"go.uber.org/zap"
)

// FixDuplicateTrafficHistory 修复 instance_traffic_histories 表中的重复数据
// 这个函数用于清理老数据库中可能存在的重复记录
// 保留 ID 最小的记录，删除其他重复项
func (ds *DatabaseService) FixDuplicateTrafficHistory() error {
	db := ds.getDB()
	if db == nil {
		return fmt.Errorf("数据库连接不可用")
	}

	global.APP_LOG.Info("开始检查并修复 instance_traffic_histories 表中的重复数据...")

	// 检查是否存在重复数据
	var duplicateCount int64
	checkSQL := `
		SELECT COUNT(*) as count FROM (
			SELECT instance_id, year, month, day, hour, COUNT(*) as cnt
			FROM instance_traffic_histories
			GROUP BY instance_id, year, month, day, hour
			HAVING cnt > 1
		) as duplicates
	`
	err := db.Raw(checkSQL).Scan(&duplicateCount).Error
	if err != nil {
		return fmt.Errorf("检查重复数据失败: %w", err)
	}

	if duplicateCount == 0 {
		global.APP_LOG.Info("未发现重复数据，无需修复")
		return nil
	}

	global.APP_LOG.Warn("发现重复数据组", zap.Int64("count", duplicateCount))

	// 删除重复数据，保留ID最小的记录
	// 使用临时表方法，兼容性更好
	deleteSQL := `
		DELETE t1 FROM instance_traffic_histories t1
		INNER JOIN (
			SELECT instance_id, year, month, day, hour, MIN(id) as min_id
			FROM instance_traffic_histories
			GROUP BY instance_id, year, month, day, hour
			HAVING COUNT(*) > 1
		) t2 
		ON t1.instance_id = t2.instance_id 
		AND t1.year = t2.year 
		AND t1.month = t2.month 
		AND t1.day = t2.day 
		AND t1.hour = t2.hour
		WHERE t1.id > t2.min_id
	`

	result := db.Exec(deleteSQL)
	if result.Error != nil {
		return fmt.Errorf("删除重复数据失败: %w", result.Error)
	}

	global.APP_LOG.Info("重复数据清理完成",
		zap.Int64("deleted_rows", result.RowsAffected))

	return nil
}

// FixAllDuplicateData 修复所有可能存在重复数据的表
func (ds *DatabaseService) FixAllDuplicateData() error {
	// 目前只有 instance_traffic_histories 表有此问题
	// 如果将来有其他表也需要修复，可以在这里添加
	return ds.FixDuplicateTrafficHistory()
}
