package admin

import (
	"strconv"

	"oneclickvirt/middleware"
	"oneclickvirt/model/common"
	consoleService "oneclickvirt/service/console"

	"github.com/gin-gonic/gin"
)

func adminConsoleInstanceID(c *gin.Context) (uint, error) {
	instanceID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || instanceID == 0 {
		return 0, common.NewError(common.CodeValidationError, "无效的实例ID")
	}
	if err := ensureInstanceOwner(c, uint(instanceID)); err != nil {
		return 0, err
	}
	return uint(instanceID), nil
}

// AdminInstanceConsoleInfo returns every detected control method for an
// administrator-selected instance. It only reads capabilities; the browser
// must explicitly select a protocol before a remote session is opened.
func AdminInstanceConsoleInfo(c *gin.Context) {
	instanceID, err := adminConsoleInstanceID(c)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	info, err := BuildInstanceConsoleInfoForAdmin(instanceID)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	middleware.IssueConsoleSessionCookie(c, instanceID)
	common.ResponseSuccess(c, info)
}

// AdminInstanceConsoleRepair starts the bounded SPICE browser adapter for a
// VM only after an administrator explicitly requests that repair.
func AdminInstanceConsoleRepair(c *gin.Context) {
	instanceID, err := adminConsoleInstanceID(c)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	info, err := RepairInstanceConsoleForAdmin(instanceID)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, info)
}

// AdminInstanceConsoleWebSocket proxies a selected VNC/SPICE control stream.
func AdminInstanceConsoleWebSocket(c *gin.Context) {
	instanceID, err := adminConsoleInstanceID(c)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	ProxyInstanceConsoleForAdmin(c, instanceID)
}

// AdminInstanceConsoleTerminalWebSocket proxies a selected server-generated
// serial/exec/attach console over the configured provider transport.
func AdminInstanceConsoleTerminalWebSocket(c *gin.Context) {
	instanceID, err := adminConsoleInstanceID(c)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	target, err := ResolveInstanceConsoleTerminalForAdmin(instanceID, c.Query("protocol"))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	ws, err := vncUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	consoleService.ProxyTerminalWebSocket(ws, consoleService.TerminalTarget{
		ProviderID: target.ProviderID, ConnectionType: target.ConnectionType,
		Provider: target.Provider, Command: target.Command, Protocol: target.Protocol,
	})
}

// AdminInstanceConsoleSpiceWebSocket proxies the browser-facing websockify
// stream after the path-scoped console cookie has passed authentication.
func AdminInstanceConsoleSpiceWebSocket(c *gin.Context) {
	instanceID, err := adminConsoleInstanceID(c)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	ProxyInstanceConsoleSpiceForAdmin(c, instanceID)
}

// AdminInstanceConsoleSpiceAsset serves node SPICE assets from the panel
// origin, preserving the administrator route scope for all iframe resources.
func AdminInstanceConsoleSpiceAsset(c *gin.Context) {
	instanceID, err := adminConsoleInstanceID(c)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	ServeInstanceConsoleSpiceAssetForAdmin(c, instanceID)
}
