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

// PVE resources can be migrated, replaced, or even recreated as a different
// runtime family while the controller stays up. Keep this cache deliberately
// short: it de-duplicates concurrent console opens without turning stale
// instance_type data into a two-minute source of truth.
const proxmoxConsoleRuntimeCacheTTL = 15 * time.Second

type proxmoxConsoleResource struct {
	VMID     int64           `json:"vmid"`
	Name     string          `json:"name"`
	Type     string          `json:"type"`
	Node     string          `json:"node"`
	Status   string          `json:"status"`
	Template json.RawMessage `json:"template"`
}

type proxmoxConsoleRuntimeCacheEntry struct {
	runtimeID   string
	runtimeType string
	node        string
	status      string
	reason      string
	updatedAt   time.Time
}

// proxmoxConsoleRuntime is the observed PVE object, not a projection of the
// controller record. type is qemu or lxc after normalization.
type proxmoxConsoleRuntime struct {
	ID     string
	Type   string
	Node   string
	Status string
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

func cacheProxmoxConsoleRuntime(key string, runtime proxmoxConsoleRuntime, reason string) {
	proxmoxConsoleRuntimeMu.Lock()
	proxmoxConsoleRuntimeCache[key] = proxmoxConsoleRuntimeCacheEntry{
		runtimeID:   runtime.ID,
		runtimeType: runtime.Type,
		node:        runtime.Node,
		status:      runtime.Status,
		reason:      reason,
		updatedAt:   time.Now(),
	}
	proxmoxConsoleRuntimeMu.Unlock()
}

// invalidateProxmoxConsoleRuntime removes all short-lived observations for one
// panel instance after its lifecycle state or mapped control channels change.
// It is called outside database transactions by the shared console cache hook.
func invalidateProxmoxConsoleRuntime(instanceID uint) {
	if instanceID == 0 {
		return
	}
	prefix := strconv.FormatUint(uint64(instanceID), 10) + ":"
	proxmoxConsoleRuntimeMu.Lock()
	for key := range proxmoxConsoleRuntimeCache {
		if strings.HasPrefix(key, prefix) {
			delete(proxmoxConsoleRuntimeCache, key)
		}
	}
	proxmoxConsoleRuntimeMu.Unlock()
}

// resolveProxmoxConsoleRuntime observes the live PVE resource even when a
// record already contains a numeric VMID. A numeric identifier says nothing
// about whether that ID currently refers to qemu or lxc, so it cannot safely
// decide graphical or terminal capabilities by itself.
func resolveProxmoxConsoleRuntime(inst providerModel.Instance, p providerModel.Provider) (proxmoxConsoleRuntime, error) {
	currentID := strings.TrimSpace(inst.ProviderVMID)
	if inst.ID == 0 {
		return proxmoxConsoleRuntime{}, fmt.Errorf("PVE 控制台缺少实例记录")
	}

	cacheKey := fmt.Sprintf("%d:%d:%s:%s:%s:%s", inst.ID, p.ID, currentID, strings.TrimSpace(inst.Name), strings.TrimSpace(p.HostName), strings.ToLower(strings.TrimSpace(inst.Status)))
	if cached, ok := cachedProxmoxConsoleRuntime(cacheKey); ok {
		if cached.reason != "" {
			return proxmoxConsoleRuntime{}, fmt.Errorf("%s", cached.reason)
		}
		return proxmoxConsoleRuntime{ID: cached.runtimeID, Type: cached.runtimeType, Node: cached.node, Status: cached.status}, nil
	}

	value, err, _ := proxmoxConsoleRuntimeGroup.Do(cacheKey, func() (interface{}, error) {
		runtime, lookupErr := lookupProxmoxConsoleRuntime(inst, p)
		if lookupErr != nil {
			reason := lookupErr.Error()
			cacheProxmoxConsoleRuntime(cacheKey, proxmoxConsoleRuntime{}, reason)
			return nil, lookupErr
		}
		cacheProxmoxConsoleRuntime(cacheKey, runtime, "")
		return runtime, nil
	})
	if err != nil {
		return proxmoxConsoleRuntime{}, err
	}
	runtime, ok := value.(proxmoxConsoleRuntime)
	if !ok || !isProxmoxConsoleRuntimeID(runtime.ID) || normalizeProxmoxConsoleRuntimeType(runtime.Type) == "" {
		return proxmoxConsoleRuntime{}, fmt.Errorf("PVE 控制台未返回有效的运行时资源")
	}
	return runtime, nil
}

// resolveProxmoxConsoleRuntimeID is retained for callers that only need the
// identifier. New capability code must use resolveProxmoxConsoleRuntime so it
// cannot lose the observed resource type.
func resolveProxmoxConsoleRuntimeID(inst providerModel.Instance, p providerModel.Provider) (string, error) {
	runtime, err := resolveProxmoxConsoleRuntime(inst, p)
	if err != nil {
		return "", err
	}
	return runtime.ID, nil
}

func lookupProxmoxConsoleRuntime(inst providerModel.Instance, p providerModel.Provider) (proxmoxConsoleRuntime, error) {
	executor, cleanup, err := newConsoleExecutor(p)
	if err != nil {
		return proxmoxConsoleRuntime{}, fmt.Errorf("无法连接 PVE 节点以恢复实例 ID: %w", err)
	}
	defer cleanup()

	runtime, lookupReason := lookupProxmoxConsoleRuntimeFromCluster(executor, inst, p)
	if lookupReason != nil {
		// Cluster-wide inventory can be restricted on a delegated PVE account.
		// A numeric VMID/CTID still permits an exact bounded lookup on qemu and
		// lxc without falling back to the panel's stored InstanceType.
		if direct, directErr := lookupProxmoxConsoleRuntimeByID(executor, inst, p); directErr == nil {
			runtime = direct
		} else {
			return proxmoxConsoleRuntime{}, fmt.Errorf("读取 PVE 实例运行态失败: %v；单资源回退失败: %v", lookupReason, directErr)
		}
	}
	// Do not touch updated_at on every console capability read. Persist only a
	// recovered legacy/display identifier; a numeric ID already in sync needs
	// no database mutation.
	if strings.TrimSpace(inst.ProviderVMID) != runtime.ID {
		if err := persistProxmoxConsoleRuntimeID(inst, runtime.ID); err != nil {
			return proxmoxConsoleRuntime{}, err
		}
	}
	return runtime, nil
}

func lookupProxmoxConsoleRuntimeFromCluster(executor utils.ShellExecutor, inst providerModel.Instance, p providerModel.Provider) (proxmoxConsoleRuntime, error) {
	output, err := executor.ExecuteWithTimeout("pvesh get /cluster/resources --type vm --output-format json", 20*time.Second)
	if err != nil {
		return proxmoxConsoleRuntime{}, fmt.Errorf("读取 PVE 实例清单失败: %w；远端输出: %s", err, utils.TruncateString(strings.TrimSpace(output), 600))
	}
	resources, err := parseProxmoxConsoleResources(output)
	if err != nil {
		return proxmoxConsoleRuntime{}, fmt.Errorf("解析 PVE 实例清单失败: %w", err)
	}
	runtime, err := findProxmoxConsoleRuntime(resources, inst, p.HostName)
	if err != nil {
		return proxmoxConsoleRuntime{}, err
	}
	return runtime, nil
}

// lookupProxmoxConsoleRuntimeByID checks both PVE resource families with the
// actual numeric ID. It is used only when inventory lookup fails, so a normal
// console request stays one cluster query while restricted nodes retain a
// correct qemu/lxc classification without an N+1 list scan.
func lookupProxmoxConsoleRuntimeByID(executor utils.ShellExecutor, inst providerModel.Instance, p providerModel.Provider) (proxmoxConsoleRuntime, error) {
	runtimeID := strings.TrimSpace(inst.ProviderVMID)
	if !isProxmoxConsoleRuntimeID(runtimeID) {
		return proxmoxConsoleRuntime{}, fmt.Errorf("实例未保存有效 VMID/CTID，无法在无清单权限时安全恢复")
	}
	nodes, err := proxmoxConsoleCandidateNodes(executor, p.HostName)
	if err != nil {
		return proxmoxConsoleRuntime{}, err
	}
	matches := make([]proxmoxConsoleRuntime, 0, 1)
	for _, node := range nodes {
		for _, runtimeType := range []string{"qemu", "lxc"} {
			command := proxmoxConsoleDirectStatusCommand(node, runtimeType, runtimeID)
			output, statusErr := executor.ExecuteWithTimeout(command, consoleRuntimeProbeTimeout)
			if statusErr != nil {
				continue
			}
			status, parsed := parseProxmoxConsoleDirectStatus(output)
			if !parsed {
				continue
			}
			matches = append(matches, proxmoxConsoleRuntime{ID: runtimeID, Type: runtimeType, Node: node, Status: status})
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return proxmoxConsoleRuntime{}, fmt.Errorf("PVE VMID/CTID %q 在单资源探测中匹配多个对象，拒绝猜测", runtimeID)
	}
	return proxmoxConsoleRuntime{}, fmt.Errorf("PVE 中未找到 VMID/CTID %q 的 qemu 或 lxc 运行时对象", runtimeID)
}

func proxmoxConsoleCandidateNodes(executor utils.ShellExecutor, configuredNode string) ([]string, error) {
	configuredNode = strings.TrimSpace(configuredNode)
	nodes := make([]string, 0, 2)
	add := func(node string) {
		node = strings.TrimSpace(node)
		if !isProxmoxConsoleNodeName(node) {
			return
		}
		for _, existing := range nodes {
			if existing == node {
				return
			}
		}
		nodes = append(nodes, node)
	}
	add(configuredNode)
	output, err := executor.ExecuteWithTimeout("hostname", consoleRuntimeProbeTimeout)
	if err == nil {
		add(strings.TrimSpace(output))
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("PVE 未返回可用节点名")
	}
	return nodes, nil
}

func isProxmoxConsoleNodeName(node string) bool {
	if node == "" {
		return false
	}
	for _, char := range node {
		if !(char >= 'a' && char <= 'z') && !(char >= 'A' && char <= 'Z') && !(char >= '0' && char <= '9') && char != '.' && char != '-' && char != '_' {
			return false
		}
	}
	return true
}

func proxmoxConsoleDirectStatusCommand(node, runtimeType, runtimeID string) string {
	return fmt.Sprintf("pvesh get %s --output-format json", utils.ShellSingleQuote(fmt.Sprintf("/nodes/%s/%s/%s/status/current", node, runtimeType, runtimeID)))
}

func parseProxmoxConsoleDirectStatus(raw string) (string, bool) {
	var response struct {
		Status string `json:"status"`
		Data   struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(raw)), &response) != nil {
		return "", false
	}
	status := strings.TrimSpace(response.Status)
	if status == "" {
		status = strings.TrimSpace(response.Data.Status)
	}
	return status, status != ""
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

func normalizeProxmoxConsoleRuntimeType(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "qemu", "vm":
		return "qemu"
	case "lxc", "container":
		return "lxc"
	default:
		return ""
	}
}

func findProxmoxConsoleRuntime(resources []proxmoxConsoleResource, inst providerModel.Instance, expectedNode string) (proxmoxConsoleRuntime, error) {
	expectedNode = strings.TrimSpace(expectedNode)
	validResource := func(resource proxmoxConsoleResource) bool {
		return resource.VMID > 0 && !isProxmoxConsoleTemplate(resource.Template) &&
			normalizeProxmoxConsoleRuntimeType(resource.Type) != ""
	}
	toRuntime := func(resource proxmoxConsoleResource) proxmoxConsoleRuntime {
		return proxmoxConsoleRuntime{
			ID: strconv.FormatInt(resource.VMID, 10), Type: normalizeProxmoxConsoleRuntimeType(resource.Type), Node: strings.TrimSpace(resource.Node), Status: strings.TrimSpace(resource.Status),
		}
	}

	// A numeric provider_vm_id is a stable PVE identity. Resolve it first, but
	// still inspect the live resource to obtain qemu/lxc rather than accepting
	// the panel's stored instance_type.
	if currentID := strings.TrimSpace(inst.ProviderVMID); isProxmoxConsoleRuntimeID(currentID) {
		// VMID/CTID remains stable when a PVE guest migrates, while the node
		// name changes. Do not let a stale provider host name hide a live
		// resource with the exact observed identifier.
		matches := make([]proxmoxConsoleRuntime, 0, 1)
		for _, resource := range resources {
			if validResource(resource) && strconv.FormatInt(resource.VMID, 10) == currentID {
				matches = append(matches, toRuntime(resource))
			}
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
		if len(matches) > 1 {
			return proxmoxConsoleRuntime{}, fmt.Errorf("PVE 中 VMID/CTID %q 对应多个运行时资源，拒绝猜测", currentID)
		}
	}

	names := map[string]struct{}{}
	for _, candidate := range []string{inst.Name, inst.ProviderVMID} {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" && !isProxmoxConsoleRuntimeID(candidate) {
			names[candidate] = struct{}{}
		}
	}
	if len(names) == 0 {
		return proxmoxConsoleRuntime{}, fmt.Errorf("PVE 实例缺少可用于恢复 VMID/CTID 的名称")
	}

	matches := make([]proxmoxConsoleRuntime, 0, 1)
	for _, resource := range resources {
		if !validResource(resource) {
			continue
		}
		if _, ok := names[strings.TrimSpace(resource.Name)]; !ok {
			continue
		}
		matches = append(matches, toRuntime(resource))
	}
	if expectedNode != "" {
		nodeMatches := make([]proxmoxConsoleRuntime, 0, 1)
		for _, match := range matches {
			if match.Node == "" || match.Node == expectedNode {
				nodeMatches = append(nodeMatches, match)
			}
		}
		if len(nodeMatches) == 1 {
			return nodeMatches[0], nil
		}
		// If no preferred-node match remains, use a unique live resource from a
		// migrated guest rather than reporting a stale-node false negative.
	}
	if len(matches) == 0 {
		return proxmoxConsoleRuntime{}, fmt.Errorf("PVE 中未找到名称为 %q 的运行时资源，无法恢复 VMID/CTID", inst.Name)
	}
	if len(matches) > 1 {
		return proxmoxConsoleRuntime{}, fmt.Errorf("PVE 中存在多个同名运行时资源，拒绝猜测 VMID/CTID")
	}
	return matches[0], nil
}

func findProxmoxConsoleRuntimeID(resources []proxmoxConsoleResource, inst providerModel.Instance, expectedNode string) (string, error) {
	runtime, err := findProxmoxConsoleRuntime(resources, inst, expectedNode)
	if err != nil {
		return "", err
	}
	return runtime.ID, nil
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

func buildProxmoxConsoleVNCTarget(inst providerModel.Instance, p providerModel.Provider, runtimeID, runtimeNode string, runtimeErr error) consoleTarget {
	target := consoleTarget{
		protocol:    consoleProtocolVNC,
		proxmoxVNC:  true,
		runtimeID:   runtimeID,
		runtimeNode: strings.TrimSpace(runtimeNode),
		instanceID:  inst.ID,
		provider:    p,
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
