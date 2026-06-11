package database

import (
	"fmt"
	"strings"

	"oneclickvirt/global"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// oauth2ColumnMapping 定义旧列名 → 新列名的映射。
// GORM 默认命名策略会将 OAuth2Avatar 等字段转换为 o_auth2_* 格式（如 o_auth2_avatar），
// 但业务代码期望使用 oauth2_* 格式（如 oauth2_avatar）。
// 该迁移将旧列名重命名为新列名，确保与显式 column tag 一致。
var oauth2ColumnMapping = map[string]string{
	"o_auth2_provider_id": "oauth2_provider_id",
	"o_auth2_uid":         "oauth2_uid",
	"o_auth2_username":    "oauth2_username",
	"o_auth2_email":       "oauth2_email",
	"o_auth2_avatar":      "oauth2_avatar",
	"o_auth2_extra":       "oauth2_extra",
}

// FixOAuth2ColumnNames 将 users 表中由 GORM 默认命名策略生成的 o_auth2_* 列
// 重命名为业务代码期望的 oauth2_* 列名。
// 该操作为幂等操作，已重命名的列不会重复处理。
func (ds *DatabaseService) FixOAuth2ColumnNames() error {
	db := ds.getDB()
	if db == nil {
		return fmt.Errorf("数据库连接不可用")
	}

	// 防御性检查：users 表不存在时直接跳过
	if !db.Migrator().HasTable("users") {
		global.APP_LOG.Info("users 表不存在，跳过 OAuth2 列名迁移（全新数据库）")
		return nil
	}

	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("获取底层数据库连接失败: %w", err)
	}

	// 获取 users 表的实际列名
	existingColumns, err := ds.getTableColumns(db, "users")
	if err != nil {
		return fmt.Errorf("获取 users 表列信息失败: %w", err)
	}

	renamedCount := 0
	for oldName, newName := range oauth2ColumnMapping {
		oldExists := existingColumns[strings.ToLower(oldName)]
		newExists := existingColumns[strings.ToLower(newName)]

		if newExists {
			// 新列名已存在，说明已迁移或通过新的 AutoMigrate 创建，跳过
			global.APP_LOG.Debug("OAuth2 列已存在，跳过重命名",
				zap.String("column", newName))
			continue
		}

		if !oldExists {
			// 旧列名也不存在，说明是全新创建或该列尚未添加，跳过
			global.APP_LOG.Debug("OAuth2 旧列不存在，跳过重命名",
				zap.String("old_column", oldName),
				zap.String("new_column", newName))
			continue
		}

		// 旧列存在、新列不存在 → 执行重命名
		renameSQL := fmt.Sprintf("ALTER TABLE users CHANGE COLUMN `%s` `%s` VARCHAR(512) DEFAULT NULL",
			oldName, newName)
		// 根据具体字段调整类型
		switch oldName {
		case "o_auth2_provider_id":
			renameSQL = fmt.Sprintf("ALTER TABLE users CHANGE COLUMN `%s` `%s` BIGINT UNSIGNED DEFAULT NULL",
				oldName, newName)
		case "o_auth2_extra":
			renameSQL = fmt.Sprintf("ALTER TABLE users CHANGE COLUMN `%s` `%s` TEXT DEFAULT NULL",
				oldName, newName)
		default:
			renameSQL = fmt.Sprintf("ALTER TABLE users CHANGE COLUMN `%s` `%s` VARCHAR(255) DEFAULT ''",
				oldName, newName)
		}

		global.APP_LOG.Info("正在重命名 OAuth2 列",
			zap.String("old", oldName),
			zap.String("new", newName))

		if _, err := sqlDB.Exec(renameSQL); err != nil {
			global.APP_LOG.Error("重命名 OAuth2 列失败",
				zap.String("old", oldName),
				zap.String("new", newName),
				zap.Error(err))
			return fmt.Errorf("重命名列 %s → %s 失败: %w", oldName, newName, err)
		}
		renamedCount++
	}

	if renamedCount > 0 {
		global.APP_LOG.Info("OAuth2 列名迁移完成",
			zap.Int("renamed_columns", renamedCount))
	} else {
		global.APP_LOG.Debug("OAuth2 列名无需迁移（已是最新或尚未创建）")
	}

	return nil
}

// getTableColumns 返回表中所有列名的小写映射（用于快速查找）。
func (ds *DatabaseService) getTableColumns(db *gorm.DB, tableName string) (map[string]bool, error) {
	var columns []string
	err := db.Raw(
		"SELECT LOWER(COLUMN_NAME) FROM information_schema.COLUMNS WHERE table_schema = DATABASE() AND table_name = ?",
		tableName,
	).Scan(&columns).Error
	if err != nil {
		return nil, err
	}

	columnMap := make(map[string]bool, len(columns))
	for _, col := range columns {
		columnMap[col] = true
	}
	return columnMap, nil
}
