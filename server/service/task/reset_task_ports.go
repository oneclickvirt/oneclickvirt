package task

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	systemModel "oneclickvirt/model/system"
	providerCore "oneclickvirt/provider"
	"oneclickvirt/provider/portmapping"
	traffic_monitor "oneclickvirt/service/admin/traffic_monitor"
	agentLifecycle "oneclickvirt/service/agent"
	"oneclickvirt/service/firewall"
	provider2 "oneclickvirt/service/provider"
	"oneclickvirt/service/resources"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// resetTask_RestorePortMappings restores already-reserved rows. Remote work is
// outside transactions, and only verified mappings become active.
func (s *TaskService) resetTask_RestorePortMappings(ctx context.Context, task *adminModel.Task, resetCtx *ResetTaskContext) error {
	s.updateTaskProgress(task.ID, 88, "step.restoringPortMappings")
	if err := ctx.Err(); err != nil {
		return err
	}
	var allPorts []providerModel.Port
	if err := global.APP_DB.WithContext(ctx).Where("instance_id = ? AND provider_id = ?", resetCtx.NewInstanceID, resetCtx.Provider.ID).Order("id").Find(&allPorts).Error; err != nil {
		return fmt.Errorf("读取重建端口占用失败: %w", err)
	}
	if len(allPorts) == 0 && len(resetCtx.OldPortMappings) > 0 {
		return fmt.Errorf("重建端口预留丢失，拒绝覆盖可能已被其他实例占用的端口")
	}
	if len(allPorts) == 0 {
		networkType := resetCtx.Instance.NetworkType
		if networkType == "" {
			networkType = resetCtx.Provider.NetworkType
		}
		switch networkType {
		case "dedicated_ipv4", "dedicated_ipv4_ipv6", "ipv6_only", "no_port_mapping":
			return nil
		}
		// Only controller-mode defaults may legitimately be deferred until
		// now: their private target address is known only after guest creation.
		if resetCtx.Provider.ConnectionType == "agent" && resetCtx.Provider.PortIP == "" && resetCtx.NewPrivateIP == "" {
			prov, _, err := (&provider2.ProviderApiService{}).GetProviderByID(resetCtx.Provider.ID)
			if err != nil {
				return err
			}
			resetCtx.NewPrivateIP = getInstancePrivateIP(ctx, prov, resetCtx.Provider.Type, resetCtx.OldInstanceName)
			if resetCtx.NewPrivateIP == "" {
				return fmt.Errorf("控制端默认映射缺少重建实例地址")
			}
			if err := global.APP_DB.WithContext(ctx).Model(&providerModel.Instance{}).Where("id = ?", resetCtx.NewInstanceID).Update("private_ip", resetCtx.NewPrivateIP).Error; err != nil {
				return err
			}
		}
		if err := (&resources.PortMappingService{}).ReserveDefaultPortMappingsForReset(ctx, resetCtx.NewInstanceID, resetCtx.Provider.ID, networkType); err != nil {
			return fmt.Errorf("创建重建默认端口失败: %w", err)
		}
		if err := global.APP_DB.WithContext(ctx).Where("instance_id = ? AND provider_id = ?", resetCtx.NewInstanceID, resetCtx.Provider.ID).Find(&allPorts).Error; err != nil {
			return err
		}
	}
	var ports []providerModel.Port
	for _, port := range allPorts {
		if port.Status == "restoring" {
			ports = append(ports, port)
		}
	}
	if len(ports) == 0 {
		return nil // Preserve intentionally inactive/failed mappings.
	}
	prov, _, loadErr := (&provider2.ProviderApiService{}).GetProviderByID(resetCtx.Provider.ID)
	if loadErr != nil {
		return errors.Join(loadErr, s.persistResetPortResults(ctx, resetCtx, ports, nil, loadErr))
	}
	return s.restoreReservedPortMappings(ctx, prov, resetCtx, ports)
}

func (s *TaskService) restoreReservedPortMappings(ctx context.Context, prov providerCore.Provider, resetCtx *ResetTaskContext, ports []providerModel.Port) error {
	if err := ctx.Err(); err != nil {
		return errors.Join(err, s.persistResetPortResults(ctx, resetCtx, ports, nil, err))
	}
	name := resetCtx.NewProviderInstanceID
	if name == "" {
		name = resetCtx.OldInstanceName
	}
	// Only look up IPv4 when it is actually needed; IPv6-only/native mappings
	// must not wait for a lease they will never receive.
	needIPv4 := false
	for _, port := range ports {
		if port.MappingType == "controller" && (strings.TrimSpace(port.InternalHost) == "" || net.ParseIP(strings.Trim(port.InternalHost, "[]")) != nil) {
			needIPv4 = true
		}
	}
	if needIPv4 && resetCtx.NewPrivateIP == "" {
		resetCtx.NewPrivateIP = getInstancePrivateIP(ctx, prov, resetCtx.Provider.Type, name)
	}
	failures := make(map[uint]error)
	var nodePorts, controllerPorts []providerModel.Port
	for _, port := range ports {
		if port.MappingType == "controller" {
			if _, err := expandPortEndpoints(port); err != nil || effectivePortCount(port) != 1 || !strings.EqualFold(port.Protocol, "tcp") {
				failures[port.ID] = fmt.Errorf("控制端端口 %d 必须是单个TCP映射", port.HostPort)
				continue
			}
			target := port.InternalHost
			if strings.TrimSpace(target) == "" || net.ParseIP(strings.Trim(target, "[]")) != nil {
				target = resetCtx.NewPrivateIP // Never fall back to a deleted guest IP.
			}
			if target == "" {
				failures[port.ID] = fmt.Errorf("控制端端口 %d 缺少重建后的目标地址", port.HostPort)
				continue
			}
			port.InternalHost = target
			controllerPorts = append(controllerPorts, port)
		} else {
			nodePorts = append(nodePorts, port)
		}
	}
	var nodeErr error
	if len(nodePorts) > 0 {
		if utils.UsesContainerRuntimePorts(resetCtx.Provider.Type, resetCtx.Instance.InstanceType) {
			nodeErr = verifyContainerRuntimePortMappings(ctx, prov, resetCtx.Provider.Type, name, nodePorts)
			if nodeErr != nil {
				for _, port := range nodePorts {
					failures[port.ID] = nodeErr
				}
			}
		} else if !utils.UsesVMPositionalPorts(resetCtx.Provider.Type, resetCtx.Instance.InstanceType) {
			copyCtx := *resetCtx
			copyCtx.OldPortMappings = nodePorts
			nodeFailures, err := s.configureProviderPortMappingsDetailed(ctx, prov, &copyCtx)
			resetCtx.NewPrivateIP = copyCtx.NewPrivateIP
			resetCtx.NewGuestIPv6 = copyCtx.NewGuestIPv6
			nodeErr = err
			for _, port := range nodePorts {
				if failure := nodeFailures[port.ID]; failure != nil {
					failures[port.ID] = failure
				} else if nodeFailures == nil && err != nil {
					failures[port.ID] = err
				}
			}
		}
	}
	controllerFailures := agentLifecycle.RestoreControllerPortForwards(ctx, resetCtx.NewInstanceID, resetCtx.Provider.ID, controllerPorts)
	for id, err := range controllerFailures {
		failures[id] = err
	}
	var errs []error
	if nodeErr != nil {
		errs = append(errs, nodeErr)
	}
	for _, err := range failures {
		errs = append(errs, err)
	}
	applyErr := errors.Join(errs...)
	return errors.Join(applyErr, s.persistResetPortResults(ctx, resetCtx, ports, failures, nil))
}

// Persist in two bounded batches, not one transaction/update per mapping.
// Cancellation still needs a short independent transaction to record outcomes.
func (s *TaskService) persistResetPortResults(ctx context.Context, resetCtx *ResetTaskContext, ports []providerModel.Port, failures map[uint]error, allFailed error) error {
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	var activeIDs, failedIDs []uint
	sshPort := 22
	for _, port := range ports {
		if allFailed != nil || failures[port.ID] != nil {
			failedIDs = append(failedIDs, port.ID)
		} else {
			activeIDs = append(activeIDs, port.ID)
			if port.IsSSH {
				sshPort = port.HostPort
			}
		}
	}
	return s.dbService.ExecuteTransaction(saveCtx, func(tx *gorm.DB) error {
		for _, group := range []struct {
			ids    []uint
			status string
		}{{activeIDs, "active"}, {failedIDs, "failed"}} {
			if len(group.ids) == 0 {
				continue
			}
			if err := tx.Model(&providerModel.Port{}).Where("id IN ? AND instance_id = ? AND provider_id = ? AND status IN ?", group.ids, resetCtx.NewInstanceID, resetCtx.Provider.ID, []string{"restoring", "pending", "active"}).Update("status", group.status).Error; err != nil {
				return fmt.Errorf("保存重建端口状态失败: %w", err)
			}
		}
		updates := map[string]interface{}{"ssh_port": sshPort}
		if resetCtx.NewPrivateIP != "" {
			updates["private_ip"] = resetCtx.NewPrivateIP
		}
		if resetCtx.NewGuestIPv6 != "" {
			updates["ipv6_address"] = resetCtx.NewGuestIPv6
			// Empty addresses on legacy automatic rows mean dual-stack. Only
			// replace an explicit binding that pointed at the deleted guest.
			if resetCtx.Instance.IPv6Address != "" {
				if err := tx.Model(&providerModel.Port{}).Where("instance_id = ? AND provider_id = ? AND ipv6_address = ?", resetCtx.NewInstanceID, resetCtx.Provider.ID, resetCtx.Instance.IPv6Address).Update("ipv6_address", resetCtx.NewGuestIPv6).Error; err != nil {
					return err
				}
			}
		}
		return tx.Model(&providerModel.Instance{}).Where("id = ? AND provider_id = ?", resetCtx.NewInstanceID, resetCtx.Provider.ID).Updates(updates).Error
	})
}

// createPortMappingDirect 直接创建端口映射（绕过任务系统）
func (s *TaskService) createPortMappingDirect(ctx context.Context, resetCtx *ResetTaskContext, oldPort providerModel.Port) error {
	// 获取Provider实例（暂时不需要直接使用prov）
	// portmapping.Manager会自动处理provider连接

	// 确定端口映射类型
	portMappingType := resetCtx.Provider.Type
	if portMappingType == "proxmox" || portMappingType == "proxmoxve" {
		portMappingType = "iptables"
	}

	// 使用portmapping管理器创建端口映射
	manager := portmapping.NewManager(&portmapping.ManagerConfig{
		DefaultMappingMethod: resetCtx.Provider.IPv4PortMappingMethod,
	})

	portReq := &portmapping.PortMappingRequest{
		InstanceID:    fmt.Sprintf("%d", resetCtx.NewInstanceID),
		ProviderID:    resetCtx.Provider.ID,
		Protocol:      oldPort.Protocol,
		HostPort:      oldPort.HostPort,
		GuestPort:     oldPort.GuestPort,
		Description:   oldPort.Description,
		MappingMethod: resetCtx.Provider.IPv4PortMappingMethod,
		IsSSH:         &oldPort.IsSSH,
	}

	// 创建端口映射（在远程服务器上）
	result, err := manager.CreatePortMapping(ctx, portMappingType, portReq)
	if err != nil {
		// 即使远程创建失败，也尝试创建数据库记录（状态为failed）
		s.dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
			newPort := providerModel.Port{
				InstanceID:    resetCtx.NewInstanceID,
				ProviderID:    resetCtx.Provider.ID,
				HostPort:      oldPort.HostPort,
				GuestPort:     oldPort.GuestPort,
				Protocol:      oldPort.Protocol,
				Description:   oldPort.Description,
				Status:        "failed",
				IsSSH:         oldPort.IsSSH,
				IsAutomatic:   oldPort.IsAutomatic,
				PortType:      oldPort.PortType,
				MappingMethod: oldPort.MappingMethod,
				IPv6Enabled:   oldPort.IPv6Enabled,
			}
			return tx.Create(&newPort).Error
		})
		return fmt.Errorf("在远程服务器上创建端口映射失败: %v", err)
	}

	global.APP_LOG.Debug("端口映射已应用到远程服务器",
		zap.Uint("portId", result.ID),
		zap.Int("hostPort", result.HostPort),
		zap.Int("guestPort", result.GuestPort))

	return nil
}

// resetTask_ReinitializeMonitoring 阶段8: 重新初始化监控
func (s *TaskService) resetTask_ReinitializeMonitoring(ctx context.Context, task *adminModel.Task, resetCtx *ResetTaskContext) error {
	s.updateTaskProgress(task.ID, 96, "step.reinitMonitoringService")

	// 检查是否启用流量控制
	var providerTrafficEnabled bool
	err := s.dbService.ExecuteQuery(ctx, func() error {
		var dbProvider providerModel.Provider
		if err := global.APP_DB.Select("enable_traffic_control").Where("id = ?", resetCtx.Provider.ID).
			First(&dbProvider).Error; err != nil {
			return err
		}
		providerTrafficEnabled = dbProvider.EnableTrafficControl
		return nil
	})

	if err != nil || !providerTrafficEnabled {
		return nil
	}

	// 使用统一的流量监控管理器重新初始化pmacct
	trafficMonitorManager := traffic_monitor.GetManager()
	if err := trafficMonitorManager.AttachMonitor(ctx, resetCtx.NewInstanceID); err != nil {
		global.APP_LOG.Warn("重新初始化流量监控失败", zap.Error(err))
	} else {
		global.APP_LOG.Debug("流量监控重新初始化成功",
			zap.Uint("instanceId", resetCtx.NewInstanceID))
	}

	// Agent监控：迁移旧监控记录到新实例（保留流量历史连续性）
	if resetCtx.OldInstanceID != 0 && resetCtx.OldInstanceID != resetCtx.NewInstanceID {
		var oldMonitor monitoringModel.AgentMonitor
		if err := global.APP_DB.Where("instance_id = ?", resetCtx.OldInstanceID).First(&oldMonitor).Error; err == nil {
			// 迁移 agent_monitor 到新实例ID
			if err := global.APP_DB.Model(&oldMonitor).Updates(map[string]interface{}{
				"instance_id":   resetCtx.NewInstanceID,
				"instance_name": resetCtx.OldInstanceName,
			}).Error; err != nil {
				global.APP_LOG.Warn("迁移agent监控记录失败", zap.Error(err))
			} else {
				global.APP_LOG.Info("已迁移agent监控记录到新实例",
					zap.Uint("old_instance_id", resetCtx.OldInstanceID),
					zap.Uint("new_instance_id", resetCtx.NewInstanceID))
			}

			// 迁移流量历史记录到新实例ID
			global.APP_DB.Model(&monitoringModel.InstanceTrafficHistory{}).
				Where("instance_id = ?", resetCtx.OldInstanceID).
				Update("instance_id", resetCtx.NewInstanceID)

			// 迁移资源监控记录到新实例ID（保留硬件监控历史连续性）
			global.APP_DB.Model(&monitoringModel.ResourceMetric{}).
				Where("instance_id = ?", resetCtx.OldInstanceID).
				Update("instance_id", resetCtx.NewInstanceID)

			// 迁移pmacct流量记录到新实例ID（保留流量监控历史连续性）
			global.APP_DB.Model(&monitoringModel.PmacctTrafficRecord{}).
				Where("instance_id = ?", resetCtx.OldInstanceID).
				Update("instance_id", resetCtx.NewInstanceID)
		}
	}

	// Agent监控：重建后更新接口
	agentCtx, agentCancel := context.WithTimeout(ctx, 2*time.Minute)
	agentLifecycle.OnInstanceRebuilt(agentCtx, global.APP_DB, resetCtx.NewInstanceID)
	agentCancel()

	// 迁移封禁规则应用从旧实例到新实例（保留封禁规则连续性）
	if resetCtx.OldInstanceID != 0 && resetCtx.OldInstanceID != resetCtx.NewInstanceID {
		firewall.MigrateInstanceApplications(resetCtx.OldInstanceID, resetCtx.NewInstanceID)
	}
	// 重新同步所有Provider的封禁规则（确保重建后规则实际应用）
	go firewall.ResyncAllProviders()

	// 更新兑换码关联的实例ID到新实例（保持兑换码链接有效）
	if resetCtx.OldInstanceID != 0 && resetCtx.OldInstanceID != resetCtx.NewInstanceID {
		result := global.APP_DB.Model(&systemModel.RedemptionCode{}).
			Where("instance_id = ?", resetCtx.OldInstanceID).
			Update("instance_id", resetCtx.NewInstanceID)
		if result.Error != nil {
			global.APP_LOG.Warn("更新兑换码关联实例失败", zap.Error(result.Error))
		} else if result.RowsAffected > 0 {
			global.APP_LOG.Info("已更新兑换码关联实例",
				zap.Uint("old_instance_id", resetCtx.OldInstanceID),
				zap.Uint("new_instance_id", resetCtx.NewInstanceID),
				zap.Int64("count", result.RowsAffected))
		}
	}

	return nil
}

// configureProviderPortMappings 配置Provider层的端口映射（实际在远程服务器上创建proxy device）
func (s *TaskService) configureProviderPortMappings(ctx context.Context, prov interface{}, resetCtx *ResetTaskContext) error {
	_, err := s.configureProviderPortMappingsDetailed(ctx, prov, resetCtx)
	return err
}

func (s *TaskService) configureProviderPortMappingsDetailed(ctx context.Context, prov interface{}, resetCtx *ResetTaskContext) (map[uint]error, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if resetCtx == nil {
		return nil, fmt.Errorf("重建上下文为空")
	}
	switch utils.NormalizeProviderType(resetCtx.Provider.Type) {
	case "incus", "lxd", "proxmox":
	default:
		// These runtimes bind ports during creation, not in this restoration step.
		return nil, nil
	}
	networkType := resetCtx.Instance.NetworkType
	if networkType == "" {
		networkType = resetCtx.Provider.NetworkType
	}
	var mappings []providerModel.Port
	for _, port := range resetCtx.OldPortMappings {
		for _, mapping := range providerModel.ExpandPortMappingFamilies(port, networkType,
			resetCtx.Provider.IPv4PortMappingMethod, resetCtx.Provider.IPv6PortMappingMethod) {
			mapping.MappingMethod = normalizePortMappingMethod(mapping.MappingMethod)
			if mapping.MappingType == "controller" || mapping.MappingMethod == "native" {
				continue
			}
			if mapping.MappingMethod == "" {
				mapping.MappingMethod = "device_proxy"
				if utils.NormalizeProviderType(resetCtx.Provider.Type) == "proxmox" {
					mapping.MappingMethod = "iptables"
				}
			}
			if _, err := expandPortEndpoints(mapping); err != nil {
				return nil, fmt.Errorf("重建端口 %d 配置无效: %w", mapping.HostPort, err)
			}
			mapping.Protocol = strings.ToLower(strings.TrimSpace(mapping.Protocol))
			if mapping.Protocol == "" {
				mapping.Protocol = "tcp"
			}
			if mapping.Protocol != "tcp" && mapping.Protocol != "udp" && mapping.Protocol != "both" {
				return nil, fmt.Errorf("重建端口 %d 协议无效: %q", mapping.HostPort, mapping.Protocol)
			}
			mappings = append(mappings, mapping)
		}
	}
	if len(mappings) == 0 {
		return nil, nil
	}
	instanceName := resetCtx.NewProviderInstanceID
	if strings.TrimSpace(instanceName) == "" {
		instanceName = resetCtx.OldInstanceName
	}
	if strings.EqualFold(strings.TrimSpace(resetCtx.Provider.ExecutionRule), "api_only") && (resetCtx.Provider.Type == "incus" || resetCtx.Provider.Type == "lxd") {
		batch, ok := prov.(interface {
			ConfigurePortMappingsAPI(context.Context, string, []providerModel.Port) error
		})
		if !ok {
			return nil, fmt.Errorf("Provider不支持API重建端口映射")
		}
		for index := range mappings {
			if mappings[index].IPv6Address == resetCtx.Instance.IPv6Address {
				mappings[index].IPv6Address = ""
			}
		}
		return nil, batch.ConfigurePortMappingsAPI(ctx, instanceName, mappings)
	}
	setter, ok := prov.(interface {
		SetupPortMappingWithIP(context.Context, string, int, int, string, string, string) error
	})
	if !ok {
		return nil, fmt.Errorf("Provider不支持重建端口映射: %s", resetCtx.Provider.Type)
	}
	// Cache both success and failure once per family. Never fall back to the old
	// guest IP or use IPv6 for a missing IPv4 lease after the guest was replaced.
	ipv4, ipv6 := strings.TrimSpace(resetCtx.NewPrivateIP), ""
	ipv4Read, ipv6Read := ipv4 != "", false
	var failures []error
	failedPorts := make(map[uint]error)
	failPort := func(id uint, err error) {
		failedPorts[id] = errors.Join(failedPorts[id], err)
		failures = append(failures, err)
	}
	needsSave := false
	for _, port := range mappings {
		if err := ctx.Err(); err != nil {
			failPort(port.ID, err)
			continue
		}
		isIPv6 := port.IPv6Enabled || strings.TrimSpace(port.IPv6Address) != ""
		target := strings.TrimSpace(port.IPv6Address)
		if isIPv6 {
			if target == "" || target == strings.TrimSpace(resetCtx.Instance.IPv6Address) {
				if !ipv6Read {
					ipv6 = getResetInstanceIPv6(ctx, prov, instanceName)
					ipv6Read = true
					resetCtx.NewGuestIPv6 = ipv6
				}
				target = ipv6
			}
		} else {
			if !ipv4Read {
				ipv4 = getInstancePrivateIP(ctx, prov, resetCtx.Provider.Type, instanceName)
				ipv4Read = true
				resetCtx.NewPrivateIP = ipv4
			}
			target = ipv4
		}
		ip := net.ParseIP(strings.Trim(strings.TrimSpace(target), "[]"))
		if ip == nil || !ip.IsGlobalUnicast() || ip.IsLoopback() || (ip.To4() == nil) != isIPv6 {
			failPort(port.ID, fmt.Errorf("重建端口 %d 缺少对应地址族的有效实例地址", port.HostPort))
			continue
		}
		endpoints, _ := expandPortEndpoints(port) // Validated before any remote I/O.
		for _, endpoint := range endpoints {
			if err := ctx.Err(); err != nil {
				failPort(port.ID, err)
				break
			}
			// A failed setup may have partially installed rules, so persist once
			// even on partial failure while retaining the original error.
			needsSave = needsSave || port.MappingMethod == "iptables"
			if err := setter.SetupPortMappingWithIP(ctx, instanceName, endpoint.host, endpoint.guest, port.Protocol, port.MappingMethod, ip.String()); err != nil {
				failPort(port.ID, fmt.Errorf("重建端口 %d -> %d 配置失败: %w", endpoint.host, endpoint.guest, err))
			}
		}
	}
	if needsSave {
		if saver, ok := prov.(interface{ SaveIptablesRules() error }); ok {
			if err := saver.SaveIptablesRules(); err != nil {
				for _, port := range mappings {
					if port.MappingMethod == "iptables" {
						failPort(port.ID, fmt.Errorf("保存重建防火墙规则失败: %w", err))
					}
				}
			}
		}
	}
	return failedPorts, errors.Join(failures...)
}

// LXD has a legacy context-free IPv6 getter; Incus and other providers use
// context-aware getters. Neither path may reuse the deleted guest's address.
func getResetInstanceIPv6(ctx context.Context, prov interface{}, name string) string {
	if getter, ok := prov.(interface {
		GetInstanceIPv6(context.Context, string) (string, error)
	}); ok {
		address, err := getter.GetInstanceIPv6(ctx, name)
		if err == nil {
			return strings.TrimSpace(address)
		}
	} else if getter, ok := prov.(interface{ GetInstanceIPv6(string) (string, error) }); ok {
		address, err := getter.GetInstanceIPv6(name)
		if err == nil {
			return strings.TrimSpace(address)
		}
	}
	return ""
}

// 辅助函数：获取实例内网IP
func getInstancePrivateIP(ctx context.Context, prov interface{}, providerType, instanceName string) string {
	switch providerType {
	case "lxd":
		if p, ok := prov.(interface {
			GetInstanceIPv4(context.Context, string) (string, error)
		}); ok {
			if ip, err := p.GetInstanceIPv4(ctx, instanceName); err == nil {
				return ip
			}
		}
	case "incus":
		if p, ok := prov.(interface {
			GetInstanceIPv4(context.Context, string) (string, error)
		}); ok {
			if ip, err := p.GetInstanceIPv4(ctx, instanceName); err == nil {
				return ip
			}
		}
	case "proxmox":
		if p, ok := prov.(interface {
			GetInstanceIPv4(context.Context, string) (string, error)
		}); ok {
			if ip, err := p.GetInstanceIPv4(ctx, instanceName); err == nil {
				return ip
			}
		}
	case "qemu", "vmware", "virtualbox", "multipass", "vagrant", "kubevirt":
		if p, ok := prov.(interface {
			GetInstance(context.Context, string) (*providerModel.ProviderInstance, error)
		}); ok {
			if info, err := p.GetInstance(ctx, instanceName); err == nil && info != nil {
				if info.PrivateIP != "" {
					return info.PrivateIP
				}
				return info.IP
			}
		}
	}
	return ""
}
