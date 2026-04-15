package user

import (
	"strconv"

	"oneclickvirt/global"
	"oneclickvirt/model/common"
	checkinService "oneclickvirt/service/checkin"
	domainService "oneclickvirt/service/domain"
	kycService "oneclickvirt/service/kyc"
	"oneclickvirt/utils"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ============ 域名绑定 ============

// GetUserDomains 获取用户域名列表
func GetUserDomains(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	svc := &domainService.Service{}
	domains, err := svc.GetUserDomains(userID)
	if err != nil {
		global.APP_LOG.Error("获取用户域名失败", zap.Uint("userID", userID), zap.Error(err))
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, domains)
}

// CreateUserDomain 用户绑定域名
func CreateUserDomain(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	var req domainService.CreateDomainRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	svc := &domainService.Service{}
	domain, err := svc.CreateDomain(userID, &req)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, domain)
}

// DeleteUserDomain 用户删除域名绑定
func DeleteUserDomain(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	domainID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的域名ID"))
		return
	}

	svc := &domainService.Service{}
	if err := svc.DeleteDomain(userID, uint(domainID)); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, nil)
}

// UpdateUserDomain 用户更新域名绑定
func UpdateUserDomain(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	domainID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的域名ID"))
		return
	}

	var req domainService.UpdateDomainRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	svc := &domainService.Service{}
	if err := svc.UpdateDomain(userID, uint(domainID), &req); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, nil)
}

// ============ KYC实名认证 ============

// GetUserKYC 获取用户KYC状态
func GetUserKYC(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	svc := &kycService.Service{}
	record, err := svc.GetUserKYC(userID)
	if err != nil {
		common.ResponseSuccess(c, gin.H{"status": "none"})
		return
	}
	common.ResponseSuccess(c, record)
}

// SubmitUserKYC 提交实名认证
func SubmitUserKYC(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	var req kycService.SubmitKYCRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	// XSS validation
	if utils.ContainsHTMLTags(req.RealName) || utils.ContainsHTMLTags(req.IDNumber) {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "输入包含不允许的HTML标签"))
		return
	}

	svc := &kycService.Service{}
	record, err := svc.SubmitKYC(userID, &req)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, record)
}

// SubmitAlipayKYC 通过支付宝人脸发起认证
func SubmitAlipayKYC(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	var req kycService.SubmitKYCRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	svc := &kycService.Service{}
	certifyURL, err := svc.SubmitAlipayKYC(userID, &req)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, gin.H{"certifyUrl": certifyURL})
}

// QueryAlipayKYCResult 查询支付宝认证结果
func QueryAlipayKYCResult(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	svc := &kycService.Service{}
	passed, err := svc.QueryAlipayKYCResult(userID)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, gin.H{"passed": passed})
}

// ============ 签到续期 ============

// GenerateCheckinCode 获取签到验证挑战
func GenerateCheckinCode(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	instanceID, err := strconv.ParseUint(c.Param("instance_id"), 10, 64)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的实例ID"))
		return
	}

	svc := &checkinService.Service{}
	result, err := svc.GetCheckinChallenge(userID, uint(instanceID))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, result)
}

// DoCheckin 用户签到
func DoCheckin(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	var req checkinService.DoCheckinRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	svc := &checkinService.Service{}
	if err := svc.DoCheckin(userID, req.InstanceID, &req); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, nil)
}

// GetCheckinRecords 获取签到记录
func GetCheckinRecords(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "10"))

	svc := &checkinService.Service{}
	records, total, err := svc.GetCheckinRecords(userID, page, pageSize)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, gin.H{
		"list":  records,
		"total": total,
	})
}
