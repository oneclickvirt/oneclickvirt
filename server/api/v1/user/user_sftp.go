package user

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"oneclickvirt/global"
	"oneclickvirt/model/common"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/service/remote"
	"oneclickvirt/service/taskgate"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type sftpEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	Mode    string `json:"mode"`
	ModTime int64  `json:"modTime"`
	IsDir   bool   `json:"isDir"`
}

func getUserInstanceForSFTP(c *gin.Context) (*providerModel.Instance, error) {
	userIDInterface, exists := c.Get("user_id")
	if !exists {
		return nil, common.NewError(common.CodeUnauthorized, "未授权")
	}
	userID := userIDInterface.(uint)

	instanceID := c.Param("id")
	if instanceID == "" {
		return nil, common.NewError(common.CodeValidationError, "实例ID不能为空")
	}

	var instance providerModel.Instance
	err := global.APP_DB.Select("id", "name", "provider_id", "status", "private_ip", "public_ip", "ssh_port", "username", "password", "is_frozen", "frozen_reason", "expires_at").
		Where("id = ? AND user_id = ?", instanceID, userID).
		First(&instance).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, common.NewError(common.CodeNotFound, "实例不存在")
		}
		return nil, err
	}

	if instance.IsFrozen {
		return nil, common.NewError(common.CodeForbidden, "实例已被冻结，无法建立SFTP连接")
	}
	if instance.ExpiresAt != nil && instance.ExpiresAt.Before(time.Now()) {
		return nil, common.NewError(common.CodeForbidden, "实例已到期，无法建立SFTP连接")
	}

	if instance.Status != "running" {
		return nil, common.NewError(common.CodeValidationError, "实例未运行，无法建立SFTP连接")
	}

	return &instance, nil
}

// UserSFTPList godoc
// @Summary 用户实例SFTP目录列表
// @Description 列出用户实例远程目录文件
// @Tags 用户/实例
// @Produce json
// @Param id path uint true "实例ID"
// @Param path query string false "远程路径"
// @Success 200 {object} common.Response
// @Router /user/instances/{id}/sftp/list [get]
func UserSFTPList(c *gin.Context) {
	instance, err := getUserInstanceForSFTP(c)
	if err != nil {
		common.ResponseWithError(c, err)
		return
	}

	target, err := remote.ResolveInstanceSSHTarget(instance)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}

	sftpClient, cleanup, err := remote.OpenSFTPClient(target)
	if err != nil {
		global.APP_LOG.Warn("用户实例SFTP连接失败", zap.Uint("instanceID", instance.ID), zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "SFTP连接失败"))
		return
	}
	defer cleanup()

	remotePath := remote.NormalizeRemotePath(c.DefaultQuery("path", "/"))
	entries, err := sftpClient.ReadDir(remotePath)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, fmt.Sprintf("读取目录失败: %v", err)))
		return
	}

	result := make([]sftpEntry, 0, len(entries))
	for _, entry := range entries {
		result = append(result, sftpEntry{
			Name:    entry.Name(),
			Path:    path.Join(remotePath, entry.Name()),
			Size:    entry.Size(),
			Mode:    entry.Mode().String(),
			ModTime: entry.ModTime().Unix(),
			IsDir:   entry.IsDir(),
		})
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].IsDir != result[j].IsDir {
			return result[i].IsDir
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})

	common.ResponseSuccess(c, gin.H{"path": remotePath, "entries": result})
}

// UserSFTPDownload godoc
// @Summary 用户实例SFTP下载
// @Description 下载用户实例上的远程文件
// @Tags 用户/实例
// @Produce octet-stream
// @Param id path uint true "实例ID"
// @Param path query string true "远程文件路径"
// @Success 200 {file} binary
// @Router /user/instances/{id}/sftp/download [get]
func UserSFTPDownload(c *gin.Context) {
	if err := taskgate.EnsureAccepting(); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	instance, err := getUserInstanceForSFTP(c)
	if err != nil {
		common.ResponseWithError(c, err)
		return
	}

	remoteFilePath := remote.NormalizeRemotePath(c.Query("path"))
	if remoteFilePath == "/" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "请指定文件路径"))
		return
	}

	target, err := remote.ResolveInstanceSSHTarget(instance)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}

	sftpClient, cleanup, err := remote.OpenSFTPClient(target)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "SFTP连接失败"))
		return
	}
	defer cleanup()

	file, err := sftpClient.Open(remoteFilePath)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, fmt.Sprintf("打开文件失败: %v", err)))
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, fmt.Sprintf("读取文件信息失败: %v", err)))
		return
	}
	if info.IsDir() {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "目录不支持下载"))
		return
	}

	filename := path.Base(remoteFilePath)
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename*=UTF-8''%s", url.QueryEscape(filename)))
	c.Header("Content-Length", strconv.FormatInt(info.Size(), 10))
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, file)
}

// UserSFTPUpload godoc
// @Summary 用户实例SFTP上传
// @Description 上传本地文件到用户实例远程目录
// @Tags 用户/实例
// @Accept multipart/form-data
// @Produce json
// @Param id path uint true "实例ID"
// @Param targetDir formData string false "远程目标目录"
// @Param file formData file true "上传文件"
// @Success 200 {object} common.Response
// @Router /user/instances/{id}/sftp/upload [post]
func UserSFTPUpload(c *gin.Context) {
	if err := taskgate.EnsureAccepting(); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	instance, err := getUserInstanceForSFTP(c)
	if err != nil {
		common.ResponseWithError(c, err)
		return
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "请选择上传文件"))
		return
	}

	targetDir := remote.NormalizeRemotePath(c.DefaultPostForm("targetDir", "/"))
	filename := path.Base(c.DefaultPostForm("filename", fileHeader.Filename))
	if filename == "" || filename == "." || filename == "/" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的文件名"))
		return
	}
	remotePath := resolveUploadTargetPath(c.PostForm("targetPath"), targetDir, filename)
	remoteDir := remote.NormalizeRemotePath(path.Dir(remotePath))

	uploadID := strings.TrimSpace(c.PostForm("uploadId"))
	if uploadID != "" {
		normalizedUploadID, idErr := remote.NormalizeChunkUploadID(uploadID)
		if idErr != nil {
			common.ResponseWithError(c, common.NewError(common.CodeValidationError, idErr.Error()))
			return
		}
		uploadID = normalizedUploadID
	}
	chunkIndex, parseErr := parseUploadInt64(c, "chunkIndex", 0)
	if parseErr != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, parseErr.Error()))
		return
	}
	totalChunks, parseErr := parseUploadInt64(c, "totalChunks", 1)
	if parseErr != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, parseErr.Error()))
		return
	}
	offset, parseErr := parseUploadInt64(c, "offset", 0)
	if parseErr != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, parseErr.Error()))
		return
	}
	isLastChunk, parseErr := parseUploadBool(c, "isLastChunk", true)
	if parseErr != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, parseErr.Error()))
		return
	}

	target, err := remote.ResolveInstanceSSHTarget(instance)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}
	remote.RegisterSFTPChunkCleanupTarget(target, remoteDir)

	sftpClient, cleanup, err := remote.OpenSFTPClient(target)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "SFTP连接失败"))
		return
	}
	defer cleanup()

	if mkErr := sftpClient.MkdirAll(remoteDir); mkErr != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, fmt.Sprintf("创建远程目录失败: %v", mkErr)))
		return
	}
	_, _ = remote.CleanupExpiredChunkParts(sftpClient, remoteDir, remote.DefaultChunkPartTTL)

	src, err := fileHeader.Open()
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, fmt.Sprintf("读取上传文件失败: %v", err)))
		return
	}
	defer src.Close()

	if uploadID != "" {
		if totalChunks <= 0 {
			totalChunks = 1
		}
		written, committed, wErr := remote.WriteSFTPChunk(sftpClient, remote.ChunkUploadMeta{
			RemotePath:  remotePath,
			UploadID:    uploadID,
			ChunkIndex:  chunkIndex,
			TotalChunks: totalChunks,
			Offset:      offset,
			IsLastChunk: isLastChunk,
		}, src)
		if wErr != nil {
			common.ResponseWithError(c, common.NewError(common.CodeInternalError, fmt.Sprintf("上传分片失败: %v", wErr)))
			return
		}
		common.ResponseSuccess(c, gin.H{
			"uploadId":   uploadID,
			"chunkIndex": chunkIndex,
			"written":    written,
			"completed":  committed,
			"path":       remotePath,
		})
		return
	}

	dst, err := sftpClient.Create(remotePath)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, fmt.Sprintf("创建远程文件失败: %v", err)))
		return
	}
	defer dst.Close()

	if _, err = io.Copy(dst, src); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, fmt.Sprintf("上传文件失败: %v", err)))
		return
	}

	common.ResponseSuccess(c, gin.H{"path": remotePath, "size": fileHeader.Size})
}

// UserSFTPUploadStatus godoc
// @Summary 用户实例SFTP上传状态
// @Description 查询分片上传进度，用于断点续传
// @Tags 用户/实例
// @Produce json
// @Param id path uint true "实例ID"
// @Param uploadId query string true "上传ID"
// @Param targetPath query string false "远程目标文件路径"
// @Param targetDir query string false "远程目标目录"
// @Param filename query string false "文件名"
// @Success 200 {object} common.Response
// @Router /user/instances/{id}/sftp/upload/status [get]
func UserSFTPUploadStatus(c *gin.Context) {
	instance, err := getUserInstanceForSFTP(c)
	if err != nil {
		common.ResponseWithError(c, err)
		return
	}

	uploadID := strings.TrimSpace(c.Query("uploadId"))
	if uploadID == "" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "uploadId不能为空"))
		return
	}
	normalizedUploadID, idErr := remote.NormalizeChunkUploadID(uploadID)
	if idErr != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, idErr.Error()))
		return
	}
	uploadID = normalizedUploadID

	targetDir := remote.NormalizeRemotePath(c.DefaultQuery("targetDir", "/"))
	filename := path.Base(c.DefaultQuery("filename", "unnamed.bin"))
	if filename == "" || filename == "." || filename == "/" {
		filename = "unnamed.bin"
	}
	remotePath := resolveUploadTargetPath(c.Query("targetPath"), targetDir, filename)

	target, err := remote.ResolveInstanceSSHTarget(instance)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}
	remote.RegisterSFTPChunkCleanupTarget(target, path.Dir(remotePath))

	sftpClient, cleanup, err := remote.OpenSFTPClient(target)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "SFTP连接失败"))
		return
	}
	defer cleanup()

	status, err := remote.QueryChunkUploadStatus(sftpClient, remotePath, uploadID)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, fmt.Sprintf("查询上传状态失败: %v", err)))
		return
	}
	cleanedParts, _ := remote.CleanupExpiredChunkParts(sftpClient, path.Dir(remotePath), remote.DefaultChunkPartTTL)

	common.ResponseSuccess(c, gin.H{
		"uploadId":      uploadID,
		"path":          remotePath,
		"uploadedBytes": status.UploadedBytes,
		"completed":     status.Completed,
		"cleanedParts":  cleanedParts,
	})
}

// UserSFTPUploadAbort godoc
// @Summary 用户实例SFTP上传中断清理
// @Description 清理指定 uploadId 对应的临时分片文件，允许重传
// @Tags 用户/实例
// @Accept multipart/form-data
// @Produce json
// @Param id path uint true "实例ID"
// @Param uploadId formData string true "上传ID"
// @Param targetPath formData string false "远程目标文件路径"
// @Param targetDir formData string false "远程目标目录"
// @Param filename formData string false "文件名"
// @Success 200 {object} common.Response
// @Router /user/instances/{id}/sftp/upload/abort [post]
func UserSFTPUploadAbort(c *gin.Context) {
	if err := taskgate.EnsureAccepting(); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	instance, err := getUserInstanceForSFTP(c)
	if err != nil {
		common.ResponseWithError(c, err)
		return
	}

	uploadID := strings.TrimSpace(c.PostForm("uploadId"))
	if uploadID == "" {
		uploadID = strings.TrimSpace(c.Query("uploadId"))
	}
	if uploadID == "" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "uploadId不能为空"))
		return
	}
	normalizedUploadID, idErr := remote.NormalizeChunkUploadID(uploadID)
	if idErr != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, idErr.Error()))
		return
	}
	uploadID = normalizedUploadID

	targetDir := remote.NormalizeRemotePath(c.DefaultPostForm("targetDir", c.DefaultQuery("targetDir", "/")))
	filename := path.Base(c.DefaultPostForm("filename", c.DefaultQuery("filename", "unnamed.bin")))
	if filename == "" || filename == "." || filename == "/" {
		filename = "unnamed.bin"
	}
	rawTargetPath := c.PostForm("targetPath")
	if strings.TrimSpace(rawTargetPath) == "" {
		rawTargetPath = c.Query("targetPath")
	}
	remotePath := resolveUploadTargetPath(rawTargetPath, targetDir, filename)

	target, err := remote.ResolveInstanceSSHTarget(instance)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}

	sftpClient, cleanup, err := remote.OpenSFTPClient(target)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "SFTP连接失败"))
		return
	}
	defer cleanup()

	removed, err := remote.AbortChunkUpload(sftpClient, remotePath, uploadID)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, fmt.Sprintf("清理上传分片失败: %v", err)))
		return
	}

	common.ResponseSuccess(c, gin.H{
		"uploadId": uploadID,
		"path":     remotePath,
		"removed":  removed,
	})
}

func resolveUploadTargetPath(rawTargetPath, targetDir, filename string) string {
	remotePath := remote.NormalizeRemotePath(rawTargetPath)
	if strings.TrimSpace(remotePath) == "/" || strings.TrimSpace(rawTargetPath) == "" {
		return path.Join(targetDir, filename)
	}
	return remotePath
}

func parseUploadInt64(c *gin.Context, key string, def int64) (int64, error) {
	raw := strings.TrimSpace(c.PostForm(key))
	if raw == "" {
		return def, nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("参数 %s 无效", key)
	}
	return v, nil
}

func parseUploadBool(c *gin.Context, key string, def bool) (bool, error) {
	raw := strings.TrimSpace(c.PostForm(key))
	if raw == "" {
		return def, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("参数 %s 无效", key)
	}
	return v, nil
}
