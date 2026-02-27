package task

import (
	"context"
	"fmt"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider/portmapping"
	traffic_monitor "oneclickvirt/service/admin/traffic_monitor"
	provider2 "oneclickvirt/service/provider"
	"oneclickvirt/service/resources"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// resetTask_RestorePortMappings 阶段7: 恢复端口映射（直接创建，不使用任务系统）
func (s *TaskService) resetTask_RestorePortMappings(ctx context.Context, task *adminModel.Task, resetCtx *ResetTaskContext) error {
	s.updateTaskProgress(task.ID, 88, "正在恢复端口映射...")

	// 对于LXD/Incus，等待实例获取IP地址
	if resetCtx.Provider.Type == "lxd" || resetCtx.Provider.Type == "incus" {
		if resetCtx.NewPrivateIP == "" {
			providerApiService := &provider2.ProviderApiService{}
			prov, _, err := providerApiService.GetProviderByID(resetCtx.Provider.ID)
			if err == nil {
				// 尝试获取IP，最多等待30秒
				for attempt := 1; attempt <= 10; attempt++ {
					ip := getInstancePrivateIP(ctx, prov, resetCtx.Provider.Type, resetCtx.OldInstanceName)
					if ip != "" {
						resetCtx.NewPrivateIP = ip
						global.APP_LOG.Debug("实例IP获取成功",
							zap.String("instanceName", resetCtx.OldInstanceName),
							zap.String("ip", ip),
							zap.Int("attempt", attempt))
						break
					}
					if attempt < 10 {
						time.Sleep(3 * time.Second)
					}
				}
			}

			if resetCtx.NewPrivateIP == "" {
				global.APP_LOG.Warn("无法获取实例IP地址，端口映射可能失败",
					zap.String("instanceName", resetCtx.OldInstanceName))
			}
		}

		// 更新实例的内网IP到数据库
		if resetCtx.NewPrivateIP != "" {
			s.dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
				return tx.Model(&providerModel.Instance{}).Where("id = ?", resetCtx.NewInstanceID).
					Update("private_ip", resetCtx.NewPrivateIP).Error
			})
		}
	}

	// 如果没有旧端口映射，创建默认端口
	if len(resetCtx.OldPortMappings) == 0 {
		portMappingService := &resources.PortMappingService{}
		if err := portMappingService.CreateDefaultPortMappings(resetCtx.NewInstanceID, resetCtx.Provider.ID); err != nil {
			global.APP_LOG.Warn("创建默认端口映射失败", zap.Error(err))
		}
		return nil
	}

	// 恢复端口映射
	successCount := 0
	failCount := 0

	// Docker类型：端口映射已在创建时设置，只需创建数据库记录
	if resetCtx.Provider.Type == "docker" {
		for _, oldPort := range resetCtx.OldPortMappings {
			err := s.dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
				newPort := providerModel.Port{
					InstanceID:    resetCtx.NewInstanceID,
					ProviderID:    resetCtx.Provider.ID,
					HostPort:      oldPort.HostPort,
					GuestPort:     oldPort.GuestPort,
					Protocol:      oldPort.Protocol,
					Description:   oldPort.Description,
					Status:        "active",
					IsSSH:         oldPort.IsSSH,
					IsAutomatic:   oldPort.IsAutomatic,
					PortType:      oldPort.PortType,
					MappingMethod: oldPort.MappingMethod,
					IPv6Enabled:   oldPort.IPv6Enabled,
				}
				return tx.Create(&newPort).Error
			})

			if err != nil {
				global.APP_LOG.Warn("创建端口映射数据库记录失败",
					zap.Int("hostPort", oldPort.HostPort),
					zap.Error(err))
				failCount++
			} else {
				successCount++
			}
		}
	} else {
		// LXD/Incus/Proxmox：需要先创建数据库记录，然后在远程服务器上配置实际的端口映射
		// Step 1: 先创建所有端口映射的数据库记录
		for _, oldPort := range resetCtx.OldPortMappings {
			err := s.dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
				newPort := providerModel.Port{
					InstanceID:    resetCtx.NewInstanceID,
					ProviderID:    resetCtx.Provider.ID,
					HostPort:      oldPort.HostPort,
					GuestPort:     oldPort.GuestPort,
					Protocol:      oldPort.Protocol,
					Description:   oldPort.Description,
					Status:        "active",
					IsSSH:         oldPort.IsSSH,
					IsAutomatic:   oldPort.IsAutomatic,
					PortType:      oldPort.PortType,
					MappingMethod: oldPort.MappingMethod,
					IPv6Enabled:   oldPort.IPv6Enabled,
				}
				return tx.Create(&newPort).Error
			})

			if err != nil {
				global.APP_LOG.Warn("创建端口映射数据库记录失败",
					zap.Int("hostPort", oldPort.HostPort),
					zap.Error(err))
				failCount++
			}
		}

		// Step 2: 调用 Provider 层的方法，在远程服务器上实际配置端口映射（proxy device）
		providerApiService := &provider2.ProviderApiService{}
		prov, _, err := providerApiService.GetProviderByID(resetCtx.Provider.ID)
		if err != nil {
			global.APP_LOG.Warn("获取Provider实例失败，无法配置远程端口映射", zap.Error(err))
		} else {
			// 调用 Provider 层的端口映射配置方法
			if err := s.configureProviderPortMappings(ctx, prov, resetCtx); err != nil {
				global.APP_LOG.Warn("配置Provider端口映射失败", zap.Error(err))
				// 端口映射配置失败不阻塞重置流程，已创建的数据库记录保留
			} else {
				successCount = len(resetCtx.OldPortMappings)
				global.APP_LOG.Info("Provider端口映射配置成功",
					zap.Int("portCount", successCount))
			}
		}
	}

	// 更新SSH端口
	s.dbService.ExecuteQuery(ctx, func() error {
		var sshPort providerModel.Port
		if err := global.APP_DB.Where("instance_id = ? AND is_ssh = true AND status = 'active'",
			resetCtx.NewInstanceID).First(&sshPort).Error; err == nil {
			global.APP_DB.Model(&providerModel.Instance{}).Where("id = ?", resetCtx.NewInstanceID).
				Update("ssh_port", sshPort.HostPort)
		} else {
			global.APP_DB.Model(&providerModel.Instance{}).Where("id = ?", resetCtx.NewInstanceID).
				Update("ssh_port", 22)
		}
		return nil
	})

	global.APP_LOG.Info("端口映射恢复完成",
		zap.Int("成功", successCount),
		zap.Int("失败", failCount))

	return nil
}

// createPortMappingDirect 直接创建端口映射（绕过任务系统）
func (s *TaskService) createPortMappingDirect(ctx context.Context, resetCtx *ResetTaskContext, oldPort providerModel.Port) error {
	// 获取Provider实例（暂时不需要直接使用prov）
	// portmapping.Manager会自动处理provider连接

	// 确定端口映射类型
	portMappingType := resetCtx.Provider.Type
	if portMappingType == "proxmox" {
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
	s.updateTaskProgress(task.ID, 96, "正在重新初始化监控...")

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

	return nil
}

// configureProviderPortMappings 配置Provider层的端口映射（实际在远程服务器上创建proxy device）
func (s *TaskService) configureProviderPortMappings(ctx context.Context, prov interface{}, resetCtx *ResetTaskContext) error {
	// 获取实例的内网IP
	instanceIP := resetCtx.NewPrivateIP
	if instanceIP == "" {
		instanceIP = getInstancePrivateIP(ctx, prov, resetCtx.Provider.Type, resetCtx.OldInstanceName)
	}

	if instanceIP == "" {
		return fmt.Errorf("无法获取实例内网IP，跳过端口映射配置")
	}

	global.APP_LOG.Debug("开始配置Provider端口映射",
		zap.String("instanceName", resetCtx.OldInstanceName),
		zap.String("instanceIP", instanceIP),
		zap.String("providerType", resetCtx.Provider.Type),
		zap.Int("portCount", len(resetCtx.OldPortMappings)))

	// 根据Provider类型调用相应的端口映射配置方法
	// 注意：这里直接使用反射调用内部方法，因为 configurePortMappingsWithIP 是私有方法
	// 通过 SetupPortMappingWithIP 公开方法来逐个配置端口
	switch resetCtx.Provider.Type {
	case "incus":
		// 导入 incus provider
		incusProv, ok := prov.(interface {
			SetupPortMappingWithIP(ctx context.Context, instanceName string, hostPort, guestPort int, protocol, method, instanceIP string) error
		})
		if !ok {
			return fmt.Errorf("Provider类型断言失败: incus")
		}

		// 逐个配置端口映射
		for _, port := range resetCtx.OldPortMappings {
			if err := incusProv.SetupPortMappingWithIP(ctx, resetCtx.OldInstanceName, port.HostPort, port.GuestPort, port.Protocol, resetCtx.Provider.IPv4PortMappingMethod, instanceIP); err != nil {
				global.APP_LOG.Warn("配置Incus端口映射失败",
					zap.Int("hostPort", port.HostPort),
					zap.Int("guestPort", port.GuestPort),
					zap.Error(err))
				// 继续配置其他端口
			}
		}

		// 如果使用 iptables 方法，保存规则到 /etc/iptables/rules.v4
		if resetCtx.Provider.IPv4PortMappingMethod == "iptables" {
			if provWithSave, ok := prov.(interface {
				SaveIptablesRules() error
			}); ok {
				if err := provWithSave.SaveIptablesRules(); err != nil {
					global.APP_LOG.Warn("保存Incus iptables规则失败，重启后可能丢失", zap.Error(err))
				} else {
					global.APP_LOG.Debug("Incus iptables规则已保存到 /etc/iptables/rules.v4")
				}
			}
		}

		return nil

	case "lxd":
		// 导入 lxd provider
		lxdProv, ok := prov.(interface {
			SetupPortMappingWithIP(ctx context.Context, instanceName string, hostPort, guestPort int, protocol, method, instanceIP string) error
		})
		if !ok {
			return fmt.Errorf("Provider类型断言失败: lxd")
		}

		// 逐个配置端口映射
		for _, port := range resetCtx.OldPortMappings {
			if err := lxdProv.SetupPortMappingWithIP(ctx, resetCtx.OldInstanceName, port.HostPort, port.GuestPort, port.Protocol, resetCtx.Provider.IPv4PortMappingMethod, instanceIP); err != nil {
				global.APP_LOG.Warn("配置LXD端口映射失败",
					zap.Int("hostPort", port.HostPort),
					zap.Int("guestPort", port.GuestPort),
					zap.Error(err))
				// 继续配置其他端口
			}
		}

		// 如果使用 iptables 方法，保存规则到 /etc/iptables/rules.v4
		if resetCtx.Provider.IPv4PortMappingMethod == "iptables" {
			if provWithSave, ok := prov.(interface {
				SaveIptablesRules() error
			}); ok {
				if err := provWithSave.SaveIptablesRules(); err != nil {
					global.APP_LOG.Warn("保存LXD iptables规则失败，重启后可能丢失", zap.Error(err))
				} else {
					global.APP_LOG.Debug("LXD iptables规则已保存到 /etc/iptables/rules.v4")
				}
			}
		}

		return nil

	case "proxmox":
		// Proxmox 使用 iptables，通过 SetupPortMappingWithIP 在远程服务器上创建端口映射规则
		// 注意：数据库记录已在 Step 1 中创建，此处仅配置 iptables 规则
		proxmoxProv, ok := prov.(interface {
			SetupPortMappingWithIP(ctx context.Context, instanceName string, hostPort, guestPort int, protocol, method, instanceIP string) error
		})
		if !ok {
			return fmt.Errorf("Provider类型断言失败: proxmox")
		}

		// 逐个配置端口映射
		for _, port := range resetCtx.OldPortMappings {
			if err := proxmoxProv.SetupPortMappingWithIP(ctx, resetCtx.OldInstanceName, port.HostPort, port.GuestPort, port.Protocol, resetCtx.Provider.IPv4PortMappingMethod, instanceIP); err != nil {
				global.APP_LOG.Warn("配置Proxmox端口映射失败",
					zap.Int("hostPort", port.HostPort),
					zap.Int("guestPort", port.GuestPort),
					zap.Error(err))
				// 继续配置其他端口
			}
		}

		// 保存iptables规则到 /etc/iptables/rules.v4
		if provWithSave, ok := prov.(interface {
			SaveIptablesRules() error
		}); ok {
			if err := provWithSave.SaveIptablesRules(); err != nil {
				global.APP_LOG.Warn("保存iptables规则失败，重启后可能丢失", zap.Error(err))
			} else {
				global.APP_LOG.Debug("iptables规则已保存到 /etc/iptables/rules.v4")
			}
		}

		return nil

	default:
		return fmt.Errorf("不支持的Provider类型: %s", resetCtx.Provider.Type)
	}
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
	}
	return ""
}
