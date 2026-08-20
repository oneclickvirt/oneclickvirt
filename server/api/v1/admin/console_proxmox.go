package admin

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/utils"

	"golang.org/x/sync/singleflight"
)

const proxmoxConsoleRuntimeCacheTTL = 2 * time.Minute

type proxmoxConsoleResource struct {
	VMID     int64           `json:"vmid"`
	Name     string          `json:"name"`
	Type     string          `json:"type"`
	Node     string          `json:"node"`
	Template json.RawMessage `json:"template"`
}

type proxmoxConsoleRuntimeCacheEntry struct {
	runtimeID string
	reason    string
	updatedAt time.Time
}

var (
	proxmoxConsoleRuntimeMu    sync.Mutex
	proxmoxConsoleRuntimeCache = make(map[string]proxmoxConsoleRuntimeCacheEntry)
	proxmoxConsoleRuntimeGroup singleflight.Group
)

func isProxmoxConsoleProviderType(providerType string) bool {
	switch strings.ToLower(strings.TrimSpace(providerType)) {
	case "proxmox", "proxmoxve", "pve":
		return true
	default:
		return false
	}
}

func isProxmoxConsoleRuntimeID(value string) bool {
	vmid, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	return err == nil && vmid > 0
}

func cachedProxmoxConsoleRuntime(key string) (proxmoxConsoleRuntimeCacheEntry, bool) {
	proxmoxConsoleRuntimeMu.Lock()
	defer proxmoxConsoleRuntimeMu.Unlock()
	now := time.Now()
	for cacheKey, entry := range proxmoxConsoleRuntimeCache {
		if now.Sub(entry.updatedAt) > proxmoxConsoleRuntimeCacheTTL {
			delete(proxmoxConsoleRuntimeCache, cacheKey)
		}
	}
	entry, ok := proxmoxConsoleRuntimeCache[key]
	return entry, ok
}

func cacheProxmoxConsoleRuntime(key, runtimeID, reason string) {
	proxmoxConsoleRuntimeMu.Lock()
	proxmoxConsoleRuntimeCache[key] = proxmoxConsoleRuntimeCacheEntry{
		runtimeID: runtimeID,
		reason:    reason,
		updatedAt: time.Now(),
	}
	proxmoxConsoleRuntimeMu.Unlock()
}

// resolveProxmoxConsoleRuntimeID upgrades legacy records that stored the panel
// display name in provider_vm_id. It performs one bounded PVE resource query,
// keeps the remote call outside database transactions, and only conditionally
// writes the discovered VMID/CTID when the legacy value has not changed.
func resolveProxmoxConsoleRuntimeID(inst providerModel.Instance, p providerModel.Provider) (string, error) {
	currentID := strings.TrimSpace(inst.ProviderVMID)
	if isProxmoxConsoleRuntimeID(currentID) {
		return currentID, nil
	}
	if inst.ID == 0 {
		return "", fmt.Errorf("PVE 控制台缺少实例记录")
	}

	cacheKey := fmt.Sprintf("%d:%s:%s:%s", inst.ID, currentID, strings.TrimSpace(inst.Name), strings.TrimSpace(inst.InstanceType))
	if cached, ok := cachedProxmoxConsoleRuntime(cacheKey); ok {
		if cached.reason != "" {
			return "", fmt.Errorf("%s", cached.reason)
		}
		return cached.runtimeID, nil
	}

	value, err, _ := proxmoxConsoleRuntimeGroup.Do(cacheKey, func() (interface{}, error) {
		runtimeID, lookupErr := lookupProxmoxConsoleRuntimeID(inst, p)
		if lookupErr != nil {
			reason := lookupErr.Error()
			cacheProxmoxConsoleRuntime(cacheKey, "", reason)
			return nil, lookupErr
		}
		cacheProxmoxConsoleRuntime(cacheKey, runtimeID, "")
		return runtimeID, nil
	})
	if err != nil {
		return "", err
	}
	runtimeID, ok := value.(string)
	if !ok || !isProxmoxConsoleRuntimeID(runtimeID) {
		return "", fmt.Errorf("PVE 控制台未返回有效的 VMID/CTID")
	}
	return runtimeID, nil
}

func lookupProxmoxConsoleRuntimeID(inst providerModel.Instance, p providerModel.Provider) (string, error) {
	executor, cleanup, err := newConsoleExecutor(p)
	if err != nil {
		return "", fmt.Errorf("无法连接 PVE 节点以恢复实例 ID: %w", err)
	}
	defer cleanup()

	output, err := executor.ExecuteWithTimeout("pvesh get /cluster/resources --type vm --output-format json", 20*time.Second)
	if err != nil {
		return "", fmt.Errorf("读取 PVE 实例清单失败: %w；远端输出: %s", err, utils.TruncateString(strings.TrimSpace(output), 600))
	}
	resources, err := parseProxmoxConsoleResources(output)
	if err != nil {
		return "", fmt.Errorf("解析 PVE 实例清单失败: %w", err)
	}
	runtimeID, err := findProxmoxConsoleRuntimeID(resources, inst, p.HostName)
	if err != nil {
		return "", err
	}
	if err := persistProxmoxConsoleRuntimeID(inst, runtimeID); err != nil {
		return "", err
	}
	return runtimeID, nil
}

func parseProxmoxConsoleResources(output string) ([]proxmoxConsoleResource, error) {
	raw := []byte(strings.TrimSpace(output))
	if len(raw) == 0 {
		return nil, fmt.Errorf("PVE 返回了空实例清单")
	}
	var resources []proxmoxConsoleResource
	if err := json.Unmarshal(raw, &resources); err == nil {
		return resources, nil
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil, fmt.Errorf("PVE 实例清单缺少 data 数组")
	}
	if err := json.Unmarshal(envelope.Data, &resources); err != nil {
		return nil, err
	}
	return resources, nil
}

func findProxmoxConsoleRuntimeID(resources []proxmoxConsoleResource, inst providerModel.Instance, expectedNode string) (string, error) {
	instanceType := strings.ToLower(strings.TrimSpace(inst.InstanceType))
	isVM := utils.IsVirtualMachineInstanceType(instanceType)
	expectedType := "lxc"
	if isVM {
		expectedType = "qemu"
	}
	names := map[string]struct{}{}
	for _, candidate := range []string{inst.Name, inst.ProviderVMID} {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" && !isProxmoxConsoleRuntimeID(candidate) {
			names[candidate] = struct{}{}
		}
	}
	if len(names) == 0 {
		return "", fmt.Errorf("PVE 实例缺少可用于恢复 VMID/CTID 的名称")
	}

	expectedNode = strings.TrimSpace(expectedNode)
	matches := make([]string, 0, 1)
	for _, resource := range resources {
		resourceType := strings.ToLower(strings.TrimSpace(resource.Type))
		if resourceType == "vm" {
			resourceType = "qemu"
		}
		if resourceType != expectedType || resource.VMID <= 0 || isProxmoxConsoleTemplate(resource.Template) {
			continue
		}
		if expectedNode != "" && strings.TrimSpace(resource.Node) != "" && strings.TrimSpace(resource.Node) != expectedNode {
			continue
		}
		if _, ok := names[strings.TrimSpace(resource.Name)]; !ok {
			continue
		}
		matches = append(matches, strconv.FormatInt(resource.VMID, 10))
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("PVE 中未找到名称为 %q 的%s，无法恢复 VMID/CTID", inst.Name, map[bool]string{true: "虚拟机", false: "容器"}[isVM])
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("PVE 中存在多个同名%s，拒绝猜测 VMID/CTID", map[bool]string{true: "虚拟机", false: "容器"}[isVM])
	}
	return matches[0], nil
}

func isProxmoxConsoleTemplate(raw json.RawMessage) bool {
	value := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	return value == "1" || strings.EqualFold(value, "true")
}

func persistProxmoxConsoleRuntimeID(inst providerModel.Instance, runtimeID string) error {
	currentID := strings.TrimSpace(inst.ProviderVMID)
	query := global.APP_DB.Model(&providerModel.Instance{}).Where("id = ?", inst.ID)
	if currentID == "" {
		query = query.Where("provider_vm_id IS NULL OR provider_vm_id = ''")
	} else {
		query = query.Where("provider_vm_id = ?", currentID)
	}
	if result := query.Update("provider_vm_id", runtimeID); result.Error != nil {
		return fmt.Errorf("保存 PVE VMID/CTID 失败: %w", result.Error)
	}
	return nil
}

func proxmoxConsoleVNCTransport(p providerModel.Provider) (string, string) {
	transport := normalizeConsoleTransport(p, "")
	switch transport {
	case "ssh":
		endpoint := strings.TrimSpace(p.Endpoint)
		if endpoint == "" {
			endpoint = strings.TrimSpace(p.PortIP)
		}
		if utils.ExtractHost(endpoint) == "" || strings.TrimSpace(p.Username) == "" {
			return transport, "PVE 图形控制台需要可用的 SSH 节点地址和用户名"
		}
		return transport, ""
	case "agent":
		if reason := consoleAgentTransportReason(p.ID); reason != "" {
			return transport, "PVE " + reason
		}
		return transport, ""
	case "local":
		return transport, ""
	default:
		if transport == "" {
			return transport, "PVE 节点未配置 SSH、Agent 或本机连接方式"
		}
		return transport, fmt.Sprintf("节点连接方式 %q 尚未提供 PVE 图形控制台代理", p.ConnectionType)
	}
}

func buildProxmoxConsoleVNCTarget(inst providerModel.Instance, p providerModel.Provider, runtimeID string, runtimeErr error) consoleTarget {
	target := consoleTarget{
		protocol:   consoleProtocolVNC,
		proxmoxVNC: true,
		runtimeID:  runtimeID,
		instanceID: inst.ID,
		provider:   p,
	}
	// PVE's vncproxy is a native, short-lived console endpoint created only
	// after the user selects VNC. It is unrelated to the generic raw-VNC port
	// setting (EnableVNC), which remains applicable to providers that expose a
	// fixed VNC listener. Requiring that setting here hid a working PVE VNC
	// option before it could ever be selected.
	if runtimeErr != nil {
		target.reason = "无法恢复 PVE VMID: " + runtimeErr.Error()
		return target
	}
	if !isProxmoxConsoleRuntimeID(runtimeID) {
		target.reason = "PVE 虚拟机缺少有效 VMID，无法建立图形控制台"
		return target
	}
	transport, reason := proxmoxConsoleVNCTransport(p)
	target.transport = transport
	target.available = reason == ""
	target.reason = reason
	return target
}
