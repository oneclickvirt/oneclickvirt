package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	userModel "oneclickvirt/model/user"
	traffic_monitor "oneclickvirt/service/admin/traffic_monitor"
	agentService "oneclickvirt/service/agent"
	"oneclickvirt/service/database"
	"oneclickvirt/service/resources"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

func updateProviderRequestHasField(req admin.UpdateProviderRequest, names ...string) bool {
	if req.ProvidedFields == nil {
		return true
	}
	for _, name := range names {
		if req.ProvidedFields[name] {
			return true
		}
	}
	return false
}

func resolveUpdatedProviderCapabilities(provider providerModel.Provider, req admin.UpdateProviderRequest) (bool, bool) {
	containerEnabled := provider.ContainerEnabled
	vmEnabled := provider.VirtualMachineEnabled
	containerProvided := updateProviderRequestHasField(req, "container_enabled", "containerEnabled")
	vmProvided := updateProviderRequestHasField(req, "vm_enabled", "vmEnabled", "virtualMachineEnabled")

	if containerProvided {
		containerEnabled = req.ContainerEnabled
	}
	if vmProvided {
		vmEnabled = req.VirtualMachineEnabled
	}
	if !containerProvided && !vmProvided && req.Type == "" {
		return provider.ContainerEnabled, provider.VirtualMachineEnabled
	}

	return normalizeProviderInstanceTypeCapabilities(provider.Type, containerEnabled, vmEnabled)
}

func resolveUpdatedTrafficOverLimitPolicy(provider providerModel.Provider, req admin.UpdateProviderRequest) (string, int) {
	if updateProviderRequestHasField(req, "trafficOverLimitAction", "trafficSpeedLimitKbps") {
		action := req.TrafficOverLimitAction
		if action == "" && updateProviderRequestHasField(req, "trafficSpeedLimitKbps") {
			action = provider.TrafficOverLimitAction
		}
		return normalizeTrafficOverLimitPolicy(action, req.TrafficSpeedLimitKbps)
	}
	if provider.TrafficOverLimitAction == "" {
		return normalizeTrafficOverLimitPolicy("", provider.TrafficSpeedLimitKbps)
	}
	return provider.TrafficOverLimitAction, provider.TrafficSpeedLimitKbps
}

func resolveUpdatedInstanceExpiryPolicy(provider providerModel.Provider, req admin.UpdateProviderRequest) (string, int) {
	if updateProviderRequestHasField(req, "instanceExpiryAction", "instanceExpiryExtendDays") {
		action := req.InstanceExpiryAction
		if action == "" && updateProviderRequestHasField(req, "instanceExpiryExtendDays") {
			action = provider.InstanceExpiryAction
		}
		return normalizeInstanceExpiryPolicy(action, req.InstanceExpiryExtendDays)
	}
	if provider.InstanceExpiryAction == "" {
		return normalizeInstanceExpiryPolicy("", provider.InstanceExpiryExtendDays)
	}
	return provider.InstanceExpiryAction, provider.InstanceExpiryExtendDays
}

// UpdateProvider 更新Provider
func (s *Service) UpdateProvider(req admin.UpdateProviderRequest) error {
	global.APP_LOG.Debug("开始更新Provider", zap.Uint("providerID", req.ID))

	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, req.ID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			global.APP_LOG.Warn("Provider更新失败：Provider不存在", zap.Uint("providerID", req.ID))
		} else {
			global.APP_LOG.Error("查询Provider失败", zap.Uint("providerID", req.ID), zap.Error(err))
		}
		return err
	}
	// 保存原始值，用于检测变更后清理缓存
	originalConnectionType := provider.ConnectionType
	originalType := provider.Type
	// 保存原始过期时间，用于后续对比是否发生变化
	originalExpiresAt := provider.ExpiresAt
	originalProviderForIOLimits := provider
	normalizedEndpoint, normalizedSSHPort := normalizeProviderEndpointAndPort(req.Endpoint, req.SSHPort)
	effectiveConnectionType := provider.ConnectionType
	if req.ConnectionType == "ssh" || req.ConnectionType == "agent" || req.ConnectionType == "local" {
		effectiveConnectionType = req.ConnectionType
	}
	if effectiveConnectionType == "local" {
		normalizedEndpoint = "127.0.0.1"
		normalizedSSHPort = 0
	}

	// 1. 检查Provider名称是否与其他Provider重复（排除当前Provider）
	if req.Name != provider.Name {
		var existingNameCount int64
		if err := global.APP_DB.Model(&providerModel.Provider{}).
			Where("name = ? AND id != ?", req.Name, req.ID).
			Count(&existingNameCount).Error; err != nil {
			global.APP_LOG.Error("检查Provider名称失败", zap.Error(err))
			return fmt.Errorf("检查Provider名称失败: %v", err)
		}
		if existingNameCount > 0 {
			global.APP_LOG.Warn("Provider更新失败：名称已存在",
				zap.Uint("providerID", req.ID),
				zap.String("name", utils.TruncateString(req.Name, 32)))
			return fmt.Errorf("Provider名称 '%s' 已被其他Provider使用，请使用其他名称", req.Name)
		}
	}

	// 2. 检查SSH地址和端口组合是否与其他Provider重复（排除当前Provider）
	if normalizedEndpoint != "" {
		// 只有当SSH地址或端口发生变化时才检查
		if normalizedEndpoint != provider.Endpoint || normalizedSSHPort != provider.SSHPort {
			var existingEndpointCount int64
			if err := global.APP_DB.Model(&providerModel.Provider{}).
				Where("endpoint = ? AND ssh_port = ? AND id != ?", normalizedEndpoint, normalizedSSHPort, req.ID).
				Count(&existingEndpointCount).Error; err != nil {
				global.APP_LOG.Error("检查Provider SSH地址失败", zap.Error(err))
				return fmt.Errorf("检查Provider SSH地址失败: %v", err)
			}
			if existingEndpointCount > 0 {
				global.APP_LOG.Warn("Provider更新失败：SSH地址和端口组合已存在",
					zap.Uint("providerID", req.ID),
					zap.String("endpoint", utils.TruncateString(normalizedEndpoint, 64)),
					zap.Int("sshPort", normalizedSSHPort))
				return fmt.Errorf("SSH地址 '%s:%d' 已被其他Provider使用，请检查是否重复配置", normalizedEndpoint, normalizedSSHPort)
			}
		}
	}

	// 解析过期时间
	if req.ExpiresAt != "" {
		// 特殊值 "never" 表示永不过期
		if req.ExpiresAt == "never" {
			provider.ExpiresAt = nil
		} else {
			// 尝试解析多种时间格式
			var t time.Time
			var err error

			// 首先尝试ISO 8601格式（前端默认格式）
			t, err = time.Parse(time.RFC3339, req.ExpiresAt)
			if err != nil {
				// 尝试标准日期时间格式
				t, err = time.Parse("2006-01-02 15:04:05", req.ExpiresAt)
				if err != nil {
					// 尝试日期格式
					t, err = time.Parse("2006-01-02", req.ExpiresAt)
					if err != nil {
						return fmt.Errorf("过期时间格式错误，请使用 'YYYY-MM-DD HH:MM:SS' 或 'YYYY-MM-DD' 格式")
					}
				}
			}
			provider.ExpiresAt = &t
		}
	}
	// 如果 req.ExpiresAt 为空字符串，不修改现有的 ExpiresAt（保持原有值）

	// 只更新请求中提供的非零值字段
	if req.Name != "" {
		provider.Name = req.Name
	}
	if updateProviderRequestHasField(req, "description") {
		provider.Description = req.Description
	}
	if req.Type != "" {
		provider.Type = utils.NormalizeProviderType(req.Type)
	}
	if effectiveConnectionType == "local" {
		provider.Type = "qemu"
	}
	if normalizedEndpoint != "" {
		provider.Endpoint = normalizedEndpoint
	}
	if req.PortIP != "" {
		provider.PortIP = req.PortIP
	}
	if normalizedSSHPort > 0 {
		provider.SSHPort = normalizedSSHPort
	}
	// Agent/本机模式不使用远程 SSH IP/端口：强制清空或固定本机，确保不保留旧值。
	if effectiveConnectionType == "agent" {
		provider.Endpoint = ""
		provider.SSHPort = 0
	} else if effectiveConnectionType == "local" {
		provider.Endpoint = "127.0.0.1"
		provider.SSHPort = 0
	}
	if req.Username != "" {
		provider.Username = req.Username
	}
	if effectiveConnectionType == "local" && provider.Username == "" {
		provider.Username = "root"
	}

	// 密码和SSH密钥的更新逻辑（使用指针以区分"未提供"和"空值"）：
	// - nil: 不修改（前端未提供该字段，保持原值）
	// - 指向空字符串: 清空该字段（切换到另一种认证方式）
	// - 指向非空字符串: 更新为新值

	// 临时保存更新后的值，用于验证
	newPassword := provider.Password
	newSSHKey := provider.SSHKey

	// 是否修改了密码
	passwordChanged := false
	if req.Password != nil {
		newPassword = *req.Password
		passwordChanged = true
		global.APP_LOG.Debug("更新Provider密码",
			zap.Uint("providerID", req.ID),
			zap.String("status", map[bool]string{true: "cleared", false: "set"}[*req.Password == ""]))
	}

	// 是否修改了SSH密钥
	sshKeyChanged := false
	if req.SSHKey != nil {
		newSSHKey = *req.SSHKey
		sshKeyChanged = true
		global.APP_LOG.Debug("更新Provider SSH密钥",
			zap.Uint("providerID", req.ID),
			zap.String("status", map[bool]string{true: "cleared", false: "set"}[*req.SSHKey == ""]))
	}

	// 验证：SSH 直连模式下必须至少保留一种认证方式；agent 模式无需 SSH 凭据
	if effectiveConnectionType != "agent" && effectiveConnectionType != "local" && newPassword == "" && newSSHKey == "" {
		global.APP_LOG.Warn("Provider更新失败：尝试清空所有认证方式",
			zap.Uint("providerID", req.ID))
		return fmt.Errorf("必须保留至少一种SSH认证方式（密码或密钥）")
	}

	// 应用更新（只有在字段被修改时才更新）
	if passwordChanged {
		provider.Password = newPassword
	}
	if sshKeyChanged {
		provider.SSHKey = newSSHKey
	}
	if req.Token != "" {
		provider.Token = req.Token
	}
	if req.Config != "" {
		provider.Config = req.Config
	}
	if req.Region != "" {
		provider.Region = req.Region
	}
	if req.Country != "" {
		provider.Country = req.Country
	}
	if req.CountryCode != "" {
		provider.CountryCode = req.CountryCode
	}
	if req.City != "" {
		provider.City = req.City
	}
	if req.Architecture != "" {
		provider.Architecture = req.Architecture
	}
	provider.ContainerEnabled, provider.VirtualMachineEnabled = resolveUpdatedProviderCapabilities(provider, req)
	if req.TotalQuota > 0 {
		provider.TotalQuota = req.TotalQuota
	}
	if updateProviderRequestHasField(req, "allowClaim") {
		provider.AllowClaim = req.AllowClaim
	}
	if updateProviderRequestHasField(req, "redeemCodeOnly") {
		provider.RedeemCodeOnly = req.RedeemCodeOnly
	}
	if req.Status != "" {
		provider.Status = req.Status
	}
	if updateProviderRequestHasField(req, "maxContainerInstances") && req.MaxContainerInstances >= 0 {
		provider.MaxContainerInstances = req.MaxContainerInstances
	}
	if updateProviderRequestHasField(req, "maxVMInstances") && req.MaxVMInstances >= 0 {
		provider.MaxVMInstances = req.MaxVMInstances
	}
	if updateProviderRequestHasField(req, "allowConcurrentTasks") {
		provider.AllowConcurrentTasks = req.AllowConcurrentTasks
	}
	if req.MaxConcurrentTasks > 0 {
		provider.MaxConcurrentTasks = req.MaxConcurrentTasks
	}
	if req.TaskPollInterval > 0 {
		provider.TaskPollInterval = req.TaskPollInterval
	}
	if updateProviderRequestHasField(req, "enableTaskPolling") {
		provider.EnableTaskPolling = req.EnableTaskPolling
	}
	// 存储配置（所有Provider类型通用）
	if req.StoragePool != "" {
		provider.StoragePool = req.StoragePool
	}
	// 操作执行配置更新
	if req.ExecutionRule != "" {
		provider.ExecutionRule = req.ExecutionRule
	}
	// Proxmox 网桥配置更新（仅更新非空值，BridgeDedicatedV6 允许设为空字符串表示无IPv6桥）
	if req.NodeInstallType != "" {
		provider.NodeInstallType = req.NodeInstallType
	}
	if req.BridgeNAT != "" {
		provider.BridgeNAT = req.BridgeNAT
	}
	if req.BridgeDedicatedV4 != "" {
		provider.BridgeDedicatedV4 = req.BridgeDedicatedV4
	}
	// BridgeDedicatedV6 可以清空（表示不支持独立IPv6），所以当 NodeInstallType 为 third_party 时允许设置空值
	if req.NodeInstallType == "third_party" {
		provider.BridgeDedicatedV6 = req.BridgeDedicatedV6
		provider.NATSubnet = req.NATSubnet // NATSubnet 同样允许更新（可为空表示使用默认）
	} else if req.BridgeDedicatedV6 != "" {
		provider.BridgeDedicatedV6 = req.BridgeDedicatedV6
	}
	// 端口映射配置更新
	if req.DefaultPortCount > 0 {
		provider.DefaultPortCount = req.DefaultPortCount
	}
	if req.PortRangeStart > 0 {
		provider.PortRangeStart = req.PortRangeStart
	}
	if req.PortRangeEnd > 0 {
		provider.PortRangeEnd = req.PortRangeEnd
	}
	if req.FixedPorts != nil {
		fixedPorts, err := resources.NormalizeProviderFixedPorts(req.FixedPorts, provider.DefaultPortCount)
		if err != nil {
			return err
		}
		provider.FixedPorts = fixedPorts
	} else if len(provider.FixedPorts) == 0 {
		provider.FixedPorts = []int{22}
	}
	if req.NetworkType != "" {
		provider.NetworkType = req.NetworkType
	}
	// 带宽配置更新
	if req.DefaultInboundBandwidth > 0 {
		provider.DefaultInboundBandwidth = req.DefaultInboundBandwidth
	}
	if req.DefaultOutboundBandwidth > 0 {
		provider.DefaultOutboundBandwidth = req.DefaultOutboundBandwidth
	}
	if req.MaxInboundBandwidth > 0 {
		provider.MaxInboundBandwidth = req.MaxInboundBandwidth
	}
	if req.MaxOutboundBandwidth > 0 {
		provider.MaxOutboundBandwidth = req.MaxOutboundBandwidth
	}
	if updateProviderRequestHasField(req, "containerReadIoLimit") {
		provider.ContainerReadIOLimit = req.ContainerReadIOLimit
	}
	if updateProviderRequestHasField(req, "containerWriteIoLimit") {
		provider.ContainerWriteIOLimit = req.ContainerWriteIOLimit
	}
	if updateProviderRequestHasField(req, "vmReadIoLimit") {
		provider.VMReadIOLimit = req.VMReadIOLimit
	}
	if updateProviderRequestHasField(req, "vmWriteIoLimit") {
		provider.VMWriteIOLimit = req.VMWriteIOLimit
	}
	// 流量控制开关更新
	oldEnableTrafficControl := provider.EnableTrafficControl
	if updateProviderRequestHasField(req, "enableTrafficControl") {
		provider.EnableTrafficControl = req.EnableTrafficControl
	}
	// 硬件资源监控开关更新
	if updateProviderRequestHasField(req, "enableResourceMonitoring") {
		provider.EnableResourceMonitoring = req.EnableResourceMonitoring
	}
	// 流量同步方式更新
	if req.TrafficSyncMethod != "" {
		provider.TrafficSyncMethod = req.TrafficSyncMethod
	}
	provider.TrafficOverLimitAction, provider.TrafficSpeedLimitKbps = resolveUpdatedTrafficOverLimitPolicy(provider, req)
	if req.TrafficQuotaVisible != nil {
		provider.TrafficQuotaVisible = *req.TrafficQuotaVisible
	}
	provider.InstanceExpiryAction, provider.InstanceExpiryExtendDays = resolveUpdatedInstanceExpiryPolicy(provider, req)

	// 检测流量统计开关是否发生变化
	trafficControlChanged := oldEnableTrafficControl != provider.EnableTrafficControl

	// 流量限制更新
	if req.MaxTraffic > 0 {
		provider.MaxTraffic = req.MaxTraffic
	}
	// 流量统计模式更新
	if req.TrafficCountMode != "" {
		provider.TrafficCountMode = req.TrafficCountMode
	}
	// 流量统计性能模式更新
	if req.TrafficStatsMode != "" {
		oldMode := provider.TrafficStatsMode
		provider.TrafficStatsMode = req.TrafficStatsMode

		// 如果切换到非自定义模式，强制应用预设配置
		if req.TrafficStatsMode != providerModel.TrafficStatsModeCustom {
			global.APP_LOG.Debug("应用流量统计预设配置",
				zap.Uint("providerID", req.ID),
				zap.String("oldMode", oldMode),
				zap.String("newMode", req.TrafficStatsMode))
			provider.ApplyTrafficStatsPreset()
		}
	}
	// 流量统计详细配置更新（仅在自定义模式下使用）
	if req.TrafficCollectInterval > 0 {
		// 验证采集间隔最大不超过5分钟（300秒）
		if req.TrafficCollectInterval > 300 {
			return fmt.Errorf("流量采集间隔不能超过300秒（5分钟），当前值: %d秒", req.TrafficCollectInterval)
		}
		provider.TrafficCollectInterval = req.TrafficCollectInterval
	}
	if req.TrafficCollectBatchSize > 0 {
		provider.TrafficCollectBatchSize = req.TrafficCollectBatchSize
	}
	if req.TrafficLimitCheckInterval > 0 {
		provider.TrafficLimitCheckInterval = req.TrafficLimitCheckInterval
	}
	if req.TrafficLimitCheckBatchSize > 0 {
		provider.TrafficLimitCheckBatchSize = req.TrafficLimitCheckBatchSize
	}
	if req.TrafficAutoResetInterval > 0 {
		provider.TrafficAutoResetInterval = req.TrafficAutoResetInterval
	}
	if req.TrafficAutoResetBatchSize > 0 {
		provider.TrafficAutoResetBatchSize = req.TrafficAutoResetBatchSize
	}
	// 流量计费倍率更新
	if req.TrafficMultiplier > 0 {
		oldValue := provider.TrafficMultiplier
		provider.TrafficMultiplier = req.TrafficMultiplier
		global.APP_LOG.Debug("更新流量计费倍率",
			zap.Uint("providerID", req.ID),
			zap.Float64("oldValue", oldValue),
			zap.Float64("newValue", req.TrafficMultiplier))
	}

	// 检查Provider过期时间是否发生变化，需要同步到非手动设置过期时间的实例
	expireTimeChanged := false
	if (originalExpiresAt == nil && provider.ExpiresAt != nil) ||
		(originalExpiresAt != nil && provider.ExpiresAt == nil) ||
		(originalExpiresAt != nil && provider.ExpiresAt != nil && !originalExpiresAt.Equal(*provider.ExpiresAt)) {
		expireTimeChanged = true
	}

	// 如果过期时间发生变化，记录日志并准备同步
	if expireTimeChanged {
		global.APP_LOG.Info("Provider过期时间发生变化，将同步非手动设置过期时间的实例",
			zap.Uint("providerID", req.ID),
			zap.Any("oldExpiresAt", originalExpiresAt),
			zap.Any("newExpiresAt", provider.ExpiresAt))
	}

	// 端口映射方式更新
	// Docker/Podman/Containerd/Orbstack 类型固定使用 native，忽略前端传入的値
	if provider.Type == "docker" || provider.Type == "podman" || provider.Type == "containerd" || provider.Type == "orbstack" {
		provider.IPv4PortMappingMethod = "native"
		provider.IPv6PortMappingMethod = "native"
	} else {
		if req.IPv4PortMappingMethod != "" {
			provider.IPv4PortMappingMethod = req.IPv4PortMappingMethod
		}
		if req.IPv6PortMappingMethod != "" {
			provider.IPv6PortMappingMethod = req.IPv6PortMappingMethod
		}
	}
	// SSH超时配置更新
	if req.SSHConnectTimeout > 0 {
		provider.SSHConnectTimeout = req.SSHConnectTimeout
	}
	if req.SSHExecuteTimeout > 0 {
		provider.SSHExecuteTimeout = req.SSHExecuteTimeout
	}
	// 容器资源限制配置更新
	if updateProviderRequestHasField(req, "containerLimitCpu") {
		provider.ContainerLimitCPU = req.ContainerLimitCpu
	}
	if updateProviderRequestHasField(req, "containerLimitMemory") {
		provider.ContainerLimitMemory = req.ContainerLimitMemory
	}
	if updateProviderRequestHasField(req, "containerLimitDisk") {
		provider.ContainerLimitDisk = req.ContainerLimitDisk
	}
	if updateProviderRequestHasField(req, "enableDomainBinding") {
		provider.EnableDomainBinding = req.EnableDomainBinding
	}
	if req.ProxyHTTPPort > 0 {
		provider.ProxyHTTPPort = req.ProxyHTTPPort
	} else if provider.ProxyHTTPPort <= 0 {
		provider.ProxyHTTPPort = 80
	}
	if req.ProxyHTTPSPort > 0 {
		provider.ProxyHTTPSPort = req.ProxyHTTPSPort
	} else if provider.ProxyHTTPSPort <= 0 {
		provider.ProxyHTTPSPort = 443
	}
	if updateProviderRequestHasField(req, "proxyEnableHttp") {
		provider.ProxyEnableHTTP = req.ProxyEnableHTTP
	}
	if updateProviderRequestHasField(req, "proxyEnableHttps") {
		provider.ProxyEnableHTTPS = req.ProxyEnableHTTPS
	}
	if !provider.ProxyEnableHTTP && !provider.ProxyEnableHTTPS {
		provider.ProxyEnableHTTP = true
	}
	if updateProviderRequestHasField(req, "proxyTlsCertPath") {
		provider.ProxyTLSCertPath = req.ProxyTLSCertPath
	}
	if updateProviderRequestHasField(req, "proxyTlsKeyPath") {
		provider.ProxyTLSKeyPath = req.ProxyTLSKeyPath
	}
	if updateProviderRequestHasField(req, "proxyAutoSync") {
		provider.ProxyAutoSync = req.ProxyAutoSync
	}
	if updateProviderRequestHasField(req, "enableVNC") {
		provider.EnableVNC = req.EnableVNC
	}
	if req.VNCBasePort > 0 {
		provider.VNCBasePort = req.VNCBasePort
	} else if provider.VNCBasePort <= 0 {
		provider.VNCBasePort = 5900
	}
	if updateProviderRequestHasField(req, "vncHost") {
		provider.VNCHost = req.VNCHost
	}
	// 虚拟机资源限制配置更新
	if updateProviderRequestHasField(req, "vmLimitCpu") {
		provider.VMLimitCPU = req.VMLimitCpu
	}
	if updateProviderRequestHasField(req, "vmLimitMemory") {
		provider.VMLimitMemory = req.VMLimitMemory
	}
	if updateProviderRequestHasField(req, "vmLimitDisk") {
		provider.VMLimitDisk = req.VMLimitDisk
	}
	// 容器特殊配置与 GPU 直通仅对 LXD/Incus 容器有效；类型切换时清理旧值。
	if utils.IsLXDIncusProvider(provider.Type) {
		if updateProviderRequestHasField(req, "containerPrivileged") {
			provider.ContainerPrivileged = req.ContainerPrivileged
		}
		if updateProviderRequestHasField(req, "containerAllowNesting") {
			provider.ContainerAllowNesting = req.ContainerAllowNesting
		}
		if updateProviderRequestHasField(req, "containerEnableLxcfs") {
			provider.ContainerEnableLXCFS = req.ContainerEnableLXCFS
		}
		if req.ContainerCPUAllowance != "" {
			provider.ContainerCPUAllowance = req.ContainerCPUAllowance
		} else if provider.ContainerCPUAllowance == "" {
			provider.ContainerCPUAllowance = "100%"
		}
		if updateProviderRequestHasField(req, "containerMemorySwap") {
			provider.ContainerMemorySwap = req.ContainerMemorySwap
		}
		if updateProviderRequestHasField(req, "containerMaxProcesses") {
			provider.ContainerMaxProcesses = req.ContainerMaxProcesses
		}
		if updateProviderRequestHasField(req, "containerDiskIoLimit") {
			provider.ContainerDiskIOLimit = req.ContainerDiskIOLimit
		}
	} else {
		provider.ContainerPrivileged = false
		provider.ContainerAllowNesting = false
		provider.ContainerEnableLXCFS = false
		provider.ContainerCPUAllowance = ""
		provider.ContainerMemorySwap = false
		provider.ContainerMaxProcesses = 0
		provider.ContainerDiskIOLimit = ""
	}
	if updateProviderRequestHasField(req, "gpuEnabled", "gpuDeviceIds") || req.Type != "" {
		gpuEnabled := provider.GpuEnabled
		gpuDeviceIDs := provider.GpuDeviceIds
		if updateProviderRequestHasField(req, "gpuEnabled") {
			gpuEnabled = req.GpuEnabled
		}
		if updateProviderRequestHasField(req, "gpuDeviceIds") {
			gpuDeviceIDs = req.GpuDeviceIds
		}
		normalizedGPUEnabled, normalizedGPUDeviceIDs, err := normalizeProviderGPUConfig(provider.Type, gpuEnabled, gpuDeviceIDs)
		if err != nil {
			return err
		}
		provider.GpuEnabled = normalizedGPUEnabled
		provider.GpuDeviceIds = normalizedGPUDeviceIDs
	}
	// 实例发现与导入配置更新
	if req.DiscoverMode != nil {
		provider.PendingDiscovery = *req.DiscoverMode
	}
	if req.AutoImport != nil {
		provider.DiscoveryAutoImport = *req.AutoImport
	}
	if req.AutoAdjustQuota != nil {
		provider.DiscoveryAutoAdjust = *req.AutoAdjustQuota
	}
	if req.ImportedInstanceOwner != nil {
		var ownerUserID uint
		if *req.ImportedInstanceOwner != "" {
			var ownerUser userModel.User
			if err := global.APP_DB.Where("username = ?", *req.ImportedInstanceOwner).First(&ownerUser).Error; err == nil {
				ownerUserID = ownerUser.ID
			} else {
				global.APP_LOG.Warn("发现实例所有者用户不存在",
					zap.Uint("providerID", req.ID),
					zap.String("username", *req.ImportedInstanceOwner))
			}
		}
		provider.DiscoveryOwnerUserID = ownerUserID
	}
	if req.ConnectionType == "agent" || req.ConnectionType == "ssh" || req.ConnectionType == "local" {
		provider.ConnectionType = req.ConnectionType
		global.APP_LOG.Debug("更新Provider连接类型",
			zap.Uint("providerID", req.ID),
			zap.String("connectionType", req.ConnectionType))
	}
	// 检测连接类型是否发生变化，需要在事务提交后清理缓存
	connectionTypeChanged := (req.ConnectionType == "agent" || req.ConnectionType == "ssh" || req.ConnectionType == "local") &&
		originalConnectionType != "" && originalConnectionType != req.ConnectionType
	if provider.ConnectionType == "agent" {
		provider.EnableTrafficControl = true
		provider.EnableResourceMonitoring = true
		provider.TrafficSyncMethod = "agent"
		// Agent模式下，若无公网IP（portIP为空），则强制使用无端口映射模式
		if provider.PortIP == "" {
			provider.NetworkType = "no_port_mapping"
		}
	}
	if provider.ConnectionType == "local" {
		provider.Type = "qemu"
		provider.Endpoint = "127.0.0.1"
		provider.SSHPort = 0
		if provider.Username == "" {
			provider.Username = "root"
		}
		if provider.NetworkType == "" {
			provider.NetworkType = "nat_ipv4"
		}
		if !provider.ContainerEnabled && !provider.VirtualMachineEnabled {
			provider.ContainerEnabled = true
			provider.VirtualMachineEnabled = true
		}
	}
	// SSH模式：允许管理员手动设置 no_port_mapping（如节点无公网IPv4的场景），不强制重置

	// 节点级别等级限制配置更新
	if req.LevelLimits != nil {
		// 转换前端发送的 camelCase 为存储的 kebab-case
		convertedLimits := make(map[int]map[string]interface{})
		for level, limits := range req.LevelLimits {
			convertedLimit := make(map[string]interface{})
			for key, value := range limits {
				// 转换 camelCase 键为 kebab-case
				switch key {
				case "maxInstances":
					convertedLimit["max-instances"] = value
				case "maxResources":
					convertedLimit["max-resources"] = value
				case "maxTraffic":
					convertedLimit["max-traffic"] = value
				default:
					// 保留其他键不变（已经是正确格式或未知字段）
					convertedLimit[key] = value
				}
			}
			convertedLimits[level] = convertedLimit
		}
		// 将转换后的 map[int]map[string]interface{} 序列化为 JSON 字符串
		levelLimitsJSON, err := json.Marshal(convertedLimits)
		if err != nil {
			global.APP_LOG.Error("序列化节点等级限制配置失败",
				zap.Uint("providerID", req.ID),
				zap.Error(err))
			return fmt.Errorf("节点等级限制配置格式错误: %v", err)
		}
		provider.LevelLimits = string(levelLimitsJSON)
	}

	// 设置默认值
	// 并发控制默认值：确保一致性
	normalizeProviderConcurrencySettings(&provider)
	if provider.TaskPollInterval <= 0 {
		provider.TaskPollInterval = 60
	}

	dbService := database.GetDatabaseService()
	txErr := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		// 保存Provider更新
		if err := tx.Save(&provider).Error; err != nil {
			return err
		}
		if err := syncProviderDomainConfig(tx, provider.ID, provider.EnableDomainBinding); err != nil {
			return err
		}

		// 同步更新该Provider下所有非手动设置过期时间的实例的到期时间
		if provider.ExpiresAt != nil {
			if err := tx.Model(&providerModel.Instance{}).
				Where("provider_id = ? AND is_manual_expiry = ? AND status NOT IN (?)", provider.ID, false, []string{"deleting", "deleted"}).
				Update("expires_at", *provider.ExpiresAt).Error; err != nil {
				global.APP_LOG.Error("同步实例到期时间失败",
					zap.Uint("providerID", provider.ID),
					zap.Time("newExpiresAt", *provider.ExpiresAt),
					zap.Error(err))
				return fmt.Errorf("同步实例到期时间失败: %v", err)
			}
			global.APP_LOG.Info("已同步非手动设置过期时间的实例到期时间",
				zap.Uint("providerID", provider.ID),
				zap.Time("newExpiresAt", *provider.ExpiresAt))
		}

		// 如果流量统计开关发生变化，触发后台任务处理监控配置
		if trafficControlChanged {
			go s.handleTrafficControlToggle(provider.ID, provider.EnableTrafficControl)
		}

		return nil
	})

	if txErr != nil {
		return txErr
	}

	// 事务成功提交后，清理可能受影响的缓存
	if connectionTypeChanged {
		global.APP_LOG.Info("Provider连接类型已变更，清理Agent缓存",
			zap.Uint("providerID", req.ID),
			zap.String("oldType", originalConnectionType),
			zap.String("newType", provider.ConnectionType))
		agentService.GetHub().DisconnectProvider(req.ID)
		agentService.RemoveClient(req.ID)
	}
	if originalType != "" && originalType != provider.Type {
		global.APP_LOG.Info("Provider虚拟化类型已变更，清理Agent客户端缓存",
			zap.Uint("providerID", req.ID),
			zap.String("oldType", originalType),
			zap.String("newType", provider.Type))
		agentService.RemoveClient(req.ID)
	}
	if providerIOLimitsChanged(originalProviderForIOLimits, provider) {
		go s.syncProviderIOLimits(provider.ID)
	}

	return nil
}

// handleTrafficControlToggle 处理流量统计开关切换（后台任务）
// 当Provider的EnableTrafficControl从false->true或true->false时调用
func (s *Service) handleTrafficControlToggle(providerID uint, enabled bool) {
	defer func() {
		if r := recover(); r != nil {
			global.APP_LOG.Error("处理流量统计开关切换时发生panic",
				zap.Uint("providerID", providerID),
				zap.Bool("enabled", enabled),
				zap.Any("panic", r))
		}
	}()

	global.APP_LOG.Info("开始处理Provider流量统计开关切换",
		zap.Uint("providerID", providerID),
		zap.Bool("enabled", enabled))

	// 获取Provider信息（预加载，避免循环中重复查询）
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, providerID).Error; err != nil {
		global.APP_LOG.Error("查询Provider失败",
			zap.Uint("providerID", providerID),
			zap.Error(err))
		return
	}

	// 获取该Provider下所有活跃实例（预加载所有字段）
	var instances []providerModel.Instance
	if err := global.APP_DB.Where("provider_id = ? AND status NOT IN (?)",
		providerID, []string{"deleted", "deleting"}).Find(&instances).Error; err != nil {
		global.APP_LOG.Error("查询Provider实例失败",
			zap.Uint("providerID", providerID),
			zap.Error(err))
		return
	}

	if len(instances) == 0 {
		global.APP_LOG.Debug("Provider没有活跃实例，无需处理",
			zap.Uint("providerID", providerID))
		return
	}

	// 使用统一的流量监控管理器
	trafficMonitorManager := traffic_monitor.GetManager()

	// 创建带超时的context
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	if enabled {
		// 启用流量统计：为所有运行中实例初始化监控
		global.APP_LOG.Info("启用流量统计，开始为实例初始化监控",
			zap.Uint("providerID", providerID),
			zap.Int("instanceCount", len(instances)))

		successCount := 0
		failCount := 0
		skippedCount := 0

		for _, instance := range instances {
			// 只为运行中的实例初始化监控
			if instance.Status != "running" {
				global.APP_LOG.Debug("跳过非运行状态实例",
					zap.Uint("instanceID", instance.ID),
					zap.String("status", instance.Status))
				skippedCount++
				continue
			}

			// 使用统一的流量监控管理器
			if err := trafficMonitorManager.AttachMonitor(ctx, instance.ID); err != nil {
				global.APP_LOG.Warn("初始化实例监控失败",
					zap.Uint("instanceID", instance.ID),
					zap.String("instanceName", instance.Name),
					zap.Error(err))
				failCount++
			} else {
				global.APP_LOG.Debug("实例监控初始化成功",
					zap.Uint("instanceID", instance.ID),
					zap.String("instanceName", instance.Name))
				successCount++
			}
		}

		global.APP_LOG.Info("Provider流量统计启用处理完成",
			zap.Uint("providerID", providerID),
			zap.Int("成功", successCount),
			zap.Int("失败", failCount),
			zap.Int("跳过", skippedCount))

	} else {
		// 禁用流量统计：清理所有实例的监控
		global.APP_LOG.Info("禁用流量统计，开始清理实例监控",
			zap.Uint("providerID", providerID),
			zap.Int("instanceCount", len(instances)))

		successCount := 0
		failCount := 0

		for _, instance := range instances {
			// 使用统一的流量监控管理器清理监控
			if err := trafficMonitorManager.DetachMonitor(ctx, instance.ID); err != nil {
				global.APP_LOG.Warn("清理实例监控失败",
					zap.Uint("instanceID", instance.ID),
					zap.String("instanceName", instance.Name),
					zap.Error(err))
				failCount++
			} else {
				global.APP_LOG.Debug("实例监控清理成功",
					zap.Uint("instanceID", instance.ID),
					zap.String("instanceName", instance.Name))
				successCount++
			}
		}

		global.APP_LOG.Info("Provider流量统计禁用处理完成",
			zap.Uint("providerID", providerID),
			zap.Int("成功", successCount),
			zap.Int("失败", failCount))
	}
} // FreezeProvider 冻结Provider
