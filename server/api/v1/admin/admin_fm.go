package admin

// admin_fm.go — Agent File Manager HTTP API 处理器。
// 仅适用于 connectionType='agent' 且 Agent 当前在线的 Provider。
// 通过 WebSocket 控制通道向 Agent 发送 FM 请求，无需 SSH/SFTP 凭据。

import (
	"fmt"
	"net/url"
	"oneclickvirt/global"
	"oneclickvirt/model/common"
	providerModel "oneclickvirt/model/provider"
	agentService "oneclickvirt/service/agent"
	"path"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func getAgentProviderForFM(c *gin.Context) (*providerModel.Provider, error) {
	providerID := c.Param("id")
	if providerID == "" {
		return nil, common.NewError(common.CodeValidationError, "Provider ID 不能为空")
	}
	var provider providerModel.Provider
	err := global.APP_DB.Select("id", "name", "connection_type").
		Where("id = ?", providerID).First(&provider).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, common.NewError(common.CodeNotFound, "Provider 不存在")
		}
		return nil, err
	}
	if provider.ConnectionType != "agent" {
		return nil, common.NewError(common.CodeValidationError, "仅 Agent 模式节点支持此接口")
	}
	return &provider, nil
}

// AdminProviderFMList godoc
// @Summary 管理员 Agent 节点文件列表
// @Description 列出 Agent 节点指定路径下的文件和目录（Agent 模式专用）
// @Tags 管理员/Provider
// @Produce json
// @Param id path uint true "Provider ID"
// @Param path query string false "远程路径"
// @Success 200 {object} common.Response
// @Router /admin/providers/{id}/fm/list [get]
func AdminProviderFMList(c *gin.Context) {
	provider, err := getAgentProviderForFM(c)
	if err != nil {
		common.ResponseWithError(c, err)
		return
	}

	remotePath := c.DefaultQuery("path", "/")
	if remotePath == "" {
		remotePath = "/"
	}

	hub := agentService.GetHub()
	actualPath, entries, err := hub.FMList(provider.ID, remotePath)
	if err != nil {
		global.APP_LOG.Warn("FM List 失败", zap.Uint("providerID", provider.ID), zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, fmt.Sprintf("获取文件列表失败: %v", err)))
		return
	}

	// 排序：目录优先，同类按名称字典序
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})

	// 补全 path 字段（前端显示使用）
	type fmEntryWithPath struct {
		agentService.FMEntry
		Path string `json:"path"`
	}
	result := make([]fmEntryWithPath, 0, len(entries))
	for _, e := range entries {
		result = append(result, fmEntryWithPath{
			FMEntry: e,
			Path:    path.Join(actualPath, e.Name),
		})
	}

	common.ResponseSuccess(c, gin.H{"path": actualPath, "entries": result})
}

// AdminProviderFMDownload godoc
// @Summary 管理员 Agent 节点文件下载
// @Description 通过 Agent 控制通道下载节点上的文件（Agent 模式专用）
// @Tags 管理员/Provider
// @Produce octet-stream
// @Param id path uint true "Provider ID"
// @Param path query string true "远程文件路径"
// @Success 200 {file} binary
// @Router /admin/providers/{id}/fm/download [get]
func AdminProviderFMDownload(c *gin.Context) {
	provider, err := getAgentProviderForFM(c)
	if err != nil {
		common.ResponseWithError(c, err)
		return
	}

	remotePath := c.Query("path")
	if remotePath == "" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "请指定文件路径"))
		return
	}

	hub := agentService.GetHub()
	data, err := hub.FMDownload(provider.ID, remotePath)
	if err != nil {
		global.APP_LOG.Warn("FM Download 失败", zap.Uint("providerID", provider.ID), zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, fmt.Sprintf("下载文件失败: %v", err)))
		return
	}

	filename := path.Base(remotePath)
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename*=UTF-8''%s", url.QueryEscape(filename)))
	c.Data(200, "application/octet-stream", data)
}

// AdminProviderFMUpload godoc
// @Summary 管理员 Agent 节点文件上传
// @Description 通过 Agent 控制通道上传文件到节点（Agent 模式专用，最大 50 MB）
// @Tags 管理员/Provider
// @Accept multipart/form-data
// @Produce json
// @Param id path uint true "Provider ID"
// @Param targetDir formData string false "目标目录"
// @Param file formData file true "上传文件"
// @Success 200 {object} common.Response
// @Router /admin/providers/{id}/fm/upload [post]
func AdminProviderFMUpload(c *gin.Context) {
	provider, err := getAgentProviderForFM(c)
	if err != nil {
		common.ResponseWithError(c, err)
		return
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "请选择要上传的文件"))
		return
	}

	targetDir := c.PostForm("targetDir")
	if targetDir == "" {
		targetDir = "/"
	}

	if fileHeader.Size > 50*1024*1024 {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "文件过大（最大 50 MB）"))
		return
	}

	f, err := fileHeader.Open()
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "读取上传文件失败"))
		return
	}
	defer f.Close()

	buf := make([]byte, fileHeader.Size)
	if _, err := f.Read(buf); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "读取文件内容失败"))
		return
	}

	remotePath := path.Join(targetDir, fileHeader.Filename)

	hub := agentService.GetHub()
	if err := hub.FMUpload(provider.ID, remotePath, buf); err != nil {
		global.APP_LOG.Warn("FM Upload 失败", zap.Uint("providerID", provider.ID), zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, fmt.Sprintf("上传文件失败: %v", err)))
		return
	}

	common.ResponseSuccess(c, gin.H{"message": "上传成功", "path": remotePath})
}

// AdminProviderFMDelete godoc
// @Summary 管理员 Agent 节点文件删除
// @Description 通过 Agent 控制通道删除节点上的文件或空目录（Agent 模式专用）
// @Tags 管理员/Provider
// @Produce json
// @Param id path uint true "Provider ID"
// @Param path query string true "远程文件路径"
// @Success 200 {object} common.Response
// @Router /admin/providers/{id}/fm/file [delete]
func AdminProviderFMDelete(c *gin.Context) {
	provider, err := getAgentProviderForFM(c)
	if err != nil {
		common.ResponseWithError(c, err)
		return
	}

	remotePath := c.Query("path")
	if remotePath == "" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "请指定文件路径"))
		return
	}

	hub := agentService.GetHub()
	if err := hub.FMDelete(provider.ID, remotePath); err != nil {
		global.APP_LOG.Warn("FM Delete 失败", zap.Uint("providerID", provider.ID), zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, fmt.Sprintf("删除失败: %v", err)))
		return
	}

	common.ResponseSuccess(c, gin.H{"message": "删除成功"})
}

// AdminProviderFMMkdir godoc
// @Summary 管理员 Agent 节点创建目录
// @Description 通过 Agent 控制通道在节点上创建目录（含父目录，Agent 模式专用）
// @Tags 管理员/Provider
// @Accept json
// @Produce json
// @Param id path uint true "Provider ID"
// @Param body body object true "路径"
// @Success 200 {object} common.Response
// @Router /admin/providers/{id}/fm/mkdir [post]
func AdminProviderFMMkdir(c *gin.Context) {
	provider, err := getAgentProviderForFM(c)
	if err != nil {
		common.ResponseWithError(c, err)
		return
	}

	var req struct {
		Path string `json:"path" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "请提供目录路径"))
		return
	}

	hub := agentService.GetHub()
	if err := hub.FMMkdir(provider.ID, req.Path); err != nil {
		global.APP_LOG.Warn("FM Mkdir 失败", zap.Uint("providerID", provider.ID), zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, fmt.Sprintf("创建目录失败: %v", err)))
		return
	}

	common.ResponseSuccess(c, gin.H{"message": "目录创建成功", "path": req.Path})
}
