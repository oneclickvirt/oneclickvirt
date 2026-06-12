package admin

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/middleware"
	"oneclickvirt/model/common"
	adminProvider "oneclickvirt/service/admin/provider"
	"oneclickvirt/service/taskgate"

	"github.com/gin-gonic/gin"
)

func parseProviderIDs(idsRaw string) ([]uint, error) {
	idsRaw = strings.TrimSpace(idsRaw)
	if idsRaw == "" {
		return nil, nil
	}

	parts := strings.Split(idsRaw, ",")
	ids := make([]uint, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id64, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("无效的provider id: %s", part)
		}
		ids = append(ids, uint(id64))
	}
	return ids, nil
}

// ExportProvidersCSV 导出节点CSV
func ExportProvidersCSV(c *gin.Context) {
	if err := taskgate.EnsureAccepting(); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	providerIDs, err := parseProviderIDs(c.Query("ids"))
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}

	service := adminProvider.NewService()
	csvBytes, err := service.ExportProvidersCSV(middleware.GetOwnerAdminID(c), providerIDs)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	filename := fmt.Sprintf("providers_%s.csv", time.Now().Format("20060102_150405"))
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	c.Data(200, "text/csv; charset=utf-8", csvBytes)
}

// ImportProvidersCSV 导入节点CSV（新增或按标识更新）
func ImportProvidersCSV(c *gin.Context) {
	if err := taskgate.EnsureAccepting(); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	file, err := c.FormFile("file")
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "请上传CSV文件（file字段）"))
		return
	}

	fd, err := file.Open()
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "读取上传文件失败"))
		return
	}
	defer fd.Close()

	body, err := io.ReadAll(fd)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "读取CSV内容失败"))
		return
	}

	service := adminProvider.NewService()
	result, err := service.ImportProvidersCSV(middleware.GetOwnerAdminID(c), body)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	common.ResponseSuccess(c, result, "节点CSV导入完成")
}
