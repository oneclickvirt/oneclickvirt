package agent

// hub.go — AgentHub 核心：连接注册/注销、消息读循环、应用层 ping 循环。
// AgentConn 方法集 → conn.go ｜ 类型/常量 → types.go
// 状态/健康/资源同步 → status.go ｜ 初始化/鉴权/工具 → init.go

import (
	"encoding/binary"
	"encoding/json"
	"math/rand"
	"sync"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	providerService "oneclickvirt/service/provider"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// ── AgentHub — 全局单例，管理所有 Agent 连接 ────────────────────────────────

type AgentHub struct {
	mu    sync.RWMutex
	conns map[uint]*AgentConn // providerID → AgentConn

	persistMu         sync.Mutex
	statusPersistMemo map[uint]agentStatusPersistState // providerID -> 最近一次状态持久化信息

	runtimeMu    sync.RWMutex
	runtimeState map[uint]agentRuntimeState // providerID -> 运行时连接健康状态
}

var (
	globalHub     *AgentHub
	globalHubOnce sync.Once
)

// GetHub 返回全局 AgentHub 单例。
func GetHub() *AgentHub {
	globalHubOnce.Do(func() {
		globalHub = &AgentHub{
			conns:             make(map[uint]*AgentConn),
			statusPersistMemo: make(map[uint]agentStatusPersistState),
			runtimeState:      make(map[uint]agentRuntimeState),
		}
	})
	return globalHub
}

// ── 连接管理 ────────────────────────────────────────────────────────────────

// Register 注册一个新连接并启动读取协程。
func (h *AgentHub) Register(ac *AgentConn) {
	h.mu.Lock()
	// 如果已有旧连接，关闭底层 TCP 连接，触发 readLoop 退出。
	// 同时记录旧 conn 指针，在锁外进行完整清理，避免 h.mu 持锁时间过长。
	var old *AgentConn
	if o, ok := h.conns[ac.ProviderID]; ok {
		old = o
		old.conn.Close()
	}
	h.conns[ac.ProviderID] = ac
	h.mu.Unlock()

	// 在锁外清理旧连接的 goroutine 和 pending 操作：
	// 旧连接的 readLoop 会因 conn.Close() 收到错误后调用 unregister，
	// 但 unregister 中 current != old 会提前返回，不会清理以下资源。
	// 必须在此处显式清理，否则 NoiseLoop/WSPingLoop goroutine 将永久泄漏。
	if old != nil {
		old.StopNoiseLoop()
		old.StopWSPingLoop()
		old.closeAllSessions() // 通知所有挂起的 exec/shell 操作立即返回错误
	}

	h.persistMu.Lock()
	delete(h.statusPersistMemo, ac.ProviderID)
	h.persistMu.Unlock()

	// 清理旧的 TunnelManager，确保后续端口转发使用新的 AgentConn
	RemoveTunnelManager(ac.ProviderID)

	global.APP_LOG.Info("Agent 已连接",
		zap.Uint("providerID", ac.ProviderID),
		zap.String("remoteAddr", ac.remoteAddr))

	// 记录重连信息：Provider 离线了多久，是否在宽限期内
	var provider providerModel.Provider
	if err := global.APP_DB.Select("name, agent_connected_at, agent_last_seen").
		Where("id = ?", ac.ProviderID).First(&provider).Error; err == nil {
		prevConnected := provider.AgentConnectedAt
		prevLastSeen := provider.AgentLastSeen
		if prevLastSeen != nil {
			offlineDuration := time.Since(*prevLastSeen)
			if offlineDuration > 30*time.Second {
				global.APP_LOG.Info("Agent 重连成功（曾离线较长时间）",
					zap.Uint("providerID", ac.ProviderID),
					zap.String("name", provider.Name),
					zap.Duration("offlineDuration", offlineDuration))
			}
		}
		_ = prevConnected
	}

	// 同步更新数据库状态，确保前端检测立即可见
	now := time.Now()
	h.markConnected(ac.ProviderID, now)
	h.updateProviderAgentStatus(ac.ProviderID, "online", &now, ac.remoteAddr, "")

	// Agent 重连成功后，如果 Provider 因健康检查连续失败被自动冻结，则自动解冻。
	// Agent 反向连接成功即证明节点可达，无需等待健康检查周期。
	// 同时确认因主控重启导致的 status 不一致（agent_status=online 但 status=inactive）。
	h.recoverProviderOnReconnect(ac.ProviderID, now)

	// 记录本次连接建立时间（用于前端显示在线时长）
	if err := global.APP_DB.Model(&providerModel.Provider{}).
		Where("id = ?", ac.ProviderID).
		Update("agent_connected_at", now).Error; err != nil {
		global.APP_LOG.Warn("更新 Agent 连接时间失败", zap.Uint("providerID", ac.ProviderID), zap.Error(err))
	}

	// 异步同步节点硬件资源（CPU/内存/磁盘），解决 Agent 模式节点无 SSH 健康检查的资源同步问题
	go h.syncNodeResources(ac)

	// 启动读取循环（异步，包含后续心跳和 info 帧处理）
	go h.readLoop(ac)

	// 启动 anti-DPI 噪声帧发送（随机间隔 + 随机长度，打破流量指纹）
	ac.StartNoiseLoop()

	// 启动 WebSocket 协议层 ping（30 s 间隔），作为传输层保活。
	// 协议 ping 不受应用层写路径死锁影响，可独立刷新读超时。
	ac.StartWSPingLoop()

	// Agent 重连后恢复该 Provider 的控制端端口转发（内网穿透）
	// 延迟执行，确保 WebSocket 连接已稳定
	go func() {
		time.Sleep(3 * time.Second)
		RecoverControllerPortForwardsByProvider(ac.ProviderID)
	}()

	// 触发延迟实例发现与导入（Agent 模式节点在创建时标记了 PendingDiscovery）
	go h.triggerPendingDiscovery(ac.ProviderID)

	// 异步确保 Provider 在 ProviderService 中已加载（解决主控重启后 Provider 内存缓存为空，
	// Agent 重连后 ProviderService.providers 中仍然缺失该 Provider，导致后续操作
	// 如 GetStoppedContainers/ExecuteSSHCommand 等需要先通过 EnsureProviderConnected
	// 重新加载的问题）。此处延迟 2 秒确保 Agent WebSocket 连接已稳定。
	go func() {
		time.Sleep(2 * time.Second)
		if _, err := providerService.GetProviderInstanceByID(ac.ProviderID); err != nil {
			global.APP_LOG.Warn("Agent 重连后加载 Provider 到内存缓存失败",
				zap.Uint("providerID", ac.ProviderID),
				zap.Error(err))
		} else {
			global.APP_LOG.Debug("Agent 重连后 Provider 内存缓存已同步",
				zap.Uint("providerID", ac.ProviderID))
		}
	}()
}

// triggerPendingDiscovery 检查 Provider 是否有待处理的实例发现任务，如有则触发。
func (h *AgentHub) triggerPendingDiscovery(providerID uint) {
	// 等待资源同步和 WebSocket 连接稳定
	time.Sleep(5 * time.Second)

	// 检查是否有待处理的发现任务
	var provider providerModel.Provider
	if err := global.APP_DB.Select("pending_discovery, discovery_owner_user_id, discovery_auto_adjust").
		Where("id = ?", providerID).First(&provider).Error; err != nil {
		global.APP_LOG.Warn("triggerPendingDiscovery: 查询 Provider 失败",
			zap.Uint("providerID", providerID), zap.Error(err))
		return
	}

	if !provider.PendingDiscovery {
		return
	}

	// 清除 PendingDiscovery 标记（无论成功与否，避免重复触发）
	if err := global.APP_DB.Model(&providerModel.Provider{}).
		Where("id = ?", providerID).
		Update("pending_discovery", false).Error; err != nil {
		global.APP_LOG.Warn("triggerPendingDiscovery: 清除 PendingDiscovery 标记失败",
			zap.Uint("providerID", providerID), zap.Error(err))
	}

	global.APP_LOG.Info("Agent 连接后触发延迟实例发现",
		zap.Uint("providerID", providerID),
		zap.Uint("ownerUserID", provider.DiscoveryOwnerUserID),
		zap.Bool("autoAdjust", provider.DiscoveryAutoAdjust))

	// 调用注册的回调执行实例发现与导入
	if OnAgentConnected != nil {
		OnAgentConnected(providerID)
	}
}

// GetConn 返回指定 Provider 的 AgentConn（如果在线）。
func (h *AgentHub) GetConn(providerID uint) (*AgentConn, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	ac, ok := h.conns[providerID]
	return ac, ok
}

// DisconnectProvider 强制断开指定 Provider 的 Agent 连接（如 provider 类型变更时）。
// 关闭底层 TCP 连接，后续的 readLoop 会自然调用 unregister 完成清理。
func (h *AgentHub) DisconnectProvider(providerID uint) {
	h.mu.RLock()
	ac, ok := h.conns[providerID]
	h.mu.RUnlock()
	if ok && ac != nil {
		global.APP_LOG.Info("主动断开 Agent 连接（Provider 配置变更）",
			zap.Uint("providerID", providerID))
		ac.conn.Close() // 关闭底层连接，触发 readLoop 退出 -> unregister
	}
}

// unregister 注销连接（同步更新 DB 状态以确保前端立即可见）。
func (h *AgentHub) unregister(ac *AgentConn) {
	providerID := ac.ProviderID

	h.mu.Lock()
	current, ok := h.conns[providerID]
	if !ok {
		h.mu.Unlock()
		return
	}
	// 仅注销当前活跃连接；若旧连接延迟退出，不应覆盖新连接状态。
	if current != ac {
		h.mu.Unlock()
		return
	}
	current.StopNoiseLoop()
	current.StopWSPingLoop()
	delete(h.conns, providerID)
	h.mu.Unlock()

	// 关闭所有挂起的 exec 请求和 shell 会话，确保 ExecuteWithTimeout 和
	// handleAgentShellTerminal 立即返回错误，而非等待各自的超时（最长 300s/30min）。
	current.closeAllSessions()

	h.persistMu.Lock()
	delete(h.statusPersistMemo, providerID)
	h.persistMu.Unlock()
	h.markDisconnected(providerID)

	// 清理该 Provider 的 TunnelManager，释放资源
	RemoveTunnelManager(providerID)

	// 停止该 Provider 的所有控制端端口转发监听器
	// Agent 断开后这些监听器无法转发流量，应释放端口资源
	StopControllerPortForwardsByProvider(providerID)

	global.APP_LOG.Info("Agent 已断开", zap.Uint("providerID", providerID))
	h.updateProviderAgentStatus(providerID, "offline", nil, "", "")
	// 清除连接建立时间（离线后无在线时长）
	if err := global.APP_DB.Model(&providerModel.Provider{}).
		Where("id = ?", providerID).
		Update("agent_connected_at", nil).Error; err != nil {
		global.APP_LOG.Warn("清除 Agent 连接时间失败", zap.Uint("providerID", providerID), zap.Error(err))
	}
}

// ── 消息读循环 ──────────────────────────────────────────────────────────────

// readLoop 持续读取来自 Agent 的消息。
func (h *AgentHub) readLoop(ac *AgentConn) {
	defer func() {
		if r := recover(); r != nil {
			global.APP_LOG.Error("Agent readLoop panic",
				zap.Uint("providerID", ac.ProviderID),
				zap.Any("panic", r),
				zap.Stack("stack"))
		}
		ac.conn.Close()
		h.unregister(ac)
	}()

	ac.conn.SetReadDeadline(time.Now().Add(readDeadlineWindow))
	ac.conn.SetPongHandler(func(string) error {
		now := time.Now()
		ac.conn.SetReadDeadline(now.Add(readDeadlineWindow))
		ac.mu.Lock()
		ac.pingFailCount = 0
		ac.mu.Unlock()
		h.markInbound(ac.ProviderID, now)
		h.updateProviderAgentStatus(ac.ProviderID, "online", &now, ac.remoteAddr, "")
		return nil
	})

	for {
		msgType, data, err := ac.conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				global.APP_LOG.Warn("Agent WebSocket 读取错误",
					zap.Uint("providerID", ac.ProviderID), zap.Error(err))
			}
			return
		}

		// 每次成功读取后刷新读超时，确保任何活动都能保持连接存活
		// ping 循环以 ~15s 间隔检测死连接，读超时仅作为最后防线
		ac.conn.SetReadDeadline(time.Now().Add(readDeadlineWindow))
		now := time.Now()
		// 仅在收到 Agent 上行帧时续命在线，避免"仅写成功但链路已半断"误判。
		h.markInbound(ac.ProviderID, now)
		h.updateProviderAgentStatus(ac.ProviderID, "online", &now, ac.remoteAddr, "")

		// 二进制帧：隧道数据 [8-byte connID hash][payload]
		if msgType == websocket.BinaryMessage {
			if len(data) <= 8 {
				continue
			}
			connHash := binary.BigEndian.Uint64(data[:8])
			payload := data[8:]
			tunnelMgrMu.RLock()
			mgr, hasMgr := tunnelMgrs[ac.ProviderID]
			tunnelMgrMu.RUnlock()
			if hasMgr {
				mgr.DeliverByHash(connHash, payload)
			}
			continue
		}

		var msg wsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			global.APP_LOG.Warn("Agent 消息解析失败", zap.Error(err))
			continue
		}

		switch msg.Type {
		case msgTypeExecResponse:
			h.markInbound(ac.ProviderID, time.Now())
			var resp execResponsePayload
			if err := json.Unmarshal(msg.Payload, &resp); err == nil {
				ac.mu.Lock()
				if ch, ok := ac.pending[msg.ID]; ok {
					select {
					case ch <- resp:
					default:
						global.APP_LOG.Debug("丢弃重复或过期的 exec 响应",
							zap.Uint("providerID", ac.ProviderID),
							zap.String("reqID", msg.ID))
					}
				}
				ac.mu.Unlock()
			}

		case msgTypePong:
			ac.conn.SetReadDeadline(time.Now().Add(readDeadlineWindow))

		case msgTypeInfo:
			var info infoPayload
			if err := json.Unmarshal(msg.Payload, &info); err == nil && info.Hostname != "" {
				// Optional second-factor: validate secret in info frame.
				if info.Secret != "" {
					var p providerModel.Provider
					if err := global.APP_DB.Select("agent_secret").
						Where("id = ?", ac.ProviderID).First(&p).Error; err == nil {
						if p.AgentSecret != "" && info.Secret != p.AgentSecret {
							global.APP_LOG.Warn("Agent info 帧 secret 验证失败，断开连接",
								zap.Uint("providerID", ac.ProviderID))
							ac.conn.Close()
							return
						}
					}
				}
				ac.mu.Lock()
				ac.hostname = info.Hostname
				ac.mu.Unlock()
				now := time.Now()
				go h.updateProviderAgentStatusWithVersion(ac.ProviderID, "online", &now, ac.remoteAddr, info.Hostname, info.Version)
			}

		case msgTypeTunnelAck:
			var ack tunnelAckPayload
			if err := json.Unmarshal(msg.Payload, &ack); err == nil {
				tunnelMgrMu.RLock()
				mgr, hasMgr := tunnelMgrs[ac.ProviderID]
				tunnelMgrMu.RUnlock()
				if hasMgr {
					mgr.DeliverAck(ack)
				}
			}

		case msgTypeTunnelClose:
			var cl tunnelClosePayload
			if err := json.Unmarshal(msg.Payload, &cl); err == nil {
				tunnelMgrMu.RLock()
				mgr, hasMgr := tunnelMgrs[ac.ProviderID]
				tunnelMgrMu.RUnlock()
				if hasMgr {
					mgr.CloseSession(cl.ConnID)
				}
			}

		case msgTypeTunnelKeepalive:
			var keepalive tunnelKeepalivePayload
			if err := json.Unmarshal(msg.Payload, &keepalive); err == nil {
				tunnelMgrMu.RLock()
				mgr, hasMgr := tunnelMgrs[ac.ProviderID]
				tunnelMgrMu.RUnlock()
				if hasMgr {
					mgr.TouchSession(keepalive.ConnID)
				}
			}

		case msgTypeShellData:
			h.markInbound(ac.ProviderID, time.Now())
			var payload shellDataPayload
			if err := json.Unmarshal(msg.Payload, &payload); err == nil {
				ac.mu.Lock()
				if session, ok := ac.shellSessions[msg.ID]; ok {
					select {
					case session.OutputCh <- []byte(payload.Data):
					default:
					}
				}
				ac.mu.Unlock()
			}

		case msgTypeShellClose:
			ac.mu.Lock()
			if session, ok := ac.shellSessions[msg.ID]; ok {
				delete(ac.shellSessions, msg.ID)
				session.safeClose()
			}
			ac.mu.Unlock()

		case msgTypeFMListResp, msgTypeFMDownloadResp, msgTypeFMUploadResp,
			msgTypeFMDeleteResp, msgTypeFMMkdirResp, msgTypeFMError:
			ac.mu.Lock()
			if ch, ok := ac.fmPending[msg.ID]; ok {
				select {
				case ch <- fmRawResp{MsgType: msg.Type, Payload: msg.Payload}:
				default:
				}
			}
			ac.mu.Unlock()

		case msgTypeNoise:
			// Anti-DPI noise frame — silently discarded.
		}
	}
}

// ── 应用层 Ping 循环 ────────────────────────────────────────────────────────

// StartPingLoop 仅作为 legacy 兜底：当链路长时间没有任何入站流量时，
// 才发送低频应用层 ping，兼容旧 Agent 或异常中间件环境。
func (h *AgentHub) StartPingLoop() {
	baseInterval := 120 * time.Second
	go func() {
		for {
			jitterRange := float64(baseInterval) * 0.35
			jitter := time.Duration(rand.Float64()*jitterRange*2 - jitterRange)
			interval := baseInterval + jitter
			if interval < 75*time.Second {
				interval = 75 * time.Second
			}
			time.Sleep(interval)

			h.mu.RLock()
			conns := make([]*AgentConn, 0, len(h.conns))
			for _, ac := range h.conns {
				conns = append(conns, ac)
			}
			h.mu.RUnlock()

			for _, ac := range conns {
				now := time.Now()
				h.runtimeMu.RLock()
				state, ok := h.runtimeState[ac.ProviderID]
				h.runtimeMu.RUnlock()
				if ok && !state.lastInbound.IsZero() && now.Sub(state.lastInbound) < 90*time.Second {
					continue
				}

				pingFrame := map[string]interface{}{
					"type": msgTypePing,
					"id":   randomID(),
				}
				pingMsg, _ := json.Marshal(pingFrame)

				if err := ac.writeTextMessage(pingMsg, 5*time.Second); err != nil {
					ac.mu.Lock()
					ac.pingFailCount++
					failCount := ac.pingFailCount
					ac.mu.Unlock()

					global.APP_LOG.Warn("Agent ping 写入失败",
						zap.Uint("providerID", ac.ProviderID),
						zap.Int("consecutiveFailures", failCount),
						zap.Error(err))

					if failCount >= 3 {
						global.APP_LOG.Error("Agent 连续 ping 失败超过阈值，强制断开",
							zap.Uint("providerID", ac.ProviderID),
							zap.Int("consecutiveFailures", failCount))
						ac.conn.Close()
						h.unregister(ac)
					}
					continue
				}
				ac.mu.Lock()
				ac.pingFailCount = 0
				ac.mu.Unlock()
			}
		}
	}()
}
