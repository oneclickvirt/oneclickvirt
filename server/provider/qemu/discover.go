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

	// 初始化防火墙管理器用于端口发现
	fwMgr := firewall.NewManager(p.sshClient, NFTTableName, InternalSubnet)
	rulesByIP := fwMgr.DiscoverAllDNATRules()

	var discovered []provider.DiscoveredInstance
	targets := []struct {
		uri          string
		instanceType string
	}{{"qemu:///system", "vm"}, {"lxc:///", "container"}}
	listSuccess := false
	for _, target := range targets {
		output, err := p.sshClient.Execute("export LC_ALL=C; " + qemuListDomainNamesCommand(target.uri))
		if err != nil {
			continue
		}
		listSuccess = true
		for _, rawName := range strings.Split(strings.TrimSpace(output), "\n") {
			name := strings.TrimSpace(rawName)
			if name == "" {
				continue
			}
			inst := provider.DiscoveredInstance{Name: name, InstanceType: target.instanceType}
			info, err := p.sshClient.Execute(fmt.Sprintf("LC_ALL=C virsh -c %s dominfo %s 2>/dev/null", shellSingleQuote(target.uri), shellSingleQuote(name)))
			if err != nil {
				continue
			}
			for _, line := range strings.Split(info, "\n") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) != 2 {
					continue
				}
				key, value := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
				switch key {
				case "UUID":
					inst.UUID = value
				case "State":
					inst.Status = mapVirshStatus(value)
				case "CPU(s)":
					inst.CPU, _ = strconv.Atoi(value)
				case "Max memory":
					if memKB, parseErr := parseKiBValue(value); parseErr == nil {
						inst.Memory = memKB / 1024
					}
				}
			}
			if inst.UUID != "" {
				inst.ProviderInstanceID = inst.UUID
			} else {
				inst.ProviderInstanceID = inst.Name
			}
			diskOutput, diskErr := p.sshClient.Execute(fmt.Sprintf(
				"virsh -c %s domblkinfo %s $(virsh -c %s domblklist %s 2>/dev/null | awk 'NR>2 && $2!=\"\"{print $2; exit}') 2>/dev/null | grep 'Capacity' | awk '{print $2}'",
				shellSingleQuote(target.uri), shellSingleQuote(name), shellSingleQuote(target.uri), shellSingleQuote(name)))
			if diskErr == nil {
				if diskBytes, parseErr := strconv.ParseInt(strings.TrimSpace(diskOutput), 10, 64); parseErr == nil {
					inst.Disk = diskBytes / (1024 * 1024)
				}
			}
			inst.PrivateIP = p.getVMIPAddress(ctx, name)
			if inst.PrivateIP != "" {
				for _, rule := range rulesByIP[inst.PrivateIP] {
					inst.PortMappings = append(inst.PortMappings, provider.DiscoveredPortMapping{HostPort: rule.HostPort, GuestPort: rule.GuestPort, Protocol: rule.Protocol, IsSSH: rule.IsSSH, MappingMethod: "iptables"})
					if rule.IsSSH {
						inst.SSHPort = rule.HostPort
					} else {
						inst.ExtraPorts = append(inst.ExtraPorts, rule.HostPort)
					}
				}
			}
			discovered = append(discovered, inst)
		}
	}
	if !listSuccess {
		return nil, fmt.Errorf("failed to list QEMU VMs and LXC containers")
	}

	global.APP_LOG.Info("QEMU虚拟机发现完成",
		zap.Int("count", len(discovered)),
		zap.String("provider", p.config.Name))

	return discovered, nil
}
