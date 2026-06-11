package user

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"oneclickvirt/constant"
	"oneclickvirt/global"
	"oneclickvirt/model/common"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/utils"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
	"gorm.io/gorm"
)

// getExecCommand returns the interactive exec command for a given provider type and instance name
func getExecCommand(providerType constant.ProviderType, instanceName string) (string, error) {
	// Start in /root (fallback to /), then prefer bash when available, fall back to sh.
	const cdWrapper = `sh -c 'cd /root 2>/dev/null || cd /; if command -v bash >/dev/null 2>&1; then exec bash; fi; exec sh'`
	switch providerType {
	case constant.ProviderTypeDocker, constant.ProviderTypeOrbstack:
		return fmt.Sprintf("docker exec -it %s %s", instanceName, cdWrapper), nil
	case constant.ProviderTypePodman:
		return fmt.Sprintf("podman exec -it %s %s", instanceName, cdWrapper), nil
	case constant.ProviderTypeContainerd:
		// nerdctl exec for containerd
		return fmt.Sprintf("nerdctl exec -it %s %s", instanceName, cdWrapper), nil
	case constant.ProviderTypeLXD:
		return fmt.Sprintf("lxc exec %s -- %s", instanceName, cdWrapper), nil
	case constant.ProviderTypeIncus:
		return fmt.Sprintf("incus exec %s -- %s", instanceName, cdWrapper), nil
	default:
		return "", fmt.Errorf("provider type %s does not support container exec", providerType)
	}
}

// ExecWebSocket handles WebSocket container exec connections
// @Summary WebSocket Container Exec
// @Description 通过WebSocket建立到容器的交互式终端连接(exec)
// @Tags 用户/实例
// @Accept json
// @Produce json
// @Param id path uint true "实例ID"
// @Success 101 {string} string "Switching Protocols"
// @Failure 400 {object} common.Response "请求参数错误"
// @Failure 401 {object} common.Response "未授权"
// @Failure 404 {object} common.Response "实例不存在"
// @Failure 500 {object} common.Response "服务器错误"
// @Router /user/instances/{id}/exec [get]
func ExecWebSocket(c *gin.Context) {
	userIDInterface, exists := c.Get("user_id")
	if !exists {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "未授权"))
		return
	}
	userID := userIDInterface.(uint)

	instanceID := c.Param("id")
	if instanceID == "" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "实例ID不能为空"))
		return
	}

	// Get instance info
	var instance providerModel.Instance
	err := global.APP_DB.Select("id", "name", "provider_id", "status", "instance_type", "is_frozen", "frozen_reason", "expires_at").
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

	if instance.IsFrozen {
		common.ResponseWithError(c, common.NewError(common.CodeForbidden, "实例已被冻结，无法进入终端"))
		return
	}
	if instance.ExpiresAt != nil && instance.ExpiresAt.Before(time.Now()) {
		common.ResponseWithError(c, common.NewError(common.CodeForbidden, "实例已到期，无法进入终端"))
		return
	}

	if instance.Status != "running" {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "实例未运行"))
		return
	}

	// Only container-type instances support exec
	if instance.InstanceType == string(constant.InstanceTypeVM) {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "虚拟机实例不支持exec，请使用SSH"))
		return
	}

	// Get provider SSH credentials
	var provider providerModel.Provider
	if err := global.APP_DB.Select("id", "type", "endpoint", "ssh_port", "username", "password", "ssh_key", "ssh_connect_timeout", "ssh_execute_timeout").
		First(&provider, instance.ProviderID).Error; err != nil {
		global.APP_LOG.Error("查询节点失败", zap.Error(err))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "节点信息不可用"))
		return
	}

	// Build exec command
	execCmd, err := getExecCommand(constant.ProviderType(provider.Type), instance.Name)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}

	// SSH to provider host
	sshHost := provider.Endpoint
	sshPort := provider.SSHPort
	if sshPort == 0 {
		sshPort = 22
	}

	// Upgrade to WebSocket
	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		global.APP_LOG.Error("WebSocket升级失败", zap.Error(err))
		return
	}
	defer ws.Close()

	// Create SSH connection to provider host
	var sshClient *ssh.Client
	var session *ssh.Session
	if provider.SSHKey != "" {
		sshClient, session, err = utils.CreateSSHConnectionWithKey(sshHost, sshPort, provider.Username, provider.SSHKey)
	} else {
		sshClient, session, err = utils.CreateSSHConnection(sshHost, sshPort, provider.Username, provider.Password)
	}
	if err != nil {
		global.APP_LOG.Error("SSH连接到节点失败",
			zap.String("host", sshHost),
			zap.Int("port", sshPort),
			zap.Error(err))
		ws.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("连接到节点失败: %v\r\n", err)))
		return
	}

	// Set terminal modes
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
		ssh.ECHOCTL:       0,
		ssh.ECHOKE:        1,
		ssh.IGNCR:         0,
		ssh.ICRNL:         1,
		ssh.OPOST:         1,
		ssh.ONLCR:         1,
	}

	if err := session.RequestPty("xterm-256color", 24, 80, modes); err != nil {
		global.APP_LOG.Error("请求PTY失败", zap.Error(err))
		ws.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("请求PTY失败: %v\r\n", err)))
		return
	}

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

	// Start the exec command instead of shell
	if err := session.Start(execCmd); err != nil {
		global.APP_LOG.Error("启动exec命令失败",
			zap.String("cmd", utils.RedactSensitiveCommand(execCmd, 200)),
			zap.Error(err))
		ws.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("exec启动失败: %v\r\n", err)))
		return
	}

	global.APP_LOG.Info("容器exec会话已建立",
		zap.String("instance", instanceID),
		zap.String("cmd", utils.RedactSensitiveCommand(execCmd, 200)))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	done := make(chan bool, 1)
	errChan := make(chan error, 3)
	wg := &sync.WaitGroup{}

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
					if isBenignWebSocketOrSessionClose(err) {
						global.APP_LOG.Debug("WebSocket exec连接已关闭", zap.Error(err))
					} else {
						global.APP_LOG.Error("WebSocket读取失败", zap.Error(err))
					}
				}
				errChan <- err
				return
			}

			if messageType == websocket.TextMessage || messageType == websocket.BinaryMessage {
				if messageType == websocket.TextMessage {
					var msg map[string]interface{}
					if err := json.Unmarshal(message, &msg); err == nil {
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
						if msg["type"] == "ping" {
							continue
						}
					}
				}

				if _, err := sshIn.Write(message); err != nil {
					global.APP_LOG.Error("写入SSH失败", zap.Error(err))
					errChan <- err
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
				if err := ws.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
					global.APP_LOG.Error("写入WebSocket失败", zap.Error(err))
					errChan <- err
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
				if err := ws.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
					global.APP_LOG.Error("写入WebSocket失败", zap.Error(err))
					return
				}
			}
		}
	}()

	// Wait for completion or timeout
	select {
	case <-done:
		global.APP_LOG.Debug("WebSocket exec连接关闭")
	case <-ctx.Done():
		global.APP_LOG.Warn("WebSocket exec连接超时")
	case err := <-errChan:
		if err != nil && err != io.EOF {
			if isBenignWebSocketOrSessionClose(err) {
				global.APP_LOG.Debug("exec会话已关闭", zap.Error(err))
			} else {
				global.APP_LOG.Error("exec会话错误", zap.Error(err))
			}
		}
	}

	cancel()

	if session != nil {
		session.Close()
	}
	if sshClient != nil {
		sshClient.Close()
	}

	goroutineDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(goroutineDone)
	}()

	gracefulTimer := time.NewTimer(3 * time.Second)
	defer gracefulTimer.Stop()

	select {
	case <-goroutineDone:
		global.APP_LOG.Debug("WebSocket exec所有goroutine已正常退出")
	case <-gracefulTimer.C:
		global.APP_LOG.Error("WebSocket exec goroutine退出超时",
			zap.String("instance", instanceID))
	}
}
