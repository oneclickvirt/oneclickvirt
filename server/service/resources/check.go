package resources

import (
	"errors"
	"oneclickvirt/global"
	"oneclickvirt/model/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// isGenericPortAvailable 通用端口可用性检查
// 仅检查数据库和系统级端口占用，不使用TCP连接测试
func (s *PortMappingService) isGenericPortAvailable(providerInfo *provider.Provider, port int) bool {
	// 首先检查数据库中是否已经有端口映射记录
	var existingMapping provider.Port
	err := global.APP_DB.Where("provider_id = ? AND host_port = ? AND status = ?",
		providerInfo.ID, port, "active").First(&existingMapping).Error

	if err == nil {
		// 如果数据库中已有活跃的端口映射，则认为端口不可用
		return false
	}

	if !errors.Is(err, gorm.ErrRecordNotFound) {
		// 数据库查询出错，为安全起见认为端口不可用
		global.APP_LOG.Error("检查端口映射时数据库查询失败",
			zap.Uint("providerId", providerInfo.ID),
			zap.Int("port", port),
			zap.Error(err))
		return false
	}

	// 检查端口是否在Provider的可用范围内
	if port < providerInfo.PortRangeStart || port > providerInfo.PortRangeEnd {
		return false
	}

	// 创建SSH客户端连接到Provider节点进行端口检查
	sshClient, err := s.createSSHClientForProvider(providerInfo)
	if err != nil {
		global.APP_LOG.Warn("创建SSH连接失败，跳过系统端口检查",
			zap.Error(err),
			zap.Uint("providerId", providerInfo.ID),
			zap.Int("port", port))
		// SSH连接失败时，仅基于数据库判断（已经检查过了，到这里说明数据库中没有记录）
		return true
	}
	defer sshClient.Close()

	// 使用新的批量检测工具（单个端口）
	isOccupied := utils.CheckPortOccupiedOnHost(sshClient, port)
	if isOccupied {
		global.APP_LOG.Debug("系统检测到端口被占用",
			zap.Uint("providerId", providerInfo.ID),
			zap.Int("port", port))
		return false
	}

	// 端口可用
	return true
}

// isPortAvailableOnProvider 检查端口在Provider上是否真正可用
func (s *PortMappingService) isPortAvailableOnProvider(providerInfo *provider.Provider, port int) bool {
	// 根据Provider类型检查端口是否被占用
	switch providerInfo.Type {
	case "docker":
		return s.isDockerPortAvailable(providerInfo, port)
	case "lxd", "incus":
		return s.isLXDPortAvailable(providerInfo, port)
	case "proxmox":
		return s.isProxmoxPortAvailable(providerInfo, port)
	default:
		// 对于未知类型，使用通用的端口检查
		return s.isGenericPortAvailable(providerInfo, port)
	}
}

// isDockerPortAvailable 检查Docker端口是否可用
// 使用专业的网络工具进行端口检查
func (s *PortMappingService) isDockerPortAvailable(providerInfo *provider.Provider, port int) bool {
	// 首先检查数据库记录
	var existingMapping provider.Port
	err := global.APP_DB.Where("provider_id = ? AND host_port = ? AND status = ?",
		providerInfo.ID, port, "active").First(&existingMapping).Error
	if err == nil {
		// 数据库中已有活跃的端口映射
		return false
	}

	// 使用通用的网络检查方法
	return s.isGenericPortAvailable(providerInfo, port)
}

// isLXDPortAvailable 检查LXD端口是否可用
func (s *PortMappingService) isLXDPortAvailable(providerInfo *provider.Provider, port int) bool {
	return s.isGenericPortAvailable(providerInfo, port)
}

// isProxmoxPortAvailable 检查Proxmox端口是否可用
func (s *PortMappingService) isProxmoxPortAvailable(providerInfo *provider.Provider, port int) bool {
	return s.isGenericPortAvailable(providerInfo, port)
}
