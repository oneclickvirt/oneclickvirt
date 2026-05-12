package agent

// tunnel.go — 控制端 TCP 端口转发（内网穿透）。
//
// 当 Port.MappingType == "controller" 时，由控制端在本地监听一个 TCP 端口，
// 并将流量通过已建立的 WebSocket AgentConn 以二进制帧转发到节点内容器的
// InternalHost:GuestPort。
//
// 协议：
//   - 控制端发送 JSON 文本帧 tunnel_open  { id, host, port }
//   - Agent 回复 JSON 文本帧 tunnel_ack   { id, ok, error? }
//   - 之后双向二进制帧 [8-byte connID][data] 承载 TCP 数据流
//   - 控制端发送 tunnel_close { id } 关闭连接

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"oneclickvirt/global"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// ──────────────────────────────────────────────────────────────────────────────
// 协议帧定义
// ──────────────────────────────────────────────────────────────────────────────

const (
	msgTypeTunnelOpen  = "tunnel_open"  // 控制端 → Agent: 请求打开隧道
	msgTypeTunnelAck   = "tunnel_ack"   // Agent → 控制端: 确认或拒绝
	msgTypeTunnelClose = "tunnel_close" // 双向: 关闭隧道
	msgTypeTunnelData  = "tunnel_data"  // 已废弃，使用二进制帧
)

type tunnelOpenPayload struct {
	ConnID string `json:"id"`
	Host   string `json:"host"`
	Port   int    `json:"port"`
}

type tunnelAckPayload struct {
	ConnID string `json:"id"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
}

type tunnelClosePayload struct {
	ConnID string `json:"id"`
}

// ──────────────────────────────────────────────────────────────────────────────
// TunnelSession — 控制端侧的单条 TCP 隧道会话
// ──────────────────────────────────────────────────────────────────────────────

type TunnelSession struct {
	connID   string
	connHash uint64      // hashString(connID)，用于二进制帧路由
	client   net.Conn    // 来自最终用户的 TCP 连接
	sendCh   chan []byte // 待写入 client 的数据（来自 Agent）
	ackCh    chan bool   // 等待 tunnel_ack 的通道
	done     chan struct{}
}

// ──────────────────────────────────────────────────────────────────────────────
// TunnelManager — 管理某个 Provider/AgentConn 上的所有隧道会话
// ──────────────────────────────────────────────────────────────────────────────

type TunnelManager struct {
	ac        *AgentConn
	mu        sync.RWMutex
	sessions  map[string]*TunnelSession // connID → session
	hashIndex map[uint64]*TunnelSession // connHash → session（快速路由二进制帧）
}

// NewTunnelManager 创建隧道管理器，需要注入已连接的 AgentConn。
func NewTunnelManager(ac *AgentConn) *TunnelManager {
	return &TunnelManager{
		ac:        ac,
		sessions:  make(map[string]*TunnelSession),
		hashIndex: make(map[uint64]*TunnelSession),
	}
}

// HandleControllerPort 在控制端监听 listenAddr 并将连接转发到 agent 侧的 targetHost:targetPort。
// 应在 goroutine 中调用，直到 stopCh 关闭才退出。
func (tm *TunnelManager) HandleControllerPort(listenAddr string, targetHost string, targetPort int, stopCh <-chan struct{}) error {
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("控制端监听 %s 失败: %w", listenAddr, err)
	}

	go func() {
		<-stopCh
		ln.Close()
	}()

	global.APP_LOG.Info("控制端端口转发已启动",
		zap.String("listen", listenAddr),
		zap.String("target", fmt.Sprintf("%s:%d", targetHost, targetPort)))

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-stopCh:
				return nil
			default:
			}
			global.APP_LOG.Warn("Accept 失败", zap.Error(err))
			continue
		}
		go tm.handleConn(conn, targetHost, targetPort)
	}
}

func (tm *TunnelManager) handleConn(client net.Conn, targetHost string, targetPort int) {
	connID := randomID()
	connHash := hashString(connID)
	sess := &TunnelSession{
		connID:   connID,
		connHash: connHash,
		client:   client,
		sendCh:   make(chan []byte, 64),
		ackCh:    make(chan bool, 1),
		done:     make(chan struct{}),
	}

	tm.mu.Lock()
	tm.sessions[connID] = sess
	tm.hashIndex[connHash] = sess
	tm.mu.Unlock()

	defer func() {
		client.Close()
		select {
		case <-sess.done:
		default:
			close(sess.done)
		}
		tm.mu.Lock()
		delete(tm.sessions, connID)
		delete(tm.hashIndex, connHash)
		tm.mu.Unlock()
		// 通知 Agent 关闭隧道
		closePayload, _ := json.Marshal(tunnelClosePayload{ConnID: connID})
		closeMsg, _ := json.Marshal(wsMessage{Type: msgTypeTunnelClose, Payload: closePayload})
		tm.ac.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		tm.ac.conn.WriteMessage(websocket.TextMessage, closeMsg) //nolint:errcheck
	}()

	// 1. 发送 tunnel_open 给 Agent
	openPayload, _ := json.Marshal(tunnelOpenPayload{ConnID: connID, Host: targetHost, Port: targetPort})
	openMsg, _ := json.Marshal(wsMessage{Type: msgTypeTunnelOpen, Payload: openPayload})
	tm.ac.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := tm.ac.conn.WriteMessage(websocket.TextMessage, openMsg); err != nil {
		global.APP_LOG.Warn("发送 tunnel_open 失败", zap.Error(err))
		return
	}

	// 2. 等待 tunnel_ack（最长 10 秒）；由 hub.readLoop 通过 DeliverAck 注入
	select {
	case ok := <-sess.ackCh:
		if !ok {
			global.APP_LOG.Warn("tunnel_ack 返回失败", zap.String("connID", connID))
			return
		}
	case <-time.After(10 * time.Second):
		global.APP_LOG.Warn("等待 tunnel_ack 超时", zap.String("connID", connID))
		return
	}

	// 3. 双向数据转发
	// Client → Agent（读 TCP，写 WS 二进制帧，[8-byte hash][data]）
	header := make([]byte, 8)
	binary.BigEndian.PutUint64(header, connHash)
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := client.Read(buf)
			if n > 0 {
				frame := make([]byte, 8+n)
				copy(frame[:8], header)
				copy(frame[8:], buf[:n])
				tm.ac.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if werr := tm.ac.conn.WriteMessage(websocket.BinaryMessage, frame); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Agent → Client（从 sess.sendCh 写 TCP）
	for {
		select {
		case data := <-sess.sendCh:
			if _, err := client.Write(data); err != nil {
				return
			}
		case <-sess.done:
			return
		}
	}
}

// DeliverByHash 将 Agent 侧返回的二进制数据投递给对应会话（由 hub readLoop 调用）。
// key 是帧头中的 8-byte hash，对应 hashString(connID)。
func (tm *TunnelManager) DeliverByHash(connHash uint64, data []byte) {
	tm.mu.RLock()
	sess, ok := tm.hashIndex[connHash]
	tm.mu.RUnlock()
	if !ok {
		return
	}
	select {
	case sess.sendCh <- data:
	default:
		// 消费方太慢，丢弃
	}
}

// DeliverData 将 Agent 侧返回的二进制数据投递给对应会话（按 connID，兼容旧调用）。
func (tm *TunnelManager) DeliverData(connID string, data []byte) {
	tm.DeliverByHash(hashString(connID), data)
}

// DeliverAck 将 tunnel_ack 推送给等待的 handleConn goroutine。
func (tm *TunnelManager) DeliverAck(ack tunnelAckPayload) {
	tm.mu.RLock()
	sess, ok := tm.sessions[ack.ConnID]
	tm.mu.RUnlock()
	if !ok {
		return
	}
	select {
	case sess.ackCh <- ack.OK:
	default:
	}
}

// CloseSession 关闭指定会话（由 hub readLoop 在收到 tunnel_close 帧时调用）。
func (tm *TunnelManager) CloseSession(connID string) {
	tm.mu.RLock()
	sess, ok := tm.sessions[connID]
	tm.mu.RUnlock()
	if ok {
		sess.client.Close()
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// 全局 TunnelManager 注册表
// ──────────────────────────────────────────────────────────────────────────────

var (
	tunnelMgrMu sync.RWMutex
	tunnelMgrs  = make(map[uint]*TunnelManager) // providerID → TunnelManager
)

// GetOrCreateTunnelManager 获取或创建指定 Provider 的 TunnelManager。
func GetOrCreateTunnelManager(providerID uint) (*TunnelManager, error) {
	// 首先检查是否有活跃的 AgentConn
	hub := GetHub()
	ac, ok := hub.GetConn(providerID)
	if !ok {
		return nil, fmt.Errorf("provider %d 的 Agent 当前离线", providerID)
	}

	tunnelMgrMu.Lock()
	defer tunnelMgrMu.Unlock()

	if mgr, exists := tunnelMgrs[providerID]; exists {
		return mgr, nil
	}

	mgr := NewTunnelManager(ac)
	tunnelMgrs[providerID] = mgr
	return mgr, nil
}

// RemoveTunnelManager 移除指定 Provider 的 TunnelManager（Agent 断线时调用）。
func RemoveTunnelManager(providerID uint) {
	tunnelMgrMu.Lock()
	delete(tunnelMgrs, providerID)
	tunnelMgrMu.Unlock()
}

// hashString 将字符串 hash 为 uint64（用于帧头）。
func hashString(s string) uint64 {
	var h uint64 = 14695981039346656037
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

// ──────────────────────────────────────────────────────────────────────────────
// 控制端端口监听池：Port.MappingType == "controller" 时，系统启动时恢复监听
// ──────────────────────────────────────────────────────────────────────────────

type controllerListener struct {
	listenPort int
	stopCh     chan struct{}
}

var (
	ctrlListenerMu sync.RWMutex
	ctrlListeners  = make(map[uint]*controllerListener) // Port.ID → listener
)

// StartControllerPortForward 为一条 Port 记录启动控制端 TCP 监听转发。
func StartControllerPortForward(portID uint, providerID uint, listenPort int, targetHost string, targetPort int) error {
	ctrlListenerMu.Lock()
	defer ctrlListenerMu.Unlock()

	if _, exists := ctrlListeners[portID]; exists {
		return nil // 已在运行
	}

	mgr, err := GetOrCreateTunnelManager(providerID)
	if err != nil {
		return err
	}

	stopCh := make(chan struct{})
	ctrlListeners[portID] = &controllerListener{listenPort: listenPort, stopCh: stopCh}

	go func() {
		addr := fmt.Sprintf("0.0.0.0:%d", listenPort)
		if err := mgr.HandleControllerPort(addr, targetHost, targetPort, stopCh); err != nil {
			global.APP_LOG.Error("控制端端口转发异常退出",
				zap.Uint("portID", portID), zap.Error(err))
		}
		ctrlListenerMu.Lock()
		delete(ctrlListeners, portID)
		ctrlListenerMu.Unlock()
	}()

	return nil
}

// StopControllerPortForward 停止指定 Port 的控制端监听。
func StopControllerPortForward(portID uint) {
	ctrlListenerMu.Lock()
	defer ctrlListenerMu.Unlock()

	if cl, ok := ctrlListeners[portID]; ok {
		close(cl.stopCh)
		delete(ctrlListeners, portID)
	}
}
