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
	// Try HTTP first
	err := c.doHTTPRequest(method, path, body, result)
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

	resp, err := c.httpClient.Do(req)
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
