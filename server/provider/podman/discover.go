package podman

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

// sshDiscoverInstances 发现Podman provider上的所有容器
func (p *PodmanProvider) sshDiscoverInstances(ctx context.Context) ([]provider.DiscoveredInstance, error) {
	global.APP_LOG.Debug("开始发现Podman容器", zap.String("provider", p.config.Name))

	cmd := fmt.Sprintf("%s ps -a --format '{{.ID}}' | xargs -r %s inspect", cliName, cliName)
	output, err := p.sshClient.Execute(cmd)
	if err != nil {
		return nil, fmt.Errorf("执行SSH命令失败: %w", err)
	}

	if strings.TrimSpace(output) == "" {
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
			Status:       mapContainerStatus(container.State.Status, container.State.Running, container.State.Paused),
			InstanceType: "container",
			Image:        container.Config.Image,
			RawData:      container,
		}

		if container.HostConfig.NanoCpus > 0 {
			discovered.CPU = int(container.HostConfig.NanoCpus / 1000000000)
		}
		if discovered.CPU == 0 {
			discovered.CPU = 1
		}

		if container.HostConfig.Memory > 0 {
			discovered.Memory = container.HostConfig.Memory / 1024 / 1024
		}
		if discovered.Memory == 0 {
			discovered.Memory = 512
		}

		discovered.Disk = 10240

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

		sshPortFound := false
		for containerPort, bindings := range container.NetworkSettings.Ports {
			if len(bindings) > 0 {
				portNum := parsePortNumber(containerPort)
				if portNum > 0 {
					if !sshPortFound && strings.HasPrefix(containerPort, "22/") {
						if hostPort, err := strconv.Atoi(bindings[0].HostPort); err == nil {
							discovered.SSHPort = hostPort
							sshPortFound = true
						}
					}
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
		discovered.OSType = extractOSType(container.Config.Env, container.Config.Labels)

		discoveredInstances = append(discoveredInstances, discovered)
	}

	global.APP_LOG.Debug("Podman容器发现完成",
		zap.String("provider", p.config.Name),
		zap.Int("count", len(discoveredInstances)))

	return discoveredInstances, nil
}

func mapContainerStatus(status string, running, paused bool) string {
	if paused {
		return "paused"
	}
	if running {
		return "running"
	}
	switch strings.ToLower(status) {
	case "exited", "created":
		return "stopped"
	case "dead":
		return "failed"
	default:
		return status
	}
}

func parsePortNumber(portStr string) int {
	parts := strings.Split(portStr, "/")
	if len(parts) > 0 {
		if port, err := strconv.Atoi(parts[0]); err == nil {
			return port
		}
	}
	return 0
}

func extractOSType(envVars []string, labels map[string]string) string {
	if osType, ok := labels["os"]; ok {
		return osType
	}
	if osType, ok := labels["org.opencontainers.image.os"]; ok {
		return osType
	}
	for _, env := range envVars {
		if strings.HasPrefix(env, "OS=") {
			return strings.TrimPrefix(env, "OS=")
		}
	}
	return "linux"
}
