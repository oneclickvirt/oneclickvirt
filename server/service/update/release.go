package update

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/constant"
	"oneclickvirt/global"

	"go.uber.org/zap"
)

type githubRelease struct {
	TagName     string        `json:"tag_name"`
	HTMLURL     string        `json:"html_url"`
	Name        string        `json:"name"`
	PublishedAt string        `json:"published_at"`
	Prerelease  bool          `json:"prerelease"`
	Draft       bool          `json:"draft"`
	Assets      []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
	ContentType        string `json:"content_type"`
	Digest             string `json:"digest"`
}

const (
	checksumAssetName       = "SHA256SUMS"
	maxChecksumManifestSize = 1 << 20
)

var releaseTagPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+/-]{0,127}$`)

func (s *Service) fetchReleases(ctx context.Context, cfg runtimeConfig) ([]githubRelease, error) {
	if cached, ok := s.cachedReleases(cfg.Repo); ok {
		return cached, nil
	}
	requestCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	apiPaths := make([]string, 0, len(cfg.APIEndpoints))
	for _, endpoint := range cfg.APIEndpoints {
		apiPaths = append(apiPaths, strings.TrimRight(endpoint, "/")+"/repos/"+cfg.Repo+"/releases?per_page=30")
	}
	if len(apiPaths) == 0 {
		apiPaths = []string{fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=30", cfg.Repo)}
	}
	var lastErr error
	for _, apiPath := range apiPaths {
		endpoints := s.metadataURLs(apiPath, cfg)
		for _, endpoint := range endpoints {
			var releases []githubRelease
			if err := s.getJSON(requestCtx, endpoint, &releases, cfg); err != nil {
				lastErr = err
				continue
			}
			filtered := make([]githubRelease, 0, len(releases))
			for _, release := range releases {
				tag := normalizeTag(release.TagName)
				if release.Draft || tag == "" || !releaseTagPattern.MatchString(release.TagName) {
					continue
				}
				filtered = append(filtered, release)
			}
			sort.SliceStable(filtered, func(i, j int) bool {
				return compareVersions(filtered[i].TagName, filtered[j].TagName) > 0
			})
			s.storeReleases(cfg.Repo, filtered)
			return filtered, nil
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("未获取到可用版本")
	}
	return nil, fmt.Errorf("获取版本发布信息失败: %w", lastErr)
}

func (s *Service) metadataURLs(original string, cfg runtimeConfig) []string {
	urls := []string{original}
	for _, endpoint := range cfg.CDNEndpoints {
		if !isHTTPSURL(endpoint) {
			continue
		}
		urls = append(urls, joinProxyURL(endpoint, original))
	}
	return uniqueStrings(urls)
}

func (s *Service) assetURLs(asset githubAsset, cfg runtimeConfig) []string {
	urls := make([]string, 0, len(cfg.CDNEndpoints)+1)
	if asset.BrowserDownloadURL != "" && isAllowedRemoteURL(asset.BrowserDownloadURL, cfg) {
		urls = append(urls, asset.BrowserDownloadURL)
	}
	for _, endpoint := range cfg.CDNEndpoints {
		if !isHTTPSURL(endpoint) || asset.BrowserDownloadURL == "" {
			continue
		}
		proxied := joinProxyURL(endpoint, asset.BrowserDownloadURL)
		if isAllowedRemoteURL(proxied, cfg) {
			urls = append(urls, proxied)
		}
	}
	return uniqueStrings(urls)
}

func (s *Service) getJSON(ctx context.Context, endpoint string, target interface{}, cfg runtimeConfig) error {
	if !isAllowedRemoteURL(endpoint, cfg) {
		return fmt.Errorf("拒绝未授权版本元数据地址")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "oneclickvirt-update/"+constant.ServerVersion)
	client := s.httpClientFor(cfg)
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("remote endpoint returned %s", response.Status)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 8<<20))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("解析版本元数据失败: %w", err)
	}
	return nil
}

func (s *Service) httpClientFor(cfg runtimeConfig) *http.Client {
	s.clientMu.Lock()
	defer s.clientMu.Unlock()
	if s.client != nil {
		return s.client
	}
	allowed := allowedRemoteHosts(cfg)
	s.client = &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if request.URL.Scheme != "https" {
				return fmt.Errorf("拒绝非 HTTPS 重定向")
			}
			if _, ok := allowed[strings.ToLower(request.URL.Hostname())]; !ok {
				return fmt.Errorf("拒绝未授权下载域名: %s", request.URL.Hostname())
			}
			if len(via) >= 5 {
				return fmt.Errorf("重定向次数过多")
			}
			return nil
		},
	}
	return s.client
}

func isHTTPSURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && strings.EqualFold(parsed.Scheme, "https") && parsed.Hostname() != "" && parsed.User == nil
}

func allowedRemoteHosts(cfg runtimeConfig) map[string]struct{} {
	allowed := make(map[string]struct{}, len(cfg.CDNEndpoints)+len(cfg.APIEndpoints)+8)
	for _, endpoint := range append(append([]string(nil), cfg.CDNEndpoints...), cfg.APIEndpoints...) {
		if parsed, err := url.Parse(endpoint); err == nil && parsed.Hostname() != "" {
			allowed[strings.ToLower(parsed.Hostname())] = struct{}{}
		}
	}
	for _, host := range []string{
		"api.github.com",
		"github.com",
		"objects.githubusercontent.com",
		"release-assets.githubusercontent.com",
		"github-releases.githubusercontent.com",
		"githubusercontent.com",
		"raw.githubusercontent.com",
	} {
		allowed[host] = struct{}{}
	}
	return allowed
}

func isAllowedRemoteURL(value string, cfg runtimeConfig) bool {
	if !isHTTPSURL(value) {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	_, ok := allowedRemoteHosts(cfg)[strings.ToLower(parsed.Hostname())]
	return ok
}

func joinProxyURL(endpoint, original string) string {
	return strings.TrimRight(endpoint, "/") + "/" + strings.TrimLeft(original, "/")
}

func normalizeTag(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "refs/tags/")
	value = strings.TrimPrefix(value, "v")
	if index := strings.IndexAny(value, " +("); index >= 0 {
		value = value[:index]
	}
	return value
}

func compareVersions(left, right string) int {
	left = normalizeTag(left)
	right = normalizeTag(right)
	if left == right {
		return 0
	}
	leftParts, leftOK := numericParts(left)
	rightParts, rightOK := numericParts(right)
	if leftOK && rightOK {
		length := len(leftParts)
		if len(rightParts) > length {
			length = len(rightParts)
		}
		for index := 0; index < length; index++ {
			var l, r int
			if index < len(leftParts) {
				l = leftParts[index]
			}
			if index < len(rightParts) {
				r = rightParts[index]
			}
			if l > r {
				return 1
			}
			if l < r {
				return -1
			}
		}
	}
	if left > right {
		return 1
	}
	return -1
}

func numericParts(value string) ([]int, bool) {
	parts := strings.Split(value, ".")
	result := make([]int, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			return nil, false
		}
		digits := part
		for index, r := range part {
			if r < '0' || r > '9' {
				digits = part[:index]
				break
			}
		}
		if digits == "" {
			return nil, false
		}
		number, err := strconv.Atoi(digits)
		if err != nil {
			return nil, false
		}
		result = append(result, number)
	}
	return result, true
}

func publicRelease(release githubRelease, cfg runtimeConfig) Release {
	result := Release{
		Tag:         release.TagName,
		Name:        release.Name,
		URL:         release.HTMLURL,
		PublishedAt: release.PublishedAt,
		Prerelease:  release.Prerelease,
		Assets:      make([]ReleaseAsset, 0, len(release.Assets)),
	}
	for _, asset := range release.Assets {
		result.Assets = append(result.Assets, ReleaseAsset{
			Name:        asset.Name,
			DownloadURL: asset.BrowserDownloadURL,
			Size:        asset.Size,
			ContentType: asset.ContentType,
			Digest:      asset.Digest,
		})
	}
	if _, err := requiredAssets(release, cfg); err != nil {
		result.CanApply = false
		result.UnavailableReason = err.Error()
	} else {
		result.CanApply = true
	}
	return result
}

func latestStableRelease(releases []githubRelease) (githubRelease, bool) {
	for _, release := range releases {
		if !release.Prerelease {
			return release, true
		}
	}
	return githubRelease{}, false
}

func requiredAssets(release githubRelease, cfg runtimeConfig) (map[string]githubAsset, error) {
	return requiredAssetsFor(release, cfg, runtime.GOOS, runtime.GOARCH)
}

func requiredAssetsFor(release githubRelease, cfg runtimeConfig, goos, arch string) (map[string]githubAsset, error) {
	if goos != "linux" {
		return nil, fmt.Errorf("当前系统不是 Linux，暂无受控本机资产")
	}
	if arch != "amd64" && arch != "arm64" {
		return nil, fmt.Errorf("当前 CPU 架构暂无受控本机资产: %s", arch)
	}
	serverName := fmt.Sprintf("server-linux-%s.tar.gz", arch)
	if cfg.Flavor == FlavorAllInOne {
		serverName = fmt.Sprintf("server-allinone-linux-%s.tar.gz", arch)
	}
	assets := make(map[string]githubAsset, len(release.Assets))
	for _, asset := range release.Assets {
		assets[asset.Name] = asset
	}
	serverAsset, ok := assets[serverName]
	if !ok || !isAllowedRemoteURL(serverAsset.BrowserDownloadURL, cfg) {
		return nil, fmt.Errorf("发布版本缺少 %s", serverName)
	}
	result := map[string]githubAsset{"server": serverAsset}
	if cfg.UpdateWeb {
		webAsset, ok := assets["web-dist.zip"]
		if !ok || !isAllowedRemoteURL(webAsset.BrowserDownloadURL, cfg) {
			return nil, fmt.Errorf("发布版本缺少 web-dist.zip")
		}
		result["web"] = webAsset
	}
	checksumAsset, checksumAvailable := assets[checksumAssetName]
	if !checksumAvailable || !isAllowedRemoteURL(checksumAsset.BrowserDownloadURL, cfg) {
		if !cfg.AllowUnverified {
			return nil, fmt.Errorf("发布版本缺少 %s 校验清单", checksumAssetName)
		}
	} else {
		result["checksums"] = checksumAsset
	}
	return result, nil
}

// parseChecksumManifest accepts the conventional output of sha256sum. Release
// asset names are deliberately restricted to a basename so a manifest cannot
// direct verification to a path outside the staged directory.
func parseChecksumManifest(data []byte, names ...string) (map[string]string, error) {
	if len(data) == 0 || len(data) > maxChecksumManifestSize {
		return nil, fmt.Errorf("SHA-256 校验清单大小异常")
	}
	wanted := make(map[string]struct{}, len(names))
	for _, name := range names {
		if name == "" || filepath.Base(name) != name || strings.ContainsAny(name, `/\\`) {
			return nil, fmt.Errorf("校验目标文件名无效")
		}
		wanted[name] = struct{}{}
	}
	result := make(map[string]string, len(wanted))
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 1024), maxChecksumManifestSize)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 2 {
			return nil, fmt.Errorf("SHA-256 校验清单格式无效")
		}
		digest, err := normalizeSHA256Digest(parts[0])
		if err != nil {
			return nil, err
		}
		name := strings.TrimPrefix(parts[1], "*")
		if name == "" || filepath.Base(name) != name || strings.ContainsAny(name, `/\\`) {
			return nil, fmt.Errorf("SHA-256 校验清单包含不安全文件名")
		}
		if _, ok := wanted[name]; !ok {
			continue
		}
		if existing, exists := result[name]; exists && existing != digest {
			return nil, fmt.Errorf("SHA-256 校验清单包含冲突条目: %s", name)
		}
		result[name] = digest
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取 SHA-256 校验清单失败: %w", err)
	}
	for name := range wanted {
		if result[name] == "" {
			return nil, fmt.Errorf("SHA-256 校验清单缺少 %s", name)
		}
	}
	return result, nil
}

func verifyDigest(filePath, digest string, allowMissing bool) error {
	if strings.TrimSpace(digest) == "" {
		if allowMissing {
			return nil
		}
		return fmt.Errorf("发布资产缺少 SHA-256 校验值")
	}
	digest, err := normalizeSHA256Digest(digest)
	if err != nil {
		return err
	}
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return err
	}
	actual := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(actual, digest) {
		return fmt.Errorf("发布资产 SHA-256 校验失败")
	}
	return nil
}

func normalizeSHA256Digest(value string) (string, error) {
	digest := strings.TrimSpace(strings.TrimPrefix(strings.ToLower(value), "sha256:"))
	if digest == "" {
		return "", fmt.Errorf("发布资产缺少 SHA-256 校验值")
	}
	if len(digest) != sha256.Size*2 {
		return "", fmt.Errorf("发布校验值格式无效")
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return "", fmt.Errorf("发布校验值格式无效: %w", err)
	}
	return digest, nil
}

func logUpdate(level string, message string, fields ...zap.Field) {
	if global.APP_LOG == nil {
		return
	}
	switch level {
	case "warn":
		global.APP_LOG.Warn(message, fields...)
	case "error":
		global.APP_LOG.Error(message, fields...)
	default:
		global.APP_LOG.Info(message, fields...)
	}
}
