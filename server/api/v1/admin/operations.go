package admin

import (
	"strconv"
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/middleware"
	"oneclickvirt/model/common"
	operationService "oneclickvirt/service/admin/operation"
	adminProviderService "oneclickvirt/service/admin/provider"
	checkinService "oneclickvirt/service/checkin"
	domainService "oneclickvirt/service/domain"
	kycService "oneclickvirt/service/kyc"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ============ 域名管理 ============

// AdminGetDomains 管理员获取所有域名绑定

// @Summary 管理员 Get 域名s
// @Description 获取管理员 Get 域名s
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "管理员 Get 域名s成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "管理员 Get 域名s失败"
// @Router /admin/domains [get]
func AdminGetDomains(c *gin.Context) {
	authCtx, _ := middleware.GetAuthContext(c)
	ownerAdminID := middleware.GetOwnerAdminID(c)

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "10"))
	page, pageSize = common.NormalizePagination(page, pageSize, common.DefaultPageSize)

	parseUintQuery := func(key string) uint {
		raw := strings.TrimSpace(c.Query(key))
		if raw == "" {
			return 0
		}
		value, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return 0
		}
		return uint(value)
	}

	req := domainService.AdminDomainListRequest{
		Page:       page,
		PageSize:   pageSize,
		Keyword:    strings.TrimSpace(c.Query("keyword")),
		Status:     strings.TrimSpace(c.Query("status")),
		UserID:     parseUintQuery("userId"),
		ProviderID: parseUintQuery("providerId"),
		InstanceID: parseUintQuery("instanceId"),
	}

	svc := &domainService.Service{}
	domains, total, err := svc.AdminGetDomainList(ownerAdminID, req)
	if err != nil {
		global.APP_LOG.Error("获取域名列表失败", zap.Error(err), zap.Uint("adminID", authCtx.UserID))
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccessWithPagination(c, domains, total, page, pageSize)
}

// AdminDeleteDomain 管理员删除域名绑定

// @Summary 管理员 Delete 域名
// @Description 删除管理员 Delete 域名
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "管理员 Delete 域名成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "管理员 Delete 域名失败"
// @Router /admin/domains/{id} [delete]
func AdminDeleteDomain(c *gin.Context) {
	domainID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的域名ID"))
		return
	}

	ownerAdminID := middleware.GetOwnerAdminID(c)
	svc := &domainService.Service{}
	if err := svc.AdminDeleteDomain(uint(domainID), ownerAdminID); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, nil)
}

// AdminUpdateDomain 管理员更新域名绑定

// @Summary 管理员 Update 域名
// @Description 更新管理员 Update 域名
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "管理员 Update 域名成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "管理员 Update 域名失败"
// @Router /admin/domains/{id} [put]
func AdminUpdateDomain(c *gin.Context) {
	domainID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的域名ID"))
		return
	}

	var req domainService.AdminUpdateDomainRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	ownerAdminID := middleware.GetOwnerAdminID(c)
	svc := &domainService.Service{}
	if err := svc.AdminUpdateDomain(uint(domainID), ownerAdminID, &req); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, nil)
}

// AdminSyncDomainProxy 管理员重新下发单个域名反向代理配置

// @Summary 管理员 Sync 域名 Proxy
// @Description 创建管理员 Sync 域名 Proxy
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "管理员 Sync 域名 Proxy成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "管理员 Sync 域名 Proxy失败"
// @Router /admin/domains/{id}/sync [post]
func AdminSyncDomainProxy(c *gin.Context) {
	domainID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的域名ID"))
		return
	}

	ownerAdminID := middleware.GetOwnerAdminID(c)
	svc := &domainService.Service{}
	if err := svc.AdminSyncDomainProxy(uint(domainID), ownerAdminID); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, nil)
}

// AdminSyncDomainProxies 管理员重新下发域名反向代理配置

// @Summary 管理员 Sync 域名 Proxies
// @Description 创建管理员 Sync 域名 Proxies
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "管理员 Sync 域名 Proxies成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "管理员 Sync 域名 Proxies失败"
// @Router /admin/domains/sync-proxies [post]
func AdminSyncDomainProxies(c *gin.Context) {
	ownerAdminID := middleware.GetOwnerAdminID(c)
	svc := &domainService.Service{}
	result, err := svc.AdminSyncDomainProxies(ownerAdminID)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, result)
}

// GetDomainConfig 获取域名配置

// @Summary 获取域名 配置
// @Description 获取获取域名 配置
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "获取域名 配置成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "获取域名 配置失败"
// @Router /admin/providers/{id}/domain-config [get]
func GetDomainConfig(c *gin.Context) {
	providerID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的Provider ID"))
		return
	}
	if err := ensureProviderOwner(c, uint(providerID)); err != nil {
		common.ResponseWithError(c, err)
		return
	}

	svc := &domainService.Service{}
	config, err := svc.GetDomainConfig(uint(providerID))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, config)
}

// UpdateDomainConfig 更新域名配置

// @Summary 更新域名 配置
// @Description 更新更新域名 配置
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "更新域名 配置成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "更新域名 配置失败"
// @Router /admin/providers/{id}/domain-config [put]
func UpdateDomainConfig(c *gin.Context) {
	providerID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的Provider ID"))
		return
	}

	if err := ensureProviderOwner(c, uint(providerID)); err != nil {
		common.ResponseWithError(c, err)
		return
	}

	var req domainService.UpdateDomainConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	svc := &domainService.Service{}
	if err := svc.UpdateDomainConfig(uint(providerID), &req); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, nil)
}

// ============ KYC管理 ============

// AdminGetKYCList 管理员获取KYC列表

// @Summary 管理员 Get 实名认证 列表
// @Description 获取管理员 Get 实名认证 列表
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "管理员 Get 实名认证 列表成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "管理员 Get 实名认证 列表失败"
// @Router /admin/kyc [get]
func AdminGetKYCList(c *gin.Context) {
	status := c.Query("status")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "10"))
	page, pageSize = common.NormalizePagination(page, pageSize, common.DefaultPageSize)

	svc := &kycService.Service{}
	records, total, err := svc.AdminGetKYCList(status, page, pageSize)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, gin.H{
		"list":  records,
		"total": total,
	})
}

// AdminReviewKYC 管理员审核KYC

// @Summary 管理员 Review 实名认证
// @Description 更新管理员 Review 实名认证
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "管理员 Review 实名认证成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "管理员 Review 实名认证失败"
// @Router /admin/kyc/{id}/review [put]
func AdminReviewKYC(c *gin.Context) {
	kycID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的KYC ID"))
		return
	}

	var req struct {
		Approved     bool   `json:"approved"`
		RejectReason string `json:"rejectReason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	authCtx, _ := middleware.GetAuthContext(c)
	svc := &kycService.Service{}
	if err := svc.AdminReviewKYC(uint(kycID), authCtx.UserID, req.Approved, req.RejectReason); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, nil)
}

// ============ 签到配置管理 ============

// AdminGetCheckinConfig 获取签到配置

// @Summary 管理员 Get 签到 配置
// @Description 获取管理员 Get 签到 配置
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "管理员 Get 签到 配置成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "管理员 Get 签到 配置失败"
// @Router /admin/providers/{id}/checkin-config [get]
func AdminGetCheckinConfig(c *gin.Context) {
	providerID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的Provider ID"))
		return
	}
	ownerAdminID := middleware.GetOwnerAdminID(c)
	if ownerAdminID > 0 {
		if err := adminProviderService.CheckProviderOwnership(uint(providerID), ownerAdminID); err != nil {
			common.ResponseWithError(c, common.NewError(common.CodeForbidden, err.Error()))
			return
		}
	}

	svc := &checkinService.Service{}
	config, err := svc.GetCheckinConfig(uint(providerID))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, config)
}

// AdminUpdateCheckinConfig 更新签到配置

// @Summary 管理员 Update 签到 配置
// @Description 更新管理员 Update 签到 配置
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "管理员 Update 签到 配置成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "管理员 Update 签到 配置失败"
// @Router /admin/providers/{id}/checkin-config [put]
func AdminUpdateCheckinConfig(c *gin.Context) {
	providerID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的Provider ID"))
		return
	}
	ownerAdminID := middleware.GetOwnerAdminID(c)
	if ownerAdminID > 0 {
		if err := adminProviderService.CheckProviderOwnership(uint(providerID), ownerAdminID); err != nil {
			common.ResponseWithError(c, common.NewError(common.CodeForbidden, err.Error()))
			return
		}
	}

	var req checkinService.UpdateCheckinConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	svc := &checkinService.Service{}
	if err := svc.UpdateCheckinConfig(uint(providerID), &req); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, nil)
}

// ============ 管理员特殊操作 ============

// AdminLoginAsUser 管理员代登录

// @Summary 管理员 日志in As 用户
// @Description 创建管理员 日志in As 用户
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "管理员 日志in As 用户成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "管理员 日志in As 用户失败"
// @Router /admin/users/{id}/login-as [post]
func AdminLoginAsUser(c *gin.Context) {
	userID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的用户ID"))
		return
	}

	authCtx, _ := middleware.GetAuthContext(c)
	svc := &operationService.OperationService{}
	token, err := svc.LoginAsUser(authCtx.UserID, uint(userID))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, gin.H{"token": token})
}

// AdminTransferInstance 管理员转移实例

// @Summary 管理员 Transfer 实例
// @Description 创建管理员 Transfer 实例
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=object} "管理员 Transfer 实例成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "管理员 Transfer 实例失败"
// @Router /admin/instances/transfer [post]
func AdminTransferInstance(c *gin.Context) {
	var req struct {
		InstanceID   uint `json:"instanceId" binding:"required"`
		TargetUserID uint `json:"targetUserId" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	authCtx, _ := middleware.GetAuthContext(c)
	svc := &operationService.OperationService{}
	if err := svc.TransferInstance(authCtx.UserID, req.InstanceID, req.TargetUserID); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, nil)
}
