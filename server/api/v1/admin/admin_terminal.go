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
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/middleware"
	"oneclickvirt/model/common"
	providerModel "oneclickvirt/model/provider"
	adminProvider "oneclickvirt/service/admin/provider"
	agentService "oneclickvirt/service/agent"
	remoteService "oneclickvirt/service/remote"

	"github.com/creack/pty"
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

	ownerAdminID := middleware.GetOwnerAdminID(c)
	if ownerAdminID > 0 {
		if err := adminProvider.CheckProviderOwnership(providerID, ownerAdminID); err != nil {
			common.ResponseWithError(c, common.NewError(common.CodeForbidden, err.Error()))
			return
		}
	}

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
	} else if dbProvider.ConnectionType == "local" {
		global.APP_LOG.Debug("使用本机模式连接Provider", zap.Uint("providerID", providerID))
		handleLocalTerminal(ws, &dbProvider, ctx)
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

	client, session, err := openProviderSSHSession(p)
	if err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("SSH 连接失败: "+err.Error()+"\r\n"))
		return
	}
	defer client.Close()
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
	var sessionClosed atomic.Bool
	defer func() {
		if !sessionClosed.Load() {
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
			// 过滤心跳包（ping），避免 JSON 文本被回显到终端
			var pingMsg struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(msg, &pingMsg) == nil && pingMsg.Type == "ping" {
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
					sessionClosed.Store(true)
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

// ── 本机模式终端 ────────────────────────────────────────────────────────────

func handleLocalTerminal(ws *websocket.Conn, p *providerModel.Provider, parentCtx context.Context) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}

	ctx, cancel := context.WithTimeout(parentCtx, 30*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, shell)
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		fmt.Sprintf("OCV_PROVIDER_ID=%d", p.ID),
		fmt.Sprintf("OCV_PROVIDER_NAME=%s", p.Name),
	)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
	if err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("启动本机 Shell 失败: "+err.Error()+"\r\n"))
		return
	}

	cmdDone := make(chan error, 1)
	go func() {
		cmdDone <- cmd.Wait()
	}()
	processExited := false

	cleanup := func() {
		_ = ptmx.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		if !processExited {
			select {
			case <-cmdDone:
			case <-time.After(2 * time.Second):
			}
		}
	}
	defer cleanup()

	writer := newWsWriter(ws)
	wg := &sync.WaitGroup{}

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
				if resize.Cols > 0 && resize.Rows > 0 {
					_ = pty.Setsize(ptmx, &pty.Winsize{Rows: uint16(resize.Rows), Cols: uint16(resize.Cols)})
				}
				continue
			}
			var pingMsg struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(msg, &pingMsg) == nil && pingMsg.Type == "ping" {
				continue
			}
			if _, err := ptmx.Write(msg); err != nil {
				cancel()
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()
		buf := make([]byte, 8192)
		for {
			n, err := ptmx.Read(buf)
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
	case <-ctx.Done():
	case <-cmdDone:
		processExited = true
		cancel()
	}

	_ = ws.SetReadDeadline(time.Now())
	_ = ptmx.Close()
	wg.Wait()
}

// ── SSH 模式终端 ────────────────────────────────────────────────────────────

func handleSSHTerminal(ws *websocket.Conn, p *providerModel.Provider, ctx context.Context) {
	client, session, err := openProviderSSHSession(p)
	if err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("SSH 连接失败: "+err.Error()+"\r\n"))
		return
	}
	defer client.Close()
	defer session.Close()

	handleSSHSessionTerminal(ws, session, ctx)
}

func openProviderSSHSession(p *providerModel.Provider) (*ssh.Client, *ssh.Session, error) {
	target, err := remoteService.ResolveProviderSSHTarget(p)
	if err != nil {
		return nil, nil, err
	}
	client, err := remoteService.OpenSSHClient(target)
	if err != nil {
		return nil, nil, err
	}
	session, err := client.NewSession()
	if err != nil {
		client.Close()
		return nil, nil, fmt.Errorf("创建 SSH 会话失败: %w", err)
	}
	return client, session, nil
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
			// 过滤心跳包（ping），避免 JSON 文本被回显到终端
			var pingMsg struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(msg, &pingMsg) == nil && pingMsg.Type == "ping" {
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
