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
	"strings"
	"sync"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"

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
		_ = tm.ac.writeTextMessage(closeMsg, 5*time.Second)
	}()

	// 1. 发送 tunnel_open 给 Agent
	openPayload, _ := json.Marshal(tunnelOpenPayload{ConnID: connID, Host: targetHost, Port: targetPort})
	openMsg, _ := json.Marshal(wsMessage{Type: msgTypeTunnelOpen, Payload: openPayload})
	if err := tm.ac.writeTextMessage(openMsg, 10*time.Second); err != nil {
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
				if werr := tm.ac.writeBinaryMessage(frame, 10*time.Second); werr != nil {
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
	doneCh     chan struct{} // closed when the HandleControllerPort goroutine has fully exited
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
	doneCh := make(chan struct{})
	ctrlListeners[portID] = &controllerListener{listenPort: listenPort, stopCh: stopCh, doneCh: doneCh}

	go func() {
		defer close(doneCh)
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

// StopControllerPortForward 停止指定 Port 的控制端监听，并等待其 goroutine 完全退出。
// 这确保端口已被释放，后续可立即重新绑定同一端口。
func StopControllerPortForward(portID uint) {
	ctrlListenerMu.Lock()
	cl, ok := ctrlListeners[portID]
	if ok {
		close(cl.stopCh)
		delete(ctrlListeners, portID)
	}
	ctrlListenerMu.Unlock()

	// 等待旧的 HandleControllerPort goroutine 完全退出，确保端口已释放
	if ok && cl.doneCh != nil {
		select {
		case <-cl.doneCh:
		case <-time.After(5 * time.Second):
			global.APP_LOG.Warn("等待控制端端口转发停止超时",
				zap.Uint("portID", portID))
		}
	}
}

// RestartControllerPortForward 重启指定 Port 的控制端监听（先停后启）。
// 用于 Agent 重连后刷新 TunnelManager 引用。
func RestartControllerPortForward(portID uint, providerID uint, listenPort int, targetHost string, targetPort int) error {
	// 先停止旧监听器
	StopControllerPortForward(portID)

	// 清理旧的 TunnelManager，确保 GetOrCreateTunnelManager 创建新的
	RemoveTunnelManager(providerID)

	// 启动新监听器
	return StartControllerPortForward(portID, providerID, listenPort, targetHost, targetPort)
}

// resolveTargetHost 解析控制器端口转发的目标地址。
// 优先使用 port.InternalHost（用户指定），若为空或可能需要刷新，
// 则从实例的当前 PrivateIP 获取并回写到数据库。
func resolveTargetHost(port *providerModel.Port) string {
	// 如果 InternalHost 已设置且不是明显的IP格式（可能是容器名），直接使用
	if port.InternalHost != "" {
		// 如果 InternalHost 看起来像容器名（非纯IP），保持不变
		if !looksLikeIP(port.InternalHost) {
			return port.InternalHost
		}
		// 如果是IP格式，检查实例当前IP是否已变更
		var instance providerModel.Instance
		if err := global.APP_DB.Select("private_ip").
			Where("id = ?", port.InstanceID).First(&instance).Error; err == nil {
			if instance.PrivateIP != "" && instance.PrivateIP != port.InternalHost {
				// 实例IP已变更，更新 InternalHost
				global.APP_LOG.Info("控制器端口转发目标IP已变更，自动更新",
					zap.Uint("portID", port.ID),
					zap.String("oldHost", port.InternalHost),
					zap.String("newHost", instance.PrivateIP))
				global.APP_DB.Model(&providerModel.Port{}).
					Where("id = ?", port.ID).
					Update("internal_host", instance.PrivateIP)
				return instance.PrivateIP
			}
		}
		return port.InternalHost
	}

	// InternalHost 为空，从实例获取
	var instance providerModel.Instance
	if err := global.APP_DB.Select("private_ip").
		Where("id = ?", port.InstanceID).First(&instance).Error; err == nil {
		if instance.PrivateIP != "" {
			// 回写 InternalHost
			global.APP_DB.Model(&providerModel.Port{}).
				Where("id = ?", port.ID).
				Update("internal_host", instance.PrivateIP)
			return instance.PrivateIP
		}
	}

	return ""
}

// looksLikeIP 判断字符串是否看起来像IP地址（用于区分容器名和IP）。
func looksLikeIP(s string) bool {
	// 简单判断：IPv4 格式 x.x.x.x，IPv6 包含多个冒号
	parts := strings.Split(s, ".")
	if len(parts) == 4 {
		return true
	}
	if strings.Count(s, ":") >= 2 {
		return true
	}
	return false
}

// RecoverControllerPortForwardsByProvider 恢复指定 Provider 的所有活跃控制端端口转发。
// 在 Agent 重连时调用，确保端口转发使用新的 WebSocket 连接。
func RecoverControllerPortForwardsByProvider(providerID uint) {
	var ports []providerModel.Port
	if err := global.APP_DB.Where("provider_id = ? AND mapping_type = ? AND status = ?",
		providerID, "controller", "active").Find(&ports).Error; err != nil {
		global.APP_LOG.Error("查询待恢复的控制器端口转发失败",
			zap.Uint("providerID", providerID), zap.Error(err))
		return
	}

	if len(ports) == 0 {
		return
	}

	global.APP_LOG.Info("开始恢复控制器端口转发",
		zap.Uint("providerID", providerID),
		zap.Int("count", len(ports)))

	recovered := 0
	for _, port := range ports {
		targetHost := resolveTargetHost(&port)
		if targetHost == "" {
			global.APP_LOG.Warn("控制器端口转发恢复失败：无目标地址",
				zap.Uint("portID", port.ID), zap.Uint("instanceID", port.InstanceID))
			continue
		}

		if err := RestartControllerPortForward(port.ID, port.ProviderID,
			port.HostPort, targetHost, port.GuestPort); err != nil {
			global.APP_LOG.Warn("恢复控制器端口转发失败",
				zap.Uint("portID", port.ID), zap.Error(err))
		} else {
			recovered++
		}
	}

	global.APP_LOG.Info("控制器端口转发恢复完成",
		zap.Uint("providerID", providerID),
		zap.Int("recovered", recovered),
		zap.Int("total", len(ports)))
}

// RecoverAllControllerPortForwards 恢复所有活跃的控制端端口转发。
// 在控制端启动时调用，尝试恢复所有之前活跃的控制器端口转发。
// 对于 Agent 尚未上线的 Provider，监听器会等待 Agent 连接后生效。
func RecoverAllControllerPortForwards() {
	var ports []providerModel.Port
	if err := global.APP_DB.Where("mapping_type = ? AND status = ?",
		"controller", "active").Find(&ports).Error; err != nil {
		global.APP_LOG.Error("查询所有待恢复的控制器端口转发失败", zap.Error(err))
		return
	}

	if len(ports) == 0 {
		global.APP_LOG.Debug("没有需要恢复的控制器端口转发")
		return
	}

	global.APP_LOG.Info("开始恢复所有控制器端口转发", zap.Int("count", len(ports)))

	recovered := 0
	skipped := 0
	for _, port := range ports {
		targetHost := resolveTargetHost(&port)
		if targetHost == "" {
			global.APP_LOG.Warn("控制器端口转发恢复失败：无目标地址",
				zap.Uint("portID", port.ID), zap.Uint("instanceID", port.InstanceID))
			skipped++
			continue
		}

		if err := StartControllerPortForward(port.ID, port.ProviderID,
			port.HostPort, targetHost, port.GuestPort); err != nil {
			global.APP_LOG.Debug("启动控制器端口转发失败（Agent可能尚未上线）",
				zap.Uint("portID", port.ID), zap.Uint("providerID", port.ProviderID), zap.Error(err))
			skipped++
		} else {
			recovered++
		}
	}

	global.APP_LOG.Info("所有控制器端口转发恢复完成",
		zap.Int("recovered", recovered),
		zap.Int("skipped", skipped),
		zap.Int("total", len(ports)))
}

// CheckAndRepairControllerPortForwards 定期检查并修复控制器端口转发。
// 发现已标记为 active 但未监听中的端口映射，自动恢复。
// 返回 (total, repaired)。
func CheckAndRepairControllerPortForwards() (int, int) {
	var ports []providerModel.Port
	if err := global.APP_DB.Where("mapping_type = ? AND status = ?",
		"controller", "active").Find(&ports).Error; err != nil {
		global.APP_LOG.Error("查询控制器端口转发失败", zap.Error(err))
		return 0, 0
	}

	repaired := 0
	for _, port := range ports {
		ctrlListenerMu.RLock()
		_, running := ctrlListeners[port.ID]
		ctrlListenerMu.RUnlock()

		if running {
			continue // 已在运行，跳过
		}

		// 监听器未运行，尝试恢复
		targetHost := resolveTargetHost(&port)
		if targetHost == "" {
			global.APP_LOG.Warn("控制器端口转发修复失败：无目标地址",
				zap.Uint("portID", port.ID))
			continue
		}

		if err := StartControllerPortForward(port.ID, port.ProviderID,
			port.HostPort, targetHost, port.GuestPort); err != nil {
			global.APP_LOG.Debug("修复控制器端口转发失败",
				zap.Uint("portID", port.ID), zap.Error(err))
		} else {
			repaired++
			global.APP_LOG.Info("已修复控制器端口转发",
				zap.Uint("portID", port.ID),
				zap.Int("hostPort", port.HostPort),
				zap.Uint("providerID", port.ProviderID))
		}
	}

	return len(ports), repaired
}

// IsControllerPortForwardRunning 检查指定端口映射的控制端监听是否在运行。
func IsControllerPortForwardRunning(portID uint) bool {
	ctrlListenerMu.RLock()
	defer ctrlListenerMu.RUnlock()
	_, ok := ctrlListeners[portID]
	return ok
}
