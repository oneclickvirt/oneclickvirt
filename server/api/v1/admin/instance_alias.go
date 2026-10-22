package admin

import (
	"strconv"

	"oneclickvirt/middleware"
	"oneclickvirt/model/common"
	"oneclickvirt/model/provider"
	"oneclickvirt/service/instancealias"

	"github.com/gin-gonic/gin"
)

// UpdateAdminInstanceAlias 修改管理员可管理实例的备注别名
// @Summary 修改实例备注别名
// @Description 管理员仅可修改归属自己节点的实例，超级管理员可修改全部实例。最多128个字符，空字符串清空。只影响面板显示，实例原名、名称搜索和宿主机标识保持不变。
// @Tags 实例管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "实例ID"
// @Param body body provider.UpdateInstanceAliasRequest true "备注别名"
// @Success 200 {object} common.Response{data=provider.InstanceAliasResponse} "修改成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 404 {object} common.Response "实例不存在或无权限"
// @Router /admin/instances/{id}/alias [put]
func UpdateAdminInstanceAlias(c *gin.Context) {
	instanceID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || instanceID == 0 {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的实例ID"))
		return
	}
	var req provider.UpdateInstanceAliasRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "请提供alias字符串，空字符串可清空别名"))
		return
	}
	alias, err := instancealias.UpdateForAdmin(c.Request.Context(), middleware.GetOwnerAdminID(c), uint(instanceID), *req.Alias)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, provider.InstanceAliasResponse{ID: uint(instanceID), Alias: alias}, "修改成功")
}
