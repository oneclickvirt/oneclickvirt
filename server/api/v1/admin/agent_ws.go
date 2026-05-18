package admin

// agent_ws.go — Agent WebSocket 连接入口
// Rust Agent 调用 GET /api/v1/ws/agent?secret=<AgentSecret> 建立 WebSocket 连接
// 鉴权通过后，连接交由 AgentHub 管理。

import (
	"net"
	"net/http"
	"strings"
	"time"

	"oneclickvirt/model/common"
	agentSvc "oneclickvirt/service/agent"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"oneclickvirt/global"
)

var wsUpgrader = websocket.Upgrader{
	HandshakeTimeout: 10 * time.Second,
	ReadBufferSize:   4096,
	WriteBufferSize:  4096,
	// 允许所有来源（Agent 连接鉴权依赖 AgentSecret，不依赖 Origin）
	CheckOrigin: func(r *http.Request) bool { return true },
}

// AgentWebSocket godoc
//
//	@Summary		Agent 反向 WebSocket 连接入口
//	@Description	Rust Agent 通过此端点主动连回控制端，支持内网穿透模式
//	@Tags			agent
//	@Param			secret	query	string	true	"Agent 鉴权密钥"
//	@Router			/v1/ws/agent [get]
func AgentWebSocket(c *gin.Context) {
	// 1. Query params (legacy, kept for backward compatibility)
	secret := c.Query("secret")
	if secret == "" {
		secret = c.Query("agent_secret")
	}
	if secret == "" {
		secret = c.Query("token")
	}
	// 2. HTTP headers (recommended — avoids URL-query logging)
	if secret == "" {
		if auth := c.GetHeader("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			secret = auth[7:]
		}
	}
	if secret == "" {
		secret = c.GetHeader("X-Agent-Secret")
	}
	if secret == "" {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "缺少 secret 参数（支持 query/header）"))
		return
	}

	providerID, err := agentSvc.LookupProviderBySecret(secret)
	if err != nil {
		global.APP_LOG.Warn("Agent WebSocket 鉴权失败",
			zap.String("remoteAddr", c.Request.RemoteAddr),
			zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "无效的 secret"))
		return
	}

	conn, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		global.APP_LOG.Warn("WebSocket Upgrade 失败",
			zap.Uint("providerID", providerID),
			zap.Error(err))
		return
	}

	// Enable TCP keepalive on the underlying connection so that
	// intermediate NAT gateways / firewalls don't silently drop the
	// WebSocket after long idle periods.  Application-level pings
	// (~30 s) + noise (~5-25 s) already keep the link busy, but TCP
	// keepalive adds defense-in-depth at the transport layer.
	if tcpConn, ok := conn.UnderlyingConn().(*net.TCPConn); ok {
		_ = tcpConn.SetKeepAlive(true)
		_ = tcpConn.SetKeepAlivePeriod(60 * time.Second)
	}

	hub := agentSvc.GetHub()
	ac := agentSvc.NewAgentConn(providerID, conn, c.Request.RemoteAddr)
	hub.Register(ac)
	// Register 内部启动 readLoop goroutine，不阻塞此 handler
}
