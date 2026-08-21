package admin

import (
	"crypto/des"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	agentService "oneclickvirt/service/agent"
	"oneclickvirt/utils"

	"github.com/gorilla/websocket"
)

const (
	proxmoxVNCSetupTimeout  = 15 * time.Second
	proxmoxVNCDialTimeout   = 5 * time.Second
	proxmoxVNCProxyAttempts = 2
)

type proxmoxVNCProxyResponse struct {
	Port     json.RawMessage `json:"port"`
	Password string          `json:"password"`
	Ticket   string          `json:"ticket"`
}

type proxmoxVNCProxyCredentials struct {
	port     int
	password string
}

// proxmoxVNCProxyCommand asks the local PVE API for a single short-lived VNC
// proxy. The JSON response never leaves the controller: it contains the VNC
// password and ticket, both of which are intentionally kept server-side.
func proxmoxVNCProxyCommand(node, runtimeID string) (string, error) {
	vmid, err := strconv.ParseInt(strings.TrimSpace(runtimeID), 10, 64)
	if err != nil || vmid <= 0 {
		return "", fmt.Errorf("PVE VMID 无效")
	}
	node = strings.TrimSpace(node)
	if node == "" {
		return fmt.Sprintf("pvesh create \"/nodes/$(hostname)/qemu/%d/vncproxy\" --websocket 1 --output-format json", vmid), nil
	}
	for _, char := range node {
		if !(char >= 'a' && char <= 'z') && !(char >= 'A' && char <= 'Z') && !(char >= '0' && char <= '9') && char != '.' && char != '-' && char != '_' {
			return "", fmt.Errorf("PVE 节点名包含不支持的字符")
		}
	}
	return fmt.Sprintf("pvesh create %s --websocket 1 --output-format json", utils.ShellSingleQuote(fmt.Sprintf("/nodes/%s/qemu/%d/vncproxy", node, vmid))), nil
}

func parseProxmoxVNCProxyResponse(raw string) (proxmoxVNCProxyCredentials, error) {
	var response proxmoxVNCProxyResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &response); err != nil {
		return proxmoxVNCProxyCredentials{}, fmt.Errorf("PVE VNC 代理响应格式无效")
	}
	port, err := parseProxmoxVNCProxyPort(response.Port)
	if err != nil || port <= 0 || port > 65535 {
		return proxmoxVNCProxyCredentials{}, fmt.Errorf("PVE VNC 代理未返回有效端口")
	}
	// The PVE API calls this short-lived VNC credential a ticket. Some
	// compatible wrappers return password instead, so accept both without ever
	// sending either value to the browser.
	password := strings.TrimSpace(response.Password)
	if password == "" {
		password = strings.TrimSpace(response.Ticket)
	}
	if password == "" {
		return proxmoxVNCProxyCredentials{}, fmt.Errorf("PVE VNC 代理未返回认证信息")
	}
	return proxmoxVNCProxyCredentials{port: port, password: password}, nil
}

func parseProxmoxVNCProxyPort(raw json.RawMessage) (int, error) {
	value := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	port, err := strconv.Atoi(value)
	if err != nil {
		return 0, err
	}
	return port, nil
}

func openProxmoxVNCConn(target consoleTarget) (net.Conn, func(), string, error) {
	node := strings.TrimSpace(target.runtimeNode)
	// An empty runtime node deliberately lets proxmoxVNCProxyCommand use the
	// executing PVE node's hostname. Provider.HostName can be an IP/FQDN and is
	// not necessarily the cluster node name accepted by pvesh.
	command, err := proxmoxVNCProxyCommand(node, target.runtimeID)
	if err != nil {
		return nil, nil, "", err
	}
	executor, executorCleanup, err := newConsoleExecutor(target.provider)
	if err != nil {
		return nil, nil, "", err
	}
	output, err := executor.ExecuteWithTimeout(command, proxmoxVNCSetupTimeout)
	if err != nil {
		executorCleanup()
		return nil, nil, "", fmt.Errorf("PVE VNC 代理创建失败: %w", err)
	}
	credentials, err := parseProxmoxVNCProxyResponse(output)
	if err != nil {
		executorCleanup()
		return nil, nil, "", err
	}

	var conn net.Conn
	dial := func() (net.Conn, error) {
		switch target.transport {
		case "agent":
			return agentService.OpenTunnelConn(target.provider.ID, "127.0.0.1", credentials.port)
		case "ssh":
			client, ok := executor.(*utils.SSHClient)
			if !ok {
				return nil, fmt.Errorf("SSH PVE 控制台传输初始化失败")
			}
			return client.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(credentials.port)))
		case "local":
			return net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(credentials.port)), proxmoxVNCDialTimeout)
		default:
			return nil, fmt.Errorf("PVE VNC 不支持节点连接方式 %q", target.transport)
		}
	}
	for attempt := 0; attempt < 3; attempt++ {
		conn, err = dial()
		if err == nil {
			break
		}
		time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
	}
	if err != nil {
		executorCleanup()
		return nil, nil, "", fmt.Errorf("连接 PVE VNC 代理失败: %w", err)
	}
	return conn, func() {
		_ = conn.Close()
		executorCleanup()
	}, credentials.password, nil
}

type proxmoxVNCConnOpener func(consoleTarget) (net.Conn, func(), string, error)
type proxmoxVNCAuthenticator func(net.Conn, string) error

// openAuthenticatedProxmoxVNCConn keeps one short retry around the whole PVE
// proxy lifecycle. PVE creates an on-demand local proxy for every session;
// after a recent probe or viewer disconnect its listener can briefly accept a
// TCP tunnel before the upstream QEMU VNC socket is ready. Retrying a fresh
// proxy prevents that transient state from being presented as an unavailable
// capability, while permanent ACL/configuration errors still fail immediately.
func openAuthenticatedProxmoxVNCConn(target consoleTarget) (net.Conn, func(), error) {
	return openAuthenticatedProxmoxVNCConnWithRetry(
		target,
		proxmoxVNCProxyAttempts,
		openProxmoxVNCConn,
		authenticateProxmoxVNC,
		func(attempt int) { time.Sleep(time.Duration(attempt+1) * 200 * time.Millisecond) },
	)
}

func openAuthenticatedProxmoxVNCConnWithRetry(
	target consoleTarget,
	attempts int,
	opener proxmoxVNCConnOpener,
	authenticator proxmoxVNCAuthenticator,
	pause func(int),
) (net.Conn, func(), error) {
	if attempts < 1 {
		attempts = 1
	}
	if opener == nil || authenticator == nil {
		return nil, nil, fmt.Errorf("PVE VNC 连接器未初始化")
	}

	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		conn, cleanup, password, err := opener(target)
		if err == nil && conn == nil {
			err = fmt.Errorf("PVE VNC 代理未返回连接")
		}
		if err == nil {
			err = authenticator(conn, password)
		}
		if err == nil {
			return conn, cleanup, nil
		}
		if cleanup != nil {
			cleanup()
		}
		lastErr = err
		if attempt+1 >= attempts || !isRetryableProxmoxVNCError(err) {
			break
		}
		if pause != nil {
			pause(attempt)
		}
	}
	return nil, nil, fmt.Errorf("PVE VNC 临时代理握手失败: %w", lastErr)
}

func isRetryableProxmoxVNCError(err error) bool {
	if err == nil {
		return false
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) && (networkErr.Timeout() || networkErr.Temporary()) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"connection refused", "connection reset", "broken pipe", "i/o timeout",
		"unexpected eof", " eof", "no route to host",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

// probeProxmoxVNCConnection performs the provider-side VNC setup and the
// password/RFB negotiation, then closes the short-lived proxy. It is used by
// the capability endpoint only; no browser WebSocket or framebuffer session is
// kept open. Keeping this separate from the browser proxy also lets the UI
// display a precise diagnostic while still requiring an explicit protocol
// selection before a user session is opened.
func probeProxmoxVNCConnection(target consoleTarget) (bool, string) {
	_, cleanup, err := openAuthenticatedProxmoxVNCConn(target)
	if err != nil {
		return false, err.Error()
	}
	defer cleanup()
	return true, ""
}

// proxyProxmoxVNCWebSocket terminates PVE's password-protected VNC handshake
// on the controller. The browser receives a no-auth RFB handshake, while the
// short-lived PVE password remains exclusively in server memory.
func proxyProxmoxVNCWebSocket(panel *websocket.Conn, target consoleTarget) {
	conn, cleanup, err := openAuthenticatedProxmoxVNCConn(target)
	if err != nil {
		writeVNCWebSocketFailure(panel, "PVE VNC 连接失败: "+err.Error())
		return
	}
	defer cleanup()

	client := &consoleWebSocketReader{ws: panel}
	if err := establishBrowserVNCHandshake(panel, client); err != nil {
		// The RFB exchange has already begun, so sending a second failure
		// handshake would violate the protocol. Keep this diagnostic server-side.
		writeConsoleSocketError(panel, "浏览器 VNC 握手失败: "+err.Error())
		return
	}
	proxyConsoleByteStreams(panel, client, conn)
}

func writeConsoleSocketError(panel *websocket.Conn, message string) {
	_ = panel.WriteMessage(websocket.TextMessage, []byte(utils.TruncateString(message, 1200)))
}

// writeVNCWebSocketFailure speaks the RFB security-failure sequence instead
// of a text WebSocket frame that noVNC cannot interpret. The browser can then
// display the server diagnostic from its securityfailure event.
func writeVNCWebSocketFailure(panel *websocket.Conn, message string) {
	reason := []byte(utils.TruncateString(strings.TrimSpace(message), 1200))
	if len(reason) == 0 {
		reason = []byte("VNC 控制台连接失败")
	}
	_ = panel.SetReadDeadline(time.Now().Add(proxmoxVNCSetupTimeout))
	defer panel.SetReadDeadline(time.Time{})
	if err := panel.WriteMessage(websocket.BinaryMessage, []byte("RFB 003.008\n")); err != nil {
		return
	}
	client := &consoleWebSocketReader{ws: panel}
	version := make([]byte, 12)
	if _, err := io.ReadFull(client, version); err != nil {
		return
	}
	major, _, err := parseRFBVersion(version)
	if err != nil || major != 3 {
		return
	}
	failure := make([]byte, 5+len(reason))
	failure[0] = 0
	binary.BigEndian.PutUint32(failure[1:5], uint32(len(reason)))
	copy(failure[5:], reason)
	_ = panel.WriteMessage(websocket.BinaryMessage, failure)
}

func authenticateProxmoxVNC(conn net.Conn, password string) error {
	_ = conn.SetDeadline(time.Now().Add(proxmoxVNCSetupTimeout))
	defer conn.SetDeadline(time.Time{})

	version := make([]byte, 12)
	if _, err := io.ReadFull(conn, version); err != nil {
		return fmt.Errorf("读取 RFB 版本失败: %w", err)
	}
	major, minor, err := parseRFBVersion(version)
	if err != nil {
		return err
	}
	if major != 3 {
		return fmt.Errorf("PVE 返回不支持的 RFB 主版本 %d", major)
	}
	if _, err := conn.Write([]byte("RFB 003.008\n")); err != nil {
		return fmt.Errorf("发送 RFB 版本失败: %w", err)
	}

	securityType := byte(0)
	if minor <= 3 {
		var rawType uint32
		if err := binary.Read(conn, binary.BigEndian, &rawType); err != nil {
			return fmt.Errorf("读取 RFB 认证类型失败: %w", err)
		}
		securityType = byte(rawType)
	} else {
		var count [1]byte
		if _, err := io.ReadFull(conn, count[:]); err != nil {
			return fmt.Errorf("读取 RFB 认证类型失败: %w", err)
		}
		if count[0] == 0 {
			return readRFBFailure(conn, "PVE VNC 未提供可用认证方式")
		}
		types := make([]byte, int(count[0]))
		if _, err := io.ReadFull(conn, types); err != nil {
			return fmt.Errorf("读取 RFB 认证列表失败: %w", err)
		}
		securityType = chooseRFBSecurityType(types)
		if securityType == 0 {
			return fmt.Errorf("PVE VNC 未提供可用认证方式")
		}
		if _, err := conn.Write([]byte{securityType}); err != nil {
			return fmt.Errorf("选择 RFB 认证方式失败: %w", err)
		}
	}

	switch securityType {
	case 1:
		// None is accepted by some explicitly configured PVE VNC backends.
	case 2:
		if err := respondToVNCChallenge(conn, password); err != nil {
			return err
		}
	default:
		return fmt.Errorf("PVE VNC 使用了未支持的认证方式 %d", securityType)
	}
	// RFB 3.3 omits SecurityResult only for the historical no-auth path. VNC
	// password authentication still returns a result word after the challenge.
	if minor >= 7 || securityType == 2 {
		var result uint32
		if err := binary.Read(conn, binary.BigEndian, &result); err != nil {
			return fmt.Errorf("读取 RFB 认证结果失败: %w", err)
		}
		if result != 0 {
			return readRFBFailure(conn, "PVE VNC 认证被拒绝")
		}
	}
	return nil
}

func parseRFBVersion(raw []byte) (int, int, error) {
	if len(raw) != 12 || !strings.HasPrefix(string(raw), "RFB ") || raw[11] != '\n' {
		return 0, 0, fmt.Errorf("PVE 返回了无效的 RFB 版本")
	}
	major, majorErr := strconv.Atoi(string(raw[4:7]))
	minor, minorErr := strconv.Atoi(string(raw[8:11]))
	if majorErr != nil || minorErr != nil {
		return 0, 0, fmt.Errorf("PVE 返回了无效的 RFB 版本")
	}
	return major, minor, nil
}

func chooseRFBSecurityType(types []byte) byte {
	for _, securityType := range types {
		if securityType == 2 {
			return securityType
		}
	}
	for _, securityType := range types {
		if securityType == 1 {
			return securityType
		}
	}
	return 0
}

func respondToVNCChallenge(conn net.Conn, password string) error {
	challenge := make([]byte, 16)
	if _, err := io.ReadFull(conn, challenge); err != nil {
		return fmt.Errorf("读取 PVE VNC 认证挑战失败: %w", err)
	}
	response, err := vncChallengeResponse(challenge, password)
	if err != nil {
		return err
	}
	if _, err := conn.Write(response); err != nil {
		return fmt.Errorf("发送 PVE VNC 认证响应失败: %w", err)
	}
	return nil
}

func vncChallengeResponse(challenge []byte, password string) ([]byte, error) {
	if len(challenge) != 16 {
		return nil, fmt.Errorf("VNC 认证挑战长度无效")
	}
	key := make([]byte, 8)
	copy(key, []byte(password))
	for index := range key {
		key[index] = reverseVNCBits(key[index])
	}
	cipher, err := des.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("初始化 VNC 认证失败: %w", err)
	}
	response := make([]byte, len(challenge))
	for offset := 0; offset < len(challenge); offset += des.BlockSize {
		cipher.Encrypt(response[offset:offset+des.BlockSize], challenge[offset:offset+des.BlockSize])
	}
	return response, nil
}

func reverseVNCBits(value byte) byte {
	value = (value&0x55)<<1 | (value>>1)&0x55
	value = (value&0x33)<<2 | (value>>2)&0x33
	return (value&0x0f)<<4 | (value>>4)&0x0f
}

func readRFBFailure(conn io.Reader, fallback string) error {
	var length uint32
	if err := binary.Read(conn, binary.BigEndian, &length); err != nil || length == 0 || length > 1024 {
		return fmt.Errorf("%s", fallback)
	}
	reason := make([]byte, length)
	if _, err := io.ReadFull(conn, reason); err != nil {
		return fmt.Errorf("%s", fallback)
	}
	clean := strings.TrimSpace(string(reason))
	if clean == "" {
		return fmt.Errorf("%s", fallback)
	}
	return fmt.Errorf("%s: %s", fallback, utils.TruncateString(clean, 600))
}

type consoleWebSocketReader struct {
	ws       *websocket.Conn
	buffered []byte
}

func (r *consoleWebSocketReader) Read(destination []byte) (int, error) {
	for len(r.buffered) == 0 {
		messageType, message, err := r.ws.ReadMessage()
		if err != nil {
			return 0, err
		}
		if messageType != websocket.BinaryMessage {
			return 0, fmt.Errorf("浏览器发送了非二进制 VNC 数据")
		}
		r.buffered = message
	}
	count := copy(destination, r.buffered)
	r.buffered = r.buffered[count:]
	return count, nil
}

func establishBrowserVNCHandshake(panel *websocket.Conn, client *consoleWebSocketReader) error {
	_ = panel.SetReadDeadline(time.Now().Add(proxmoxVNCSetupTimeout))
	defer panel.SetReadDeadline(time.Time{})
	if err := panel.WriteMessage(websocket.BinaryMessage, []byte("RFB 003.008\n")); err != nil {
		return err
	}
	version := make([]byte, 12)
	if _, err := io.ReadFull(client, version); err != nil {
		return err
	}
	major, _, err := parseRFBVersion(version)
	if err != nil || major != 3 {
		return fmt.Errorf("浏览器没有协商兼容的 RFB 版本")
	}
	if err := panel.WriteMessage(websocket.BinaryMessage, []byte{1, 1}); err != nil {
		return err
	}
	selected := make([]byte, 1)
	if _, err := io.ReadFull(client, selected); err != nil {
		return err
	}
	if selected[0] != 1 {
		return fmt.Errorf("浏览器未选择无密码的内部 VNC 认证")
	}
	return panel.WriteMessage(websocket.BinaryMessage, []byte{0, 0, 0, 0})
}

func proxyConsoleByteStreams(panel *websocket.Conn, client io.Reader, upstream io.ReadWriter) {
	done := make(chan struct{}, 2)
	go func() {
		defer func() { done <- struct{}{} }()
		buffer := make([]byte, 64*1024)
		for {
			count, err := client.Read(buffer)
			if count > 0 {
				if _, writeErr := upstream.Write(buffer[:count]); writeErr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		defer func() { done <- struct{}{} }()
		buffer := make([]byte, 64*1024)
		for {
			count, err := upstream.Read(buffer)
			if count > 0 {
				if writeErr := panel.WriteMessage(websocket.BinaryMessage, buffer[:count]); writeErr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	<-done
}
