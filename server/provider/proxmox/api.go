package proxmox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/provider"

	"go.uber.org/zap"
)

// apiListInstances 通过API方式获取Proxmox实例列表
func (p *ProxmoxProvider) apiListInstances(ctx context.Context) ([]provider.Instance, error) {
	var instances []provider.Instance

	// 获取虚拟机列表
	vmURL := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/qemu", p.config.Host, p.node)
	vmReq, err := http.NewRequestWithContext(ctx, "GET", vmURL, nil)
	if err != nil {
		return nil, err
	}

	// 设置认证头
	p.setAPIAuth(vmReq)

	vmResp, err := p.apiClient.Do(vmReq)
	if err != nil {
		global.APP_LOG.Warn("获取虚拟机列表失败", zap.Error(err))
	} else {
		defer vmResp.Body.Close()

		var vmResponse map[string]interface{}
		if err := json.NewDecoder(vmResp.Body).Decode(&vmResponse); err == nil {
			if data, ok := vmResponse["data"].([]interface{}); ok {
				for _, item := range data {
					if vmData, ok := item.(map[string]interface{}); ok {
						status := "stopped"
						if vmData["status"].(string) == "running" {
							status = "running"
						}

						instance := provider.Instance{
							ID:     fmt.Sprintf("%v", vmData["vmid"]),
							Name:   vmData["name"].(string),
							Status: status,
							Type:   "vm",
							CPU:    fmt.Sprintf("%v", vmData["cpus"]),
							Memory: fmt.Sprintf("%.0f MB", vmData["mem"].(float64)/1024/1024),
						}

						// 获取VM的IP地址
						if ipAddress, err := p.getInstanceIPAddress(ctx, instance.ID, "vm"); err == nil && ipAddress != "" {
							instance.IP = ipAddress
							instance.PrivateIP = ipAddress
						}
						instances = append(instances, instance)
					}
				}
			}
		}
	}

	// 获取容器列表
	ctURL := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/lxc", p.config.Host, p.node)
	ctReq, err := http.NewRequestWithContext(ctx, "GET", ctURL, nil)
	if err != nil {
		global.APP_LOG.Warn("创建容器请求失败", zap.Error(err))
	} else {
		// 设置认证头
		p.setAPIAuth(ctReq)

		ctResp, err := p.apiClient.Do(ctReq)
		if err != nil {
			global.APP_LOG.Warn("获取容器列表失败", zap.Error(err))
		} else {
			defer ctResp.Body.Close()

			var ctResponse map[string]interface{}
			if err := json.NewDecoder(ctResp.Body).Decode(&ctResponse); err == nil {
				if data, ok := ctResponse["data"].([]interface{}); ok {
					for _, item := range data {
						if ctData, ok := item.(map[string]interface{}); ok {
							status := "stopped"
							if ctData["status"].(string) == "running" {
								status = "running"
							}

							instance := provider.Instance{
								ID:     fmt.Sprintf("%v", ctData["vmid"]),
								Name:   ctData["name"].(string),
								Status: status,
								Type:   "container",
								CPU:    fmt.Sprintf("%v", ctData["cpus"]),
								Memory: fmt.Sprintf("%.0f MB", ctData["mem"].(float64)/1024/1024),
							}

							// 获取容器的IP地址
							if ipAddress, err := p.getInstanceIPAddress(ctx, instance.ID, "container"); err == nil && ipAddress != "" {
								instance.IP = ipAddress
								instance.PrivateIP = ipAddress
							}
							instances = append(instances, instance)
						}
					}
				}
			}
		}
	}

	global.APP_LOG.Info("通过API成功获取Proxmox实例列表",
		zap.Int("totalCount", len(instances)))
	return instances, nil
}

// apiCreateInstance 通过API方式创建Proxmox实例
func (p *ProxmoxProvider) apiCreateInstance(ctx context.Context, config provider.InstanceConfig) error {
	return p.apiCreateInstanceWithProgress(ctx, config, nil)
}

// apiCreateInstanceWithProgress 通过API方式创建Proxmox实例，并支持进度回调
func (p *ProxmoxProvider) apiCreateInstanceWithProgress(ctx context.Context, config provider.InstanceConfig, progressCallback provider.ProgressCallback) error {
	// 进度更新辅助函数
	updateProgress := func(percentage int, message string) {
		if progressCallback != nil {
			progressCallback(percentage, message)
		}
		global.APP_LOG.Debug("Proxmox API实例创建进度",
			zap.String("instance", config.Name),
			zap.Int("percentage", percentage),
			zap.String("message", message))
	}

	updateProgress(10, "开始Proxmox API创建实例...")

	// 获取下一个可用的VMID
	vmid, err := p.getNextVMID(ctx, config.InstanceType)
	if err != nil {
		return fmt.Errorf("获取VMID失败: %w", err)
	}
	defer p.releasePendingVMID(vmid)

	updateProgress(20, "准备镜像和资源...")

	// 确保必要的镜像存在（通过SSH准备镜像，因为API和SSH使用相同的文件系统）
	if err := p.prepareImage(ctx, config.Image, config.InstanceType); err != nil {
		return fmt.Errorf("准备镜像失败: %w", err)
	}

	updateProgress(40, "通过API创建实例配置...")

	// 根据实例类型通过API创建容器或虚拟机
	if config.InstanceType == "container" {
		if err := p.apiCreateContainer(ctx, vmid, config, updateProgress); err != nil {
			return fmt.Errorf("API创建容器失败: %w", err)
		}
	} else {
		if err := p.apiCreateVM(ctx, vmid, config, updateProgress); err != nil {
			return fmt.Errorf("API创建虚拟机失败: %w", err)
		}
	}

	updateProgress(90, "配置网络和启动...")

	// 配置网络
	if err := p.configureInstanceNetwork(ctx, vmid, config); err != nil {
		global.APP_LOG.Warn("网络配置失败", zap.Int("vmid", vmid), zap.Error(err))
	}

	// 启动实例
	if err := p.apiStartInstance(ctx, fmt.Sprintf("%d", vmid)); err != nil {
		global.APP_LOG.Warn("启动实例失败", zap.Int("vmid", vmid), zap.Error(err))
	}

	// 虚拟机和容器的带宽限制已在创建时通过 rate 参数配置

	// 配置端口映射
	updateProgress(91, "配置端口映射...")
	if err := p.configureInstancePortMappings(ctx, config, vmid); err != nil {
		global.APP_LOG.Warn("配置端口映射失败", zap.Error(err))
	}

	// 配置SSH密码
	updateProgress(92, "配置SSH密码...")
	if err := p.configureInstanceSSHPasswordByVMID(ctx, vmid, config); err != nil {
		global.APP_LOG.Warn("配置SSH密码失败", zap.Error(err))
	}

	// 初始化pmacct流量监控
	updateProgress(95, "初始化pmacct流量监控...")
	if err := p.initializePmacctMonitoring(ctx, vmid, config.Name); err != nil {
		global.APP_LOG.Warn("初始化流量监控失败",
			zap.Int("vmid", vmid),
			zap.String("name", config.Name),
			zap.Error(err))
	}

	// 更新实例notes - 将配置信息写入到配置文件中
	updateProgress(97, "更新实例配置信息...")
	if err := p.updateInstanceNotes(ctx, vmid, config); err != nil {
		global.APP_LOG.Warn("更新实例notes失败",
			zap.Int("vmid", vmid),
			zap.String("name", config.Name),
			zap.Error(err))
	}

	updateProgress(100, "Proxmox API实例创建完成")

	global.APP_LOG.Info("Proxmox API实例创建成功",
		zap.String("name", config.Name),
		zap.Int("vmid", vmid),
		zap.String("type", config.InstanceType))

	return nil
}

// apiStartInstance 通过API方式启动Proxmox实例
func (p *ProxmoxProvider) apiStartInstance(ctx context.Context, id string) error {
	// 先查找实例的VMID和类型，以便确定正确的API端点
	vmid, instanceType, err := p.findVMIDByNameOrID(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to find instance %s: %w", id, err)
	}

	// 根据实例类型构建正确的URL
	var url string
	switch instanceType {
	case "vm":
		url = fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/qemu/%s/status/start", p.config.Host, p.node, vmid)
	case "container":
		url = fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/lxc/%s/status/start", p.config.Host, p.node, vmid)
	default:
		return fmt.Errorf("unknown instance type: %s", instanceType)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return err
	}

	// 设置认证头
	p.setAPIAuth(req)

	resp, err := p.apiClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to start %s: %d", instanceType, resp.StatusCode)
	}

	global.APP_LOG.Debug("已发送启动命令，等待实例启动",
		zap.String("id", id),
		zap.String("vmid", vmid),
		zap.String("type", instanceType))

	// 等待实例真正启动
	maxWaitTime := 120 * time.Second
	checkInterval := 3 * time.Second
	startTime := time.Now()

	for {
		// 检查是否超时
		if time.Since(startTime) > maxWaitTime {
			return fmt.Errorf("等待实例启动超时 (120秒)")
		}

		// 等待一段时间后再检查
		time.Sleep(checkInterval)

		// 使用SSH检查实例状态
		var statusCmd string
		switch instanceType {
		case "vm":
			statusCmd = fmt.Sprintf("qm status %s", vmid)
		case "container":
			statusCmd = fmt.Sprintf("pct status %s", vmid)
		}

		statusOutput, err := p.sshClient.Execute(statusCmd)
		if err == nil && strings.Contains(statusOutput, "status: running") {
			// 实例已经启动
			global.APP_LOG.Debug("Proxmox实例已成功启动",
				zap.String("id", id),
				zap.String("vmid", vmid),
				zap.String("type", instanceType),
				zap.Duration("wait_time", time.Since(startTime)))

			// 对于VM类型，智能检测QEMU Guest Agent（可选，不影响主流程）
			if instanceType == "vm" {
				// 快速检测2次，判断是否支持Agent
				agentSupported := false
				for i := 0; i < 2; i++ {
					agentCmd := fmt.Sprintf("qm agent %s ping 2>/dev/null", vmid)
					_, err := p.sshClient.Execute(agentCmd)
					if err == nil {
						agentSupported = true
						global.APP_LOG.Debug("QEMU Guest Agent已就绪",
							zap.String("vmid", vmid))
						break
					}
					time.Sleep(2 * time.Second)
				}

				// 如果未检测到，进行短时等待
				if !agentSupported {
					agentWaitTime := 12 * time.Second
					agentStartTime := time.Now()
					for time.Since(agentStartTime) < agentWaitTime {
						agentCmd := fmt.Sprintf("qm agent %s ping 2>/dev/null", vmid)
						_, err := p.sshClient.Execute(agentCmd)
						if err == nil {
							global.APP_LOG.Debug("QEMU Guest Agent已就绪",
								zap.String("vmid", vmid),
								zap.Duration("elapsed", time.Since(agentStartTime)))
							break
						}
						time.Sleep(3 * time.Second)
					}
				}
			}

			// 额外等待确保系统稳定
			time.Sleep(3 * time.Second)
			return nil
		}

		global.APP_LOG.Debug("等待实例启动",
			zap.String("vmid", vmid),
			zap.Duration("elapsed", time.Since(startTime)))
	}
}

// apiStopInstance 通过API方式停止Proxmox实例
func (p *ProxmoxProvider) apiStopInstance(ctx context.Context, id string) error {
	url := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/qemu/%s/status/stop", p.config.Host, p.node, id)
	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return err
	}

	// 设置认证头
	p.setAPIAuth(req)

	resp, err := p.apiClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to stop VM: %d", resp.StatusCode)
	}

	return nil
}

// apiRestartInstance 通过API方式重启Proxmox实例
func (p *ProxmoxProvider) apiRestartInstance(ctx context.Context, id string) error {
	url := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/qemu/%s/status/reboot", p.config.Host, p.node, id)
	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return err
	}

	// 设置认证头
	p.setAPIAuth(req)

	resp, err := p.apiClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to restart VM: %d", resp.StatusCode)
	}

	return nil
}
