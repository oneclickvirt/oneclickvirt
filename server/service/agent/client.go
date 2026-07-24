package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// Client communicates with the oneclickvirt-agent HTTP API on a provider host.
type Client struct {
	baseURL     string
	token       string
	httpClient  *http.Client
	providerID  uint
	isAgentMode bool // true if provider uses agent reverse-connect mode
}

var (
	clientPool = sync.Map{} // providerID -> *Client
)

func GetClientWithMode(providerID uint, host string, port int, token string, isAgentMode bool) *Client {
	key := fmt.Sprintf("%d", providerID)
	if v, ok := clientPool.Load(key); ok {
		c := v.(*Client)
		expected := fmt.Sprintf("http://%s:%d", host, port)
		if c.baseURL == expected && c.token == token && c.isAgentMode == isAgentMode {
			return c
		}
		if global.APP_LOG != nil {
			global.APP_LOG.Debug("Agent客户端配置变化，刷新缓存",
				zap.Uint("providerID", providerID),
				zap.String("baseURL", expected),
				zap.Bool("agentMode", isAgentMode))
		}
	}

	c := &Client{
		baseURL:     fmt.Sprintf("http://%s:%d", host, port),
		token:       token,
		httpClient:  utils.GetHTTPClientWithTimeout(30 * time.Second),
		providerID:  providerID,
		isAgentMode: isAgentMode,
	}
	clientPool.Store(key, c)
	return c
}

// GetClient returns a cached or new agent client for the given provider.
func GetClient(providerID uint, host string, port int, token string) *Client {
	// Always re-check agent mode from DB — connection_type can change after
	// the client was first cached (e.g. provider reconfigured from SSH to agent).
	isAgent := false
	if global.APP_DB != nil {
		var p providerModel.Provider
		if err := global.APP_DB.Select("connection_type").Where("id = ?", providerID).First(&p).Error; err == nil {
			isAgent = p.ConnectionType == "agent"
		}
	}
	return GetClientWithMode(providerID, host, port, token, isAgent)
}

// RemoveClient removes the cached client for a provider.
func RemoveClient(providerID uint) {
	clientPool.Delete(fmt.Sprintf("%d", providerID))
}

// ---- Request/Response types ----

type AddRequest struct {
	Interface    interface{} `json:"interface"` // string or []string
	ProviderKind string      `json:"provider_kind,omitempty"`
	InstanceName string      `json:"instance_name,omitempty"`
	InnerIP      string      `json:"inner_ip,omitempty"`
}

type AddResponse struct {
	ID        int64    `json:"id"`
	Interface []string `json:"interface"`
}

type UpdateRequest struct {
	ID           int64       `json:"id"`
	NewInterface interface{} `json:"new_interface"` // string or []string
	ProviderKind string      `json:"provider_kind,omitempty"`
	InstanceName string      `json:"instance_name,omitempty"`
	InnerIP      string      `json:"inner_ip"`
}

type UpdateResponse struct {
	ID        int64    `json:"id"`
	Interface []string `json:"interface"`
}

type DeleteRequest struct {
	ID int64 `json:"id"`
}

type DeleteResponse struct {
	ID      int64 `json:"id"`
	Deleted bool  `json:"deleted"`
}

type InfoRequest struct {
	ID int64 `json:"id"`
}

type BatchInfoRequest struct {
	IDs []int64 `json:"ids"`
}

type BatchInfoResponse struct {
	Monitors []InfoResponse `json:"monitors"`
	Total    int            `json:"total"`
}

type InfoResponse struct {
	ID               int64    `json:"id"`
	Interface        []string `json:"interface"`
	UsedTraffic      uint64   `json:"used_traffic"`
	UsedTrafficIn    uint64   `json:"used_traffic_in"`
	UsedTrafficOut   uint64   `json:"used_traffic_out"`
	UsedTrafficHuman *string  `json:"used_traffic_human"`
	LastUpdateTime   int64    `json:"last_update_time"`
}

type ResourceQueryRequest struct {
	ID    int64 `json:"id"`
	Limit int64 `json:"limit,omitempty"`
}

type ResourceDataPoint struct {
	Timestamp   int64   `json:"timestamp"`
	CPUPercent  float64 `json:"cpu_percent"`
	MemoryUsed  uint64  `json:"memory_used"`
	MemoryTotal uint64  `json:"memory_total"`
	DiskUsed    uint64  `json:"disk_used"`
	DiskTotal   uint64  `json:"disk_total"`
}

type ResourceQueryResponse struct {
	ID   int64               `json:"id"`
	Data []ResourceDataPoint `json:"data"`
}

type CleanupRequest struct {
	MaxUpdateTime string `json:"max_update_time"`
}

type CleanupResponse struct {
	Deleted          int   `json:"deleted"`
	MaxUpdateSeconds int64 `json:"max_update_seconds"`
}

type ListMonitorItem struct {
	ID            int64    `json:"id"`
	Interface     []string `json:"interface"`
	ProviderKind  *string  `json:"provider_kind"`
	InstanceName  *string  `json:"instance_name"`
	TotalBytes    uint64   `json:"total_bytes"`
	TotalBytesIn  uint64   `json:"total_bytes_in"`
	TotalBytesOut uint64   `json:"total_bytes_out"`
	UpdatedAt     int64    `json:"updated_at"`
}

type ListMonitorsResponse struct {
	Monitors []ListMonitorItem `json:"monitors"`
	Total    int               `json:"total"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

// ---- API Methods ----

func (c *Client) doRequest(method, path string, body interface{}, result interface{}) error {
	requestTimeout := 35 * time.Second
	if path == "/api/v1/egress/state" {
		requestTimeout = 5 * time.Minute
	}
	// New egress endpoints can carry private tunnel material. Agent-mode nodes
	// must use the provider-bound typed WebSocket frame: HTTP-first could hit a
	// different localhost Agent, while the legacy curl fallback exposes JSON in
	// a shell process argument.
	if c.isAgentMode && strings.HasPrefix(path, "/api/v1/egress/") {
		conn, ok := GetHub().GetConn(c.providerID)
		if !ok || conn == nil {
			return fmt.Errorf("agent not connected for provider %d", c.providerID)
		}
		return conn.CallAPI(method, path, body, result, requestTimeout)
	}

	// Try HTTP first
	err := c.doHTTPRequestWithTimeout(method, path, body, result, requestTimeout)
	if err == nil {
		return nil
	}

	// For agent-mode providers behind NAT, HTTP may fail.
	// Fall back to WebSocket exec + curl to the agent's localhost API.
	if c.isAgentMode {
		if wsErr := c.doWSRequest(method, path, body, result); wsErr == nil {
			return nil
		} else {
			if global.APP_LOG != nil {
				global.APP_LOG.Warn("agent WS fallback failed, monitoring may not work",
					zap.Uint("provider_id", c.providerID),
					zap.String("path", path),
					zap.String("http_err", err.Error()),
					zap.String("ws_err", wsErr.Error()))
			}
			return fmt.Errorf("agent API call failed (http: %v, ws: %v)", err, wsErr)
		}
	}

	return err
}

// doHTTPRequest performs the actual HTTP call.
func (c *Client) doHTTPRequest(method, path string, body interface{}, result interface{}) error {
	return c.doHTTPRequestWithTimeout(method, path, body, result, 0)
}

func (c *Client) doHTTPRequestWithTimeout(method, path string, body interface{}, result interface{}, timeout time.Duration) error {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	url := c.baseURL + path
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-token", c.token)

	httpClient := c.httpClient
	if timeout > 0 && httpClient.Timeout != timeout {
		clone := *httpClient
		clone.Timeout = timeout
		httpClient = &clone
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request to %s failed: %w", url, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var errResp ErrorResponse
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			return fmt.Errorf("agent API error (status %d): %s", resp.StatusCode, errResp.Error)
		}
		return fmt.Errorf("agent API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("unmarshal response: %w", err)
		}
	}
	return nil
}

// doWSRequest executes the agent API call via WebSocket exec + curl to localhost.
// This is used as a fallback for agent-mode providers where the agent's HTTP API
// is behind NAT and only reachable via the WebSocket reverse connection.
func (c *Client) doWSRequest(method, path string, body interface{}, result interface{}) error {
	hub := GetHub()
	conn, ok := hub.GetConn(c.providerID)
	if !ok || conn == nil {
		return fmt.Errorf("agent not connected for provider %d", c.providerID)
	}

	// Extract port from baseURL (e.g., "http://127.0.0.1:23782" → "23782")
	port := fmt.Sprintf("%d", AgentPort)
	if idx := strings.LastIndex(c.baseURL, ":"); idx > 0 {
		port = c.baseURL[idx+1:]
	}

	// Build curl command to call agent's localhost HTTP API.
	// Use 127.0.0.1 explicitly to avoid IPv6 resolution issues
	// (the agent binds to 127.0.0.1:23782, not [::1]:23782).
	curlURL := fmt.Sprintf("http://127.0.0.1:%s%s", port, path)
	const statusMarker = "__OCV_HTTP_STATUS__:"
	curlCmd := fmt.Sprintf("curl -sS --max-time 25 -w %s -X %s %s -H %s -H %s",
		shellEscapeArg("\n"+statusMarker+"%{http_code}"),
		shellEscapeArg(method),
		shellEscapeArg(curlURL),
		shellEscapeArg("Content-Type: application/json"),
		shellEscapeArg("x-token: "+c.token))

	if body != nil {
		bodyJSON, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body for WS request: %w", err)
		}
		curlCmd += fmt.Sprintf(" -d %s", shellEscapeArg(string(bodyJSON)))
	}

	output, err := conn.ExecuteWithTimeout(curlCmd, 30*time.Second)
	if err != nil {
		return fmt.Errorf("ws exec curl failed for %s: %w (output: %s)", path, err, output)
	}

	output = strings.TrimRight(output, "\r\n")
	if output == "" {
		return fmt.Errorf("ws exec curl returned empty for %s", path)
	}

	statusIdx := strings.LastIndex(output, statusMarker)
	if statusIdx < 0 {
		return fmt.Errorf("ws exec curl returned response without status marker for %s: %s", path, output)
	}
	respBody := strings.TrimSpace(output[:statusIdx])
	statusRaw := strings.TrimSpace(output[statusIdx+len(statusMarker):])
	statusCode, parseStatusErr := strconv.Atoi(statusRaw)
	if parseStatusErr != nil {
		return fmt.Errorf("ws exec curl returned invalid status for %s: %q (body: %s)", path, statusRaw, respBody)
	}

	// Check if output looks like an error response
	var errResp ErrorResponse
	if json.Unmarshal([]byte(respBody), &errResp) == nil && errResp.Error != "" {
		return fmt.Errorf("agent API error via WS: %s", errResp.Error)
	}
	if statusCode >= 400 {
		return fmt.Errorf("agent API error via WS (status %d): %s", statusCode, respBody)
	}

	if result != nil {
		if err := json.Unmarshal([]byte(respBody), result); err != nil {
			return fmt.Errorf("unmarshal WS response for %s: %w (body: %s)", path, err, respBody)
		}
	}
	return nil
}

// AddMonitor creates a new monitor on the agent for the given interfaces.
func (c *Client) AddMonitor(interfaces []string, providerKind, instanceName, innerIP string) (*AddResponse, error) {
	var iface interface{}
	if len(interfaces) == 1 {
		iface = interfaces[0]
	} else {
		iface = interfaces
	}
	req := AddRequest{
		Interface:    iface,
		ProviderKind: providerKind,
		InstanceName: instanceName,
		InnerIP:      innerIP,
	}
	var resp AddResponse
	if err := c.doRequest("POST", "/api/v1/add", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// UpdateMonitor updates the interfaces for an existing monitor.
func (c *Client) UpdateMonitor(id int64, interfaces []string, providerKind, instanceName, innerIP string) (*UpdateResponse, error) {
	var iface interface{}
	if len(interfaces) == 1 {
		iface = interfaces[0]
	} else {
		iface = interfaces
	}
	req := UpdateRequest{
		ID:           id,
		NewInterface: iface,
		ProviderKind: providerKind,
		InstanceName: instanceName,
		InnerIP:      innerIP,
	}
	var resp UpdateResponse
	if err := c.doRequest("POST", "/api/v1/update", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DeleteMonitor deletes a monitor from the agent.
func (c *Client) DeleteMonitor(id int64) (*DeleteResponse, error) {
	req := DeleteRequest{ID: id}
	var resp DeleteResponse
	if err := c.doRequest("POST", "/api/v1/delete", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetInfo returns the current traffic info for a monitor.
func (c *Client) GetInfo(id int64) (*InfoResponse, error) {
	req := InfoRequest{ID: id}
	var resp InfoResponse
	if err := c.doRequest("POST", "/api/v1/info?human=1", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetResources returns resource monitoring history for a monitor.
func (c *Client) GetResources(id int64, limit int64) (*ResourceQueryResponse, error) {
	req := ResourceQueryRequest{ID: id, Limit: limit}
	var resp ResourceQueryResponse
	if err := c.doRequest("POST", "/api/v1/resources", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Cleanup removes stale monitors from the agent.
func (c *Client) Cleanup(maxUpdateTime string) (*CleanupResponse, error) {
	req := CleanupRequest{MaxUpdateTime: maxUpdateTime}
	var resp CleanupResponse
	if err := c.doRequest("POST", "/api/v1/cleanup", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Ping checks if the agent is reachable.
func (c *Client) Ping() error {
	req, err := http.NewRequest("GET", c.baseURL+"/swagger-ui/", nil)
	if err != nil {
		return fmt.Errorf("failed to create ping request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("agent returned status %d", resp.StatusCode)
	}
	return nil
}

// ListMonitors returns all monitors on the agent.
func (c *Client) ListMonitors() (*ListMonitorsResponse, error) {
	var resp ListMonitorsResponse
	if err := c.doRequest("GET", "/api/v1/list", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// BatchGetInfo fetches traffic info for multiple monitors in one agent request.
func (c *Client) BatchGetInfo(ids []int64) (map[int64]*InfoResponse, error) {
	results := make(map[int64]*InfoResponse)
	if len(ids) == 0 {
		return results, nil
	}

	seen := make(map[int64]struct{}, len(ids))
	uniqueIDs := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
	}
	if len(uniqueIDs) == 0 {
		return results, nil
	}

	req := BatchInfoRequest{IDs: uniqueIDs}
	var resp BatchInfoResponse
	if err := c.doRequest("POST", "/api/v1/batch-info", req, &resp); err != nil {
		return nil, err
	}
	for i := range resp.Monitors {
		info := resp.Monitors[i]
		results[info.ID] = &info
	}
	return results, nil
}

// ---- Block Rules API ----

type ApplyBlockRulesRequest struct {
	Strings   []string `json:"strings"`
	IPVersion string   `json:"ip_version,omitempty"`
}

type ApplyBlockRulesResponse struct {
	Applied int `json:"applied"`
}

type RemoveBlockRulesResponse struct {
	Removed bool `json:"removed"`
}

type GetBlockRulesResponse struct {
	Strings   []string `json:"strings"`
	Count     int      `json:"count"`
	IPVersion string   `json:"ip_version"`
}

// ApplyBlockRules sends string-match block rules to the agent.
func (c *Client) ApplyBlockRules(strings []string, ipVersion string) error {
	if ipVersion == "" {
		ipVersion = "both"
	}
	req := ApplyBlockRulesRequest{Strings: strings, IPVersion: ipVersion}
	var resp ApplyBlockRulesResponse
	return c.doRequest("POST", "/api/v1/block-rules", req, &resp)
}

// RemoveBlockRules removes all block rules from the agent.
func (c *Client) RemoveBlockRules() error {
	var resp RemoveBlockRulesResponse
	return c.doRequest("DELETE", "/api/v1/block-rules", nil, &resp)
}

// GetBlockRules returns current block rules from the agent.
func (c *Client) GetBlockRules() (*GetBlockRulesResponse, error) {
	var resp GetBlockRulesResponse
	if err := c.doRequest("GET", "/api/v1/block-rules", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ---- Domain Proxy API ----

type AddDomainProxyRequest struct {
	Domain       string `json:"domain"`
	InternalIP   string `json:"internal_ip"`
	InternalPort int    `json:"internal_port"`
	Protocol     string `json:"protocol,omitempty"`
	EnableSSL    bool   `json:"enable_ssl,omitempty"`
	SSLCert      string `json:"ssl_cert,omitempty"`
	SSLKey       string `json:"ssl_key,omitempty"`
}

type AddDomainProxyResponse struct {
	Domain string `json:"domain"`
	Status string `json:"status"`
}

type RemoveDomainProxyRequest struct {
	Domain string `json:"domain"`
}

type RemoveDomainProxyResponse struct {
	Domain  string `json:"domain"`
	Removed bool   `json:"removed"`
}

type DomainProxyItem struct {
	Domain       string `json:"domain"`
	InternalIP   string `json:"internal_ip"`
	InternalPort int    `json:"internal_port"`
	Protocol     string `json:"protocol"`
	EnableSSL    bool   `json:"enable_ssl"`
	HasCert      bool   `json:"has_cert"`
	CreatedAt    int64  `json:"created_at"`
}

type ListDomainProxiesResponse struct {
	Proxies []DomainProxyItem `json:"proxies"`
	Total   int               `json:"total"`
}

// AddDomainProxy adds a domain reverse proxy on the agent host.
func (c *Client) AddDomainProxy(domain, internalIP string, internalPort int, protocol string, enableSSL bool, sslCert, sslKey string) (*AddDomainProxyResponse, error) {
	req := AddDomainProxyRequest{
		Domain:       domain,
		InternalIP:   internalIP,
		InternalPort: internalPort,
		Protocol:     protocol,
		EnableSSL:    enableSSL,
		SSLCert:      sslCert,
		SSLKey:       sslKey,
	}
	var resp AddDomainProxyResponse
	if err := c.doRequest("POST", "/api/v1/domain-proxy", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// RemoveDomainProxy removes a domain reverse proxy from the agent host.
func (c *Client) RemoveDomainProxy(domain string) error {
	req := RemoveDomainProxyRequest{Domain: domain}
	var resp RemoveDomainProxyResponse
	return c.doRequest("DELETE", "/api/v1/domain-proxy", req, &resp)
}

// ListDomainProxies returns all domain proxies from the agent.
func (c *Client) ListDomainProxies() (*ListDomainProxiesResponse, error) {
	var resp ListDomainProxiesResponse
	if err := c.doRequest("GET", "/api/v1/domain-proxy", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ---- Transparent Egress API ----

// EgressCapabilities describes the host-side prerequisites reported by the
// Rust Agent.  A controller must treat Supported=false or ApplyEnabled=false
// as a hard stop and must not fall back to the node's default route.
type EgressCapabilities struct {
	Supported           bool     `json:"supported"`
	Mode                string   `json:"mode"`
	RunningAsRoot       bool     `json:"running_as_root"`
	IPAvailable         bool     `json:"ip_available"`
	NFTAvailable        bool     `json:"nft_available"`
	WireguardAvailable  bool     `json:"wireguard_available"`
	CurlAvailable       bool     `json:"curl_available"`
	IPv4Forwarding      bool     `json:"ipv4_forwarding"`
	IPv6Forwarding      bool     `json:"ipv6_forwarding"`
	ApplyEnabled        bool     `json:"apply_enabled"`
	AutoInstallEnabled  bool     `json:"auto_install_enabled"`
	PackageManager      *string  `json:"package_manager,omitempty"`
	MissingDependencies []string `json:"missing_dependencies,omitempty"`
	CheckedAt           int64    `json:"checked_at"`
	Reasons             []string `json:"reasons"`
}

type EgressProfile struct {
	ID              string                 `json:"id"`
	Mode            string                 `json:"mode"`
	TunnelType      string                 `json:"tunnel_type"`
	TunnelInterface string                 `json:"tunnel_interface"`
	Gateway         *string                `json:"gateway,omitempty"`
	RouteTable      uint32                 `json:"route_table"`
	Mark            uint32                 `json:"mark"`
	PublicIPv4      *string                `json:"public_ipv4,omitempty"`
	PublicIPv6      *string                `json:"public_ipv6,omitempty"`
	Enabled         bool                   `json:"enabled"`
	FailClosed      bool                   `json:"fail_closed"`
	Status          string                 `json:"status"`
	LastError       *string                `json:"last_error,omitempty"`
	WireGuard       *EgressWireGuardStatus `json:"wireguard,omitempty"`
	TunnelReady     bool                   `json:"tunnel_ready"`
	LastHandshakeAt *int64                 `json:"last_handshake_at,omitempty"`
	UpdatedAt       int64                  `json:"updated_at"`
}

// EgressWireGuardStatus is safe to return to the controller: it only reports
// whether key material is configured and never contains private key bytes.
type EgressWireGuardStatus struct {
	Managed                bool     `json:"managed"`
	PeerPublicKey          *string  `json:"peer_public_key,omitempty"`
	Endpoint               *string  `json:"endpoint,omitempty"`
	Addresses              []string `json:"addresses,omitempty"`
	AllowedIPs             []string `json:"allowed_ips,omitempty"`
	PersistentKeepalive    uint16   `json:"persistent_keepalive"`
	MTU                    uint16   `json:"mtu"`
	PrivateKeyConfigured   bool     `json:"private_key_configured"`
	PresharedKeyConfigured bool     `json:"preshared_key_configured"`
}

type EgressProfileRequest struct {
	ID              string                  `json:"id"`
	Mode            string                  `json:"mode"`
	TunnelType      string                  `json:"tunnel_type,omitempty"`
	TunnelInterface string                  `json:"tunnel_interface"`
	Gateway         *string                 `json:"gateway,omitempty"`
	RouteTable      uint32                  `json:"route_table,omitempty"`
	Mark            uint32                  `json:"mark,omitempty"`
	PublicIPv4      *string                 `json:"public_ipv4,omitempty"`
	PublicIPv6      *string                 `json:"public_ipv6,omitempty"`
	Enabled         *bool                   `json:"enabled,omitempty"`
	FailClosed      *bool                   `json:"fail_closed,omitempty"`
	WireGuard       *EgressWireGuardRequest `json:"wireguard,omitempty"`
}

// EgressWireGuardRequest contains write-only WireGuard material. Agent
// responses intentionally do not expose this structure, so private and
// preshared keys cannot be reflected back through controller APIs.
type EgressWireGuardRequest struct {
	Managed             *bool    `json:"managed,omitempty"`
	PrivateKey          string   `json:"private_key,omitempty"`
	PeerPublicKey       string   `json:"peer_public_key,omitempty"`
	PresharedKey        string   `json:"preshared_key,omitempty"`
	Endpoint            string   `json:"endpoint,omitempty"`
	Addresses           []string `json:"addresses,omitempty"`
	AllowedIPs          []string `json:"allowed_ips,omitempty"`
	PersistentKeepalive *uint16  `json:"persistent_keepalive,omitempty"`
	MTU                 *uint16  `json:"mtu,omitempty"`
}

type EgressProfileDeleteRequest struct {
	ID string `json:"id"`
}

type EgressProfilesResponse struct {
	Profiles []EgressProfile `json:"profiles"`
	Total    int             `json:"total"`
}

type EgressBinding struct {
	InstanceID          string   `json:"instance_id"`
	ProfileID           string   `json:"profile_id"`
	Source              string   `json:"source"`
	Sources             []string `json:"sources,omitempty"`
	Interface           *string  `json:"interface,omitempty"`
	InterfaceV4         *string  `json:"interface_v4,omitempty"`
	InterfaceV6         *string  `json:"interface_v6,omitempty"`
	Enabled             bool     `json:"enabled"`
	State               string   `json:"state"`
	LastError           *string  `json:"last_error,omitempty"`
	FailClosedEnforced  *bool    `json:"fail_closed_enforced,omitempty"`
	TrafficBytesIn      uint64   `json:"traffic_bytes_in,omitempty"`
	TrafficBytesOut     uint64   `json:"traffic_bytes_out,omitempty"`
	TrafficBytesDropped uint64   `json:"traffic_bytes_dropped,omitempty"`
	UpdatedAt           int64    `json:"updated_at"`
}

type EgressBindingRequest struct {
	InstanceID  string   `json:"instance_id"`
	ProfileID   string   `json:"profile_id"`
	Source      string   `json:"source"`
	Sources     []string `json:"sources,omitempty"`
	Interface   *string  `json:"interface,omitempty"`
	InterfaceV4 *string  `json:"interface_v4,omitempty"`
	InterfaceV6 *string  `json:"interface_v6,omitempty"`
	Enabled     *bool    `json:"enabled,omitempty"`
}

type EgressBindingDeleteRequest struct {
	InstanceID string `json:"instance_id"`
}

type EgressBindingsResponse struct {
	Bindings []EgressBinding `json:"bindings"`
	Total    int             `json:"total"`
}

type EgressReconcileRequest struct {
	Apply bool `json:"apply,omitempty"`
}

type EgressStateRequest struct {
	Profiles []EgressProfileRequest `json:"profiles"`
	Bindings []EgressBindingRequest `json:"bindings"`
	Apply    bool                   `json:"apply,omitempty"`
}

type EgressStateResponse struct {
	ProfileCount int                     `json:"profile_count"`
	BindingCount int                     `json:"binding_count"`
	Reconcile    EgressReconcileResponse `json:"reconcile"`
}

type EgressRoutePlan struct {
	InstanceID string   `json:"instance_id"`
	ProfileID  string   `json:"profile_id"`
	Status     string   `json:"status"`
	Commands   []string `json:"commands"`
	Error      *string  `json:"error,omitempty"`
}

type EgressReconcileResponse struct {
	Applied      bool               `json:"applied"`
	FailClosed   bool               `json:"fail_closed"`
	Capabilities EgressCapabilities `json:"capabilities"`
	Plans        []EgressRoutePlan  `json:"plans"`
	Errors       []string           `json:"errors,omitempty"`
}

type EgressDependencyEnsureRequest struct {
	PackageSet string `json:"package_set,omitempty"`
}

type EgressDependencyEnsureResponse struct {
	Attempted      bool               `json:"attempted"`
	Installed      bool               `json:"installed"`
	PackageSet     string             `json:"package_set"`
	PackageManager *string            `json:"package_manager,omitempty"`
	Capabilities   EgressCapabilities `json:"capabilities"`
	Message        string             `json:"message,omitempty"`
}

// GetEgressCapabilities returns host prerequisites without changing state.
func (c *Client) GetEgressCapabilities() (*EgressCapabilities, error) {
	var resp EgressCapabilities
	if err := c.doRequest("GET", "/api/v1/egress/capabilities", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListEgressProfiles returns the agent's persisted desired egress profiles.
func (c *Client) ListEgressProfiles() (*EgressProfilesResponse, error) {
	var resp EgressProfilesResponse
	if err := c.doRequest("GET", "/api/v1/egress/profiles", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// PutEgressProfile creates or updates one profile and resets it to pending.
func (c *Client) PutEgressProfile(req EgressProfileRequest) (*EgressProfile, error) {
	var resp EgressProfile
	if err := c.doRequest("PUT", "/api/v1/egress/profiles", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) DeleteEgressProfile(id string) error {
	var resp map[string]interface{}
	return c.doRequest("DELETE", "/api/v1/egress/profiles", EgressProfileDeleteRequest{ID: id}, &resp)
}

func (c *Client) ListEgressBindings() (*EgressBindingsResponse, error) {
	var resp EgressBindingsResponse
	if err := c.doRequest("GET", "/api/v1/egress/bindings", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// PutEgressBinding creates or updates the source identity for one instance.
func (c *Client) PutEgressBinding(req EgressBindingRequest) (*EgressBinding, error) {
	var resp EgressBinding
	if err := c.doRequest("PUT", "/api/v1/egress/bindings", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *Client) DeleteEgressBinding(instanceID string) error {
	var resp map[string]interface{}
	return c.doRequest("DELETE", "/api/v1/egress/bindings", EgressBindingDeleteRequest{InstanceID: instanceID}, &resp)
}

// ReplaceEgressState atomically replaces all controller-owned egress desired
// state on one Agent and reconciles the host data plane exactly once.
func (c *Client) ReplaceEgressState(req EgressStateRequest) (*EgressStateResponse, error) {
	var resp EgressStateResponse
	if err := c.doRequest("PUT", "/api/v1/egress/state", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ReconcileEgress requests a dry-run plan by default.  The Rust Agent only
// marks routes active after a future audited data-plane adapter applies them;
// callers must inspect Applied/FailClosed and every plan status.
func (c *Client) ReconcileEgress(apply bool) (*EgressReconcileResponse, error) {
	var resp EgressReconcileResponse
	if err := c.doRequest("POST", "/api/v1/egress/reconcile", EgressReconcileRequest{Apply: apply}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// EnsureEgressDependencies asks the Agent to install/check only its declared
// native or WireGuard prerequisites. The Agent is responsible for package
// manager selection and idempotence; no shell command is accepted here.
func (c *Client) EnsureEgressDependencies(packageSet string) (*EgressDependencyEnsureResponse, error) {
	var resp EgressDependencyEnsureResponse
	if err := c.doRequest("POST", "/api/v1/egress/dependencies/ensure", EgressDependencyEnsureRequest{PackageSet: packageSet}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
