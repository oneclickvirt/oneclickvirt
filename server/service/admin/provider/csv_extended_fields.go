package provider

import (
	"fmt"

	"oneclickvirt/global"
	"oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
)

func applyCSVBoolField(values map[string]string, key string, set func(bool)) error {
	if v := values[key]; v != "" {
		b, err := parseCSVBool(v)
		if err != nil {
			return fmt.Errorf("%s 解析失败: %w", key, err)
		}
		set(b)
	}
	return nil
}

func applyCSVIntField(values map[string]string, key string, set func(int)) error {
	if v := values[key]; v != "" {
		n, err := parseCSVInt(v)
		if err != nil {
			return fmt.Errorf("%s 解析失败: %w", key, err)
		}
		set(n)
	}
	return nil
}

func applyExtendedCSVToCreateReq(req *admin.CreateProviderRequest, values map[string]string) error {
	for _, field := range []struct {
		key string
		set func(string)
	}{
		{"config", func(v string) { req.Config = v }},
		{"storagePool", func(v string) { req.StoragePool = v }},
		{"trafficSyncMethod", func(v string) { req.TrafficSyncMethod = v }},
		{"trafficOverLimitAction", func(v string) { req.TrafficOverLimitAction = v }},
		{"instanceExpiryAction", func(v string) { req.InstanceExpiryAction = v }},
		{"trafficStatsMode", func(v string) { req.TrafficStatsMode = v }},
		{"ipv4PortMappingMethod", func(v string) { req.IPv4PortMappingMethod = v }},
		{"ipv6PortMappingMethod", func(v string) { req.IPv6PortMappingMethod = v }},
		{"containerCpuAllowance", func(v string) { req.ContainerCPUAllowance = v }},
		{"containerDiskIoLimit", func(v string) { req.ContainerDiskIOLimit = v }},
		{"gpuDeviceIds", func(v string) { req.GpuDeviceIds = v }},
		{"nodeInstallType", func(v string) { req.NodeInstallType = v }},
		{"bridgeNAT", func(v string) { req.BridgeNAT = v }},
		{"bridgeDedicatedV4", func(v string) { req.BridgeDedicatedV4 = v }},
		{"bridgeDedicatedV6", func(v string) { req.BridgeDedicatedV6 = v }},
		{"natSubnet", func(v string) { req.NATSubnet = v }},
	} {
		if v := values[field.key]; v != "" {
			field.set(v)
		}
	}
	if req.ConnectionType != "agent" && req.ConnectionType != "local" {
		if v := values["token"]; v != "" {
			req.Token = v
		}
	}

	for _, field := range []struct {
		key string
		set func(bool)
	}{
		{"containerLimitCpu", func(v bool) { req.ContainerLimitCpu = v }},
		{"containerLimitMemory", func(v bool) { req.ContainerLimitMemory = v }},
		{"containerLimitDisk", func(v bool) { req.ContainerLimitDisk = v }},
		{"vmLimitCpu", func(v bool) { req.VMLimitCpu = v }},
		{"vmLimitMemory", func(v bool) { req.VMLimitMemory = v }},
		{"vmLimitDisk", func(v bool) { req.VMLimitDisk = v }},
		{"containerPrivileged", func(v bool) { req.ContainerPrivileged = v }},
		{"containerAllowNesting", func(v bool) { req.ContainerAllowNesting = v }},
		{"containerEnableLxcfs", func(v bool) { req.ContainerEnableLXCFS = v }},
		{"containerMemorySwap", func(v bool) { req.ContainerMemorySwap = v }},
		{"gpuEnabled", func(v bool) { req.GpuEnabled = v }},
		{"allowConcurrentTasks", func(v bool) { req.AllowConcurrentTasks = v }},
		{"enableTaskPolling", func(v bool) { req.EnableTaskPolling = v }},
		{"trafficQuotaVisible", func(v bool) { req.TrafficQuotaVisible = boolPtr(v) }},
	} {
		if err := applyCSVBoolField(values, field.key, field.set); err != nil {
			return err
		}
	}

	for _, field := range []struct {
		key string
		set func(int)
	}{
		{"totalQuota", func(v int) { req.TotalQuota = v }},
		{"maxContainerInstances", func(v int) { req.MaxContainerInstances = v }},
		{"maxVMInstances", func(v int) { req.MaxVMInstances = v }},
		{"maxConcurrentTasks", func(v int) { req.MaxConcurrentTasks = v }},
		{"taskPollInterval", func(v int) { req.TaskPollInterval = v }},
		{"defaultInboundBandwidth", func(v int) { req.DefaultInboundBandwidth = v }},
		{"defaultOutboundBandwidth", func(v int) { req.DefaultOutboundBandwidth = v }},
		{"maxInboundBandwidth", func(v int) { req.MaxInboundBandwidth = v }},
		{"maxOutboundBandwidth", func(v int) { req.MaxOutboundBandwidth = v }},
		{"trafficCollectInterval", func(v int) { req.TrafficCollectInterval = v }},
		{"trafficCollectBatchSize", func(v int) { req.TrafficCollectBatchSize = v }},
		{"trafficLimitCheckInterval", func(v int) { req.TrafficLimitCheckInterval = v }},
		{"trafficLimitCheckBatchSize", func(v int) { req.TrafficLimitCheckBatchSize = v }},
		{"trafficAutoResetInterval", func(v int) { req.TrafficAutoResetInterval = v }},
		{"trafficAutoResetBatchSize", func(v int) { req.TrafficAutoResetBatchSize = v }},
		{"trafficSpeedLimitKbps", func(v int) { req.TrafficSpeedLimitKbps = v }},
		{"instanceExpiryExtendDays", func(v int) { req.InstanceExpiryExtendDays = v }},
		{"sshConnectTimeout", func(v int) { req.SSHConnectTimeout = v }},
		{"sshExecuteTimeout", func(v int) { req.SSHExecuteTimeout = v }},
		{"containerMaxProcesses", func(v int) { req.ContainerMaxProcesses = v }},
	} {
		if err := applyCSVIntField(values, field.key, field.set); err != nil {
			return err
		}
	}

	return nil
}

func applyExtendedCSVToUpdateReq(req *admin.UpdateProviderRequest, values map[string]string) error {
	for _, field := range []struct {
		key string
		set func(string)
	}{
		{"config", func(v string) { req.Config = v }},
		{"storagePool", func(v string) { req.StoragePool = v }},
		{"trafficSyncMethod", func(v string) { req.TrafficSyncMethod = v }},
		{"trafficOverLimitAction", func(v string) { req.TrafficOverLimitAction = v }},
		{"instanceExpiryAction", func(v string) { req.InstanceExpiryAction = v }},
		{"trafficStatsMode", func(v string) { req.TrafficStatsMode = v }},
		{"ipv4PortMappingMethod", func(v string) { req.IPv4PortMappingMethod = v }},
		{"ipv6PortMappingMethod", func(v string) { req.IPv6PortMappingMethod = v }},
		{"containerCpuAllowance", func(v string) { req.ContainerCPUAllowance = v }},
		{"containerDiskIoLimit", func(v string) { req.ContainerDiskIOLimit = v }},
		{"gpuDeviceIds", func(v string) { req.GpuDeviceIds = v }},
		{"nodeInstallType", func(v string) { req.NodeInstallType = v }},
		{"bridgeNAT", func(v string) { req.BridgeNAT = v }},
		{"bridgeDedicatedV4", func(v string) { req.BridgeDedicatedV4 = v }},
		{"bridgeDedicatedV6", func(v string) { req.BridgeDedicatedV6 = v }},
		{"natSubnet", func(v string) { req.NATSubnet = v }},
	} {
		if v := values[field.key]; v != "" {
			field.set(v)
		}
	}
	if req.ConnectionType != "agent" && req.ConnectionType != "local" {
		if v := values["token"]; v != "" {
			req.Token = v
		}
	}

	for _, field := range []struct {
		key string
		set func(bool)
	}{
		{"containerLimitCpu", func(v bool) { req.ContainerLimitCpu = v }},
		{"containerLimitMemory", func(v bool) { req.ContainerLimitMemory = v }},
		{"containerLimitDisk", func(v bool) { req.ContainerLimitDisk = v }},
		{"vmLimitCpu", func(v bool) { req.VMLimitCpu = v }},
		{"vmLimitMemory", func(v bool) { req.VMLimitMemory = v }},
		{"vmLimitDisk", func(v bool) { req.VMLimitDisk = v }},
		{"containerPrivileged", func(v bool) { req.ContainerPrivileged = v }},
		{"containerAllowNesting", func(v bool) { req.ContainerAllowNesting = v }},
		{"containerEnableLxcfs", func(v bool) { req.ContainerEnableLXCFS = v }},
		{"containerMemorySwap", func(v bool) { req.ContainerMemorySwap = v }},
		{"gpuEnabled", func(v bool) { req.GpuEnabled = v }},
		{"allowConcurrentTasks", func(v bool) { req.AllowConcurrentTasks = v }},
		{"enableTaskPolling", func(v bool) { req.EnableTaskPolling = v }},
		{"trafficQuotaVisible", func(v bool) { req.TrafficQuotaVisible = boolPtr(v) }},
	} {
		if err := applyCSVBoolField(values, field.key, field.set); err != nil {
			return err
		}
	}

	for _, field := range []struct {
		key string
		set func(int)
	}{
		{"totalQuota", func(v int) { req.TotalQuota = v }},
		{"maxContainerInstances", func(v int) { req.MaxContainerInstances = v }},
		{"maxVMInstances", func(v int) { req.MaxVMInstances = v }},
		{"maxConcurrentTasks", func(v int) { req.MaxConcurrentTasks = v }},
		{"taskPollInterval", func(v int) { req.TaskPollInterval = v }},
		{"defaultInboundBandwidth", func(v int) { req.DefaultInboundBandwidth = v }},
		{"defaultOutboundBandwidth", func(v int) { req.DefaultOutboundBandwidth = v }},
		{"maxInboundBandwidth", func(v int) { req.MaxInboundBandwidth = v }},
		{"maxOutboundBandwidth", func(v int) { req.MaxOutboundBandwidth = v }},
		{"trafficCollectInterval", func(v int) { req.TrafficCollectInterval = v }},
		{"trafficCollectBatchSize", func(v int) { req.TrafficCollectBatchSize = v }},
		{"trafficLimitCheckInterval", func(v int) { req.TrafficLimitCheckInterval = v }},
		{"trafficLimitCheckBatchSize", func(v int) { req.TrafficLimitCheckBatchSize = v }},
		{"trafficAutoResetInterval", func(v int) { req.TrafficAutoResetInterval = v }},
		{"trafficAutoResetBatchSize", func(v int) { req.TrafficAutoResetBatchSize = v }},
		{"trafficSpeedLimitKbps", func(v int) { req.TrafficSpeedLimitKbps = v }},
		{"instanceExpiryExtendDays", func(v int) { req.InstanceExpiryExtendDays = v }},
		{"sshConnectTimeout", func(v int) { req.SSHConnectTimeout = v }},
		{"sshExecuteTimeout", func(v int) { req.SSHExecuteTimeout = v }},
		{"containerMaxProcesses", func(v int) { req.ContainerMaxProcesses = v }},
	} {
		if err := applyCSVIntField(values, field.key, field.set); err != nil {
			return err
		}
	}

	return nil
}

func restoreCSVProviderPrivateFields(providerID uint, values map[string]string) error {
	var provider providerModel.Provider
	if err := global.APP_DB.Select("connection_type, agent_secret").Where("id = ?", providerID).First(&provider).Error; err != nil {
		return err
	}
	updates := make(map[string]interface{})
	if v := values["agentSecret"]; v != "" {
		if provider.ConnectionType != "agent" || provider.AgentSecret == "" {
			updates["agent_secret"] = v
		}
	}
	if v := values["storagePoolPath"]; v != "" {
		updates["storage_pool_path"] = v
	}
	if len(updates) == 0 {
		return nil
	}
	return global.APP_DB.Model(&providerModel.Provider{}).Where("id = ?", providerID).Updates(updates).Error
}
