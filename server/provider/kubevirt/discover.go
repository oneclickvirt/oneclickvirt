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
	"oneclickvirt/utils"

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
								Cores   int `json:"cores"`
								Sockets int `json:"sockets"`
								Threads int `json:"threads"`
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
							DataVolume *struct {
								Name string `json:"name"`
							} `json:"dataVolume,omitempty"`
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

	// 一次性获取所有 Service，避免 N+1 问题（每个VM不再单独查询）
	allSvcs := p.fetchAllServices()

	var discovered []provider.DiscoveredInstance

	for _, item := range vmList.Items {
		inst := provider.DiscoveredInstance{
			UUID:               item.Metadata.UID,
			ProviderInstanceID: item.Metadata.Name,
			Name:               item.Metadata.Name,
			InstanceType:       "vm",
			Status:             mapKubeVirtStatus(item.Status.PrintableStatus),
			CPU:                item.Spec.Template.Spec.Domain.CPU.Cores,
		}
		if item.Spec.Template.Spec.Domain.CPU.Sockets > 0 {
			inst.CPU *= item.Spec.Template.Spec.Domain.CPU.Sockets
		}
		if item.Spec.Template.Spec.Domain.CPU.Threads > 0 {
			inst.CPU *= item.Spec.Template.Spec.Domain.CPU.Threads
		}
		if inst.CPU <= 0 {
			inst.CPU = 1
		}

		memStr := item.Spec.Template.Spec.Domain.Resources.Requests.Memory
		if memMB := parseMemoryString(memStr); memMB > 0 {
			inst.Memory = memMB
		}

		seenPVCs := make(map[string]bool)
		for _, vol := range item.Spec.Template.Spec.Volumes {
			pvcName := ""
			if vol.PersistentVolumeClaim != nil {
				pvcName = vol.PersistentVolumeClaim.ClaimName
			} else if vol.DataVolume != nil {
				pvcName = vol.DataVolume.Name
			}
			if pvcName != "" && !seenPVCs[pvcName] {
				seenPVCs[pvcName] = true
				sizeOutput, err := p.sshClient.Execute(fmt.Sprintf(
					"kubectl get pvc %s -n %s -o jsonpath='{.spec.resources.requests.storage}' 2>/dev/null", shellSingleQuote(pvcName), shellSingleQuote(Namespace)))
				if err == nil {
					if diskMB := parseStorageString(strings.TrimSpace(sizeOutput)); diskMB > 0 {
						inst.Disk += diskMB
					}
				}
			}
		}

		// 获取端口映射 - 从预取的 Service 列表中过滤，再补充防火墙DNAT规则
		inst.PortMappings = filterPortMappings(allSvcs, item.Metadata.Name)

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

	containers, err := p.discoverContainerDeployments(allSvcs)
	if err != nil {
		global.APP_LOG.Warn("KubeVirt容器Deployment发现失败", zap.Error(err))
	} else {
		discovered = append(discovered, containers...)
	}

	global.APP_LOG.Info("KubeVirt虚拟机发现完成",
		zap.Int("count", len(discovered)),
		zap.String("provider", p.config.Name))

	return discovered, nil
}

func (p *KubeVirtProvider) discoverContainerDeployments(allSvcs []svcItem) ([]provider.DiscoveredInstance, error) {
	output, err := p.sshClient.Execute(fmt.Sprintf(
		"kubectl get deploy -n %s -l oneclickvirt.io/type=container -o json 2>/dev/null", Namespace))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(output) == "" {
		return nil, nil
	}
	var deployments struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
				UID  string `json:"uid"`
			} `json:"metadata"`
			Spec struct {
				Replicas *int `json:"replicas"`
				Template struct {
					Spec struct {
						Containers []struct {
							Image     string `json:"image"`
							Resources struct {
								Limits   map[string]string `json:"limits"`
								Requests map[string]string `json:"requests"`
							} `json:"resources"`
						} `json:"containers"`
					} `json:"spec"`
				} `json:"template"`
			} `json:"spec"`
			Status struct {
				ReadyReplicas int `json:"readyReplicas"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(output), &deployments); err != nil {
		return nil, fmt.Errorf("failed to parse container deployments: %w", err)
	}

	result := make([]provider.DiscoveredInstance, 0, len(deployments.Items))
	for _, deployment := range deployments.Items {
		status := "stopped"
		desiredReplicas := 1
		if deployment.Spec.Replicas != nil {
			desiredReplicas = *deployment.Spec.Replicas
		}
		if deployment.Status.ReadyReplicas > 0 {
			status = "running"
		} else if desiredReplicas > 0 {
			status = "pending"
		}
		instance := provider.DiscoveredInstance{
			UUID:               deployment.Metadata.UID,
			ProviderInstanceID: deployment.Metadata.Name,
			Name:               deployment.Metadata.Name,
			Status:             status,
			InstanceType:       "container",
			PortMappings:       filterPortMappings(allSvcs, deployment.Metadata.Name),
			RawData:            deployment,
		}
		images := make([]string, 0, len(deployment.Spec.Template.Spec.Containers))
		for _, container := range deployment.Spec.Template.Spec.Containers {
			if container.Image != "" {
				images = append(images, container.Image)
			}
			cpuValue := container.Resources.Limits["cpu"]
			if cpuValue == "" {
				cpuValue = container.Resources.Requests["cpu"]
			}
			memoryValue := container.Resources.Limits["memory"]
			if memoryValue == "" {
				memoryValue = container.Resources.Requests["memory"]
			}
			instance.CPU += parseCPUString(cpuValue)
			instance.Memory += parseMemoryString(memoryValue)
		}
		instance.Image = strings.Join(images, ",")
		instance.OSType = utils.DetectOSTypeFromText(instance.Image)
		for _, mapping := range instance.PortMappings {
			if mapping.IsSSH {
				instance.SSHPort = mapping.HostPort
			} else {
				instance.ExtraPorts = append(instance.ExtraPorts, mapping.HostPort)
			}
		}
		result = append(result, instance)
	}
	return result, nil
}

// svcItem 内部用于保存解析后的 Service 条目
type svcItem struct {
	Name       string
	TargetName string
	Ports      []svcPort
}

type svcPort struct {
	Name       string
	NodePort   int
	TargetPort int
	Protocol   string
}

// fetchAllServices 一次性获取命名空间内所有 Service 并解析，供 filterPortMappings 使用
func (p *KubeVirtProvider) fetchAllServices() []svcItem {
	output, err := p.sshClient.Execute(fmt.Sprintf(
		"kubectl get svc -n %s -o json 2>/dev/null", Namespace))
	if err != nil {
		return nil
	}

	var raw struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Selector map[string]string `json:"selector"`
				Ports    []struct {
					Name       string          `json:"name"`
					NodePort   int             `json:"nodePort"`
					TargetPort json.RawMessage `json:"targetPort"`
					Protocol   string          `json:"protocol"`
				} `json:"ports"`
			} `json:"spec"`
		} `json:"items"`
	}

	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &raw); err != nil {
		return nil
	}

	result := make([]svcItem, 0, len(raw.Items))
	for _, it := range raw.Items {
		targetName := it.Spec.Selector["kubevirt.io/domain"]
		if targetName == "" {
			targetName = it.Spec.Selector["oneclickvirt.io/instance"]
		}
		if targetName == "" {
			targetName = it.Spec.Selector["app"]
		}
		s := svcItem{Name: it.Metadata.Name, TargetName: targetName}
		for _, rawPort := range it.Spec.Ports {
			targetPort := 0
			if err := json.Unmarshal(rawPort.TargetPort, &targetPort); err != nil {
				var targetName string
				if json.Unmarshal(rawPort.TargetPort, &targetName) == nil && strings.Contains(strings.ToLower(targetName), "ssh") {
					targetPort = 22
				}
			}
			s.Ports = append(s.Ports, svcPort{Name: rawPort.Name, NodePort: rawPort.NodePort, TargetPort: targetPort, Protocol: rawPort.Protocol})
		}
		result = append(result, s)
	}
	return result
}

// filterPortMappings 从预取的 Service 列表中过滤出属于 vmName 的端口映射
func filterPortMappings(allSvcs []svcItem, vmName string) []provider.DiscoveredPortMapping {
	var mappings []provider.DiscoveredPortMapping
	for _, svc := range allSvcs {
		if svc.TargetName != "" && svc.TargetName != vmName {
			continue
		}
		if svc.TargetName == "" && svc.Name != vmName && !strings.HasPrefix(svc.Name, vmName+"-") {
			continue
		}
		for _, port := range svc.Ports {
			if port.NodePort <= 0 || port.NodePort > 65535 || port.TargetPort <= 0 || port.TargetPort > 65535 {
				continue
			}
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

// discoverPortMappings 发现VM的端口映射（NodePort Service）- 保留供单VM查询使用
func (p *KubeVirtProvider) discoverPortMappings(ctx context.Context, vmName string) []provider.DiscoveredPortMapping {
	return filterPortMappings(p.fetchAllServices(), vmName)
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
	if strings.HasSuffix(memStr, "Ti") {
		if v, err := strconv.ParseFloat(strings.TrimSuffix(memStr, "Ti"), 64); err == nil {
			return int64(v * 1024 * 1024)
		}
	}
	if strings.HasSuffix(memStr, "Mi") {
		if v, err := strconv.ParseFloat(strings.TrimSuffix(memStr, "Mi"), 64); err == nil {
			return int64(v)
		}
	}
	if strings.HasSuffix(memStr, "Ki") {
		if v, err := strconv.ParseFloat(strings.TrimSuffix(memStr, "Ki"), 64); err == nil {
			return int64(v / 1024)
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
	if strings.HasSuffix(memStr, "T") {
		if v, err := strconv.ParseFloat(strings.TrimSuffix(memStr, "T"), 64); err == nil {
			return int64(v * 1024 * 1024)
		}
	}
	return 0
}

func parseCPUString(cpu string) int {
	cpu = strings.TrimSpace(cpu)
	if cpu == "" {
		return 0
	}
	if strings.HasSuffix(cpu, "m") {
		value, err := strconv.ParseFloat(strings.TrimSuffix(cpu, "m"), 64)
		if err != nil || value <= 0 {
			return 0
		}
		return int((value + 999) / 1000)
	}
	value, err := strconv.ParseFloat(cpu, 64)
	if err != nil || value <= 0 {
		return 0
	}
	cores := int(value)
	if float64(cores) < value {
		cores++
	}
	return cores
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
	if strings.HasSuffix(sizeStr, "Mi") {
		if v, err := strconv.ParseFloat(strings.TrimSuffix(sizeStr, "Mi"), 64); err == nil {
			return int64(v)
		}
	}
	if strings.HasSuffix(sizeStr, "Ki") {
		if v, err := strconv.ParseFloat(strings.TrimSuffix(sizeStr, "Ki"), 64); err == nil {
			return int64(v / 1024)
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
	if strings.HasSuffix(sizeStr, "M") {
		if v, err := strconv.ParseFloat(strings.TrimSuffix(sizeStr, "M"), 64); err == nil {
			return int64(v)
		}
	}
	if strings.HasSuffix(sizeStr, "T") {
		if v, err := strconv.ParseFloat(strings.TrimSuffix(sizeStr, "T"), 64); err == nil {
			return int64(v * 1024 * 1024)
		}
	}
	return 0
}
