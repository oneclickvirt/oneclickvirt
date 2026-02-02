package incus

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

// DiscoverInstances 发现Incus provider上的所有实例
func (i *IncusProvider) DiscoverInstances(ctx context.Context) ([]provider.DiscoveredInstance, error) {
	if !i.connected {
		return nil, fmt.Errorf("not connected")
	}

	global.APP_LOG.Info("开始发现Incus实例", zap.String("provider", i.config.Name))

	// 优先使用API方式发现
	if i.shouldUseAPI() {
		instances, err := i.apiDiscoverInstances(ctx)
		if err == nil {
			global.APP_LOG.Info("Incus API发现实例成功",
				zap.String("provider", i.config.Name),
				zap.Int("count", len(instances)))
			return instances, nil
		}
		global.APP_LOG.Warn("Incus API发现实例失败", zap.Error(err))

		if !i.shouldFallbackToSSH() {
			return nil, fmt.Errorf("API调用失败且不允许回退到SSH: %w", err)
		}
		global.APP_LOG.Info("回退到SSH执行 - 发现实例")
	}

	if !i.shouldUseSSH() {
		return nil, fmt.Errorf("执行规则不允许使用SSH")
	}

	return i.sshDiscoverInstances(ctx)
}

// apiDiscoverInstances 通过Incus API发现实例
func (i *IncusProvider) apiDiscoverInstances(ctx context.Context) ([]provider.DiscoveredInstance, error) {
	url := fmt.Sprintf("https://%s:8443/1.0/instances?recursion=2", i.config.Host)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	resp, err := i.apiClient.Do(req)
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
			Config         map[string]string      `json:"config"`
			Devices        map[string]interface{} `json:"devices"`
			ExpandedConfig map[string]string      `json:"expanded_config"`
			State          *struct {
				Network map[string]struct {
					Addresses []struct {
						Family  string `json:"family"`
						Address string `json:"address"`
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
			Status:       i.mapIncusStatus(inst.Status),
			InstanceType: i.mapIncusType(inst.Type),
			RawData:      inst,
		}

		// 解析资源配置
		if cpuLimit, ok := inst.ExpandedConfig["limits.cpu"]; ok {
			if cpu, err := strconv.Atoi(cpuLimit); err == nil {
				discovered.CPU = cpu
			}
		}
		if discovered.CPU == 0 {
			discovered.CPU = 1
		}

		if memLimit, ok := inst.ExpandedConfig["limits.memory"]; ok {
			discovered.Memory = i.parseMemoryLimit(memLimit)
		}
		if discovered.Memory == 0 {
			discovered.Memory = 512
		}

		// 解析磁盘大小
		if rootDevice, ok := inst.Devices["root"].(map[string]interface{}); ok {
			if size, ok := rootDevice["size"].(string); ok {
				discovered.Disk = i.parseDiskSize(size)
			}
		}
		if discovered.Disk == 0 {
			discovered.Disk = 10240
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

		// 镜像信息
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
			discovered.UUID = fmt.Sprintf("incus-%s-%s", i.config.Name, inst.Name)
		}

		discoveredInstances = append(discoveredInstances, discovered)
	}

	return discoveredInstances, nil
}

// sshDiscoverInstances 通过SSH命令发现实例
func (i *IncusProvider) sshDiscoverInstances(ctx context.Context) ([]provider.DiscoveredInstance, error) {
	if i.sshClient == nil {
		return nil, fmt.Errorf("SSH client not initialized")
	}

	cmd := "incus list --format=json"
	output, err := i.sshClient.Execute(cmd)
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
			Status:       i.mapIncusStatus(inst.Status),
			InstanceType: i.mapIncusType(inst.Type),
			RawData:      inst,
		}

		if cpuLimit, ok := inst.Config["limits.cpu"]; ok {
			if cpu, err := strconv.Atoi(cpuLimit); err == nil {
				discovered.CPU = cpu
			}
		}
		if discovered.CPU == 0 {
			discovered.CPU = 1
		}

		if memLimit, ok := inst.Config["limits.memory"]; ok {
			discovered.Memory = i.parseMemoryLimit(memLimit)
		}
		if discovered.Memory == 0 {
			discovered.Memory = 512
		}

		// 网络信息
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
			discovered.UUID = fmt.Sprintf("incus-%s-%s", i.config.Name, inst.Name)
		}

		discoveredInstances = append(discoveredInstances, discovered)
	}

	return discoveredInstances, nil
}

// 辅助函数
func (i *IncusProvider) mapIncusStatus(status string) string {
	switch strings.ToLower(status) {
	case "running":
		return "running"
	case "stopped":
		return "stopped"
	case "frozen":
		return "frozen"
	default:
		return status
	}
}

func (i *IncusProvider) mapIncusType(incusType string) string {
	if strings.Contains(strings.ToLower(incusType), "virtual") || incusType == "vm" {
		return "vm"
	}
	return "container"
}

func (i *IncusProvider) parseMemoryLimit(memStr string) int64 {
	memStr = strings.ToUpper(strings.TrimSpace(memStr))
	memStr = strings.TrimSuffix(memStr, "B")

	var multiplier int64 = 1
	if strings.HasSuffix(memStr, "M") {
		multiplier = 1
		memStr = strings.TrimSuffix(memStr, "M")
	} else if strings.HasSuffix(memStr, "G") {
		multiplier = 1024
		memStr = strings.TrimSuffix(memStr, "G")
	} else if strings.HasSuffix(memStr, "T") {
		multiplier = 1024 * 1024
		memStr = strings.TrimSuffix(memStr, "T")
	}

	if value, err := strconv.ParseInt(memStr, 10, 64); err == nil {
		return value * multiplier
	}

	return 0
}

func (i *IncusProvider) parseDiskSize(sizeStr string) int64 {
	sizeStr = strings.ToUpper(strings.TrimSpace(sizeStr))
	sizeStr = strings.TrimSuffix(sizeStr, "B")

	var multiplier int64 = 1
	if strings.HasSuffix(sizeStr, "M") {
		multiplier = 1
		sizeStr = strings.TrimSuffix(sizeStr, "M")
	} else if strings.HasSuffix(sizeStr, "G") {
		multiplier = 1024
		sizeStr = strings.TrimSuffix(sizeStr, "G")
	} else if strings.HasSuffix(sizeStr, "T") {
		multiplier = 1024 * 1024
		sizeStr = strings.TrimSuffix(sizeStr, "T")
	}

	if value, err := strconv.ParseInt(sizeStr, 10, 64); err == nil {
		return value * multiplier
	}

	return 0
}
