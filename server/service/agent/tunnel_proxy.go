package agent

// tunnel_proxy.go — 控制端端口监听池：端口转发启停、恢复及健康修复。

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"

	"go.uber.org/zap"
	"gorm.io/gorm"
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

const (
	providerControllerRecoveryDebounce = 2 * time.Second
	providerControllerRecoveryCooldown = 15 * time.Second
)

// providerControllerRecovery coalesces explicit post-reboot repair requests.
// A recovery discovery, an Agent reconnect, and a burst of recovered starts
// can all point at the same Provider. Keep those requests to one bounded
// controller-forward rebuild instead of repeatedly tearing down live tunnels.
var providerControllerRecovery = struct {
	sync.Mutex
	pending map[uint]bool
	running map[uint]bool
	lastRun map[uint]time.Time
}{
	pending: make(map[uint]bool),
	running: make(map[uint]bool),
	lastRun: make(map[uint]time.Time),
}

// ScheduleProviderControllerPortForwardRecovery requests a full controller
// tunnel rebind for one Agent Provider. It is intentionally asynchronous: a
// manual recovery task must not hold an HTTP worker or a DB transaction while
// listener shutdown/rebind work happens.
func ScheduleProviderControllerPortForwardRecovery(providerID uint) {
	if providerID == 0 || global.APP_DB == nil {
		return
	}
	providerControllerRecovery.Lock()
	providerControllerRecovery.pending[providerID] = true
	if providerControllerRecovery.running[providerID] {
		providerControllerRecovery.Unlock()
		return
	}
	providerControllerRecovery.running[providerID] = true
	providerControllerRecovery.Unlock()

	go func() {
		for {
			providerControllerRecovery.Lock()
			lastRun := providerControllerRecovery.lastRun[providerID]
			providerControllerRecovery.Unlock()

			delay := providerControllerRecoveryDebounce
			if !lastRun.IsZero() {
				if remaining := providerControllerRecoveryCooldown - time.Since(lastRun); remaining > delay {
					delay = remaining
				}
			}
			if !waitForAgentShutdown(delay) {
				providerControllerRecovery.Lock()
				delete(providerControllerRecovery.pending, providerID)
				delete(providerControllerRecovery.running, providerID)
				providerControllerRecovery.Unlock()
				return
			}

			providerControllerRecovery.Lock()
			pending := providerControllerRecovery.pending[providerID]
			delete(providerControllerRecovery.pending, providerID)
			providerControllerRecovery.Unlock()
			if pending {
				EnsureControllerPortForwardsByProvider(providerID)
			}

			providerControllerRecovery.Lock()
			providerControllerRecovery.lastRun[providerID] = time.Now()
			if !providerControllerRecovery.pending[providerID] {
				delete(providerControllerRecovery.running, providerID)
				providerControllerRecovery.Unlock()
				return
			}
			providerControllerRecovery.Unlock()
		}
	}()
}

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
	return startControllerPortForwardWithKnownPort(port, targetHost)
}

// startControllerPortForwardWithKnownPort avoids an immediate per-port read
// when a caller has just loaded and validated a batch of controller mappings.
// It is intentionally private: public callers must retain the defensive
// database validation in StartControllerPortForward.
func startControllerPortForwardWithKnownPort(port providerModel.Port, targetHost string) error {
	if port.ID == 0 || port.ProviderID == 0 || port.HostPort <= 0 || port.GuestPort <= 0 ||
		port.MappingType != "controller" || (port.Status != "active" && port.Status != "pending") {
		return fmt.Errorf("controller port %d metadata mismatch or inactive", port.ID)
	}
	portID := port.ID
	providerID := port.ProviderID
	listenPort := port.HostPort
	targetPort := port.GuestPort

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

	// An already active controller mapping has just been read and validated by
	// the caller. Avoid turning a provider-wide recovery into one redundant DB
	// UPDATE per port; pending/misconfigured rows still transition atomically.
	if port.Status != "active" || port.MappingMethod != "controller" {
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
		case <-agentShutdownContext().Done():
			global.APP_LOG.Debug("主控关闭，中断等待控制端端口转发停止",
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
	if !waitForAgentShutdown(200 * time.Millisecond) {
		return context.Canceled
	}

	// 带重试的启动（处理端口尚未完全释放的情况）
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			if !waitForAgentShutdown(time.Duration(attempt) * 500 * time.Millisecond) {
				return context.Canceled
			}
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

// restartControllerPortForwardWithKnownPort is the batch counterpart of
// RestartControllerPortForward. The mapping row was read as part of the
// surrounding batch, so it avoids adding a second startup lookup per port
// while retaining the same bounded bind retry behavior.
func restartControllerPortForwardWithKnownPort(port providerModel.Port, targetHost string) error {
	StopControllerPortForward(port.ID)
	if !waitForAgentShutdown(200 * time.Millisecond) {
		return context.Canceled
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			if !waitForAgentShutdown(time.Duration(attempt) * 500 * time.Millisecond) {
				return context.Canceled
			}
		}
		err := startControllerPortForwardWithKnownPort(port, targetHost)
		if err == nil {
			return nil
		}
		if !strings.Contains(err.Error(), "address already in use") {
			return err
		}
		lastErr = err
		global.APP_LOG.Debug("端口仍被占用，批量重绑重试中",
			zap.Uint("portID", port.ID),
			zap.Int("attempt", attempt+1),
			zap.Int("listenPort", port.HostPort))
	}
	return fmt.Errorf("批量重绑端口转发失败（已重试3次）: %w", lastErr)
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

const (
	controllerPortRecoveryReadBatchSize  = 200
	controllerPortRecoveryWriteBatchSize = 50
	controllerPortRecoveryConcurrency    = 4
)

// EnsureControllerPortForwardsByProvider restores only missing active/pending
// controller forwards. Existing listeners are left untouched, which makes
// periodic checks and a normal Agent reconnect idempotent and non-disruptive.
// Database reads and target updates are bounded pages; no transaction spans
// listener work.
func EnsureControllerPortForwardsByProvider(providerID uint) {
	if global.APP_DB == nil || providerID == 0 {
		return
	}
	recoveryMu.Lock()
	defer recoveryMu.Unlock()

	total := 0
	started := 0
	skipped := 0
	var afterID uint
	for {
		ports, err := loadControllerRecoveryPortBatch(providerID, afterID)
		if err != nil {
			global.APP_LOG.Error("查询待补齐的控制器端口转发失败",
				zap.Uint("providerID", providerID), zap.Error(err))
			return
		}
		if len(ports) == 0 {
			break
		}
		total += len(ports)
		targets, err := resolveControllerRecoveryTargets(ports)
		if err != nil {
			global.APP_LOG.Warn("批量解析控制器端口转发目标失败",
				zap.Uint("providerID", providerID), zap.Error(err))
		}
		batchStarted, batchSkipped := startControllerRecoveryPortBatch(ports, targets)
		started += batchStarted
		skipped += batchSkipped
		afterID = ports[len(ports)-1].ID
	}
	if total > 0 {
		global.APP_LOG.Info("控制器端口转发补齐完成",
			zap.Uint("providerID", providerID),
			zap.Int("started", started), zap.Int("skipped", skipped), zap.Int("total", total))
	}
}

// RecoverControllerPortForwardsByProvider is kept as a compatibility entry
// point for older callers. Recovery is intentionally non-destructive; callers
// which know that an Agent connection was replaced should use the explicit
// RebuildControllerPortForwardsByProvider path below.
func RecoverControllerPortForwardsByProvider(providerID uint) {
	EnsureControllerPortForwardsByProvider(providerID)
}

// RebuildControllerPortForwardsByProvider performs the destructive stop and
// rebind required when an existing Agent connection is replaced. It is never
// used by periodic health checks, so healthy listeners are not repeatedly torn
// down during ordinary reconciliation.
func RebuildControllerPortForwardsByProvider(providerID uint) {
	if global.APP_DB == nil || providerID == 0 {
		return
	}
	// 互斥：与 StopControllerPortForwardsByProvider 互斥，防止并发操作
	// 同一 Provider 的监听器导致竞态条件。
	recoveryMu.Lock()
	defer recoveryMu.Unlock()

	total := 0
	stopped := 0
	var afterID uint
	for {
		ports, err := loadControllerRecoveryPortBatch(providerID, afterID)
		if err != nil {
			global.APP_LOG.Error("查询待恢复的控制器端口转发失败",
				zap.Uint("providerID", providerID), zap.Error(err))
			return
		}
		if len(ports) == 0 {
			break
		}
		total += len(ports)
		stopped += stopControllerRecoveryPortBatch(ports)
		afterID = ports[len(ports)-1].ID
	}
	if total == 0 {
		return
	}

	global.APP_LOG.Info("开始重建控制器端口转发",
		zap.Uint("providerID", providerID), zap.Int("count", total))
	RemoveTunnelManager(providerID)
	if !waitForAgentShutdown(200 * time.Millisecond) {
		return
	}

	recovered := 0
	skipped := 0
	afterID = 0
	for {
		ports, err := loadControllerRecoveryPortBatch(providerID, afterID)
		if err != nil {
			global.APP_LOG.Error("重新查询控制器端口转发失败",
				zap.Uint("providerID", providerID), zap.Error(err))
			break
		}
		if len(ports) == 0 {
			break
		}
		targets, err := resolveControllerRecoveryTargets(ports)
		if err != nil {
			global.APP_LOG.Warn("批量解析控制器端口转发目标失败",
				zap.Uint("providerID", providerID), zap.Error(err))
		}
		started, omitted := startControllerRecoveryPortBatch(ports, targets)
		recovered += started
		skipped += omitted
		afterID = ports[len(ports)-1].ID
	}

	global.APP_LOG.Info("控制器端口转发重建完成",
		zap.Uint("providerID", providerID),
		zap.Int("stopped", stopped), zap.Int("recovered", recovered),
		zap.Int("skipped", skipped), zap.Int("total", total))
}

func loadControllerRecoveryPortBatch(providerID, afterID uint) ([]providerModel.Port, error) {
	var ports []providerModel.Port
	err := global.APP_DB.Model(&providerModel.Port{}).
		Select("id", "provider_id", "instance_id", "host_port", "guest_port", "mapping_type", "mapping_method", "status", "internal_host").
		Where("provider_id = ? AND mapping_type = ? AND status IN ? AND id > ?", providerID, "controller", controllerPortRecoverStatuses, afterID).
		Order("id ASC").
		Limit(controllerPortRecoveryReadBatchSize).
		Find(&ports).Error
	return ports, err
}

func stopControllerRecoveryPortBatch(ports []providerModel.Port) int {
	if len(ports) == 0 {
		return 0
	}
	workers := controllerPortRecoveryConcurrency
	if workers > len(ports) {
		workers = len(ports)
	}
	jobs := make(chan providerModel.Port)
	var wg sync.WaitGroup
	var stoppedMu sync.Mutex
	stopped := 0
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for port := range jobs {
				if IsControllerPortForwardRunning(port.ID) {
					stoppedMu.Lock()
					stopped++
					stoppedMu.Unlock()
				}
				StopControllerPortForward(port.ID)
			}
		}()
	}
	for _, port := range ports {
		jobs <- port
	}
	close(jobs)
	wg.Wait()
	return stopped
}

// resolveControllerRecoveryTargets reads all runtime addresses needed by a
// page of controller mappings at once. Explicit hostnames are never replaced;
// IP-style and empty targets are refreshed from the authoritative instance IP.
func resolveControllerRecoveryTargets(ports []providerModel.Port) (map[uint]string, error) {
	targets := make(map[uint]string, len(ports))
	instanceIDs := make([]uint, 0, len(ports))
	seenInstanceIDs := make(map[uint]struct{}, len(ports))
	for _, port := range ports {
		internalHost := strings.TrimSpace(port.InternalHost)
		if internalHost != "" && !looksLikeIP(internalHost) {
			targets[port.ID] = internalHost
			continue
		}
		if port.InstanceID == 0 {
			if target, _ := ResolveControllerPortTarget(internalHost, ""); target != "" {
				targets[port.ID] = target
			}
			continue
		}
		if _, exists := seenInstanceIDs[port.InstanceID]; !exists {
			seenInstanceIDs[port.InstanceID] = struct{}{}
			instanceIDs = append(instanceIDs, port.InstanceID)
		}
	}
	if len(instanceIDs) == 0 {
		return targets, nil
	}

	var instances []struct {
		ID        uint
		PrivateIP string
	}
	if err := global.APP_DB.Model(&providerModel.Instance{}).
		Select("id", "private_ip").
		Where("id IN ?", instanceIDs).
		Find(&instances).Error; err != nil {
		return targets, err
	}
	privateIPs := make(map[uint]string, len(instances))
	for _, instance := range instances {
		privateIPs[instance.ID] = instance.PrivateIP
	}

	updates := make(map[uint]string)
	for _, port := range ports {
		if _, alreadyResolved := targets[port.ID]; alreadyResolved {
			continue
		}
		target, shouldUpdate := ResolveControllerPortTarget(port.InternalHost, privateIPs[port.InstanceID])
		if target == "" {
			continue
		}
		targets[port.ID] = target
		if shouldUpdate {
			updates[port.ID] = target
		}
	}
	if len(updates) == 0 {
		return targets, nil
	}
	for _, failedPortID := range persistControllerRecoveryTargets(updates) {
		// Do not start a listener whose new target did not commit. Its dynamic
		// resolver would otherwise read the stale address and route traffic to
		// the old guest until the next repair pass.
		delete(targets, failedPortID)
	}
	return targets, nil
}

func persistControllerRecoveryTargets(updates map[uint]string) []uint {
	portIDs := make([]uint, 0, len(updates))
	for portID := range updates {
		portIDs = append(portIDs, portID)
	}
	sort.Slice(portIDs, func(i, j int) bool { return portIDs[i] < portIDs[j] })
	failed := make([]uint, 0)
	for start := 0; start < len(portIDs); start += controllerPortRecoveryWriteBatchSize {
		end := start + controllerPortRecoveryWriteBatchSize
		if end > len(portIDs) {
			end = len(portIDs)
		}
		batchIDs := portIDs[start:end]
		var builder strings.Builder
		builder.WriteString("CASE id")
		args := make([]interface{}, 0, len(batchIDs)*2)
		for _, portID := range batchIDs {
			builder.WriteString(" WHEN ? THEN ?")
			args = append(args, portID, updates[portID])
		}
		builder.WriteString(" ELSE internal_host END")
		if err := global.APP_DB.Model(&providerModel.Port{}).
			Where("id IN ?", batchIDs).
			Update("internal_host", gorm.Expr(builder.String(), args...)).Error; err != nil {
			global.APP_LOG.Warn("批量更新控制器端口转发目标地址失败", zap.Error(err))
			failed = append(failed, batchIDs...)
		}
	}
	return failed
}

func startControllerRecoveryPortBatch(ports []providerModel.Port, targets map[uint]string) (int, int) {
	workers := controllerPortRecoveryConcurrency
	if workers > len(ports) {
		workers = len(ports)
	}
	if workers == 0 {
		return 0, 0
	}
	jobs := make(chan providerModel.Port)
	var wg sync.WaitGroup
	var resultMu sync.Mutex
	started := 0
	skipped := 0
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for port := range jobs {
				targetHost := targets[port.ID]
				if targetHost == "" {
					global.APP_LOG.Warn("控制器端口转发恢复失败：无目标地址",
						zap.Uint("portID", port.ID), zap.Uint("instanceID", port.InstanceID))
					resultMu.Lock()
					skipped++
					resultMu.Unlock()
					continue
				}
				if err := startControllerPortForwardWithKnownPort(port, targetHost); err != nil {
					global.APP_LOG.Warn("恢复控制器端口转发失败",
						zap.Uint("portID", port.ID), zap.Error(err))
					resultMu.Lock()
					skipped++
					resultMu.Unlock()
					continue
				}
				resultMu.Lock()
				started++
				resultMu.Unlock()
			}
		}()
	}
	for _, port := range ports {
		jobs <- port
	}
	close(jobs)
	wg.Wait()
	return started, skipped
}

// RecoverAllControllerPortForwards 恢复所有活跃的控制端端口转发。
// 在控制端启动时调用，尝试恢复所有之前活跃的控制器端口转发。
// 对于 Agent 尚未上线的 Provider，监听器会等待 Agent 连接后生效。
func RecoverAllControllerPortForwards() {
	if global.APP_DB == nil {
		return
	}
	providerCount := 0
	var afterProviderID uint
	for {
		var rows []struct{ ProviderID uint }
		if err := global.APP_DB.Model(&providerModel.Port{}).
			Distinct("provider_id").
			Select("provider_id").
			Where("mapping_type = ? AND status IN ? AND provider_id > ?", "controller", controllerPortRecoverStatuses, afterProviderID).
			Order("provider_id ASC").
			Limit(controllerPortRecoveryReadBatchSize).
			Find(&rows).Error; err != nil {
			global.APP_LOG.Error("查询所有待恢复的控制器端口Provider失败", zap.Error(err))
			return
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			if row.ProviderID == 0 {
				continue
			}
			providerCount++
			EnsureControllerPortForwardsByProvider(row.ProviderID)
		}
		afterProviderID = rows[len(rows)-1].ProviderID
	}
	if providerCount == 0 {
		global.APP_LOG.Debug("没有需要恢复的控制器端口转发")
		return
	}
	global.APP_LOG.Info("所有控制器端口转发恢复完成", zap.Int("providers", providerCount))
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
