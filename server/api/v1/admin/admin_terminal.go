package admin

// admin_terminal.go — Admin Provider Terminal (WebSocket 交互式终端)
//
// GET /api/v1/admin/providers/:id/terminal?token=<JWT>
//   - Agent 模式: 通过 Agent WebSocket 启动交互式 shell（sh），双向转发 stdin/stdout
//   - SSH 模式:   建立 SSH 连接到 Provider，启动交互式 shell
//
// 安全设计：
//   - 每个 Provider 同一时间只允许一个管理终端连接，新连接会自动取消旧连接。
//   - 使用 safeClose() 防止 session 通道重复关闭导致 panic。
//   - 写超时控制在 3 秒以内，避免清理操作长时间阻塞新会话。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/common"
	providerModel "oneclickvirt/model/provider"
	agentService "oneclickvirt/service/agent"
	"oneclickvirt/utils"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

var terminalUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		return true // 鉴权依赖 JWT token query param
	},
}

// ── 每 Provider 管理终端互斥 ────────────────────────────────────────────────
// 同一 Provider 只允许一个管理终端连接，防止并发 shell 会话导致 Agent 状态混乱。

var (
	adminTerminalMu      sync.Mutex
	adminTerminalCancels = make(map[uint]context.CancelFunc) // providerID → cancel
	adminTerminalGen     = make(map[uint]uint64)             // providerID → 当前会话代数
)

// acquireAdminTerminal 获取 Provider 的管理终端使用权。
// 如果已有活跃终端，会取消旧终端并等待其清理完成（最多 3 秒）。
// 返回一个 context 和 release 函数，调用方必须在终端结束时调用 release。
func acquireAdminTerminal(providerID uint) (ctx context.Context, release func()) {
	adminTerminalMu.Lock()

	// 取消旧的管理终端会话（如果存在）
	if oldCancel, exists := adminTerminalCancels[providerID]; exists {
		oldCancel()
		delete(adminTerminalCancels, providerID)
		// 递增代数，旧会话可以通过检查代数变化来感知自己被取代
		adminTerminalGen[providerID]++
		// 释放锁等待旧会话清理
		adminTerminalMu.Unlock()
		// 给旧会话一点时间完成清理（CloseShell 写超时为 3s）
		time.Sleep(500 * time.Millisecond)
		adminTerminalMu.Lock()
	}

	ctx, cancel := context.WithCancel(context.Background())
	adminTerminalCancels[providerID] = cancel
	gen := adminTerminalGen[providerID]
	adminTerminalMu.Unlock()

	release = func() {
		adminTerminalMu.Lock()
		// 仅当当前代数匹配时才清理（防止旧会话覆盖新会话的状态）
		if adminTerminalGen[providerID] == gen {
			delete(adminTerminalCancels, providerID)
		}
		adminTerminalMu.Unlock()
		cancel()
	}

	return ctx, release
}

// AdminProviderTerminal 管理员远程连接 Provider 的 WebSocket 终端
// 鉴权由 RequireNormalAdmin() 中间件保证
func AdminProviderTerminal(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的Provider ID"))
		return
	}
	providerID := uint(id)

	var dbProvider providerModel.Provider
	if err := global.APP_DB.Where("id = ?", providerID).First(&dbProvider).Error; err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeNotFound, "Provider不存在"))
		return
	}

	// 获取该 Provider 的管理终端使用权（取消旧会话）
	ctx, release := acquireAdminTerminal(providerID)
	defer release()

	ws, err := terminalUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		global.APP_LOG.Error("WebSocket 升级失败", zap.Error(err))
		return
	}
	defer ws.Close()

	// 记录连接类型和Provider信息
	global.APP_LOG.Info("Provider远程连接请求",
		zap.Uint("providerID", providerID),
		zap.String("providerName", dbProvider.Name),
		zap.String("connectionType", dbProvider.ConnectionType),
		zap.String("type", dbProvider.Type))

	// 根据连接类型分发处理
	if dbProvider.ConnectionType == "agent" {
		global.APP_LOG.Debug("使用Agent模式连接Provider", zap.Uint("providerID", providerID))
		handleAgentTerminal(ws, &dbProvider, ctx)
	} else if dbProvider.ConnectionType == "ssh" {
		global.APP_LOG.Debug("使用SSH模式连接Provider", zap.Uint("providerID", providerID))
		handleSSHTerminal(ws, &dbProvider, ctx)
	} else {
		global.APP_LOG.Warn("Provider连接类型未设置或不合法，默认使用SSH",
			zap.Uint("providerID", providerID),
			zap.String("connectionType", dbProvider.ConnectionType))
		handleSSHTerminal(ws, &dbProvider, ctx)
	}
}

// ── 安全写入辅助函数 ────────────────────────────────────────────────────────
// wsWriter 为单个管理终端 WebSocket 提供带写超时的安全写入。
// 内嵌互斥锁确保 gorilla/websocket 的 WriteMessage / SetWriteDeadline 不并发调用，
// 避免违反库的并发约束导致连接状态损坏。
// 写超时防止 TCP 发送缓冲区满时无限阻塞。

const wsWriteTimeout = 5 * time.Second

type wsWriter struct {
	mu sync.Mutex
	ws *websocket.Conn
}

func newWsWriter(ws *websocket.Conn) *wsWriter {
	return &wsWriter{ws: ws}
}

func (w *wsWriter) writeSafe(ctx context.Context, messageType int, data []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	w.ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	return w.ws.WriteMessage(messageType, data)
}

func handleAgentTerminal(ws *websocket.Conn, p *providerModel.Provider, ctx context.Context) {
	if incompatible, minVersion := isAgentVersionIncompatible(p.AgentVersion); incompatible {
		ws.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("Agent版本不兼容（当前: %s，最低要求: %s），请先升级Agent后再重试\r\n", p.AgentVersion, minVersion)))
		return
	}

	hub := agentService.GetHub()

	// 等待 Agent 连接就绪（最多等待 15 秒，适应 Agent 重连场景）
	conn := waitForAgentConn(hub, p.ID, 15*time.Second)
	if conn == nil {
		runtimeHealth := hub.GetRuntimeHealth(p.ID)
		global.APP_LOG.Warn("Agent 终端连接失败：Agent 未连接",
			zap.Uint("providerID", p.ID),
			zap.String("providerName", p.Name),
			zap.String("connectionType", p.ConnectionType),
			zap.String("agentStatus", p.AgentStatus),
			zap.String("runtimeStatus", runtimeHealth.Status),
			zap.Bool("runtimeConnected", runtimeHealth.Connected))
		ws.WriteMessage(websocket.TextMessage, []byte("Agent 节点未连接，请稍后重试\r\n"))
		return
	}

	if p.Username == "" || (p.Password == "" && p.SSHKey == "") {
		handleAgentShellTerminal(ws, p, hub, ctx)
		return
	}

	// SSH 隧道模式：通过 Agent WebSocket 隧道连接 Provider 的 SSH
	tunnelConn, err := agentService.OpenTunnelConn(p.ID, "127.0.0.1", 22)
	if err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("Agent 隧道建立失败: "+err.Error()+"\r\n"))
		return
	}
	defer tunnelConn.Close()

	sshConfig, err := buildTerminalSSHConfig(p)
	if err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("SSH 配置无效: "+err.Error()+"\r\n"))
		return
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(tunnelConn, fmt.Sprintf("agent-provider-%d", p.ID), sshConfig)
	if err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("通过 Agent 隧道建立 SSH 连接失败: "+err.Error()+"\r\n"))
		return
	}
	client := ssh.NewClient(sshConn, chans, reqs)
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("创建 SSH 会话失败: "+err.Error()+"\r\n"))
		return
	}
	defer session.Close()

	handleSSHSessionTerminal(ws, session, ctx)
}

// waitForAgentConn 等待 Agent 连接就绪，支持重连等待。
func waitForAgentConn(hub *agentService.AgentHub, providerID uint, timeout time.Duration) *agentService.AgentConn {
	deadline := time.Now().Add(timeout)
	delay := 200 * time.Millisecond
	for {
		conn, ok := hub.GetConn(providerID)
		if ok && conn != nil {
			return conn
		}
		if time.Now().After(deadline) {
			return nil
		}
		time.Sleep(delay)
		delay *= 2
		if delay > 2*time.Second {
			delay = 2 * time.Second
		}
	}
}

func handleAgentShellTerminal(ws *websocket.Conn, p *providerModel.Provider, hub *agentService.AgentHub, parentCtx context.Context) {
	// 获取当前连接的 AgentConn
	conn := waitForAgentConn(hub, p.ID, 10*time.Second)
	if conn == nil {
		ws.WriteMessage(websocket.TextMessage, []byte("Agent 节点未连接\r\n"))
		return
	}

	session, err := conn.StartShell(80, 24)
	if err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("启动 Agent Shell 失败: "+err.Error()+"\r\n"))
		return
	}

	// 确保会话最终被关闭（使用 safeClose 防止重复关闭 panic）
	sessionClosed := false
	defer func() {
		if !sessionClosed {
			conn.CloseShell(session.ID)
		}
	}()

	ctx, cancel := context.WithTimeout(parentCtx, 30*time.Minute)
	defer cancel()
	wg := &sync.WaitGroup{}

	// 创建安全写入器（每连接独立互斥锁，不阻塞其他管理终端）
	writer := newWsWriter(ws)

	// WebSocket → Shell stdin
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			_, msg, err := ws.ReadMessage()
			if err != nil {
				cancel()
				return
			}
			var resize struct {
				Type string `json:"type"`
				Cols int    `json:"cols"`
				Rows int    `json:"rows"`
			}
			if json.Unmarshal(msg, &resize) == nil && resize.Type == "resize" {
				_ = conn.ResizeShell(session.ID, resize.Cols, resize.Rows)
				continue
			}
			if err := conn.WriteShellInput(session.ID, msg); err != nil {
				_ = writer.writeSafe(ctx, websocket.TextMessage, []byte("\r\nAgent shell 输入失败: "+err.Error()+"\r\n"))
				cancel()
				return
			}
		}
	}()

	// Shell stdout/stderr → WebSocket
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case data, ok := <-session.OutputCh:
				if !ok {
					// OutputCh closed means safeClose() has flushed all buffered
					// data.  Only treat session as closed here, not on DoneCh,
					// to avoid losing the last bytes still queued in OutputCh.
					sessionClosed = true
					cancel()
					return
				}
				if err := writer.writeSafe(ctx, websocket.BinaryMessage, data); err != nil {
					cancel()
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	<-ctx.Done()
	// 强制解除 ws.ReadMessage() 阻塞，让 stdin goroutine 立即退出。
	// 当 Agent shell 会话结束（OutputCh 关闭，小 stdout goroutine 调用 cancel()）时，
	// stdin goroutine 可能阻塞在 ws.ReadMessage()，不设置读超时则 wg.Wait() 永久挂起。
	// 同理，当管理员切换到同一 Provider 的新终端时，父上下文取消会触发此路径。
	_ = ws.SetReadDeadline(time.Now())
	wg.Wait()
}

// ── SSH 模式终端 ────────────────────────────────────────────────────────────

func handleSSHTerminal(ws *websocket.Conn, p *providerModel.Provider, ctx context.Context) {
	sshPort := p.SSHPort
	if sshPort == 0 {
		sshPort = 22
	}

	var client *ssh.Client
	var session *ssh.Session
	var err error

	if p.SSHKey != "" {
		client, session, err = utils.CreateSSHConnectionWithKey(p.Endpoint, sshPort, p.Username, p.SSHKey)
	} else {
		client, session, err = utils.CreateSSHConnection(p.Endpoint, sshPort, p.Username, p.Password)
	}
	if err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("SSH 连接失败: "+err.Error()+"\r\n"))
		return
	}
	defer client.Close()
	defer session.Close()

	handleSSHSessionTerminal(ws, session, ctx)
}

func buildTerminalSSHConfig(p *providerModel.Provider) (*ssh.ClientConfig, error) {
	authMethods := make([]ssh.AuthMethod, 0, 2)
	if p.SSHKey != "" {
		signer, err := ssh.ParsePrivateKey([]byte(p.SSHKey))
		if err != nil {
			return nil, err
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}
	if p.Password != "" {
		authMethods = append(authMethods, ssh.Password(p.Password))
	}
	if len(authMethods) == 0 {
		return nil, fmt.Errorf("missing SSH password or key")
	}
	return &ssh.ClientConfig{
		User:            p.Username,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         30 * time.Second,
	}, nil
}

func handleSSHSessionTerminal(ws *websocket.Conn, session *ssh.Session, parentCtx context.Context) {

	// 设置 PTY
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := session.RequestPty("xterm-256color", 24, 80, modes); err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("请求 PTY 失败: "+err.Error()+"\r\n"))
		return
	}

	stdinPipe, err := session.StdinPipe()
	if err != nil {
		return
	}
	stdoutPipe, err := session.StdoutPipe()
	if err != nil {
		return
	}
	stderrPipe, err := session.StderrPipe()
	if err != nil {
		return
	}

	if err := session.Shell(); err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("启动 Shell 失败: "+err.Error()+"\r\n"))
		return
	}

	// 不强制切换 shell：RequestPty + Shell() 已为用户启动默认 shell
	ctx, cancel := context.WithTimeout(parentCtx, 30*time.Minute)
	defer cancel()
	done := make(chan struct{})
	wg := &sync.WaitGroup{}
	writer := newWsWriter(ws)

	// WebSocket → SSH stdin
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			_, msg, err := ws.ReadMessage()
			if err != nil {
				close(done)
				return
			}
			var resize struct {
				Type string `json:"type"`
				Cols int    `json:"cols"`
				Rows int    `json:"rows"`
			}
			if json.Unmarshal(msg, &resize) == nil && resize.Type == "resize" {
				session.WindowChange(resize.Rows, resize.Cols)
				continue
			}
			stdinPipe.Write(msg)
		}
	}()

	// SSH stdout → WebSocket
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel() // SSH 进程退出时取消 context，解除 stdin goroutine 堵塞
		buf := make([]byte, 8192)
		for {
			n, err := stdoutPipe.Read(buf)
			if n > 0 {
				if werr := writer.writeSafe(ctx, websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// SSH stderr → WebSocket
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel() // SSH 进程退出时取消 context
		buf := make([]byte, 8192)
		for {
			n, err := stderrPipe.Read(buf)
			if n > 0 {
				if werr := writer.writeSafe(ctx, websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	select {
	case <-done:
	case <-ctx.Done():
	}
	// 强制解除 ws.ReadMessage() 阻塞以允许 stdin goroutine 退出
	_ = ws.SetReadDeadline(time.Now())
	// 关闭 SSH session：触发 stdoutPipe/stderrPipe 的 Read 返回 io.EOF，
	// 解除 stdout/stderr goroutine 的阻塞，使 wg.Wait() 能及时返回。
	// 若 WebSocket 由用户关闭（done 触发），不关闭 session 则 goroutine 可能
	// 阻塞在 Read() 长达 30 分钟（直至 ctx 超时）。
	_ = session.Close()
	wg.Wait()
}
