package system

import (
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"oneclickvirt/service/storage"
	"os"
	"path/filepath"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/middleware"
	"oneclickvirt/model/common"
	userService "oneclickvirt/service/user"
	"oneclickvirt/utils"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// getMaxAvatarSize 获取头像最大大小限制（字节）
func getMaxAvatarSize() int64 {
	// 从全局配置读取，单位是MB，需要转换为字节
	maxSizeMB := global.APP_CONFIG.Upload.MaxAvatarSize
	if maxSizeMB <= 0 {
		// 如果配置无效，使用默认值 2MB
		maxSizeMB = 2
	}
	return maxSizeMB * 1024 * 1024
}

// 文件上传配置
var (
	// 允许的头像文件类型 - 仅支持 PNG 和 JPEG
	AllowedAvatarTypes = map[string]bool{
		"image/jpeg": true,
		"image/png":  true,
	}

	// 允许的头像文件扩展名 - 仅支持 .png 和 .jpeg/.jpg
	AllowedAvatarExts = map[string]bool{
		".jpg":  true,
		".jpeg": true,
		".png":  true,
	}

	// 获取存储服务实例并动态获取上传目录
	storageService = storage.GetStorageService()
	UploadDir      = storageService.GetUploadsPath()
	AvatarDir      = storageService.GetAvatarsPath()
)

func init() {
	// 确保上传目录存在
	os.MkdirAll(AvatarDir, 0755)
}

// 文件验证结果
type FileValidationResult struct {
	Valid bool
	Error string
	Size  int64
	Type  string
	Ext   string
}

// 验证文件安全性
func validateFile(file *multipart.FileHeader, allowedTypes map[string]bool, allowedExts map[string]bool, maxSize int64) FileValidationResult {
	result := FileValidationResult{
		Valid: true,
		Size:  file.Size,
	}

	// 验证文件大小
	if file.Size > maxSize {
		result.Valid = false
		result.Error = fmt.Sprintf("文件大小超过限制，最大允许 %d MB", maxSize/(1024*1024))
		return result
	}

	if file.Size == 0 {
		result.Valid = false
		result.Error = "文件大小为0"
		return result
	}

	// 获取文件扩展名
	ext := strings.ToLower(filepath.Ext(file.Filename))
	result.Ext = ext

	// 验证扩展名白名单
	if !allowedExts[ext] {
		result.Valid = false
		result.Error = "仅支持 PNG 和 JPEG 格式的图片文件"
		return result
	}

	// 验证MIME类型
	src, err := file.Open()
	if err != nil {
		result.Valid = false
		result.Error = "无法读取文件"
		return result
	}
	defer src.Close()

	// 读取文件头部分析MIME类型
	buffer := make([]byte, 512)
	n, err := src.Read(buffer)
	if err != nil && err != io.EOF {
		result.Valid = false
		result.Error = "文件读取失败"
		return result
	}

	mimeType := http.DetectContentType(buffer[:n])
	result.Type = mimeType

	// 验证MIME类型白名单
	if !allowedTypes[mimeType] {
		result.Valid = false
		result.Error = "仅支持 PNG 和 JPEG 格式的图片文件"
		return result
	}

	// 重置文件指针
	src.Seek(0, 0)

	return result
}

// 生成安全的文件名
func generateSafeFilename(originalName string) string {
	ext := filepath.Ext(originalName)
	// 使用UUID + 时间戳生成唯一文件名
	return fmt.Sprintf("%s_%d%s", uuid.New().String(), time.Now().Unix(), ext)
}

// UploadAvatar 上传用户头像
// @Summary 上传用户头像
// @Description 上传用户头像图片，仅支持PNG和JPEG格式，最大2MB
// @Tags 文件上传
// @Accept multipart/form-data
// @Produce json
// @Security BearerAuth
// @Param avatar formData file true "头像文件"
// @Success 200 {object} common.Response{data=object{url=string}} "上传成功"
// @Failure 400 {object} common.Response "请求参数错误"
// @Failure 401 {object} common.Response "用户未登录"
// @Failure 413 {object} common.Response "文件过大"
// @Failure 415 {object} common.Response "文件类型不支持"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /upload/avatar [post]
func UploadAvatar(c *gin.Context) {
	authCtx, exists := middleware.GetAuthContext(c)
	if !exists {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "用户未登录"))
		return
	}

	// 获取上传的文件
	file, err := c.FormFile("avatar")
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "请选择要上传的文件"))
		return
	}

	// 验证文件
	maxSize := getMaxAvatarSize()
	validation := validateFile(file, AllowedAvatarTypes, AllowedAvatarExts, maxSize)
	if !validation.Valid {
		code := common.CodeValidationError
		if strings.Contains(validation.Error, "大小超过限制") {
			code = common.CodeRequestTooLarge
		}
		common.ResponseWithError(c, common.NewError(code, validation.Error))
		return
	}

	// 安全扫描
	scanner := &utils.FileSecurityScanner{}
	if err := scanner.ScanFile(file); err != nil {
		if utils.IsSecurityError(err) {
			common.ResponseWithError(c, common.NewError(common.CodeForbidden, "文件安全检查失败: "+err.Error()))
		} else {
			common.ResponseWithError(c, common.NewError(common.CodeInternalError, "文件安全扫描失败"))
		}
		return
	}

	// 生成安全的文件名
	safeFilename := generateSafeFilename(file.Filename)

	// 创建用户头像目录
	userAvatarDir := filepath.Join(AvatarDir, fmt.Sprintf("user_%d", authCtx.UserID))
	if err := os.MkdirAll(userAvatarDir, 0755); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "创建上传目录失败"))
		return
	}

	// 保存文件
	filePath := filepath.Join(userAvatarDir, safeFilename)
	if err := c.SaveUploadedFile(file, filePath); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "文件保存失败"))
		return
	}

	// 生成访问URL
	avatarURL := fmt.Sprintf("/api/v1/static/avatars/user_%d/%s", authCtx.UserID, safeFilename)

	// 更新用户头像字段
	userServiceInstance := userService.NewService()
	if err := userServiceInstance.UpdateAvatar(authCtx.UserID, avatarURL); err != nil {
		// 如果数据库更新失败，删除已上传的文件
		os.Remove(filePath)
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "头像更新失败"))
		return
	}

	// 记录操作日志
	global.APP_LOG.Info("用户上传头像",
		zap.Uint("user_id", authCtx.UserID),
		zap.String("filename", safeFilename),
		zap.String("action", "upload_avatar"))

	common.ResponseSuccess(c, gin.H{
		"url":      avatarURL,
		"filename": safeFilename,
		"size":     validation.Size,
		"type":     validation.Type,
	}, "头像上传成功")
}

// ServeStaticFile 提供静态文件访问
// @Summary 获取静态文件
// @Description 获取上传的静态文件（如头像）
// @Tags 文件访问
// @Produce application/octet-stream
// @Param type path string true "文件类型" Enums(avatars)
// @Param path path string true "文件路径"
// @Success 200 {file} file "文件内容"
// @Failure 404 {object} common.Response "文件不存在"
// @Failure 403 {object} common.Response "访问被拒绝"
// @Router /static/{type}/{path} [get]
func ServeStaticFile(c *gin.Context) {
	fileType := c.Param("type")
	filePath := c.Param("path")

	// 验证文件类型
	if fileType != "avatars" {
		c.JSON(http.StatusForbidden, common.Response{
			Code: 403,
			Msg:  "访问被拒绝",
		})
		return
	}

	// 构建完整文件路径，防止路径遍历攻击
	safePath := filepath.Clean(filePath)
	if strings.Contains(safePath, "..") || strings.HasPrefix(safePath, "/") {
		c.JSON(http.StatusForbidden, common.Response{
			Code: 403,
			Msg:  "非法的文件路径",
		})
		return
	}

	fullPath := filepath.Join(AvatarDir, safePath)

	// 确保请求的文件在允许的目录内
	absUploadDir, _ := filepath.Abs(AvatarDir)
	absFilePath, _ := filepath.Abs(fullPath)
	if !strings.HasPrefix(absFilePath, absUploadDir) {
		c.JSON(http.StatusForbidden, common.Response{
			Code: 403,
			Msg:  "访问被拒绝",
		})
		return
	}

	// 检查文件是否存在
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, common.Response{
			Code: 404,
			Msg:  "文件不存在",
		})
		return
	}

	// 设置适当的Content-Type
	ext := strings.ToLower(filepath.Ext(fullPath))
	switch ext {
	case ".jpg", ".jpeg":
		c.Header("Content-Type", "image/jpeg")
	case ".png":
		c.Header("Content-Type", "image/png")
	case ".webp":
		c.Header("Content-Type", "image/webp")
	default:
		c.Header("Content-Type", "application/octet-stream")
	}

	// 设置缓存头
	c.Header("Cache-Control", "public, max-age=86400") // 1天缓存

	// 提供文件
	c.File(fullPath)
}
