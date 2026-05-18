package admin

import (
	"strconv"

	"oneclickvirt/global"
	"oneclickvirt/middleware"
	"oneclickvirt/model/common"
	operationService "oneclickvirt/service/admin/operation"
	checkinService "oneclickvirt/service/checkin"
	domainService "oneclickvirt/service/domain"
	kycService "oneclickvirt/service/kyc"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ============ 域名管理 ============

// AdminGetDomains 管理员获取所有域名绑定
func AdminGetDomains(c *gin.Context) {
	authCtx, _ := middleware.GetAuthContext(c)
	ownerAdminID := middleware.GetOwnerAdminID(c)

	svc := &domainService.Service{}
	domains, err := svc.AdminGetAllDomains(ownerAdminID)
	if err != nil {
		global.APP_LOG.Error("获取域名列表失败", zap.Error(err), zap.Uint("adminID", authCtx.UserID))
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, domains)
}

// AdminDeleteDomain 管理员删除域名绑定
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

// GetDomainConfig 获取域名配置
func GetDomainConfig(c *gin.Context) {
	providerID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的Provider ID"))
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
func UpdateDomainConfig(c *gin.Context) {
	providerID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的Provider ID"))
		return
	}

	// 普通管理员只能更改自己节点的配置
	ownerAdminID := middleware.GetOwnerAdminID(c)
	if ownerAdminID > 0 {
		var count int64
		global.APP_DB.Table("providers").Where("id = ? AND owner_admin_id = ?", providerID, ownerAdminID).Count(&count)
		if count == 0 {
			common.ResponseWithError(c, common.NewError(common.CodeForbidden, "无权操作该节点的域名配置"))
			return
		}
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
func AdminGetCheckinConfig(c *gin.Context) {
	providerID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的Provider ID"))
		return
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
func AdminUpdateCheckinConfig(c *gin.Context) {
	providerID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的Provider ID"))
		return
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
