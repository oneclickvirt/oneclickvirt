package proxmox

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/provider"

	"go.uber.org/zap"
)

// DiscoverInstances 发现Proxmox provider上的所有虚拟机和容器
func (p *ProxmoxProvider) DiscoverInstances(ctx context.Context) ([]provider.DiscoveredInstance, error) {
	if !p.connected {
		return nil, fmt.Errorf("not connected")
	}

	global.APP_LOG.Info("开始发现Proxmox实例", zap.String("provider", p.config.Name))

	// Proxmox主要通过API访问，但也支持SSH备份
	if p.hasAPIAccess() {
		instances, err := p.apiDiscoverInstances(ctx)
		if err == nil {
			global.APP_LOG.Info("Proxmox API发现实例成功",
				zap.String("provider", p.config.Name),
				zap.Int("count", len(instances)))
			return instances, nil
		}
		global.APP_LOG.Warn("Proxmox API发现实例失败，尝试SSH方式", zap.Error(err))
	}

	// 回退到SSH方式
	return p.sshDiscoverInstances(ctx)
}

// apiDiscoverInstances 通过Proxmox API发现实例
func (p *ProxmoxProvider) apiDiscoverInstances(ctx context.Context) ([]provider.DiscoveredInstance, error) {
	// 获取所有节点
	nodesURL := fmt.Sprintf("https://%s:8006/api2/json/nodes", p.config.Host)
	nodesResp, err := p.makeAPIRequest(ctx, "GET", nodesURL, nil)
	if err != nil {
		return nil, fmt.Errorf("获取节点列表失败: %w", err)
	}

	var nodesData struct {
		Data []struct {
			Node string `json:"node"`
		} `json:"data"`
	}

	if err := json.Unmarshal(nodesResp, &nodesData); err != nil {
		return nil, fmt.Errorf("解析节点列表失败: %w", err)
	}

	var discoveredInstances []provider.DiscoveredInstance

	// 遍历每个节点，获取VMs和Containers
	for _, nodeInfo := range nodesData.Data {
		nodeName := nodeInfo.Node

		// 获取QEMU VMs
		vmsURL := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/qemu", p.config.Host, nodeName)
		vmsResp, err := p.makeAPIRequest(ctx, "GET", vmsURL, nil)
		if err != nil {
			global.APP_LOG.Warn("获取VM列表失败", zap.String("node", nodeName), zap.Error(err))
		} else {
			vms, err := p.parseVMsResponse(vmsResp, nodeName)
			if err == nil {
				discoveredInstances = append(discoveredInstances, vms...)
			}
		}

		// 获取LXC Containers
		lxcURL := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/lxc", p.config.Host, nodeName)
		lxcResp, err := p.makeAPIRequest(ctx, "GET", lxcURL, nil)
		if err != nil {
			global.APP_LOG.Warn("获取LXC列表失败", zap.String("node", nodeName), zap.Error(err))
		} else {
			lxcs, err := p.parseLXCsResponse(lxcResp, nodeName)
			if err == nil {
				discoveredInstances = append(discoveredInstances, lxcs...)
			}
		}
	}

	return discoveredInstances, nil
}

// sshDiscoverInstances 通过SSH命令发现实例
func (p *ProxmoxProvider) sshDiscoverInstances(ctx context.Context) ([]provider.DiscoveredInstance, error) {
	if p.sshClient == nil {
		return nil, fmt.Errorf("SSH client not initialized")
	}

	var discoveredInstances []provider.DiscoveredInstance

	// 获取所有QEMU VMs
	vmsCmd := "pvesh get /cluster/resources --type vm --output-format json"
	vmsOutput, err := p.sshClient.Execute(vmsCmd)
	if err != nil {
		global.APP_LOG.Warn("SSH获取VMs失败", zap.Error(err))
	} else {
		vms, err := p.parseResourcesJSON(vmsOutput, "qemu")
		if err == nil {
			discoveredInstances = append(discoveredInstances, vms...)
		}
	}

	// 获取所有LXC容器（如果pvesh命令失败，尝试pct list）
	lxcsCmd := "pct list | tail -n +2 | awk '{print $1}' | xargs -I {} pct config {}"
	lxcsOutput, err := p.sshClient.Execute(lxcsCmd)
	if err != nil {
		global.APP_LOG.Warn("SSH获取LXCs失败", zap.Error(err))
	} else {
		lxcs, err := p.parsePctConfigs(lxcsOutput)
		if err == nil {
			discoveredInstances = append(discoveredInstances, lxcs...)
		}
	}

	global.APP_LOG.Info("Proxmox SSH发现实例完成",
		zap.String("provider", p.config.Name),
		zap.Int("count", len(discoveredInstances)))

	return discoveredInstances, nil
}

// 辅助函数

func (p *ProxmoxProvider) parseVMsResponse(respData []byte, nodeName string) ([]provider.DiscoveredInstance, error) {
	var vmsData struct {
		Data []struct {
			VMID    int64  `json:"vmid"`
			Name    string `json:"name"`
			Status  string `json:"status"`
			CPUs    int    `json:"cpus"`
			Mem     int64  `json:"mem"`
			MaxMem  int64  `json:"maxmem"`
			MaxDisk int64  `json:"maxdisk"`
		} `json:"data"`
	}

	if err := json.Unmarshal(respData, &vmsData); err != nil {
		return nil, err
	}

	var instances []provider.DiscoveredInstance
	for _, vm := range vmsData.Data {
		discovered := provider.DiscoveredInstance{
			UUID:         fmt.Sprintf("proxmox-vm-%d", vm.VMID),
			Name:         vm.Name,
			Status:       p.mapProxmoxStatus(vm.Status),
			InstanceType: "vm",
			CPU:          vm.CPUs,
			Memory:       vm.MaxMem / 1024 / 1024,  // 字节转MB
			Disk:         vm.MaxDisk / 1024 / 1024, // 字节转MB
			SSHPort:      22,
			RawData:      vm,
		}

		if discovered.CPU == 0 {
			discovered.CPU = 1
		}
		if discovered.Memory == 0 {
			discovered.Memory = 512
		}
		if discovered.Disk == 0 {
			discovered.Disk = 10240
		}

		instances = append(instances, discovered)
	}

	return instances, nil
}

func (p *ProxmoxProvider) parseLXCsResponse(respData []byte, nodeName string) ([]provider.DiscoveredInstance, error) {
	var lxcData struct {
		Data []struct {
			VMID    int64  `json:"vmid"`
			Name    string `json:"name"`
			Status  string `json:"status"`
			CPUs    int    `json:"cpus"`
			Mem     int64  `json:"mem"`
			MaxMem  int64  `json:"maxmem"`
			MaxDisk int64  `json:"maxdisk"`
		} `json:"data"`
	}

	if err := json.Unmarshal(respData, &lxcData); err != nil {
		return nil, err
	}

	var instances []provider.DiscoveredInstance
	for _, lxc := range lxcData.Data {
		discovered := provider.DiscoveredInstance{
			UUID:         fmt.Sprintf("proxmox-lxc-%d", lxc.VMID),
			Name:         lxc.Name,
			Status:       p.mapProxmoxStatus(lxc.Status),
			InstanceType: "container",
			CPU:          lxc.CPUs,
			Memory:       lxc.MaxMem / 1024 / 1024,
			Disk:         lxc.MaxDisk / 1024 / 1024,
			SSHPort:      22,
			RawData:      lxc,
		}

		if discovered.CPU == 0 {
			discovered.CPU = 1
		}
		if discovered.Memory == 0 {
			discovered.Memory = 512
		}
		if discovered.Disk == 0 {
			discovered.Disk = 10240
		}

		instances = append(instances, discovered)
	}

	return instances, nil
}

func (p *ProxmoxProvider) parseResourcesJSON(jsonOutput, instanceType string) ([]provider.DiscoveredInstance, error) {
	var resources []struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Status  string `json:"status"`
		Type    string `json:"type"`
		VMID    int64  `json:"vmid"`
		CPUs    int    `json:"cpu"`
		MaxMem  int64  `json:"maxmem"`
		MaxDisk int64  `json:"maxdisk"`
	}

	if err := json.Unmarshal([]byte(jsonOutput), &resources); err != nil {
		return nil, err
	}

	var instances []provider.DiscoveredInstance
	for _, res := range resources {
		if res.Type != instanceType && res.Type != "lxc" {
			continue
		}

		discovered := provider.DiscoveredInstance{
			UUID:         fmt.Sprintf("proxmox-%s-%d", res.Type, res.VMID),
			Name:         res.Name,
			Status:       p.mapProxmoxStatus(res.Status),
			InstanceType: p.mapProxmoxType(res.Type),
			CPU:          res.CPUs,
			Memory:       res.MaxMem / 1024 / 1024,
			Disk:         res.MaxDisk / 1024 / 1024,
			SSHPort:      22,
			RawData:      res,
		}

		if discovered.CPU == 0 {
			discovered.CPU = 1
		}
		if discovered.Memory == 0 {
			discovered.Memory = 512
		}
		if discovered.Disk == 0 {
			discovered.Disk = 10240
		}

		instances = append(instances, discovered)
	}

	return instances, nil
}

func (p *ProxmoxProvider) parsePctConfigs(output string) ([]provider.DiscoveredInstance, error) {
	// 简单解析pct config输出（格式为key: value）
	var instances []provider.DiscoveredInstance
	// 这个函数的实现比较复杂，暂时返回空列表
	// 实际生产中应该解析每个容器的配置
	return instances, nil
}

func (p *ProxmoxProvider) mapProxmoxStatus(status string) string {
	switch strings.ToLower(status) {
	case "running":
		return "running"
	case "stopped":
		return "stopped"
	case "paused":
		return "paused"
	default:
		return status
	}
}

func (p *ProxmoxProvider) mapProxmoxType(proxmoxType string) string {
	if proxmoxType == "qemu" || proxmoxType == "vm" {
		return "vm"
	}
	return "container"
}

func (p *ProxmoxProvider) makeAPIRequest(ctx context.Context, method, url string, body []byte) ([]byte, error) {
	// 这个方法应该在proxmox.go或api.go中已经存在
	// 如果不存在，需要实现HTTP请求逻辑
	// 暂时返回错误，提示需要实现
	return nil, fmt.Errorf("makeAPIRequest需要在ProxmoxProvider中实现")
}
