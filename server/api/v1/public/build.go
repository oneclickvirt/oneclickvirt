package public

import (
	"oneclickvirt/constant"
	"oneclickvirt/model/common"

	"github.com/gin-gonic/gin"
)

// GetBuildInfo 获取构建信息
func GetBuildInfo(c *gin.Context) {
	common.ResponseSuccess(c, gin.H{
		"version":   constant.DisplayVersion(),
		"commit":    constant.BuildCommit,
		"buildTime": constant.BuildTime,
		"official":  constant.IsOfficialBuild(),
	})
}
