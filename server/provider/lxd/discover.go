package lxd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/provider"

	"go.uber.org/zap"
)

// DiscoverInstances 发现LXD provider上的所有实例
func (l *LXDProvider) DiscoverInstances(ctx context.Context) ([]provider.DiscoveredInstance, error) {
	if !l.connected {
		return nil, fmt.Errorf("not connected")
	}

	global.APP_LOG.Info("开始发现LXD实例", zap.String("provider", l.config.Name))

	// 优先使用API方式发现
	if l.shouldUseAPI() {
		instances, err := l.apiDiscoverInstances(ctx)
		if err == nil {
			global.APP_LOG.Info("LXD API发现实例成功",
				zap.String("provider", l.config.Name),
				zap.Int("count", len(instances)))
			return instances, nil
		}
		global.APP_LOG.Warn("LXD API发现实例失败", zap.Error(err))

		// 检查是否可以回退到SSH
		if !l.shouldFallbackToSSH() {
			return nil, fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
		}
		global.APP_LOG.Info("回退到SSH执行 - 发现实例")
	}

	// 如果执行规则不允许使用SSH，则返回错误
	if !l.shouldUseSSH() {
		return nil, fmt.Errorf("执行规则不允许使用SSH")
	}

	// SSH方式发现
	return l.sshDiscoverInstances(ctx)
}

// apiDiscoverInstances 通过LXD API发现实例
func (l *LXDProvider) apiDiscoverInstances(ctx context.Context) ([]provider.DiscoveredInstance, error) {
	// 获取所有实例的详细信息
	url := fmt.Sprintf("https://%s:8443/1.0/instances?recursion=2", l.config.Host)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	resp, err := l.apiClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API返回错误状态码: %d", resp.StatusCode)
	}

	var response struct {
		Type     string `json:"type"`
		Metadata []struct {
			Name           string                 `json:"name"`
			Status         string                 `json:"status"`
			Type           string                 `json:"type"`
			Description    string                 `json:"description"`
			Config         map[string]string      `json:"config"`
			Devices        map[string]interface{} `json:"devices"`
			ExpandedConfig map[string]string      `json:"expanded_config"`
			State          *struct {
				Status  string                 `json:"status"`
				CPU     map[string]interface{} `json:"cpu"`
				Memory  map[string]interface{} `json:"memory"`
				Network map[string]struct {
					Addresses []struct {
						Family  string `json:"family"`
						Address string `json:"address"`
						Netmask string `json:"netmask"`
						Scope   string `json:"scope"`
					} `json:"addresses"`
					Hwaddr string `json:"hwaddr"`
				} `json:"network"`
			} `json:"state,omitempty"`
		} `json:"metadata"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	var discoveredInstances []provider.DiscoveredInstance

	for _, inst := range response.Metadata {
		discovered := provider.DiscoveredInstance{
			Name:         inst.Name,
			Status:       l.mapLXDStatus(inst.Status),
			InstanceType: l.mapLXDType(inst.Type),
			RawData:      inst,
		}

		// 解析资源配置
		if cpuLimit, ok := inst.ExpandedConfig["limits.cpu"]; ok {
			if cpu, err := strconv.Atoi(cpuLimit); err == nil {
				discovered.CPU = cpu
			}
		}
		// 如果没有CPU限制，默认为1核
		if discovered.CPU == 0 {
			discovered.CPU = 1
		}

		if memLimit, ok := inst.ExpandedConfig["limits.memory"]; ok {
			discovered.Memory = l.parseMemoryLimit(memLimit)
		}
		// 如果没有内存限制，默认为512MB
		if discovered.Memory == 0 {
			discovered.Memory = 512
		}

		// 解析磁盘大小（从root设备）
		if rootDevice, ok := inst.Devices["root"].(map[string]interface{}); ok {
			if size, ok := rootDevice["size"].(string); ok {
				discovered.Disk = l.parseDiskSize(size)
			}
		}
		// 如果没有磁盘限制，默认为10GB
		if discovered.Disk == 0 {
			discovered.Disk = 10240
		}

		// 解析网络信息
		if inst.State != nil && inst.State.Network != nil {
			var extraPorts []int
			for netName, netInfo := range inst.State.Network {
				if netName == "lo" {
					continue
				}

				// 提取MAC地址
				if discovered.MACAddress == "" {
					discovered.MACAddress = netInfo.Hwaddr
				}

				// 提取IP地址
				for _, addr := range netInfo.Addresses {
					if addr.Scope != "global" && addr.Scope != "link" {
						continue
					}
					if addr.Family == "inet" && discovered.PrivateIP == "" {
						discovered.PrivateIP = addr.Address
					}
					if addr.Family == "inet6" && discovered.IPv6Address == "" {
						discovered.IPv6Address = addr.Address
					}
				}
			}
			discovered.ExtraPorts = extraPorts
		}

		// 尝试从配置中获取镜像信息
		if image, ok := inst.Config["image.description"]; ok {
			discovered.Image = image
		} else if os, ok := inst.Config["image.os"]; ok {
			discovered.Image = os
		}

		// 操作系统类型
		if osType, ok := inst.Config["image.os"]; ok {
			discovered.OSType = osType
		}

		// SSH端口默认为22
		discovered.SSHPort = 22

		// 生成UUID（如果LXD没有提供，使用实例名称的哈希）
		if uuid, ok := inst.Config["volatile.uuid"]; ok {
			discovered.UUID = uuid
		} else {
			// 使用名称作为标识
			discovered.UUID = fmt.Sprintf("lxd-%s-%s", l.config.Name, inst.Name)
		}

		discoveredInstances = append(discoveredInstances, discovered)
	}

	return discoveredInstances, nil
}

// sshDiscoverInstances 通过SSH命令发现实例
func (l *LXDProvider) sshDiscoverInstances(ctx context.Context) ([]provider.DiscoveredInstance, error) {
	if l.sshClient == nil {
		return nil, fmt.Errorf("SSH client not initialized")
	}

	// 使用lxc list命令获取所有实例的详细信息
	cmd := "lxc list --format=json"
	output, err := l.sshClient.Execute(cmd)
	if err != nil {
		return nil, fmt.Errorf("执行SSH命令失败: %w", err)
	}

	var instances []struct {
		Name    string                 `json:"name"`
		Status  string                 `json:"status"`
		Type    string                 `json:"type"`
		Config  map[string]string      `json:"config"`
		Devices map[string]interface{} `json:"devices"`
		State   *struct {
			Network map[string]struct {
				Addresses []struct {
					Family  string `json:"family"`
					Address string `json:"address"`
					Scope   string `json:"scope"`
				} `json:"addresses"`
				Hwaddr string `json:"hwaddr"`
			} `json:"network"`
		} `json:"state,omitempty"`
	}

	if err := json.Unmarshal([]byte(output), &instances); err != nil {
		return nil, fmt.Errorf("解析JSON失败: %w", err)
	}

	var discoveredInstances []provider.DiscoveredInstance

	for _, inst := range instances {
		discovered := provider.DiscoveredInstance{
			Name:         inst.Name,
			Status:       l.mapLXDStatus(inst.Status),
			InstanceType: l.mapLXDType(inst.Type),
			RawData:      inst,
		}

		// 解析配置（与API方式类似）
		if cpuLimit, ok := inst.Config["limits.cpu"]; ok {
			if cpu, err := strconv.Atoi(cpuLimit); err == nil {
				discovered.CPU = cpu
			}
		}
		if discovered.CPU == 0 {
			discovered.CPU = 1
		}

		if memLimit, ok := inst.Config["limits.memory"]; ok {
			discovered.Memory = l.parseMemoryLimit(memLimit)
		}
		if discovered.Memory == 0 {
			discovered.Memory = 512
		}

		// 解析网络信息
		if inst.State != nil && inst.State.Network != nil {
			for netName, netInfo := range inst.State.Network {
				if netName == "lo" {
					continue
				}

				if discovered.MACAddress == "" {
					discovered.MACAddress = netInfo.Hwaddr
				}

				for _, addr := range netInfo.Addresses {
					if addr.Scope != "global" && addr.Scope != "link" {
						continue
					}
					if addr.Family == "inet" && discovered.PrivateIP == "" {
						discovered.PrivateIP = addr.Address
					}
					if addr.Family == "inet6" && discovered.IPv6Address == "" {
						discovered.IPv6Address = addr.Address
					}
				}
			}
		}

		// 镜像和系统信息
		if image, ok := inst.Config["image.description"]; ok {
			discovered.Image = image
		}
		if osType, ok := inst.Config["image.os"]; ok {
			discovered.OSType = osType
		}

		discovered.SSHPort = 22

		if uuid, ok := inst.Config["volatile.uuid"]; ok {
			discovered.UUID = uuid
		} else {
			discovered.UUID = fmt.Sprintf("lxd-%s-%s", l.config.Name, inst.Name)
		}

		discoveredInstances = append(discoveredInstances, discovered)
	}

	return discoveredInstances, nil
}

// 辅助函数

func (l *LXDProvider) mapLXDStatus(lxdStatus string) string {
	switch strings.ToLower(lxdStatus) {
	case "running":
		return "running"
	case "stopped":
		return "stopped"
	case "frozen":
		return "frozen"
	default:
		return lxdStatus
	}
}

func (l *LXDProvider) mapLXDType(lxdType string) string {
	if strings.Contains(strings.ToLower(lxdType), "virtual") || lxdType == "vm" {
		return "vm"
	}
	return "container"
}

func (l *LXDProvider) parseMemoryLimit(memStr string) int64 {
	memStr = strings.ToUpper(strings.TrimSpace(memStr))

	// 移除末尾的B
	memStr = strings.TrimSuffix(memStr, "B")

	var multiplier float64 = 1.0
	if strings.HasSuffix(memStr, "K") {
		multiplier = 1.0 / 1024.0 // KB to MB
		memStr = strings.TrimSuffix(memStr, "K")
	} else if strings.HasSuffix(memStr, "M") {
		multiplier = 1.0 // Already in MB
		memStr = strings.TrimSuffix(memStr, "M")
	} else if strings.HasSuffix(memStr, "G") {
		multiplier = 1024.0 // GB to MB
		memStr = strings.TrimSuffix(memStr, "G")
	} else if strings.HasSuffix(memStr, "T") {
		multiplier = 1024.0 * 1024.0 // TB to MB
		memStr = strings.TrimSuffix(memStr, "T")
	}

	if value, err := strconv.ParseFloat(memStr, 64); err == nil {
		return int64(value * multiplier)
	}

	return 0
}

func (l *LXDProvider) parseDiskSize(sizeStr string) int64 {
	sizeStr = strings.ToUpper(strings.TrimSpace(sizeStr))

	// 移除末尾的B
	sizeStr = strings.TrimSuffix(sizeStr, "B")

	var multiplier float64 = 1.0
	if strings.HasSuffix(sizeStr, "K") {
		multiplier = 1.0 / 1024.0 // KB to MB
		sizeStr = strings.TrimSuffix(sizeStr, "K")
	} else if strings.HasSuffix(sizeStr, "M") {
		multiplier = 1.0 // Already in MB
		sizeStr = strings.TrimSuffix(sizeStr, "M")
	} else if strings.HasSuffix(sizeStr, "G") {
		multiplier = 1024.0 // GB to MB
		sizeStr = strings.TrimSuffix(sizeStr, "G")
	} else if strings.HasSuffix(sizeStr, "T") {
		multiplier = 1024.0 * 1024.0 // TB to MB
		sizeStr = strings.TrimSuffix(sizeStr, "T")
	}

	if value, err := strconv.ParseFloat(sizeStr, 64); err == nil {
		return int64(value * multiplier)
	}

	return 0
}
