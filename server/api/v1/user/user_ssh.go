package user

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"

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
		return true // 在生产环境中应该进行更严格的检查
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
// @Router /v1/user/instances/{id}/ssh [get]
func SSHWebSocket(c *gin.Context) {
	// 获取用户ID
	userIDInterface, exists := c.Get("user_id")
	if !exists {
		c.JSON(401, gin.H{"code": 401, "message": "未授权"})
		return
	}
	userID := userIDInterface.(uint)

	// 获取实例ID
	instanceID := c.Param("id")
	if instanceID == "" {
		c.JSON(400, gin.H{"code": 400, "message": "实例ID不能为空"})
		return
	}

	// 获取实例信息
	var instance providerModel.Instance
	err := global.APP_DB.Where("id = ? AND user_id = ?", instanceID, userID).First(&instance).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(404, gin.H{"code": 404, "message": "实例不存在"})
			return
		}
		global.APP_LOG.Error("查询实例失败", zap.Error(err))
		c.JSON(500, gin.H{"code": 500, "message": "查询实例失败"})
		return
	}

	// 检查实例状态
	if instance.Status != "running" {
		c.JSON(400, gin.H{"code": 400, "message": "实例未运行，无法连接SSH"})
		return
	}

	// 获取Provider信息以获取SSH连接地址
	var provider providerModel.Provider
	err = global.APP_DB.Where("id = ?", instance.ProviderID).First(&provider).Error
	if err != nil {
		global.APP_LOG.Error("查询Provider失败", zap.Error(err))
		c.JSON(500, gin.H{"code": 500, "message": "查询Provider失败"})
		return
	}

	// 获取SSH端口映射
	var sshPort int
	var sshPortMapping providerModel.Port
	if err := global.APP_DB.Where("instance_id = ? AND is_ssh = true AND status = 'active'", instance.ID).First(&sshPortMapping).Error; err == nil {
		sshPort = sshPortMapping.HostPort
	} else {
		sshPort = instance.SSHPort
	}

	// 构建SSH地址
	var sshHost string
	if provider.PortIP != "" {
		sshHost = provider.PortIP
	} else {
		sshHost = provider.Endpoint
	}

	// 升级到WebSocket
	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		global.APP_LOG.Error("WebSocket升级失败", zap.Error(err))
		return
	}
	defer ws.Close()

	// 建立SSH连接
	sshClient, session, err := createSSHConnection(
		sshHost,
		sshPort,
		instance.Username,
		instance.Password,
	)
	if err != nil {
		global.APP_LOG.Error("SSH连接失败",
			zap.String("host", sshHost),
			zap.Int("port", sshPort),
			zap.Error(err))
		ws.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("SSH连接失败: %v\r\n", err)))
		return
	}
	defer sshClient.Close()
	defer session.Close()

	// 设置终端模式
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}

	// 请求PTY
	if err := session.RequestPty("xterm-256color", 40, 80, modes); err != nil {
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

	// 创建通道来处理错误
	done := make(chan bool)
	errChan := make(chan error, 2)

	// WebSocket -> SSH
	go func() {
		defer close(done)
		for {
			messageType, message, err := ws.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
					global.APP_LOG.Error("WebSocket读取失败", zap.Error(err))
				}
				errChan <- err
				return
			}

			if messageType == websocket.TextMessage {
				// 处理特殊消息（终端大小调整）
				var msg map[string]interface{}
				if err := json.Unmarshal(message, &msg); err == nil {
					if msg["type"] == "resize" {
						if cols, ok := msg["cols"].(float64); ok {
							if rows, ok := msg["rows"].(float64); ok {
								session.WindowChange(int(rows), int(cols))
								continue
							}
						}
					}
				}

				// 普通输入
				if _, err := sshIn.Write(message); err != nil {
					global.APP_LOG.Error("写入SSH失败", zap.Error(err))
					errChan <- err
					return
				}
			}
		}
	}()

	// SSH -> WebSocket (stdout)
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := sshOut.Read(buf)
			if err != nil {
				if err != io.EOF {
					global.APP_LOG.Error("读取SSH输出失败", zap.Error(err))
				}
				errChan <- err
				return
			}
			if n > 0 {
				if err := ws.WriteMessage(websocket.TextMessage, buf[:n]); err != nil {
					global.APP_LOG.Error("写入WebSocket失败", zap.Error(err))
					errChan <- err
					return
				}
			}
		}
	}()

	// SSH -> WebSocket (stderr)
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := sshErr.Read(buf)
			if err != nil {
				if err != io.EOF {
					global.APP_LOG.Error("读取SSH错误输出失败", zap.Error(err))
				}
				return
			}
			if n > 0 {
				if err := ws.WriteMessage(websocket.TextMessage, buf[:n]); err != nil {
					global.APP_LOG.Error("写入WebSocket失败", zap.Error(err))
					return
				}
			}
		}
	}()

	// 等待连接结束
	select {
	case <-done:
		global.APP_LOG.Info("WebSocket连接关闭")
	case err := <-errChan:
		if err != nil && err != io.EOF {
			global.APP_LOG.Error("SSH会话错误", zap.Error(err))
		}
	}

	// 等待SSH会话结束
	session.Wait()
}

// createSSHConnection 创建SSH连接
func createSSHConnection(host string, port int, username, password string) (*ssh.Client, *ssh.Session, error) {
	config := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // 生产环境应该验证host key
		Timeout:         10 * time.Second,
	}

	// 连接SSH服务器
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, nil, fmt.Errorf("SSH连接失败: %w", err)
	}

	// 创建会话
	session, err := client.NewSession()
	if err != nil {
		client.Close()
		return nil, nil, fmt.Errorf("创建SSH会话失败: %w", err)
	}

	return client, session, nil
}
