package admin

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"oneclickvirt/global"
	"oneclickvirt/middleware"
	"oneclickvirt/model/common"
	providerModel "oneclickvirt/model/provider"
	adminProvider "oneclickvirt/service/admin/provider"
	"oneclickvirt/service/remote"
	"oneclickvirt/service/taskgate"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type adminSFTPEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	Mode    string `json:"mode"`
	ModTime int64  `json:"modTime"`
	IsDir   bool   `json:"isDir"`
}

func getAdminInstanceForSFTP(c *gin.Context) (*providerModel.Instance, error) {
	instanceID := c.Param("id")
	if instanceID == "" {
		return nil, common.NewError(common.CodeValidationError, "实例ID不能为空")
	}

	var instance providerModel.Instance
	err := global.APP_DB.Select("id", "name", "provider_id", "status", "private_ip", "public_ip", "ssh_port", "username", "password").
		Where("id = ?", instanceID).
		First(&instance).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, common.NewError(common.CodeNotFound, "实例不存在")
		}
		return nil, err
	}
	if instance.Status != "running" {
		return nil, common.NewError(common.CodeValidationError, "实例未运行，无法建立SFTP连接")
	}
	ownerAdminID := middleware.GetOwnerAdminID(c)
	if ownerAdminID > 0 {
		if err := adminProvider.CheckProviderOwnership(instance.ProviderID, ownerAdminID); err != nil {
			return nil, common.NewError(common.CodeForbidden, err.Error())
		}
	}
	return &instance, nil
}

func getAdminProviderForSFTP(c *gin.Context) (*providerModel.Provider, error) {
	providerID := c.Param("id")
	if providerID == "" {
		return nil, common.NewError(common.CodeValidationError, "Provider ID不能为空")
	}

	var provider providerModel.Provider
	err := global.APP_DB.Select("id", "name", "connection_type", "endpoint", "port_ip", "ssh_port", "username", "password", "ssh_key").
		Where("id = ?", providerID).
		First(&provider).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, common.NewError(common.CodeNotFound, "Provider不存在")
		}
		return nil, err
	}
	ownerAdminID := middleware.GetOwnerAdminID(c)
	if ownerAdminID > 0 {
		if err := adminProvider.CheckProviderOwnership(provider.ID, ownerAdminID); err != nil {
			return nil, common.NewError(common.CodeForbidden, err.Error())
		}
	}
	return &provider, nil
}

func writeSFTPListResponse(c *gin.Context, remotePath string, entries []os.FileInfo) {
	result := make([]adminSFTPEntry, 0, len(entries))
	for _, entry := range entries {
		result = append(result, adminSFTPEntry{
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

func writeSFTPDownloadResponse(c *gin.Context, filePath string, reader io.Reader, size int64) {
	filename := path.Base(filePath)
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename*=UTF-8''%s", url.QueryEscape(filename)))
	c.Header("Content-Length", strconv.FormatInt(size, 10))
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, reader)
}

// AdminInstanceSFTPList godoc
// @Summary 管理员实例SFTP目录列表
// @Description 列出管理员实例远程目录文件
// @Tags 管理员/实例
// @Produce json
// @Param id path uint true "实例ID"
// @Param path query string false "远程路径"
// @Success 200 {object} common.Response
// @Router /admin/instances/{id}/sftp/list [get]
func AdminInstanceSFTPList(c *gin.Context) {
	instance, err := getAdminInstanceForSFTP(c)
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
		global.APP_LOG.Warn("管理员实例SFTP连接失败", zap.Uint("instanceID", instance.ID), zap.Error(err))
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
	writeSFTPListResponse(c, remotePath, entries)
}

// AdminInstanceSFTPDownload godoc
// @Summary 管理员实例SFTP下载
// @Description 下载管理员实例上的远程文件
// @Tags 管理员/实例
// @Produce octet-stream
// @Param id path uint true "实例ID"
// @Param path query string true "远程文件路径"
// @Success 200 {file} binary
// @Router /admin/instances/{id}/sftp/download [get]
func AdminInstanceSFTPDownload(c *gin.Context) {
	if err := taskgate.EnsureAccepting(); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	instance, err := getAdminInstanceForSFTP(c)
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

	writeSFTPDownloadResponse(c, remoteFilePath, file, info.Size())
}

// AdminInstanceSFTPUpload godoc
// @Summary 管理员实例SFTP上传
// @Description 上传本地文件到管理员实例远程目录
// @Tags 管理员/实例
// @Accept multipart/form-data
// @Produce json
// @Param id path uint true "实例ID"
// @Param targetDir formData string false "远程目标目录"
// @Param file formData file true "上传文件"
// @Success 200 {object} common.Response
// @Router /admin/instances/{id}/sftp/upload [post]
func AdminInstanceSFTPUpload(c *gin.Context) {
	if err := taskgate.EnsureAccepting(); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	instance, err := getAdminInstanceForSFTP(c)
	if err != nil {
		common.ResponseWithError(c, err)
		return
	}

	target, err := remote.ResolveInstanceSSHTarget(instance)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}
	handleAdminSFTPUpload(c, target)
}

// AdminProviderSFTPList godoc
// @Summary 管理员节点SFTP目录列表
// @Description 列出管理员连接到节点宿主机后的远程目录文件
// @Tags 管理员/Provider
// @Produce json
// @Param id path uint true "Provider ID"
// @Param path query string false "远程路径"
// @Success 200 {object} common.Response
// @Router /admin/providers/{id}/sftp/list [get]
func AdminProviderSFTPList(c *gin.Context) {
	provider, err := getAdminProviderForSFTP(c)
	if err != nil {
		common.ResponseWithError(c, err)
		return
	}

	target, err := remote.ResolveProviderSSHTarget(provider)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}

	sftpClient, cleanup, err := remote.OpenSFTPClient(target)
	if err != nil {
		global.APP_LOG.Warn("管理员节点SFTP连接失败", zap.Uint("providerID", provider.ID), zap.Error(err))
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
	writeSFTPListResponse(c, remotePath, entries)
}

// AdminProviderSFTPDownload godoc
// @Summary 管理员节点SFTP下载
// @Description 下载管理员连接到节点宿主机上的远程文件
// @Tags 管理员/Provider
// @Produce octet-stream
// @Param id path uint true "Provider ID"
// @Param path query string true "远程文件路径"
// @Success 200 {file} binary
// @Router /admin/providers/{id}/sftp/download [get]
func AdminProviderSFTPDownload(c *gin.Context) {
	if err := taskgate.EnsureAccepting(); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	provider, err := getAdminProviderForSFTP(c)
	if err != nil {
		common.ResponseWithError(c, err)
		return
	}

	remoteFilePath := remote.NormalizeRemotePath(c.Query("path"))
	if remoteFilePath == "/" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "请指定文件路径"))
		return
	}

	target, err := remote.ResolveProviderSSHTarget(provider)
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

	writeSFTPDownloadResponse(c, remoteFilePath, file, info.Size())
}

// AdminProviderSFTPUpload godoc
// @Summary 管理员节点SFTP上传
// @Description 上传本地文件到管理员连接到节点宿主机上的远程目录
// @Tags 管理员/Provider
// @Accept multipart/form-data
// @Produce json
// @Param id path uint true "Provider ID"
// @Param targetDir formData string false "远程目标目录"
// @Param file formData file true "上传文件"
// @Success 200 {object} common.Response
// @Router /admin/providers/{id}/sftp/upload [post]
func AdminProviderSFTPUpload(c *gin.Context) {
	if err := taskgate.EnsureAccepting(); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	provider, err := getAdminProviderForSFTP(c)
	if err != nil {
		common.ResponseWithError(c, err)
		return
	}

	target, err := remote.ResolveProviderSSHTarget(provider)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}
	handleAdminSFTPUpload(c, target)
}

// AdminInstanceSFTPUploadStatus godoc
// @Summary 管理员实例SFTP上传状态
// @Description 查询实例分片上传进度，用于断点续传
// @Tags 管理员/实例
// @Produce json
// @Param id path uint true "实例ID"
// @Param uploadId query string true "上传ID"
// @Param targetPath query string false "远程目标文件路径"
// @Param targetDir query string false "远程目标目录"
// @Param filename query string false "文件名"
// @Success 200 {object} common.Response
// @Router /admin/instances/{id}/sftp/upload/status [get]
func AdminInstanceSFTPUploadStatus(c *gin.Context) {
	instance, err := getAdminInstanceForSFTP(c)
	if err != nil {
		common.ResponseWithError(c, err)
		return
	}
	target, err := remote.ResolveInstanceSSHTarget(instance)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}
	handleAdminSFTPUploadStatus(c, target)
}

// AdminProviderSFTPUploadStatus godoc
// @Summary 管理员节点SFTP上传状态
// @Description 查询节点宿主机分片上传进度，用于断点续传
// @Tags 管理员/Provider
// @Produce json
// @Param id path uint true "Provider ID"
// @Param uploadId query string true "上传ID"
// @Param targetPath query string false "远程目标文件路径"
// @Param targetDir query string false "远程目标目录"
// @Param filename query string false "文件名"
// @Success 200 {object} common.Response
// @Router /admin/providers/{id}/sftp/upload/status [get]
func AdminProviderSFTPUploadStatus(c *gin.Context) {
	provider, err := getAdminProviderForSFTP(c)
	if err != nil {
		common.ResponseWithError(c, err)
		return
	}
	target, err := remote.ResolveProviderSSHTarget(provider)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}
	handleAdminSFTPUploadStatus(c, target)
}

// AdminInstanceSFTPUploadAbort godoc
// @Summary 管理员实例SFTP上传中断清理
// @Description 清理实例指定 uploadId 对应临时分片文件，允许重传
// @Tags 管理员/实例
// @Accept multipart/form-data
// @Produce json
// @Param id path uint true "实例ID"
// @Param uploadId formData string true "上传ID"
// @Param targetPath formData string false "远程目标文件路径"
// @Param targetDir formData string false "远程目标目录"
// @Param filename formData string false "文件名"
// @Success 200 {object} common.Response
// @Router /admin/instances/{id}/sftp/upload/abort [post]
func AdminInstanceSFTPUploadAbort(c *gin.Context) {
	instance, err := getAdminInstanceForSFTP(c)
	if err != nil {
		common.ResponseWithError(c, err)
		return
	}
	target, err := remote.ResolveInstanceSSHTarget(instance)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}
	handleAdminSFTPUploadAbort(c, target)
}

// AdminProviderSFTPUploadAbort godoc
// @Summary 管理员节点SFTP上传中断清理
// @Description 清理节点宿主机指定 uploadId 对应临时分片文件，允许重传
// @Tags 管理员/Provider
// @Accept multipart/form-data
// @Produce json
// @Param id path uint true "Provider ID"
// @Param uploadId formData string true "上传ID"
// @Param targetPath formData string false "远程目标文件路径"
// @Param targetDir formData string false "远程目标目录"
// @Param filename formData string false "文件名"
// @Success 200 {object} common.Response
// @Router /admin/providers/{id}/sftp/upload/abort [post]
func AdminProviderSFTPUploadAbort(c *gin.Context) {
	provider, err := getAdminProviderForSFTP(c)
	if err != nil {
		common.ResponseWithError(c, err)
		return
	}
	target, err := remote.ResolveProviderSSHTarget(provider)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}
	handleAdminSFTPUploadAbort(c, target)
}

func handleAdminSFTPUploadStatus(c *gin.Context, target *remote.SSHAccessTarget) {
	uploadID := strings.TrimSpace(c.Query("uploadId"))
	if uploadID == "" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "uploadId不能为空"))
		return
	}

	targetDir := remote.NormalizeRemotePath(c.DefaultQuery("targetDir", "/"))
	filename := path.Base(c.DefaultQuery("filename", "unnamed.bin"))
	if filename == "" || filename == "." || filename == "/" {
		filename = "unnamed.bin"
	}
	remotePath := adminResolveUploadTargetPath(c.Query("targetPath"), targetDir, filename)
	remoteDir := remote.NormalizeRemotePath(path.Dir(remotePath))
	remote.RegisterSFTPChunkCleanupTarget(target, path.Dir(remotePath))
	uploadID, idErr := remote.NormalizeChunkUploadID(uploadID)
	if idErr != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, idErr.Error()))
		return
	}

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
	cleanedParts, _ := remote.CleanupExpiredChunkParts(sftpClient, remoteDir, remote.DefaultChunkPartTTL)

	common.ResponseSuccess(c, gin.H{
		"uploadId":      uploadID,
		"path":          remotePath,
		"uploadedBytes": status.UploadedBytes,
		"completed":     status.Completed,
		"cleanedParts":  cleanedParts,
	})
}

func handleAdminSFTPUploadAbort(c *gin.Context, target *remote.SSHAccessTarget) {
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
	remotePath := adminResolveUploadTargetPath(rawTargetPath, targetDir, filename)

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

func handleAdminSFTPUpload(c *gin.Context, target *remote.SSHAccessTarget) {
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

	remotePath := adminResolveUploadTargetPath(c.PostForm("targetPath"), targetDir, filename)
	remoteDir := remote.NormalizeRemotePath(path.Dir(remotePath))
	remote.RegisterSFTPChunkCleanupTarget(target, remoteDir)

	uploadID := strings.TrimSpace(c.PostForm("uploadId"))
	if uploadID != "" {
		normalizedUploadID, idErr := remote.NormalizeChunkUploadID(uploadID)
		if idErr != nil {
			common.ResponseWithError(c, common.NewError(common.CodeValidationError, idErr.Error()))
			return
		}
		uploadID = normalizedUploadID
	}
	chunkIndex, parseErr := adminParseUploadInt64(c, "chunkIndex", 0)
	if parseErr != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, parseErr.Error()))
		return
	}
	totalChunks, parseErr := adminParseUploadInt64(c, "totalChunks", 1)
	if parseErr != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, parseErr.Error()))
		return
	}
	offset, parseErr := adminParseUploadInt64(c, "offset", 0)
	if parseErr != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, parseErr.Error()))
		return
	}
	isLastChunk, parseErr := adminParseUploadBool(c, "isLastChunk", true)
	if parseErr != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, parseErr.Error()))
		return
	}

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

func adminResolveUploadTargetPath(rawTargetPath, targetDir, filename string) string {
	remotePath := remote.NormalizeRemotePath(rawTargetPath)
	if strings.TrimSpace(remotePath) == "/" || strings.TrimSpace(rawTargetPath) == "" {
		return path.Join(targetDir, filename)
	}
	return remotePath
}

func adminParseUploadInt64(c *gin.Context, key string, def int64) (int64, error) {
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

func adminParseUploadBool(c *gin.Context, key string, def bool) (bool, error) {
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
