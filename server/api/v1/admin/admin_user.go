package admin

import (
	"fmt"
	"oneclickvirt/service/provider"
	"strconv"
	"strings"

	"oneclickvirt/middleware"
	"oneclickvirt/model/admin"
	"oneclickvirt/model/common"
	"oneclickvirt/service/admin/user"

	"github.com/gin-gonic/gin"
)

// requireAdminOnly 检查是否为管理员用户
func requireAdminOnly(c *gin.Context) bool {
	authCtx, exists := middleware.GetAuthContext(c)
	if !exists {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "用户未认证"))
		return false
	}

	if authCtx.UserType != "admin" {
		common.ResponseWithError(c, common.NewError(common.CodeForbidden, "此操作仅限管理员执行"))
		return false
	}

	return true
}

// GetUserList 获取用户列表
// @Summary 获取用户列表
// @Description 获取系统中所有用户的列表（分页）
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码" default(1)
// @Param pageSize query int false "每页数量" default(10)
// @Param username query string false "用户名搜索"
// @Param nickname query string false "昵称搜索"
// @Param email query string false "邮箱搜索"
// @Param status query string false "用户状态"
// @Success 200 {object} common.Response{data=object} "获取成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "获取失败"
// @Router /admin/users [get]
func GetUserList(c *gin.Context) {
	var req admin.UserListRequest

	// 使用请求处理服务处理参数
	requestProcessService := provider.RequestProcessService{}
	if err := requestProcessService.ProcessUserListRequest(c, &req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	userService := user.NewService()
	users, total, err := userService.GetUserList(req)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取用户列表失败"))
		return
	}

	common.ResponseSuccessWithPagination(c, users, total, req.Page, req.PageSize)
}

// CreateUser 创建用户
// @Summary 创建用户
// @Description 管理员创建新用户账户
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body admin.CreateUserRequest true "创建用户请求参数"
// @Success 200 {object} common.Response "创建成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "创建失败"
// @Router /admin/users [post]
func CreateUser(c *gin.Context) {
	// 只有管理员可以创建用户
	if !requireAdminOnly(c) {
		return
	}

	var req admin.CreateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	userService := user.NewService()
	err := userService.CreateUser(req)
	if err != nil {
		// 根据错误内容选择合适的错误码
		if strings.Contains(err.Error(), "用户名已存在") {
			common.ResponseWithError(c, common.NewError(common.CodeUserExists, err.Error()))
		} else {
			common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		}
		return
	}

	common.ResponseSuccess(c, nil, "创建用户成功")
}

func UpdateUser(c *gin.Context) {
	userIDStr := c.Param("id")
	userID, err := strconv.ParseUint(userIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "无效的用户ID"))
		return
	}

	var req admin.UpdateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	// 设置用户ID
	req.ID = uint(userID)

	// 获取当前用户ID
	currentUserID, err := getUserIDFromContext(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "未找到用户信息"))
		return
	}

	userService := user.NewService()
	err = userService.UpdateUser(req, currentUserID)
	if err != nil {
		common.ResponseWithError(c, err)
		return
	}

	common.ResponseSuccess(c, nil, "更新用户成功")
}

func DeleteUser(c *gin.Context) {
	// 只有管理员可以删除用户
	if !requireAdminOnly(c) {
		return
	}

	userIDStr := c.Param("id")
	userID, err := strconv.ParseUint(userIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "无效的用户ID"))
		return
	}

	userService := user.NewService()
	err = userService.DeleteUser(uint(userID))
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	common.ResponseSuccess(c, nil, "删除用户成功")
}

// UpdateUserStatus 更新用户状态
// @Summary 更新用户状态
// @Description 管理员更新用户状态（启用/禁用）
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "用户ID"
// @Param request body admin.UpdateUserStatusRequest true "更新用户状态请求参数"
// @Success 200 {object} common.Response "更新成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "更新失败"
// @Router /admin/users/{id}/status [put]
func UpdateUserStatus(c *gin.Context) {
	userIDStr := c.Param("id")
	userID, err := strconv.ParseUint(userIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "无效的用户ID"))
		return
	}

	var req admin.UpdateUserStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	userService := user.NewService()
	err = userService.UpdateUserStatus(uint(userID), req.Status)
	if err != nil {
		common.ResponseWithError(c, err)
		return
	}

	common.ResponseSuccess(c, nil, "更新用户状态成功")
}

// AdminBatchDeleteUsers 管理员批量删除用户
// @Summary 管理员批量删除用户
// @Description 管理员批量删除用户
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body admin.BatchDeleteUsersRequest true "批量删除用户请求参数"
// @Success 200 {object} common.Response "删除成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "删除失败"
// @Router /admin/users/batch-delete [post]
func AdminBatchDeleteUsers(c *gin.Context) {
	// 只有管理员可以批量删除用户
	if !requireAdminOnly(c) {
		return
	}

	var req admin.BatchDeleteUsersRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	if len(req.UserIDs) == 0 {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "用户ID列表不能为空"))
		return
	}

	userService := user.NewService()
	err := userService.BatchDeleteUsers(req.UserIDs)
	if err != nil {
		// 根据错误信息判断错误类型
		errMsg := err.Error()
		if errMsg == "不能删除管理员用户" {
			common.ResponseWithError(c, common.NewError(common.CodeForbidden, errMsg))
		} else {
			common.ResponseWithError(c, common.NewError(common.CodeInternalError, errMsg))
		}
		return
	}

	common.ResponseSuccess(c, nil, "批量删除用户成功")
}

// AdminBatchUpdateUserStatus 管理员批量更新用户状态
// @Summary 管理员批量更新用户状态
// @Description 管理员批量更新用户状态（启用/禁用）
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body admin.BatchUpdateUserStatusRequest true "批量更新用户状态请求参数"
// @Success 200 {object} common.Response "更新成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "更新失败"
// @Router /admin/users/batch-status [put]
func AdminBatchUpdateUserStatus(c *gin.Context) {
	var req admin.BatchUpdateUserStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	if len(req.UserIDs) == 0 {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "用户ID列表不能为空"))
		return
	}

	userService := user.NewService()
	err := userService.BatchUpdateUserStatus(req.UserIDs, req.Status)
	if err != nil {
		// 根据错误信息判断错误类型
		errMsg := err.Error()
		if errMsg == "不能修改管理员用户状态" {
			common.ResponseWithError(c, common.NewError(common.CodeForbidden, errMsg))
		} else {
			common.ResponseWithError(c, common.NewError(common.CodeInternalError, errMsg))
		}
		return
	}

	statusText := "启用"
	if req.Status == 0 {
		statusText = "禁用"
	}
	common.ResponseSuccess(c, nil, "批量"+statusText+"用户成功")
}

// AdminBatchUpdateUserLevel 管理员批量更新用户等级
// @Summary 批量更新用户等级
// @Description 管理员批量更新多个用户的等级
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body admin.BatchUpdateUserLevelRequest true "批量更新用户等级请求参数"
// @Success 200 {object} common.Response "更新成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "更新失败"
// @Router /admin/users/batch-level [put]
func AdminBatchUpdateUserLevel(c *gin.Context) {
	var req admin.BatchUpdateUserLevelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	if len(req.UserIDs) == 0 {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "用户ID列表不能为空"))
		return
	}

	userService := user.NewService()
	err := userService.BatchUpdateUserLevel(req.UserIDs, req.Level)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	common.ResponseSuccess(c, nil, fmt.Sprintf("批量设置用户等级为%d成功", req.Level))
}

// UpdateUserLevel 更新单个用户等级
// @Summary 更新用户等级
// @Description 管理员更新指定用户的等级
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "用户ID"
// @Param request body admin.UpdateUserLevelRequest true "更新用户等级请求参数"
// @Success 200 {object} common.Response "更新成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "更新失败"
// @Router /admin/users/{id}/level [put]
func UpdateUserLevel(c *gin.Context) {
	userIDStr := c.Param("id")
	userID, err := strconv.ParseUint(userIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "无效的用户ID"))
		return
	}

	var req admin.UpdateUserLevelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	userService := user.NewService()
	err = userService.UpdateUserLevel(uint(userID), req.Level)
	if err != nil {
		common.ResponseWithError(c, err)
		return
	}

	common.ResponseSuccess(c, nil, fmt.Sprintf("设置用户等级为%d成功", req.Level))
}

// ResetUserPassword 管理员强制重置用户密码
// @Summary 管理员强制重置用户密码
// @Description 管理员强制重置指定用户的登录密码，系统自动生成符合安全策略的新密码
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "用户ID"
// @Param request body admin.ResetUserPasswordRequest true "重置密码请求参数（可为空对象）"
// @Success 200 {object} common.Response{data=admin.ResetUserPasswordResponse} "重置成功，返回新密码"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 404 {object} common.Response "用户不存在"
// @Failure 500 {object} common.Response "重置失败"
// @Router /admin/users/{id}/reset-password [put]
func ResetUserPassword(c *gin.Context) {
	// 只有管理员可以重置用户密码
	if !requireAdminOnly(c) {
		return
	}

	userIDStr := c.Param("id")
	userID, err := strconv.ParseUint(userIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "无效的用户ID"))
		return
	}

	var req admin.ResetUserPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// 由于不再需要密码参数，这里可以忽略绑定错误
	}

	userService := user.NewService()
	newPassword, err := userService.ResetUserPassword(uint(userID))
	if err != nil {
		common.ResponseWithError(c, err)
		return
	}

	// 返回生成的密码
	response := admin.ResetUserPasswordResponse{
		NewPassword: newPassword,
	}

	common.ResponseSuccess(c, response, "重置用户密码成功")
}

// ResetUserPasswordAndNotify 管理员重置用户密码并发送到用户通信渠道
// @Summary 管理员重置用户密码并发送到用户通信渠道
// @Description 管理员重置指定用户的登录密码，系统自动生成符合安全策略的新密码并发送到用户绑定的通信渠道
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "用户ID"
// @Param request body admin.ResetUserPasswordRequest true "重置密码请求参数（可为空对象）"
// @Success 200 {object} common.Response "重置成功，新密码已发送到用户绑定的通信渠道"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 404 {object} common.Response "用户不存在"
// @Failure 500 {object} common.Response "重置失败"
// @Router /admin/users/{id}/reset-password-notify [put]
func ResetUserPasswordAndNotify(c *gin.Context) {
	// 只有管理员可以重置用户密码
	if !requireAdminOnly(c) {
		return
	}

	userIDStr := c.Param("id")
	userID, err := strconv.ParseUint(userIDStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "无效的用户ID"))
		return
	}

	var req admin.ResetUserPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// 由于不再需要密码参数，这里可以忽略绑定错误
	}

	userService := user.NewService()
	err = userService.ResetUserPasswordAndNotify(uint(userID))
	if err != nil {
		common.ResponseWithError(c, err)
		return
	}

	common.ResponseSuccess(c, nil, "重置用户密码成功，新密码已发送到用户绑定的通信渠道")
}
