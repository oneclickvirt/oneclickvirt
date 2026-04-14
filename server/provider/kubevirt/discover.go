package kubevirt

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/provider/firewall"

	"go.uber.org/zap"
)

// DiscoverInstances 发现所有KubeVirt虚拟机
func (p *KubeVirtProvider) DiscoverInstances(ctx context.Context) ([]provider.DiscoveredInstance, error) {
	if !p.connected {
		return nil, fmt.Errorf("not connected")
	}
	if p.sshClient == nil {
		return nil, fmt.Errorf("SSH client not initialized")
	}

	global.APP_LOG.Debug("开始发现KubeVirt虚拟机", zap.String("provider", p.config.Name))

	output, err := p.sshClient.Execute(fmt.Sprintf(
		"kubectl get vm -n %s -o json 2>/dev/null", Namespace))
	if err != nil {
		return nil, fmt.Errorf("failed to list VMs: %w", err)
	}

	var vmList struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
				UID  string `json:"uid"`
			} `json:"metadata"`
			Spec struct {
				Running  *bool `json:"running"`
				Template struct {
					Spec struct {
						Domain struct {
							CPU struct {
								Cores int `json:"cores"`
							} `json:"cpu"`
							Resources struct {
								Requests struct {
									Memory string `json:"memory"`
								} `json:"requests"`
							} `json:"resources"`
						} `json:"domain"`
						Volumes []struct {
							Name                  string `json:"name"`
							PersistentVolumeClaim *struct {
								ClaimName string `json:"claimName"`
							} `json:"persistentVolumeClaim,omitempty"`
						} `json:"volumes"`
					} `json:"spec"`
				} `json:"template"`
			} `json:"spec"`
			Status struct {
				PrintableStatus string `json:"printableStatus"`
			} `json:"status"`
		} `json:"items"`
	}

	if err := json.Unmarshal([]byte(output), &vmList); err != nil {
		return nil, fmt.Errorf("failed to parse VM list: %w", err)
	}

	// 初始化防火墙管理器用于发现DNAT规则
	fwMgr := firewall.NewManager(p.sshClient, NFTTableName, "")
	fwMgr.DetectBackend(FWBackendFile)

	var discovered []provider.DiscoveredInstance

	for _, item := range vmList.Items {
		inst := provider.DiscoveredInstance{
			UUID:         item.Metadata.UID,
			Name:         item.Metadata.Name,
			InstanceType: "vm",
			Status:       mapKubeVirtStatus(item.Status.PrintableStatus),
			CPU:          item.Spec.Template.Spec.Domain.CPU.Cores,
		}

		memStr := item.Spec.Template.Spec.Domain.Resources.Requests.Memory
		if memMB := parseMemoryString(memStr); memMB > 0 {
			inst.Memory = memMB
		}

		for _, vol := range item.Spec.Template.Spec.Volumes {
			if vol.PersistentVolumeClaim != nil {
				pvcName := vol.PersistentVolumeClaim.ClaimName
				sizeOutput, err := p.sshClient.Execute(fmt.Sprintf(
					"kubectl get pvc '%s' -n %s -o jsonpath='{.spec.resources.requests.storage}' 2>/dev/null", pvcName, Namespace))
				if err == nil {
					if diskMB := parseStorageString(strings.TrimSpace(sizeOutput)); diskMB > 0 {
						inst.Disk = diskMB
					}
				}
				break
			}
		}

		// 获取端口映射 - 优先从NodePort Service发现，再补充防火墙DNAT规则
		inst.PortMappings = p.discoverPortMappings(ctx, item.Metadata.Name)

		// 补充通过防火墙发现的DNAT规则
		fwRules := fwMgr.DiscoverDNATRules(item.Metadata.Name)
		existingPorts := make(map[int]bool)
		for _, pm := range inst.PortMappings {
			existingPorts[pm.HostPort] = true
		}
		for _, rule := range fwRules {
			if !existingPorts[rule.HostPort] {
				inst.PortMappings = append(inst.PortMappings, provider.DiscoveredPortMapping{
					HostPort:  rule.HostPort,
					GuestPort: rule.GuestPort,
					Protocol:  rule.Protocol,
					IsSSH:     rule.IsSSH,
				})
			}
		}

		for _, pm := range inst.PortMappings {
			if pm.IsSSH {
				inst.SSHPort = pm.HostPort
			} else {
				inst.ExtraPorts = append(inst.ExtraPorts, pm.HostPort)
			}
		}

		discovered = append(discovered, inst)
	}

	global.APP_LOG.Info("KubeVirt虚拟机发现完成",
		zap.Int("count", len(discovered)),
		zap.String("provider", p.config.Name))

	return discovered, nil
}

// discoverPortMappings 发现VM的端口映射（NodePort Service）
func (p *KubeVirtProvider) discoverPortMappings(ctx context.Context, vmName string) []provider.DiscoveredPortMapping {
	output, err := p.sshClient.Execute(fmt.Sprintf(
		"kubectl get svc -n %s -o json 2>/dev/null",
		Namespace))
	if err != nil {
		return nil
	}

	var svcList struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Ports []struct {
					Name       string `json:"name"`
					NodePort   int    `json:"nodePort"`
					TargetPort int    `json:"targetPort"`
					Protocol   string `json:"protocol"`
				} `json:"ports"`
			} `json:"spec"`
		} `json:"items"`
	}

	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &svcList); err != nil {
		return nil
	}

	var mappings []provider.DiscoveredPortMapping
	for _, svc := range svcList.Items {
		if !strings.HasPrefix(svc.Metadata.Name, vmName) {
			continue
		}

		for _, port := range svc.Spec.Ports {
			pm := provider.DiscoveredPortMapping{
				HostPort:  port.NodePort,
				GuestPort: port.TargetPort,
				Protocol:  strings.ToLower(port.Protocol),
			}
			if port.TargetPort == 22 || strings.Contains(port.Name, "ssh") {
				pm.IsSSH = true
			}
			mappings = append(mappings, pm)
		}
	}

	return mappings
}

// parseMemoryString 解析内存字符串 (如 "1Gi", "512Mi", "2048M")
func parseMemoryString(memStr string) int64 {
	memStr = strings.TrimSpace(memStr)
	if memStr == "" {
		return 0
	}
	if strings.HasSuffix(memStr, "Gi") {
		if v, err := strconv.ParseFloat(strings.TrimSuffix(memStr, "Gi"), 64); err == nil {
			return int64(v * 1024)
		}
	}
	if strings.HasSuffix(memStr, "Mi") {
		if v, err := strconv.ParseFloat(strings.TrimSuffix(memStr, "Mi"), 64); err == nil {
			return int64(v)
		}
	}
	if strings.HasSuffix(memStr, "G") {
		if v, err := strconv.ParseFloat(strings.TrimSuffix(memStr, "G"), 64); err == nil {
			return int64(v * 1024)
		}
	}
	if strings.HasSuffix(memStr, "M") {
		if v, err := strconv.ParseFloat(strings.TrimSuffix(memStr, "M"), 64); err == nil {
			return int64(v)
		}
	}
	return 0
}

// parseStorageString 解析存储字符串 (如 "10Gi", "20G")
func parseStorageString(sizeStr string) int64 {
	sizeStr = strings.TrimSpace(sizeStr)
	if sizeStr == "" {
		return 0
	}
	if strings.HasSuffix(sizeStr, "Gi") {
		if v, err := strconv.ParseFloat(strings.TrimSuffix(sizeStr, "Gi"), 64); err == nil {
			return int64(v * 1024)
		}
	}
	if strings.HasSuffix(sizeStr, "Ti") {
		if v, err := strconv.ParseFloat(strings.TrimSuffix(sizeStr, "Ti"), 64); err == nil {
			return int64(v * 1024 * 1024)
		}
	}
	if strings.HasSuffix(sizeStr, "G") {
		if v, err := strconv.ParseFloat(strings.TrimSuffix(sizeStr, "G"), 64); err == nil {
			return int64(v * 1024)
		}
	}
	return 0
}
