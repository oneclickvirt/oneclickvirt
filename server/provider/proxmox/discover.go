package proxmox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/provider/firewall"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

const maxProxmoxDiscoveryResponseSize = 16 << 20

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

// apiDiscoverInstances uses the cluster resource endpoint so PVE 8/9 and
// multi-node clusters are handled in one request. A denied or malformed
// response is returned as an error instead of being mistaken for an empty
// clean node.
func (p *ProxmoxProvider) apiDiscoverInstances(ctx context.Context) ([]provider.DiscoveredInstance, error) {
	resourcesURL := fmt.Sprintf("https://%s:8006/api2/json/cluster/resources?type=vm", p.config.Host)
	resp, err := p.makeAPIRequest(ctx, http.MethodGet, resourcesURL, nil)
	if err != nil {
		return nil, fmt.Errorf("获取Proxmox集群实例失败: %w", err)
	}

	resources, err := parseProxmoxResourceEnvelope(resp)
	if err != nil {
		return nil, fmt.Errorf("解析Proxmox集群实例失败: %w", err)
	}
	return p.convertDiscoveredResources(resources)
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
	var resources []proxmoxDiscoveredResource
	if err := json.Unmarshal([]byte(strings.TrimSpace(jsonOutput)), &resources); err != nil {
		return nil, err
	}
	return p.convertDiscoveredResources(resources)
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
			SSHPort:            22,
			RawData:            resource,
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
