package proxmox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// probeAPIConnection performs one bounded authenticated request and records a
// usable node name from the response. It is intentionally independent of SSH
// so api_only providers can be loaded and recovered after a host reboot.
func (p *ProxmoxProvider) probeAPIConnection(ctx context.Context) error {
	if p.apiClient == nil {
		return fmt.Errorf("PVE API客户端未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, p.apiEndpoint("/api2/json/nodes"), nil)
	if err != nil {
		return err
	}
	p.setAPIAuth(req)
	resp, err := p.apiClient.Do(req)
	if err != nil {
		return err
	}
	if resp == nil || resp.Body == nil {
		return fmt.Errorf("PVE API返回空响应")
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if readErr != nil {
		return readErr
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("PVE API返回HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var envelope struct {
		Data []struct {
			Node string `json:"node"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("解析PVE节点响应失败: %w", err)
	}
	if strings.TrimSpace(p.nodeName()) == "" {
		for _, node := range envelope.Data {
			if value := strings.TrimSpace(node.Node); value != "" {
				p.setNodeName(value)
				break
			}
		}
	}
	if strings.TrimSpace(p.nodeName()) == "" {
		return fmt.Errorf("PVE节点响应缺少可用node")
	}
	return nil
}
