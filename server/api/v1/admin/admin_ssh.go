package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"oneclickvirt/utils"
	"sync"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/common"
	providerModel "oneclickvirt/model/provider"
	remoteService "oneclickvirt/service/remote"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
	"gorm.io/gorm"
)

var adminUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		appConfig := global.GetAppConfig()
		return utils.OriginAllowedForRequest(r, origin, appConfig.System.FrontendURL, appConfig.Cors.Whitelist)
	},
}

// AdminSSHWebSocket 管理员WebSocket SSH连接
// @Summary 管理员WebSocket SSH连接
// @Description 管理员通过WebSocket建立到任意实例的SSH连接
// @Tags 管理员/实例
// @Accept json
// @Produce json
// @Param id path uint true "实例ID"
// @Success 101 {string} string "Switching Protocols"
// @Failure 400 {object} common.Response "请求参数错误"
// @Failure 401 {object} common.Response "未授权"
// @Failure 404 {object} common.Response "实例不存在"
// @Failure 500 {object} common.Response "服务器错误"
// @Router /admin/instances/{id}/ssh [get]
func AdminSSHWebSocket(c *gin.Context) {
	// 获取实例ID
	instanceID := c.Param("id")
	if instanceID == "" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "实例ID不能为空"))
		return
	}

	// 获取实例信息（管理员可以访问任意实例）
	var instance providerModel.Instance
	err := global.APP_DB.Select("id", "name", "provider_id", "status", "private_ip", "public_ip", "ipv6_address", "public_ipv6", "ssh_port", "username", "password").
		Where("id = ?", instanceID).
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

	target, err := remoteService.ResolveInstanceSSHTarget(&instance)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}

	global.APP_LOG.Info("管理员实例SSH目标已解析",
		zap.String("host", target.Host),
		zap.Int("port", target.Port),
		zap.Bool("agentTunnel", target.UseAgentTunnel),
		zap.Uint("providerID", target.ProviderID))

	// 升级到WebSocket
	ws, err := adminUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		global.APP_LOG.Error("WebSocket升级失败", zap.Error(err))
		return
	}
	defer ws.Close()

	var sshClient *ssh.Client
	var sshSession *ssh.Session

	sshClient, err = remoteService.OpenSSHClient(target)
	if err == nil {
		sshSession, err = sshClient.NewSession()
	}

	if err != nil {
		global.APP_LOG.Error("SSH连接失败",
			zap.Error(err),
			zap.String("host", target.Host),
			zap.Int("port", target.Port),
			zap.Bool("agentTunnel", target.UseAgentTunnel),
		)
		ws.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("SSH连接失败: %v\r\n", err)))
		if sshClient != nil {
			sshClient.Close()
		}
		return
	}
	// 安全兼顾：确保 SSH 资源在任何早期退出路径下都能被释放。
	// 清理阶段的显式 Close() 仍然必要（需在 goroutine 启动后解除阻塞），
	// defer 则覆盖 goroutine 启动前的所有提前退出（StdinPipe/RequestPty/Shell 失败等）。
	defer func() {
		if sshSession != nil {
			sshSession.Close()
		}
		if sshClient != nil {
			sshClient.Close()
		}
	}()

	// 获取SSH输入输出流
	sshStdin, err := sshSession.StdinPipe()
	if err != nil {
		global.APP_LOG.Error("获取SSH stdin失败", zap.Error(err))
		ws.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("获取SSH输入流失败: %v\r\n", err)))
		return
	}

	sshStdout, err := sshSession.StdoutPipe()
	if err != nil {
		global.APP_LOG.Error("获取SSH stdout失败", zap.Error(err))
		ws.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("获取SSH输出流失败: %v\r\n", err)))
		return
	}

	sshStderr, err := sshSession.StderrPipe()
	if err != nil {
		global.APP_LOG.Error("获取SSH stderr失败", zap.Error(err))
		ws.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("获取SSH错误流失败: %v\r\n", err)))
		return
	}

	// 请求伪终端 - 添加更多vim/vi需要的终端模式
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

	// 初始大小设为24x80，这是标准终端大小，与vim兼容性最好
	if err := sshSession.RequestPty("xterm-256color", 24, 80, modes); err != nil {
		global.APP_LOG.Error("请求PTY失败", zap.Error(err))
		ws.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("请求终端失败: %v\r\n", err)))
		return
	}

	// 启动shell
	if err := sshSession.Shell(); err != nil {
		global.APP_LOG.Error("启动Shell失败", zap.Error(err))
		ws.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("启动Shell失败: %v\r\n", err)))
		return
	}

	// 创建context用于超时控制
	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
	defer cancel()

	// 创建通道用于协程通信和退出控制
	done := make(chan struct{})
	wsInputDone := make(chan struct{})
	sshOutputDone := make(chan struct{})
	sshErrorDone := make(chan struct{})
	wg := &sync.WaitGroup{} // 跟踪所有goroutine
	// 使用带写超时的安全写入器（防止客户端TCP缓冲区满时无限阻塞写路径）
	writer := newWsWriter(ws)

	// WebSocket -> SSH (处理用户输入)
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				global.APP_LOG.Error("Admin SSH WebSocket读取goroutine panic", zap.Any("panic", r))
			}
			close(wsInputDone)
		}()

		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			messageType, p, err := ws.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
					global.APP_LOG.Warn("WebSocket读取错误", zap.Error(err))
				}
				return
			}

			// 支持 TextMessage 和 BinaryMessage
			if messageType == websocket.TextMessage || messageType == websocket.BinaryMessage {
				// 处理终端调整大小消息和心跳 - 只对文本消息尝试JSON解析
				if messageType == websocket.TextMessage {
					var msg map[string]interface{}
					if err := json.Unmarshal(p, &msg); err == nil {
						if msgType, ok := msg["type"].(string); ok {
							// 处理终端大小调整
							if msgType == "resize" {
								if cols, ok := msg["cols"].(float64); ok {
									if rows, ok := msg["rows"].(float64); ok {
										if err := sshSession.WindowChange(int(rows), int(cols)); err != nil {
											global.APP_LOG.Warn("窗口大小调整失败", zap.Error(err))
										}
										continue
									}
								}
							}
							// 处理心跳包 - 收到心跳后直接忽略，不需要发送到SSH
							if msgType == "ping" {
								continue
							}
						}
					}
				}

				// 发送数据到SSH - 直接写入原始字节
				if _, err := sshStdin.Write(p); err != nil {
					global.APP_LOG.Warn("写入SSH stdin失败", zap.Error(err))
					return
				}
			}
		}
	}()

	// SSH stdout -> WebSocket
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				global.APP_LOG.Error("Admin SSH stdout goroutine panic", zap.Any("panic", r))
			}
			close(sshOutputDone)
		}()

		buf := make([]byte, 8192)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			n, err := sshStdout.Read(buf)
			if err != nil {
				if err != io.EOF {
					global.APP_LOG.Warn("读取SSH stdout失败", zap.Error(err))
				}
				return
			}
			if n > 0 {
				// 使用 BinaryMessage 而不是 TextMessage，避免UTF-8验证问题
				if writeErr := writer.writeSafe(ctx, websocket.BinaryMessage, buf[:n]); writeErr != nil {
					global.APP_LOG.Warn("写入WebSocket失败", zap.Error(writeErr))
					return
				}
			}
		}
	}()

	// SSH stderr -> WebSocket
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				global.APP_LOG.Error("Admin SSH stderr goroutine panic", zap.Any("panic", r))
			}
			close(sshErrorDone)
		}()

		buf := make([]byte, 8192)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			n, err := sshStderr.Read(buf)
			if err != nil {
				if err != io.EOF {
					global.APP_LOG.Warn("读取SSH stderr失败", zap.Error(err))
				}
				return
			}
			if n > 0 {
				// 使用 BinaryMessage 而不是 TextMessage
				if writeErr := writer.writeSafe(ctx, websocket.BinaryMessage, buf[:n]); writeErr != nil {
					global.APP_LOG.Warn("写入WebSocket失败", zap.Error(writeErr))
					return
				}
			}
		}
	}()

	// 等待任意goroutine退出即触发清理（防止SSH会话服务端结束后终端冻结）。
	// 原先顺序等待 wsInputDone→sshOutputDone→sshErrorDone 会在 SSH 进程退出、
	// 客户端尚未断开时无限等待 wsInputDone，导致终端看起来卡死。
	go func() {
		select {
		case <-wsInputDone:
		case <-sshOutputDone:
		case <-sshErrorDone:
		}
		close(done)
	}()

	// 等待连接关闭或超时
	select {
	case <-done:
		// 正常关闭
		global.APP_LOG.Info("管理员SSH会话正常关闭",
			zap.String("instanceID", instanceID))
	case <-ctx.Done():
		// 超时保护，强制关闭
		global.APP_LOG.Warn("SSH会话超时，强制关闭",
			zap.String("instanceID", instanceID))
	}

	// 立即取消context
	cancel()

	// 强制关闭SSH连接和session，确保goroutine能退出
	if sshSession != nil {
		sshSession.Close() // 立即关闭session，中断所有IO操作
	}
	if sshClient != nil {
		sshClient.Close() // 关闭底层连接，强制终止所有goroutine
	}
	// 强制解除 ws.ReadMessage() 阻塞，让 stdin goroutine 立即退出，
	// 而不是等待 defer ws.Close() 触发（否则最多额外延迟 3 秒）。
	_ = ws.SetReadDeadline(time.Now())

	// 等待所有goroutine退出（最多3秒）
	goroutineDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(goroutineDone)
	}()

	gracefulTimer := time.NewTimer(3 * time.Second)
	defer gracefulTimer.Stop()

	select {
	case <-goroutineDone:
		global.APP_LOG.Debug("Admin SSH所有goroutine已正常退出",
			zap.String("instanceID", instanceID))
	case <-gracefulTimer.C:
		// 理论上不应该发生，因为已经强制关闭了所有连接
		global.APP_LOG.Error("Admin SSH goroutine退出超时（连接已强制关闭）",
			zap.String("instanceID", instanceID))
	}

	global.APP_LOG.Info("管理员SSH会话结束",
		zap.String("instanceID", instanceID),
		zap.String("instanceName", instance.Name),
	)
}
