// Package mcp provides Model Context Protocol (MCP) server implementation
// for OneClickVirt, enabling AI assistants to manage virtualization resources.
//
// MCP Protocol: https://modelcontextprotocol.io/
//
// Supports stdio transport for local AI tools like Claude Desktop and Copilot.
package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"oneclickvirt/constant"
	"oneclickvirt/global"

	"go.uber.org/zap"
)

// MCP Protocol version
const ProtocolVersion = "2024-11-05"

// JSON-RPC 2.0 message types
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      interface{}   `json:"id"`
	Result  interface{}   `json:"result,omitempty"`
	Error   *JSONRPCError `json:"error,omitempty"`
}

type JSONRPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// MCP Server Capabilities
type ServerCapabilities struct {
	Tools     *ToolsCapability     `json:"tools,omitempty"`
	Resources *ResourcesCapability `json:"resources,omitempty"`
	Prompts   *PromptsCapability   `json:"prompts,omitempty"`
}

type ToolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

type ResourcesCapability struct {
	Subscribe   bool `json:"subscribe"`
	ListChanged bool `json:"listChanged"`
}

type PromptsCapability struct {
	ListChanged bool `json:"listChanged"`
}

// Initialize result
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      ServerInfo         `json:"serverInfo"`
}

type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Tool definition
type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema InputSchema `json:"inputSchema"`
}

type InputSchema struct {
	Type       string                 `json:"type"`
	Properties map[string]interface{} `json:"properties"`
	Required   []string               `json:"required,omitempty"`
}

// Resource definition
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// Prompt definition
type Prompt struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// MCPServer is the MCP protocol handler
type MCPServer struct {
	mu         sync.RWMutex
	apiURL     string
	apiToken   string
	httpClient *http.Client
	tools      []Tool
	resources  []Resource
	prompts    []Prompt
}

// NewMCPServer creates a new MCP server instance
func NewMCPServer(apiURL, apiToken string) *MCPServer {
	s := &MCPServer{
		apiURL:     apiURL,
		apiToken:   apiToken,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
	s.registerTools()
	s.registerResources()
	s.registerPrompts()
	return s
}

// registerTools defines all available MCP tools
func (s *MCPServer) registerTools() {
	s.tools = []Tool{
		{
			Name:        "list_instances",
			Description: "List all virtual machine and container instances in OneClickVirt",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"page":     map[string]interface{}{"type": "integer", "description": "Page number (default: 1)"},
					"pageSize": map[string]interface{}{"type": "integer", "description": "Items per page (default: 20)"},
					"status":   map[string]interface{}{"type": "string", "description": "Filter by status: running, stopped, deleted"},
				},
			},
		},
		{
			Name:        "create_instance",
			Description: "Create a new virtual machine or container instance",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"provider_id":   map[string]interface{}{"type": "integer", "description": "Provider node ID"},
					"instance_type": map[string]interface{}{"type": "string", "description": "container or vm"},
					"image":         map[string]interface{}{"type": "string", "description": "OS image (e.g., debian:12, ubuntu:22.04, alpine:latest)"},
					"cpu":           map[string]interface{}{"type": "integer", "description": "CPU cores"},
					"memory":        map[string]interface{}{"type": "integer", "description": "Memory in MB"},
					"disk":          map[string]interface{}{"type": "integer", "description": "Disk size in GB"},
					"name":          map[string]interface{}{"type": "string", "description": "Instance name (optional)"},
				},
				Required: []string{"provider_id", "instance_type", "image", "cpu", "memory", "disk"},
			},
		},
		{
			Name:        "start_instance",
			Description: "Start a stopped instance",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"instance_id": map[string]interface{}{"type": "integer", "description": "Instance ID"},
				},
				Required: []string{"instance_id"},
			},
		},
		{
			Name:        "stop_instance",
			Description: "Stop a running instance",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"instance_id": map[string]interface{}{"type": "integer", "description": "Instance ID"},
				},
				Required: []string{"instance_id"},
			},
		},
		{
			Name:        "restart_instance",
			Description: "Restart an instance",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"instance_id": map[string]interface{}{"type": "integer", "description": "Instance ID"},
				},
				Required: []string{"instance_id"},
			},
		},
		{
			Name:        "delete_instance",
			Description: "Delete an instance (DANGER: irreversible)",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"instance_id": map[string]interface{}{"type": "integer", "description": "Instance ID"},
					"confirm":     map[string]interface{}{"type": "boolean", "description": "Must be true to confirm deletion"},
				},
				Required: []string{"instance_id", "confirm"},
			},
		},
		{
			Name:        "get_instance_detail",
			Description: "Get detailed information about a specific instance",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"instance_id": map[string]interface{}{"type": "integer", "description": "Instance ID"},
				},
				Required: []string{"instance_id"},
			},
		},
		{
			Name:        "list_providers",
			Description: "List all virtualization provider nodes",
			InputSchema: InputSchema{
				Type:       "object",
				Properties: map[string]interface{}{},
			},
		},
		{
			Name:        "health_check",
			Description: "Run a health check on a provider node",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"provider_id": map[string]interface{}{"type": "integer", "description": "Provider ID"},
				},
				Required: []string{"provider_id"},
			},
		},
		{
			Name:        "get_instance_logs",
			Description: "Get recent logs from an instance",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"instance_id": map[string]interface{}{"type": "integer", "description": "Instance ID"},
					"lines":       map[string]interface{}{"type": "integer", "description": "Number of log lines (default: 50)"},
				},
				Required: []string{"instance_id"},
			},
		},
		{
			Name:        "get_system_status",
			Description: "Get overall system status including instance counts and resource usage",
			InputSchema: InputSchema{
				Type:       "object",
				Properties: map[string]interface{}{},
			},
		},
		{
			Name:        "get_metrics",
			Description: "Get monitoring metrics for the system, a provider, or an instance",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"metric_type": map[string]interface{}{"type": "string", "description": "Metric scope: system, provider, or instance"},
					"provider_id": map[string]interface{}{"type": "integer", "description": "Provider ID for provider metrics"},
					"instance_id": map[string]interface{}{"type": "integer", "description": "Instance ID for instance metrics"},
					"hours":       map[string]interface{}{"type": "integer", "description": "Time range in hours for instance metrics (default: 24)"},
				},
			},
		},
	}
}

// registerResources defines all available MCP resources
func (s *MCPServer) registerResources() {
	s.resources = []Resource{
		{
			URI:         "oneclickvirt://instances/list",
			Name:        "Instance List",
			Description: "Current list of all instances",
			MimeType:    "application/json",
		},
		{
			URI:         "oneclickvirt://providers/list",
			Name:        "Provider List",
			Description: "Current list of all provider nodes",
			MimeType:    "application/json",
		},
		{
			URI:         "oneclickvirt://system/status",
			Name:        "System Status",
			Description: "Overall system health and metrics",
			MimeType:    "application/json",
		},
		{
			URI:         "oneclickvirt://health/status",
			Name:        "Health Status",
			Description: "Public OneClickVirt API health status",
			MimeType:    "application/json",
		},
		{
			URI:         "oneclickvirt://config/system",
			Name:        "System Configuration",
			Description: "Administrator-visible system configuration",
			MimeType:    "application/json",
		},
	}
}

// registerPrompts defines all available MCP prompts
func (s *MCPServer) registerPrompts() {
	s.prompts = []Prompt{
		{
			Name:        "create_debian_container",
			Description: "Template for creating a Debian Linux container",
			Arguments: []PromptArgument{
				{Name: "provider_id", Description: "Provider node ID", Required: true},
				{Name: "name", Description: "Instance name", Required: false},
			},
		},
		{
			Name:        "create_ubuntu_vm",
			Description: "Template for creating an Ubuntu virtual machine",
			Arguments: []PromptArgument{
				{Name: "provider_id", Description: "Provider node ID", Required: true},
				{Name: "name", Description: "Instance name", Required: false},
			},
		},
		{
			Name:        "troubleshoot_instance",
			Description: "Template for troubleshooting an instance that won't start",
			Arguments: []PromptArgument{
				{Name: "instance_id", Description: "Instance ID to troubleshoot", Required: true},
			},
		},
		{
			Name:        "quick_status_check",
			Description: "Template for checking instance, provider, and platform health",
		},
	}
}

// HandleRequest processes an incoming MCP JSON-RPC request
func (s *MCPServer) HandleRequest(req JSONRPCRequest) JSONRPCResponse {
	if global.APP_LOG != nil {
		global.APP_LOG.Debug("MCP request",
			zap.String("method", req.Method),
			zap.Any("id", req.ID),
		)
	}

	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "initialized":
		return JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: struct{}{}}
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(req)
	case "resources/list":
		return s.handleResourcesList(req)
	case "resources/read":
		return s.handleResourcesRead(req)
	case "prompts/list":
		return s.handlePromptsList(req)
	case "prompts/get":
		return s.handlePromptsGet(req)
	default:
		return JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &JSONRPCError{
				Code:    -32601,
				Message: fmt.Sprintf("Method not found: %s", req.Method),
			},
		}
	}
}

func (s *MCPServer) handleInitialize(req JSONRPCRequest) JSONRPCResponse {
	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: InitializeResult{
			ProtocolVersion: ProtocolVersion,
			Capabilities: ServerCapabilities{
				Tools:     &ToolsCapability{ListChanged: false},
				Resources: &ResourcesCapability{Subscribe: false, ListChanged: false},
				Prompts:   &PromptsCapability{ListChanged: false},
			},
			ServerInfo: ServerInfo{
				Name:    "OneClickVirt MCP",
				Version: constant.ServerVersion,
			},
		},
	}
}

func (s *MCPServer) handleToolsList(req JSONRPCRequest) JSONRPCResponse {
	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  map[string]interface{}{"tools": s.tools},
	}
}

func (s *MCPServer) handleToolsCall(req JSONRPCRequest) JSONRPCResponse {
	var params struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.errorResponse(req.ID, -32602, "Invalid params: "+err.Error())
	}

	result, err := s.executeTool(params.Name, params.Arguments)
	if err != nil {
		return s.errorResponse(req.ID, -32000, err.Error())
	}

	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": result,
				},
			},
		},
	}
}

func (s *MCPServer) handleResourcesList(req JSONRPCRequest) JSONRPCResponse {
	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  map[string]interface{}{"resources": s.resources},
	}
}

func (s *MCPServer) handleResourcesRead(req JSONRPCRequest) JSONRPCResponse {
	var params struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.errorResponse(req.ID, -32602, "Invalid params")
	}

	content, err := s.readResource(params.URI)
	if err != nil {
		return s.errorResponse(req.ID, -32000, err.Error())
	}

	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"contents": []map[string]interface{}{
				{
					"uri":      params.URI,
					"mimeType": "application/json",
					"text":     content,
				},
			},
		},
	}
}

func (s *MCPServer) handlePromptsList(req JSONRPCRequest) JSONRPCResponse {
	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  map[string]interface{}{"prompts": s.prompts},
	}
}

func (s *MCPServer) handlePromptsGet(req JSONRPCRequest) JSONRPCResponse {
	var params struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return s.errorResponse(req.ID, -32602, "Invalid params: "+err.Error())
	}
	text, err := s.renderPrompt(params.Name, params.Arguments)
	if err != nil {
		return s.errorResponse(req.ID, -32000, err.Error())
	}
	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"description": promptDescription(s.prompts, params.Name),
			"messages": []map[string]interface{}{
				{
					"role": "user",
					"content": map[string]interface{}{
						"type": "text",
						"text": text,
					},
				},
			},
		},
	}
}

func (s *MCPServer) errorResponse(id interface{}, code int, message string) JSONRPCResponse {
	return JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: message,
		},
	}
}

// ---- Tool execution ----

func (s *MCPServer) executeTool(name string, args map[string]interface{}) (string, error) {
	switch name {
	case "list_instances":
		return s.listInstances(args)
	case "create_instance":
		return s.createInstance(args)
	case "start_instance":
		return s.instanceAction(args, "start")
	case "stop_instance":
		return s.instanceAction(args, "stop")
	case "restart_instance":
		return s.instanceAction(args, "restart")
	case "delete_instance":
		return s.deleteInstance(args)
	case "get_instance_detail":
		return s.getInstanceDetail(args)
	case "list_providers":
		return s.listProviders(args)
	case "health_check":
		return s.healthCheck(args)
	case "get_instance_logs":
		return s.getInstanceLogs(args)
	case "get_system_status":
		return s.getSystemStatus(args)
	case "get_metrics":
		return s.getMetrics(args)
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

func (s *MCPServer) makeAPIRequest(method, path string, body interface{}) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequest(method, strings.TrimRight(s.apiURL, "/")+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if s.apiToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.apiToken)
	}

	client := s.httpClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("OneClickVirt API returned HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

func (s *MCPServer) listInstances(args map[string]interface{}) (string, error) {
	page := intArg(args, "page", 1)
	pageSize := intArg(args, "pageSize", 20)
	path := fmt.Sprintf("/api/v1/admin/instances?page=%d&pageSize=%d", page, pageSize)
	if status := stringArg(args, "status", ""); status != "" {
		path += "&status=" + url.QueryEscape(status)
	}
	resp, err := s.makeAPIRequest(http.MethodGet, path, nil)
	return string(resp), err
}

func (s *MCPServer) createInstance(args map[string]interface{}) (string, error) {
	payload := map[string]interface{}{
		"provider_id":   intArg(args, "provider_id", 0),
		"instance_type": stringArg(args, "instance_type", "container"),
		"image":         stringArg(args, "image", ""),
		"cpu":           intArg(args, "cpu", 1),
		"memory":        intArg(args, "memory", 512),
		"disk":          intArg(args, "disk", 5),
	}
	if name := stringArg(args, "name", ""); name != "" {
		payload["name"] = name
	}
	if payload["provider_id"] == 0 || payload["image"] == "" {
		return "", fmt.Errorf("provider_id and image are required")
	}
	resp, err := s.makeAPIRequest(http.MethodPost, "/api/v1/admin/instances", payload)
	return string(resp), err
}

func (s *MCPServer) instanceAction(args map[string]interface{}, action string) (string, error) {
	id := intArg(args, "instance_id", 0)
	if id == 0 {
		return "", fmt.Errorf("instance_id is required")
	}
	resp, err := s.makeAPIRequest(http.MethodPost, fmt.Sprintf("/api/v1/admin/instances/%d/action", id), map[string]string{"action": action})
	return string(resp), err
}

func (s *MCPServer) deleteInstance(args map[string]interface{}) (string, error) {
	confirm, _ := args["confirm"].(bool)
	if !confirm {
		return "", fmt.Errorf("confirmation required: set confirm=true to delete instance")
	}
	id := intArg(args, "instance_id", 0)
	if id == 0 {
		return "", fmt.Errorf("instance_id is required")
	}
	resp, err := s.makeAPIRequest(http.MethodDelete, fmt.Sprintf("/api/v1/admin/instances/%d", id), nil)
	return string(resp), err
}

func (s *MCPServer) getInstanceDetail(args map[string]interface{}) (string, error) {
	id := intArg(args, "instance_id", 0)
	if id == 0 {
		return "", fmt.Errorf("instance_id is required")
	}
	resp, err := s.makeAPIRequest(http.MethodGet, fmt.Sprintf("/api/v1/admin/instances/%d", id), nil)
	return string(resp), err
}

func (s *MCPServer) listProviders(args map[string]interface{}) (string, error) {
	page := intArg(args, "page", 1)
	pageSize := intArg(args, "pageSize", 100)
	resp, err := s.makeAPIRequest(http.MethodGet, fmt.Sprintf("/api/v1/admin/providers?page=%d&pageSize=%d", page, pageSize), nil)
	return string(resp), err
}

func (s *MCPServer) healthCheck(args map[string]interface{}) (string, error) {
	id := intArg(args, "provider_id", 0)
	if id == 0 {
		return "", fmt.Errorf("provider_id is required")
	}
	resp, err := s.makeAPIRequest(http.MethodPost, fmt.Sprintf("/api/v1/admin/providers/%d/health-check", id), nil)
	return string(resp), err
}

func (s *MCPServer) getInstanceLogs(args map[string]interface{}) (string, error) {
	id := intArg(args, "instance_id", 0)
	if id == 0 {
		return "", fmt.Errorf("instance_id is required")
	}
	lines := intArg(args, "lines", 50)
	return fmt.Sprintf(`{"message":"Instance log retrieval is not exposed by the current HTTP API","instance_id":%d,"requested_lines":%d}`, id, lines), nil
}

func (s *MCPServer) getSystemStatus(args map[string]interface{}) (string, error) {
	resp, err := s.makeAPIRequest(http.MethodGet, "/api/v1/admin/dashboard", nil)
	return string(resp), err
}

func (s *MCPServer) getMetrics(args map[string]interface{}) (string, error) {
	if id := intArg(args, "instance_id", 0); id > 0 {
		hours := intArg(args, "hours", 24)
		resp, err := s.makeAPIRequest(http.MethodGet, fmt.Sprintf("/api/v1/admin/instances/%d/monitoring/resources?hours=%d", id, hours), nil)
		return string(resp), err
	}
	if id := intArg(args, "provider_id", 0); id > 0 {
		resp, err := s.makeAPIRequest(http.MethodGet, fmt.Sprintf("/api/v1/admin/providers/%d/monitoring/resources", id), nil)
		return string(resp), err
	}
	metricType := stringArg(args, "metric_type", "system")
	if metricType != "" && metricType != "system" && metricType != "dashboard" && metricType != "overview" {
		return "", fmt.Errorf("provider_id or instance_id is required for metric_type=%s", metricType)
	}
	return s.getSystemStatus(args)
}

func (s *MCPServer) readResource(uri string) (string, error) {
	switch uri {
	case "oneclickvirt://instances/list":
		return s.listInstances(nil)
	case "oneclickvirt://providers/list":
		return s.listProviders(nil)
	case "oneclickvirt://system/status":
		return s.getSystemStatus(nil)
	case "oneclickvirt://health/status":
		resp, err := s.makeAPIRequest(http.MethodGet, "/api/v1/health", nil)
		return string(resp), err
	case "oneclickvirt://config/system":
		resp, err := s.makeAPIRequest(http.MethodGet, "/api/v1/config?scope=admin", nil)
		return string(resp), err
	default:
		return "", fmt.Errorf("unknown resource: %s", uri)
	}
}

func (s *MCPServer) renderPrompt(name string, args map[string]interface{}) (string, error) {
	providerID := intArg(args, "provider_id", 0)
	instanceID := intArg(args, "instance_id", 0)
	instanceName := stringArg(args, "name", "")

	switch name {
	case "create_debian_container":
		return fmt.Sprintf("Create a Debian 12 container in OneClickVirt using the create_instance tool. Use provider_id=%d, instance_type=container, image=debian:12, cpu=1, memory=1024, disk=20, and name=%q if a name is desired. If provider_id is 0, list providers first and ask which provider to use.", providerID, instanceName), nil
	case "create_ubuntu_vm":
		return fmt.Sprintf("Create an Ubuntu VM in OneClickVirt using the create_instance tool. Use provider_id=%d, instance_type=vm, image=ubuntu:22.04, cpu=2, memory=2048, disk=40, and name=%q if a name is desired. If provider_id is 0, list providers first and ask which provider to use.", providerID, instanceName), nil
	case "troubleshoot_instance":
		return fmt.Sprintf("Troubleshoot OneClickVirt instance %d. First call get_instance_detail, then get_metrics with instance_id=%d, then list_providers and health_check for the owning provider if available. Summarize the likely cause and propose a safe next action before calling start/stop/restart.", instanceID, instanceID), nil
	case "quick_status_check":
		return "Check OneClickVirt status by calling get_system_status, list_instances with pageSize=20, list_providers, and get_metrics with metric_type=system. Summarize unhealthy providers, stopped or frozen instances, and capacity concerns.", nil
	default:
		return "", fmt.Errorf("unknown prompt: %s", name)
	}
}

func promptDescription(prompts []Prompt, name string) string {
	for _, prompt := range prompts {
		if prompt.Name == name {
			return prompt.Description
		}
	}
	return ""
}

func intArg(args map[string]interface{}, key string, fallback int) int {
	if args == nil {
		return fallback
	}
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case float32:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case json.Number:
		i, err := strconv.Atoi(v.String())
		if err == nil {
			return i
		}
	case string:
		i, err := strconv.Atoi(v)
		if err == nil {
			return i
		}
	}
	return fallback
}

func stringArg(args map[string]interface{}, key, fallback string) string {
	if args == nil {
		return fallback
	}
	if v, ok := args[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return fallback
}

// ---- Transport handlers ----

// RunStdio runs the MCP server over stdin/stdout (for local AI tool integration)
func (s *MCPServer) RunStdio() error {
	reader := bufio.NewReader(os.Stdin)
	writer := os.Stdout

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			if global.APP_LOG != nil {
				global.APP_LOG.Error("MCP stdio read error", zap.Error(err))
			}
			return err
		}

		var req JSONRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			if global.APP_LOG != nil {
				global.APP_LOG.Warn("MCP invalid JSON received", zap.Error(err))
			}
			continue
		}

		resp := s.HandleRequest(req)
		respBytes, _ := json.Marshal(resp)
		fmt.Fprintf(writer, "%s\n", respBytes)
	}
}

// RunHTTP starts an HTTP JSON-RPC endpoint for MCP experiments.
func (s *MCPServer) RunHTTP(addr string) error {
	mux := http.NewServeMux()

	// POST endpoint for MCP messages
	mux.HandleFunc("/api/v1/mcp/message", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Auth check
		token := r.Header.Get("Authorization")
		if token != "Bearer "+s.apiToken && s.apiToken != "" {
			http.Error(w, `{"jsonrpc":"2.0","error":{"code":-32001,"message":"Unauthorized"}}`, http.StatusUnauthorized)
			return
		}

		var req JSONRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"jsonrpc":"2.0","error":{"code":-32700,"message":"Parse error"}}`, http.StatusBadRequest)
			return
		}

		resp := s.HandleRequest(req)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	if global.APP_LOG != nil {
		global.APP_LOG.Info("MCP HTTP server starting", zap.String("addr", addr))
	}
	return http.ListenAndServe(addr, mux)
}
