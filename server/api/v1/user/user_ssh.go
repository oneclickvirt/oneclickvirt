package user

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"oneclickvirt/utils"
	"strings"
	"sync"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/common"
	providerModel "oneclickvirt/model/provider"
	agentService "oneclickvirt/service/agent"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
	"gorm.io/gorm"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// WebSocket SSH 使用 query param 传递 token，非 cookie 认证
		// 但仍校验 Origin 以防止跨站 WebSocket 劫持
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // 无 Origin 头（非浏览器客户端）允许
		}
		frontendURL := global.GetAppConfig().System.FrontendURL
		if frontendURL == "" {
			return true // 未配置前端 URL 时放行
		}
		return strings.HasPrefix(origin, frontendURL)
	},
}

// SSHWebSocket 处理WebSocket SSH连接
// @Summary WebSocket SSH连接
// @Description 通过WebSocket建立到实例的SSH连接
// @Tags 用户/实例
// @Accept json
// @Produce json
// @Param id path uint true "实例ID"
// @Success 101 {string} string "Switching Protocols"
// @Failure 400 {object} common.Response "请求参数错误"
// @Failure 401 {object} common.Response "未授权"
// @Failure 404 {object} common.Response "实例不存在"
// @Failure 500 {object} common.Response "服务器错误"
// @Router /user/instances/{id}/ssh [get]
func SSHWebSocket(c *gin.Context) {
	// 获取用户ID
	userIDInterface, exists := c.Get("user_id")
	if !exists {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "未授权"))
		return
	}
	userID := userIDInterface.(uint)

	// 获取实例ID
	instanceID := c.Param("id")
	if instanceID == "" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "实例ID不能为空"))
		return
	}

	// 获取实例信息
	var instance providerModel.Instance
	err := global.APP_DB.Select("id", "name", "provider_id", "status", "private_ip", "public_ip", "ipv6_address", "public_ipv6", "ssh_port", "username", "password").
		Where("id = ? AND user_id = ?", instanceID, userID).
		First(&instance).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ResponseWithError(c, common.NewError(common.CodeNotFound, "实例不存在"))
			return
		}
		global.APP_LOG.Error("查询实例失败", zap.Error(err))
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}

	// 检查实例状态
	if instance.Status != "running" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "实例未运行，无法连接SSH"))
		return
	}

	// 构建SSH连接地址和端口（基于实例信息）
	var sshHost string
	var sshPort int
	var useAgentTunnel bool // 是否使用 Agent 隧道（控制端转发模式）

	// 优先使用SSH端口映射（适用于容器等需要端口转发的场景）
	var sshPortMapping providerModel.Port
	if err := global.APP_DB.Where("instance_id = ? AND is_ssh = true AND status = 'active'", instance.ID).First(&sshPortMapping).Error; err == nil {
		// 检查是否为控制端转发模式（内网穿透）
		if sshPortMapping.MappingType == "controller" {
			// 控制端转发模式：通过 Agent WebSocket 隧道连接
			useAgentTunnel = true
			// 目标地址：优先使用 InternalHost，否则使用实例私有IP
			sshHost = sshPortMapping.InternalHost
			if sshHost == "" {
				sshHost = instance.PrivateIP
			}
			// 目标端口：使用 GuestPort（容器内部SSH端口，通常为22）
			sshPort = sshPortMapping.GuestPort
			if sshPort == 0 {
				sshPort = 22
			}
			global.APP_LOG.Debug("使用Agent隧道连接（控制端转发）",
				zap.String("targetHost", sshHost),
				zap.Int("targetPort", sshPort),
				zap.Uint("providerID", instance.ProviderID))
		} else {
			// 节点侧映射：使用节点公网IP/私有IP + 公网端口
			if instance.PublicIP != "" {
				sshHost = instance.PublicIP
			} else if instance.PrivateIP != "" {
				sshHost = instance.PrivateIP
			} else {
				global.APP_LOG.Error("实例没有可用的IP地址")
				common.ResponseWithError(c, common.NewError(common.CodeInternalError, "实例没有可用的IP地址"))
				return
			}
			sshPort = sshPortMapping.HostPort
			global.APP_LOG.Debug("使用SSH端口映射连接",
				zap.String("host", sshHost),
				zap.Int("hostPort", sshPortMapping.HostPort),
				zap.Int("guestPort", sshPortMapping.GuestPort))
		}
	} else {
		// 没有端口映射，直接使用实例的IP和SSH端口（适用于有独立公网IP的虚拟机）
		if instance.PublicIP != "" {
			sshHost = instance.PublicIP
		} else if instance.PrivateIP != "" {
			sshHost = instance.PrivateIP
			// Agent 模式 provider 的私有IP从控制端不可直接路由，需要通过 Agent WS 隧道转发
			var provider providerModel.Provider
			if err := global.APP_DB.Select("connection_type").Where("id = ?", instance.ProviderID).First(&provider).Error; err == nil && provider.ConnectionType == "agent" {
				useAgentTunnel = true
			}
		} else {
			global.APP_LOG.Error("实例没有可用的IP地址")
			common.ResponseWithError(c, common.NewError(common.CodeInternalError, "实例没有可用的IP地址"))
			return
		}
		sshPort = instance.SSHPort
		if sshPort == 0 {
			sshPort = 22
		}
		global.APP_LOG.Debug("直接使用实例IP和SSH端口连接",
			zap.String("host", sshHost),
			zap.Int("sshPort", sshPort),
			zap.Bool("agentTunnel", useAgentTunnel))
	}

	// 升级到WebSocket
	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		global.APP_LOG.Error("WebSocket升级失败", zap.Error(err))
		return
	}
	defer ws.Close()

	var sshClient *ssh.Client
	var session *ssh.Session

	if useAgentTunnel {
		// 控制端转发模式：通过 Agent WebSocket 隧道建立 SSH 连接
		sshClient, session, err = createSSHOverAgentTunnel(
			instance.ProviderID,
			sshHost,
			sshPort,
			instance.Username,
			instance.Password,
			ws,
		)
	} else {
		// 标准模式：直接 SSH 连接
		sshClient, session, err = createSSHConnection(
			sshHost,
			sshPort,
			instance.Username,
			instance.Password,
		)
	}

	if err != nil {
		global.APP_LOG.Error("SSH连接失败",
			zap.String("host", sshHost),
			zap.Int("port", sshPort),
			zap.Error(err))
		ws.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("SSH连接失败: %v\r\n", err)))
		return
	}
	// 安全兼顾：确保 SSH 资源在任何早期退出路径下都能被释放。
	// 清理阶段的显式 Close() 仍然必要（需在 goroutine 启动后解除阻塞），
	// defer 则覆盖 goroutine 启动前的所有提前退出（StdinPipe/RequestPty/Shell 失败等）。
	defer func() {
		if session != nil {
			session.Close()
		}
		if sshClient != nil {
			sshClient.Close()
		}
	}()

	// 设置终端模式 - 添加更多vim/vi需要的终端模式
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,     // 启用回显
		ssh.TTY_OP_ISPEED: 14400, // 输入速度
		ssh.TTY_OP_OSPEED: 14400, // 输出速度
		ssh.ECHOCTL:       0,     // 不回显控制字符
		ssh.ECHOKE:        1,     // 删除键回显
		ssh.IGNCR:         0,     // 不忽略回车
		ssh.ICRNL:         1,     // 回车转换为换行
		ssh.OPOST:         1,     // 输出后处理
		ssh.ONLCR:         1,     // 换行转换为回车换行
	}

	// 请求PTY - 初始大小设为24x80，这是标准终端大小，与vim兼容性最好
	if err := session.RequestPty("xterm-256color", 24, 80, modes); err != nil {
		global.APP_LOG.Error("请求PTY失败", zap.Error(err))
		ws.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("请求PTY失败: %v\r\n", err)))
		return
	}

	// 获取SSH会话的输入输出
	sshIn, err := session.StdinPipe()
	if err != nil {
		global.APP_LOG.Error("获取SSH stdin失败", zap.Error(err))
		return
	}

	sshOut, err := session.StdoutPipe()
	if err != nil {
		global.APP_LOG.Error("获取SSH stdout失败", zap.Error(err))
		return
	}

	sshErr, err := session.StderrPipe()
	if err != nil {
		global.APP_LOG.Error("获取SSH stderr失败", zap.Error(err))
		return
	}

	// 启动shell
	if err := session.Shell(); err != nil {
		global.APP_LOG.Error("启动shell失败", zap.Error(err))
		ws.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("启动shell失败: %v\r\n", err)))
		return
	}

	// 创建通道来处理错误和超时
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	done := make(chan bool, 1)
	errChan := make(chan error, 3)
	wg := &sync.WaitGroup{} // 跟踪所有goroutine
	// 保护并发 ws.WriteMessage 调用（gorilla/websocket 每次只允许一个写者）
	var wsWriteMu sync.Mutex

	// WebSocket -> SSH
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				global.APP_LOG.Error("WebSocket读取goroutine panic", zap.Any("panic", r))
			}
			select {
			case done <- true:
			default:
			}
		}()

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			messageType, message, err := ws.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
					global.APP_LOG.Error("WebSocket读取失败", zap.Error(err))
				}
				errChan <- err
				return
			}

			// 支持 TextMessage 和 BinaryMessage
			if messageType == websocket.TextMessage || messageType == websocket.BinaryMessage {
				// 处理特殊消息（终端大小调整和心跳）- 只对文本消息尝试JSON解析
				if messageType == websocket.TextMessage {
					var msg map[string]interface{}
					if err := json.Unmarshal(message, &msg); err == nil {
						// 处理终端大小调整
						if msg["type"] == "resize" {
							if cols, ok := msg["cols"].(float64); ok {
								if rows, ok := msg["rows"].(float64); ok {
									if err := session.WindowChange(int(rows), int(cols)); err != nil {
										global.APP_LOG.Error("窗口大小调整失败", zap.Error(err))
									}
									continue
								}
							}
						}
						// 处理心跳包 - 收到心跳后直接忽略，不需要发送到SSH
						if msg["type"] == "ping" {
							continue
						}
					}
				}

				// 普通输入 - 直接写入原始字节，不做任何转换
				if _, err := sshIn.Write(message); err != nil {
					global.APP_LOG.Error("写入SSH失败", zap.Error(err))
					errChan <- err
					return
				}
			}
		}
	}()

	// SSH -> WebSocket (stdout) - 使用更小的buffer减少内存占用
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				global.APP_LOG.Error("SSH stdout goroutine panic", zap.Any("panic", r))
			}
		}()

		buf := make([]byte, 8192)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			n, err := sshOut.Read(buf)
			if err != nil {
				if err != io.EOF {
					global.APP_LOG.Error("读取SSH输出失败", zap.Error(err))
				}
				errChan <- err
				return
			}
			if n > 0 {
				// 使用 BinaryMessage 而不是 TextMessage，避免UTF-8验证问题
				wsWriteMu.Lock()
				ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
				writeErr := ws.WriteMessage(websocket.BinaryMessage, buf[:n])
				wsWriteMu.Unlock()
				if writeErr != nil {
					global.APP_LOG.Error("写入WebSocket失败", zap.Error(writeErr))
					errChan <- writeErr
					return
				}
			}
		}
	}()

	// SSH -> WebSocket (stderr)
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				global.APP_LOG.Error("SSH stderr goroutine panic", zap.Any("panic", r))
			}
		}()

		buf := make([]byte, 8192)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			n, err := sshErr.Read(buf)
			if err != nil {
				if err != io.EOF {
					global.APP_LOG.Error("读取SSH错误输出失败", zap.Error(err))
				}
				return
			}
			if n > 0 {
				// 使用 BinaryMessage 而不是 TextMessage
				wsWriteMu.Lock()
				ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
				writeErr := ws.WriteMessage(websocket.BinaryMessage, buf[:n])
				wsWriteMu.Unlock()
				if writeErr != nil {
					global.APP_LOG.Error("写入WebSocket失败", zap.Error(writeErr))
					return
				}
			}
		}
	}()

	// 等待连接结束或超时
	select {
	case <-done:
		global.APP_LOG.Debug("WebSocket连接关闭")
	case <-ctx.Done():
		global.APP_LOG.Warn("WebSocket连接超时")
	case err := <-errChan:
		if err != nil && err != io.EOF {
			global.APP_LOG.Error("SSH会话错误", zap.Error(err))
		}
	}

	// 立即取消context，通知所有goroutine退出
	cancel()

	// 强制关闭SSH连接和session，确保goroutine能退出
	if session != nil {
		session.Close() // 立即关闭session，中断所有IO操作
	}
	if sshClient != nil {
		sshClient.Close() // 关闭底层连接，强制终止所有goroutine
	}
	// 强制解除 ws.ReadMessage() 阻塞，让 stdin goroutine 立即退出，
	// 而不是等待 defer ws.Close() 触发（否则最多额外延迟 3 秒）。
	_ = ws.SetReadDeadline(time.Now())

	// 等待所有goroutine退出（最多3秒，因为已经强制关闭连接）
	goroutineDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(goroutineDone)
	}()

	gracefulTimer := time.NewTimer(3 * time.Second)
	defer gracefulTimer.Stop()

	select {
	case <-goroutineDone:
		global.APP_LOG.Debug("WebSocket SSH所有goroutine已正常退出")
	case <-gracefulTimer.C:
		// 理论上不应该发生，因为已经强制关闭了所有连接
		global.APP_LOG.Error("WebSocket SSH goroutine退出超时（连接已强制关闭）",
			zap.String("instance", instanceID))
	}
}

// createSSHConnection 创建SSH连接（使用全局函数）
func createSSHConnection(host string, port int, username, password string) (*ssh.Client, *ssh.Session, error) {
	return utils.CreateSSHConnection(host, port, username, password)
}

// createSSHOverAgentTunnel 通过 Agent WebSocket 隧道建立到目标实例的 SSH 连接。
// 用于控制端转发（内网穿透）模式，流量路径：用户 → 控制端TCP监听 → Agent WebSocket → 节点内容器。
func createSSHOverAgentTunnel(providerID uint, targetHost string, targetPort int, username, password string, ws *websocket.Conn) (*ssh.Client, *ssh.Session, error) {
	// 通过 Agent 隧道建立到目标容器的 TCP 连接
	tunnelConn, err := agentService.OpenTunnelConn(providerID, targetHost, targetPort)
	if err != nil {
		ws.WriteMessage(websocket.TextMessage, []byte("Agent 隧道建立失败: "+err.Error()+"\r\n"))
		return nil, nil, fmt.Errorf("agent tunnel failed: %w", err)
	}
	// 注意：tunnelConn 由调用者通过 sshClient.Close() 间接关闭，不在此处 defer

	// 构建 SSH 配置
	sshConfig := &ssh.ClientConfig{
		User:            username,
		Auth:            []ssh.AuthMethod{ssh.Password(password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	}

	// 在隧道连接上建立 SSH 客户端
	sshConn, chans, reqs, err := ssh.NewClientConn(tunnelConn,
		fmt.Sprintf("agent-instance-%d", providerID), sshConfig)
	if err != nil {
		tunnelConn.Close()
		ws.WriteMessage(websocket.TextMessage, []byte("通过 Agent 隧道建立 SSH 连接失败: "+err.Error()+"\r\n"))
		return nil, nil, fmt.Errorf("ssh over agent tunnel failed: %w", err)
	}
	client := ssh.NewClient(sshConn, chans, reqs)

	session, err := client.NewSession()
	if err != nil {
		client.Close()
		ws.WriteMessage(websocket.TextMessage, []byte("创建 SSH 会话失败: "+err.Error()+"\r\n"))
		return nil, nil, fmt.Errorf("ssh session failed: %w", err)
	}

	global.APP_LOG.Debug("通过Agent隧道成功建立SSH连接",
		zap.Uint("providerID", providerID),
		zap.String("targetHost", targetHost),
		zap.Int("targetPort", targetPort))

	return client, session, nil
}
