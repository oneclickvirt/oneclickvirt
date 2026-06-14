package agent

// tunnel_manager.go — TunnelManager 结构体、构造函数、会话管理及全局注册表。

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"sync"
	"time"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

// ──────────────────────────────────────────────────────────────────────────────
// TunnelSession — 控制端侧的单条 TCP 隧道会话
// ──────────────────────────────────────────────────────────────────────────────

type TunnelSession struct {
	connID   string
	connHash uint64                // hashString(connID)，用于二进制帧路由
	client   net.Conn              // 来自最终用户的 TCP 连接
	sendCh   chan []byte           // 待写入 client 的数据（来自 Agent）
	ackCh    chan tunnelAckPayload // 等待 tunnel_ack 的通道
	activity chan struct{}
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

	var listenerConns sync.Map
	defer func() {
		listenerConns.Range(func(key, _ any) bool {
			if conn, ok := key.(net.Conn); ok {
				_ = conn.Close()
			}
			return true
		})
	}()

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
		listenerConns.Store(conn, struct{}{})
		go func() {
			defer listenerConns.Delete(conn)
			tm.handleConn(conn, targetHost, targetPort)
		}()
	}
}

func (tm *TunnelManager) handleConn(client net.Conn, targetHost string, targetPort int) {
	normalizedHost, ok := validateTunnelTarget(targetHost, targetPort)
	targetHost = normalizedHost
	if !ok {
		global.APP_LOG.Warn("拒绝创建隧道会话：目标参数无效",
			zap.Uint("providerID", tm.ac.ProviderID),
			zap.String("targetHost", targetHost),
			zap.Int("targetPort", targetPort))
		_ = client.Close()
		return
	}

	connID := ""
	connHash := uint64(0)

	tm.mu.Lock()
	for attempt := 0; attempt < 8; attempt++ {
		candidateID := randomID()
		candidateHash := hashString(candidateID)
		if _, exists := tm.hashIndex[candidateHash]; exists {
			continue
		}
		connID = candidateID
		connHash = candidateHash
		break
	}
	if connID == "" {
		tm.mu.Unlock()
		global.APP_LOG.Warn("创建隧道会话失败：无法分配唯一 connHash", zap.Uint("providerID", tm.ac.ProviderID))
		_ = client.Close()
		return
	}

	sess := &TunnelSession{
		connID:   connID,
		connHash: connHash,
		client:   client,
		sendCh:   make(chan []byte, 64),
		ackCh:    make(chan tunnelAckPayload, 1),
		activity: make(chan struct{}, 1),
		done:     make(chan struct{}),
	}

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
		// 通知 Agent 关闭隧道（使用短超时，避免阻塞新隧道建立）
		closePayload, _ := json.Marshal(tunnelClosePayload{ConnID: connID})
		closeMsg, _ := json.Marshal(wsMessage{Type: msgTypeTunnelClose, Payload: closePayload})
		_ = tm.ac.writeTextMessage(closeMsg, 2*time.Second)
	}()

	// 1. 发送 tunnel_open 给 Agent（幂等重试，使用同一个 connID）
	openPayload, _ := json.Marshal(tunnelOpenPayload{ConnID: connID, Host: targetHost, Port: targetPort})
	openMsg, _ := json.Marshal(wsMessage{Type: msgTypeTunnelOpen, Payload: openPayload})
	_, openErr := sendTunnelOpenWithRetry(connID, func() error {
		return tm.ac.writeTextMessage(openMsg, 10*time.Second)
	}, sess.ackCh, tunnelOpenAckAttempts, tunnelOpenAckTimeout)
	if openErr != nil {
		global.APP_LOG.Warn("tunnel_open 握手失败",
			zap.String("connID", connID),
			zap.String("target", fmt.Sprintf("%s:%d", targetHost, targetPort)),
			zap.Error(openErr))
		return
	}

	// 3. 双向数据转发
	idleTimer := time.NewTimer(tunnelSessionIdleTimeout)
	defer idleTimer.Stop()
	notifyActivity := func() {
		select {
		case sess.activity <- struct{}{}:
		default:
		}
	}

	go func() {
		ticker := time.NewTicker(tunnelKeepaliveInterval)
		defer ticker.Stop()
		for {
			select {
			case <-sess.done:
				return
			case <-ticker.C:
				payload, _ := json.Marshal(tunnelKeepalivePayload{ConnID: connID})
				msg, _ := json.Marshal(wsMessage{Type: msgTypeTunnelKeepalive, Payload: payload})
				if err := tm.ac.writeTextMessage(msg, 2*time.Second); err != nil {
					return
				}
				notifyActivity()
			}
		}
	}()

	// Client → Agent（读 TCP，写 WS 二进制帧，[8-byte hash][data]）
	//
	// Anti-DPI: vary read buffer size (8KB-64KB) and add occasional
	// sub-millisecond delays to break fixed-size / fixed-interval patterns.
	header := make([]byte, 8)
	binary.BigEndian.PutUint64(header, connHash)
	go func() {
		for {
			_ = client.SetReadDeadline(time.Now().Add(tunnelSessionIdleTimeout))
			bufSize := 8192 + rand.Intn(57344) // 8KB - 64KB random
			buf := make([]byte, bufSize)
			n, err := client.Read(buf)
			if n > 0 {
				notifyActivity()
				frame := make([]byte, 8+n)
				copy(frame[:8], header)
				copy(frame[8:], buf[:n])
				if werr := tm.ac.writeBinaryMessage(frame, 10*time.Second); werr != nil {
					return
				}
				// Occasional micro-delay (0-3ms, ~20% probability) to
				// break perfect timing patterns
				if rand.Intn(5) == 0 {
					time.Sleep(time.Duration(rand.Intn(3000)) * time.Microsecond)
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
		case <-sess.activity:
			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			idleTimer.Reset(tunnelSessionIdleTimeout)
		case data, ok := <-sess.sendCh:
			if !ok {
				return
			}
			notifyActivity()
			if _, err := client.Write(data); err != nil {
				return
			}
		case <-idleTimer.C:
			global.APP_LOG.Info("隧道会话空闲超时，主动关闭",
				zap.String("connID", connID),
				zap.Duration("idleTimeout", tunnelSessionIdleTimeout))
			return
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
		global.APP_LOG.Warn("隧道会话缓冲区已满，主动终止以避免静默丢包",
			zap.Uint("providerID", tm.ac.ProviderID),
			zap.Uint64("connHash", connHash))
		sess.client.Close()
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
		global.APP_LOG.Debug("丢弃未知隧道 ACK",
			zap.Uint("providerID", tm.ac.ProviderID),
			zap.String("connID", ack.ConnID),
			zap.Bool("ok", ack.OK))
		return
	}
	select {
	case sess.ackCh <- ack:
	default:
	}
}

func (tm *TunnelManager) TouchSession(connID string) {
	tm.mu.RLock()
	sess, ok := tm.sessions[connID]
	tm.mu.RUnlock()
	if !ok {
		return
	}
	select {
	case sess.activity <- struct{}{}:
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

// CloseAllSessions 关闭该 TunnelManager 中的所有活跃会话。
// 当监听器停止（HandleControllerPort 返回）时调用，释放所有 in-flight
// handleConn goroutine 持有的资源，防止它们在监听器停止后继续写入 WebSocket。
// 同时关闭 client 连接和 done 通道，确保 handleConn 主循环退出。
func (tm *TunnelManager) CloseAllSessions() {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	if len(tm.sessions) == 0 {
		return
	}

	global.APP_LOG.Debug("关闭所有隧道会话",
		zap.Uint("providerID", tm.ac.ProviderID),
		zap.Int("count", len(tm.sessions)))

	for _, sess := range tm.sessions {
		// 关闭 done 通道（触发 handleConn 主循环退出）
		select {
		case <-sess.done:
		default:
			close(sess.done)
		}
		// 关闭 client 连接（触发读/写 goroutine 退出）
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
	if hub == nil {
		return nil, fmt.Errorf("AgentHub 未初始化")
	}
	ac, ok := hub.GetConn(providerID)
	if !ok || ac == nil {
		return nil, fmt.Errorf("provider %d 的 Agent 当前离线或未连接", providerID)
	}

	tunnelMgrMu.Lock()
	defer tunnelMgrMu.Unlock()

	if mgr, exists := tunnelMgrs[providerID]; exists {
		// 检查现有的 TunnelManager 引用的 AgentConn 是否仍然有效
		if mgr.ac == ac {
			return mgr, nil
		}
		// AgentConn 已更换，需要重建 TunnelManager
		delete(tunnelMgrs, providerID)
	}

	mgr := NewTunnelManager(ac)
	if mgr == nil {
		return nil, fmt.Errorf("创建 TunnelManager 失败")
	}
	tunnelMgrs[providerID] = mgr
	return mgr, nil
}

// OpenTunnelConn returns a net.Conn bridged through the agent tunnel to the target host and port.
func OpenTunnelConn(providerID uint, targetHost string, targetPort int) (net.Conn, error) {
	mgr, err := GetOrCreateTunnelManager(providerID)
	if err != nil {
		return nil, err
	}
	localConn, tunnelConn := net.Pipe()
	go mgr.handleConn(tunnelConn, targetHost, targetPort)
	return localConn, nil
}

// RemoveTunnelManager 移除指定 Provider 的 TunnelManager（Agent 断线时调用）。
func RemoveTunnelManager(providerID uint) {
	tunnelMgrMu.Lock()
	mgr := tunnelMgrs[providerID]
	delete(tunnelMgrs, providerID)
	tunnelMgrMu.Unlock()
	if mgr != nil {
		mgr.CloseAllSessions()
	}
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
