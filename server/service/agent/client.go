package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

// Client communicates with the oneclickvirt-agent HTTP API on a provider host.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

var (
	clientPool = sync.Map{} // providerID -> *Client
)

// GetClient returns a cached or new agent client for the given provider.
func GetClient(providerID uint, host string, port int, token string) *Client {
	key := fmt.Sprintf("%d", providerID)
	if v, ok := clientPool.Load(key); ok {
		c := v.(*Client)
		expected := fmt.Sprintf("http://%s:%d", host, port)
		if c.baseURL == expected && c.token == token {
			return c
		}
	}
	c := &Client{
		baseURL: fmt.Sprintf("http://%s:%d", host, port),
		token:   token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	clientPool.Store(key, c)
	return c
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
	InnerIP      string      `json:"inner_ip,omitempty"`
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
	ID           int64    `json:"id"`
	Interface    []string `json:"interface"`
	ProviderKind *string  `json:"provider_kind"`
	InstanceName *string  `json:"instance_name"`
	TotalBytes   uint64   `json:"total_bytes"`
	UpdatedAt    int64    `json:"updated_at"`
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

// BatchGetInfo fetches traffic info for multiple monitors.
func (c *Client) BatchGetInfo(ids []int64) (map[int64]*InfoResponse, error) {
	results := make(map[int64]*InfoResponse)
	for _, id := range ids {
		info, err := c.GetInfo(id)
		if err != nil {
			if global.APP_LOG != nil {
				global.APP_LOG.Warn("agent batch get info: single monitor failed",
					zap.Int64("agent_monitor_id", id),
					zap.Error(err))
			}
			continue
		}
		results[id] = info
	}
	return results, nil
}
