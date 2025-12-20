package system

import (
	"oneclickvirt/service/log"
	"oneclickvirt/service/storage"
	"strconv"

	"oneclickvirt/model/common"

	"github.com/gin-gonic/gin"
)

// StorageApi 存储管理API
type StorageApi struct{}

// GetStorageInfo 获取存储信息
// @Tags System
// @Summary 获取存储目录信息
// @Description 获取存储目录结构和状态信息
// @Security BearerAuth
// @Accept json
// @Produce json
// @Success 200 {object} common.Response{data=map[string]interface{}} "success"
// @Router /api/v1/admin/storage/info [get]
func (s *StorageApi) GetStorageInfo(c *gin.Context) {
	storageService := storage.GetStorageService()
	info := storageService.GetStorageInfo()
	common.ResponseSuccess(c, info)
}

// InitializeStorage 重新初始化存储目录
// @Tags System
// @Summary 重新初始化存储目录
// @Description 重新创建所有必要的存储目录
// @Security BearerAuth
// @Accept json
// @Produce json
// @Success 200 {object} common.Response "success"
// @Router /api/v1/admin/storage/init [post]
func (s *StorageApi) InitializeStorage(c *gin.Context) {
	storageService := storage.GetStorageService()
	if err := storageService.InitializeStorage(); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "存储目录初始化失败: "+err.Error()))
		return
	}
	common.ResponseSuccess(c, "存储目录初始化成功")
}

// CleanupTempFiles 清理临时文件
// @Tags System
// @Summary 清理临时文件
// @Description 清理临时目录中的所有文件
// @Security BearerAuth
// @Accept json
// @Produce json
// @Success 200 {object} common.Response "success"
// @Router /api/v1/admin/storage/cleanup [post]
func (s *StorageApi) CleanupTempFiles(c *gin.Context) {
	storageService := storage.GetStorageService()
	if err := storageService.CleanupTempFiles(); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "临时文件清理失败: "+err.Error()))
		return
	}
	common.ResponseSuccess(c, "临时文件清理成功")
}

// GetLogFiles 获取日志文件列表
// @Tags System
// @Summary 获取日志文件列表
// @Description 获取所有日志文件的信息，包括按日期分割的日志文件
// @Security BearerAuth
// @Accept json
// @Produce json
// @Success 200 {object} common.Response{data=[]log.LogFileInfo} "success"
// @Router /api/v1/admin/logs/files [get]
func (s *StorageApi) GetLogFiles(c *gin.Context) {
	logRotationService := log.GetLogRotationService()
	files, err := logRotationService.GetLogFiles()
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "获取日志文件列表失败: "+err.Error()))
		return
	}
	common.ResponseSuccess(c, files)
}

// ReadLogFile 读取日志文件内容
// @Tags System
// @Summary 读取日志文件内容
// @Description 读取指定日志文件的内容，支持指定返回行数
// @Security BearerAuth
// @Accept json
// @Produce json
// @Param filename query string true "日志文件名"
// @Param lines query int false "返回行数，默认100行"
// @Success 200 {object} common.Response{data=[]string} "success"
// @Router /api/v1/admin/logs/read [get]
func (s *StorageApi) ReadLogFile(c *gin.Context) {
	filename := c.Query("filename")
	if filename == "" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "文件名不能为空"))
		return
	}

	linesStr := c.DefaultQuery("lines", "100")
	lines, err := strconv.Atoi(linesStr)
	if err != nil {
		lines = 100
	}

	logRotationService := log.GetLogRotationService()
	content, err := logRotationService.ReadLogFile(filename, lines)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "读取日志文件失败: "+err.Error()))
		return
	}

	common.ResponseSuccess(c, gin.H{
		"filename": filename,
		"lines":    len(content),
		"content":  content,
	})
}

// CleanupOldLogs 清理旧日志文件
// @Tags System
// @Summary 清理旧日志文件
// @Description 根据配置的保留天数清理旧的日志文件
// @Security BearerAuth
// @Accept json
// @Produce json
// @Success 200 {object} common.Response "success"
// @Router /api/v1/admin/logs/cleanup [post]
func (s *StorageApi) CleanupOldLogs(c *gin.Context) {
	logRotationService := log.GetLogRotationService()
	if err := logRotationService.CleanupOldLogs(); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "清理旧日志失败: "+err.Error()))
		return
	}
	common.ResponseSuccess(c, "旧日志清理成功")
}
