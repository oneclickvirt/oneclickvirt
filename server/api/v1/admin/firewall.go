package admin

import (
	"strconv"

	"oneclickvirt/global"
	"oneclickvirt/middleware"
	"oneclickvirt/model/common"
	firewallModel "oneclickvirt/model/firewall"
	firewallService "oneclickvirt/service/firewall"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const maxBlockRuleRequestIDs = 5000

// GetBlockRules returns all block rules.

// @Summary 获取屏蔽规则s
// @Description 获取获取屏蔽规则s
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码" default(1)
// @Param pageSize query int false "每页数量" default(10)
// @Success 200 {object} common.Response{data=object} "获取屏蔽规则s成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "获取屏蔽规则s失败"
// @Router /admin/block-rules [get]
func GetBlockRules(c *gin.Context) {
	svc := &firewallService.Service{}
	rules, err := svc.ListRules()
	if err != nil {
		global.APP_LOG.Error("获取屏蔽规则失败", zap.Error(err))
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, rules)
}

// GetBlockRule returns a single block rule.

// @Summary 获取屏蔽规则
// @Description 获取获取屏蔽规则
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "获取屏蔽规则成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "获取屏蔽规则失败"
// @Router /admin/block-rules/{id} [get]
func GetBlockRule(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的规则ID"))
		return
	}
	svc := &firewallService.Service{}
	rule, err := svc.GetRule(uint(id))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, rule)
}

// CreateBlockRule creates a new block rule.

// @Summary 创建屏蔽规则
// @Description 创建创建屏蔽规则
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "创建屏蔽规则成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "创建屏蔽规则失败"
// @Router /admin/block-rules [post]
func CreateBlockRule(c *gin.Context) {
	var req firewallModel.CreateBlockRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}
	svc := &firewallService.Service{}
	rule, err := svc.CreateRule(&req)
	if err != nil {
		global.APP_LOG.Error("创建屏蔽规则失败", zap.Error(err))
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, rule, "创建成功")
}

// UpdateBlockRule updates an existing block rule.

// @Summary 更新屏蔽规则
// @Description 更新更新屏蔽规则
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "更新屏蔽规则成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "更新屏蔽规则失败"
// @Router /admin/block-rules/{id} [put]
func UpdateBlockRule(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的规则ID"))
		return
	}
	var req firewallModel.UpdateBlockRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}
	svc := &firewallService.Service{}
	rule, err := svc.UpdateRule(uint(id), &req)
	if err != nil {
		global.APP_LOG.Error("更新屏蔽规则失败", zap.Error(err))
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, rule, "更新成功")
}

// DeleteBlockRule deletes a block rule.

// @Summary 删除屏蔽规则
// @Description 删除删除屏蔽规则
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "删除屏蔽规则成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "删除屏蔽规则失败"
// @Router /admin/block-rules/{id} [delete]
func DeleteBlockRule(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的规则ID"))
		return
	}
	svc := &firewallService.Service{}
	if err := svc.DeleteRule(uint(id)); err != nil {
		global.APP_LOG.Error("删除屏蔽规则失败", zap.Error(err))
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, nil, "删除成功")
}

// ApplyBlockRules applies block rules to targets.

// @Summary 应用屏蔽规则s
// @Description 创建应用屏蔽规则s
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "应用屏蔽规则s成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "应用屏蔽规则s失败"
// @Router /admin/block-rules/apply [post]
func ApplyBlockRules(c *gin.Context) {
	var req firewallModel.ApplyBlockRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}
	if len(req.RuleIDs) == 0 {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "规则ID不能为空"))
		return
	}
	if len(req.RuleIDs) > maxBlockRuleRequestIDs || len(req.TargetIDs) > maxBlockRuleRequestIDs {
		common.ResponseWithError(c, common.NewError(common.CodeTooLarge, "单次请求的规则或目标ID不能超过5000个"))
		return
	}
	validScopes := map[string]bool{"global": true, "provider": true, "instance": true, "user": true}
	if !validScopes[req.Scope] {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的scope, 可选值: global, provider, instance, user"))
		return
	}
	validIPVersions := map[string]bool{"": true, "both": true, "ipv4": true, "ipv6": true}
	if !validIPVersions[req.IPVersion] {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的IP版本"))
		return
	}
	if req.Scope != "global" && len(req.TargetIDs) == 0 {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "目标ID不能为空"))
		return
	}

	// 普通管理员数据隔离：不能使用global scope，只能操作自己的provider/instance
	ownerAdminID := middleware.GetOwnerAdminID(c)
	if ownerAdminID > 0 {
		if req.Scope == "global" {
			common.ResponseWithError(c, common.NewError(common.CodeForbidden, "普通管理员不能应用全局屏蔽规则"))
			return
		}
		// 验证目标provider/instance归属于当前管理员
		switch req.Scope {
		case "provider":
			if err := ensureProviderOwners(c, req.TargetIDs); err != nil {
				common.ResponseWithError(c, err)
				return
			}
		case "instance":
			if err := ensureInstanceOwners(c, req.TargetIDs); err != nil {
				common.ResponseWithError(c, err)
				return
			}
		case "user":
			common.ResponseWithError(c, common.NewError(common.CodeForbidden, "普通管理员不能应用用户级别的屏蔽规则"))
			return
		}
	}

	svc := &firewallService.Service{}
	apps, err := svc.ApplyRules(c.Request.Context(), &req)
	if err != nil {
		global.APP_LOG.Error("应用屏蔽规则失败", zap.Error(err))
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, apps, "规则应用中")
}

// RemoveBlockRuleApplications removes applied rules.

// @Summary Remove 屏蔽规则 Applications
// @Description 创建Remove 屏蔽规则 Applications
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "Remove 屏蔽规则 Applications成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "Remove 屏蔽规则 Applications失败"
// @Router /admin/block-rules/remove [post]
func RemoveBlockRuleApplications(c *gin.Context) {
	var req firewallModel.RemoveBlockRuleApplicationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	if len(req.ApplicationIDs) == 0 {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "应用ID不能为空"))
		return
	}
	if len(req.ApplicationIDs) > maxBlockRuleRequestIDs {
		common.ResponseWithError(c, common.NewError(common.CodeTooLarge, "单次最多移除5000条规则应用"))
		return
	}
	req.ApplicationIDs = uniqueNonZeroIDs(req.ApplicationIDs)
	if len(req.ApplicationIDs) == 0 {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "应用ID不能为空"))
		return
	}

	// 普通管理员数据隔离：验证目标归属
	ownerAdminID := middleware.GetOwnerAdminID(c)
	if ownerAdminID > 0 {
		// 查询这些application的scope和target_id
		var apps []firewallModel.BlockRuleApplication
		if err := global.APP_DB.Where("id IN ?", req.ApplicationIDs).Find(&apps).Error; err != nil {
			common.ResponseWithError(c, common.ClassifyError(err))
			return
		}
		if len(apps) != len(req.ApplicationIDs) {
			common.ResponseWithError(c, common.NewError(common.CodeNotFound, "部分规则应用不存在"))
			return
		}
		providerIDs := make([]uint, 0, len(apps))
		instanceIDs := make([]uint, 0, len(apps))
		for _, app := range apps {
			switch app.Scope {
			case "global":
				common.ResponseWithError(c, common.NewError(common.CodeForbidden, "普通管理员不能移除全局规则"))
				return
			case "provider":
				providerIDs = append(providerIDs, app.TargetID)
			case "instance":
				instanceIDs = append(instanceIDs, app.TargetID)
			case "user":
				common.ResponseWithError(c, common.NewError(common.CodeForbidden, "普通管理员不能移除用户级别的规则"))
				return
			}
		}
		if len(providerIDs) > 0 {
			if err := ensureProviderOwners(c, providerIDs); err != nil {
				common.ResponseWithError(c, err)
				return
			}
		}
		if len(instanceIDs) > 0 {
			if err := ensureInstanceOwners(c, instanceIDs); err != nil {
				common.ResponseWithError(c, err)
				return
			}
		}
	}

	svc := &firewallService.Service{}
	if err := svc.RemoveApplications(c.Request.Context(), &req); err != nil {
		global.APP_LOG.Error("移除屏蔽规则应用失败", zap.Error(err))
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, nil, "规则已移除")
}

// GetBlockRuleApplications returns all rule applications.

// @Summary 获取屏蔽规则 Applications
// @Description 获取获取屏蔽规则 Applications
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "获取屏蔽规则 Applications成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "获取屏蔽规则 Applications失败"
// @Router /admin/block-rules/applications [get]
func GetBlockRuleApplications(c *gin.Context) {
	ruleIDStr := c.Query("rule_id")
	var ruleID uint
	if ruleIDStr != "" {
		id, err := strconv.ParseUint(ruleIDStr, 10, 64)
		if err != nil || id == 0 {
			common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的规则ID"))
			return
		}
		ruleID = uint(id)
	}
	svc := &firewallService.Service{}
	apps, err := svc.ListApplications(ruleID, middleware.GetOwnerAdminID(c))
	if err != nil {
		global.APP_LOG.Error("获取屏蔽规则应用记录失败", zap.Error(err))
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, apps)
}

// GetProviderBlockStatus returns which rules are applied to a specific provider.

// @Summary 获取Provider Block 状态
// @Description 获取获取Provider Block 状态
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "获取Provider Block 状态成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "获取Provider Block 状态失败"
// @Router /admin/providers/{id}/block-status [get]
func GetProviderBlockStatus(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的节点ID"))
		return
	}
	if err := ensureProviderOwner(c, uint(id)); err != nil {
		common.ResponseWithError(c, err)
		return
	}
	svc := &firewallService.Service{}
	status, err := svc.GetProviderBlockStatus(uint(id))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, status)
}

// GetAgentEnabledProviders returns provider IDs with agent monitoring enabled.

// @Summary 获取Agent Enabled Providers
// @Description 获取获取Agent Enabled Providers
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "获取Agent Enabled Providers成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "获取Agent Enabled Providers失败"
// @Router /admin/block-rules/agent-providers [get]
func GetAgentEnabledProviders(c *gin.Context) {
	svc := &firewallService.Service{}
	ids, err := svc.GetAgentEnabledProviders(middleware.GetOwnerAdminID(c))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, ids)
}
