package qemu

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/provider/firewall"

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

	// 初始化防火墙管理器用于端口发现
	fwMgr := firewall.NewManager(p.sshClient, NFTTableName, InternalSubnet)
	fwMgr.DetectBackend(FWBackendFile)

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
		info, err := p.sshClient.Execute(fmt.Sprintf("virsh dominfo %s 2>/dev/null", shellSingleQuote(name)))
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
					inst.Memory = memKB / 1024
				}
			}
		}

		// 获取磁盘大小
		diskOutput, err := p.sshClient.Execute(fmt.Sprintf(
			"virsh domblkinfo %s $(virsh domblklist %s 2>/dev/null | awk 'NR>2 && $2!=\"\"{print $2; exit}') 2>/dev/null | grep 'Capacity' | awk '{print $2}'", shellSingleQuote(name), shellSingleQuote(name)))
		if err == nil {
			if diskBytes, err := strconv.ParseInt(strings.TrimSpace(diskOutput), 10, 64); err == nil {
				inst.Disk = diskBytes / (1024 * 1024)
			}
		}

		// 获取IP
		inst.PrivateIP = p.getVMIPAddress(ctx, name)

		// 使用防火墙管理器发现端口映射
		if inst.PrivateIP != "" {
			rules := fwMgr.DiscoverDNATRules(inst.PrivateIP)
			for _, r := range rules {
				pm := provider.DiscoveredPortMapping{
					HostPort:  r.HostPort,
					GuestPort: r.GuestPort,
					Protocol:  r.Protocol,
					IsSSH:     r.IsSSH,
				}
				inst.PortMappings = append(inst.PortMappings, pm)
				if r.IsSSH {
					inst.SSHPort = r.HostPort
				} else {
					inst.ExtraPorts = append(inst.ExtraPorts, r.HostPort)
				}
			}
		}

		// 获取操作系统信息
		osOutput, err := p.sshClient.Execute(fmt.Sprintf(
			"virsh dumpxml %s 2>/dev/null | grep -oP '<type[^>]*>\\K[^<]+'", shellSingleQuote(name)))
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
