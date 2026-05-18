package agent

// status.go — Agent 运行时健康追踪、状态持久化、Provider 恢复与节点资源同步。

import (
	"net"
	"strings"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"

	"go.uber.org/zap"
)

// ── 运行时健康标记 ──────────────────────────────────────────────────────────

func (h *AgentHub) markConnected(providerID uint, now time.Time) {
	h.runtimeMu.Lock()
	h.runtimeState[providerID] = agentRuntimeState{
		connected:   true,
		connectedAt: now,
		lastInbound: now,
	}
	h.runtimeMu.Unlock()
}

func (h *AgentHub) markDisconnected(providerID uint) {
	h.runtimeMu.Lock()
	state := h.runtimeState[providerID]
	state.connected = false
	h.runtimeState[providerID] = state
	h.runtimeMu.Unlock()
}

func (h *AgentHub) markInbound(providerID uint, now time.Time) {
	h.runtimeMu.Lock()
	state := h.runtimeState[providerID]
	if state.connectedAt.IsZero() {
		state.connectedAt = now
	}
	state.connected = true
	state.lastInbound = now
	h.runtimeState[providerID] = state
	h.runtimeMu.Unlock()
}

func buildRuntimeHealth(providerID uint, now time.Time, state agentRuntimeState) AgentRuntimeHealth {
	health := AgentRuntimeHealth{
		ProviderID: providerID,
		Connected:  state.connected,
		Status:     "offline",
	}

	if !state.connected {
		return health
	}

	if !state.connectedAt.IsZero() {
		t := state.connectedAt
		health.ConnectedAt = &t
	}
	if !state.lastInbound.IsZero() {
		t := state.lastInbound
		health.ControlLastSeen = &t
	}

	if state.lastInbound.IsZero() || now.Sub(state.lastInbound) > readDeadlineWindow+15*time.Second {
		health.Status = "offline"
		return health
	}

	health.Status = "online"
	return health
}

func (h *AgentHub) GetRuntimeHealth(providerID uint) AgentRuntimeHealth {
	now := time.Now()
	h.runtimeMu.RLock()
	state, ok := h.runtimeState[providerID]
	h.runtimeMu.RUnlock()
	if !ok {
		return AgentRuntimeHealth{ProviderID: providerID, Status: "offline", Connected: false}
	}
	return buildRuntimeHealth(providerID, now, state)
}

func (h *AgentHub) GetRuntimeHealthBatch(providerIDs []uint) map[uint]AgentRuntimeHealth {
	now := time.Now()
	result := make(map[uint]AgentRuntimeHealth, len(providerIDs))

	h.runtimeMu.RLock()
	defer h.runtimeMu.RUnlock()

	for _, providerID := range providerIDs {
		state, ok := h.runtimeState[providerID]
		if !ok {
			result[providerID] = AgentRuntimeHealth{ProviderID: providerID, Status: "offline", Connected: false}
			continue
		}
		result[providerID] = buildRuntimeHealth(providerID, now, state)
	}

	return result
}

// ── 状态持久化 ──────────────────────────────────────────────────────────────

func (h *AgentHub) updateProviderAgentStatus(providerID uint, status string, lastSeen *time.Time, remoteAddr string, hostname string) {
	h.updateProviderAgentStatusWithVersion(providerID, status, lastSeen, remoteAddr, hostname, "")
}

func (h *AgentHub) updateProviderAgentStatusWithVersion(providerID uint, status string, lastSeen *time.Time, remoteAddr string, hostname string, version string) {
	now := time.Now()
	shouldPersist := true

	// 对 online 心跳做持久化节流：状态不变且未到窗口期时跳过 DB 写。
	if status == "online" {
		h.persistMu.Lock()
		memo, ok := h.statusPersistMemo[providerID]
		shouldPersist = !ok || memo.status != status || now.Sub(memo.lastPersist) >= heartbeatPersistInterval
		h.persistMu.Unlock()
		if !shouldPersist {
			return
		}
	}

	updates := map[string]interface{}{
		"agent_status": status,
	}
	remoteIP := ""
	if lastSeen != nil {
		updates["agent_last_seen"] = lastSeen
	}
	if remoteAddr != "" {
		// 只保存 IP 部分
		if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
			updates["agent_remote_ip"] = host
			remoteIP = host
		} else {
			updates["agent_remote_ip"] = remoteAddr
			remoteIP = remoteAddr
		}
	}
	if hostname != "" {
		updates["agent_hostname"] = hostname
	}
	if version != "" {
		updates["agent_version"] = version
	}
	if err := global.APP_DB.Model(&providerModel.Provider{}).
		Where("id = ?", providerID).
		Updates(updates).Error; err != nil {
		global.APP_LOG.Warn("更新 Agent 状态失败", zap.Uint("providerID", providerID), zap.Error(err))
		return
	}

	h.persistMu.Lock()
	h.statusPersistMemo[providerID] = agentStatusPersistState{status: status, lastPersist: now}
	h.persistMu.Unlock()

	if status == "online" && remoteIP != "" {
		if err := global.APP_DB.Model(&providerModel.Provider{}).
			Where("id = ? AND connection_type = ? AND (endpoint IS NULL OR endpoint = '')", providerID, "agent").
			Update("endpoint", remoteIP).Error; err != nil {
			global.APP_LOG.Warn("回填 Agent 节点 endpoint 失败", zap.Uint("providerID", providerID), zap.Error(err))
		}
	}
}

// ── Provider 恢复 ───────────────────────────────────────────────────────────

// recoverProviderOnReconnect 在 Agent 重连成功后恢复 Provider 状态。
// 处理两种边界条件：
//  1. Provider 因健康检查连续失败被自动冻结 → 自动解冻
//  2. 主控重启后 agent_status 与 status 不一致（agent_status=online 但 status=inactive）→ 确认 status
func (h *AgentHub) recoverProviderOnReconnect(providerID uint, now time.Time) {
	var p providerModel.Provider
	if err := global.APP_DB.Select("id, is_frozen, frozen_reason, status, connection_type, allow_claim").
		Where("id = ?", providerID).First(&p).Error; err != nil {
		return
	}

	needsUpdate := false
	updates := map[string]interface{}{}

	// 如果 Provider 因健康检查连续失败被自动冻结，Agent 重连成功即证明节点可达，自动解冻
	if p.IsFrozen && p.FrozenReason != "" &&
		(p.FrozenReason == "expired" || strings.Contains(p.FrozenReason, "健康检查连续失败") || strings.Contains(p.FrozenReason, "Agent 反向连接连续断开")) {
		// expired 类型的冻结通过 Agent 重连无法解冻（需管理员手动设置新的过期时间）
		// 仅对健康检查自动冻结（含 Agent 断连自动冻结）进行解冻
		if strings.Contains(p.FrozenReason, "健康检查连续失败") || strings.Contains(p.FrozenReason, "Agent 反向连接连续断开") {
			updates["is_frozen"] = false
			updates["frozen_at"] = nil
			updates["frozen_reason"] = ""
			updates["allow_claim"] = true
			needsUpdate = true
			global.APP_LOG.Info("Agent 重连后自动解冻 Provider（此前因健康检查失败被冻结）",
				zap.Uint("providerID", providerID),
				zap.String("frozen_reason", p.FrozenReason))
		}
	}

	// Agent 重连后，若 general status 为 inactive，确认为 active
	// 避免主控重启后 agent_status=online 但 status=inactive 的不一致状态
	// 同时恢复 allow_claim，防止健康检查将其设为 false 后节点显示为"禁用"
	if p.Status == "inactive" && p.ConnectionType == "agent" {
		updates["status"] = "active"
		updates["allow_claim"] = true
		needsUpdate = true
		global.APP_LOG.Info("Agent 重连后恢复 Provider 状态为 active",
			zap.Uint("providerID", providerID),
			zap.String("old_status", p.Status))
	}

	if needsUpdate {
		if err := global.APP_DB.Model(&providerModel.Provider{}).
			Where("id = ?", providerID).
			Updates(updates).Error; err != nil {
			global.APP_LOG.Warn("Agent 重连后恢复 Provider 状态失败",
				zap.Uint("providerID", providerID), zap.Error(err))
		}
	}
}

// ── 节点资源同步 ────────────────────────────────────────────────────────────

// syncNodeResources 通过 Agent WebSocket 获取节点硬件资源（CPU/内存/磁盘），
// 更新 Provider 的 NodeCPUCores / NodeMemoryTotal / NodeDiskTotal。
// Agent 模式节点不依赖 SSH 健康检查，资源通过此函数在 Agent 上线时同步。
func (h *AgentHub) syncNodeResources(ac *AgentConn) {
	// 稍等片刻确保 WebSocket 连接稳定
	time.Sleep(2 * time.Second)

	providerID := ac.ProviderID

	// 检查 Provider 是否已有资源数据，已有则跳过（避免覆盖手动设置的值）
	var provider providerModel.Provider
	if err := global.APP_DB.Select("node_cpu_cores, node_memory_total, node_disk_total, resource_synced").
		Where("id = ?", providerID).First(&provider).Error; err != nil {
		global.APP_LOG.Warn("syncNodeResources: 查询 Provider 失败", zap.Uint("providerID", providerID), zap.Error(err))
		return
	}
	if provider.ResourceSynced && provider.NodeCPUCores > 0 && provider.NodeMemoryTotal > 0 && provider.NodeDiskTotal > 0 {
		return // 已同步过，无需重复
	}

	// 获取 CPU 核心数
	cpuOutput, cpuErr := ac.ExecuteWithTimeout("nproc 2>/dev/null || echo 0", 10*time.Second)
	cpuCores := 0
	if cpuErr == nil {
		cpuCores = parseFirstInt(strings.TrimSpace(cpuOutput))
	}

	// 获取总内存（字节）
	memOutput, memErr := ac.ExecuteWithTimeout("free -b 2>/dev/null | awk '/^Mem:/{print $2}' || echo 0", 10*time.Second)
	memTotal := int64(0)
	if memErr == nil {
		memTotal = parseInt64(strings.TrimSpace(memOutput))
	}

	// 获取根分区总磁盘（字节）
	diskOutput, diskErr := ac.ExecuteWithTimeout("df -B1 / 2>/dev/null | awk 'NR==2{print $2}' || echo 0", 10*time.Second)
	diskTotal := int64(0)
	if diskErr == nil {
		diskTotal = parseInt64(strings.TrimSpace(diskOutput))
	}

	if cpuCores == 0 && memTotal == 0 && diskTotal == 0 {
		global.APP_LOG.Warn("syncNodeResources: 未能获取节点资源信息",
			zap.Uint("providerID", providerID))
		return
	}

	// 转换为 MB（内存和磁盘原始值为字节）
	memMB := memTotal / (1024 * 1024)
	diskMB := diskTotal / (1024 * 1024)

	now := time.Now()
	updates := map[string]interface{}{
		"resource_synced":    true,
		"resource_synced_at": &now,
	}
	if cpuCores > 0 {
		updates["node_cpu_cores"] = cpuCores
	}
	if memMB > 0 {
		updates["node_memory_total"] = memMB
	}
	if diskMB > 0 {
		updates["node_disk_total"] = diskMB
	}

	if err := global.APP_DB.Model(&providerModel.Provider{}).
		Where("id = ?", providerID).
		Updates(updates).Error; err != nil {
		global.APP_LOG.Warn("syncNodeResources: 更新 Provider 资源失败",
			zap.Uint("providerID", providerID), zap.Error(err))
		return
	}

	global.APP_LOG.Info("Agent 节点资源同步完成",
		zap.Uint("providerID", providerID),
		zap.Int("cpuCores", cpuCores),
		zap.Int64("memoryMB", memMB),
		zap.Int64("diskMB", diskMB))
}
