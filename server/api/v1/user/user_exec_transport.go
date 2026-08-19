package user

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	providerModel "oneclickvirt/model/provider"
	agentService "oneclickvirt/service/agent"
	"oneclickvirt/utils"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
)

const userExecSessionTimeout = 30 * time.Minute

// userExecWriter serializes WebSocket writes. Agent/local exec can emit an
// error while its output goroutine is still forwarding terminal bytes.
type userExecWriter struct {
	mu sync.Mutex
	ws *websocket.Conn
}

func (w *userExecWriter) write(ctx context.Context, messageType int, data []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return w.ws.WriteMessage(messageType, data)
}

type userExecControlMessage struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

func parseUserExecControl(message []byte) (userExecControlMessage, bool) {
	var control userExecControlMessage
	if json.Unmarshal(message, &control) != nil || control.Type == "" {
		return userExecControlMessage{}, false
	}
	return control, true
}

func waitForAgentExecConn(providerID uint, timeout time.Duration) *agentService.AgentConn {
	hub := agentService.GetHub()
	deadline := time.Now().Add(timeout)
	delay := 200 * time.Millisecond
	for {
		if conn, ok := hub.GetConn(providerID); ok && conn != nil {
			return conn
		}
		if time.Now().After(deadline) {
			return nil
		}
		time.Sleep(delay)
		if delay < 2*time.Second {
			delay *= 2
			if delay > 2*time.Second {
				delay = 2 * time.Second
			}
		}
	}
}

// handleAgentExecTerminal executes the provider-specific container command in
// an Agent PTY. The command is exec'd so a failed container command cannot
// leave the customer attached to a host-level shell.
func handleAgentExecTerminal(ws *websocket.Conn, providerID uint, command string) {
	conn := waitForAgentExecConn(providerID, 15*time.Second)
	if conn == nil {
		_ = ws.WriteMessage(websocket.TextMessage, []byte("节点 Agent 未连接，请稍后重试\r\n"))
		return
	}

	session, err := conn.StartShell(80, 24)
	if err != nil {
		_ = ws.WriteMessage(websocket.TextMessage, []byte("启动 Agent Exec 终端失败: "+err.Error()+"\r\n"))
		return
	}
	var closeOnce sync.Once
	closeSession := func() {
		closeOnce.Do(func() { _ = conn.CloseShell(session.ID) })
	}
	defer closeSession()

	ctx, cancel := context.WithTimeout(context.Background(), userExecSessionTimeout)
	defer cancel()
	writer := &userExecWriter{ws: ws}

	// Agent opens a host shell. `exec` replaces it with the container process;
	// the trailing exit closes the shell if command setup itself fails.
	if err := conn.WriteShellInput(session.ID, []byte("exec "+command+"; exit $?\n")); err != nil {
		_ = writer.write(ctx, websocket.TextMessage, []byte("启动容器 Exec 失败: "+err.Error()+"\r\n"))
		return
	}

	var sessionClosed atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			_, message, readErr := ws.ReadMessage()
			if readErr != nil {
				cancel()
				return
			}
			if control, ok := parseUserExecControl(message); ok {
				switch control.Type {
				case "resize":
					if control.Cols > 0 && control.Rows > 0 {
						_ = conn.ResizeShell(session.ID, control.Cols, control.Rows)
					}
					continue
				case "ping":
					continue
				}
			}
			if err := conn.WriteShellInput(session.ID, message); err != nil {
				_ = writer.write(ctx, websocket.TextMessage, []byte("\r\nAgent Exec 输入失败: "+err.Error()+"\r\n"))
				cancel()
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case data, ok := <-session.OutputCh:
				if !ok {
					sessionClosed.Store(true)
					cancel()
					return
				}
				if err := writer.write(ctx, websocket.BinaryMessage, data); err != nil {
					cancel()
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	<-ctx.Done()
	_ = ws.SetReadDeadline(time.Now())
	if !sessionClosed.Load() {
		closeSession()
	}
	wg.Wait()
}

// handleLocalExecTerminal provides the same interactive container terminal for
// providers deliberately configured to run on the controller host.
func handleLocalExecTerminal(ws *websocket.Conn, command string) {
	ctx, cancel := context.WithTimeout(context.Background(), userExecSessionTimeout)
	defer cancel()

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	cmd := exec.CommandContext(ctx, shell, "-c", "exec "+command)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
	if err != nil {
		_ = ws.WriteMessage(websocket.TextMessage, []byte("启动本机容器 Exec 失败: "+err.Error()+"\r\n"))
		return
	}
	processDone := make(chan error, 1)
	go func() { processDone <- cmd.Wait() }()
	processExited := false
	defer func() {
		_ = ptmx.Close()
		if !processExited && cmd.Process != nil {
			_ = cmd.Process.Kill()
			select {
			case <-processDone:
			case <-time.After(2 * time.Second):
			}
		}
	}()

	writer := &userExecWriter{ws: ws}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			_, message, readErr := ws.ReadMessage()
			if readErr != nil {
				cancel()
				return
			}
			if control, ok := parseUserExecControl(message); ok {
				switch control.Type {
				case "resize":
					if control.Cols > 0 && control.Rows > 0 {
						_ = pty.Setsize(ptmx, &pty.Winsize{Rows: uint16(control.Rows), Cols: uint16(control.Cols)})
					}
					continue
				case "ping":
					continue
				}
			}
			if _, err := ptmx.Write(message); err != nil {
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
			n, readErr := ptmx.Read(buf)
			if n > 0 {
				if err := writer.write(ctx, websocket.BinaryMessage, buf[:n]); err != nil {
					return
				}
			}
			if readErr != nil {
				return
			}
		}
	}()

	select {
	case <-ctx.Done():
	case <-processDone:
		processExited = true
		cancel()
	}
	_ = ws.SetReadDeadline(time.Now())
	_ = ptmx.Close()
	wg.Wait()
}

// handleSSHCommandTerminal bridges a fixed, server-generated provider command
// to an interactive browser terminal. It deliberately does not accept a
// browser-supplied command, preventing the console capability from becoming a
// general host shell.
func handleSSHCommandTerminal(ws *websocket.Conn, provider providerModel.Provider, command string) {
	ctx, cancel := context.WithTimeout(context.Background(), userExecSessionTimeout)
	defer cancel()
	writer := &userExecWriter{ws: ws}

	endpoint := provider.Endpoint
	if endpoint == "" {
		endpoint = provider.PortIP
	}
	host, port := utils.ParseEndpoint(endpoint, provider.SSHPort)
	if port <= 0 {
		port = 22
	}
	if host == "" || provider.Username == "" {
		_ = writer.write(ctx, websocket.TextMessage, []byte("节点缺少 SSH 地址或用户名，无法连接控制台\r\n"))
		return
	}

	var (
		client  *ssh.Client
		session *ssh.Session
		err     error
	)
	if provider.SSHKey != "" {
		client, session, err = utils.CreateSSHConnectionWithKey(host, port, provider.Username, provider.SSHKey)
	} else {
		client, session, err = utils.CreateSSHConnection(host, port, provider.Username, provider.Password)
	}
	if err != nil {
		_ = writer.write(ctx, websocket.TextMessage, []byte("连接节点 SSH 失败: "+err.Error()+"\r\n"))
		return
	}
	defer client.Close()
	defer session.Close()

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
		_ = writer.write(ctx, websocket.TextMessage, []byte("请求控制台 PTY 失败: "+err.Error()+"\r\n"))
		return
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		_ = writer.write(ctx, websocket.TextMessage, []byte("获取控制台输入流失败: "+err.Error()+"\r\n"))
		return
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = writer.write(ctx, websocket.TextMessage, []byte("获取控制台输出流失败: "+err.Error()+"\r\n"))
		return
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		_ = writer.write(ctx, websocket.TextMessage, []byte("获取控制台错误流失败: "+err.Error()+"\r\n"))
		return
	}
	if err := session.Start(command); err != nil {
		_ = writer.write(ctx, websocket.TextMessage, []byte("启动宿主机控制台失败: "+err.Error()+"\r\n"))
		return
	}

	var (
		finishOnce sync.Once
		wg         sync.WaitGroup
		outputWG   sync.WaitGroup
	)
	done := make(chan struct{})
	finish := func() {
		finishOnce.Do(func() {
			close(done)
			cancel()
		})
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer finish()
		for {
			_, message, readErr := ws.ReadMessage()
			if readErr != nil {
				return
			}
			if control, ok := parseUserExecControl(message); ok {
				switch control.Type {
				case "resize":
					if control.Cols > 0 && control.Rows > 0 {
						_ = session.WindowChange(control.Rows, control.Cols)
					}
					continue
				case "ping":
					continue
				}
			}
			if _, writeErr := stdin.Write(message); writeErr != nil {
				return
			}
		}
	}()

	forward := func(stream io.Reader) {
		defer wg.Done()
		defer outputWG.Done()
		buffer := make([]byte, 8192)
		for {
			n, readErr := stream.Read(buffer)
			if n > 0 {
				if writeErr := writer.write(ctx, websocket.BinaryMessage, buffer[:n]); writeErr != nil {
					return
				}
			}
			if readErr != nil {
				// An individual stdout/stderr pipe can reach EOF before the
				// interactive serial command exits. session.Wait is the lifecycle
				// authority; only unexpected read errors should tear it down.
				if readErr != io.EOF {
					finish()
				}
				return
			}
		}
	}
	outputDone := make(chan struct{})
	outputWG.Add(2)
	wg.Add(2)
	go forward(stdout)
	go forward(stderr)
	go func() {
		outputWG.Wait()
		close(outputDone)
	}()
	waitResult := make(chan error, 1)
	go func() {
		waitResult <- session.Wait()
	}()

	remoteExited := false
	select {
	case <-done:
	case <-ctx.Done():
	case <-waitResult:
		remoteExited = true
		// Wait reports command completion, but it does not promise that the
		// browser-facing readers have consumed their final buffered bytes yet.
		select {
		case <-outputDone:
		case <-time.After(2 * time.Second):
		}
		finish()
	}
	_ = session.Close()
	_ = client.Close()
	_ = ws.SetReadDeadline(time.Now())

	waitDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(waitDone)
	}()
	select {
	case <-waitDone:
	case <-time.After(3 * time.Second):
	}
	if !remoteExited {
		select {
		case <-waitResult:
		case <-time.After(3 * time.Second):
		}
	}
}

func execTransportError(connectionType string) error {
	return fmt.Errorf("节点连接方式 %q 不支持容器 Exec", connectionType)
}
