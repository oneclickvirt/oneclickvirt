package database

import (
	"fmt"
	"oneclickvirt/global"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// FixDuplicateTrafficHistory 确认 instance_traffic_histories 表中的重复数据
// 这个函数用于清理老数据库中可能存在的重复记录
// 保留 ID 最小的记录，删除其他重复项
func (ds *DatabaseService) FixDuplicateTrafficHistory() error {
	db := ds.getDB()
	if db == nil {
		return fmt.Errorf("数据库连接不可用")
	}

	// 防御性检查：表不存在时直接跳过（全新数据库无需确认）
	if !db.Migrator().HasTable("instance_traffic_histories") {
		global.APP_LOG.Info("instance_traffic_histories 表不存在，跳过重复数据检查（全新数据库）")
		return nil
	}

	global.APP_LOG.Info("开始检查并确认 instance_traffic_histories 表中的重复数据...")

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
		global.APP_LOG.Info("未发现重复数据，无需确认")
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

// FixAllDuplicateData 确认所有可能存在重复数据的表
func (ds *DatabaseService) FixAllDuplicateData() error {
	// 修复流量历史表重复数据
	if err := ds.FixDuplicateTrafficHistory(); err != nil {
		return err
	}
	// 迁移 ports 表唯一索引：将 (provider_id, host_port) 升级为 (provider_id, host_port, deleted_at)
	// 解决 GORM 软删除记录占用唯一索引槽位的问题
	if err := ds.MigratePortsIndex(); err != nil {
		return err
	}
	// 迁移 users 表 OAuth2 列名：将 GORM 默认生成的 o_auth2_* 重命名为 oauth2_*
	// 解决显式 column tag 与 GORM 默认命名策略不一致导致的"Unknown column"错误
	if err := ds.FixOAuth2ColumnNames(); err != nil {
		return err
	}
	return nil
}

// MigratePortsIndex 将 ports 表的唯一索引从双列 (provider_id, host_port)
// 迁移到三列 (provider_id, host_port, deleted_at)，使 GORM 软删除记录不再占用唯一索引槽位。
// 幂等操作：若新索引已存在或旧索引不存在，均安全跳过。
func (ds *DatabaseService) MigratePortsIndex() error {
	db := ds.getDB()
	if db == nil {
		return fmt.Errorf("数据库连接不可用")
	}

	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("获取底层数据库连接失败: %w", err)
	}

	// 防御性检查：表不存在时跳过
	if !db.Migrator().HasTable("ports") {
		global.APP_LOG.Info("ports 表不存在，跳过索引迁移（全新数据库）")
		return nil
	}

	// 检查 idx_provider_host_port 索引的列数
	var columnCount int
	checkSQL := `
		SELECT COUNT(*) FROM information_schema.STATISTICS
		WHERE table_schema = DATABASE()
		  AND table_name = 'ports'
		  AND index_name = 'idx_provider_host_port'
	`
	row := sqlDB.QueryRow(checkSQL)
	if err := row.Scan(&columnCount); err != nil {
		return fmt.Errorf("检查 ports 索引失败: %w", err)
	}

	// 如果索引已经是 3 列（包含 deleted_at），说明已迁移，跳过
	if columnCount == 3 {
		global.APP_LOG.Info("ports 表索引已包含 deleted_at，无需迁移")
		return nil
	}

	if columnCount == 2 {
		global.APP_LOG.Info("发现旧的 ports 双列唯一索引，开始迁移到三列索引（含 deleted_at）")

		// 步骤1：硬删除所有软删除记录（它们已被标记删除，不应继续占用端口槽位）
		cleanResult, err := sqlDB.Exec("DELETE FROM ports WHERE deleted_at IS NOT NULL")
		if err != nil {
			global.APP_LOG.Warn("清理 ports 软删除记录失败", zap.Error(err))
		} else if rows, _ := cleanResult.RowsAffected(); rows > 0 {
			global.APP_LOG.Info("已清理 ports 软删除记录",
				zap.Int64("count", rows))
		}

		// 步骤2：删除旧的 2 列唯一索引
		if _, err := sqlDB.Exec("ALTER TABLE ports DROP INDEX `idx_provider_host_port`"); err != nil {
			return fmt.Errorf("删除旧索引 idx_provider_host_port 失败: %w", err)
		}
		global.APP_LOG.Info("旧索引 idx_provider_host_port 已删除")

		// 步骤3：创建新的 3 列唯一索引 (provider_id, host_port, deleted_at)
		createSQL := "ALTER TABLE ports ADD UNIQUE INDEX `idx_provider_host_port` (`provider_id`, `host_port`, `deleted_at`)"
		if _, err := sqlDB.Exec(createSQL); err != nil {
			return fmt.Errorf("创建新索引 idx_provider_host_port 失败: %w", err)
		}
		global.APP_LOG.Info("新索引 idx_provider_host_port (provider_id, host_port, deleted_at) 已创建")

		return nil
	}

	// 索引不存在（全新安装），AutoMigrate 会根据 struct tag 自动创建，无需处理
	global.APP_LOG.Info("ports 表尚未创建 idx_provider_host_port 索引，将由 AutoMigrate 创建")
	return nil
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
