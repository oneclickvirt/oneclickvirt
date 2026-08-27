package proxmox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"

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
	statusURL, err := p.apiGuestEndpoint(instanceType, vmid, "status/current")
	if err != nil {
		return err
	}

	statusReq, err := http.NewRequestWithContext(ctx, "GET", statusURL, nil)
	if err != nil {
		return fmt.Errorf("创建状态查询请求失败: %w", err)
	}
	p.setAPIAuth(statusReq)

	if p.apiClient == nil {
		return fmt.Errorf("查询实例状态失败: PVE API客户端未初始化")
	}
	statusResp, err := p.apiClient.Do(statusReq)
	if err != nil {
		return fmt.Errorf("查询实例状态失败: %w", err)
	}
	if statusResp == nil || statusResp.Body == nil {
		return fmt.Errorf("查询实例状态失败: PVE API返回空响应")
	}
	defer statusResp.Body.Close()
	if statusResp.StatusCode < http.StatusOK || statusResp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("查询实例状态失败: 状态码 %d", statusResp.StatusCode)
	}

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
	url, err := p.apiGuestEndpoint("container", vmid, "exec")
	if err != nil {
		return err
	}

	// PVE 的 exec API 是异步任务。通过 base64 传递完整的 root 凭据，
	// 避免密码中的引号、空格或 shell 元字符改变命令含义。
	credential := base64.StdEncoding.EncodeToString([]byte("root:" + password + "\n"))
	payload := map[string]interface{}{
		"command": fmt.Sprintf("printf %s | base64 -d | chpasswd", shellSingleQuote(credential)),
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("序列化请求失败: %w", err)
	}

	if err := p.submitProxmoxAPITaskAndWait(ctx, http.MethodPost, url, jsonData, "设置容器密码"); err != nil {
		return fmt.Errorf("设置容器密码失败: %w", err)
	}

	global.APP_LOG.Info("通过API成功设置容器密码", zap.String("vmid", vmid))
	return nil
}

// apiSetVMPassword 通过API为QEMU虚拟机设置密码
func (p *ProxmoxProvider) apiSetVMPassword(ctx context.Context, vmid, password string) error {
	// 使用cloud-init设置密码
	url, err := p.apiGuestEndpoint("vm", vmid, "config")
	if err != nil {
		return err
	}

	// 构造cloud-init密码配置
	payload := map[string]interface{}{
		"cipassword": password,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("序列化请求失败: %w", err)
	}

	if err := p.submitProxmoxConfigTaskWithRetry(ctx, url, jsonData, "设置虚拟机密码"); err != nil {
		return fmt.Errorf("设置虚拟机密码失败: %w", err)
	}

	// 重启虚拟机以应用密码更改
	restartURL, err := p.apiGuestEndpoint("vm", vmid, "status/reboot")
	if err != nil {
		return err
	}
	if err := p.submitProxmoxAPITaskAndWait(ctx, http.MethodPost, restartURL, nil, "重启虚拟机"); err != nil {
		global.APP_LOG.Warn("重启虚拟机失败，密码已写入但可能需要手动重启",
			zap.String("vmid", vmid), zap.Error(err))
		return nil
	}

	global.APP_LOG.Info("通过API成功设置虚拟机密码并重启", zap.String("vmid", vmid))
	return nil
}
