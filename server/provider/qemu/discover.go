package qemu

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/provider"

	"go.uber.org/zap"
)

// DiscoverInstances 发现宿主机上所有QEMU虚拟机
func (p *QEMUProvider) DiscoverInstances(ctx context.Context) ([]provider.DiscoveredInstance, error) {
	if !p.connected {
		return nil, fmt.Errorf("not connected")
	}
	if p.sshClient == nil {
		return nil, fmt.Errorf("SSH client not initialized")
	}

	global.APP_LOG.Debug("开始发现QEMU虚拟机", zap.String("provider", p.config.Name))

	// 获取所有VM名称
	output, err := p.sshClient.Execute("virsh list --all --name 2>/dev/null | grep -v '^$'")
	if err != nil {
		return nil, fmt.Errorf("failed to list VMs: %w", err)
	}

	var discovered []provider.DiscoveredInstance
	names := strings.Split(strings.TrimSpace(output), "\n")

	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		inst := provider.DiscoveredInstance{
			Name:         name,
			InstanceType: "vm",
		}

		// 获取dominfo
		info, err := p.sshClient.Execute(fmt.Sprintf("virsh dominfo '%s' 2>/dev/null", name))
		if err != nil {
			continue
		}

		for _, line := range strings.Split(info, "\n") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) != 2 {
				continue
			}
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])

			switch key {
			case "UUID":
				inst.UUID = value
			case "State":
				inst.Status = mapVirshStatus(value)
			case "CPU(s)":
				if cpu, err := strconv.Atoi(value); err == nil {
					inst.CPU = cpu
				}
			case "Max memory":
				if memKB, err := parseKiBValue(value); err == nil {
					inst.Memory = memKB / 1024 // KB → MB
				}
			}
		}

		// 获取磁盘大小
		diskOutput, err := p.sshClient.Execute(fmt.Sprintf(
			"virsh domblkinfo '%s' $(virsh domblklist '%s' 2>/dev/null | awk 'NR>2 && $2!=\"\"{print $2; exit}') 2>/dev/null | grep 'Capacity' | awk '{print $2}'", name, name))
		if err == nil {
			if diskBytes, err := strconv.ParseInt(strings.TrimSpace(diskOutput), 10, 64); err == nil {
				inst.Disk = diskBytes / (1024 * 1024) // bytes → MB
			}
		}

		// 获取IP
		inst.PrivateIP = p.getVMIPAddress(ctx, name)

		// 获取端口映射 (从iptables DNAT规则)
		if inst.PrivateIP != "" {
			inst.PortMappings = p.discoverPortMappings(ctx, inst.PrivateIP)
			for _, pm := range inst.PortMappings {
				if pm.IsSSH {
					inst.SSHPort = pm.HostPort
				} else {
					inst.ExtraPorts = append(inst.ExtraPorts, pm.HostPort)
				}
			}
		}

		// 获取操作系统信息
		osOutput, err := p.sshClient.Execute(fmt.Sprintf(
			"virsh dumpxml '%s' 2>/dev/null | grep -oP '<type[^>]*>\\K[^<]+'", name))
		if err == nil {
			inst.OSType = strings.TrimSpace(osOutput)
		}

		discovered = append(discovered, inst)
	}

	global.APP_LOG.Info("QEMU虚拟机发现完成",
		zap.Int("count", len(discovered)),
		zap.String("provider", p.config.Name))

	return discovered, nil
}

// discoverPortMappings 从iptables DNAT规则发现端口映射
func (p *QEMUProvider) discoverPortMappings(ctx context.Context, vmIP string) []provider.DiscoveredPortMapping {
	// 获取指向该IP的所有DNAT规则
	output, err := p.sshClient.Execute(fmt.Sprintf(
		"iptables -t nat -L PREROUTING -n 2>/dev/null | grep 'DNAT' | grep '%s'", vmIP))
	if err != nil {
		return nil
	}

	var mappings []provider.DiscoveredPortMapping
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		pm := parseIptablesDNATRule(line, vmIP)
		if pm != nil {
			mappings = append(mappings, *pm)
		}
	}
	return mappings
}

// parseIptablesDNATRule 解析 iptables DNAT 规则
// 格式: DNAT tcp -- 0.0.0.0/0 0.0.0.0/0 tcp dpt:10022 to:192.168.122.2:22
func parseIptablesDNATRule(line, vmIP string) *provider.DiscoveredPortMapping {
	if !strings.Contains(line, "DNAT") {
		return nil
	}

	pm := &provider.DiscoveredPortMapping{
		Protocol: "tcp",
	}

	// 提取协议
	if strings.Contains(line, "udp") {
		pm.Protocol = "udp"
	}

	// 提取宿主机端口 (dpt:xxxxx)
	if idx := strings.Index(line, "dpt:"); idx >= 0 {
		portStr := line[idx+4:]
		portStr = strings.Fields(portStr)[0]
		if port, err := strconv.Atoi(portStr); err == nil {
			pm.HostPort = port
		}
	}

	// 提取目标端口 (to:IP:port)
	if idx := strings.Index(line, "to:"); idx >= 0 {
		target := line[idx+3:]
		target = strings.Fields(target)[0]
		parts := strings.Split(target, ":")
		if len(parts) == 2 {
			if port, err := strconv.Atoi(parts[1]); err == nil {
				pm.GuestPort = port
			}
		}
	}

	if pm.HostPort == 0 || pm.GuestPort == 0 {
		return nil
	}

	// 检查是否为SSH端口
	if pm.GuestPort == 22 {
		pm.IsSSH = true
	}

	return pm
}
