package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

var adminUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // 在生产环境中应该进行更严格的检查
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
// @Router /v1/admin/instances/{id}/ssh [get]
func AdminSSHWebSocket(c *gin.Context) {
	// 获取实例ID
	instanceID := c.Param("id")
	if instanceID == "" {
		c.JSON(400, gin.H{"code": 400, "message": "实例ID不能为空"})
		return
	}

	// 获取实例信息（管理员可以访问任意实例）
	var instance providerModel.Instance
	err := global.APP_DB.Where("id = ?", instanceID).First(&instance).Error
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

	sshAddress := fmt.Sprintf("%s:%d", sshHost, sshPort)

	global.APP_LOG.Info("管理员SSH连接",
		zap.String("instanceID", instanceID),
		zap.String("instanceName", instance.Name),
		zap.String("sshAddress", sshAddress),
		zap.String("username", instance.Username),
	)

	// 升级到WebSocket
	ws, err := adminUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		global.APP_LOG.Error("WebSocket升级失败", zap.Error(err))
		return
	}
	defer ws.Close()

	// 建立SSH连接
	sshClient, sshSession, err := createAdminSSHConnection(
		sshAddress,
		instance.Username,
		instance.Password,
	)
	if err != nil {
		global.APP_LOG.Error("SSH连接失败",
			zap.Error(err),
			zap.String("address", sshAddress),
		)
		ws.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("SSH连接失败: %v\r\n", err)))
		return
	}
	defer sshClient.Close()
	defer sshSession.Close()

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

	// 请求伪终端
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}

	if err := sshSession.RequestPty("xterm-256color", 40, 120, modes); err != nil {
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

	// 创建通道用于协程通信
	done := make(chan struct{})

	// WebSocket -> SSH (处理用户输入)
	go func() {
		defer close(done)
		for {
			messageType, p, err := ws.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
					global.APP_LOG.Error("WebSocket读取错误", zap.Error(err))
				}
				return
			}

			if messageType == websocket.TextMessage {
				// 处理终端调整大小消息
				var msg map[string]interface{}
				if err := json.Unmarshal(p, &msg); err == nil {
					if msgType, ok := msg["type"].(string); ok && msgType == "resize" {
						if cols, ok := msg["cols"].(float64); ok {
							if rows, ok := msg["rows"].(float64); ok {
								sshSession.WindowChange(int(rows), int(cols))
								continue
							}
						}
					}
				}
			}

			// 发送数据到SSH
			if _, err := sshStdin.Write(p); err != nil {
				global.APP_LOG.Error("写入SSH stdin失败", zap.Error(err))
				return
			}
		}
	}()

	// SSH stdout -> WebSocket
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := sshStdout.Read(buf)
			if err != nil {
				if err != io.EOF {
					global.APP_LOG.Error("读取SSH stdout失败", zap.Error(err))
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

	// SSH stderr -> WebSocket
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := sshStderr.Read(buf)
			if err != nil {
				if err != io.EOF {
					global.APP_LOG.Error("读取SSH stderr失败", zap.Error(err))
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

	// 等待连接关闭
	<-done
	sshSession.Wait()

	global.APP_LOG.Info("管理员SSH会话结束",
		zap.String("instanceID", instanceID),
		zap.String("instanceName", instance.Name),
	)
}

// createAdminSSHConnection 创建管理员SSH连接
func createAdminSSHConnection(address, username, password string) (*ssh.Client, *ssh.Session, error) {
	config := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	client, err := ssh.Dial("tcp", address, config)
	if err != nil {
		return nil, nil, fmt.Errorf("SSH连接失败: %w", err)
	}

	session, err := client.NewSession()
	if err != nil {
		client.Close()
		return nil, nil, fmt.Errorf("创建SSH会话失败: %w", err)
	}

	return client, session, nil
}
