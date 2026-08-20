package user

import (
	"strconv"

	adminAPI "oneclickvirt/api/v1/admin"
	"oneclickvirt/model/common"
	consoleService "oneclickvirt/service/console"
	trafficService "oneclickvirt/service/traffic"

	"github.com/gin-gonic/gin"
)

// UserInstanceConsoleTerminalWebSocket provides a provider-host terminal for
// the `exec` and `serial` capabilities advertised by /console. It is separate
// from the legacy Web SSH endpoint: the command is generated server-side from
// a provider template and the browser cannot supply host commands.
func UserInstanceConsoleTerminalWebSocket(c *gin.Context) {
	userID, err := getUserID(c)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, err.Error()))
		return
	}
	instanceID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的实例ID"))
		return
	}
	if err := trafficService.NewThreeTierLimitService().EnsureUserInstanceOperationAllowed(userID, uint(instanceID), "console"); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeForbidden, err.Error()))
		return
	}
	target, err := adminAPI.ResolveInstanceConsoleTerminalForUser(uint(instanceID), userID, c.Query("protocol"))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	ProxyResolvedInstanceConsoleTerminalWebSocket(c, target)
}

// ProxyResolvedInstanceConsoleTerminalWebSocket upgrades a terminal only
// after its caller has authenticated and resolved a fixed command. Share-token
// routes use this hand-off without re-running JWT-only user authentication.
func ProxyResolvedInstanceConsoleTerminalWebSocket(c *gin.Context, target adminAPI.InstanceConsoleTerminalTarget) {
	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	consoleService.ProxyTerminalWebSocket(ws, consoleService.TerminalTarget{
		ProviderID: target.ProviderID, ConnectionType: target.ConnectionType,
		Provider: target.Provider, Command: target.Command, Protocol: target.Protocol,
	})
}
