package admin

import (
	"oneclickvirt/model/common"

	"github.com/gin-gonic/gin"
)

// BuildInstanceVNCInfoForUser is used by the user API package without duplicating VNC resolution code.
func BuildInstanceVNCInfoForUser(instanceID uint, userID uint) (gin.H, error) {
	return buildInstanceVNCInfo(instanceID, userID, false)
}

func ProxyInstanceVNCForUser(c *gin.Context, instanceID uint, userID uint) {
	host, port, err := resolveInstanceVNCTarget(instanceID, userID, false)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	proxyVNCWebSocket(c, host, port)
}
