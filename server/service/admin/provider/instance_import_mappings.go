package provider

import (
	"context"
	"fmt"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

func (s *Service) RemoveImportedInstancesMark(ctx context.Context, instanceIDs []uint) error {
	if len(instanceIDs) == 0 {
		return nil
	}

	err := global.APP_DB.Model(&providerModel.Instance{}).
		Where("id IN ?", instanceIDs).
		Updates(map[string]interface{}{
			"is_imported":          false,
			"has_port_conflict":    false,
			"port_conflict_detail": "",
		}).Error

	if err != nil {
		return fmt.Errorf("移除导入标记失败: %w", err)
	}

	global.APP_LOG.Info("已移除实例导入标记", zap.Int("count", len(instanceIDs)))
	return nil
}

// createImportedPortMappings 为导入的实例创建端口映射记录
func (s *Service) createImportedPortMappings(tx *gorm.DB, discovered *provider.DiscoveredInstance, instanceID, providerID uint, defaultMappingMethod string, skipHostPorts map[int]bool) error {
	if len(discovered.PortMappings) == 0 && discovered.SSHPort <= 0 {
		return nil
	}

	var ports []providerModel.Port
	var portRangeStart, portRangeEnd int

	if len(discovered.PortMappings) > 0 {
		seenHostPorts := make(map[int]bool)
		for _, pm := range discovered.PortMappings {
			if skipHostPorts[pm.HostPort] || seenHostPorts[pm.HostPort] || !validDiscoveredPort(pm.HostPort) || !validDiscoveredPort(pm.GuestPort) {
				continue
			}
			seenHostPorts[pm.HostPort] = true
			protocol := pm.Protocol
			if protocol == "" {
				protocol = "both"
			}
			mappingMethod := resolveImportedMappingMethod(pm.MappingMethod, defaultMappingMethod)
			port := providerModel.Port{
				InstanceID:    instanceID,
				ProviderID:    providerID,
				HostPort:      pm.HostPort,
				GuestPort:     pm.GuestPort,
				Protocol:      protocol,
				Status:        "active",
				IsSSH:         pm.IsSSH,
				IsAutomatic:   true,
				PortType:      "range_mapped",
				MappingMethod: mappingMethod,
			}
			if pm.IsSSH {
				port.Description = "SSH"
			} else {
				port.Description = fmt.Sprintf("端口%d", pm.HostPort)
			}
			ports = append(ports, port)

			// 计算端口范围
			if portRangeStart == 0 || pm.HostPort < portRangeStart {
				portRangeStart = pm.HostPort
			}
			if pm.HostPort > portRangeEnd {
				portRangeEnd = pm.HostPort
			}
		}
	} else if discovered.SSHPort > 0 && discovered.SSHPort != 22 && !skipHostPorts[discovered.SSHPort] {
		// 只有SSH端口信息，没有完整的端口映射
		ports = append(ports, providerModel.Port{
			InstanceID:    instanceID,
			ProviderID:    providerID,
			HostPort:      discovered.SSHPort,
			GuestPort:     22,
			Protocol:      "both",
			Status:        "active",
			IsSSH:         true,
			IsAutomatic:   true,
			PortType:      "range_mapped",
			Description:   "SSH",
			MappingMethod: resolveImportedMappingMethod("", defaultMappingMethod),
		})
		portRangeStart = discovered.SSHPort
		portRangeEnd = discovered.SSHPort
	}

	if len(ports) > 0 {
		if err := tx.CreateInBatches(ports, 100).Error; err != nil {
			return fmt.Errorf("批量创建端口映射失败: %w", err)
		}
	}

	// 更新实例的端口范围和SSH端口
	updates := map[string]interface{}{}
	if portRangeStart > 0 {
		updates["port_range_start"] = portRangeStart
	}
	if portRangeEnd > 0 {
		updates["port_range_end"] = portRangeEnd
	}
	if discovered.SSHPort > 0 {
		updates["ssh_port"] = discovered.SSHPort
	}
	if len(updates) > 0 {
		if err := tx.Model(&providerModel.Instance{}).Where("id = ?", instanceID).Updates(updates).Error; err != nil {
			return fmt.Errorf("更新实例端口范围失败: %w", err)
		}
	}

	return nil
}

func resolveImportedMappingMethod(discoveredMethod, defaultMethod string) string {
	if method := normalizeDiscoveredMappingMethod(discoveredMethod); method != "" {
		return method
	}
	if method := normalizeDiscoveredMappingMethod(defaultMethod); method != "" {
		return method
	}
	return "native"
}
