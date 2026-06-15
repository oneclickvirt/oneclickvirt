package agent

// tunnel_proxy.go — 控制端端口监听池：端口转发启停、恢复及健康修复。

import (
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
// 控制端端口监听池：Port.MappingType == "controller" 时，系统启动时恢复监听
// ──────────────────────────────────────────────────────────────────────────────

type controllerListener struct {
	listenPort int
	stopCh     chan struct{}
	doneCh     chan struct{} // closed when the HandleControllerPort goroutine has fully exited
}

var controllerPortRecoverStatuses = []string{"active", "pending"}

var (
	ctrlListenerMu sync.RWMutex
	ctrlListeners  = make(map[uint]*controllerListener) // Port.ID → listener

	// recoveryMu 防止同一 Provider 的端口转发恢复操作并发执行
	recoveryMu sync.Mutex
)

// ResolveControllerPortTarget resolves the effective target for controller-mode
// port forwarding. Explicit hostnames are preserved; only empty or IP-style
// InternalHost values are refreshed from the instance's current private IP.
// The bool return indicates whether InternalHost should be persisted back.
func ResolveControllerPortTarget(internalHost, privateIP string) (string, bool) {
	internalHost = strings.TrimSpace(internalHost)
	privateIP = strings.TrimSpace(privateIP)

	if internalHost != "" {
		if !looksLikeIP(internalHost) {
			return internalHost, false
		}
		if privateIP != "" && privateIP != internalHost {
			return privateIP, true
		}
		return internalHost, false
	}

	if privateIP != "" {
		return privateIP, true
	}

	return "", false
}

// StartControllerPortForward 为一条 Port 记录启动控制端 TCP 监听转发。
func StartControllerPortForward(portID uint, providerID uint, listenPort int, targetHost string, targetPort int) error {
	var port providerModel.Port
	if err := global.APP_DB.Select("id", "provider_id", "host_port", "guest_port", "mapping_type", "status").
		Where("id = ?", portID).
		First(&port).Error; err != nil {
		return fmt.Errorf("load controller port %d: %w", portID, err)
	}
	if port.ProviderID != providerID || port.HostPort != listenPort || port.GuestPort != targetPort ||
		port.MappingType != "controller" || (port.Status != "active" && port.Status != "pending") {
		return fmt.Errorf("controller port %d metadata mismatch or inactive", portID)
	}

	ctrlListenerMu.Lock()
	if _, exists := ctrlListeners[portID]; exists {
		ctrlListenerMu.Unlock()
		return nil // 已在运行
	}
	ctrlListenerMu.Unlock()

	addr := fmt.Sprintf(":%d", listenPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("控制端监听 %s 失败: %w", addr, err)
	}

	mgr, err := GetOrCreateTunnelManager(providerID)
	if err != nil {
		_ = ln.Close()
		return err
	}

	stopCh := make(chan struct{})
	doneCh := make(chan struct{})

	ctrlListenerMu.Lock()
	// 双重检查：可能在获取 TunnelManager 期间其他 goroutine 已启动
	if _, exists := ctrlListeners[portID]; exists {
		ctrlListenerMu.Unlock()
		_ = ln.Close()
		close(stopCh)
		return nil
	}
	ctrlListeners[portID] = &controllerListener{listenPort: listenPort, stopCh: stopCh, doneCh: doneCh}
	ctrlListenerMu.Unlock()

	if err := global.APP_DB.Model(&providerModel.Port{}).
		Where("id = ?", portID).
		Updates(map[string]interface{}{
			"status":         "active",
			"mapping_method": "controller",
		}).Error; err != nil {
		ctrlListenerMu.Lock()
		delete(ctrlListeners, portID)
		ctrlListenerMu.Unlock()
		close(stopCh)
		close(doneCh)
		_ = ln.Close()
		return fmt.Errorf("更新控制端端口状态失败: %w", err)
	}

	resolver := func() (string, int, error) {
		var current providerModel.Port
		if err := global.APP_DB.Select("id", "provider_id", "instance_id", "host_port", "guest_port", "mapping_type", "status", "internal_host").
			Where("id = ?", portID).
			First(&current).Error; err != nil {
			return "", 0, fmt.Errorf("load controller port %d: %w", portID, err)
		}
		if current.ProviderID != providerID || current.HostPort != listenPort ||
			current.MappingType != "controller" || current.Status != "active" {
			return "", 0, fmt.Errorf("controller port %d metadata mismatch or inactive", portID)
		}

		effectiveHost := resolveTargetHost(&current)
		if effectiveHost == "" {
			effectiveHost = targetHost
		}
		effectivePort := current.GuestPort
		if effectivePort <= 0 {
			effectivePort = targetPort
		}
		if effectiveHost == "" || effectivePort <= 0 {
			return "", 0, fmt.Errorf("controller port %d target unavailable", portID)
		}
		return effectiveHost, effectivePort, nil
	}

	go func() {
		defer close(doneCh)
		if err := mgr.serveControllerPort(ln, addr, fmt.Sprintf("port:%d(dynamic)", portID), resolver, stopCh); err != nil {
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
// 同时关闭所有关联的 in-flight 隧道会话，防止残留的 handleConn goroutine
// 在监听器停止后继续向 WebSocket 写入数据，导致写路径拥塞。
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

// StopControllerPortForwardsByProvider 停止指定 Provider 的所有控制端端口转发监听器。
// 当 Agent 断开连接时调用，释放已失效的端口资源。
// Agent 重连后会通过 RecoverControllerPortForwardsByProvider 重新启动。
// 使用 recoveryMu 防止与 RecoverControllerPortForwardsByProvider 并发执行。
func StopControllerPortForwardsByProvider(providerID uint) {
	// 互斥：与 RecoverControllerPortForwardsByProvider 互斥，防止并发操作
	// 同一 Provider 的监听器导致竞态条件。
	recoveryMu.Lock()
	defer recoveryMu.Unlock()

	var ports []providerModel.Port
	if err := global.APP_DB.Where("provider_id = ? AND mapping_type = ? AND status IN ?",
		providerID, "controller", controllerPortRecoverStatuses).Find(&ports).Error; err != nil {
		global.APP_LOG.Warn("查询待停止的控制器端口转发失败",
			zap.Uint("providerID", providerID), zap.Error(err))
		return
	}

	if len(ports) == 0 {
		return
	}

	global.APP_LOG.Info("Agent 断开，停止控制端端口转发监听器",
		zap.Uint("providerID", providerID),
		zap.Int("count", len(ports)))

	stopped := 0
	for _, port := range ports {
		StopControllerPortForward(port.ID)
		stopped++
	}

	global.APP_LOG.Info("控制端端口转发监听器已停止",
		zap.Uint("providerID", providerID),
		zap.Int("stopped", stopped),
		zap.Int("total", len(ports)))
}

// RestartControllerPortForward 重启指定 Port 的控制端监听（先停后启）。
// 包含重试机制以处理端口尚未完全释放的情况。
func RestartControllerPortForward(portID uint, providerID uint, listenPort int, targetHost string, targetPort int) error {
	// 先停止旧监听器（同步等待其完全退出）
	StopControllerPortForward(portID)

	// 短暂等待以确保操作系统完全释放端口
	time.Sleep(200 * time.Millisecond)

	// 带重试的启动（处理端口尚未完全释放的情况）
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
		}
		err := StartControllerPortForward(portID, providerID, listenPort, targetHost, targetPort)
		if err == nil {
			return nil
		}
		// 仅对"地址已占用"错误进行重试，其他错误立即返回
		if !strings.Contains(err.Error(), "address already in use") {
			return err
		}
		lastErr = err
		global.APP_LOG.Debug("端口仍被占用，重试中",
			zap.Uint("portID", portID),
			zap.Int("attempt", attempt+1),
			zap.Int("listenPort", listenPort))
	}
	return fmt.Errorf("重启端口转发失败（已重试3次）: %w", lastErr)
}

// resolveTargetHost 解析控制器端口转发的目标地址。
// 优先使用 port.InternalHost（用户指定），若为空或可能需要刷新，
// 则从实例的当前 PrivateIP 获取并回写到数据库。
func resolveTargetHost(port *providerModel.Port) string {
	internalHost := strings.TrimSpace(port.InternalHost)
	if internalHost != "" && !looksLikeIP(internalHost) {
		return internalHost
	}

	var instance providerModel.Instance
	if err := global.APP_DB.Select("private_ip").
		Where("id = ?", port.InstanceID).First(&instance).Error; err == nil {
		targetHost, shouldUpdate := ResolveControllerPortTarget(internalHost, instance.PrivateIP)
		if shouldUpdate {
			global.APP_LOG.Info("控制器端口转发目标IP已变更，自动更新",
				zap.Uint("portID", port.ID),
				zap.String("oldHost", internalHost),
				zap.String("newHost", targetHost))
			global.APP_DB.Model(&providerModel.Port{}).
				Where("id = ?", port.ID).
				Update("internal_host", targetHost)
		}
		return targetHost
	}

	return internalHost
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
// 在 Agent 重连时调用，必须重启所有监听器，确保每个端口都使用同一个新的
// TunnelManager/AgentConn。不能跳过正在运行的监听器：旧监听器闭包里可能仍持有
// 断线前的 TunnelManager，导致 tunnel_ack 被全局路由到新 manager 后丢失。
func RecoverControllerPortForwardsByProvider(providerID uint) {
	// 互斥：与 StopControllerPortForwardsByProvider 互斥，防止并发操作
	// 同一 Provider 的监听器导致竞态条件。
	recoveryMu.Lock()
	defer recoveryMu.Unlock()

	var ports []providerModel.Port
	if err := global.APP_DB.Where("provider_id = ? AND mapping_type = ? AND status IN ?",
		providerID, "controller", controllerPortRecoverStatuses).Find(&ports).Error; err != nil {
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

	stopped := 0
	for _, port := range ports {
		if IsControllerPortForwardRunning(port.ID) {
			stopped++
		}
		StopControllerPortForward(port.ID)
	}

	RemoveTunnelManager(providerID)
	time.Sleep(200 * time.Millisecond)

	recovered := 0
	for _, port := range ports {
		targetHost := resolveTargetHost(&port)
		if targetHost == "" {
			global.APP_LOG.Warn("控制器端口转发恢复失败：无目标地址",
				zap.Uint("portID", port.ID), zap.Uint("instanceID", port.InstanceID))
			continue
		}

		if err := StartControllerPortForward(port.ID, port.ProviderID,
			port.HostPort, targetHost, port.GuestPort); err != nil {
			global.APP_LOG.Warn("恢复控制器端口转发失败",
				zap.Uint("portID", port.ID), zap.Error(err))
		} else {
			recovered++
		}
	}

	global.APP_LOG.Info("控制器端口转发恢复完成",
		zap.Uint("providerID", providerID),
		zap.Int("stopped", stopped),
		zap.Int("recovered", recovered),
		zap.Int("total", len(ports)))
}

// RecoverAllControllerPortForwards 恢复所有活跃的控制端端口转发。
// 在控制端启动时调用，尝试恢复所有之前活跃的控制器端口转发。
// 对于 Agent 尚未上线的 Provider，监听器会等待 Agent 连接后生效。
func RecoverAllControllerPortForwards() {
	var ports []providerModel.Port
	if err := global.APP_DB.Where("mapping_type = ? AND status IN ?",
		"controller", controllerPortRecoverStatuses).Find(&ports).Error; err != nil {
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

// portRepairFailCount 跟踪每个端口确认失败的次数，防止无限重试。
var (
	portRepairFailMu    sync.Mutex
	portRepairFailCount = make(map[uint]int) // PortID → 连续失败次数
)

const maxRepairFailCount = 5 // 连续失败超过此次数后标记端口为 error 状态

// CheckAndRepairControllerPortForwards 定期检查并确认控制器端口转发。
// 发现已标记为 active 但未监听中的端口映射，自动恢复。
// 连续失败超过阈值的端口会被标记为 error 状态，避免无限重试。
// 返回 (total, repaired)。
func CheckAndRepairControllerPortForwards() (int, int) {
	var ports []providerModel.Port
	if err := global.APP_DB.Where("mapping_type = ? AND status IN ?",
		"controller", controllerPortRecoverStatuses).Find(&ports).Error; err != nil {
		global.APP_LOG.Error("查询控制器端口转发失败", zap.Error(err))
		return 0, 0
	}

	desired := make(map[uint]struct{}, len(ports))
	for _, port := range ports {
		desired[port.ID] = struct{}{}
	}
	var orphanListeners []uint
	ctrlListenerMu.RLock()
	for portID := range ctrlListeners {
		if _, ok := desired[portID]; !ok {
			orphanListeners = append(orphanListeners, portID)
		}
	}
	ctrlListenerMu.RUnlock()
	for _, portID := range orphanListeners {
		global.APP_LOG.Warn("停止数据库中不存在或非活跃的控制端端口转发监听器",
			zap.Uint("portID", portID))
		StopControllerPortForward(portID)
	}

	repaired := 0
	for _, port := range ports {
		ctrlListenerMu.RLock()
		_, running := ctrlListeners[port.ID]
		ctrlListenerMu.RUnlock()

		if running {
			if port.Status != "active" {
				if err := global.APP_DB.Model(&providerModel.Port{}).
					Where("id = ?", port.ID).
					Update("status", "active").Error; err != nil {
					global.APP_LOG.Warn("控制端端口转发已运行但状态修正失败",
						zap.Uint("portID", port.ID),
						zap.String("status", port.Status),
						zap.Error(err))
				}
			}
			// 确认成功运行后重置失败计数
			portRepairFailMu.Lock()
			delete(portRepairFailCount, port.ID)
			portRepairFailMu.Unlock()
			continue
		}

		if hub := GetHub(); hub != nil {
			if ac, ok := hub.GetConn(port.ProviderID); !ok || ac == nil {
				// Agent 离线不是端口映射配置错误。保持 DB 中 active 状态，
				// 等 Agent 重连后 RecoverControllerPortForwardsByProvider 统一恢复。
				global.APP_LOG.Debug("控制器端口转发等待 Agent 重连后恢复",
					zap.Uint("portID", port.ID),
					zap.Uint("providerID", port.ProviderID),
					zap.Int("hostPort", port.HostPort))
				continue
			}
		}

		// 检查连续失败次数
		portRepairFailMu.Lock()
		failCount := portRepairFailCount[port.ID]
		if failCount >= maxRepairFailCount {
			portRepairFailMu.Unlock()
			// 超过阈值，标记为 error 状态以避免无限重试
			global.APP_DB.Model(&providerModel.Port{}).Where("id = ?", port.ID).
				Update("status", "error")
			global.APP_LOG.Warn("控制器端口转发连续确认失败，标记为 error",
				zap.Uint("portID", port.ID),
				zap.Int("hostPort", port.HostPort),
				zap.Int("failCount", failCount))
			continue
		}
		portRepairFailMu.Unlock()

		// 监听器未运行，尝试恢复
		targetHost := resolveTargetHost(&port)
		if targetHost == "" {
			global.APP_LOG.Warn("控制器端口转发确认失败：无目标地址",
				zap.Uint("portID", port.ID))
			continue
		}

		// 使用 RestartControllerPortForward 以获得重试逻辑
		err := RestartControllerPortForward(port.ID, port.ProviderID,
			port.HostPort, targetHost, port.GuestPort)
		if err != nil {
			global.APP_LOG.Debug("确认控制器端口转发失败",
				zap.Uint("portID", port.ID), zap.Error(err))
			// 记录失败次数
			portRepairFailMu.Lock()
			portRepairFailCount[port.ID] = failCount + 1
			portRepairFailMu.Unlock()
		} else {
			repaired++
			// 确认成功，重置失败计数
			portRepairFailMu.Lock()
			delete(portRepairFailCount, port.ID)
			portRepairFailMu.Unlock()
			global.APP_LOG.Info("已确认控制器端口转发",
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
