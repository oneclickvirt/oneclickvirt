package provider

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider/incus"
	"oneclickvirt/provider/lxd"
	providerService "oneclickvirt/service/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
	"gorm.io/gorm"
)

// storesGuestPublicIPv6 reports whether the instance itself owns a routable
// IPv6 address. Incus/LXD nat_ipv4_ipv6 normally uses a ULA guest address and
// a node-side IPv6 DNAT/proxy, but its explicit native mode assigns a routed
// public /128 to the guest. Persisting the node's shared IPv6 in managed mode
// is wrong; clearing a native allocation is equally wrong because it removes
// the only public endpoint from the API immediately after creation.
func storesGuestPublicIPv6(providerType, networkType, ipv6PortMappingMethod string) bool {
	switch strings.ToLower(strings.TrimSpace(networkType)) {
	case "dedicated_ipv4_ipv6", "ipv6_only":
		return true
	case "nat_ipv4_ipv6":
		providerType = strings.ToLower(strings.TrimSpace(providerType))
		return providerType == "proxmox" || providerType == "proxmoxve" ||
			((providerType == "incus" || providerType == "lxd" || providerType == "podman") &&
				strings.EqualFold(strings.TrimSpace(ipv6PortMappingMethod), "native"))
	default:
		return false
	}
}

func publicIPv6Update(providerType, networkType, ipv6PortMappingMethod, value string) map[string]interface{} {
	if !storesGuestPublicIPv6(providerType, networkType, ipv6PortMappingMethod) {
		// NAT modes must actively clear a stale host address, including when the
		// current probe cannot find a guest public address.
		return map[string]interface{}{"public_ipv6": ""}
	}
	if strings.TrimSpace(value) != "" {
		return map[string]interface{}{"public_ipv6": strings.TrimSpace(value)}
	}
	// A routed guest's probe may be temporarily unavailable. Do not erase a
	// known-good address and make the instance look unconfigured on a transient
	// provider/API failure; the next successful reconciliation will refresh it.
	return nil
}

func effectiveInstanceNetworkType(instanceNetworkType, providerNetworkType string) string {
	if networkType := strings.ToLower(strings.TrimSpace(instanceNetworkType)); networkType != "" {
		return networkType
	}
	return strings.ToLower(strings.TrimSpace(providerNetworkType))
}

func instanceRequiresIPv4(instanceNetworkType, providerNetworkType string) bool {
	return effectiveInstanceNetworkType(instanceNetworkType, providerNetworkType) != "ipv6_only"
}

func passwordVerificationEndpoint(instance providerModel.Instance, dbProvider providerModel.Provider, mapping *providerModel.Port) (string, int, bool) {
	switch effectiveInstanceNetworkType(instance.NetworkType, dbProvider.NetworkType) {
	case "ipv6_only":
		for _, candidate := range []string{instance.PublicIPv6, instance.IPv6Address} {
			candidate = strings.TrimSpace(candidate)
			if !utils.IsPublicIPv6(candidate) {
				continue
			}
			host, err := utils.NormalizeIPv6Address(candidate)
			if err == nil && host != "" {
				return host, 22, true
			}
		}
		return "", 0, false
	case "dedicated_ipv4", "dedicated_ipv4_ipv6":
		host := strings.TrimSpace(instance.PublicIP)
		if host == "" {
			host = strings.TrimSpace(instance.PrivateIP)
		}
		host = utils.ExtractHost(host)
		return host, 22, host != ""
	}

	if mapping != nil {
		if mapping.HostPort < 1 || mapping.HostPort > 65535 {
			return "", 0, false
		}
		// A controller connected to its local PVE through a numeric loopback
		// endpoint can reach the guest bridge directly. Public DNAT normally
		// applies to PREROUTING, so probing it from this host needs hairpin
		// rules and otherwise waits until timeout. Keep remote nodes on their
		// advertised mapping and never substitute the node's own port 22.
		if utils.NormalizeProviderType(dbProvider.Type) == "proxmox" {
			node := net.ParseIP(utils.ExtractHost(strings.TrimSpace(dbProvider.Endpoint)))
			guest := net.ParseIP(strings.TrimSpace(instance.PrivateIP))
			if node != nil && node.IsLoopback() && guest != nil && guest.To4() != nil && guest.IsPrivate() && !guest.IsLoopback() {
				return guest.String(), 22, true
			}
		}
		host := strings.TrimSpace(dbProvider.PortIP)
		if host == "" {
			host = strings.TrimSpace(dbProvider.Endpoint)
		}
		host = utils.ExtractHost(host)
		return host, mapping.HostPort, host != ""
	}

	// Without an active mapping there is no unambiguous NAT guest endpoint.
	// Never send a guest password to the provider host merely because its own
	// SSH daemon is listening on port 22.
	return "", 0, false
}

// The advertised SSH endpoint follows the active mapping for every provider.
// Incus/LXD also expose NAT ports; defaulting these providers to 22 sends a
// guest password to the node's SSH daemon instead of the container.
func applyFinalizedSSHPort(db *gorm.DB, instance providerModel.Instance, dbProvider providerModel.Provider, instanceUpdates map[string]interface{}) error {
	if db == nil {
		return fmt.Errorf("数据库不可用，保留实例SSH端口")
	}
	var sshPortMapping providerModel.Port
	err := db.Where("instance_id = ? AND is_ssh = ? AND status = ? AND protocol IN ?",
		instance.ID, true, "active", []string{"tcp", "both"}).First(&sshPortMapping).Error
	if err == nil {
		if sshPortMapping.HostPort < 1 || sshPortMapping.HostPort > 65535 {
			return fmt.Errorf("SSH映射端口无效: %d", sshPortMapping.HostPort)
		}
		instanceUpdates["ssh_port"] = sshPortMapping.HostPort
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	// Keep an existing reservation or custom guest port if the mapping is not
	// active yet. Only a direct network with no known port gets the default.
	if instance.SSHPort > 0 {
		return nil
	}
	networkType := instance.NetworkType
	if networkType == "" {
		networkType = dbProvider.NetworkType
	}
	switch networkType {
	case "dedicated_ipv4", "dedicated_ipv4_ipv6", "ipv6_only", "no_port_mapping":
		instanceUpdates["ssh_port"] = 22
	}
	return nil
}

// waitForInstanceSSHReady 智能等待实例SSH服务就绪
// 通过轮询检查SSH端口是否可连接，而不是盲目等待固定时间
func (s *Service) waitForInstanceSSHReady(ctx context.Context, instanceID, providerID, taskID uint, maxWaitTime time.Duration) error {
	return s.waitForInstanceSSHReadyInRange(ctx, instanceID, providerID, taskID, maxWaitTime, 75, 79)
}

func providerCreateSSHWaitTimeout(provider providerModel.Provider, instance providerModel.Instance) time.Duration {
	providerType := utils.NormalizeProviderType(provider.Type)
	instanceType := utils.NormalizeInstanceType(instance.InstanceType)
	if providerType == "proxmox" {
		if instanceType == "vm" {
			if provider.PveKvmAvailable != nil && !*provider.PveKvmAvailable {
				return 360 * time.Second
			}
			return 240 * time.Second
		}
		return 90 * time.Second
	}
	if instanceType != "vm" {
		return 30 * time.Second
	}
	switch {
	case providerType == "lxd" || providerType == "incus":
		return 90 * time.Second
	case providerType == "qemu" || providerType == "kubevirt" || utils.IsVMOnlyProvider(providerType):
		return 360 * time.Second
	default:
		return 180 * time.Second
	}
}

func (s *Service) waitForInstanceSSHReadyInRange(ctx context.Context, instanceID, providerID, taskID uint, maxWaitTime time.Duration, progressStart, progressEnd int) error {
	if progressEnd < progressStart {
		progressEnd = progressStart
	}
	// 获取实例信息
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		return fmt.Errorf("获取实例信息失败: %w", err)
	}

	// 获取Provider信息
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, providerID).Error; err != nil {
		return fmt.Errorf("获取Provider信息失败: %w", err)
	}

	// The readiness probe must use the same per-instance endpoint as password
	// verification; a provider default cannot override a guest's network mode.
	var mapping providerModel.Port
	var activeMapping *providerModel.Port
	err := global.APP_DB.Where("instance_id = ? AND is_ssh = true AND status = 'active' AND protocol IN ?", instanceID, []string{"tcp", "both"}).First(&mapping).Error
	if err == nil {
		activeMapping = &mapping
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("读取SSH映射失败: %w", err)
	}
	sshHost, sshPort, available := passwordVerificationEndpoint(instance, provider, activeMapping)
	if !available {
		s.updateTaskProgress(taskID, progressEnd, "step.skipSSHDetection")
		return nil
	}

	global.APP_LOG.Debug("开始等待实例 SSH服务就绪",
		zap.Uint("instanceId", instanceID),
		zap.String("instanceName", instance.Name),
		zap.String("sshHost", sshHost),
		zap.Int("sshPort", sshPort),
		zap.Duration("maxWaitTime", maxWaitTime))

	checkInterval := 5 * time.Second
	startTime := time.Now()
	attemptCount := 0

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("等待SSH服务已取消: %w", ctx.Err())
		default:
		}

		attemptCount++
		elapsed := time.Since(startTime)

		// 检查是否超时
		if elapsed >= maxWaitTime {
			return fmt.Errorf("等待SSH服务超时 (%v), 尝试次数: %d", maxWaitTime, attemptCount)
		}

		// 计算当前进度（75-79%范围内）
		progressPercent := float64(elapsed) / float64(maxWaitTime)
		currentProgress := progressStart + int(float64(progressEnd-progressStart)*progressPercent)
		if currentProgress > progressEnd {
			currentProgress = progressEnd
		}

		// 更新进度和消息
		s.updateTaskProgress(taskID, currentProgress, "step.waitingSSHReady")

		// 仅检测TCP端口连通性（不尝试密码认证，因为此时密码尚未写入实例）
		address := net.JoinHostPort(sshHost, fmt.Sprintf("%d", sshPort))
		conn, err := net.DialTimeout("tcp", address, 5*time.Second)
		if err == nil {
			conn.Close()
			global.APP_LOG.Debug("实例SSH端口已开放",
				zap.Uint("instanceId", instanceID),
				zap.String("instanceName", instance.Name),
				zap.Duration("waitTime", elapsed),
				zap.Int("attempts", attemptCount))
			s.updateTaskProgress(taskID, progressEnd, "step.sshPortOpened")
			return nil
		}

		// 连接失败，记录日志并等待重试
		global.APP_LOG.Debug("等待实例SSH就绪",
			zap.Uint("instanceId", instanceID),
			zap.String("instanceName", instance.Name),
			zap.Int("attempt", attemptCount),
			zap.Duration("elapsed", elapsed),
			zap.String("error", err.Error()))

		// 等待后重试
		select {
		case <-ctx.Done():
			return fmt.Errorf("等待SSH服务已取消: %w", ctx.Err())
		case <-time.After(checkInterval):
		}
	}
}

// ensureInstanceRunnableAfterCreate verifies that the provider still reports the
// instance as runnable after creation. This catches cases where the provider
// command returned success or partial success, but the LXD/Incus container ended
// up STOPPED/FROZEN and later password/network steps would only fail with a
// generic "container not running" error.
func (s *Service) ensureInstanceRunnableAfterCreate(ctx context.Context, instanceID, providerID, taskID uint, progress int) error {
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		return fmt.Errorf("获取实例信息失败: %w", err)
	}

	var dbProvider providerModel.Provider
	if err := global.APP_DB.First(&dbProvider, providerID).Error; err != nil {
		return fmt.Errorf("获取Provider信息失败: %w", err)
	}
	providerSvc := providerService.GetProviderService()
	providerInstance, exists := providerSvc.GetProviderByID(providerID)
	if !exists || providerInstance == nil || !providerInstance.IsConnected() {
		return fmt.Errorf("Provider %d 未连接，无法确认实例 %s 创建后状态", providerID, instance.Name)
	}

	actual, err := providerInstance.GetInstance(ctx, instance.ProviderInstanceIdentifier())
	if err != nil {
		return fmt.Errorf("创建后获取实例 %s 状态失败: %w", instance.Name, err)
	}
	if actual == nil {
		return fmt.Errorf("创建后Provider未返回实例 %s 的状态", instance.Name)
	}

	statusRaw := strings.TrimSpace(actual.Status)
	status := strings.ToLower(statusRaw)
	if status == "" {
		return nil
	}
	if status == "running" || status == "active" || status == "started" {
		return nil
	}

	badStatus := map[string]bool{
		"stopped": true,
		"stop":    true,
		"exited":  true,
		"created": true,
		"paused":  true,
		"frozen":  true,
		"failed":  true,
		"error":   true,
		"aborted": true,
	}
	if !badStatus[status] {
		global.APP_LOG.Warn("Provider返回未知实例状态，继续后处理但记录详情",
			zap.Uint("taskId", taskID),
			zap.Uint("instanceId", instanceID),
			zap.String("instanceName", instance.Name),
			zap.String("status", statusRaw))
		utils.AppendTaskLog(taskID, progress, "warn", fmt.Sprintf("step.instanceUnknownStatus:%s", statusRaw))
		return nil
	}

	diag := ""
	switch dbProvider.Type {
	case "lxd":
		if output, diagErr := providerInstance.ExecuteSSHCommand(ctx, fmt.Sprintf("lxc info %s --show-log", shellSingleQuote(instance.Name))); diagErr == nil {
			diag = output
		} else {
			diag = diagErr.Error()
		}
	case "incus":
		if output, diagErr := providerInstance.ExecuteSSHCommand(ctx, fmt.Sprintf("incus info %s --show-log", shellSingleQuote(instance.Name))); diagErr == nil {
			diag = output
		} else {
			diag = diagErr.Error()
		}
	}

	baseErr := fmt.Errorf("实例 %s 创建后未处于运行状态，Provider状态: %s", instance.Name, statusRaw)
	if strings.TrimSpace(diag) != "" {
		baseErr = fmt.Errorf("%w; diagnostics: %s", baseErr, utils.TruncateString(diag, 2000))
	}
	utils.AppendTaskError(taskID, progress, "step.instanceNotRunnable", baseErr)
	return baseErr
}

func (s *Service) ensureInstanceNetworkAddresses(ctx context.Context, instanceID, providerID uint) error {
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		return fmt.Errorf("获取实例信息失败: %w", err)
	}

	var dbProvider providerModel.Provider
	if err := global.APP_DB.First(&dbProvider, providerID).Error; err != nil {
		return fmt.Errorf("获取Provider信息失败: %w", err)
	}
	effectiveNetworkType := effectiveInstanceNetworkType(instance.NetworkType, dbProvider.NetworkType)
	ipv6Only := !instanceRequiresIPv4(instance.NetworkType, dbProvider.NetworkType)

	providerSvc := providerService.GetProviderService()
	providerInstance, exists := providerSvc.GetProviderByID(providerID)
	if !exists {
		return fmt.Errorf("provider %d 未初始化", providerID)
	}

	deadline := time.Now().Add(90 * time.Second)
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("实例网络地址补齐已取消: %w", ctx.Err())
		default:
		}

		updates := map[string]interface{}{}
		if ipv6Only {
			updates["private_ip"] = ""
			updates["pmacct_interface_v4"] = ""
		}

		switch dbProvider.Type {
		case "lxd":
			if lxdProvider, ok := providerInstance.(*lxd.LXDProvider); ok {
				if !ipv6Only {
					if ip, err := lxdProvider.GetInstanceIPv4(ctx, instance.Name); err == nil && ip != "" {
						updates["private_ip"] = ip
					}
				}
				if ipv6, err := lxdProvider.GetInstanceIPv6(instance.Name); err == nil && ipv6 != "" {
					updates["ipv6_address"] = ipv6
				}
				publicIPv6 := ""
				if candidate, err := lxdProvider.GetInstancePublicIPv6(instance.Name); err == nil {
					publicIPv6 = candidate
				}
				for key, value := range publicIPv6Update(dbProvider.Type, effectiveNetworkType, dbProvider.IPv6PortMappingMethod, publicIPv6) {
					updates[key] = value
				}
			}
		case "incus":
			if incusProvider, ok := providerInstance.(*incus.IncusProvider); ok {
				if !ipv6Only {
					if ip, err := incusProvider.GetInstanceIPv4(ctx, instance.Name); err == nil && ip != "" {
						updates["private_ip"] = ip
					}
				}
				if ipv6, err := incusProvider.GetInstanceIPv6(ctx, instance.Name); err == nil && ipv6 != "" {
					updates["ipv6_address"] = ipv6
				}
				publicIPv6 := ""
				if candidate, err := incusProvider.GetInstancePublicIPv6(ctx, instance.Name); err == nil {
					publicIPv6 = candidate
				}
				for key, value := range publicIPv6Update(dbProvider.Type, effectiveNetworkType, dbProvider.IPv6PortMappingMethod, publicIPv6) {
					updates[key] = value
				}
			}
		case "proxmox", "proxmoxve":
			s.gatherProxmoxNetworkInfo(ctx, providerInstance, &instance, &dbProvider, updates)
		case "qemu", "kubevirt", "vmware", "virtualbox", "multipass", "vagrant":
			if actualInstance, err := providerInstance.GetInstance(ctx, instance.ProviderInstanceIdentifier()); err == nil && actualInstance != nil {
				if actualInstance.PrivateIP != "" {
					updates["private_ip"] = actualInstance.PrivateIP
				} else if actualInstance.IP != "" {
					updates["private_ip"] = actualInstance.IP
				}
			}
		case "docker", "podman", "containerd", "orbstack":
			// 容器类 Provider 在实例创建时已将 private_ip 写入数据库，
			// 此处仅做幂等补齐，若已有则直接返回。
			if instance.PrivateIP != "" {
				return nil
			}
		}

		if len(updates) > 0 {
			if err := global.APP_DB.Model(&providerModel.Instance{}).Where("id = ?", instanceID).Updates(updates).Error; err != nil {
				return fmt.Errorf("更新实例网络地址失败: %w", err)
			}
			if ipv6Only {
				if publicIPv6, ok := updates["public_ipv6"].(string); ok && strings.TrimSpace(publicIPv6) != "" {
					return nil
				}
				if strings.TrimSpace(instance.PublicIPv6) != "" {
					return nil
				}
			} else if privateIP, ok := updates["private_ip"].(string); ok && privateIP != "" {
				return nil
			}
		}

		if time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("实例网络地址补齐已取消: %w", ctx.Err())
		case <-time.After(10 * time.Second):
		}
	}

	if ipv6Only {
		return fmt.Errorf("实例公网IPv6在重试窗口内仍未就绪")
	}
	return fmt.Errorf("实例内网IP在重试窗口内仍未就绪")
}

// verifySSHPasswordAuth 在密码设置完成后，通过密码认证验证SSH可用性（最多重试3次）
func (s *Service) verifySSHPasswordAuth(ctx context.Context, instanceID uint, sshHost string, sshPort int, username, password string) bool {
	address := net.JoinHostPort(sshHost, fmt.Sprintf("%d", sshPort))
	config := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	for i := 0; i < 3; i++ {
		if ctx.Err() != nil {
			return false
		}
		// ssh.ClientConfig.Timeout bounds TCP connection establishment only.
		// A peer which accepts TCP but never sends its SSH banner otherwise
		// blocks finalization indefinitely, including after task cancellation.
		dialer := net.Dialer{Timeout: 10 * time.Second}
		raw, err := dialer.DialContext(ctx, "tcp", address)
		if err == nil {
			stop := context.AfterFunc(ctx, func() { _ = raw.Close() })
			if err = raw.SetDeadline(time.Now().Add(10 * time.Second)); err == nil {
				var client ssh.Conn
				client, _, _, err = ssh.NewClientConn(raw, address, config)
				if err == nil {
					_ = client.Close()
				}
			}
			stop()
			_ = raw.Close()
			if err == nil {
				global.APP_LOG.Info("SSH密码认证验证成功",
					zap.Uint("instanceId", instanceID),
					zap.String("address", address))
				return true
			}
		}
		global.APP_LOG.Debug("SSH密码认证验证失败，等待重试",
			zap.Uint("instanceId", instanceID),
			zap.Int("attempt", i+1),
			zap.Error(err))
		if i < 2 {
			select {
			case <-ctx.Done():
				return false
			case <-time.After(5 * time.Second):
			}
		}
	}
	global.APP_LOG.Warn("SSH密码认证验证失败（密码可能未正确设置）",
		zap.Uint("instanceId", instanceID),
		zap.String("address", address))
	return false
}

// 辅助函数：创建 bool 指针
func boolPtr(b bool) *bool {
	return &b
}

// 辅助函数：创建 string 指针
func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// 辅助函数：创建 int 指针
func intPtr(i int) *int {
	if i == 0 {
		return nil
	}
	return &i
}
