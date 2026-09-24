package lxd

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	neturl "net/url"
	"strings"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// apiCreateCommittedError indicates that the API has accepted the create
// mutation.  The caller must not fall back to SSH after this point: doing so
// would create a second instance with the same name while the first one may
// still be starting or recovering from a transient API error.
type apiCreateCommittedError struct {
	err error
}

func (e *apiCreateCommittedError) Error() string { return e.err.Error() }
func (e *apiCreateCommittedError) Unwrap() error { return e.err }

func (l *LXDProvider) apiListInstances(ctx context.Context) ([]provider.Instance, error) {
	url := l.apiEndpoint("/1.0/instances")
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := l.apiClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("列出LXD实例失败: status %d", resp.StatusCode)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}

	rawMetadata, ok := response["metadata"]
	if !ok {
		return nil, fmt.Errorf("列出LXD实例响应缺少metadata")
	}
	metadata, ok := rawMetadata.([]interface{})
	if !ok {
		return nil, fmt.Errorf("列出LXD实例响应metadata格式无效")
	}

	var instances []provider.Instance
	for _, item := range metadata {
		if instanceData, ok := item.(map[string]interface{}); ok {
			name, nameOK := instanceData["name"].(string)
			status, _ := instanceData["status"].(string)
			instanceType, _ := instanceData["type"].(string)
			if !nameOK || strings.TrimSpace(name) == "" {
				continue
			}
			instance := provider.Instance{
				ID:     name,
				Name:   name,
				Status: status,
				Type:   instanceType,
			}
			instances = append(instances, instance)
		}
	}

	return instances, nil
}

func (l *LXDProvider) apiCreateInstance(ctx context.Context, config provider.InstanceConfig) error {
	return l.apiCreateInstanceWithProgress(ctx, config, nil)
}

// apiNodePortMappings loads mappings that belong to the node. Controller and
// native mappings are excluded because they are terminated elsewhere.
func (l *LXDProvider) apiNodePortMappings(config provider.InstanceConfig) ([]providerModel.Port, string, error) {
	if global.APP_DB == nil || l.config.ID == 0 {
		return nil, "device_proxy", nil
	}
	var instance providerModel.Instance
	if err := global.APP_DB.Where("name = ? AND provider_id = ?", config.Name, l.config.ID).First(&instance).Error; err != nil {
		return nil, "device_proxy", nil
	}
	var ports []providerModel.Port
	if err := global.APP_DB.Where("instance_id = ? AND status = ?", instance.ID, "active").Find(&ports).Error; err != nil {
		return nil, "device_proxy", fmt.Errorf("读取实例端口映射失败: %w", err)
	}
	var providerConfig providerModel.Provider
	_ = global.APP_DB.First(&providerConfig, l.config.ID).Error
	networkType := providerConfig.NetworkType
	if config.Metadata != nil && config.Metadata["network_type"] != "" {
		networkType = config.Metadata["network_type"]
	}
	var mappings []providerModel.Port
	for _, port := range ports {
		mappings = append(mappings, providerModel.ExpandPortMappingFamilies(port, networkType,
			normalizeLXDMappingMethod(providerConfig.IPv4PortMappingMethod), normalizeLXDMappingMethod(providerConfig.IPv6PortMappingMethod))...)
	}
	return mappings, normalizeLXDMappingMethod(providerConfig.IPv4PortMappingMethod), nil
}

func buildAPIPortMappingDevices(ports []providerModel.Port, defaultMethod, targetIP, listenIP string, devices map[string]interface{}) error {
	return buildAPIPortMappingDevicesWithTargets(ports, defaultMethod, targetIP, "", listenIP, "", devices)
}

func buildAPIPortMappingDevicesWithTargets(ports []providerModel.Port, defaultMethod, targetIPv4, targetIPv6, listenIPv4, listenIPv6 string, devices map[string]interface{}) error {
	if devices == nil {
		return nil
	}
	validateTarget := func(value string, wantIPv6 bool) (string, error) {
		targetAddr := net.ParseIP(strings.Trim(strings.TrimSpace(value), "[]"))
		if targetAddr == nil || !targetAddr.IsGlobalUnicast() || targetAddr.IsLoopback() || targetAddr.IsLinkLocalUnicast() || (wantIPv6 && targetAddr.To4() != nil) || (!wantIPv6 && targetAddr.To4() == nil) {
			return "", fmt.Errorf("API端口映射缺少有效实例地址: %q", value)
		}
		return targetAddr.String(), nil
	}
	if strings.TrimSpace(targetIPv4) != "" {
		var err error
		targetIPv4, err = validateTarget(targetIPv4, false)
		if err != nil {
			return err
		}
	}
	if strings.TrimSpace(targetIPv6) != "" {
		var err error
		targetIPv6, err = validateTarget(targetIPv6, true)
		if err != nil {
			return err
		}
	}
	for _, port := range ports {
		method := normalizeLXDMappingMethod(port.MappingMethod)
		if strings.TrimSpace(port.MappingMethod) == "" {
			method = defaultMethod
		}
		if port.MappingType == "controller" || method == "native" {
			continue
		}
		portIPv6 := port.IPv6Enabled || strings.TrimSpace(port.IPv6Address) != ""
		targetIP := targetIPv4
		if portIPv6 {
			targetIP = strings.TrimSpace(port.IPv6Address)
			if targetIP == "" {
				targetIP = targetIPv6
			}
			if targetIP == "" {
				return fmt.Errorf("IPv6端口映射缺少实例IPv6地址: %d", port.HostPort)
			}
		} else if targetIP == "" {
			return fmt.Errorf("API端口映射缺少有效实例IPv4地址: %d", port.HostPort)
		}
		var err error
		targetIP, err = validateTarget(targetIP, portIPv6)
		if err != nil {
			return err
		}
		listenAddress := listenIPv4
		if portIPv6 {
			listenAddress = listenIPv6
		}
		listenAddress, err = validateTarget(listenAddress, portIPv6)
		if err != nil {
			return fmt.Errorf("NAT proxy监听地址无效: %w", err)
		}
		if port.HostPort < 1 || port.GuestPort < 1 {
			return fmt.Errorf("端口映射 %d -> %d 超出有效范围", port.HostPort, port.GuestPort)
		}
		if method == "iptables" {
			return fmt.Errorf("API-only无法配置实例 %d 的iptables/nftables端口映射，请改用device_proxy或SSH执行规则", port.HostPort)
		}
		if port.HostPort > 65535 || port.GuestPort > 65535 {
			return fmt.Errorf("端口映射 %d -> %d 超出有效范围", port.HostPort, port.GuestPort)
		}
		if port.PortCount < 0 || port.PortCount > 65535 || port.HostPortEnd < 0 || port.GuestPortEnd < 0 {
			return fmt.Errorf("端口映射数量无效: %d", port.PortCount)
		}
		hostEnd, guestEnd := port.HostPortEnd, port.GuestPortEnd
		if hostEnd <= 0 {
			hostEnd = port.HostPort
		}
		if guestEnd <= 0 {
			guestEnd = port.GuestPort
		}
		if port.PortCount > 1 {
			if hostEnd <= port.HostPort {
				hostEnd = port.HostPort + port.PortCount - 1
			}
			if guestEnd <= port.GuestPort {
				guestEnd = port.GuestPort + port.PortCount - 1
			}
		}
		if hostEnd < port.HostPort || guestEnd < port.GuestPort || hostEnd > 65535 || guestEnd > 65535 || hostEnd-port.HostPort != guestEnd-port.GuestPort {
			return fmt.Errorf("端口映射范围无效: host=%d-%d guest=%d-%d", port.HostPort, hostEnd, port.GuestPort, guestEnd)
		}
		protocols := []string{strings.ToLower(strings.TrimSpace(port.Protocol))}
		if protocols[0] == "" {
			protocols[0] = "tcp"
		}
		if protocols[0] == "both" {
			protocols = []string{"tcp", "udp"}
		}
		for _, protocol := range protocols {
			if protocol != "tcp" && protocol != "udp" {
				return fmt.Errorf("端口 %d 使用了不支持的协议 %q", port.HostPort, port.Protocol)
			}
			devicePrefix := "proxy-"
			if portIPv6 {
				devicePrefix = "proxy-v6-"
			}
			deviceName := fmt.Sprintf("%s%s-%d", devicePrefix, protocol, port.HostPort)
			listen := lxdProxyEndpoint(protocol, listenAddress, port.HostPort)
			connect := lxdProxyEndpoint(protocol, targetIP, port.GuestPort)
			if hostEnd > port.HostPort {
				deviceName = fmt.Sprintf("%s%s-%d-%d", devicePrefix, protocol, port.HostPort, hostEnd)
				listen = lxdProxyEndpointRange(protocol, listenAddress, port.HostPort, hostEnd)
				connect = lxdProxyEndpointRange(protocol, targetIP, port.GuestPort, guestEnd)
			}
			if raw, exists := devices[deviceName]; exists {
				existing := make(map[string]interface{})
				switch value := raw.(type) {
				case map[string]interface{}:
					existing = value
				case map[string]string:
					for key, item := range value {
						existing[key] = item
					}
				default:
					return fmt.Errorf("实例设备 %s 已存在且格式无效", deviceName)
				}
				natOK := existing["nat"] == "true" || existing["nat"] == true
				existingConnect, _ := existing["connect"].(string)
				if existing["listen"] != listen || !utils.LXCProxyConnectMatches(existingConnect, connect) || existing["type"] != "proxy" || !natOK {
					return fmt.Errorf("实例设备 %s 已存在且配置不一致", deviceName)
				}
				continue
			}
			devices[deviceName] = map[string]string{
				"type":    "proxy",
				"listen":  listen,
				"connect": connect,
				"nat":     "true",
			}
		}
	}
	return nil
}

func (l *LXDProvider) apiGetInstanceResource(ctx context.Context, id, resource string) (map[string]interface{}, error) {
	url := l.apiEndpoint("/1.0/instances/" + neturl.PathEscape(id) + resource)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := l.apiClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("读取实例 %s 失败: status %d", id, resp.StatusCode)
	}
	var envelope map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, err
	}
	metadata, ok := envelope["metadata"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("实例 %s 响应缺少metadata", id)
	}
	metadata["_etag"] = resp.Header.Get("ETag")
	return metadata, nil
}

func (l *LXDProvider) apiInstanceIPv4(state map[string]interface{}) string {
	network, _ := state["network"].(map[string]interface{})
	fallback := ""
	for _, raw := range network {
		iface, _ := raw.(map[string]interface{})
		addresses, _ := iface["addresses"].([]interface{})
		for _, rawAddr := range addresses {
			addr, _ := rawAddr.(map[string]interface{})
			if addr["family"] != "inet" {
				continue
			}
			ip := net.ParseIP(fmt.Sprint(addr["address"]))
			if ip != nil && ip.To4() != nil && ip.IsGlobalUnicast() && !ip.IsLoopback() {
				if ip.IsLinkLocalUnicast() {
					continue
				}
				if scope, _ := addr["scope"].(string); scope == "global" || scope == "" {
					return ip.String()
				}
				fallback = ip.String()
			}
		}
	}
	return fallback
}

func (l *LXDProvider) apiInstanceIPv6(state map[string]interface{}) string {
	network, _ := state["network"].(map[string]interface{})
	fallback := ""
	for _, raw := range network {
		iface, _ := raw.(map[string]interface{})
		addresses, _ := iface["addresses"].([]interface{})
		for _, rawAddr := range addresses {
			addr, _ := rawAddr.(map[string]interface{})
			if addr["family"] != "inet6" {
				continue
			}
			ip := net.ParseIP(strings.Split(fmt.Sprint(addr["address"]), "/")[0])
			if ip != nil && ip.To4() == nil && ip.IsGlobalUnicast() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
				if scope, _ := addr["scope"].(string); scope == "global" || scope == "" {
					return ip.String()
				}
				fallback = ip.String()
			}
		}
	}
	return fallback
}

func (l *LXDProvider) apiWaitForInstanceIPv4(ctx context.Context, id string) (string, map[string]interface{}, error) {
	deadline := time.Now().Add(60 * time.Second)
	for {
		// Runtime network addresses are exposed by /state, not InstanceGet.
		state, err := l.apiGetInstanceResource(ctx, id, "/state")
		if err == nil {
			if ip := l.apiInstanceIPv4(state); ip != "" {
				metadata, err := l.apiGetInstanceResource(ctx, id, "")
				if err == nil {
					metadata["_network_state"] = state
				}
				return ip, metadata, err
			}
		}
		if time.Now().After(deadline) {
			if err != nil {
				return "", nil, fmt.Errorf("等待实例IPv4地址失败: %w", err)
			}
			return "", nil, fmt.Errorf("等待实例IPv4地址超时")
		}
		select {
		case <-ctx.Done():
			return "", nil, ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
}

func (l *LXDProvider) apiWaitForInstanceIPv6(ctx context.Context, id string) (string, map[string]interface{}, error) {
	deadline := time.Now().Add(60 * time.Second)
	for {
		state, err := l.apiGetInstanceResource(ctx, id, "/state")
		if err == nil {
			if ip := l.apiInstanceIPv6(state); ip != "" {
				metadata, metadataErr := l.apiGetInstanceResource(ctx, id, "")
				if metadataErr == nil {
					metadata["_network_state"] = state
				}
				return ip, metadata, metadataErr
			}
		}
		if time.Now().After(deadline) {
			if err != nil {
				return "", nil, fmt.Errorf("等待实例IPv6地址失败: %w", err)
			}
			return "", nil, fmt.Errorf("等待实例IPv6地址超时")
		}
		select {
		case <-ctx.Done():
			return "", nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (l *LXDProvider) apiUpdateInstanceDevices(ctx context.Context, id string, metadata map[string]interface{}) error {
	// PUT replaces the writable instance configuration. Preserve all InstancePut
	// fields and use the GET ETag so concurrent edits are rejected, not lost.
	payload := make(map[string]interface{})
	for _, key := range []string{"architecture", "config", "devices", "ephemeral", "profiles", "description", "stateful"} {
		if value, ok := metadata[key]; ok {
			payload[key] = value
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	url := l.apiEndpoint("/1.0/instances/" + neturl.PathEscape(id))
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if etag, _ := metadata["_etag"].(string); etag != "" {
		req.Header.Set("If-Match", etag)
	}
	resp, err := l.apiClient.Do(req)
	if err != nil {
		return err
	}
	return l.waitForAPIMutation(ctx, resp, "更新实例端口设备")
}

func (l *LXDProvider) apiConfigurePortMappings(ctx context.Context, config provider.InstanceConfig) error {
	ports, defaultMethod, err := l.apiNodePortMappings(config)
	if err != nil || len(ports) == 0 {
		return err
	}
	return l.apiApplyPortMappings(ctx, config.Name, ports, defaultMethod)
}

// ConfigurePortMappingsAPI applies a batch without reloading only active DB
// rows. Rebuild reservations are not active until this operation succeeds.
// It never falls back to SSH after a mutation has been accepted.
func (l *LXDProvider) ConfigurePortMappingsAPI(ctx context.Context, instanceName string, ports []providerModel.Port) error {
	return l.apiApplyPortMappings(ctx, instanceName, ports, "device_proxy")
}

func (l *LXDProvider) apiApplyPortMappings(ctx context.Context, instanceName string, ports []providerModel.Port, defaultMethod string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(ports) == 0 {
		return nil
	}
	if l.apiClient == nil {
		return fmt.Errorf("API客户端不可用")
	}
	var err error
	needIPv4, needIPv6 := false, false
	for _, port := range ports {
		method := normalizeLXDMappingMethod(port.MappingMethod)
		if strings.TrimSpace(port.MappingMethod) == "" {
			method = defaultMethod
		}
		if port.MappingType != "controller" && method != "native" {
			if port.IPv6Enabled || strings.TrimSpace(port.IPv6Address) != "" {
				needIPv6 = true
			} else {
				needIPv4 = true
			}
		}
	}
	if !needIPv4 && !needIPv6 {
		return nil
	}
	targetIP := ""
	targetIPv6 := ""
	var metadata map[string]interface{}
	if needIPv4 {
		targetIP, metadata, err = l.apiWaitForInstanceIPv4(ctx, instanceName)
	}
	if err == nil && needIPv6 {
		targetIPv6, metadata, err = l.apiWaitForInstanceIPv6(ctx, instanceName)
	}
	if err != nil {
		return err
	}
	listenIPv4, listenIPv6 := "", ""
	for _, port := range ports {
		method := normalizeLXDMappingMethod(port.MappingMethod)
		if strings.TrimSpace(method) == "" {
			method = defaultMethod
		}
		if port.MappingType == "controller" || method == "native" {
			continue
		}
		if port.IPv6Enabled || strings.TrimSpace(port.IPv6Address) != "" {
			if listenIPv6 == "" {
				listenIPv6, err = l.getNATProxyListenIP(ctx, true)
			}
		} else if listenIPv4 == "" {
			listenIPv4, err = l.getNATProxyListenIP(ctx, false)
		}
		if err != nil {
			return err
		}
	}
	prepareDevices := func(current map[string]interface{}) (bool, error) {
		devices, _ := current["devices"].(map[string]interface{})
		if devices == nil {
			devices = make(map[string]interface{})
		}
		beforeDevices := len(devices)
		// Include inherited names in conflict checks without copying every
		// profile device into the local overrides.
		expanded, _ := current["expanded_devices"].(map[string]interface{})
		candidateDevices := make(map[string]interface{}, len(expanded)+len(devices))
		for name, value := range expanded {
			candidateDevices[name] = value
		}
		for name, value := range devices {
			candidateDevices[name] = value
		}
		if err := buildAPIPortMappingDevicesWithTargets(ports, defaultMethod, targetIP, targetIPv6, listenIPv4, listenIPv6, candidateDevices); err != nil {
			return false, err
		}
		for name, value := range candidateDevices {
			_, inherited := expanded[name]
			_, local := devices[name]
			if !inherited || local {
				devices[name] = value
			}
		}
		bindingsChanged, err := utils.BindLXCProxyNICs(current, devices)
		if err != nil {
			return false, fmt.Errorf("固定NAT proxy目标网卡地址失败: %w", err)
		}
		current["devices"] = devices
		return len(devices) != beforeDevices || bindingsChanged, nil
	}
	changed, err := prepareDevices(metadata)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	if err := l.apiStopInstance(ctx, instanceName); err != nil {
		return fmt.Errorf("停止实例配置API端口映射失败: %w", err)
	}
	applyAndStart := func() error {
		// Stopping changes volatile.last_state.power and therefore the ETag.
		// Rebuild from the current configuration, preserving concurrent edits.
		// The stopped guest no longer reports network state, so retain only
		// that read-only snapshot to match the target address to its NIC.
		current, err := l.apiGetInstanceResource(ctx, instanceName, "")
		if err != nil {
			return fmt.Errorf("停止后读取实例配置失败: %w", err)
		}
		current["_network_state"] = metadata["_network_state"]
		changed, err := prepareDevices(current)
		if err != nil {
			return err
		}
		if changed {
			if err := l.apiUpdateInstanceDevices(ctx, instanceName, current); err != nil {
				return fmt.Errorf("更新API端口设备失败: %w", err)
			}
		}
		if err := l.apiStartInstance(ctx, instanceName); err != nil {
			return fmt.Errorf("启动实例完成API端口映射失败: %w", err)
		}
		return nil
	}
	if err := applyAndStart(); err != nil {
		recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if recoveryErr := l.apiStartInstance(recoveryCtx, instanceName); recoveryErr != nil {
			return fmt.Errorf("%w; 恢复启动失败: %v", err, recoveryErr)
		}
		return err
	}
	return nil
}

func (l *LXDProvider) apiCreateInstanceWithProgress(ctx context.Context, config provider.InstanceConfig, progressCallback provider.ProgressCallback) error {
	// 进度更新辅助函数
	updateProgress := func(percentage int, message string) {
		if progressCallback != nil {
			progressCallback(percentage, message)
		}
		global.APP_LOG.Debug("LXD API实例创建进度",
			zap.String("instance", config.Name),
			zap.Int("percentage", percentage),
			zap.String("message", message))
	}

	updateProgress(10, "开始LXD API创建实例...")

	// 在API创建之前，处理镜像下载和导入
	updateProgress(30, "处理镜像下载和导入...")
	if err := l.handleImageDownloadAndImport(ctx, &config, progressCallback); err != nil {
		return fmt.Errorf("镜像处理失败 [%s]: %w", l.formatImageContext(config, ""), err)
	}

	updateProgress(50, "调用LXD API创建实例...")

	// 构造实例配置
	instanceConfig := map[string]interface{}{
		"name": config.Name,
		"source": map[string]interface{}{
			"type":  "image",
			"alias": config.Image,
		},
		"config":   map[string]interface{}{},
		"devices":  map[string]interface{}{},
		"profiles": []string{"default"},
	}
	instanceConfigConfig := instanceConfig["config"].(map[string]interface{})
	instanceConfigDevices := instanceConfig["devices"].(map[string]interface{})

	// 设置实例类型
	if config.InstanceType == "vm" {
		instanceConfig["type"] = "virtual-machine"
	} else {
		instanceConfig["type"] = "container"
	}

	// 资源配置
	if config.CPU != "" {
		instanceConfigConfig["limits.cpu"] = config.CPU
	}
	if config.Memory != "" {
		instanceConfigConfig["limits.memory"] = convertMemoryFormat(config.Memory)
	}
	if config.InstanceType != "vm" {
		nesting := "true"
		if config.AllowNesting != nil && !*config.AllowNesting {
			nesting = "false"
		}
		instanceConfigConfig["security.nesting"] = nesting
	}
	// Swap and CPU scheduling priority are container-only LXC options. Sending
	// either during VM creation makes the whole API request fail validation.
	if config.InstanceType != "vm" && config.MemorySwap != nil {
		if *config.MemorySwap {
			instanceConfigConfig["limits.memory.swap"] = "true"
		} else {
			instanceConfigConfig["limits.memory.swap"] = "false"
		}
	}
	if config.InstanceType != "vm" && config.CPU != "" {
		instanceConfigConfig["limits.cpu.priority"] = "0"
	}
	if config.Disk != "" {
		// 使用 Provider 配置中真实存在的存储池；配置无效时自动从远端 storage list 纠正，
		// 不再把 local/空值硬切到 default，避免 default 池不存在时创建失败。
		poolName := l.resolveStoragePoolForInstance()
		if poolName != "" {
			instanceConfigDevices["root"] = map[string]interface{}{
				"type": "disk",
				"path": "/",
				"pool": poolName,
				"size": convertDiskFormat(config.Disk),
			}
		} else {
			global.APP_LOG.Warn("未检测到可用LXD存储池，创建实例时跳过显式root磁盘设备，改用default profile",
				zap.String("instance", config.Name))
		}
	}
	// 序列化请求体
	jsonData, err := json.Marshal(instanceConfig)
	if err != nil {
		return fmt.Errorf("marshal instance config failed: %w", err)
	}

	// 发送创建请求
	url := l.apiEndpoint("/1.0/instances")
	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(jsonData)))
	if err != nil {
		return fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := l.apiClient.Do(req)
	if err != nil {
		// The transport can fail after LXD accepted the POST. Treat the result
		// as ambiguous and block SSH fallback to avoid duplicate containers.
		return &apiCreateCommittedError{err: fmt.Errorf("execute API request failed: %w", err)}
	}
	if err := l.waitForAPIMutation(ctx, resp, "创建实例"); err != nil {
		if resp.StatusCode >= http.StatusBadRequest && resp.StatusCode < http.StatusInternalServerError {
			return fmt.Errorf("failed to create instance via API [%s]: %w", l.formatImageContext(config, ""), err)
		}
		return &apiCreateCommittedError{err: fmt.Errorf("failed to create instance via API [%s]: %w", l.formatImageContext(config, ""), err)}
	}

	updateProgress(70, "启动实例...")
	// 启动实例
	if err := l.apiStartInstance(ctx, config.Name); err != nil {
		return &apiCreateCommittedError{err: fmt.Errorf("failed to start instance [%s]: %w", l.formatImageContext(config, ""), err)}
	}

	updateProgress(90, "配置SSH密码...")
	// 等待实例启动并设置密码. API-only providers have no SSH executor, so
	// waiting through the CLI would always time out and delay every create.
	if l.shouldUseSSH() {
		if err := l.waitForInstanceReady(ctx, config.Name); err != nil {
			global.APP_LOG.Warn("等待实例启动超时，尝试直接设置SSH密码",
				zap.String("instanceName", config.Name),
				zap.Error(err))
		}
	}

	if err := l.apiConfigurePortMappings(ctx, config); err != nil {
		return &apiCreateCommittedError{err: fmt.Errorf("配置LXD API端口映射失败: %w", err)}
	}

	// 设置SSH密码 - 从元数据中获取密码
	if config.Metadata != nil {
		if password, ok := config.Metadata["password"]; ok {
			if err := l.apiSetInstancePassword(ctx, config.Name, password); err != nil {
				global.APP_LOG.Warn("配置SSH密码失败", zap.Error(err))
			}
		}
	}

	updateProgress(100, "LXD API实例创建完成")
	global.APP_LOG.Info("LXD API实例创建成功", zap.String("name", config.Name))
	return nil
}

func (l *LXDProvider) apiStartInstance(ctx context.Context, id string) error {
	url := l.apiEndpoint("/1.0/instances/" + neturl.PathEscape(id) + "/state")
	payload := map[string]interface{}{
		"action": "start",
	}

	jsonData, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "PUT", url, strings.NewReader(string(jsonData)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := l.apiClient.Do(req)
	if err != nil {
		return err
	}
	return l.waitForAPIMutation(ctx, resp, "启动实例")
}

func (l *LXDProvider) apiStopInstance(ctx context.Context, id string) error {
	url := l.apiEndpoint("/1.0/instances/" + neturl.PathEscape(id) + "/state")
	payload := map[string]interface{}{
		"action": "stop",
	}

	jsonData, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "PUT", url, strings.NewReader(string(jsonData)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := l.apiClient.Do(req)
	if err != nil {
		return err
	}
	return l.waitForAPIMutation(ctx, resp, "停止实例")
}

func (l *LXDProvider) apiRestartInstance(ctx context.Context, id string) error {
	url := l.apiEndpoint("/1.0/instances/" + neturl.PathEscape(id) + "/state")
	payload := map[string]interface{}{
		"action": "restart",
	}

	jsonData, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "PUT", url, strings.NewReader(string(jsonData)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := l.apiClient.Do(req)
	if err != nil {
		return err
	}
	return l.waitForAPIMutation(ctx, resp, "重启实例")
}

func (l *LXDProvider) apiDeleteInstance(ctx context.Context, id string) error {
	url := l.apiEndpoint("/1.0/instances/" + neturl.PathEscape(id))
	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		return err
	}

	resp, err := l.apiClient.Do(req)
	if err != nil {
		return err
	}
	return l.waitForAPIMutation(ctx, resp, "删除实例")
}

func (l *LXDProvider) apiListImages(ctx context.Context) ([]provider.Image, error) {
	url := l.apiEndpoint("/1.0/images")
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := l.apiClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var response map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}

	var images []provider.Image
	if metadata, ok := response["metadata"].([]interface{}); ok {
		for _, item := range metadata {
			if imageData, ok := item.(map[string]interface{}); ok {
				fingerprint, _ := imageData["fingerprint"].(string)
				if strings.TrimSpace(fingerprint) == "" {
					continue
				}
				imageID := fingerprint
				if len(imageID) > 12 {
					imageID = imageID[:12]
				}
				sizeBytes, _ := imageData["size"].(float64)
				image := provider.Image{
					ID:   imageID,
					Name: "unknown",
					Tag:  "latest",
					Size: fmt.Sprintf("%.2f MB", sizeBytes/1024/1024),
				}
				if aliases, ok := imageData["aliases"].([]interface{}); ok && len(aliases) > 0 {
					if alias, ok := aliases[0].(map[string]interface{}); ok {
						if name, ok := alias["name"].(string); ok && name != "" {
							image.Name = name
						}
					}
				}
				images = append(images, image)
			}
		}
	}

	return images, nil
}

func (l *LXDProvider) apiPullImage(ctx context.Context, image string) error {
	// 构造从远程镜像服务器拉取镜像的请求
	pullConfig := map[string]interface{}{
		"server":      "https://images.linuxcontainers.org",
		"protocol":    "simplestreams",
		"alias":       image,
		"auto_update": false,
	}

	jsonData, err := json.Marshal(pullConfig)
	if err != nil {
		return fmt.Errorf("marshal pull config failed: %w", err)
	}

	url := l.apiEndpoint("/1.0/images")
	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(jsonData)))
	if err != nil {
		return fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := l.apiClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute API request failed: %w", err)
	}
	if err := l.waitForAPIMutation(ctx, resp, "拉取镜像"); err != nil {
		return err
	}

	global.APP_LOG.Info("LXD API拉取镜像成功", zap.String("image", utils.TruncateString(image, 100)))
	return nil
}

func (l *LXDProvider) apiDeleteImage(ctx context.Context, id string) error {
	url := l.apiEndpoint("/1.0/images/" + neturl.PathEscape(id))
	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		return err
	}

	resp, err := l.apiClient.Do(req)
	if err != nil {
		return err
	}
	return l.waitForAPIMutation(ctx, resp, "删除镜像")
}

// apiSetInstancePassword 通过API设置实例密码
func (l *LXDProvider) apiSetInstancePassword(ctx context.Context, instanceID, password string) error {
	// LXD API方式设置密码
	// 构造执行命令的请求
	passwordCmd := fmt.Sprintf("printf 'root:%%s\\n' %s | chpasswd", shellSingleQuote(password))
	execData := map[string]interface{}{
		"command":            []string{"sh", "-c", passwordCmd},
		"wait-for-websocket": false,
		"interactive":        false,
	}

	execDataBytes, err := json.Marshal(execData)
	if err != nil {
		return fmt.Errorf("marshal exec data failed: %w", err)
	}

	// 发送执行请求
	url := l.apiEndpoint("/1.0/instances/" + neturl.PathEscape(instanceID) + "/exec")
	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(execDataBytes)))
	if err != nil {
		return fmt.Errorf("create request failed: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := l.apiClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute API request failed: %w", err)
	}
	if err := l.waitForAPIMutation(ctx, resp, "设置实例密码"); err != nil {
		return err
	}

	global.APP_LOG.Info("LXD实例密码设置成功(API)",
		zap.String("instanceID", utils.TruncateString(instanceID, 12)))

	return nil
}

// apiSetInstanceConfig 通过API设置实例配置
func (l *LXDProvider) apiSetInstanceConfig(ctx context.Context, instanceID string, key string, value string) error {
	// 构造配置更新请求
	configData := map[string]interface{}{
		"config": map[string]string{
			key: value,
		},
	}

	jsonData, err := json.Marshal(configData)
	if err != nil {
		return fmt.Errorf("marshal config data failed: %w", err)
	}

	url := l.apiEndpoint("/1.0/instances/" + neturl.PathEscape(instanceID))
	req, err := http.NewRequestWithContext(ctx, "PUT", url, strings.NewReader(string(jsonData)))
	if err != nil {
		return fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := l.apiClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute API request failed: %w", err)
	}
	if err := l.waitForAPIMutation(ctx, resp, "设置实例配置"); err != nil {
		return err
	}

	global.APP_LOG.Info("LXD实例配置设置成功(API)",
		zap.String("instanceID", utils.TruncateString(instanceID, 12)),
		zap.String("key", key),
		zap.String("value", utils.TruncateString(value, 50)))

	return nil
}

// apiSetInstanceDeviceConfig 通过API设置实例设备配置
func (l *LXDProvider) apiSetInstanceDeviceConfig(ctx context.Context, instanceID string, deviceName string, key string, value string) error {
	// 首先获取当前实例配置
	url := l.apiEndpoint("/1.0/instances/" + neturl.PathEscape(instanceID))
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("create get request failed: %w", err)
	}

	resp, err := l.apiClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute get API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to get instance config: status %d", resp.StatusCode)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return fmt.Errorf("decode response failed: %w", err)
	}

	metadata, ok := response["metadata"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid response format")
	}

	// 获取或创建devices部分
	devices, ok := metadata["devices"].(map[string]interface{})
	if !ok {
		devices = make(map[string]interface{})
	}

	// 获取或创建特定设备
	device, ok := devices[deviceName].(map[string]interface{})
	if !ok {
		device = make(map[string]interface{})
	}

	// 设置新的配置值
	device[key] = value
	devices[deviceName] = device

	// 构造更新请求
	updateData := map[string]interface{}{
		"devices": devices,
	}

	jsonData, err := json.Marshal(updateData)
	if err != nil {
		return fmt.Errorf("marshal update data failed: %w", err)
	}

	// 发送更新请求
	req, err = http.NewRequestWithContext(ctx, "PUT", url, strings.NewReader(string(jsonData)))
	if err != nil {
		return fmt.Errorf("create put request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err = l.apiClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute put API request failed: %w", err)
	}
	if err := l.waitForAPIMutation(ctx, resp, "设置实例设备配置"); err != nil {
		return err
	}

	global.APP_LOG.Info("LXD实例设备配置设置成功(API)",
		zap.String("instanceID", utils.TruncateString(instanceID, 12)),
		zap.String("device", deviceName),
		zap.String("key", key),
		zap.String("value", utils.TruncateString(value, 50)))

	return nil
}
