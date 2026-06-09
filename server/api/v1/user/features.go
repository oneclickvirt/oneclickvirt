package user

import (
	"strconv"
	"strings"

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
		// 如果用户没有KYC记录，返回 "none" 状态（不是错误）
		if strings.Contains(err.Error(), "不存在") || strings.Contains(err.Error(), "找不到") ||
			strings.Contains(strings.ToLower(err.Error()), "not found") ||
			strings.Contains(strings.ToLower(err.Error()), "record not found") {
			common.ResponseSuccess(c, gin.H{"status": "none"})
			return
		}
		common.ResponseWithError(c, common.ClassifyError(err))
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

// GetEligibleCheckinInstances 获取当前用户可签到续期的实例
func GetEligibleCheckinInstances(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	svc := &checkinService.Service{}
	instances, err := svc.GetEligibleCheckinInstances(userID)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, instances)
}

// GenerateCheckinCode 获取签到验证挑战
// @Summary 获取签到验证挑战
// @Description 为指定实例生成签到续期所需的验证码或 PoW challenge
// @Tags 用户/签到续期
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param instance_id path int true "实例ID"
// @Success 200 {object} common.Response "获取成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "认证失败"
// @Router /user/checkin/code/{instance_id} [post]
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
// @Summary 用户签到续期
// @Description 为单个实例执行签到续期
// @Tags 用户/签到续期
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body checkin.DoCheckinRequest true "签到请求"
// @Success 200 {object} common.Response "签到成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "认证失败"
// @Router /user/checkin [post]
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
// @Summary 获取签到记录
// @Description 获取当前用户的签到续期记录，支持分页和筛选
// @Tags 用户/签到续期
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码" default(1)
// @Param pageSize query int false "每页数量" default(10)
// @Param startDate query string false "开始日期 YYYY-MM-DD"
// @Param endDate query string false "结束日期 YYYY-MM-DD"
// @Param instanceId query int false "实例ID"
// @Param result query string false "结果筛选" Enums(all,success,failed)
// @Success 200 {object} common.Response{data=object} "获取成功"
// @Failure 401 {object} common.Response "认证失败"
// @Router /user/checkin/records [get]
func GetCheckinRecords(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "10"))
	page, pageSize = common.NormalizePagination(page, pageSize, common.DefaultPageSize)
	instanceID64, _ := strconv.ParseUint(c.DefaultQuery("instanceId", "0"), 10, 64)
	filter := &checkinService.CheckinRecordsFilter{
		StartDate:  c.Query("startDate"),
		EndDate:    c.Query("endDate"),
		InstanceID: uint(instanceID64),
		Result:     c.DefaultQuery("result", "all"),
	}

	svc := &checkinService.Service{}
	records, total, err := svc.GetCheckinRecordsFiltered(userID, page, pageSize, filter)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, gin.H{
		"list":  records,
		"total": total,
	})
}

// GetCheckinStats 获取签到统计
// @Summary 获取签到统计
// @Description 获取当前用户签到次数、连续签到、最长连续签到和累计续期天数
// @Tags 用户/签到续期
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response{data=checkin.CheckinStats} "获取成功"
// @Failure 401 {object} common.Response "认证失败"
// @Router /user/checkin/stats [get]
func GetCheckinStats(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	svc := &checkinService.Service{}
	stats, err := svc.GetCheckinStats(userID)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, stats)
}

// BatchCheckin 用户批量签到
// @Summary 批量签到续期
// @Description 为多个实例执行签到续期，最多 50 个实例
// @Tags 用户/签到续期
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body checkin.BatchCheckinRequest true "批量签到请求"
// @Success 200 {object} common.Response{data=checkin.BatchCheckinResult} "签到成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "认证失败"
// @Router /user/checkin/batch [post]
// @Router /user/checkin/batch-checkin [post]
func BatchCheckin(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}

	var req checkinService.BatchCheckinRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	svc := &checkinService.Service{}
	result, err := svc.BatchCheckin(userID, &req)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, result)
}
