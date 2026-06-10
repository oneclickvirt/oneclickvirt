package admin

import "github.com/gin-gonic/gin"

// BuildInstanceVNCInfoForUser is used by the user API package without duplicating VNC resolution code.
func BuildInstanceVNCInfoForUser(instanceID uint, userID uint) (gin.H, error) {
	return buildInstanceVNCInfo(instanceID, userID, false)
}

func ProxyInstanceVNCForUser(c *gin.Context, instanceID uint, userID uint) {
	host, port, err := resolveInstanceVNCTarget(instanceID, userID, false)
	if err != nil {
		c.JSON(400, gin.H{"code": 400, "msg": err.Error()})
		return
	}
	proxyVNCWebSocket(c, host, port)
}
