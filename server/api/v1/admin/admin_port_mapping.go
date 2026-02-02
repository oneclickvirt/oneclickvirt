package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"oneclickvirt/global"
	"oneclickvirt/middleware"
	"oneclickvirt/model/admin"
	"oneclickvirt/model/common"
	"oneclickvirt/model/provider"
	"oneclickvirt/service/resources"
	"oneclickvirt/service/task"
	"oneclickvirt/utils"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// GetPortMappingList 获取端口映射列表
// @Summary 获取端口映射列表
// @Description 管理员获取端口映射列表
// @Tags 端口映射管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码" default(1)
// @Param pageSize query int false "每页数量" default(10)
// @Param providerId query int false "Provider ID"
// @Param instanceId query int false "实例ID"
// @Param protocol query string false "协议类型"
// @Param status query string false "状态"
// @Success 200 {object} common.Response{data=object} "获取成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "获取失败"
// @Router /admin/port-mappings [get]
func GetPortMappingList(c *gin.Context) {
	var req admin.PortMappingListRequest

	// 解析查询参数
	req.Page, _ = strconv.Atoi(c.DefaultQuery("page", "1"))
	req.PageSize, _ = strconv.Atoi(c.DefaultQuery("pageSize", "10"))
	req.Keyword = c.Query("keyword") // 搜索关键字

	if providerID := c.Query("providerId"); providerID != "" {
		if id, err := strconv.ParseUint(providerID, 10, 32); err == nil {
			req.ProviderID = uint(id)
		}
	}

	if instanceID := c.Query("instanceId"); instanceID != "" {
		if id, err := strconv.ParseUint(instanceID, 10, 32); err == nil {
			req.InstanceID = uint(id)
		}
	}

	req.Protocol = c.Query("protocol")
	req.Status = c.Query("status")

	// 参数验证
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 || req.PageSize > 100 {
		req.PageSize = 10
	}

	portMappingService := resources.PortMappingService{}
	ports, total, err := portMappingService.GetPortMappingList(req)
	if err != nil {
		global.APP_LOG.Error("获取端口映射列表失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取端口映射列表失败"))
		return
	}

	// 批量预加载实例和Provider信息
	var instanceIDs, providerIDs []uint
	instanceIDSet := make(map[uint]bool)
	providerIDSet := make(map[uint]bool)
	instanceMap := make(map[uint]provider.Instance)
	providerMap := make(map[uint]provider.Provider)

	// 去重收集ID
	for _, port := range ports {
		if !instanceIDSet[port.InstanceID] {
			instanceIDs = append(instanceIDs, port.InstanceID)
			instanceIDSet[port.InstanceID] = true
		}
		if !providerIDSet[port.ProviderID] {
			providerIDs = append(providerIDs, port.ProviderID)
			providerIDSet[port.ProviderID] = true
		}
	}

	// 批量查询实例（只选择需要的字段）
	if len(instanceIDs) > 0 {
		var instances []provider.Instance
		if err := global.APP_DB.Select("id", "name").
			Where("id IN ?", instanceIDs).
			Limit(500).
			Find(&instances).Error; err == nil {
			for _, inst := range instances {
				instanceMap[inst.ID] = inst
			}
		}
	}

	// 批量查询Provider（只选择需要的字段）
	if len(providerIDs) > 0 {
		var providers []provider.Provider
		if err := global.APP_DB.Select("id", "name", "port_ip", "endpoint").
			Where("id IN ?", providerIDs).
			Limit(500).
			Find(&providers).Error; err == nil {
			for _, prov := range providers {
				providerMap[prov.ID] = prov
			}
		}
	}

	// 转换为前端期望的格式
	formattedPorts := make([]map[string]interface{}, len(ports))
	for i, port := range ports {
		// 从预加载的map中获取实例名称
		var instanceName string
		if instance, ok := instanceMap[port.InstanceID]; ok {
			instanceName = instance.Name
		}

		// 从预加载的map中获取Provider信息
		var providerName string
		var publicIP string
		if providerInfo, ok := providerMap[port.ProviderID]; ok {
			providerName = providerInfo.Name
			// 优先使用PortIP，如果为空则使用Endpoint
			ipSource := providerInfo.PortIP
			if ipSource == "" {
				ipSource = providerInfo.Endpoint
			}
			// 提取纯IP地址，移除端口号
			publicIP = extractIPFromEndpoint(ipSource)
		}

		formattedPorts[i] = map[string]interface{}{
			"id":           port.ID,
			"instanceId":   port.InstanceID,
			"instanceName": instanceName,
			"providerId":   port.ProviderID,
			"providerName": providerName,
			"hostPort":     port.HostPort,  // 统一使用 hostPort
			"guestPort":    port.GuestPort, // 统一使用 guestPort
			"publicIP":     publicIP,       // 仅IP地址，不含端口
			"protocol":     port.Protocol,
			"status":       port.Status,
			"description":  port.Description,
			"isSSH":        port.IsSSH,
			"isAutomatic":  port.IsAutomatic,
			"portType":     port.PortType, // 添加端口类型字段
			"isIPv6":       port.IPv6Enabled,
			"createdAt":    port.CreatedAt,
		}
	}

	common.ResponseSuccess(c, map[string]interface{}{
		"items": formattedPorts,
		"total": total,
	}, "获取成功")
}

// CreatePortMapping 创建端口映射
// @Summary 创建端口映射
// @Description 管理员创建新的端口映射（异步执行）
// @Tags 端口映射管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body admin.CreatePortMappingRequest true "创建端口映射请求参数"
// @Success 200 {object} common.Response{data=object} "创建成功，返回任务ID"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "创建失败"
// @Router /admin/port-mappings [post]
func CreatePortMapping(c *gin.Context) {
	var req admin.CreatePortMappingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	// 获取当前管理员用户ID（使用认证上下文）
	authCtx, exists := middleware.GetAuthContext(c)
	if !exists {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "未授权"))
		return
	}

	portMappingService := resources.PortMappingService{}
	portID, taskData, err := portMappingService.CreatePortMappingWithTask(req)
	if err != nil {
		global.APP_LOG.Error("创建端口映射失败", zap.Error(err))
		// 判断是否为端口范围验证错误
		if errors.Is(err, resources.ErrPortRangeValidation) {
			// 去掉错误类型前缀，只保留实际的错误消息
			errMsg := strings.TrimPrefix(err.Error(), "port range validation error: ")
			common.ResponseWithError(c, common.NewError(common.CodeValidationError, errMsg))
		} else {
			common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		}
		return
	}

	// 序列化任务数据
	taskDataJSON, err := json.Marshal(taskData)
	if err != nil {
		global.APP_LOG.Error("序列化任务数据失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "创建任务失败"))
		return
	}

	// 创建任务
	taskService := task.GetTaskService()
	newTask, err := taskService.CreateTask(
		authCtx.UserID,
		&taskData.ProviderID,
		&taskData.InstanceID,
		"create-port-mapping",
		string(taskDataJSON),
		600, // 10分钟超时
	)
	if err != nil {
		global.APP_LOG.Error("创建端口映射任务失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "创建任务失败"))
		return
	}

	// 启动任务
	if err := taskService.StartTask(newTask.ID); err != nil {
		global.APP_LOG.Error("启动端口映射任务失败", zap.Uint("task_id", newTask.ID), zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "启动任务失败"))
		return
	}

	common.ResponseSuccess(c, map[string]interface{}{
		"taskId": newTask.ID,
		"portId": portID,
	}, "端口映射任务已创建")
}

// DeletePortMapping 删除端口映射（仅支持删除手动添加的端口，通过异步任务执行）
// @Summary 删除端口映射
// @Description 管理员删除端口映射（仅支持删除手动添加的端口，区间映射的端口不能删除）
// @Tags 端口映射管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "端口映射ID"
// @Success 200 {object} common.Response "删除任务已创建"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "创建任务失败"
// @Router /admin/port-mappings/{id} [delete]
func DeletePortMapping(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "无效的端口映射ID"))
		return
	}

	// 获取当前管理员用户ID
	authCtx, exists := middleware.GetAuthContext(c)
	if !exists {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "未授权"))
		return
	}

	portMappingService := resources.PortMappingService{}
	taskData, err := portMappingService.DeletePortMappingWithTask(uint(id))
	if err != nil {
		global.APP_LOG.Error("创建端口删除任务数据失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	// 序列化任务数据
	taskDataJSON, err := json.Marshal(taskData)
	if err != nil {
		global.APP_LOG.Error("序列化任务数据失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "创建任务失败"))
		return
	}

	// 创建任务
	taskService := task.GetTaskService()
	newTask, err := taskService.CreateTask(
		authCtx.UserID,
		&taskData.ProviderID,
		&taskData.InstanceID,
		"delete-port-mapping",
		string(taskDataJSON),
		600, // 10分钟超时
	)
	if err != nil {
		global.APP_LOG.Error("创建端口删除任务失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "创建任务失败"))
		return
	}

	// 启动任务
	if err := taskService.StartTask(newTask.ID); err != nil {
		global.APP_LOG.Error("启动端口删除任务失败", zap.Uint("task_id", newTask.ID), zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "启动任务失败"))
		return
	}

	common.ResponseSuccess(c, map[string]interface{}{
		"taskId": newTask.ID,
		"portId": taskData.PortID,
	}, "端口删除任务已创建")
}

// BatchDeletePortMapping 批量删除端口映射（仅支持删除手动添加的端口，通过异步任务执行）
// @Summary 批量删除端口映射
// @Description 管理员批量删除端口映射（仅支持删除手动添加的端口，区间映射的端口不能删除）
// @Tags 端口映射管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body admin.BatchDeletePortMappingRequest true "批量删除端口映射请求参数"
// @Success 200 {object} common.Response "删除任务已创建"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "创建任务失败"
// @Router /admin/port-mappings/batch-delete [post]
func BatchDeletePortMapping(c *gin.Context) {
	var req admin.BatchDeletePortMappingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	// 获取当前管理员用户ID
	authCtx, exists := middleware.GetAuthContext(c)
	if !exists {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "未授权"))
		return
	}

	portMappingService := resources.PortMappingService{}
	taskDataList, err := portMappingService.BatchDeletePortMappingWithTask(req)
	if err != nil {
		global.APP_LOG.Error("创建批量端口删除任务数据失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	// 为每个端口创建一个删除任务
	taskService := task.GetTaskService()
	var taskIDs []uint
	var failedPorts []uint

	for _, taskData := range taskDataList {
		// 序列化任务数据
		taskDataJSON, err := json.Marshal(taskData)
		if err != nil {
			global.APP_LOG.Error("序列化任务数据失败",
				zap.Uint("portId", taskData.PortID),
				zap.Error(err))
			failedPorts = append(failedPorts, taskData.PortID)
			continue
		}

		// 创建任务
		newTask, err := taskService.CreateTask(
			authCtx.UserID,
			&taskData.ProviderID,
			&taskData.InstanceID,
			"delete-port-mapping",
			string(taskDataJSON),
			600, // 10分钟超时
		)
		if err != nil {
			global.APP_LOG.Error("创建端口删除任务失败",
				zap.Uint("portId", taskData.PortID),
				zap.Error(err))
			failedPorts = append(failedPorts, taskData.PortID)
			continue
		}

		// 启动任务
		if err := taskService.StartTask(newTask.ID); err != nil {
			global.APP_LOG.Error("启动端口删除任务失败",
				zap.Uint("taskId", newTask.ID),
				zap.Uint("portId", taskData.PortID),
				zap.Error(err))
			failedPorts = append(failedPorts, taskData.PortID)
			continue
		}

		taskIDs = append(taskIDs, newTask.ID)
	}

	if len(failedPorts) > 0 {
		common.ResponseSuccess(c, map[string]interface{}{
			"taskIds":     taskIDs,
			"failedPorts": failedPorts,
		}, fmt.Sprintf("已创建 %d 个删除任务，%d 个端口创建任务失败", len(taskIDs), len(failedPorts)))
	} else {
		common.ResponseSuccess(c, map[string]interface{}{
			"taskIds": taskIDs,
		}, fmt.Sprintf("已创建 %d 个端口删除任务", len(taskIDs)))
	}
}

// UpdateProviderPortConfig 更新Provider端口配置
// @Summary 更新Provider端口配置
// @Description 管理员更新Provider的端口映射配置
// @Tags 端口映射管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Provider ID"
// @Param request body admin.ProviderPortConfigRequest true "Provider端口配置请求参数"
// @Success 200 {object} common.Response "更新成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "更新失败"
// @Router /admin/provider/{id}/port-config [put]
func UpdateProviderPortConfig(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "无效的Provider ID"))
		return
	}

	var req admin.ProviderPortConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	portMappingService := resources.PortMappingService{}
	err = portMappingService.UpdateProviderPortConfig(uint(id), req)
	if err != nil {
		global.APP_LOG.Error("更新Provider端口配置失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	common.ResponseSuccess(c, nil, "更新Provider端口配置成功")
}

// GetProviderPortUsage 获取Provider端口使用情况
// @Summary 获取Provider端口使用情况
// @Description 管理员获取Provider的端口使用统计
// @Tags 端口映射管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Provider ID"
// @Success 200 {object} common.Response{data=object} "获取成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "获取失败"
// @Router /admin/provider/{id}/port-usage [get]
func GetProviderPortUsage(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "无效的Provider ID"))
		return
	}

	portMappingService := resources.PortMappingService{}
	usage, err := portMappingService.GetProviderPortUsage(uint(id))
	if err != nil {
		global.APP_LOG.Error("获取Provider端口使用情况失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	common.ResponseSuccess(c, usage, "获取Provider端口使用情况成功")
}

// GetInstancePortMappings 获取实例的端口映射
// @Summary 获取实例端口映射
// @Description 管理员获取指定实例的所有端口映射
// @Tags 端口映射管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "实例ID"
// @Success 200 {object} common.Response{data=object} "获取成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "获取失败"
// @Router /admin/instances/{id}/port-mappings [get]
func GetInstancePortMappings(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "无效的实例ID"))
		return
	}

	portMappingService := resources.PortMappingService{}
	ports, err := portMappingService.GetInstancePortMappings(uint(id))
	if err != nil {
		global.APP_LOG.Error("获取实例端口映射失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	common.ResponseSuccess(c, ports, "获取实例端口映射成功")
}

// extractIPFromEndpoint 从endpoint中提取纯IP地址（使用全局函数）
func extractIPFromEndpoint(endpoint string) string {
	return utils.ExtractIPFromEndpoint(endpoint)
}

// CheckPortAvailability 检查端口可用性
// @Summary 检查端口可用性
// @Description 检查指定Provider上的端口或端口段是否可用
// @Tags 端口映射管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body admin.CheckPortAvailabilityRequest true "检查端口可用性请求"
// @Success 200 {object} common.Response{data=admin.CheckPortAvailabilityResponse} "检查成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "检查失败"
// @Router /admin/ports/check [post]
func CheckPortAvailability(c *gin.Context) {
	var req admin.CheckPortAvailabilityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	// 默认端口数量为1
	if req.PortCount == 0 {
		req.PortCount = 1
	}

	portMappingService := resources.PortMappingService{}
	response, err := portMappingService.CheckPortAvailability(req)
	if err != nil {
		global.APP_LOG.Error("检查端口可用性失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	common.ResponseSuccess(c, response, "检查完成")
}

// SyncPortMappings godoc
// @Summary 同步端口映射
// @Description 创建一个后台任务，检测并清理孤立的端口映射（实例已删除但数据库记录仍存在）。为每个Provider创建独立的同步任务
// @Tags Admin-Port-Mapping
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param data body admin.SyncPortMappingsTaskRequest false "同步参数（可选指定Provider IDs）"
// @Success 200 {object} common.Response "同步任务已创建"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "未授权"
// @Failure 500 {object} common.Response "服务器错误"
// @Router /admin/port-mappings/sync [post]
func SyncPortMappings(c *gin.Context) {
	var req admin.SyncPortMappingsTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	// 获取当前用户ID
	userID, err := getUserIDFromContext(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "未授权"))
		return
	}

	// 创建同步任务（为每个Provider创建独立任务）
	taskService := task.GetTaskService()
	tasks, err := taskService.CreateSyncPortMappingsTask(userID, &req)
	if err != nil {
		global.APP_LOG.Error("创建端口映射同步任务失败",
			zap.Uint("userId", userID),
			zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	taskIDs := make([]uint, len(tasks))
	for i, t := range tasks {
		taskIDs[i] = t.ID
	}

	global.APP_LOG.Info("创建端口映射同步任务成功",
		zap.Uint("userId", userID),
		zap.Int("taskCount", len(tasks)),
		zap.Uints("taskIds", taskIDs))

	common.ResponseSuccess(c, map[string]interface{}{
		"tasks":     tasks,
		"taskCount": len(tasks),
	}, fmt.Sprintf("已创建 %d 个同步任务，正在后台执行", len(tasks)))
}
