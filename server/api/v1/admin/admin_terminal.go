package admin

// admin_terminal.go — Admin Provider Terminal (WebSocket 交互式终端)
//
// GET /api/v1/admin/providers/:id/terminal?token=<JWT>
//   - Agent 模式: 通过 Agent WebSocket 启动交互式 shell（sh），双向转发 stdin/stdout
//   - SSH 模式:   建立 SSH 连接到 Provider，启动交互式 shell

import (
	"context"
	"encoding/json"
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

// AdminProviderTerminal 管理员远程连接 Provider 的 WebSocket 终端
// 鉴权由 RequireNormalAdmin() 中间件保证
func AdminProviderTerminal(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的Provider ID"))
		return
	}

	var dbProvider providerModel.Provider
	if err := global.APP_DB.Where("id = ?", id).First(&dbProvider).Error; err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeNotFound, "Provider不存在"))
		return
	}

	ws, err := terminalUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		global.APP_LOG.Error("WebSocket 升级失败", zap.Error(err))
		return
	}
	defer ws.Close()

	// 记录连接类型和Provider信息
	global.APP_LOG.Info("Provider远程连接请求",
		zap.Uint("providerID", uint(id)),
		zap.String("providerName", dbProvider.Name),
		zap.String("connectionType", dbProvider.ConnectionType),
		zap.String("type", dbProvider.Type))

	// 根据连接类型分发处理
	if dbProvider.ConnectionType == "agent" {
		global.APP_LOG.Debug("使用Agent模式连接Provider", zap.Uint("providerID", uint(id)))
		handleAgentTerminal(ws, &dbProvider)
	} else if dbProvider.ConnectionType == "ssh" {
		global.APP_LOG.Debug("使用SSH模式连接Provider", zap.Uint("providerID", uint(id)))
		handleSSHTerminal(ws, &dbProvider)
	} else {
		// 如果connectionType为空或未知值，记录警告并默认使用SSH
		global.APP_LOG.Warn("Provider连接类型未设置或不合法，默认使用SSH",
			zap.Uint("providerID", uint(id)),
			zap.String("connectionType", dbProvider.ConnectionType))
		handleSSHTerminal(ws, &dbProvider)
	}
}

// ── Agent 模式终端 ──────────────────────────────────────────────────────────

func handleAgentTerminal(ws *websocket.Conn, p *providerModel.Provider) {
	hub := agentService.GetHub()
	conn, ok := hub.GetConn(p.ID)
	if !ok || conn == nil {
		ws.WriteMessage(websocket.TextMessage, []byte("Agent 节点未连接\r\n"))
		return
	}

	// 通过 agent 启动交互式 shell
	// 使用 sh -i 获取交互式 shell（不是一次性命令）
	// agent Execute 是同步的，需要用特殊方式保持 stdin/stdout 流
	// 方案：启动 `sh -i` 并持续通过 exec 传递数据
	// 更简单的方案：定期 exec 并模拟交互

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	done := make(chan struct{})
	wg := &sync.WaitGroup{}

	// WebSocket → Agent
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			_, msg, err := ws.ReadMessage()
			if err != nil {
				return
			}

			// 解析终端输入
			var input struct {
				Type string `json:"type,omitempty"`
				Data string `json:"data,omitempty"`
				Cols int    `json:"cols,omitempty"`
				Rows int    `json:"rows,omitempty"`
			}
			if json.Unmarshal(msg, &input) == nil && input.Type == "resize" {
				continue // agent 模式暂不支持 resize
			}

			// 执行命令
			cmd := string(msg)
			output, execErr := conn.ExecuteWithTimeout(cmd, 15*time.Second)
			if execErr != nil {
				ws.WriteMessage(websocket.TextMessage, []byte("\r\nError: "+execErr.Error()+"\r\n"))
			} else {
				ws.WriteMessage(websocket.TextMessage, []byte(output))
			}
		}
	}()

	// 发送初始提示
	ws.WriteMessage(websocket.TextMessage, []byte("Connected to "+p.Name+" (agent mode)\r\n$ "))

	wg.Wait()
}

// ── SSH 模式终端 ────────────────────────────────────────────────────────────

func handleSSHTerminal(ws *websocket.Conn, p *providerModel.Provider) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	done := make(chan struct{})
	wg := &sync.WaitGroup{}

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
			// 检查是否为 resize 消息
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
		buf := make([]byte, 8192)
		for {
			n, err := stdoutPipe.Read(buf)
			if n > 0 {
				ws.WriteMessage(websocket.BinaryMessage, buf[:n])
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
		buf := make([]byte, 8192)
		for {
			n, err := stderrPipe.Read(buf)
			if n > 0 {
				ws.WriteMessage(websocket.BinaryMessage, buf[:n])
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
	wg.Wait()
}
