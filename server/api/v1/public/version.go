package public

import (
	"oneclickvirt/constant"
	"oneclickvirt/model/common"

	"github.com/gin-gonic/gin"
)

// VersionInfo holds the server and compatible agent version.
type VersionInfo struct {
	ServerVersion          string `json:"server_version"`
	CompatibleAgentVersion string `json:"compatible_agent_version"`
}

// GetVersion returns the current server version and the compatible agent version.
func GetVersion(c *gin.Context) {
	common.ResponseSuccess(c, VersionInfo{
		ServerVersion:          constant.ServerVersion,
		CompatibleAgentVersion: constant.CompatibleAgentVersion,
	}, "success")
}
