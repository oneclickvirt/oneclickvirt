package proxmox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

// apiSetInstancePassword 通过API设置实例密码
func (p *ProxmoxProvider) apiSetInstancePassword(ctx context.Context, instanceID, password string) error {
	// 先查找实例的VMID和类型
	vmid, instanceType, err := p.findVMIDByNameOrID(ctx, instanceID)
	if err != nil {
		global.APP_LOG.Error("API查找Proxmox实例失败",
			zap.String("instanceID", instanceID),
			zap.Error(err))
		return fmt.Errorf("查找实例失败: %w", err)
	}

	// 检查实例状态
	var statusURL string
	switch instanceType {
	case "container":
		statusURL = fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/lxc/%s/status/current", p.config.Host, p.node, vmid)
	case "vm":
		statusURL = fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/qemu/%s/status/current", p.config.Host, p.node, vmid)
	default:
		return fmt.Errorf("未知的实例类型: %s", instanceType)
	}

	statusReq, err := http.NewRequestWithContext(ctx, "GET", statusURL, nil)
	if err != nil {
		return fmt.Errorf("创建状态查询请求失败: %w", err)
	}
	p.setAPIAuth(statusReq)

	statusResp, err := p.apiClient.Do(statusReq)
	if err != nil {
		return fmt.Errorf("查询实例状态失败: %w", err)
	}
	defer statusResp.Body.Close()

	var statusResponse map[string]interface{}
	if err := json.NewDecoder(statusResp.Body).Decode(&statusResponse); err != nil {
		return fmt.Errorf("解析状态响应失败: %w", err)
	}

	if data, ok := statusResponse["data"].(map[string]interface{}); ok {
		if status, ok := data["status"].(string); ok && status != "running" {
			return fmt.Errorf("实例 %s (VMID: %s) 未运行，当前状态: %s", instanceID, vmid, status)
		}
	}

	// 根据实例类型设置密码
	switch instanceType {
	case "container":
		// LXC容器 - 通过API执行命令设置密码
		return p.apiSetContainerPassword(ctx, vmid, password)
	case "vm":
		// QEMU虚拟机 - 通过API设置cloud-init密码
		return p.apiSetVMPassword(ctx, vmid, password)
	default:
		return fmt.Errorf("未知的实例类型: %s", instanceType)
	}
}

// apiSetContainerPassword 通过API为LXC容器设置密码
func (p *ProxmoxProvider) apiSetContainerPassword(ctx context.Context, vmid, password string) error {
	// 使用LXC容器的exec API执行chpasswd命令
	url := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/lxc/%s/exec", p.config.Host, p.node, vmid)

	// 构造执行命令的请求体
	payload := map[string]interface{}{
		"command": fmt.Sprintf("echo 'root:%s' | chpasswd", password),
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("序列化请求失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(jsonData)))
	if err != nil {
		return fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	p.setAPIAuth(req)

	resp, err := p.apiClient.Do(req)
	if err != nil {
		return fmt.Errorf("执行API请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var respData map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&respData)
		return fmt.Errorf("设置容器密码失败: status %d, response: %v", resp.StatusCode, respData)
	}

	global.APP_LOG.Info("通过API成功设置容器密码", zap.String("vmid", vmid))
	return nil
}

// apiSetVMPassword 通过API为QEMU虚拟机设置密码
func (p *ProxmoxProvider) apiSetVMPassword(ctx context.Context, vmid, password string) error {
	// 使用cloud-init设置密码
	url := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/qemu/%s/config", p.config.Host, p.node, vmid)

	// 构造cloud-init密码配置
	payload := map[string]interface{}{
		"cipassword": password,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("序列化请求失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "PUT", url, strings.NewReader(string(jsonData)))
	if err != nil {
		return fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	p.setAPIAuth(req)

	resp, err := p.apiClient.Do(req)
	if err != nil {
		return fmt.Errorf("执行API请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var respData map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&respData)
		return fmt.Errorf("设置虚拟机密码失败: status %d, response: %v", resp.StatusCode, respData)
	}

	// 重启虚拟机以应用密码更改
	restartURL := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/qemu/%s/status/reboot", p.config.Host, p.node, vmid)
	restartReq, err := http.NewRequestWithContext(ctx, "POST", restartURL, nil)
	if err != nil {
		global.APP_LOG.Warn("创建重启请求失败", zap.String("vmid", vmid), zap.Error(err))
		return nil // 密码已设置，重启失败不影响
	}
	p.setAPIAuth(restartReq)

	restartResp, err := p.apiClient.Do(restartReq)
	if err != nil {
		global.APP_LOG.Warn("重启虚拟机失败", zap.String("vmid", vmid), zap.Error(err))
		return nil // 密码已设置，重启失败不影响
	}
	defer restartResp.Body.Close()

	global.APP_LOG.Info("通过API成功设置虚拟机密码并重启", zap.String("vmid", vmid))
	return nil
}
