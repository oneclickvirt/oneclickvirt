package proxmox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"

	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/provider/firewall"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

const maxProxmoxDiscoveryResponseSize = 16 << 20
const maxProxmoxMetadataWorkers = 8
const maxProxmoxMetadataBatchSize = 64
const maxProxmoxRecoveryNetworkBatchSize = 48

type proxmoxDiscoveredResource struct {
	ID       string          `json:"id"`
	Node     string          `json:"node"`
	Name     string          `json:"name"`
	Status   string          `json:"status"`
	Type     string          `json:"type"`
	VMID     int64           `json:"vmid"`
	CPUs     float64         `json:"cpus"`
	MaxCPU   float64         `json:"maxcpu"`
	MaxMem   int64           `json:"maxmem"`
	MaxDisk  int64           `json:"maxdisk"`
	Template json.RawMessage `json:"template"`
	// Description is populated by the per-guest config endpoint. PVE shell
	// projects store their import record in the config header/description.
	Description string `json:"description,omitempty"`
	// Recovery-only network fields come from bounded config reads. Keep them
	// out of normal resource JSON decoding because cluster/resources does not
	// provide current guest addresses.
	PrivateIP   string `json:"-"`
	IPv6Address string `json:"-"`
}

type proxmoxBatchCommand struct {
	Path   string         `json:"path"`
	Method string         `json:"method"`
	Args   map[string]any `json:"args,omitempty"`
}

type proxmoxBatchCommandResult struct {
	Status  int             `json:"status"`
	Data    json.RawMessage `json:"data"`
	Message string          `json:"message"`
}

// DiscoverInstances discovers every QEMU VM and LXC container on a Proxmox
// node/cluster. API errors are only eligible for SSH fallback in auto mode;
// ssh_only must never try the API merely because a token happens to exist.
func (p *ProxmoxProvider) DiscoverInstances(ctx context.Context) ([]provider.DiscoveredInstance, error) {
	if !p.connected {
		return nil, fmt.Errorf("not connected")
	}

	global.APP_LOG.Debug("开始发现Proxmox实例", zap.String("provider", p.config.Name))

	if p.shouldUseAPI() {
		instances, err := p.apiDiscoverInstances(ctx)
		if err == nil {
			instances = p.enrichDiscoveredInstances(ctx, instances)
			global.APP_LOG.Debug("Proxmox API发现实例成功",
				zap.String("provider", p.config.Name),
				zap.Int("count", len(instances)))
			return instances, nil
		}
		if fallbackErr := p.ensureSSHBeforeFallback(err, "发现实例"); fallbackErr != nil {
			return nil, fallbackErr
		}
	}

	if !p.shouldUseSSH() {
		return nil, fmt.Errorf("执行规则不允许使用SSH发现实例")
	}
	return p.sshDiscoverInstances(ctx)
}

// DiscoverInstancesForRecovery avoids import-oriented per-guest enrichment
// during an outage recovery. API-capable nodes use the PVE node execute
// endpoint in bounded batches; SSH/Agent nodes use one resource query plus one
// generated pvesh config command. Neither path falls back to one HTTP/SSH call
// per guest when optional address metadata is unavailable.
func (p *ProxmoxProvider) DiscoverInstancesForRecovery(ctx context.Context) ([]provider.DiscoveredInstance, error) {
	if !p.connected {
		return nil, fmt.Errorf("not connected")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if p.shouldUseAPI() {
		instances, err := p.apiDiscoverInstancesForRecovery(ctx)
		if err == nil {
			return instances, nil
		}
		if fallbackErr := p.ensureSSHBeforeFallback(err, "恢复发现实例"); fallbackErr != nil {
			return nil, fallbackErr
		}
	}
	if !p.shouldUseSSH() {
		return nil, fmt.Errorf("执行规则不允许使用SSH发现实例")
	}
	return p.sshDiscoverInstancesForRecovery(ctx)
}

func (p *ProxmoxProvider) apiDiscoverInstancesForRecovery(ctx context.Context) ([]provider.DiscoveredInstance, error) {
	resourcesURL := p.apiEndpoint("/api2/json/cluster/resources?type=vm")
	response, err := p.makeAPIRequest(ctx, http.MethodGet, resourcesURL, nil)
	if err != nil {
		return nil, fmt.Errorf("获取Proxmox集群恢复实例失败: %w", err)
	}
	resources, err := parseProxmoxResourceEnvelope(response)
	if err != nil {
		return nil, fmt.Errorf("解析Proxmox集群恢复实例失败: %w", err)
	}
	p.enrichRecoveryResourceNetworksFromAPI(ctx, resources)
	return p.convertDiscoveredResources(resources)
}

func (p *ProxmoxProvider) sshDiscoverInstancesForRecovery(ctx context.Context) ([]provider.DiscoveredInstance, error) {
	if !p.sshClient.HasExecutor() {
		return nil, fmt.Errorf("SSH client not initialized")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	output, err := p.sshClient.Execute("pvesh get /cluster/resources --type vm --output-format json")
	if err != nil {
		return nil, fmt.Errorf("SSH获取Proxmox恢复实例失败: %w", err)
	}
	resources, err := parseProxmoxResourcesJSON(output)
	if err != nil {
		return nil, fmt.Errorf("SSH解析Proxmox恢复实例失败: %w", err)
	}
	p.enrichRecoveryResourceNetworksFromSSH(ctx, resources)
	return p.convertDiscoveredResources(resources)
}

// apiDiscoverInstances uses the cluster resource endpoint so PVE 8/9 and
// multi-node clusters are handled in one request. A denied or malformed
// response is returned as an error instead of being mistaken for an empty
// clean node.
func (p *ProxmoxProvider) apiDiscoverInstances(ctx context.Context) ([]provider.DiscoveredInstance, error) {
	resourcesURL := p.apiEndpoint("/api2/json/cluster/resources?type=vm")
	resp, err := p.makeAPIRequest(ctx, http.MethodGet, resourcesURL, nil)
	if err != nil {
		return nil, fmt.Errorf("获取Proxmox集群实例失败: %w", err)
	}

	resources, err := parseProxmoxResourceEnvelope(resp)
	if err != nil {
		return nil, fmt.Errorf("解析Proxmox集群实例失败: %w", err)
	}
	resources = p.enrichAPIResourceDescriptions(ctx, resources)
	return p.convertDiscoveredResources(resources)
}

// enrichRecoveryResourceNetworksFromAPI reads only guest config fields needed
// to repair controller address mappings. The execute endpoint groups up to a
// fixed number of config reads into one request per PVE node; a denied batch is
// intentionally treated as missing optional address data rather than causing
// an individual-request fallback during recovery.
func (p *ProxmoxProvider) enrichRecoveryResourceNetworksFromAPI(ctx context.Context, resources []proxmoxDiscoveredResource) {
	groups := make(map[string][]int)
	for index := range resources {
		if resources[index].VMID <= 0 || strings.TrimSpace(resources[index].Node) == "" {
			continue
		}
		if _, err := normalizeProxmoxResourceKind(resources[index].Type); err != nil {
			continue
		}
		groups[strings.TrimSpace(resources[index].Node)] = append(groups[strings.TrimSpace(resources[index].Node)], index)
	}
	nodes := make([]string, 0, len(groups))
	for node := range groups {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)
	for _, node := range nodes {
		indexes := groups[node]
		for start := 0; start < len(indexes); start += maxProxmoxRecoveryNetworkBatchSize {
			if ctx.Err() != nil {
				return
			}
			end := start + maxProxmoxRecoveryNetworkBatchSize
			if end > len(indexes) {
				end = len(indexes)
			}
			if err := p.fetchRecoveryResourceNetworksBatch(ctx, node, indexes[start:end], resources); err != nil && global.APP_LOG != nil {
				global.APP_LOG.Debug("Proxmox恢复批量网络配置读取失败，保留已有地址",
					zap.String("provider", p.config.Name), zap.String("node", node), zap.Error(err))
			}
		}
	}
}

func (p *ProxmoxProvider) fetchRecoveryResourceNetworksBatch(
	ctx context.Context,
	node string,
	indexes []int,
	resources []proxmoxDiscoveredResource,
) error {
	commands := make([]proxmoxBatchCommand, 0, len(indexes))
	commandIndexes := make([]int, 0, len(indexes))
	for _, index := range indexes {
		kind, err := normalizeProxmoxResourceKind(resources[index].Type)
		if err != nil {
			continue
		}
		commands = append(commands, proxmoxBatchCommand{
			Path:   fmt.Sprintf("%s/%d/config", kind, resources[index].VMID),
			Method: http.MethodGet,
		})
		commandIndexes = append(commandIndexes, index)
	}
	if len(commands) == 0 {
		return nil
	}
	commandsJSON, err := json.Marshal(commands)
	if err != nil {
		return fmt.Errorf("编码Proxmox恢复批量网络命令失败: %w", err)
	}
	body, err := json.Marshal(map[string]string{"commands": string(commandsJSON)})
	if err != nil {
		return fmt.Errorf("编码Proxmox恢复批量网络请求失败: %w", err)
	}
	executeURL := p.apiEndpoint(fmt.Sprintf("/api2/json/nodes/%s/execute", url.PathEscape(strings.TrimSpace(node))))
	response, err := p.makeAPIRequest(ctx, http.MethodPost, executeURL, body)
	if err != nil {
		return err
	}
	var payload struct {
		Data []proxmoxBatchCommandResult `json:"data"`
	}
	if err := json.Unmarshal(response, &payload); err != nil {
		return fmt.Errorf("解析Proxmox恢复批量网络响应失败: %w", err)
	}
	if len(payload.Data) != len(commandIndexes) {
		return fmt.Errorf("Proxmox恢复批量网络响应数量异常: got %d want %d", len(payload.Data), len(commandIndexes))
	}
	for commandIndex, result := range payload.Data {
		if result.Status < http.StatusOK || result.Status >= http.StatusMultipleChoices || len(result.Data) == 0 {
			continue
		}
		network := parseProxmoxRecoveryNetworkConfig(result.Data)
		resource := &resources[commandIndexes[commandIndex]]
		if resource.PrivateIP == "" {
			resource.PrivateIP = network.PrivateIP
		}
		if resource.IPv6Address == "" {
			resource.IPv6Address = network.IPv6Address
		}
	}
	return nil
}

// enrichRecoveryResourceNetworksFromSSH performs one bounded host command
// after the cluster resource query. The command reads only static guest config
// and emits line-delimited JSON keyed by the resource index, avoiding a fresh
// controller-to-node SSH round trip for each guest.
func (p *ProxmoxProvider) enrichRecoveryResourceNetworksFromSSH(ctx context.Context, resources []proxmoxDiscoveredResource) {
	if !p.shouldUseSSH() || !p.sshClient.HasExecutor() || ctx.Err() != nil {
		return
	}
	var command strings.Builder
	for index := range resources {
		resource := resources[index]
		kind, err := normalizeProxmoxResourceKind(resource.Type)
		if err != nil || resource.VMID <= 0 || strings.TrimSpace(resource.Node) == "" {
			continue
		}
		path := fmt.Sprintf("/nodes/%s/%s/%d/config", strings.TrimSpace(resource.Node), kind, resource.VMID)
		fmt.Fprintf(&command,
			"printf 'OCVREC\\t%d\\t'; pvesh get %s --output-format json 2>/dev/null | tr '\\n' ' '; printf '\\n'\n",
			index, utils.ShellSingleQuote(path))
	}
	if command.Len() == 0 {
		return
	}
	output, err := p.sshClient.Execute(command.String())
	if err != nil {
		if global.APP_LOG != nil {
			global.APP_LOG.Debug("Proxmox恢复SSH批量网络配置读取失败，保留已有地址",
				zap.String("provider", p.config.Name), zap.Error(err))
		}
		return
	}
	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "\t", 3)
		if len(parts) != 3 || parts[0] != "OCVREC" {
			continue
		}
		index, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || index < 0 || index >= len(resources) {
			continue
		}
		network := parseProxmoxRecoveryNetworkConfig([]byte(parts[2]))
		if resources[index].PrivateIP == "" {
			resources[index].PrivateIP = network.PrivateIP
		}
		if resources[index].IPv6Address == "" {
			resources[index].IPv6Address = network.IPv6Address
		}
	}
}

type proxmoxRecoveryNetwork struct {
	PrivateIP   string
	IPv6Address string
}

func parseProxmoxRecoveryNetworkConfig(raw []byte) proxmoxRecoveryNetwork {
	var config map[string]interface{}
	if err := json.Unmarshal(raw, &config); err != nil {
		return proxmoxRecoveryNetwork{}
	}
	if nested, ok := config["data"].(map[string]interface{}); ok {
		config = nested
	}
	keys := make([]string, 0, len(config))
	for key := range config {
		lowerKey := strings.ToLower(strings.TrimSpace(key))
		if strings.HasPrefix(lowerKey, "net") || strings.HasPrefix(lowerKey, "ipconfig") {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	network := proxmoxRecoveryNetwork{}
	for _, key := range keys {
		value, ok := config[key].(string)
		if !ok {
			continue
		}
		for _, segment := range strings.Split(value, ",") {
			name, address, found := strings.Cut(strings.TrimSpace(segment), "=")
			if !found {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(name)) {
			case "ip":
				if network.PrivateIP == "" {
					network.PrivateIP = normalizeProxmoxRecoveryAddress(address, false)
				}
			case "ip6":
				if network.IPv6Address == "" {
					network.IPv6Address = normalizeProxmoxRecoveryAddress(address, true)
				}
			}
		}
	}
	return network
}

func normalizeProxmoxRecoveryAddress(raw string, wantV6 bool) string {
	raw = strings.TrimSpace(strings.Trim(raw, "[]"))
	if raw == "" || strings.EqualFold(raw, "dhcp") || strings.EqualFold(raw, "auto") {
		return ""
	}
	if prefix, err := netip.ParsePrefix(raw); err == nil {
		addr := prefix.Addr().Unmap()
		if addr.Is6() == wantV6 && !addr.IsUnspecified() {
			return addr.String()
		}
		return ""
	}
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return ""
	}
	addr = addr.Unmap()
	if addr.Is6() != wantV6 || addr.IsUnspecified() {
		return ""
	}
	return addr.String()
}

// enrichAPIResourceDescriptions covers api_only nodes where the controller
// cannot read /etc/pve over SSH. PVE's node execute endpoint can batch config
// reads, so discovery normally needs one request per node and fixed-size batch.
// Older/denied execute endpoints fall back to bounded individual reads so
// metadata required by WebSSH is still imported whenever permissions allow it.
func (p *ProxmoxProvider) enrichAPIResourceDescriptions(ctx context.Context, resources []proxmoxDiscoveredResource) []proxmoxDiscoveredResource {
	groups := make(map[string][]int)
	for index := range resources {
		if strings.TrimSpace(resources[index].Description) != "" || resources[index].VMID <= 0 {
			continue
		}
		node := strings.TrimSpace(resources[index].Node)
		if node == "" {
			continue
		}
		groups[node] = append(groups[node], index)
	}
	if len(groups) == 0 {
		return resources
	}

	type metadataBatchJob struct {
		node    string
		indexes []int
	}
	type metadataBatchResult struct {
		descriptions map[int]string
		retry        []int
	}

	jobs := make([]metadataBatchJob, 0)
	for node, indexes := range groups {
		for start := 0; start < len(indexes); start += maxProxmoxMetadataBatchSize {
			end := min(start+maxProxmoxMetadataBatchSize, len(indexes))
			jobs = append(jobs, metadataBatchJob{node: node, indexes: indexes[start:end]})
		}
	}

	jobQueue := make(chan metadataBatchJob)
	results := make(chan metadataBatchResult, len(jobs))
	workerCount := min(len(jobs), maxProxmoxMetadataWorkers)
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for job := range jobQueue {
				if ctx.Err() != nil {
					continue
				}
				descriptions, retry, err := p.fetchAPIResourceDescriptionsBatch(ctx, job.node, job.indexes, resources)
				if err != nil {
					retry = append([]int(nil), job.indexes...)
				}
				results <- metadataBatchResult{descriptions: descriptions, retry: retry}
			}
		}()
	}

	go func() {
		defer close(jobQueue)
		for _, job := range jobs {
			select {
			case jobQueue <- job:
			case <-ctx.Done():
				return
			}
		}
	}()
	workers.Wait()
	close(results)

	retry := make([]int, 0)
	for result := range results {
		for index, description := range result.descriptions {
			resources[index].Description = description
		}
		retry = append(retry, result.retry...)
	}
	p.enrichAPIResourceDescriptionsIndividually(ctx, resources, retry)
	return resources
}

func (p *ProxmoxProvider) fetchAPIResourceDescriptionsBatch(
	ctx context.Context,
	node string,
	indexes []int,
	resources []proxmoxDiscoveredResource,
) (map[int]string, []int, error) {
	commands := make([]proxmoxBatchCommand, 0, len(indexes))
	commandIndexes := make([]int, 0, len(indexes))
	retry := make([]int, 0)
	for _, index := range indexes {
		kind, err := normalizeProxmoxResourceKind(resources[index].Type)
		if err != nil {
			retry = append(retry, index)
			continue
		}
		commands = append(commands, proxmoxBatchCommand{
			Path:   fmt.Sprintf("%s/%d/config", kind, resources[index].VMID),
			Method: http.MethodGet,
		})
		commandIndexes = append(commandIndexes, index)
	}
	if len(commands) == 0 {
		return nil, retry, nil
	}

	commandsJSON, err := json.Marshal(commands)
	if err != nil {
		return nil, indexes, fmt.Errorf("编码Proxmox批量元数据命令失败: %w", err)
	}
	body, err := json.Marshal(map[string]string{"commands": string(commandsJSON)})
	if err != nil {
		return nil, indexes, fmt.Errorf("编码Proxmox批量元数据请求失败: %w", err)
	}
	executeURL := p.apiEndpoint(fmt.Sprintf(
		"/api2/json/nodes/%s/execute",
		url.PathEscape(strings.TrimSpace(node)),
	))
	response, err := p.makeAPIRequest(ctx, http.MethodPost, executeURL, body)
	if err != nil {
		return nil, indexes, err
	}
	var payload struct {
		Data []proxmoxBatchCommandResult `json:"data"`
	}
	if err := json.Unmarshal(response, &payload); err != nil {
		return nil, indexes, fmt.Errorf("解析Proxmox批量元数据响应失败: %w", err)
	}
	if len(payload.Data) != len(commandIndexes) {
		return nil, indexes, fmt.Errorf("Proxmox批量元数据响应数量异常: got %d want %d", len(payload.Data), len(commandIndexes))
	}

	descriptions := make(map[int]string, len(commandIndexes))
	for commandIndex, result := range payload.Data {
		resourceIndex := commandIndexes[commandIndex]
		if result.Status < http.StatusOK || result.Status >= http.StatusMultipleChoices {
			retry = append(retry, resourceIndex)
			continue
		}
		var config struct {
			Description string `json:"description"`
		}
		if len(result.Data) == 0 || string(result.Data) == "null" || json.Unmarshal(result.Data, &config) != nil {
			retry = append(retry, resourceIndex)
			continue
		}
		descriptions[resourceIndex] = strings.TrimSpace(config.Description)
	}
	return descriptions, retry, nil
}

func (p *ProxmoxProvider) enrichAPIResourceDescriptionsIndividually(ctx context.Context, resources []proxmoxDiscoveredResource, indexes []int) {
	workerCount := min(len(indexes), maxProxmoxMetadataWorkers)
	if workerCount == 0 {
		return
	}
	jobs := make(chan int)
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for index := range jobs {
				if ctx.Err() != nil || strings.TrimSpace(resources[index].Description) != "" {
					continue
				}
				if description, err := p.fetchAPIResourceDescription(ctx, resources[index]); err == nil {
					resources[index].Description = description
				}
			}
		}()
	}

enqueue:
	for _, index := range indexes {
		select {
		case jobs <- index:
		case <-ctx.Done():
			break enqueue
		}
	}
	close(jobs)
	workers.Wait()
}

func (p *ProxmoxProvider) fetchAPIResourceDescription(ctx context.Context, resource proxmoxDiscoveredResource) (string, error) {
	if resource.VMID <= 0 || strings.TrimSpace(resource.Node) == "" {
		return "", fmt.Errorf("Proxmox资源缺少节点或vmid")
	}
	kind, err := normalizeProxmoxResourceKind(resource.Type)
	if err != nil {
		return "", err
	}
	configURL := p.apiEndpoint(fmt.Sprintf(
		"/api2/json/nodes/%s/%s/%d/config",
		url.PathEscape(strings.TrimSpace(resource.Node)),
		kind,
		resource.VMID,
	))
	response, err := p.makeAPIRequest(ctx, http.MethodGet, configURL, nil)
	if err != nil {
		return "", err
	}
	var payload struct {
		Data struct {
			Description string `json:"description"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response, &payload); err != nil {
		return "", err
	}
	return strings.TrimSpace(payload.Data.Description), nil
}

func normalizeProxmoxResourceKind(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "qemu", "vm":
		return "qemu", nil
	case "lxc":
		return "lxc", nil
	default:
		return "", fmt.Errorf("未知的Proxmox实例类型")
	}
}

// sshDiscoverInstances uses the same cluster resource data as the API path.
// pvesh is available on all supported PVE releases and includes both QEMU and
// LXC resources, avoiding the old incomplete `pct config` parser.
func (p *ProxmoxProvider) sshDiscoverInstances(ctx context.Context) ([]provider.DiscoveredInstance, error) {
	if !p.sshClient.HasExecutor() {
		return nil, fmt.Errorf("SSH client not initialized")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	output, err := p.sshClient.Execute("pvesh get /cluster/resources --type vm --output-format json")
	if err != nil {
		return nil, fmt.Errorf("SSH获取Proxmox实例失败: %w", err)
	}

	instances, err := p.parseResourcesJSON(output)
	if err != nil {
		return nil, fmt.Errorf("SSH解析Proxmox实例失败: %w", err)
	}
	instances = p.enrichDiscoveredInstances(ctx, instances)
	global.APP_LOG.Debug("Proxmox SSH发现实例完成",
		zap.String("provider", p.config.Name),
		zap.Int("count", len(instances)))
	return instances, nil
}

// enrichDiscoveredInstances fills runtime addresses and imports DNAT mappings
// from both nftables and iptables. Discovery remains usable when an individual
// guest has no agent/IP information: the default VMID-derived address is only
// accepted when firewall rules actually target it.
func (p *ProxmoxProvider) enrichDiscoveredInstances(ctx context.Context, instances []provider.DiscoveredInstance) []provider.DiscoveredInstance {
	if !p.shouldUseSSH() || !p.sshClient.HasExecutor() {
		return instances
	}

	fwMgr := firewall.NewManager(p.sshClient, "proxmox", "")
	rulesByIP := fwMgr.DiscoverAllDNATRules()
	for index := range instances {
		if err := ctx.Err(); err != nil {
			break
		}
		instance := &instances[index]
		vmid := strings.TrimSpace(instance.ProviderInstanceID)
		if vmid == "" {
			continue
		}

		if ip, err := p.getInstanceIPAddress(ctx, vmid, instance.InstanceType); err == nil {
			instance.PrivateIP = strings.TrimSpace(ip)
		}
		candidateIPs := make([]string, 0, 2)
		if instance.PrivateIP != "" {
			candidateIPs = append(candidateIPs, instance.PrivateIP)
		}
		if instance.PrivateIP == "" {
			numericVMID, err := strconv.Atoi(vmid)
			if err != nil {
				continue
			}
			inferredIP := p.vmidToInternalIP(numericVMID)
			if inferredIP != "" {
				candidateIPs = append(candidateIPs, inferredIP)
			}
		}

		for _, candidateIP := range candidateIPs {
			rules := rulesByIP[candidateIP]
			if len(rules) == 0 {
				continue
			}
			if instance.PrivateIP == "" {
				instance.PrivateIP = candidateIP
			}
			for _, rule := range rules {
				instance.PortMappings = append(instance.PortMappings, provider.DiscoveredPortMapping{
					HostPort: rule.HostPort, GuestPort: rule.GuestPort, Protocol: rule.Protocol, IsSSH: rule.IsSSH, MappingMethod: "iptables",
				})
				if rule.IsSSH {
					instance.SSHPort = rule.HostPort
				} else {
					instance.ExtraPorts = append(instance.ExtraPorts, rule.HostPort)
				}
			}
		}
	}
	return instances
}

func (p *ProxmoxProvider) parseVMsResponse(respData []byte, nodeName string) ([]provider.DiscoveredInstance, error) {
	var payload struct {
		Data []proxmoxDiscoveredResource `json:"data"`
	}
	if err := json.Unmarshal(respData, &payload); err != nil {
		return nil, err
	}
	for index := range payload.Data {
		payload.Data[index].Type = "qemu"
		if payload.Data[index].Node == "" {
			payload.Data[index].Node = nodeName
		}
	}
	return p.convertDiscoveredResources(payload.Data)
}

func (p *ProxmoxProvider) parseLXCsResponse(respData []byte, nodeName string) ([]provider.DiscoveredInstance, error) {
	var payload struct {
		Data []proxmoxDiscoveredResource `json:"data"`
	}
	if err := json.Unmarshal(respData, &payload); err != nil {
		return nil, err
	}
	for index := range payload.Data {
		payload.Data[index].Type = "lxc"
		if payload.Data[index].Node == "" {
			payload.Data[index].Node = nodeName
		}
	}
	return p.convertDiscoveredResources(payload.Data)
}

func (p *ProxmoxProvider) parseResourcesJSON(jsonOutput string) ([]provider.DiscoveredInstance, error) {
	resources, err := parseProxmoxResourcesJSON(jsonOutput)
	if err != nil {
		return nil, err
	}
	return p.convertDiscoveredResources(resources)
}

func parseProxmoxResourcesJSON(jsonOutput string) ([]proxmoxDiscoveredResource, error) {
	var resources []proxmoxDiscoveredResource
	if err := json.Unmarshal([]byte(strings.TrimSpace(jsonOutput)), &resources); err != nil {
		return nil, err
	}
	return resources, nil
}

func parseProxmoxResourceEnvelope(data []byte) ([]proxmoxDiscoveredResource, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	rawData, exists := envelope["data"]
	if !exists || len(bytes.TrimSpace(rawData)) == 0 || bytes.Equal(bytes.TrimSpace(rawData), []byte("null")) {
		return nil, fmt.Errorf("Proxmox API响应缺少data数组")
	}
	var resources []proxmoxDiscoveredResource
	if err := json.Unmarshal(rawData, &resources); err != nil {
		return nil, fmt.Errorf("Proxmox API data必须是数组: %w", err)
	}
	return resources, nil
}

func parseProxmoxTemplateFlag(raw json.RawMessage) (bool, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" || trimmed == "0" || trimmed == "false" || trimmed == `"0"` || trimmed == `"false"` {
		return false, nil
	}
	if trimmed == "1" || trimmed == "true" || trimmed == `"1"` || trimmed == `"true"` {
		return true, nil
	}
	return false, fmt.Errorf("无效的template标记: %s", utils.TruncateString(trimmed, 32))
}

func (p *ProxmoxProvider) convertDiscoveredResources(resources []proxmoxDiscoveredResource) ([]provider.DiscoveredInstance, error) {
	instances := make([]provider.DiscoveredInstance, 0, len(resources))
	for _, resource := range resources {
		isTemplate, err := parseProxmoxTemplateFlag(resource.Template)
		if err != nil {
			return nil, fmt.Errorf("Proxmox资源 %q: %w", resource.ID, err)
		}
		if isTemplate {
			continue
		}

		if resource.Type != "qemu" && resource.Type != "vm" && resource.Type != "lxc" {
			return nil, fmt.Errorf("Proxmox资源 %q 包含未知实例类型 %q", resource.ID, resource.Type)
		}
		instanceType := p.mapProxmoxType(resource.Type)

		remoteID := strconv.FormatInt(resource.VMID, 10)
		if resource.VMID <= 0 {
			return nil, fmt.Errorf("Proxmox资源 %q 缺少有效vmid", resource.ID)
		}
		name := strings.TrimSpace(resource.Name)
		if name == "" {
			if instanceType == "vm" {
				name = "vm-" + remoteID
			} else {
				name = "ct-" + remoteID
			}
		}

		cpu := int(math.Ceil(resource.MaxCPU))
		if cpu <= 0 {
			cpu = int(math.Ceil(resource.CPUs))
		}
		if cpu <= 0 {
			cpu = 1
		}
		memory := resource.MaxMem / 1024 / 1024
		if memory <= 0 {
			memory = 512
		}
		disk := resource.MaxDisk / 1024 / 1024
		if disk <= 0 {
			disk = 10240
		}

		canonicalType := "lxc"
		if instanceType == "vm" {
			canonicalType = "vm"
		}
		instances = append(instances, provider.DiscoveredInstance{
			UUID:               fmt.Sprintf("proxmox-%s-%s", canonicalType, remoteID),
			ProviderInstanceID: remoteID,
			Name:               name,
			Status:             p.mapProxmoxStatus(resource.Status),
			InstanceType:       instanceType,
			CPU:                cpu,
			Memory:             memory,
			Disk:               disk,
			PrivateIP:          resource.PrivateIP,
			IPv6Address:        resource.IPv6Address,
			SSHPort:            22,
			RawData:            resource,
			RuntimeIdentity: &provider.RecoveryInstanceIdentity{
				Node: strings.TrimSpace(resource.Node),
				ID:   remoteID,
				Type: instanceType,
			},
		})
	}
	return instances, nil
}

func (p *ProxmoxProvider) mapProxmoxStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running":
		return "running"
	case "stopped":
		return "stopped"
	case "paused", "suspended":
		return "paused"
	default:
		return strings.ToLower(strings.TrimSpace(status))
	}
}

func (p *ProxmoxProvider) mapProxmoxType(proxmoxType string) string {
	if proxmoxType == "qemu" || proxmoxType == "vm" {
		return "vm"
	}
	return "container"
}

func (p *ProxmoxProvider) makeAPIRequest(ctx context.Context, method, url string, body []byte) ([]byte, error) {
	var requestBody io.Reader
	if len(body) > 0 {
		requestBody = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, requestBody)
	if err != nil {
		return nil, fmt.Errorf("创建Proxmox API请求失败: %w", err)
	}
	p.setAPIAuth(req)
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := p.apiClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Proxmox API请求失败: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxProxmoxDiscoveryResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("读取Proxmox API响应失败: %w", err)
	}
	if len(data) > maxProxmoxDiscoveryResponseSize {
		return nil, fmt.Errorf("Proxmox API响应超过%d字节限制", maxProxmoxDiscoveryResponseSize)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("Proxmox API返回HTTP %d: %s", resp.StatusCode, utils.TruncateString(strings.TrimSpace(string(data)), 500))
	}
	return data, nil
}
