package provider

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
)

var providerCSVHeaders = []string{
	"id",
	"name",
	"type",
	"endpoint",
	"portIP",
	"sshPort",
	"username",
	"password",
	"sshKey",
	"token",
	"config",
	"agentSecret",
	"connectionType",
	"status",
	"architecture",
	"container_enabled",
	"vm_enabled",
	"totalQuota",
	"allowClaim",
	"redeemCodeOnly",
	"region",
	"country",
	"countryCode",
	"city",
	"executionRule",
	"storagePool",
	"storagePoolPath",
	"networkType",
	"defaultPortCount",
	"portRangeStart",
	"portRangeEnd",
	"defaultInboundBandwidth",
	"defaultOutboundBandwidth",
	"maxInboundBandwidth",
	"maxOutboundBandwidth",
	"maxTraffic",
	"trafficCountMode",
	"trafficMultiplier",
	"trafficSyncMethod",
	"trafficStatsMode",
	"trafficCollectInterval",
	"trafficCollectBatchSize",
	"trafficLimitCheckInterval",
	"trafficLimitCheckBatchSize",
	"trafficAutoResetInterval",
	"trafficAutoResetBatchSize",
	"enableTrafficControl",
	"enableResourceMonitoring",
	"ipv4PortMappingMethod",
	"ipv6PortMappingMethod",
	"sshConnectTimeout",
	"sshExecuteTimeout",
	"containerLimitCpu",
	"containerLimitMemory",
	"containerLimitDisk",
	"vmLimitCpu",
	"vmLimitMemory",
	"vmLimitDisk",
	"containerPrivileged",
	"containerAllowNesting",
	"containerEnableLxcfs",
	"containerCpuAllowance",
	"containerMemorySwap",
	"containerMaxProcesses",
	"containerDiskIoLimit",
	"gpuEnabled",
	"gpuDeviceIds",
	"maxContainerInstances",
	"maxVMInstances",
	"allowConcurrentTasks",
	"maxConcurrentTasks",
	"taskPollInterval",
	"enableTaskPolling",
	"nodeInstallType",
	"bridgeNAT",
	"bridgeDedicatedV4",
	"bridgeDedicatedV6",
	"natSubnet",
}

type ImportProvidersCSVResult struct {
	TotalRows int      `json:"totalRows"`
	Created   int      `json:"created"`
	Updated   int      `json:"updated"`
	Skipped   int      `json:"skipped"`
	Errors    []string `json:"errors"`
}

func parseCSVBool(raw string) (bool, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case "1", "true", "yes", "y", "on":
		return true, nil
	case "0", "false", "no", "n", "off":
		return false, nil
	default:
		return false, fmt.Errorf("无效布尔值: %s", raw)
	}
}

func parseCSVInt(raw string) (int, error) {
	v := strings.TrimSpace(raw)
	i64, err := strconv.ParseInt(v, 10, 32)
	if err != nil {
		return 0, err
	}
	return int(i64), nil
}

func parseCSVInt64(raw string) (int64, error) {
	v := strings.TrimSpace(raw)
	return strconv.ParseInt(v, 10, 64)
}

func parseCSVFloat64(raw string) (float64, error) {
	v := strings.TrimSpace(raw)
	return strconv.ParseFloat(v, 64)
}

func normalizeCSVHeader(h string) string {
	h = strings.TrimSpace(h)
	h = strings.TrimPrefix(h, "\ufeff")
	key := strings.ToLower(strings.NewReplacer("_", "", "-", "", " ", "").Replace(h))
	aliases := map[string]string{
		"sshpassword":           "password",
		"sshprivatekey":         "sshKey",
		"privatekey":            "sshKey",
		"agentsecret":           "agentSecret",
		"agenttoken":            "token",
		"containertypeenabled":  "container_enabled",
		"containerenabled":      "container_enabled",
		"vmenabled":             "vm_enabled",
		"virtualmachineenabled": "vm_enabled",
	}
	for _, canonical := range providerCSVHeaders {
		aliases[strings.ToLower(strings.NewReplacer("_", "", "-", "", " ", "").Replace(canonical))] = canonical
	}
	if canonical, ok := aliases[key]; ok {
		return canonical
	}
	return h
}

func defaultCreateProviderRequest(name, providerType string) admin.CreateProviderRequest {
	req := admin.CreateProviderRequest{
		Name:                     name,
		Type:                     providerType,
		SSHPort:                  22,
		Architecture:             "amd64",
		ContainerEnabled:         true,
		VirtualMachineEnabled:    false,
		AllowClaim:               true,
		Status:                   "active",
		ExecutionRule:            "auto",
		DefaultPortCount:         10,
		PortRangeStart:           10000,
		PortRangeEnd:             65535,
		NetworkType:              "nat_ipv4",
		DefaultInboundBandwidth:  300,
		DefaultOutboundBandwidth: 300,
		MaxInboundBandwidth:      1000,
		MaxOutboundBandwidth:     1000,
		MaxTraffic:               1048576,
		TrafficCountMode:         "both",
		TrafficMultiplier:        1.0,
		EnableTrafficControl:     false,
		EnableResourceMonitoring: false,
		TrafficSyncMethod:        "agent",
		IPv4PortMappingMethod:    "device_proxy",
		IPv6PortMappingMethod:    "device_proxy",
		SSHConnectTimeout:        30,
		SSHExecuteTimeout:        300,
		AllowConcurrentTasks:     false,
		MaxConcurrentTasks:       1,
		TaskPollInterval:         60,
		EnableTaskPolling:        true,
		StoragePool:              "local",
		ConnectionType:           "ssh",
		ContainerLimitDisk:       true,
		VMLimitCpu:               true,
		VMLimitMemory:            true,
		VMLimitDisk:              true,
		ContainerAllowNesting:    true,
		ContainerEnableLXCFS:     true,
		ContainerCPUAllowance:    "100%",
		ContainerMemorySwap:      true,
	}

	switch providerType {
	case "docker", "podman", "containerd", "orbstack":
		req.ContainerEnabled = true
		req.VirtualMachineEnabled = false
		req.IPv4PortMappingMethod = "native"
		req.IPv6PortMappingMethod = "native"
	case "qemu", "kubevirt", "vmware", "virtualbox", "multipass", "vagrant":
		req.ContainerEnabled = false
		req.VirtualMachineEnabled = true
		req.IPv4PortMappingMethod = "iptables"
		req.IPv6PortMappingMethod = "iptables"
	case "proxmox":
		req.ContainerEnabled = true
		req.VirtualMachineEnabled = true
		req.IPv4PortMappingMethod = "iptables"
		req.IPv6PortMappingMethod = "native"
	case "lxd", "incus":
		req.ContainerEnabled = true
		req.VirtualMachineEnabled = true
		req.IPv4PortMappingMethod = "device_proxy"
		req.IPv6PortMappingMethod = "device_proxy"
	}

	return req
}

func updateReqFromProvider(p providerModel.Provider) admin.UpdateProviderRequest {
	return admin.UpdateProviderRequest{
		ID:                       p.ID,
		Name:                     p.Name,
		Type:                     p.Type,
		Endpoint:                 p.Endpoint,
		PortIP:                   p.PortIP,
		SSHPort:                  p.SSHPort,
		Username:                 p.Username,
		Token:                    p.Token,
		Config:                   p.Config,
		Region:                   p.Region,
		Country:                  p.Country,
		CountryCode:              p.CountryCode,
		City:                     p.City,
		Architecture:             p.Architecture,
		ContainerEnabled:         p.ContainerEnabled,
		VirtualMachineEnabled:    p.VirtualMachineEnabled,
		TotalQuota:               p.TotalQuota,
		AllowClaim:               p.AllowClaim,
		RedeemCodeOnly:           p.RedeemCodeOnly,
		Status:                   p.Status,
		MaxContainerInstances:    p.MaxContainerInstances,
		MaxVMInstances:           p.MaxVMInstances,
		AllowConcurrentTasks:     p.AllowConcurrentTasks,
		MaxConcurrentTasks:       p.MaxConcurrentTasks,
		TaskPollInterval:         p.TaskPollInterval,
		EnableTaskPolling:        p.EnableTaskPolling,
		StoragePool:              p.StoragePool,
		ExecutionRule:            p.ExecutionRule,
		DefaultPortCount:         p.DefaultPortCount,
		PortRangeStart:           p.PortRangeStart,
		PortRangeEnd:             p.PortRangeEnd,
		NetworkType:              p.NetworkType,
		DefaultInboundBandwidth:  p.DefaultInboundBandwidth,
		DefaultOutboundBandwidth: p.DefaultOutboundBandwidth,
		MaxInboundBandwidth:      p.MaxInboundBandwidth,
		MaxOutboundBandwidth:     p.MaxOutboundBandwidth,
		EnableTrafficControl:     p.EnableTrafficControl,
		EnableResourceMonitoring: p.EnableResourceMonitoring,
		MaxTraffic:               p.MaxTraffic,
		TrafficCountMode:         p.TrafficCountMode,
		TrafficMultiplier:        p.TrafficMultiplier,
		TrafficSyncMethod:        p.TrafficSyncMethod,
		IPv4PortMappingMethod:    p.IPv4PortMappingMethod,
		IPv6PortMappingMethod:    p.IPv6PortMappingMethod,
		SSHConnectTimeout:        p.SSHConnectTimeout,
		SSHExecuteTimeout:        p.SSHExecuteTimeout,
		ContainerLimitCpu:        p.ContainerLimitCPU,
		ContainerLimitMemory:     p.ContainerLimitMemory,
		ContainerLimitDisk:       p.ContainerLimitDisk,
		VMLimitCpu:               p.VMLimitCPU,
		VMLimitMemory:            p.VMLimitMemory,
		VMLimitDisk:              p.VMLimitDisk,
		ContainerPrivileged:      p.ContainerPrivileged,
		ContainerAllowNesting:    p.ContainerAllowNesting,
		ContainerEnableLXCFS:     p.ContainerEnableLXCFS,
		ContainerCPUAllowance:    p.ContainerCPUAllowance,
		ContainerMemorySwap:      p.ContainerMemorySwap,
		ContainerMaxProcesses:    p.ContainerMaxProcesses,
		ContainerDiskIOLimit:     p.ContainerDiskIOLimit,
		GpuEnabled:               p.GpuEnabled,
		GpuDeviceIds:             p.GpuDeviceIds,
		ConnectionType:           p.ConnectionType,
		NodeInstallType:          p.NodeInstallType,
		BridgeNAT:                p.BridgeNAT,
		BridgeDedicatedV4:        p.BridgeDedicatedV4,
		BridgeDedicatedV6:        p.BridgeDedicatedV6,
		NATSubnet:                p.NATSubnet,
	}
}

func (s *Service) applyCSVToCreateReq(req *admin.CreateProviderRequest, values map[string]string) error {
	if v := values["name"]; v != "" {
		req.Name = v
	}
	if v := values["type"]; v != "" {
		req.Type = v
	}
	if v := values["endpoint"]; v != "" {
		req.Endpoint = v
	}
	if v := values["portIP"]; v != "" {
		req.PortIP = v
	}
	if v := values["sshPort"]; v != "" {
		n, err := parseCSVInt(v)
		if err != nil {
			return fmt.Errorf("sshPort 解析失败: %w", err)
		}
		req.SSHPort = n
	}
	if v := values["username"]; v != "" {
		req.Username = v
	}
	if v := values["password"]; v != "" {
		req.Password = v
	}
	if v := values["sshKey"]; v != "" {
		req.SSHKey = v
	}
	if v := values["connectionType"]; v != "" {
		req.ConnectionType = v
	}
	if v := values["status"]; v != "" {
		req.Status = v
	}
	if v := values["architecture"]; v != "" {
		req.Architecture = v
	}
	if v := values["region"]; v != "" {
		req.Region = v
	}
	if v := values["country"]; v != "" {
		req.Country = v
	}
	if v := values["countryCode"]; v != "" {
		req.CountryCode = v
	}
	if v := values["city"]; v != "" {
		req.City = v
	}
	if v := values["executionRule"]; v != "" {
		req.ExecutionRule = v
	}
	if v := values["networkType"]; v != "" {
		req.NetworkType = v
	}

	if v := values["container_enabled"]; v != "" {
		b, err := parseCSVBool(v)
		if err != nil {
			return err
		}
		req.ContainerEnabled = b
	}
	if v := values["vm_enabled"]; v != "" {
		b, err := parseCSVBool(v)
		if err != nil {
			return err
		}
		req.VirtualMachineEnabled = b
	}
	if v := values["allowClaim"]; v != "" {
		b, err := parseCSVBool(v)
		if err != nil {
			return err
		}
		req.AllowClaim = b
	}
	if v := values["redeemCodeOnly"]; v != "" {
		b, err := parseCSVBool(v)
		if err != nil {
			return err
		}
		req.RedeemCodeOnly = b
	}
	if v := values["enableTrafficControl"]; v != "" {
		b, err := parseCSVBool(v)
		if err != nil {
			return err
		}
		req.EnableTrafficControl = b
	}
	if v := values["enableResourceMonitoring"]; v != "" {
		b, err := parseCSVBool(v)
		if err != nil {
			return err
		}
		req.EnableResourceMonitoring = b
	}

	if v := values["defaultPortCount"]; v != "" {
		n, err := parseCSVInt(v)
		if err != nil {
			return fmt.Errorf("defaultPortCount 解析失败: %w", err)
		}
		req.DefaultPortCount = n
	}
	if v := values["portRangeStart"]; v != "" {
		n, err := parseCSVInt(v)
		if err != nil {
			return fmt.Errorf("portRangeStart 解析失败: %w", err)
		}
		req.PortRangeStart = n
	}
	if v := values["portRangeEnd"]; v != "" {
		n, err := parseCSVInt(v)
		if err != nil {
			return fmt.Errorf("portRangeEnd 解析失败: %w", err)
		}
		req.PortRangeEnd = n
	}
	if v := values["maxTraffic"]; v != "" {
		n, err := parseCSVInt64(v)
		if err != nil {
			return fmt.Errorf("maxTraffic 解析失败: %w", err)
		}
		req.MaxTraffic = n
	}
	if v := values["trafficCountMode"]; v != "" {
		req.TrafficCountMode = v
	}
	if v := values["trafficMultiplier"]; v != "" {
		f, err := parseCSVFloat64(v)
		if err != nil {
			return fmt.Errorf("trafficMultiplier 解析失败: %w", err)
		}
		req.TrafficMultiplier = f
	}

	return applyExtendedCSVToCreateReq(req, values)
}

func (s *Service) applyCSVToUpdateReq(req *admin.UpdateProviderRequest, values map[string]string) error {
	if v := values["name"]; v != "" {
		req.Name = v
	}
	if v := values["type"]; v != "" {
		req.Type = v
	}
	if v := values["endpoint"]; v != "" {
		req.Endpoint = v
	}
	if v := values["portIP"]; v != "" {
		req.PortIP = v
	}
	if v := values["sshPort"]; v != "" {
		n, err := parseCSVInt(v)
		if err != nil {
			return fmt.Errorf("sshPort 解析失败: %w", err)
		}
		req.SSHPort = n
	}
	if v := values["username"]; v != "" {
		req.Username = v
	}
	if v, ok := values["password"]; ok && v != "" {
		vCopy := v
		req.Password = &vCopy
	}
	if v, ok := values["sshKey"]; ok && v != "" {
		vCopy := v
		req.SSHKey = &vCopy
	}
	if v := values["connectionType"]; v != "" {
		req.ConnectionType = v
	}
	if v := values["status"]; v != "" {
		req.Status = v
	}
	if v := values["architecture"]; v != "" {
		req.Architecture = v
	}
	if v := values["region"]; v != "" {
		req.Region = v
	}
	if v := values["country"]; v != "" {
		req.Country = v
	}
	if v := values["countryCode"]; v != "" {
		req.CountryCode = v
	}
	if v := values["city"]; v != "" {
		req.City = v
	}
	if v := values["executionRule"]; v != "" {
		req.ExecutionRule = v
	}
	if v := values["networkType"]; v != "" {
		req.NetworkType = v
	}

	if v := values["container_enabled"]; v != "" {
		b, err := parseCSVBool(v)
		if err != nil {
			return err
		}
		req.ContainerEnabled = b
	}
	if v := values["vm_enabled"]; v != "" {
		b, err := parseCSVBool(v)
		if err != nil {
			return err
		}
		req.VirtualMachineEnabled = b
	}
	if v := values["allowClaim"]; v != "" {
		b, err := parseCSVBool(v)
		if err != nil {
			return err
		}
		req.AllowClaim = b
	}
	if v := values["redeemCodeOnly"]; v != "" {
		b, err := parseCSVBool(v)
		if err != nil {
			return err
		}
		req.RedeemCodeOnly = b
	}
	if v := values["enableTrafficControl"]; v != "" {
		b, err := parseCSVBool(v)
		if err != nil {
			return err
		}
		req.EnableTrafficControl = b
	}
	if v := values["enableResourceMonitoring"]; v != "" {
		b, err := parseCSVBool(v)
		if err != nil {
			return err
		}
		req.EnableResourceMonitoring = b
	}

	if v := values["defaultPortCount"]; v != "" {
		n, err := parseCSVInt(v)
		if err != nil {
			return fmt.Errorf("defaultPortCount 解析失败: %w", err)
		}
		req.DefaultPortCount = n
	}
	if v := values["portRangeStart"]; v != "" {
		n, err := parseCSVInt(v)
		if err != nil {
			return fmt.Errorf("portRangeStart 解析失败: %w", err)
		}
		req.PortRangeStart = n
	}
	if v := values["portRangeEnd"]; v != "" {
		n, err := parseCSVInt(v)
		if err != nil {
			return fmt.Errorf("portRangeEnd 解析失败: %w", err)
		}
		req.PortRangeEnd = n
	}
	if v := values["maxTraffic"]; v != "" {
		n, err := parseCSVInt64(v)
		if err != nil {
			return fmt.Errorf("maxTraffic 解析失败: %w", err)
		}
		req.MaxTraffic = n
	}
	if v := values["trafficCountMode"]; v != "" {
		req.TrafficCountMode = v
	}
	if v := values["trafficMultiplier"]; v != "" {
		f, err := parseCSVFloat64(v)
		if err != nil {
			return fmt.Errorf("trafficMultiplier 解析失败: %w", err)
		}
		req.TrafficMultiplier = f
	}

	return applyExtendedCSVToUpdateReq(req, values)
}

// ExportProvidersCSV 导出Provider CSV。即使没有数据，也返回只有表头的CSV模板。
func (s *Service) ExportProvidersCSV(ownerAdminID uint, providerIDs []uint) ([]byte, error) {
	var providers []providerModel.Provider
	query := global.APP_DB.Model(&providerModel.Provider{})
	if ownerAdminID > 0 {
		query = query.Where("owner_admin_id = ?", ownerAdminID)
	}
	if len(providerIDs) > 0 {
		query = query.Where("id IN ?", providerIDs)
	}
	if err := query.Order("id ASC").Find(&providers).Error; err != nil {
		return nil, fmt.Errorf("查询节点失败: %w", err)
	}

	buf := &bytes.Buffer{}
	writer := csv.NewWriter(buf)
	if err := writer.Write(providerCSVHeaders); err != nil {
		return nil, err
	}

	for _, p := range providers {
		row := []string{
			strconv.FormatUint(uint64(p.ID), 10),
			p.Name,
			p.Type,
			p.Endpoint,
			p.PortIP,
			strconv.Itoa(p.SSHPort),
			p.Username,
			p.Password,
			p.SSHKey,
			p.Token,
			p.Config,
			p.AgentSecret,
			p.ConnectionType,
			p.Status,
			p.Architecture,
			strconv.FormatBool(p.ContainerEnabled),
			strconv.FormatBool(p.VirtualMachineEnabled),
			strconv.Itoa(p.TotalQuota),
			strconv.FormatBool(p.AllowClaim),
			strconv.FormatBool(p.RedeemCodeOnly),
			p.Region,
			p.Country,
			p.CountryCode,
			p.City,
			p.ExecutionRule,
			p.StoragePool,
			p.StoragePoolPath,
			p.NetworkType,
			strconv.Itoa(p.DefaultPortCount),
			strconv.Itoa(p.PortRangeStart),
			strconv.Itoa(p.PortRangeEnd),
			strconv.Itoa(p.DefaultInboundBandwidth),
			strconv.Itoa(p.DefaultOutboundBandwidth),
			strconv.Itoa(p.MaxInboundBandwidth),
			strconv.Itoa(p.MaxOutboundBandwidth),
			strconv.FormatInt(p.MaxTraffic, 10),
			p.TrafficCountMode,
			strconv.FormatFloat(p.TrafficMultiplier, 'f', -1, 64),
			p.TrafficSyncMethod,
			p.TrafficStatsMode,
			strconv.Itoa(p.TrafficCollectInterval),
			strconv.Itoa(p.TrafficCollectBatchSize),
			strconv.Itoa(p.TrafficLimitCheckInterval),
			strconv.Itoa(p.TrafficLimitCheckBatchSize),
			strconv.Itoa(p.TrafficAutoResetInterval),
			strconv.Itoa(p.TrafficAutoResetBatchSize),
			strconv.FormatBool(p.EnableTrafficControl),
			strconv.FormatBool(p.EnableResourceMonitoring),
			p.IPv4PortMappingMethod,
			p.IPv6PortMappingMethod,
			strconv.Itoa(p.SSHConnectTimeout),
			strconv.Itoa(p.SSHExecuteTimeout),
			strconv.FormatBool(p.ContainerLimitCPU),
			strconv.FormatBool(p.ContainerLimitMemory),
			strconv.FormatBool(p.ContainerLimitDisk),
			strconv.FormatBool(p.VMLimitCPU),
			strconv.FormatBool(p.VMLimitMemory),
			strconv.FormatBool(p.VMLimitDisk),
			strconv.FormatBool(p.ContainerPrivileged),
			strconv.FormatBool(p.ContainerAllowNesting),
			strconv.FormatBool(p.ContainerEnableLXCFS),
			p.ContainerCPUAllowance,
			strconv.FormatBool(p.ContainerMemorySwap),
			strconv.Itoa(p.ContainerMaxProcesses),
			p.ContainerDiskIOLimit,
			strconv.FormatBool(p.GpuEnabled),
			p.GpuDeviceIds,
			strconv.Itoa(p.MaxContainerInstances),
			strconv.Itoa(p.MaxVMInstances),
			strconv.FormatBool(p.AllowConcurrentTasks),
			strconv.Itoa(p.MaxConcurrentTasks),
			strconv.Itoa(p.TaskPollInterval),
			strconv.FormatBool(p.EnableTaskPolling),
			p.NodeInstallType,
			p.BridgeNAT,
			p.BridgeDedicatedV4,
			p.BridgeDedicatedV6,
			p.NATSubnet,
		}
		if err := writer.Write(row); err != nil {
			return nil, err
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// ImportProvidersCSV 导入Provider CSV，按 id 或 name 匹配已存在节点并更新，否则新增。
func (s *Service) ImportProvidersCSV(ownerAdminID uint, csvBytes []byte) (*ImportProvidersCSVResult, error) {
	reader := csv.NewReader(bytes.NewReader(csvBytes))
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("CSV解析失败: %w", err)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("CSV为空")
	}

	headers := records[0]
	headerIdx := make(map[string]int, len(headers))
	for i, h := range headers {
		headerIdx[normalizeCSVHeader(h)] = i
	}
	if _, ok := headerIdx["name"]; !ok {
		return nil, fmt.Errorf("CSV缺少必需表头: name")
	}
	if _, ok := headerIdx["type"]; !ok {
		return nil, fmt.Errorf("CSV缺少必需表头: type")
	}

	candidateIDs := make([]uint, 0)
	candidateNames := make([]string, 0)
	seenID := make(map[uint]struct{})
	seenName := make(map[string]struct{})
	for rowNum := 1; rowNum < len(records); rowNum++ {
		row := records[rowNum]
		if idIdx, ok := headerIdx["id"]; ok && idIdx < len(row) {
			rawID := strings.TrimSpace(row[idIdx])
			if rawID != "" {
				if id64, parseErr := strconv.ParseUint(rawID, 10, 32); parseErr == nil {
					id := uint(id64)
					if _, exists := seenID[id]; !exists {
						seenID[id] = struct{}{}
						candidateIDs = append(candidateIDs, id)
					}
				}
			}
		}

		nameIdx, ok := headerIdx["name"]
		if !ok || nameIdx >= len(row) {
			continue
		}
		name := strings.TrimSpace(row[nameIdx])
		if name == "" {
			continue
		}
		if _, exists := seenName[name]; exists {
			continue
		}
		seenName[name] = struct{}{}
		candidateNames = append(candidateNames, name)
	}

	existingByID := make(map[uint]providerModel.Provider)
	existingByName := make(map[string]providerModel.Provider)

	if len(candidateIDs) > 0 {
		byID := make([]providerModel.Provider, 0, len(candidateIDs))
		q := global.APP_DB.Where("id IN ?", candidateIDs)
		if ownerAdminID > 0 {
			q = q.Where("owner_admin_id = ?", ownerAdminID)
		}
		if err := q.Find(&byID).Error; err != nil {
			return nil, fmt.Errorf("查询已有节点失败: %w", err)
		}
		for _, p := range byID {
			existingByID[p.ID] = p
			existingByName[p.Name] = p
		}
	}

	if len(candidateNames) > 0 {
		byName := make([]providerModel.Provider, 0, len(candidateNames))
		q := global.APP_DB.Where("name IN ?", candidateNames)
		if ownerAdminID > 0 {
			q = q.Where("owner_admin_id = ?", ownerAdminID)
		}
		if err := q.Find(&byName).Error; err != nil {
			return nil, fmt.Errorf("查询已有节点失败: %w", err)
		}
		for _, p := range byName {
			existingByID[p.ID] = p
			existingByName[p.Name] = p
		}
	}

	result := &ImportProvidersCSVResult{
		TotalRows: len(records) - 1,
		Errors:    make([]string, 0),
	}

	for rowNum := 1; rowNum < len(records); rowNum++ {
		row := records[rowNum]
		values := make(map[string]string, len(headerIdx))
		for key, idx := range headerIdx {
			if idx < len(row) {
				values[key] = strings.TrimSpace(row[idx])
			} else {
				values[key] = ""
			}
		}

		name := values["name"]
		providerType := values["type"]
		if name == "" || providerType == "" {
			result.Skipped++
			result.Errors = append(result.Errors, fmt.Sprintf("第%d行跳过: name/type 不能为空", rowNum+1))
			continue
		}

		var existing providerModel.Provider
		existingFound := false
		if rawID, ok := values["id"]; ok && rawID != "" {
			id64, err := strconv.ParseUint(rawID, 10, 32)
			if err != nil {
				result.Skipped++
				result.Errors = append(result.Errors, fmt.Sprintf("第%d行跳过: id 无效", rowNum+1))
				continue
			}
			if found, ok := existingByID[uint(id64)]; ok {
				existing = found
				existingFound = true
			}
		}

		if !existingFound {
			if found, ok := existingByName[name]; ok {
				existing = found
				existingFound = true
			}
		}

		if existingFound {
			oldName := existing.Name
			updateReq := updateReqFromProvider(existing)
			if err := s.applyCSVToUpdateReq(&updateReq, values); err != nil {
				result.Skipped++
				result.Errors = append(result.Errors, fmt.Sprintf("第%d行跳过: 字段解析失败: %v", rowNum+1, err))
				continue
			}

			if err := s.UpdateProvider(updateReq); err != nil {
				result.Skipped++
				result.Errors = append(result.Errors, fmt.Sprintf("第%d行更新失败: %v", rowNum+1, err))
				continue
			}
			if err := restoreCSVProviderPrivateFields(existing.ID, values); err != nil {
				result.Skipped++
				result.Errors = append(result.Errors, fmt.Sprintf("第%d行更新私有字段失败: %v", rowNum+1, err))
				continue
			}
			if oldName != updateReq.Name {
				delete(existingByName, oldName)
			}
			existing.Name = updateReq.Name
			existingByID[existing.ID] = existing
			existingByName[existing.Name] = existing
			result.Updated++
			continue
		}

		createReq := defaultCreateProviderRequest(name, providerType)
		if err := s.applyCSVToCreateReq(&createReq, values); err != nil {
			result.Skipped++
			result.Errors = append(result.Errors, fmt.Sprintf("第%d行跳过: 字段解析失败: %v", rowNum+1, err))
			continue
		}

		if createReq.ConnectionType != "agent" && createReq.Password == "" && createReq.SSHKey == "" {
			result.Skipped++
			result.Errors = append(result.Errors, fmt.Sprintf("第%d行跳过: SSH模式需提供 password 或 sshKey", rowNum+1))
			continue
		}

		createdProvider, err := s.CreateProvider(createReq, ownerAdminID)
		if err != nil {
			result.Skipped++
			result.Errors = append(result.Errors, fmt.Sprintf("第%d行创建失败: %v", rowNum+1, err))
			continue
		}
		if err := restoreCSVProviderPrivateFields(createdProvider.ID, values); err != nil {
			result.Skipped++
			result.Errors = append(result.Errors, fmt.Sprintf("第%d行创建后恢复私有字段失败: %v", rowNum+1, err))
			continue
		}
		existingByID[createdProvider.ID] = *createdProvider
		existingByName[createdProvider.Name] = *createdProvider
		result.Created++
	}

	return result, nil
}
