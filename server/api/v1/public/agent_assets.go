package public

import (
	"net/http"
	"oneclickvirt/assets"
	"oneclickvirt/model/common"
	"path/filepath"
	"regexp"

	"github.com/gin-gonic/gin"
)

var allowedAgentReleaseName = regexp.MustCompile(`^oneclickvirt-agent-linux-(amd64|arm64)\.tar\.gz$`)

const (
	githubAgentInstallerURL = "https://raw.githubusercontent.com/oneclickvirt/oneclickvirt/main/scripts/install_agent.sh"
	githubReleaseBaseURL    = "https://github.com/oneclickvirt/oneclickvirt/releases/download"
)

func DownloadAgentInstaller(c *gin.Context) {
	content, err := assets.ReadAgentAsset("install_agent.sh")
	if err != nil {
		// Source-build deployments may not package embedded assets; fall back to GitHub script.
		c.Redirect(http.StatusTemporaryRedirect, githubAgentInstallerURL)
		return
	}
	c.Header("Cache-Control", "public, max-age=300")
	c.Header("Content-Disposition", "inline; filename=install_agent.sh")
	c.Data(http.StatusOK, "text/x-shellscript; charset=utf-8", content)
}

func DownloadAgentRelease(c *gin.Context) {
	name := filepath.Base(c.Param("filename"))
	if !allowedAgentReleaseName.MatchString(name) {
		common.ResponseWithError(c, common.NewError(common.CodeInvalidParam, "invalid release filename"))
		return
	}

	content, err := assets.ReadAgentAsset(name)
	if err != nil {
		// Keep controller source usable even when local release assets are absent.
		releaseURL := githubReleaseBaseURL + "/latest/" + name
		c.Redirect(http.StatusTemporaryRedirect, releaseURL)
		return
	}
	c.Header("Cache-Control", "public, max-age=300")
	c.Header("Content-Disposition", "attachment; filename="+name)
	c.Data(http.StatusOK, "application/gzip", content)
}
