package instancealias

import (
	"context"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"oneclickvirt/global"
	"oneclickvirt/model/common"

	"gorm.io/gorm"
)

func normalize(alias string) (string, error) {
	alias = strings.TrimSpace(alias)
	if !utf8.ValidString(alias) || utf8.RuneCountInString(alias) > 128 || strings.ContainsFunc(alias, unicode.IsControl) {
		return "", common.NewError(common.CodeValidationError, "备注别名最多128个字符，不能包含控制字符")
	}
	return alias, nil
}

func UpdateForUser(ctx context.Context, userID, instanceID uint, alias string) (string, error) {
	if userID == 0 {
		return "", common.NewError(common.CodeUnauthorized, "用户未登录")
	}
	return update(ctx, global.APP_DB.Table("instances").Where("user_id = ?", userID), instanceID, alias)
}

// ownerAdminID == 0 is the existing super-administrator scope. Ordinary
// administrators can edit only instances on their own non-deleted providers.
func UpdateForAdmin(ctx context.Context, ownerAdminID, instanceID uint, alias string) (string, error) {
	query := global.APP_DB.Table("instances")
	if ownerAdminID > 0 {
		query = query.Where("provider_id IN (?)", global.APP_DB.Table("providers").
			Select("id").Where("owner_admin_id = ? AND deleted_at IS NULL", ownerAdminID))
	}
	return update(ctx, query, instanceID, alias)
}

func update(ctx context.Context, query *gorm.DB, instanceID uint, alias string) (string, error) {
	alias, err := normalize(alias)
	if err != nil {
		return "", err
	}
	// Use a table-scoped column update so no runtime identity, resource, traffic,
	// lifecycle state, provider call or task submission is involved.
	// The model's create-only Alias permission protects it from stale lifecycle
	// saves; only this endpoint writes the column after creation.
	query = query.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", instanceID)
	result := query.Updates(map[string]interface{}{"alias": alias, "updated_at": time.Now()})
	if result.Error != nil {
		return "", result.Error
	}
	if result.RowsAffected == 0 {
		// MySQL may report zero changed rows for an idempotent update.
		var count int64
		if err := query.Count(&count).Error; err != nil {
			return "", err
		}
		if count == 0 {
			return "", common.NewError(common.CodeNotFound, "实例不存在或无权限")
		}
	}
	return alias, nil
}
