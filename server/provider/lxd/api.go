package lxd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

func (l *LXDProvider) apiListInstances(ctx context.Context) ([]provider.Instance, error) {
	url := fmt.Sprintf("https://%s:8443/1.0/instances", l.config.Host)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := l.apiClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var response map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}

	var instances []provider.Instance
	if metadata, ok := response["metadata"].([]interface{}); ok {
		for _, item := range metadata {
			if instanceData, ok := item.(map[string]interface{}); ok {
				instance := provider.Instance{
					ID:     instanceData["name"].(string),
					Name:   instanceData["name"].(string),
					Status: instanceData["status"].(string),
					Type:   instanceData["type"].(string),
				}
				instances = append(instances, instance)
			}
		}
	}

	return instances, nil
}

func (l *LXDProvider) apiCreateInstance(ctx context.Context, config provider.InstanceConfig) error {
	return l.apiCreateInstanceWithProgress(ctx, config, nil)
}

func (l *LXDProvider) apiCreateInstanceWithProgress(ctx context.Context, config provider.InstanceConfig, progressCallback provider.ProgressCallback) error {
	// 进度更新辅助函数
	updateProgress := func(percentage int, message string) {
		if progressCallback != nil {
			progressCallback(percentage, message)
		}
		global.APP_LOG.Info("LXD API实例创建进度",
			zap.String("instance", config.Name),
			zap.Int("percentage", percentage),
			zap.String("message", message))
	}

	updateProgress(10, "开始LXD API创建实例...")

	// 在API创建之前，处理镜像下载和导入
	updateProgress(30, "处理镜像下载和导入...")
	if err := l.handleImageDownloadAndImport(ctx, &config); err != nil {
		return fmt.Errorf("镜像处理失败: %w", err)
	}

	updateProgress(50, "调用LXD API创建实例...")

	// 构造实例配置
	instanceConfig := map[string]interface{}{
		"name": config.Name,
		"source": map[string]interface{}{
			"type":  "image",
			"alias": config.Image,
		},
		"config":   map[string]interface{}{},
		"devices":  map[string]interface{}{},
		"profiles": []string{"default"},
	}

	// 设置实例类型
	if config.InstanceType == "vm" {
		instanceConfig["type"] = "virtual-machine"
	} else {
		instanceConfig["type"] = "container"
	}

	// 资源配置
	if config.CPU != "" {
		instanceConfig["config"].(map[string]interface{})["limits.cpu"] = config.CPU
	}
	if config.Memory != "" {
		instanceConfig["config"].(map[string]interface{})["limits.memory"] = config.Memory
	}
	if config.Disk != "" {
		instanceConfig["devices"].(map[string]interface{})["root"] = map[string]interface{}{
			"type": "disk",
			"path": "/",
			"pool": "default",
			"size": config.Disk,
		}
	}

	// 序列化请求体
	jsonData, err := json.Marshal(instanceConfig)
	if err != nil {
		return fmt.Errorf("marshal instance config failed: %w", err)
	}

	// 发送创建请求
	url := fmt.Sprintf("https://%s:8443/1.0/instances", l.config.Host)
	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(jsonData)))
	if err != nil {
		return fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := l.apiClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		var respData map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&respData)
		return fmt.Errorf("failed to create instance via API: status %d, response: %v", resp.StatusCode, respData)
	}

	updateProgress(70, "启动实例...")
	// 启动实例
	if err := l.apiStartInstance(ctx, config.Name); err != nil {
		return fmt.Errorf("failed to start instance: %w", err)
	}

	updateProgress(90, "配置SSH密码...")
	// 等待实例启动并设置密码
	if err := l.waitForInstanceReady(ctx, config.Name); err != nil {
		global.APP_LOG.Warn("等待实例启动超时，尝试直接设置SSH密码",
			zap.String("instanceName", config.Name),
			zap.Error(err))
	}

	// 设置SSH密码 - 从元数据中获取密码
	if config.Metadata != nil {
		if password, ok := config.Metadata["password"]; ok {
			if err := l.apiSetInstancePassword(ctx, config.Name, password); err != nil {
				global.APP_LOG.Warn("配置SSH密码失败", zap.Error(err))
			}
		}
	}

	updateProgress(100, "LXD API实例创建完成")
	global.APP_LOG.Info("LXD API实例创建成功", zap.String("name", config.Name))
	return nil
}

func (l *LXDProvider) apiStartInstance(ctx context.Context, id string) error {
	url := fmt.Sprintf("https://%s:8443/1.0/instances/%s/state", l.config.Host, id)
	payload := map[string]interface{}{
		"action": "start",
	}

	jsonData, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "PUT", url, strings.NewReader(string(jsonData)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := l.apiClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("failed to start instance: %d", resp.StatusCode)
	}

	return nil
}

func (l *LXDProvider) apiStopInstance(ctx context.Context, id string) error {
	url := fmt.Sprintf("https://%s:8443/1.0/instances/%s/state", l.config.Host, id)
	payload := map[string]interface{}{
		"action": "stop",
	}

	jsonData, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "PUT", url, strings.NewReader(string(jsonData)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := l.apiClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("failed to stop instance: %d", resp.StatusCode)
	}

	return nil
}

func (l *LXDProvider) apiRestartInstance(ctx context.Context, id string) error {
	url := fmt.Sprintf("https://%s:8443/1.0/instances/%s/state", l.config.Host, id)
	payload := map[string]interface{}{
		"action": "restart",
	}

	jsonData, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "PUT", url, strings.NewReader(string(jsonData)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := l.apiClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("failed to restart instance: %d", resp.StatusCode)
	}

	return nil
}

func (l *LXDProvider) apiDeleteInstance(ctx context.Context, id string) error {
	url := fmt.Sprintf("https://%s:8443/1.0/instances/%s", l.config.Host, id)
	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		return err
	}

	resp, err := l.apiClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("failed to delete instance: %d", resp.StatusCode)
	}

	return nil
}

func (l *LXDProvider) apiListImages(ctx context.Context) ([]provider.Image, error) {
	url := fmt.Sprintf("https://%s:8443/1.0/images", l.config.Host)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := l.apiClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var response map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}

	var images []provider.Image
	if metadata, ok := response["metadata"].([]interface{}); ok {
		for _, item := range metadata {
			if imageData, ok := item.(map[string]interface{}); ok {
				image := provider.Image{
					ID:   imageData["fingerprint"].(string)[:12],
					Name: "unknown",
					Tag:  "latest",
					Size: fmt.Sprintf("%.2f MB", imageData["size"].(float64)/1024/1024),
				}
				if aliases, ok := imageData["aliases"].([]interface{}); ok && len(aliases) > 0 {
					if alias, ok := aliases[0].(map[string]interface{}); ok {
						image.Name = alias["name"].(string)
					}
				}
				images = append(images, image)
			}
		}
	}

	return images, nil
}

func (l *LXDProvider) apiPullImage(ctx context.Context, image string) error {
	// 构造从远程镜像服务器拉取镜像的请求
	pullConfig := map[string]interface{}{
		"server":      "https://images.linuxcontainers.org",
		"protocol":    "simplestreams",
		"alias":       image,
		"auto_update": false,
	}

	jsonData, err := json.Marshal(pullConfig)
	if err != nil {
		return fmt.Errorf("marshal pull config failed: %w", err)
	}

	url := fmt.Sprintf("https://%s:8443/1.0/images", l.config.Host)
	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(jsonData)))
	if err != nil {
		return fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := l.apiClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		var respData map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&respData)
		return fmt.Errorf("failed to pull image via API: status %d, response: %v", resp.StatusCode, respData)
	}

	global.APP_LOG.Info("LXD API拉取镜像成功", zap.String("image", utils.TruncateString(image, 100)))
	return nil
}

func (l *LXDProvider) apiDeleteImage(ctx context.Context, id string) error {
	url := fmt.Sprintf("https://%s:8443/1.0/images/%s", l.config.Host, id)
	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		return err
	}

	resp, err := l.apiClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("failed to delete image: %d", resp.StatusCode)
	}

	return nil
}

// apiSetInstancePassword 通过API设置实例密码
func (l *LXDProvider) apiSetInstancePassword(ctx context.Context, instanceID, password string) error {
	// LXD API方式设置密码
	// 构造执行命令的请求
	execData := map[string]interface{}{
		"command":     []string{"bash", "-c", fmt.Sprintf("echo 'root:%s' | chpasswd", password)},
		"wait-for-ws": true,
		"interactive": false,
	}

	execDataBytes, err := json.Marshal(execData)
	if err != nil {
		return fmt.Errorf("marshal exec data failed: %w", err)
	}

	// 发送执行请求
	url := fmt.Sprintf("https://%s:8443/1.0/instances/%s/exec", l.config.Host, instanceID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(execDataBytes)))
	if err != nil {
		return fmt.Errorf("create request failed: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := l.apiClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("API request failed with status: %d", resp.StatusCode)
	}

	global.APP_LOG.Info("LXD实例密码设置成功(API)",
		zap.String("instanceID", utils.TruncateString(instanceID, 12)))

	return nil
}

// apiSetInstanceConfig 通过API设置实例配置
func (l *LXDProvider) apiSetInstanceConfig(ctx context.Context, instanceID string, key string, value string) error {
	// 构造配置更新请求
	configData := map[string]interface{}{
		"config": map[string]string{
			key: value,
		},
	}

	jsonData, err := json.Marshal(configData)
	if err != nil {
		return fmt.Errorf("marshal config data failed: %w", err)
	}

	url := fmt.Sprintf("https://%s:8443/1.0/instances/%s", l.config.Host, instanceID)
	req, err := http.NewRequestWithContext(ctx, "PUT", url, strings.NewReader(string(jsonData)))
	if err != nil {
		return fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := l.apiClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		var respData map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&respData)
		return fmt.Errorf("failed to set instance config via API: status %d, response: %v", resp.StatusCode, respData)
	}

	global.APP_LOG.Info("LXD实例配置设置成功(API)",
		zap.String("instanceID", utils.TruncateString(instanceID, 12)),
		zap.String("key", key),
		zap.String("value", utils.TruncateString(value, 50)))

	return nil
}

// apiSetInstanceDeviceConfig 通过API设置实例设备配置
func (l *LXDProvider) apiSetInstanceDeviceConfig(ctx context.Context, instanceID string, deviceName string, key string, value string) error {
	// 首先获取当前实例配置
	url := fmt.Sprintf("https://%s:8443/1.0/instances/%s", l.config.Host, instanceID)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("create get request failed: %w", err)
	}

	resp, err := l.apiClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute get API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to get instance config: status %d", resp.StatusCode)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return fmt.Errorf("decode response failed: %w", err)
	}

	metadata, ok := response["metadata"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid response format")
	}

	// 获取或创建devices部分
	devices, ok := metadata["devices"].(map[string]interface{})
	if !ok {
		devices = make(map[string]interface{})
	}

	// 获取或创建特定设备
	device, ok := devices[deviceName].(map[string]interface{})
	if !ok {
		device = make(map[string]interface{})
	}

	// 设置新的配置值
	device[key] = value
	devices[deviceName] = device

	// 构造更新请求
	updateData := map[string]interface{}{
		"devices": devices,
	}

	jsonData, err := json.Marshal(updateData)
	if err != nil {
		return fmt.Errorf("marshal update data failed: %w", err)
	}

	// 发送更新请求
	req, err = http.NewRequestWithContext(ctx, "PUT", url, strings.NewReader(string(jsonData)))
	if err != nil {
		return fmt.Errorf("create put request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err = l.apiClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute put API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		var respData map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&respData)
		return fmt.Errorf("failed to set device config via API: status %d, response: %v", resp.StatusCode, respData)
	}

	global.APP_LOG.Info("LXD实例设备配置设置成功(API)",
		zap.String("instanceID", utils.TruncateString(instanceID, 12)),
		zap.String("device", deviceName),
		zap.String("key", key),
		zap.String("value", utils.TruncateString(value, 50)))

	return nil
}
