package admin

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/model/common"
	trafficMonitorService "oneclickvirt/service/admin/traffic_monitor"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// TrafficMonitorOperation 流量监控操作
// @Summary 流量监控操作
// @Description 批量启用、删除或检测Provider下所有实例的流量监控
// @Tags 流量监控管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body adminModel.TrafficMonitorOperationRequest true "操作请求"
// @Success 200 {object} common.Response{data=object} "操作成功"
// @Failure 400 {object} common.Response "请求参数错误"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /admin/provider/traffic-monitor [post]
func TrafficMonitorOperation(c *gin.Context) {
	var req adminModel.TrafficMonitorOperationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "参数错误: " + err.Error(),
		})
		return
	}

	// 确定任务类型
	var taskType string
	switch req.Operation {
	case "enable":
		taskType = "enable_all"
	case "disable":
		taskType = "disable_all"
	case "detect":
		taskType = "detect_all"
	default:
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "不支持的操作类型",
		})
		return
	}

	// 创建任务记录
	task := adminModel.TrafficMonitorTask{
		ProviderID: req.ProviderID,
		TaskType:   taskType,
		Status:     "pending",
		Progress:   0,
		Message:    "任务已创建，等待执行",
	}

	if err := global.APP_DB.Create(&task).Error; err != nil {
		global.APP_LOG.Error("创建流量监控任务失败",
			zap.Uint("providerID", req.ProviderID),
			zap.String("operation", req.Operation),
			zap.Error(err))
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  "创建任务失败",
		})
		return
	}

	// 异步执行任务
	go func(taskID uint, providerID uint, operation string) {
		defer func() {
			if r := recover(); r != nil {
				global.APP_LOG.Error("流量监控任务执行panic",
					zap.Uint("taskID", taskID),
					zap.Any("panic", r),
					zap.Stack("stack"))

				// 更新任务状态为失败
				completedAt := time.Now()
				global.APP_DB.Model(&adminModel.TrafficMonitorTask{}).
					Where("id = ?", taskID).
					Updates(map[string]interface{}{
						"status":       "failed",
						"message":      fmt.Sprintf("任务执行异常: %v", r),
						"completed_at": completedAt,
					})
			}
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()

		manager := trafficMonitorService.GetManager()

		var err error
		switch operation {
		case "enable":
			err = manager.BatchEnableMonitoring(ctx, providerID, taskID)
		case "disable":
			err = manager.BatchDisableMonitoring(ctx, providerID, taskID)
		case "detect":
			err = manager.BatchDetectMonitoring(ctx, providerID, taskID)
		}

		if err != nil {
			global.APP_LOG.Error("流量监控任务执行失败",
				zap.Uint("taskID", taskID),
				zap.String("operation", operation),
				zap.Error(err))
		}
	}(task.ID, req.ProviderID, req.Operation)

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "任务已创建",
		Data: map[string]interface{}{
			"taskId": task.ID,
		},
	})
}

// GetTrafficMonitorTaskList 获取流量监控任务列表
// @Summary 获取流量监控任务列表
// @Description 查询流量监控操作任务列表
// @Tags 流量监控管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码" default(1)
// @Param pageSize query int false "每页数量" default(10)
// @Param providerId query int false "Provider ID"
// @Param taskType query string false "任务类型"
// @Param status query string false "任务状态"
// @Success 200 {object} common.Response{data=object} "查询成功"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /admin/provider/traffic-monitor/tasks [get]
func GetTrafficMonitorTaskList(c *gin.Context) {
	var req adminModel.TrafficMonitorTaskListRequest
	req.Page = 1
	req.PageSize = 10

	if err := c.ShouldBindQuery(&req); err != nil {
		global.APP_LOG.Warn("任务列表查询参数绑定失败，使用默认值", zap.Error(err))
	}

	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 || req.PageSize > 100 {
		req.PageSize = 10
	}

	db := global.APP_DB.Model(&adminModel.TrafficMonitorTask{})

	// 应用筛选条件
	if req.ProviderID > 0 {
		db = db.Where("provider_id = ?", req.ProviderID)
	}
	if req.TaskType != "" {
		db = db.Where("task_type = ?", req.TaskType)
	}
	if req.Status != "" {
		db = db.Where("status = ?", req.Status)
	}

	// 查询总数
	var total int64
	if err := db.Count(&total).Error; err != nil {
		global.APP_LOG.Error("查询任务总数失败", zap.Error(err))
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  "查询失败",
		})
		return
	}

	// 查询列表
	var tasks []adminModel.TrafficMonitorTask
	offset := (req.Page - 1) * req.PageSize
	if err := db.Order("created_at DESC").
		Limit(req.PageSize).
		Offset(offset).
		Find(&tasks).Error; err != nil {
		global.APP_LOG.Error("查询任务列表失败", zap.Error(err))
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  "查询失败",
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "查询成功",
		Data: map[string]interface{}{
			"list":  tasks,
			"total": total,
		},
	})
}

// GetTrafficMonitorTaskDetail 获取流量监控任务详情
// @Summary 获取流量监控任务详情
// @Description 获取指定任务的详细信息和输出日志
// @Tags 流量监控管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "任务ID"
// @Success 200 {object} common.Response{data=adminModel.TrafficMonitorTask} "查询成功"
// @Failure 404 {object} common.Response "任务不存在"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /admin/provider/traffic-monitor/tasks/{id} [get]
func GetTrafficMonitorTaskDetail(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "无效的任务ID",
		})
		return
	}

	var task adminModel.TrafficMonitorTask
	if err := global.APP_DB.First(&task, uint(id)).Error; err != nil {
		c.JSON(http.StatusNotFound, common.Response{
			Code: 404,
			Msg:  "任务不存在",
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "查询成功",
		Data: task,
	})
}

// GetLatestTrafficMonitorTask 获取Provider的最新流量监控任务
// @Summary 获取Provider的最新流量监控任务
// @Description 获取指定Provider的最新流量监控任务（用于显示运行中的任务）
// @Tags 流量监控管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param providerId query int true "Provider ID"
// @Success 200 {object} common.Response{data=adminModel.TrafficMonitorTask} "查询成功"
// @Failure 404 {object} common.Response "没有任务"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /admin/provider/traffic-monitor/latest [get]
func GetLatestTrafficMonitorTask(c *gin.Context) {
	providerIDStr := c.Query("providerId")
	if providerIDStr == "" {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "缺少providerId参数",
		})
		return
	}

	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "无效的providerId",
		})
		return
	}

	var task adminModel.TrafficMonitorTask
	if err := global.APP_DB.Where("provider_id = ?", uint(providerID)).
		Order("created_at DESC").
		First(&task).Error; err != nil {
		c.JSON(http.StatusNotFound, common.Response{
			Code: 404,
			Msg:  "没有找到任务",
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "查询成功",
		Data: task,
	})
}
