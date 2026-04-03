package admin

import (
	"context"
	"strconv"

	"oneclickvirt/global"
	"oneclickvirt/model/common"
	firewallModel "oneclickvirt/model/firewall"
	firewallService "oneclickvirt/service/firewall"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// GetBlockRules returns all block rules.
func GetBlockRules(c *gin.Context) {
	svc := &firewallService.Service{}
	rules, err := svc.ListRules()
	if err != nil {
		global.APP_LOG.Error("获取屏蔽规则失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取屏蔽规则失败"))
		return
	}
	common.ResponseSuccess(c, rules)
}

// GetBlockRule returns a single block rule.
func GetBlockRule(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的规则ID"))
		return
	}
	svc := &firewallService.Service{}
	rule, err := svc.GetRule(uint(id))
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取屏蔽规则失败"))
		return
	}
	common.ResponseSuccess(c, rule)
}

// CreateBlockRule creates a new block rule.
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
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "创建屏蔽规则失败"))
		return
	}
	common.ResponseSuccess(c, rule, "创建成功")
}

// UpdateBlockRule updates an existing block rule.
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
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "更新屏蔽规则失败"))
		return
	}
	common.ResponseSuccess(c, rule, "更新成功")
}

// DeleteBlockRule deletes a block rule.
func DeleteBlockRule(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的规则ID"))
		return
	}
	svc := &firewallService.Service{}
	if err := svc.DeleteRule(uint(id)); err != nil {
		global.APP_LOG.Error("删除屏蔽规则失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "删除屏蔽规则失败"))
		return
	}
	common.ResponseSuccess(c, nil, "删除成功")
}

// ApplyBlockRules applies block rules to targets.
func ApplyBlockRules(c *gin.Context) {
	var req firewallModel.ApplyBlockRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}
	validScopes := map[string]bool{"global": true, "provider": true, "instance": true, "user": true}
	if !validScopes[req.Scope] {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的scope, 可选值: global, provider, instance, user"))
		return
	}
	svc := &firewallService.Service{}
	apps, err := svc.ApplyRules(context.Background(), &req)
	if err != nil {
		global.APP_LOG.Error("应用屏蔽规则失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "应用屏蔽规则失败: "+err.Error()))
		return
	}
	common.ResponseSuccess(c, apps, "规则应用中")
}

// RemoveBlockRuleApplications removes applied rules.
func RemoveBlockRuleApplications(c *gin.Context) {
	var req firewallModel.RemoveBlockRuleApplicationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}
	svc := &firewallService.Service{}
	if err := svc.RemoveApplications(context.Background(), &req); err != nil {
		global.APP_LOG.Error("移除屏蔽规则应用失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "移除屏蔽规则应用失败"))
		return
	}
	common.ResponseSuccess(c, nil, "规则已移除")
}

// GetBlockRuleApplications returns all rule applications.
func GetBlockRuleApplications(c *gin.Context) {
	ruleIDStr := c.Query("rule_id")
	var ruleID uint
	if ruleIDStr != "" {
		id, err := strconv.ParseUint(ruleIDStr, 10, 64)
		if err == nil {
			ruleID = uint(id)
		}
	}
	svc := &firewallService.Service{}
	apps, err := svc.ListApplications(ruleID)
	if err != nil {
		global.APP_LOG.Error("获取屏蔽规则应用记录失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取屏蔽规则应用记录失败"))
		return
	}
	common.ResponseSuccess(c, apps)
}

// GetProviderBlockStatus returns which rules are applied to a specific provider.
func GetProviderBlockStatus(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的节点ID"))
		return
	}
	svc := &firewallService.Service{}
	status, err := svc.GetProviderBlockStatus(uint(id))
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取节点屏蔽状态失败"))
		return
	}
	common.ResponseSuccess(c, status)
}

// GetAgentEnabledProviders returns provider IDs with agent monitoring enabled.
func GetAgentEnabledProviders(c *gin.Context) {
	svc := &firewallService.Service{}
	ids, err := svc.GetAgentEnabledProviders()
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取已启用Agent的节点列表失败"))
		return
	}
	common.ResponseSuccess(c, ids)
}
