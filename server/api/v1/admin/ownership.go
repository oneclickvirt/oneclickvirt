package admin

import (
	"fmt"

	"oneclickvirt/global"
	"oneclickvirt/middleware"
	"oneclickvirt/model/common"
	providerModel "oneclickvirt/model/provider"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func ensureProviderOwner(c *gin.Context, providerID uint) error {
	return ensureOwnedCount(c, []uint{providerID}, func(ownerAdminID uint, ids []uint) *gorm.DB {
		return global.APP_DB.Model(&providerModel.Provider{}).
			Where("id IN ? AND owner_admin_id = ?", ids, ownerAdminID)
	}, "Provider")
}

func ensureProviderOwners(c *gin.Context, providerIDs []uint) error {
	return ensureOwnedCount(c, providerIDs, func(ownerAdminID uint, ids []uint) *gorm.DB {
		return global.APP_DB.Model(&providerModel.Provider{}).
			Where("id IN ? AND owner_admin_id = ?", ids, ownerAdminID)
	}, "Provider")
}

func ensureInstanceOwner(c *gin.Context, instanceID uint) error {
	return ensureInstanceOwners(c, []uint{instanceID})
}

func ensureInstanceOwners(c *gin.Context, instanceIDs []uint) error {
	return ensureOwnedCount(c, instanceIDs, func(ownerAdminID uint, ids []uint) *gorm.DB {
		providerIDs := global.APP_DB.Model(&providerModel.Provider{}).
			Select("id").
			Where("owner_admin_id = ?", ownerAdminID)
		return global.APP_DB.Model(&providerModel.Instance{}).
			Where("id IN ? AND provider_id IN (?)", ids, providerIDs)
	}, "实例")
}

func ensurePortMappingOwner(c *gin.Context, portID uint) error {
	return ensurePortMappingOwners(c, []uint{portID})
}

func ensurePortMappingOwners(c *gin.Context, portIDs []uint) error {
	return ensureOwnedCount(c, portIDs, func(ownerAdminID uint, ids []uint) *gorm.DB {
		providerIDs := global.APP_DB.Model(&providerModel.Provider{}).
			Select("id").
			Where("owner_admin_id = ?", ownerAdminID)
		return global.APP_DB.Model(&providerModel.Port{}).
			Where("id IN ? AND provider_id IN (?)", ids, providerIDs)
	}, "端口映射")
}

func ensureOwnedCount(
	c *gin.Context,
	ids []uint,
	query func(ownerAdminID uint, ids []uint) *gorm.DB,
	resourceName string,
) error {
	ownerAdminID := middleware.GetOwnerAdminID(c)
	if ownerAdminID == 0 {
		return nil
	}

	uniqueIDs := uniqueNonZeroIDs(ids)
	if len(uniqueIDs) == 0 {
		return common.NewError(common.CodeValidationError, fmt.Sprintf("%s ID不能为空", resourceName))
	}
	if global.APP_DB == nil {
		return common.NewError(common.CodeDatabaseError, "数据库连接不可用")
	}

	var count int64
	if err := query(ownerAdminID, uniqueIDs).Count(&count).Error; err != nil {
		return common.NewError(common.CodeDatabaseError, err.Error())
	}
	if count != int64(len(uniqueIDs)) {
		return common.NewError(common.CodeForbidden, fmt.Sprintf("无权操作该%s", resourceName))
	}
	return nil
}

func uniqueNonZeroIDs(ids []uint) []uint {
	seen := make(map[uint]struct{}, len(ids))
	result := make([]uint, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}
