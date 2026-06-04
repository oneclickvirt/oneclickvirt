package public

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"oneclickvirt/constant"
	"oneclickvirt/model/common"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// VersionInfo holds the server and compatible agent version.
type VersionInfo struct {
	ServerVersion          string `json:"server_version"`
	CompatibleAgentVersion string `json:"compatible_agent_version"`
	LatestVersion          string `json:"latest_version,omitempty"`
	UpdateAvailable        bool   `json:"update_available"`
	VersionCheckStatus     string `json:"version_check_status"`
	VersionCheckError      string `json:"version_check_error,omitempty"`
	ReleaseURL             string `json:"release_url,omitempty"`
}

type githubVersionResponse struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Name    string `json:"name"`
}

type githubTagResponse struct {
	Name string `json:"name"`
}

type versionCheckCache struct {
	latestVersion string
	releaseURL    string
	status        string
	err           string
	checkedAt     time.Time
}

var (
	versionCacheMu sync.Mutex
	versionCache   versionCheckCache
)

const versionCacheTTL = 30 * time.Minute

// GetVersion returns the current server version and the compatible agent version.
func GetVersion(c *gin.Context) {
	latest, releaseURL, status, checkErr := getLatestVersion()
	common.ResponseSuccess(c, VersionInfo{
		ServerVersion:          constant.DisplayVersion(),
		CompatibleAgentVersion: constant.CompatibleAgentVersion,
		LatestVersion:          latest,
		UpdateAvailable:        isVersionNewer(latest, constant.ServerVersion),
		VersionCheckStatus:     status,
		VersionCheckError:      checkErr,
		ReleaseURL:             releaseURL,
	}, "success")
}

func getLatestVersion() (latestVersion, releaseURL, status, checkErr string) {
	versionCacheMu.Lock()
	if time.Since(versionCache.checkedAt) < versionCacheTTL && versionCache.status != "" {
		cached := versionCache
		versionCacheMu.Unlock()
		return cached.latestVersion, cached.releaseURL, cached.status, cached.err
	}
	versionCacheMu.Unlock()

	latestVersion, releaseURL, err := fetchLatestVersion()
	status = "ok"
	if err != nil {
		status = "failed"
		checkErr = err.Error()
	}

	versionCacheMu.Lock()
	versionCache = versionCheckCache{
		latestVersion: latestVersion,
		releaseURL:    releaseURL,
		status:        status,
		err:           checkErr,
		checkedAt:     time.Now(),
	}
	versionCacheMu.Unlock()
	return latestVersion, releaseURL, status, checkErr
}

func fetchLatestVersion() (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	release, err := getJSON[githubVersionResponse](ctx, "https://api.github.com/repos/oneclickvirt/oneclickvirt/releases/latest")
	if err == nil && strings.TrimSpace(release.TagName) != "" {
		return strings.TrimSpace(release.TagName), strings.TrimSpace(release.HTMLURL), nil
	}

	tags, tagErr := getJSON[[]githubTagResponse](ctx, "https://api.github.com/repos/oneclickvirt/oneclickvirt/tags?per_page=1")
	if tagErr != nil {
		return "", "", fmt.Errorf("release check failed: %v; tag fallback failed: %v", err, tagErr)
	}
	if len(tags) == 0 || strings.TrimSpace(tags[0].Name) == "" {
		return "", "", fmt.Errorf("no release tags found")
	}
	tag := strings.TrimSpace(tags[0].Name)
	return tag, "https://github.com/oneclickvirt/oneclickvirt/releases/tag/" + tag, nil
}

func getJSON[T any](ctx context.Context, url string) (T, error) {
	var zero T
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return zero, err
	}
	req.Header.Set("User-Agent", "oneclickvirt-version-check/"+constant.ServerVersion)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return zero, fmt.Errorf("GitHub API returned %s", resp.Status)
	}
	var out T
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return zero, err
	}
	return out, nil
}

func isVersionNewer(latest, current string) bool {
	latest = normalizeVersionTag(latest)
	current = normalizeVersionTag(current)
	if latest == "" || current == "" || latest == current {
		return false
	}
	latestParts := parseNumericVersion(latest)
	currentParts := parseNumericVersion(current)
	if len(latestParts) == 0 || len(currentParts) == 0 {
		return latest != current
	}
	maxLen := len(latestParts)
	if len(currentParts) > maxLen {
		maxLen = len(currentParts)
	}
	for i := 0; i < maxLen; i++ {
		var l, c int
		if i < len(latestParts) {
			l = latestParts[i]
		}
		if i < len(currentParts) {
			c = currentParts[i]
		}
		if l > c {
			return true
		}
		if l < c {
			return false
		}
	}
	return false
}

func normalizeVersionTag(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "refs/tags/")
	value = strings.TrimPrefix(value, "v")
	if idx := strings.IndexAny(value, " +("); idx >= 0 {
		value = value[:idx]
	}
	return value
}

func parseNumericVersion(value string) []int {
	raw := strings.Split(value, ".")
	parts := make([]int, 0, len(raw))
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		var digits strings.Builder
		for _, r := range item {
			if r < '0' || r > '9' {
				break
			}
			digits.WriteRune(r)
		}
		if digits.Len() == 0 {
			return nil
		}
		n, err := strconv.Atoi(digits.String())
		if err != nil {
			return nil
		}
		parts = append(parts, n)
	}
	return parts
}
