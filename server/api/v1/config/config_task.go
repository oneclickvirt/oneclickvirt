package config

import (
	"context"
	"fmt"
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

const autoConfigureTaskTimeout = 30 * time.Minute

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
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "请求参数错误: "+err.Error()))
		return
	}

	// 获取用户信息
	authCtx, exists := middleware.GetAuthContext(c)
	if !exists {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "认证失败"))
		return
	}

	// 检查Provider是否存在且属于当前普通管理员
	var provider provider.Provider
	providerQuery := global.APP_DB.Where("id = ?", req.ProviderID)
	if ownerAdminID := middleware.GetOwnerAdminID(c); ownerAdminID > 0 {
		providerQuery = providerQuery.Where("owner_admin_id = ?", ownerAdminID)
	}
	if err := providerQuery.First(&provider).Error; err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "Provider不存在"))
		return
	}

	// 检查Provider类型
	if provider.Type != "lxd" && provider.Type != "incus" && provider.Type != "proxmox" && provider.Type != "proxmoxve" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "不支持的Provider类型: "+provider.Type))
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
	if runningTask != nil && (runningTask.Status == adminModel.TaskStatusCancelled || runningTask.Status == adminModel.TaskStatusCancelling) {
		response.CanProceed = false
	}

	// 取消请求已经提交但旧协程尚未退出时，仍然占用Provider执行槽；
	// 将其展示为停止中，避免用户误以为可以立即启动第二个配置任务。
	if runningTask != nil && (runningTask.Status == adminModel.TaskStatusCancelled || runningTask.Status == adminModel.TaskStatusCancelling || !req.Force) {
		response.Status = "running"
		response.Message = fmt.Sprintf("Provider %s 正在执行配置任务", provider.Name)
		if runningTask.Status == adminModel.TaskStatusCancelled || runningTask.Status == adminModel.TaskStatusCancelling {
			response.Status = "cancelling"
			response.Message = fmt.Sprintf("Provider %s 的旧配置任务正在停止，请稍后重新执行", provider.Name)
		}
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

		common.ResponseSuccess(c, response)
		return
	}

	// 如果只是查看历史记录
	if req.ShowHistory {
		response.Status = "history"
		response.Message = "历史记录查询成功"
		common.ResponseSuccess(c, response)
		return
	}

	// 如果有正在运行的任务且强制执行，先取消原任务
	if runningTask != nil && req.Force {
		if err := configService.CancelTaskScoped(runningTask.ID, middleware.GetOwnerAdminID(c)); err != nil {
			global.APP_LOG.Error("取消原任务失败", zap.Error(err))
			common.ResponseWithError(c, common.ClassifyError(err))
			return
		}
		// 取消会立即发出 context 信号，但旧配置协程可能仍在等待远端
		// 调用返回。等待一个有界时间；超时就让用户稍后重试，绝不并发
		// 启动第二个 Provider 配置。
		if err := configService.WaitForTaskRelease(req.ProviderID, runningTask.ID, 10*time.Second); err != nil {
			response.Status = "cancelling"
			response.CanProceed = false
			response.Message = fmt.Sprintf("Provider %s 的旧配置任务正在停止，请稍后重新执行", provider.Name)
			common.ResponseSuccess(c, response)
			return
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
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	// 启动任务
	if err := configService.StartTask(task.ID); err != nil {
		if cancelErr := configService.CancelTask(task.ID); cancelErr != nil {
			global.APP_LOG.Warn("清理启动失败的配置任务失败", zap.Uint("taskId", task.ID), zap.Error(cancelErr))
		}
		common.ResponseWithError(c, common.ClassifyError(err))
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
				_ = configService.FinishTask(task.ID, false, fmt.Sprintf("配置过程发生异常: %v", r), nil)
			}
		}()

		// 与任务取消信号和系统关闭信号关联。
		baseContext := configService.GetTaskContext(task.ID)
		if baseContext == nil {
			baseContext = global.APP_SHUTDOWN_CONTEXT
		}
		if baseContext == nil {
			baseContext = context.Background()
		}
		ctx, cancel := context.WithTimeout(baseContext, autoConfigureTaskTimeout)
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

	common.ResponseSuccess(c, response)
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
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "请求参数错误: "+err.Error()))
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
	tasks, total, err := configService.GetTaskList(&req, middleware.GetOwnerAdminID(c))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	response := adminModel.ConfigurationTaskListResponse{
		List:  tasks,
		Total: total,
	}

	common.ResponseSuccess(c, response)
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
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的任务ID"))
		return
	}

	configService := config.GetTaskService()
	task, err := configService.GetTaskDetail(uint(taskID), middleware.GetOwnerAdminID(c))
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeNotFound, "任务不存在"))
		return
	}

	common.ResponseSuccess(c, task)
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
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的任务ID"))
		return
	}

	configService := config.GetTaskService()
	if err := configService.CancelTaskScoped(uint(taskID), middleware.GetOwnerAdminID(c)); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, nil, "已请求停止任务")
}

// executeAutoConfiguration 执行自动配置（支持context取消）
func executeAutoConfigurationWithContext(ctx context.Context, taskID uint, provider *provider.Provider) error {
	configService := config.GetTaskService()

	var logBuffer strings.Builder
	var success bool
	var errorMessage string
	lastFlush := time.Time{}
	flushLog := func() {
		if err := configService.UpdateTaskLog(taskID, logBuffer.String()); err != nil {
			global.APP_LOG.Warn("保存配置任务日志失败", zap.Uint("taskId", taskID), zap.Error(err))
		}
		lastFlush = time.Now()
	}
	// One goroutine owns the buffer. Limit the retained tail and coalesce DB
	// writes so verbose installers cannot create quadratic log storage traffic.
	writeLog := func(format string, args ...interface{}) {
		line := fmt.Sprintf(format, args...)
		logBuffer.WriteString(line)
		logBuffer.WriteString("\n")
		if logBuffer.Len() > 256*1024 {
			tail := logBuffer.String()
			tail = tail[len(tail)-128*1024:]
			if newline := strings.IndexByte(tail, '\n'); newline >= 0 {
				tail = tail[newline+1:]
			}
			logBuffer.Reset()
			logBuffer.WriteString(strings.ToValidUTF8(tail, ""))
		}
		if time.Since(lastFlush) >= time.Second {
			flushLog()
		}
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
		writeLog("⏰ 任务超时时间: %s", autoConfigureTaskTimeout)

		// 更新进度
		configService.UpdateTaskProgress(taskID, 10)

		certService := &provider2.CertService{}
		err := runConfigurationWithLogs(func(logs chan string) error {
			return certService.AutoConfigureProviderWithStreamContext(ctx, provider, logs)
		}, func(line string) { writeLog("%s", line) })
		// The producer has exited and every log line has been consumed before
		// cancellation is finalized or a new configuration may reserve this slot.
		if ctx.Err() != nil {
			errorMessage = "任务已取消"
			if ctx.Err() == context.DeadlineExceeded {
				errorMessage = "任务执行超时"
			}
			writeLog("❌ %s", errorMessage)
			return
		}
		if err != nil {
			errorMessage = err.Error()
			writeLog("❌ 自动配置失败: %s", errorMessage)
			return
		}
		success = true
		writeLog("✅ 自动配置成功")

	}()

	flushLog()

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

// runConfigurationWithLogs joins the producer even after cancellation; callers
// retain provider ownership until it returns. Panics become normal failures.
func runConfigurationWithLogs(run func(chan string) error, log func(string)) error {
	logs := make(chan string, 100)
	done := make(chan error, 1)
	go func() {
		var result error
		defer func() {
			if r := recover(); r != nil {
				result = fmt.Errorf("配置过程中发生错误: %v", r)
			}
			close(logs)
			done <- result
		}()
		result = run(logs)
	}()
	for line := range logs {
		log(line)
	}
	return <-done
}
