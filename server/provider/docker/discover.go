package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/provider"

	"go.uber.org/zap"
)

// DiscoverInstances 发现Docker provider上的所有容器
func (d *DockerProvider) DiscoverInstances(ctx context.Context) ([]provider.DiscoveredInstance, error) {
	if !d.connected {
		return nil, fmt.Errorf("not connected")
	}

	global.APP_LOG.Info("开始发现Docker容器", zap.String("provider", d.config.Name))

	if d.sshClient == nil {
		return nil, fmt.Errorf("SSH client not initialized")
	}

	// 使用docker inspect命令获取所有容器的详细信息
	cmd := "docker ps -a --format '{{.ID}}' | xargs -r docker inspect"
	output, err := d.sshClient.Execute(cmd)
	if err != nil {
		return nil, fmt.Errorf("执行SSH命令失败: %w", err)
	}

	if strings.TrimSpace(output) == "" {
		global.APP_LOG.Info("未发现任何Docker容器", zap.String("provider", d.config.Name))
		return []provider.DiscoveredInstance{}, nil
	}

	var containers []struct {
		ID    string `json:"Id"`
		Name  string `json:"Name"`
		State struct {
			Status  string `json:"Status"`
			Running bool   `json:"Running"`
			Paused  bool   `json:"Paused"`
		} `json:"State"`
		Config struct {
			Image    string            `json:"Image"`
			Hostname string            `json:"Hostname"`
			Env      []string          `json:"Env"`
			Labels   map[string]string `json:"Labels"`
		} `json:"Config"`
		HostConfig struct {
			NanoCpus     int64 `json:"NanoCpus"`
			Memory       int64 `json:"Memory"`
			PortBindings map[string][]struct {
				HostIP   string `json:"HostIp"`
				HostPort string `json:"HostPort"`
			} `json:"PortBindings"`
		} `json:"HostConfig"`
		NetworkSettings struct {
			Networks map[string]struct {
				IPAddress         string `json:"IPAddress"`
				MacAddress        string `json:"MacAddress"`
				Gateway           string `json:"Gateway"`
				IPv6Gateway       string `json:"IPv6Gateway"`
				GlobalIPv6Address string `json:"GlobalIPv6Address"`
			} `json:"Networks"`
			Ports map[string][]struct {
				HostIP   string `json:"HostIp"`
				HostPort string `json:"HostPort"`
			} `json:"Ports"`
		} `json:"NetworkSettings"`
	}

	if err := json.Unmarshal([]byte(output), &containers); err != nil {
		return nil, fmt.Errorf("解析JSON失败: %w", err)
	}

	var discoveredInstances []provider.DiscoveredInstance

	for _, container := range containers {
		discovered := provider.DiscoveredInstance{
			UUID:         container.ID,
			Name:         strings.TrimPrefix(container.Name, "/"),
			Status:       d.mapDockerStatus(container.State.Status, container.State.Running, container.State.Paused),
			InstanceType: "container", // Docker只支持容器
			Image:        container.Config.Image,
			RawData:      container,
		}

		// 解析CPU配置（NanoCPUs转换为核心数）
		if container.HostConfig.NanoCpus > 0 {
			discovered.CPU = int(container.HostConfig.NanoCpus / 1000000000)
		}
		if discovered.CPU == 0 {
			discovered.CPU = 1
		}

		// 解析内存配置（字节转MB）
		if container.HostConfig.Memory > 0 {
			discovered.Memory = container.HostConfig.Memory / 1024 / 1024
		}
		if discovered.Memory == 0 {
			discovered.Memory = 512
		}

		// Docker容器磁盘大小默认值（需要通过其他方式获取准确值）
		discovered.Disk = 10240

		// 解析网络信息
		var extraPorts []int
		for netName, netInfo := range container.NetworkSettings.Networks {
			if netName == "none" {
				continue
			}

			if discovered.PrivateIP == "" {
				discovered.PrivateIP = netInfo.IPAddress
			}
			if discovered.IPv6Address == "" && netInfo.GlobalIPv6Address != "" {
				discovered.IPv6Address = netInfo.GlobalIPv6Address
			}
			if discovered.MACAddress == "" {
				discovered.MACAddress = netInfo.MacAddress
			}
		}

		// 解析端口映射
		sshPortFound := false
		for containerPort, bindings := range container.NetworkSettings.Ports {
			if len(bindings) > 0 {
				portNum := d.parsePortNumber(containerPort)
				if portNum > 0 {
					// 检查是否为SSH端口（22）
					if !sshPortFound && strings.HasPrefix(containerPort, "22/") {
						if hostPort, err := strconv.Atoi(bindings[0].HostPort); err == nil {
							discovered.SSHPort = hostPort
							sshPortFound = true
						}
					}
					// 收集其他端口
					if hostPort, err := strconv.Atoi(bindings[0].HostPort); err == nil {
						extraPorts = append(extraPorts, hostPort)
					}
				}
			}
		}

		if !sshPortFound {
			discovered.SSHPort = 22
		}
		discovered.ExtraPorts = extraPorts

		// 尝试从环境变量或标签中提取OS类型
		discovered.OSType = d.extractOSType(container.Config.Env, container.Config.Labels)

		discoveredInstances = append(discoveredInstances, discovered)
	}

	global.APP_LOG.Info("Docker容器发现完成",
		zap.String("provider", d.config.Name),
		zap.Int("count", len(discoveredInstances)))

	return discoveredInstances, nil
}

// 辅助函数
func (d *DockerProvider) mapDockerStatus(status string, running, paused bool) string {
	if paused {
		return "paused"
	}
	if running {
		return "running"
	}
	switch strings.ToLower(status) {
	case "exited":
		return "stopped"
	case "created":
		return "stopped"
	case "dead":
		return "failed"
	default:
		return status
	}
}

func (d *DockerProvider) parsePortNumber(portStr string) int {
	// 格式如 "22/tcp" 或 "80/udp"
	parts := strings.Split(portStr, "/")
	if len(parts) > 0 {
		if port, err := strconv.Atoi(parts[0]); err == nil {
			return port
		}
	}
	return 0
}

func (d *DockerProvider) extractOSType(envVars []string, labels map[string]string) string {
	// 尝试从标签中提取
	if osType, ok := labels["os"]; ok {
		return osType
	}
	if osType, ok := labels["org.opencontainers.image.os"]; ok {
		return osType
	}

	// 尝试从环境变量中提取
	for _, env := range envVars {
		if strings.HasPrefix(env, "OS=") {
			return strings.TrimPrefix(env, "OS=")
		}
	}

	// 默认假设为Linux
	return "linux"
}
