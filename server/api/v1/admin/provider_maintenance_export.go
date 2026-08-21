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
	target, err := resolveInstanceConsoleTargetForProtocol(instanceID, userID, false, consoleProtocolVNC)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	if !target.available {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "VNC控制台不可用: "+target.reason))
		return
	}
	proxyInstanceConsoleWebSocket(c, target)
}
