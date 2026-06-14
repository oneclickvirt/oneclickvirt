package user

import (
	"fmt"
	"io"
	"net/http"
	"strconv"

	"oneclickvirt/model/common"
	snapshotSvc "oneclickvirt/service/snapshot"
	"oneclickvirt/service/taskgate"

	"github.com/gin-gonic/gin"
)

func GetUserInstanceSnapshots(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}
	instanceID, ok := parseUserPathUint(c, "id")
	if !ok {
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "20"))
	service := &snapshotSvc.Service{}
	list, total, err := service.ListUserInstanceSnapshots(userID, instanceID, page, pageSize)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccessWithPagination(c, list, total, page, pageSize)
}

func CreateUserInstanceSnapshot(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}
	instanceID, ok := parseUserPathUint(c, "id")
	if !ok {
		return
	}
	var req snapshotSvc.SnapshotRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}
	service := &snapshotSvc.Service{}
	result, err := service.StartCreateSnapshotTaskForUser(instanceID, req, userID)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, result, "快照创建任务已提交")
}

func DeleteUserSnapshot(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}
	snapshotID, ok := parseUserPathUint(c, "id")
	if !ok {
		return
	}
	service := &snapshotSvc.Service{}
	result, err := service.StartDeleteSnapshotTaskForUser(snapshotID, userID)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, result, "快照删除任务已提交")
}

func RestoreUserSnapshot(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}
	snapshotID, ok := parseUserPathUint(c, "id")
	if !ok {
		return
	}
	service := &snapshotSvc.Service{}
	result, err := service.StartRestoreSnapshotTaskForUser(snapshotID, userID)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, result, "快照恢复任务已提交")
}

func DownloadUserSnapshot(c *gin.Context) {
	if err := taskgate.EnsureAccepting(); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}
	snapshotID, ok := parseUserPathUint(c, "id")
	if !ok {
		return
	}
	service := &snapshotSvc.Service{}
	payload, filename, err := service.BuildSnapshotDownloadManifest(snapshotID, userID, 0)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	c.Data(http.StatusOK, "application/json; charset=utf-8", payload)
}

func UploadUserSnapshot(c *gin.Context) {
	if err := taskgate.EnsureAccepting(); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}
	instanceID, ok := parseUserPathUint(c, "id")
	if !ok {
		return
	}

	const maxSnapshotManifestUploadBytes = 1 << 20
	var payload []byte
	if fileHeader, fileErr := c.FormFile("file"); fileErr == nil {
		if fileHeader.Size > maxSnapshotManifestUploadBytes {
			common.ResponseWithError(c, common.NewError(common.CodeValidationError, "快照清单文件不能超过1MB"))
			return
		}
		file, openErr := fileHeader.Open()
		if openErr != nil {
			common.ResponseWithError(c, common.NewError(common.CodeValidationError, "读取快照清单失败"))
			return
		}
		defer file.Close()
		payload, err = io.ReadAll(io.LimitReader(file, maxSnapshotManifestUploadBytes+1))
	} else {
		payload, err = io.ReadAll(io.LimitReader(c.Request.Body, maxSnapshotManifestUploadBytes+1))
	}
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "读取快照清单失败"))
		return
	}
	if len(payload) > maxSnapshotManifestUploadBytes {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "快照清单文件不能超过1MB"))
		return
	}

	service := &snapshotSvc.Service{}
	snapshot, err := service.ImportUserSnapshotManifest(instanceID, userID, payload)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, snapshot, "快照清单上传成功")
}

func parseUserPathUint(c *gin.Context, key string) (uint, bool) {
	value, err := strconv.ParseUint(c.Param(key), 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的ID"))
		return 0, false
	}
	return uint(value), true
}
