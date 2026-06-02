package admin

import (
	"errors"
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/middleware"
	"oneclickvirt/model/common"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/utils"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// AdminGroupInfoRequest 管理员分组信息请求
type AdminGroupInfoRequest struct {
	GroupName        string `json:"groupName" binding:"max=64"`
	GroupDescription string `json:"groupDescription"` // 支持富文本HTML
}

// AdminGroupInfoResponse 管理员分组信息响应
type AdminGroupInfoResponse struct {
	GroupName        string `json:"groupName"`
	GroupDescription string `json:"groupDescription"`
}

func defaultAdminGroupName(c *gin.Context) string {
	lang := c.GetHeader("Accept-Language")
	if lang != "" && (lang == "en" || lang == "en-US" || lang == "en_US" ||
		len(lang) >= 2 && lang[:2] == "en") {
		return "Test"
	}
	return "测试"
}

// GetAdminGroupInfo 获取管理员分组信息
func GetAdminGroupInfo(c *gin.Context) {
	ownerAdminID := middleware.GetOwnerAdminID(c)

	var setting providerModel.AdminGroupSetting
	if err := global.APP_DB.Where("owner_admin_id = ?", ownerAdminID).First(&setting).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			global.APP_LOG.Error("查询管理员分组设置失败",
				zap.Uint("ownerAdminID", ownerAdminID),
				zap.Error(err))
			common.ResponseWithError(c, common.ClassifyError(err))
			return
		}
	} else {
		groupName := setting.GroupName
		if groupName == "" {
			groupName = defaultAdminGroupName(c)
		}
		common.ResponseSuccess(c, AdminGroupInfoResponse{
			GroupName:        groupName,
			GroupDescription: utils.SanitizeHTML(setting.GroupDescription),
		}, "获取成功")
		return
	}

	// 兼容旧数据：未创建独立分组设置时，回退读取该管理员第一个 Provider 的分组信息。
	var provider providerModel.Provider
	query := global.APP_DB.Model(&providerModel.Provider{}).Select("group_name, group_description")
	if ownerAdminID > 0 {
		query = query.Where("owner_admin_id = ?", ownerAdminID)
	} else {
		query = query.Where("owner_admin_id = 0")
	}

	if err := query.First(&provider).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			global.APP_LOG.Error("查询Provider分组信息失败",
				zap.Uint("ownerAdminID", ownerAdminID),
				zap.Error(err))
			common.ResponseWithError(c, common.ClassifyError(err))
			return
		}
		common.ResponseSuccess(c, AdminGroupInfoResponse{
			GroupName: defaultAdminGroupName(c),
		}, "获取成功")
		return
	}

	groupName := provider.GroupName
	if groupName == "" {
		groupName = defaultAdminGroupName(c)
	}

	common.ResponseSuccess(c, AdminGroupInfoResponse{
		GroupName:        groupName,
		GroupDescription: utils.SanitizeHTML(provider.GroupDescription),
	}, "获取成功")
}

// UpdateAdminGroupInfo 更新管理员分组信息
func UpdateAdminGroupInfo(c *gin.Context) {
	var req AdminGroupInfoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误: "+err.Error()))
		return
	}
	req.GroupName = strings.TrimSpace(req.GroupName)
	if req.GroupName == "" {
		req.GroupName = defaultAdminGroupName(c)
	}
	if utils.ContainsHTMLTags(req.GroupName) || utils.ContainsSQLInjectionPattern(req.GroupName) {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "分组名称包含非法内容"))
		return
	}
	req.GroupDescription = utils.SanitizeHTML(req.GroupDescription)

	ownerAdminID := middleware.GetOwnerAdminID(c)

	if err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
		var setting providerModel.AdminGroupSetting
		if err := tx.Where("owner_admin_id = ?", ownerAdminID).First(&setting).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			setting = providerModel.AdminGroupSetting{
				OwnerAdminID:     ownerAdminID,
				GroupName:        req.GroupName,
				GroupDescription: req.GroupDescription,
			}
			if err := tx.Create(&setting).Error; err != nil {
				return err
			}
		} else if err := tx.Model(&setting).Updates(map[string]interface{}{
			"group_name":        req.GroupName,
			"group_description": req.GroupDescription,
		}).Error; err != nil {
			return err
		}

		return tx.Model(&providerModel.Provider{}).
			Where("owner_admin_id = ?", ownerAdminID).
			Updates(map[string]interface{}{
				"group_name":        req.GroupName,
				"group_description": req.GroupDescription,
			}).Error
	}); err != nil {
		global.APP_LOG.Error("更新管理员分组信息失败", zap.Error(err))
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, nil, "更新成功")
}
