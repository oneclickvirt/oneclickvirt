package admin

import (
	"net/http"

	"oneclickvirt/global"
	"oneclickvirt/middleware"
	"oneclickvirt/model/common"
	providerModel "oneclickvirt/model/provider"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
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

// GetAdminGroupInfo 获取管理员分组信息
func GetAdminGroupInfo(c *gin.Context) {
	ownerAdminID := middleware.GetOwnerAdminID(c)

	// 查询该管理员的分组信息（取第一个Provider的分组信息）
	var provider providerModel.Provider
	query := global.APP_DB.Model(&providerModel.Provider{}).Select("group_name, group_description")
	if ownerAdminID > 0 {
		query = query.Where("owner_admin_id = ?", ownerAdminID)
	} else {
		query = query.Where("owner_admin_id = 0")
	}

	if err := query.First(&provider).Error; err != nil {
		// 没有Provider时返回默认分组信息
		defaultName := "测试"
		lang := c.GetHeader("Accept-Language")
		if lang != "" && (lang == "en" || lang == "en-US" || lang == "en_US" ||
			len(lang) >= 2 && lang[:2] == "en") {
			defaultName = "Test"
		}
		c.JSON(http.StatusOK, common.Response{
			Code: 200,
			Msg:  "获取成功",
			Data: AdminGroupInfoResponse{
				GroupName: defaultName,
			},
		})
		return
	}

	groupName := provider.GroupName
	if groupName == "" {
		groupName = "测试"
		lang := c.GetHeader("Accept-Language")
		if lang != "" && (lang == "en" || lang == "en-US" || lang == "en_US" ||
			len(lang) >= 2 && lang[:2] == "en") {
			groupName = "Test"
		}
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "获取成功",
		Data: AdminGroupInfoResponse{
			GroupName:        groupName,
			GroupDescription: provider.GroupDescription,
		},
	})
}

// UpdateAdminGroupInfo 更新管理员分组信息
func UpdateAdminGroupInfo(c *gin.Context) {
	var req AdminGroupInfoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{Code: 400, Msg: "参数错误: " + err.Error()})
		return
	}

	ownerAdminID := middleware.GetOwnerAdminID(c)

	// 批量更新该管理员下所有Provider的分组信息
	query := global.APP_DB.Model(&providerModel.Provider{})
	if ownerAdminID > 0 {
		query = query.Where("owner_admin_id = ?", ownerAdminID)
	} else {
		query = query.Where("owner_admin_id = 0")
	}

	if err := query.Updates(map[string]interface{}{
		"group_name":        req.GroupName,
		"group_description": req.GroupDescription,
	}).Error; err != nil {
		global.APP_LOG.Error("更新管理员分组信息失败", zap.Error(err))
		c.JSON(http.StatusInternalServerError, common.Response{Code: 500, Msg: "更新分组信息失败"})
		return
	}

	c.JSON(http.StatusOK, common.Response{Code: 200, Msg: "更新成功"})
}
