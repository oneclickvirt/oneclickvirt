package admin

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	agentService "oneclickvirt/service/agent"
	"oneclickvirt/utils"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const (
	spiceHealthCacheTTL     = 10 * time.Second
	spiceHealthProbeTimeout = 4 * time.Second
)

func isTerminalConsoleProtocol(protocol string) bool {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case consoleProtocolExec, consoleProtocolAttach, consoleProtocolNamespace, consoleProtocolSerial:
		return true
	default:
		return false
	}
}

type spiceHealthState struct {
	available bool
	reason    string
	checkedAt time.Time
}

var (
	spiceHealthMu       sync.Mutex
	spiceHealthCache    = make(map[uint]spiceHealthState)
	spiceHealthInFlight = make(map[uint]chan struct{})
	spiceHealthSlots    = make(chan struct{}, 8)
)

func invalidateSPICEHealth(instanceID uint) {
	spiceHealthMu.Lock()
	pruneSPICEHealthLocked(time.Now())
	delete(spiceHealthCache, instanceID)
	spiceHealthMu.Unlock()
}

// pruneSPICEHealthLocked bounds the short-lived probe cache for controllers
// that serve a large number of instances over a long lifetime. Callers must
// hold spiceHealthMu.
func pruneSPICEHealthLocked(now time.Time) {
	for instanceID, state := range spiceHealthCache {
		if now.Sub(state.checkedAt) > spiceHealthCacheTTL {
			delete(spiceHealthCache, instanceID)
		}
	}
}

// refreshSPICEConsoleTarget proves that persisted websockify metadata still
// maps to a live node-side HTTP service. A reboot removes the old process, so
// treating the database port as authoritative would produce a blank iframe.
func refreshSPICEConsoleTarget(target consoleTarget) consoleTarget {
	if target.protocol != consoleProtocolSPICE || !target.available {
		return target
	}
	available, reason := checkSPICEHealth(target)
	if available {
		return target
	}
	target.available = false
	target.reason = "SPICE 浏览器代理健康检查失败: " + reason
	if consoleProviderTypeSupportsSPICE(target.provider.Type) {
		target.repairable = true
		if target.repairStatus == "" {
			target.repairStatus = "stale"
		}
	}
	return target
}

func cachedSPICEHealth(instanceID uint) (spiceHealthState, bool) {
	spiceHealthMu.Lock()
	defer spiceHealthMu.Unlock()
	pruneSPICEHealthLocked(time.Now())
	state, ok := spiceHealthCache[instanceID]
	return state, ok
}

func checkSPICEHealth(target consoleTarget) (bool, string) {
	if state, ok := cachedSPICEHealth(target.instanceID); ok {
		return state.available, state.reason
	}

	spiceHealthMu.Lock()
	pruneSPICEHealthLocked(time.Now())
	if state, ok := spiceHealthCache[target.instanceID]; ok && time.Since(state.checkedAt) < spiceHealthCacheTTL {
		spiceHealthMu.Unlock()
		return state.available, state.reason
	}
	if done, ok := spiceHealthInFlight[target.instanceID]; ok {
		spiceHealthMu.Unlock()
		select {
		case <-done:
			if state, cached := cachedSPICEHealth(target.instanceID); cached {
				return state.available, state.reason
			}
			return false, "SPICE 健康检查未返回结果"
		case <-time.After(spiceHealthProbeTimeout):
			return false, "SPICE 健康检查繁忙，请稍后重试"
		}
	}
	done := make(chan struct{})
	spiceHealthInFlight[target.instanceID] = done
	spiceHealthMu.Unlock()

	available := false
	reason := "SPICE 健康检查未返回结果"
	select {
	case spiceHealthSlots <- struct{}{}:
		available, reason = probeSPICEWebsockify(target)
		<-spiceHealthSlots
	default:
		reason = "SPICE 健康检查队列繁忙，请稍后重试"
	}

	spiceHealthMu.Lock()
	spiceHealthCache[target.instanceID] = spiceHealthState{available: available, reason: reason, checkedAt: time.Now()}
	delete(spiceHealthInFlight, target.instanceID)
	close(done)
	spiceHealthMu.Unlock()
	return available, reason
}

func probeSPICEWebsockify(target consoleTarget) (bool, string) {
	if target.port < 1 || target.port > 65535 {
		return false, "SPICE 端口无效"
	}
	conn, cleanup, err := openConsoleConn(target)
	if err != nil {
		return false, err.Error()
	}
	defer cleanup()
	_ = conn.SetDeadline(time.Now().Add(spiceHealthProbeTimeout))
	if _, err := fmt.Fprint(conn, "GET /spice_auto.html HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"); err != nil {
		return false, "请求 SPICE 健康资源失败: " + err.Error()
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return false, "读取 SPICE 健康响应失败: " + err.Error()
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusBadRequest {
		return false, fmt.Sprintf("SPICE 健康资源返回 HTTP %d", response.StatusCode)
	}
	return true, ""
}

func openConsoleConn(target consoleTarget) (net.Conn, func(), error) {
	address := net.JoinHostPort(target.host, strconv.Itoa(target.port))
	transport := strings.ToLower(strings.TrimSpace(target.transport))
	if transport == "agent" {
		conn, err := agentService.OpenTunnelConn(target.provider.ID, target.host, target.port)
		return conn, func() {
			if conn != nil {
				_ = conn.Close()
			}
		}, err
	}
	if transport == "ssh" {
		exec, cleanup, err := newConsoleExecutor(target.provider)
		if err != nil {
			return nil, nil, err
		}
		client, ok := exec.(*utils.SSHClient)
		if !ok {
			cleanup()
			return nil, nil, fmt.Errorf("SSH 控制台传输初始化失败")
		}
		conn, err := client.Dial("tcp", address)
		return conn, func() {
			if conn != nil {
				_ = conn.Close()
			}
			cleanup()
		}, err
	}
	conn, err := net.DialTimeout("tcp", address, 10*time.Second)
	return conn, func() {
		if conn != nil {
			_ = conn.Close()
		}
	}, err
}

func proxyInstanceConsoleWebSocket(c *gin.Context, target consoleTarget) {
	ws, err := vncUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	if target.protocol == consoleProtocolSPICE {
		proxySpiceWebSocket(ws, target)
		return
	}
	conn, cleanup, err := openConsoleConn(target)
	if err != nil {
		_ = ws.WriteMessage(websocket.TextMessage, []byte(strings.ToUpper(target.protocol)+" 连接失败: "+err.Error()))
		return
	}
	defer cleanup()
	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			messageType, message, readErr := ws.ReadMessage()
			if readErr != nil {
				return
			}
			if messageType == websocket.BinaryMessage || messageType == websocket.TextMessage {
				if _, writeErr := conn.Write(message); writeErr != nil {
					return
				}
			}
		}
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, 64*1024)
		for {
			n, readErr := conn.Read(buf)
			if n > 0 {
				if writeErr := ws.WriteMessage(websocket.BinaryMessage, buf[:n]); writeErr != nil {
					return
				}
			}
			if readErr != nil {
				return
			}
		}
	}()
	<-done
	_ = ws.Close()
	_ = conn.Close()
}

// proxySpiceWebSocket terminates the panel WebSocket and opens a second
// WebSocket connection to node-local websockify. websockify speaks the
// WebSocket handshake itself, so forwarding the browser's frames to a raw TCP
// socket would leave the SPICE session stuck before negotiation.
func proxySpiceWebSocket(panel *websocket.Conn, target consoleTarget) {
	remoteConn, cleanup, err := openConsoleConn(target)
	if err != nil {
		_ = panel.WriteMessage(websocket.TextMessage, []byte("SPICE 连接失败: "+err.Error()))
		return
	}
	defer cleanup()
	used := false
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		NetDialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			if used {
				return nil, fmt.Errorf("SPICE 代理连接已被使用")
			}
			used = true
			return remoteConn, nil
		},
	}
	remoteURL := "ws://" + net.JoinHostPort(target.host, strconv.Itoa(target.port)) + "/websockify"
	remote, response, err := dialer.Dial(remoteURL, nil)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		_ = panel.WriteMessage(websocket.TextMessage, []byte("SPICE websockify 握手失败: "+err.Error()))
		return
	}
	defer remote.Close()
	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			messageType, message, readErr := panel.ReadMessage()
			if readErr != nil {
				return
			}
			if messageType == websocket.BinaryMessage || messageType == websocket.TextMessage {
				if writeErr := remote.WriteMessage(messageType, message); writeErr != nil {
					return
				}
			}
		}
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			messageType, message, readErr := remote.ReadMessage()
			if readErr != nil {
				return
			}
			if writeErr := panel.WriteMessage(messageType, message); writeErr != nil {
				return
			}
		}
	}()
	<-done
	_ = panel.Close()
	_ = remote.Close()
}

func rewriteSpiceHTML(body []byte, instanceID uint) []byte {
	wsPath := fmt.Sprintf("/api/v1/user/instances/%d/console/spice-ws", instanceID)
	replacement := fmt.Sprintf("spice_query_var('path', %q)", wsPath)
	return spicePathPattern.ReplaceAll(body, []byte(replacement))
}

func cleanSpiceAssetPath(requested string) (string, error) {
	decoded, err := url.PathUnescape(requested)
	if err != nil {
		return "", fmt.Errorf("SPICE 资源路径编码无效")
	}
	if strings.ContainsAny(decoded, "\\\\\r\n\x00") {
		return "", fmt.Errorf("SPICE 资源路径包含不安全字符")
	}
	trimmed := strings.TrimPrefix(decoded, "/")
	for _, segment := range strings.Split(trimmed, "/") {
		if segment == ".." {
			return "", fmt.Errorf("SPICE 资源路径不能包含上级目录")
		}
	}
	clean := path.Clean("/" + trimmed)
	if clean == "/" {
		return "/spice_auto.html", nil
	}
	return clean, nil
}

func copySafeSpiceResponseHeaders(c *gin.Context, response *http.Response) {
	for _, key := range []string{"Content-Type", "Cache-Control", "ETag", "Last-Modified"} {
		for _, value := range response.Header.Values(key) {
			c.Writer.Header().Add(key, value)
		}
	}
	// Node-provided console HTML runs under the panel origin. Keep it confined
	// to same-origin resources and never relay node cookies, redirects or CSP.
	c.Writer.Header().Set("Cache-Control", "no-store")
	c.Writer.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self' ws: wss:; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; worker-src 'self' blob:; base-uri 'none'; form-action 'none'")
	c.Writer.Header().Set("Referrer-Policy", "no-referrer")
	c.Writer.Header().Set("X-Content-Type-Options", "nosniff")
	c.Writer.Header().Set("X-Frame-Options", "SAMEORIGIN")
}

func serveInstanceConsoleSpiceAsset(c *gin.Context, instanceID, userID uint) {
	target, err := resolveInstanceConsoleTargetForProtocol(instanceID, userID, false, consoleProtocolSPICE)
	if err != nil {
		c.String(http.StatusBadGateway, "控制台不可用: %s", err.Error())
		return
	}
	if !target.available {
		c.String(http.StatusBadGateway, "SPICE 控制台尚未就绪: %s", target.reason)
		return
	}
	requested := c.Param("path")
	if requested == "" || requested == "/" {
		requested = "/spice_auto.html"
	}
	clean, err := cleanSpiceAssetPath(requested)
	if err != nil {
		c.String(http.StatusBadRequest, "无效的控制台资源路径: %s", err.Error())
		return
	}
	conn, cleanup, err := openConsoleConn(target)
	if err != nil {
		c.String(http.StatusBadGateway, "连接 SPICE 代理失败: %s", err.Error())
		return
	}
	defer cleanup()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	if _, err := fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\nAccept-Encoding: identity\r\n\r\n", clean); err != nil {
		c.String(http.StatusBadGateway, "请求 SPICE 资源失败: %s", err.Error())
		return
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		c.String(http.StatusBadGateway, "读取 SPICE 资源失败: %s", err.Error())
		return
	}
	defer response.Body.Close()
	const maxSpiceAssetSize = 8 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, maxSpiceAssetSize+1))
	if err != nil {
		c.String(http.StatusBadGateway, "读取 SPICE 资源内容失败: %s", err.Error())
		return
	}
	if len(body) > maxSpiceAssetSize {
		c.String(http.StatusBadGateway, "SPICE 资源超过允许的大小")
		return
	}
	if strings.HasSuffix(clean, ".html") {
		body = rewriteSpiceHTML(body, instanceID)
	}
	copySafeSpiceResponseHeaders(c, response)
	c.Writer.Header().Set("Content-Length", strconv.Itoa(len(body)))
	c.Status(response.StatusCode)
	_, _ = c.Writer.Write(body)
}

// BuildInstanceConsoleInfoForUser is the user-facing capability endpoint.
func BuildInstanceConsoleInfoForUser(instanceID, userID uint) (gin.H, error) {
	return buildInstanceConsoleInfo(instanceID, userID, false)
}

// RepairInstanceConsoleForUser starts an asynchronous, idempotent SPICE setup.
func RepairInstanceConsoleForUser(instanceID, userID uint) (gin.H, error) {
	return startInstanceConsoleRepair(instanceID, userID, false)
}

// ProxyInstanceConsoleForUser proxies the selected VNC or SPICE stream.
func ProxyInstanceConsoleForUser(c *gin.Context, instanceID, userID uint) {
	protocol := strings.ToLower(strings.TrimSpace(c.Query("protocol")))
	if isTerminalConsoleProtocol(protocol) {
		c.String(http.StatusBadRequest, "%s 控制台请使用终端入口，不能通过通用 TCP 控制台 WebSocket 建立", protocol)
		return
	}
	target, err := resolveInstanceConsoleTargetForProtocol(instanceID, userID, false, protocol)
	if err != nil {
		c.String(http.StatusBadGateway, "控制台不可用: %s", err.Error())
		return
	}
	if !target.available {
		c.String(http.StatusBadGateway, "控制台不可用: %s", target.reason)
		return
	}
	if target.protocol != consoleProtocolVNC && target.protocol != consoleProtocolSPICE {
		c.String(http.StatusBadRequest, "协议 %q 需要使用原生控制台链接，不能通过通用 WebSocket 代理", target.protocol)
		return
	}
	proxyInstanceConsoleWebSocket(c, target)
}

// ProxyInstanceConsoleSpiceForUser proxies the SPICE websockify stream.
func ProxyInstanceConsoleSpiceForUser(c *gin.Context, instanceID, userID uint) {
	target, err := resolveInstanceConsoleTargetForProtocol(instanceID, userID, false, consoleProtocolSPICE)
	if err != nil {
		c.String(http.StatusBadGateway, "SPICE 控制台不可用: %s", err.Error())
		return
	}
	if !target.available {
		c.String(http.StatusBadGateway, "SPICE 控制台不可用: %s", target.reason)
		return
	}
	proxyInstanceConsoleWebSocket(c, target)
}

// ServeInstanceConsoleSpiceAssetForUser serves spice-html5 through the
// authenticated panel origin, keeping the node console port private.
func ServeInstanceConsoleSpiceAssetForUser(c *gin.Context, instanceID, userID uint) {
	serveInstanceConsoleSpiceAsset(c, instanceID, userID)
}
