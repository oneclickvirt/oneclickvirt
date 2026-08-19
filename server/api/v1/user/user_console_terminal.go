package user

import (
	"strconv"

	adminAPI "oneclickvirt/api/v1/admin"
	"oneclickvirt/model/common"
	trafficService "oneclickvirt/service/traffic"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
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

	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer ws.Close()

	switch target.ConnectionType {
	case "agent":
		handleAgentExecTerminal(ws, target.ProviderID, target.Command)
	case "local":
		handleLocalExecTerminal(ws, target.Command)
	case "ssh":
		handleSSHCommandTerminal(ws, target.Provider, target.Command)
	default:
		_ = ws.WriteMessage(websocket.TextMessage, []byte("节点控制台传输不可用\r\n"))
	}
}
