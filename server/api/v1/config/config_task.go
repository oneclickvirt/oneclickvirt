package config

import (
	"context"
	"fmt"
	"net/http"
	provider2 "oneclickvirt/service/provider"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/middleware"
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/model/common"
	"oneclickvirt/model/provider"
	"oneclickvirt/service/config"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// AutoConfigureProvider 自动配置Provider
// @Summary 自动配置Provider
// @Description 自动配置Provider，支持检查历史记录和防重复执行
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body adminModel.AutoConfigureRequest true "自动配置请求"
// @Success 200 {object} common.Response{data=adminModel.AutoConfigureResponse} "配置响应"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 403 {object} common.Response "权限不足"
// @Failure 500 {object} common.Response "配置失败"
// @Router /admin/provider/auto-configure [post]
func AutoConfigureProvider(c *gin.Context) {
	var req adminModel.AutoConfigureRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Error("请求参数错误: "+err.Error()))
		return
	}

	// 获取用户信息
	authCtx, exists := middleware.GetAuthContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, common.Error("认证失败"))
		return
	}

	// 检查Provider是否存在
	var provider provider.Provider
	if err := global.APP_DB.First(&provider, req.ProviderID).Error; err != nil {
		c.JSON(http.StatusBadRequest, common.Error("Provider不存在"))
		return
	}

	// 检查Provider类型
	if provider.Type != "lxd" && provider.Type != "incus" && provider.Type != "proxmox" {
		c.JSON(http.StatusBadRequest, common.Error("不支持的Provider类型: "+provider.Type))
		return
	}

	configService := config.GetTaskService()

	// 检查是否有正在运行的任务
	runningTask := configService.GetRunningTask(req.ProviderID)

	// 获取历史任务
	historyTasks, err := configService.GetProviderHistory(req.ProviderID, 5)
	if err != nil {
		global.APP_LOG.Error("获取历史任务失败", zap.Error(err))
	}

	response := &adminModel.AutoConfigureResponse{
		CanProceed:   runningTask == nil || req.Force,
		HistoryTasks: historyTasks,
	}

	// 如果有正在运行的任务且不强制执行
	if runningTask != nil && !req.Force {
		response.Status = "running"
		response.Message = fmt.Sprintf("Provider %s 正在执行配置任务", provider.Name)
		response.RunningTask = &adminModel.ConfigurationTaskResponse{
			ID:           runningTask.ID,
			ProviderID:   runningTask.ProviderID,
			ProviderName: provider.Name,
			ProviderType: provider.Type,
			TaskType:     runningTask.TaskType,
			Status:       runningTask.Status,
			Progress:     runningTask.Progress,
			StartedAt:    runningTask.StartedAt,
			ExecutorID:   runningTask.ExecutorID,
			ExecutorName: runningTask.ExecutorName,
		}
		response.StreamURL = fmt.Sprintf("/api/v1/admin/provider/%d/auto-configure-stream/%d", req.ProviderID, runningTask.ID)

		c.JSON(http.StatusOK, common.Success(response))
		return
	}

	// 如果只是查看历史记录
	if req.ShowHistory {
		response.Status = "history"
		response.Message = "历史记录查询成功"
		c.JSON(http.StatusOK, common.Success(response))
		return
	}

	// 如果有正在运行的任务且强制执行，先取消原任务
	if runningTask != nil && req.Force {
		if err := configService.CancelTask(runningTask.ID); err != nil {
			global.APP_LOG.Error("取消原任务失败", zap.Error(err))
		}
	}

	// 创建新任务
	task, err := configService.CreateTask(
		req.ProviderID,
		adminModel.TaskTypeAutoConfig,
		authCtx.UserID,
		authCtx.Username,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Error("创建任务失败: "+err.Error()))
		return
	}

	// 启动任务
	if err := configService.StartTask(task.ID); err != nil {
		c.JSON(http.StatusInternalServerError, common.Error("启动任务失败: "+err.Error()))
		return
	}

	// 异步执行配置（带超时控制和统一生命周期管理）
	go func() {
		defer func() {
			if r := recover(); r != nil {
				global.APP_LOG.Error("自动配置执行panic",
					zap.Uint("taskId", task.ID),
					zap.Uint("providerId", req.ProviderID),
					zap.Any("panic", r))
			}
		}()

		// 创备2分钟超时的context，但与系统关闭信号关联
		ctx, cancel := context.WithTimeout(global.APP_SHUTDOWN_CONTEXT, 2*time.Minute)
		defer cancel()

		// 使用带context的执行函数
		if err := executeAutoConfigurationWithContext(ctx, task.ID, &provider); err != nil {
			global.APP_LOG.Error("自动配置执行失败",
				zap.Uint("taskId", task.ID),
				zap.Uint("providerId", req.ProviderID),
				zap.Error(err))
		}
	}()

	response.TaskID = task.ID
	response.Status = "started"
	response.Message = fmt.Sprintf("已开始为 %s 执行自动配置，请稍后查看任务详情", provider.Name)

	c.JSON(http.StatusOK, common.Success(response))
}

// GetConfigurationTasks 获取配置任务列表
// @Summary 获取配置任务列表
// @Description 获取配置任务列表，支持分页和筛选
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码" default(1)
// @Param pageSize query int false "页大小" default(10)
// @Param providerId query int false "Provider ID"
// @Param taskType query string false "任务类型"
// @Param status query string false "任务状态"
// @Param executorId query int false "执行者ID"
// @Success 200 {object} common.Response{data=adminModel.ConfigurationTaskListResponse} "获取成功"
// @Failure 500 {object} common.Response "获取失败"
// @Router /admin/configuration-tasks [get]
func GetConfigurationTasks(c *gin.Context) {
	var req adminModel.ConfigurationTaskListRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Error("请求参数错误: "+err.Error()))
		return
	}

	// 设置默认值
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 {
		req.PageSize = 10
	}

	configService := config.GetTaskService()
	tasks, total, err := configService.GetTaskList(&req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Error("获取任务列表失败: "+err.Error()))
		return
	}

	response := adminModel.ConfigurationTaskListResponse{
		List:  tasks,
		Total: total,
	}

	c.JSON(http.StatusOK, common.Success(response))
}

// GetConfigurationTaskDetail 获取配置任务详情
// @Summary 获取配置任务详情
// @Description 获取指定配置任务的详细信息，包括完整日志
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "任务ID"
// @Success 200 {object} common.Response{data=adminModel.ConfigurationTaskDetailResponse} "获取成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 404 {object} common.Response "任务不存在"
// @Failure 500 {object} common.Response "获取失败"
// @Router /admin/configuration-tasks/{id} [get]
func GetConfigurationTaskDetail(c *gin.Context) {
	idStr := c.Param("id")
	taskID, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Error("无效的任务ID"))
		return
	}

	configService := config.GetTaskService()
	task, err := configService.GetTaskDetail(uint(taskID))
	if err != nil {
		c.JSON(http.StatusNotFound, common.Error("任务不存在"))
		return
	}

	c.JSON(http.StatusOK, common.Success(task))
}

// CancelConfigurationTask 取消配置任务
// @Summary 取消配置任务
// @Description 取消正在运行的配置任务
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "任务ID"
// @Success 200 {object} common.Response "取消成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 404 {object} common.Response "任务不存在"
// @Failure 500 {object} common.Response "取消失败"
// @Router /admin/configuration-tasks/{id}/cancel [post]
func CancelConfigurationTask(c *gin.Context) {
	idStr := c.Param("id")
	taskID, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Error("无效的任务ID"))
		return
	}

	configService := config.GetTaskService()
	if err := configService.CancelTask(uint(taskID)); err != nil {
		c.JSON(http.StatusInternalServerError, common.Error("取消任务失败: "+err.Error()))
		return
	}

	c.JSON(http.StatusOK, common.Success("任务已取消"))
}

// executeAutoConfiguration 执行自动配置（支持context取消）
func executeAutoConfigurationWithContext(ctx context.Context, taskID uint, provider *provider.Provider) error {
	configService := config.GetTaskService()

	// 创建简单的日志缓冲区
	var logBuffer strings.Builder
	var success bool
	var errorMessage string

	// 简单的日志记录函数
	writeLog := func(format string, args ...interface{}) {
		line := fmt.Sprintf(format, args...)
		logBuffer.WriteString(line)
		logBuffer.WriteString("\n")

		// 实时更新到数据库
		configService.UpdateTaskLog(taskID, logBuffer.String())
	}

	// 执行配置任务
	func() {
		defer func() {
			if r := recover(); r != nil {
				success = false
				errorMessage = fmt.Sprintf("配置过程中发生错误: %v", r)
				writeLog("❌ 配置过程中发生错误: %v", r)
			}
		}()

		// 检查context是否已经取消
		select {
		case <-ctx.Done():
			success = false
			errorMessage = "任务被取消或超时"
			writeLog("❌ 任务被取消或超时")
			return
		default:
		}

		// 记录开始日志
		writeLog("=== 开始自动配置 %s Provider: %s ===", provider.Type, provider.Name)
		writeLog("Provider地址: %s", provider.Endpoint)
		writeLog("SSH用户: %s", provider.Username)
		writeLog("⏰ 任务超时时间: 2分钟")

		// 更新进度
		configService.UpdateTaskProgress(taskID, 10)

		// 创建一个简单的输出通道用于日志收集
		logChan := make(chan string, 100)
		configDone := make(chan error, 1)

		// 启动日志收集协程
		go func() {
			for logLine := range logChan {
				writeLog("%s", logLine)
			}
		}()

		// 启动配置执行协程
		go func() {
			defer close(logChan)
			// 执行实际的配置逻辑
			certService := &provider2.CertService{}
			configDone <- certService.AutoConfigureProviderWithStream(provider, logChan)
		}()

		// 等待配置完成或context取消
		select {
		case err := <-configDone:
			if err != nil {
				success = false
				errorMessage = err.Error()
				writeLog("❌ 自动配置失败: %s", err.Error())
				return
			}
			success = true

			// 根据类型返回不同的成功消息
			var message string
			switch provider.Type {
			case "proxmox":
				message = "Proxmox VE API 自动配置成功，Token已创建并应用到系统"
			case "lxd":
				message = "LXD 自动配置成功，证书已安装并配置监听地址"
			case "incus":
				message = "Incus 自动配置成功，证书已安装并配置监听地址"
			default:
				message = "自动配置成功"
			}
			writeLog("✅ %s", message)

		case <-ctx.Done():
			success = false
			if ctx.Err() == context.DeadlineExceeded {
				errorMessage = "任务执行超时（超过2分钟）"
				writeLog("❌ 任务执行超时（超过2分钟），自动终止")
			} else {
				errorMessage = "任务被取消"
				writeLog("❌ 任务被手动取消")
			}
			return
		}
	}()

	// 最终更新进度
	if success {
		configService.UpdateTaskProgress(taskID, 100)
	}

	// 完成任务
	resultData := map[string]interface{}{
		"providerId":   provider.ID,
		"providerName": provider.Name,
		"providerType": provider.Type,
		"configuredAt": time.Now().Format(time.RFC3339),
	}

	return configService.FinishTask(taskID, success, errorMessage, resultData)
}
