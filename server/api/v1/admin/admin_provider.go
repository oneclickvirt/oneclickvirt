package admin

import (
	"context"
	"fmt"
	"net/http"
	"oneclickvirt/service/provider"
	"oneclickvirt/utils"
	"strconv"
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/model/admin"
	"oneclickvirt/model/common"
	adminProvider "oneclickvirt/service/admin/provider"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// GetProviderList 获取提供商列表
// @Summary 获取提供商列表
// @Description 管理员获取系统中的虚拟化提供商列表，支持分页和查询
// @Tags 提供商管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param page query int false "页码" default(1)
// @Param pageSize query int false "每页数量" default(10)
// @Param keyword query string false "搜索关键字"
// @Param type query string false "提供商类型筛选"
// @Success 200 {object} common.Response{data=object} "获取成功"
// @Failure 400 {object} common.Response "请求参数错误"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /admin/provider [get]
func GetProviderList(c *gin.Context) {
	var req admin.ProviderListRequest
	// 设置默认值
	req.Page = 1
	req.PageSize = 10

	// 如果绑定失败，只记录错误但不返回400，使用默认值继续执行
	if err := c.ShouldBindQuery(&req); err != nil {
		global.APP_LOG.Warn("Provider列表查询参数绑定失败，使用默认值", zap.Error(err))
	}

	// 确保页码和页大小的合理性
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 || req.PageSize > 100 {
		req.PageSize = 10
	}

	providerService := adminProvider.NewService()
	providers, total, err := providerService.GetProviderList(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  "获取提供商列表失败",
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "获取成功",
		Data: map[string]interface{}{
			"list":  providers,
			"total": total,
		},
	})
}

// CreateProvider 创建提供商
// @Summary 创建提供商
// @Description 管理员创建新的虚拟化提供商配置
// @Tags 提供商管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body admin.CreateProviderRequest true "创建提供商请求参数"
// @Success 200 {object} common.Response "创建成功"
// @Failure 400 {object} common.Response "请求参数错误"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /admin/provider [post]
func CreateProvider(c *gin.Context) {
	var req admin.CreateProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		global.APP_LOG.Error("CreateProvider参数绑定失败", zap.Error(err))
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "参数错误: " + err.Error(),
		})
		return
	}

	providerService := adminProvider.NewService()
	err := providerService.CreateProvider(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "创建提供商成功",
	})
}

func UpdateProvider(c *gin.Context) {
	// 从URL路径参数获取ID
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "无效的Provider ID",
		})
		return
	}

	var req admin.UpdateProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		global.APP_LOG.Error("UpdateProvider参数绑定失败", zap.Error(err))
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "参数错误: " + err.Error(),
		})
		return
	}

	// 设置ID从URL参数
	req.ID = uint(id)

	providerService := adminProvider.NewService()
	if err := providerService.UpdateProvider(req); err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "更新提供商成功",
	})
}

func DeleteProvider(c *gin.Context) {
	providerIDStr := c.Param("id")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "无效的提供商ID",
		})
		return
	}

	// 获取force参数，用于强制删除离线节点
	forceDelete := c.Query("force") == "true"

	providerService := adminProvider.NewService()
	err = providerService.DeleteProvider(uint(providerID), forceDelete)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "删除提供商成功",
	})
}

func FreezeProvider(c *gin.Context) {
	var req admin.FreezeProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "参数错误",
		})
		return
	}

	providerService := adminProvider.NewService()
	err := providerService.FreezeProvider(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "提供商已冻结",
	})
}

func UnfreezeProvider(c *gin.Context) {
	var req admin.UnfreezeProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "参数错误",
		})
		return
	}

	providerService := adminProvider.NewService()
	err := providerService.UnfreezeProvider(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "提供商已解冻",
	})
}

// GenerateProviderCert 为Provider生成证书或配置
// @Summary 为Provider生成证书或配置
// @Description 为LXD/Incus Provider生成客户端证书和设置脚本，为Proxmox VE生成API Token配置脚本
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Provider ID"
// @Success 200 {object} common.Response{data=object} "生成成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "生成失败"
// @Router /admin/provider/{id}/generate-cert [post]
func GenerateProviderCert(c *gin.Context) {
	providerIDStr := c.Param("id")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "无效的Provider ID",
		})
		return
	}

	providerService := adminProvider.NewService()
	setupCommand, err := providerService.GenerateProviderCert(uint(providerID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  "生成证书失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "证书生成成功",
		Data: gin.H{
			"setupCommand": setupCommand,
		},
	})
}

// AutoConfigureProviderStream 实时自动配置Provider
// @Summary 实时自动配置Provider
// @Description 使用Server-Sent Events实时显示配置过程和输出
// @Tags 管理员管理
// @Accept json
// @Produce text/plain
// @Security BearerAuth
// @Param id path int true "Provider ID"
// @Success 200 {string} string "实时配置输出"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "配置失败"
// @Router /admin/provider/{id}/auto-configure-stream [post]
func AutoConfigureProviderStream(c *gin.Context) {
	providerIDStr := c.Param("id")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "无效的Provider ID",
		})
		return
	}

	// 设置SSE头部
	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")

	// 创建输出通道
	outputChan := make(chan string, 100)
	errorChan := make(chan error, 1)

	// 创建可取消的context，关联到客户端请求
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel() // 确保函数退出时取消后台任务

	// 启动配置过程
	go func() {
		defer close(outputChan)
		defer close(errorChan)

		providerService := adminProvider.NewService()
		// 传递context以便客户端断开时能取消操作
		err := providerService.AutoConfigureProviderWithStreamContext(ctx, uint(providerID), outputChan)
		if err != nil {
			select {
			case errorChan <- err:
			case <-ctx.Done():
				// Context已取消，不发送错误
			}
		}
	}()

	// 实时输出
	c.Writer.Header().Set("Transfer-Encoding", "chunked")
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  "服务器不支持实时输出",
		})
		return
	}

	for {
		select {
		case output, ok := <-outputChan:
			if !ok {
				// 通道已关闭，配置完成
				c.Writer.WriteString("\n\n=== 配置完成 ===\n")
				flusher.Flush()
				return
			}
			c.Writer.WriteString(output + "\n")
			flusher.Flush()

		case err := <-errorChan:
			if err != nil {
				c.Writer.WriteString(fmt.Sprintf("\n\n=== 错误: %s ===\n", err.Error()))
				flusher.Flush()
				return
			}

		case <-ctx.Done():
			// 客户端断开连接或超时
			c.Writer.WriteString("\n\n=== 连接已断开 ===\n")
			flusher.Flush()
			return
		}
	}
}

// CheckProviderHealth 检查Provider健康状态
// @Summary 检查Provider健康状态
// @Description 检查Provider的API和SSH连接状态
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Provider ID"
// @Param forceRefresh query bool false "是否强制刷新资源信息" default(true)
// @Success 200 {object} common.Response{data=admin.ProviderStatusResponse} "检查成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "检查失败"
// @Router /admin/provider/{id}/health-check [post]
func CheckProviderHealth(c *gin.Context) {
	providerIDStr := c.Param("id")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "无效的Provider ID",
		})
		return
	}

	// 获取forceRefresh参数，默认为true（手动触发时强制刷新）
	forceRefresh := c.DefaultQuery("forceRefresh", "true") == "true"

	providerService := adminProvider.NewService()
	err = providerService.CheckProviderHealthWithOptions(uint(providerID), forceRefresh)
	if err != nil {
		// 错误消息
		errorMsg := "健康检查失败"
		if strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "i/o timeout") {
			errorMsg = "健康检查超时，请检查网络连接或服务器状态"
		} else if strings.Contains(err.Error(), "connection refused") {
			errorMsg = "无法连接到服务器，请检查服务器状态和网络配置"
		} else if strings.Contains(err.Error(), "handshake failed") {
			errorMsg = "SSH握手失败，请检查认证信息和服务器配置"
		} else {
			errorMsg = "健康检查失败: " + err.Error()
		}

		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  errorMsg,
		})
		return
	}

	// 健康检查完成后，获取最新状态并返回
	status, err := providerService.GetProviderStatus(uint(providerID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  "获取状态失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "健康检查完成",
		Data: status,
	})
}

// GetProviderStatus 获取Provider状态详情
// @Summary 获取Provider状态详情
// @Description 获取Provider的详细状态信息，包括证书信息
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param id path int true "Provider ID"
// @Success 200 {object} common.Response{data=admin.ProviderStatusResponse} "获取成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 500 {object} common.Response "获取失败"
// @Router /admin/provider/{id}/status [get]
func GetProviderStatus(c *gin.Context) {
	providerIDStr := c.Param("id")
	providerID, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "无效的Provider ID",
		})
		return
	}

	providerService := adminProvider.NewService()
	status, err := providerService.GetProviderStatus(uint(providerID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  "获取状态失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "获取状态成功",
		Data: status,
	})
}

// ExportProviderConfigs 导出所有Provider配置
// @Summary 导出所有Provider配置
// @Description 导出所有已配置的Provider认证信息到文件
// @Tags 管理员管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response "导出成功"
// @Failure 500 {object} common.Response "导出失败"
// @Router /admin/provider/export-configs [post]
func ExportProviderConfigs(c *gin.Context) {
	configService := &provider.ProviderConfigService{}

	// 导出到 exports 目录
	exportDir := "exports"
	err := configService.ExportAllConfigs(exportDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  "导出配置失败: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "配置导出成功，文件保存在 " + exportDir + " 目录",
		Data: gin.H{
			"exportDir": exportDir,
		},
	})
}

// TestSSHConnection 测试SSH连接延迟
// @Summary 测试SSH连接延迟
// @Description 测试SSH连接延迟，执行多次测试并返回最小、最大、平均延迟及推荐超时时间
// @Tags 提供商管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body admin.TestSSHConnectionRequest true "测试SSH连接请求参数"
// @Success 200 {object} common.Response{data=admin.TestSSHConnectionResponse} "测试成功"
// @Failure 400 {object} common.Response "请求参数错误"
// @Failure 500 {object} common.Response "测试失败"
// @Router /admin/providers/test-ssh-connection [post]
func TestSSHConnection(c *gin.Context) {
	var req admin.TestSSHConnectionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "参数错误: " + err.Error(),
		})
		return
	}

	// 设置默认测试次数
	if req.TestCount <= 0 {
		req.TestCount = 3
	}
	if req.TestCount > 10 {
		req.TestCount = 10 // 最多测试10次
	}

	global.APP_LOG.Info("开始测试SSH连接",
		zap.String("host", req.Host),
		zap.Int("port", req.Port),
		zap.String("username", req.Username),
		zap.Int("testCount", req.TestCount))

	// 验证认证方式：必须提供密码或SSH密钥其中一种
	if req.Password == "" && req.SSHKey == "" {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "必须提供SSH密码或SSH密钥其中一种认证方式",
		})
		return
	}

	// 导入 utils 包
	sshConfig := utils.SSHConfig{
		Host:       req.Host,
		Port:       req.Port,
		Username:   req.Username,
		Password:   req.Password,
		PrivateKey: req.SSHKey,
	}

	// 执行测试
	minLatency, maxLatency, avgLatency, err := utils.TestSSHConnectionLatency(sshConfig, req.TestCount)
	if err != nil {
		global.APP_LOG.Error("SSH连接测试失败",
			zap.String("host", req.Host),
			zap.Int("port", req.Port),
			zap.Error(err))

		c.JSON(http.StatusOK, common.Response{
			Code: 500,
			Msg:  "SSH连接测试失败",
			Data: admin.TestSSHConnectionResponse{
				Success:      false,
				ErrorMessage: err.Error(),
				TestCount:    req.TestCount,
			},
		})
		return
	}

	// 计算推荐超时时间：最大延迟 * 2（向上取整到秒）
	recommendedTimeout := int((maxLatency * 2).Seconds())
	if recommendedTimeout < 10 {
		recommendedTimeout = 10 // 最小10秒
	}

	response := admin.TestSSHConnectionResponse{
		Success:            true,
		MinLatency:         minLatency.Milliseconds(),
		MaxLatency:         maxLatency.Milliseconds(),
		AvgLatency:         avgLatency.Milliseconds(),
		RecommendedTimeout: recommendedTimeout,
		TestCount:          req.TestCount,
	}

	global.APP_LOG.Info("SSH连接测试成功",
		zap.String("host", req.Host),
		zap.Int("port", req.Port),
		zap.Int64("minLatency", response.MinLatency),
		zap.Int64("maxLatency", response.MaxLatency),
		zap.Int64("avgLatency", response.AvgLatency),
		zap.Int("recommendedTimeout", response.RecommendedTimeout))

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "SSH连接测试成功",
		Data: response,
	})
}

// CheckProviderName 检查Provider名称是否已存在
// @Summary 检查Provider名称是否已存在
// @Description 检查指定的Provider名称是否已被使用（用于前端实时验证）
// @Tags 提供商管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param name query string true "要检查的Provider名称"
// @Param excludeId query int false "排除的Provider ID（编辑时使用）"
// @Success 200 {object} common.Response{data=map[string]bool} "检查结果"
// @Failure 400 {object} common.Response "请求参数错误"
// @Router /admin/providers/check-name [get]
func CheckProviderName(c *gin.Context) {
	name := c.Query("name")
	if name == "" {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "名称参数不能为空",
		})
		return
	}

	excludeIdStr := c.Query("excludeId")
	var excludeId *uint
	if excludeIdStr != "" {
		id, err := strconv.ParseUint(excludeIdStr, 10, 32)
		if err == nil {
			uid := uint(id)
			excludeId = &uid
		}
	}

	providerService := adminProvider.NewService()
	exists, err := providerService.CheckProviderNameExists(name, excludeId)
	if err != nil {
		global.APP_LOG.Error("检查Provider名称失败", zap.Error(err))
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  "检查失败",
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "检查成功",
		Data: map[string]bool{
			"exists": exists,
		},
	})
}

// CheckProviderEndpoint 检查Provider SSH地址和端口是否已存在
// @Summary 检查Provider SSH地址和端口是否已存在
// @Description 检查指定的SSH地址和端口组合是否已被使用（用于前端实时验证）
// @Tags 提供商管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param endpoint query string true "SSH地址"
// @Param sshPort query int true "SSH端口"
// @Param excludeId query int false "排除的Provider ID（编辑时使用）"
// @Success 200 {object} common.Response{data=map[string]bool} "检查结果"
// @Failure 400 {object} common.Response "请求参数错误"
// @Router /admin/providers/check-endpoint [get]
func CheckProviderEndpoint(c *gin.Context) {
	endpoint := c.Query("endpoint")
	if endpoint == "" {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "endpoint参数不能为空",
		})
		return
	}

	sshPortStr := c.Query("sshPort")
	if sshPortStr == "" {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "sshPort参数不能为空",
		})
		return
	}

	sshPort, err := strconv.Atoi(sshPortStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "sshPort参数格式错误",
		})
		return
	}

	excludeIdStr := c.Query("excludeId")
	var excludeId *uint
	if excludeIdStr != "" {
		id, err := strconv.ParseUint(excludeIdStr, 10, 32)
		if err == nil {
			uid := uint(id)
			excludeId = &uid
		}
	}

	providerService := adminProvider.NewService()
	exists, err := providerService.CheckProviderEndpointExists(endpoint, sshPort, excludeId)
	if err != nil {
		global.APP_LOG.Error("检查Provider SSH地址失败", zap.Error(err))
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  "检查失败",
		})
		return
	}

	c.JSON(http.StatusOK, common.Response{
		Code: 200,
		Msg:  "检查成功",
		Data: map[string]bool{
			"exists": exists,
		},
	})
}
