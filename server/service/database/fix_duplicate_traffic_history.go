package database

import (
	"fmt"
	"oneclickvirt/global"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// FixDuplicateTrafficHistory 修复 instance_traffic_histories 表中的重复数据
// 这个函数用于清理老数据库中可能存在的重复记录
// 保留 ID 最小的记录，删除其他重复项
func (ds *DatabaseService) FixDuplicateTrafficHistory() error {
	db := ds.getDB()
	if db == nil {
		return fmt.Errorf("数据库连接不可用")
	}

	// 防御性检查：表不存在时直接跳过（全新数据库无需修复）
	if !db.Migrator().HasTable("instance_traffic_histories") {
		global.APP_LOG.Info("instance_traffic_histories 表不存在，跳过重复数据检查（全新数据库）")
		return nil
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

// MigrateSystemConfigIndex 将 system_configs 表的唯一索引从单列 idx_system_configs_key
// 迁移到复合列 idx_system_configs_cat_key（category + key），允许不同 category 共用相同 key 名。
// 幂等操作：若旧索引不存在或新索引已存在，均安全跳过。
func (ds *DatabaseService) MigrateSystemConfigIndex(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("数据库连接不可用")
	}

	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("获取底层数据库连接失败: %w", err)
	}

	// 检查旧的单列唯一索引是否存在
	var oldIndexCount int
	checkSQL := `
		SELECT COUNT(*) FROM information_schema.STATISTICS
		WHERE table_schema = DATABASE()
		  AND table_name = 'system_configs'
		  AND index_name = 'idx_system_configs_key'
	`
	row := sqlDB.QueryRow(checkSQL)
	if err := row.Scan(&oldIndexCount); err != nil {
		return fmt.Errorf("检查旧索引失败: %w", err)
	}

	if oldIndexCount > 0 {
		global.APP_LOG.Info("发现旧的 system_configs 单列唯一索引，开始迁移到复合索引")
		if _, err := sqlDB.Exec("ALTER TABLE system_configs DROP INDEX `idx_system_configs_key`"); err != nil {
			return fmt.Errorf("删除旧索引 idx_system_configs_key 失败: %w", err)
		}
		global.APP_LOG.Info("旧索引 idx_system_configs_key 已删除")
	}

	return nil
}
