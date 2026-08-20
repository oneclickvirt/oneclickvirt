package public

import (
	"net/url"

	adminAPI "oneclickvirt/api/v1/admin"
	userAPI "oneclickvirt/api/v1/user"
	"oneclickvirt/model/common"
	providerModel "oneclickvirt/model/provider"
	trafficService "oneclickvirt/service/traffic"

	"github.com/gin-gonic/gin"
)

// loadSharedConsoleInstance validates the share token and binds every console
// request to its one database instance before a provider connection is opened.
// Static SPICE assets retain token validation but omit operation accounting;
// the matching WebSocket route applies the full traffic guard before opening a
// provider-side session.
func loadSharedConsoleInstance(c *gin.Context, checkTraffic bool) (uint, uint, bool) {
	var (
		instance *providerModel.Instance
		ok       bool
	)
	if checkTraffic {
		_, instance, ok = loadSharedInstance(c)
	} else {
		_, instance, ok = loadSharedInstanceReadOnly(c)
	}
	if !ok {
		return 0, 0, false
	}
	if err := ensureSharedInstanceUsable(instance, "console"); err != nil {
		common.ResponseWithError(c, err)
		return 0, 0, false
	}
	if checkTraffic {
		if err := trafficService.NewThreeTierLimitService().EnsureUserInstanceOperationAllowed(instance.UserID, instance.ID, "console"); err != nil {
			common.ResponseWithError(c, common.ClassifyError(err))
			return 0, 0, false
		}
	}
	return instance.ID, instance.UserID, true
}

// SharedInstanceConsoleInfo exposes the same explicit protocol chooser as the
// owner and admin views. Capability lookup never opens a remote console.
func SharedInstanceConsoleInfo(c *gin.Context) {
	instanceID, userID, ok := loadSharedConsoleInstance(c, true)
	if !ok {
		return
	}
	info, err := adminAPI.BuildInstanceConsoleInfoForUser(instanceID, userID)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, info)
}

// SharedInstanceConsoleRepair explicitly starts the bounded SPICE adapter.
func SharedInstanceConsoleRepair(c *gin.Context) {
	instanceID, userID, ok := loadSharedConsoleInstance(c, true)
	if !ok {
		return
	}
	info, err := adminAPI.RepairInstanceConsoleForUser(instanceID, userID)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, info)
}

// SharedInstanceConsoleWebSocket proxies the user-selected VNC/SPICE stream.
func SharedInstanceConsoleWebSocket(c *gin.Context) {
	instanceID, userID, ok := loadSharedConsoleInstance(c, true)
	if !ok {
		return
	}
	adminAPI.ProxyInstanceConsoleForUser(c, instanceID, userID)
}

// SharedInstanceConsoleTerminalWebSocket resolves the token-bound target
// before upgrading. It deliberately does not call the JWT-only user handler.
func SharedInstanceConsoleTerminalWebSocket(c *gin.Context) {
	instanceID, userID, ok := loadSharedConsoleInstance(c, true)
	if !ok {
		return
	}
	target, err := adminAPI.ResolveInstanceConsoleTerminalForUser(instanceID, userID, c.Query("protocol"))
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	userAPI.ProxyResolvedInstanceConsoleTerminalWebSocket(c, target)
}

// SharedInstanceConsoleSpiceWebSocket proxies the websockify stream. The
// share token remains in the route rather than being exposed as a JWT query.
func SharedInstanceConsoleSpiceWebSocket(c *gin.Context) {
	instanceID, userID, ok := loadSharedConsoleInstance(c, true)
	if !ok {
		return
	}
	adminAPI.ProxyInstanceConsoleSpiceForUser(c, instanceID, userID)
}

// SharedInstanceConsoleSpiceAsset serves every node SPICE asset through the
// public share route and rewrites its WebSocket to the same bound token scope.
func SharedInstanceConsoleSpiceAsset(c *gin.Context) {
	instanceID, userID, ok := loadSharedConsoleInstance(c, false)
	if !ok {
		return
	}
	wsPath := "/api/v1/public/instance-shares/" + url.PathEscape(c.Param("token")) + "/console/spice-ws"
	adminAPI.ServeInstanceConsoleSpiceAssetForScopedUser(c, instanceID, userID, wsPath)
}
