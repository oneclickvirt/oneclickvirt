package proxmox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
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
		global.APP_LOG.Info("Proxmox API实例创建进度",
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

	global.APP_LOG.Info("已发送启动命令，等待实例启动",
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
			global.APP_LOG.Info("Proxmox实例已成功启动",
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
						global.APP_LOG.Info("QEMU Guest Agent已就绪",
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
							global.APP_LOG.Info("QEMU Guest Agent已就绪",
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

// apiDeleteInstance 通过API方式删除Proxmox实例
func (p *ProxmoxProvider) apiDeleteInstance(ctx context.Context, id string) error {
	// 先通过SSH查找实例信息（API可能无法直接获取所有必要信息）
	vmid, instanceType, err := p.findVMIDByNameOrID(ctx, id)
	if err != nil {
		global.APP_LOG.Error("API删除: 无法找到实例对应的VMID",
			zap.String("id", id),
			zap.Error(err))
		return fmt.Errorf("无法找到实例 %s 对应的VMID: %w", id, err)
	}

	// 获取实例IP地址用于后续清理
	ipAddress, err := p.getInstanceIPAddress(ctx, vmid, instanceType)
	if err != nil {
		global.APP_LOG.Warn("API删除: 无法获取实例IP地址",
			zap.String("id", id),
			zap.String("vmid", vmid),
			zap.Error(err))
		ipAddress = "" // 继续执行，但IP地址为空
	}

	global.APP_LOG.Info("开始API删除Proxmox实例",
		zap.String("id", id),
		zap.String("vmid", vmid),
		zap.String("type", instanceType),
		zap.String("ip", ipAddress))

	// 在删除实例前先清理pmacct监控
	if err := p.cleanupPmacctMonitoring(ctx, id); err != nil {
		global.APP_LOG.Warn("API删除: 清理pmacct监控失败",
			zap.String("id", id),
			zap.String("vmid", vmid),
			zap.Error(err))
	}

	// 根据实例类型选择不同的API端点
	if instanceType == "container" {
		return p.apiDeleteContainer(ctx, vmid, ipAddress)
	} else {
		return p.apiDeleteVM(ctx, vmid, ipAddress)
	}
}

// apiDeleteVM 通过API删除虚拟机
func (p *ProxmoxProvider) apiDeleteVM(ctx context.Context, vmid string, ipAddress string) error {
	global.APP_LOG.Info("开始API删除VM流程",
		zap.String("vmid", vmid),
		zap.String("ip", ipAddress))

	// 1. 解锁VM（通过SSH，因为API可能不支持unlock操作）
	global.APP_LOG.Info("解锁VM", zap.String("vmid", vmid))
	_, err := p.sshClient.Execute(fmt.Sprintf("qm unlock %s 2>/dev/null || true", vmid))
	if err != nil {
		global.APP_LOG.Warn("解锁VM失败", zap.String("vmid", vmid), zap.Error(err))
	}

	// 2. 停止VM
	global.APP_LOG.Info("停止VM", zap.String("vmid", vmid))
	stopURL := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/qemu/%s/status/stop", p.config.Host, p.node, vmid)
	stopReq, err := http.NewRequestWithContext(ctx, "POST", stopURL, nil)
	if err != nil {
		return fmt.Errorf("创建停止请求失败: %w", err)
	}
	p.setAPIAuth(stopReq)

	stopResp, err := p.apiClient.Do(stopReq)
	if err != nil {
		global.APP_LOG.Warn("API停止VM失败，尝试SSH方式", zap.String("vmid", vmid), zap.Error(err))
		_, _ = p.sshClient.Execute(fmt.Sprintf("qm stop %s 2>/dev/null || true", vmid))
	} else {
		stopResp.Body.Close()
	}

	// 3. 检查VM是否完全停止
	if err := p.checkVMCTStatus(ctx, vmid, "vm"); err != nil {
		global.APP_LOG.Warn("VM未完全停止", zap.String("vmid", vmid), zap.Error(err))
		// 继续执行删除，但记录警告
	}

	// 4. 删除VM
	global.APP_LOG.Info("销毁VM", zap.String("vmid", vmid))
	deleteURL := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/qemu/%s", p.config.Host, p.node, vmid)
	deleteReq, err := http.NewRequestWithContext(ctx, "DELETE", deleteURL, nil)
	if err != nil {
		return fmt.Errorf("创建删除请求失败: %w", err)
	}
	p.setAPIAuth(deleteReq)

	deleteResp, err := p.apiClient.Do(deleteReq)
	if err != nil {
		return fmt.Errorf("API删除VM失败: %w", err)
	}
	defer deleteResp.Body.Close()

	if deleteResp.StatusCode != http.StatusOK {
		return fmt.Errorf("API删除VM失败，状态码: %d", deleteResp.StatusCode)
	}

	// 执行后续清理工作（通过SSH，因为这些操作API通常不支持）
	return p.performPostDeletionCleanup(ctx, vmid, ipAddress, "vm")
}

// apiDeleteContainer 通过API删除容器
func (p *ProxmoxProvider) apiDeleteContainer(ctx context.Context, ctid string, ipAddress string) error {
	global.APP_LOG.Info("开始API删除CT流程",
		zap.String("ctid", ctid),
		zap.String("ip", ipAddress))

	// 1. 停止容器
	global.APP_LOG.Info("停止CT", zap.String("ctid", ctid))
	stopURL := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/lxc/%s/status/stop", p.config.Host, p.node, ctid)
	stopReq, err := http.NewRequestWithContext(ctx, "POST", stopURL, nil)
	if err != nil {
		return fmt.Errorf("创建停止请求失败: %w", err)
	}
	p.setAPIAuth(stopReq)

	stopResp, err := p.apiClient.Do(stopReq)
	if err != nil {
		global.APP_LOG.Warn("API停止CT失败，尝试SSH方式", zap.String("ctid", ctid), zap.Error(err))
		_, _ = p.sshClient.Execute(fmt.Sprintf("pct stop %s 2>/dev/null || true", ctid))
	} else {
		stopResp.Body.Close()
	}

	// 2. 检查容器是否完全停止
	if err := p.checkVMCTStatus(ctx, ctid, "container"); err != nil {
		global.APP_LOG.Warn("CT未完全停止", zap.String("ctid", ctid), zap.Error(err))
		// 继续执行删除，但记录警告
	}

	// 3. 删除容器
	global.APP_LOG.Info("销毁CT", zap.String("ctid", ctid))
	deleteURL := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/lxc/%s", p.config.Host, p.node, ctid)
	deleteReq, err := http.NewRequestWithContext(ctx, "DELETE", deleteURL, nil)
	if err != nil {
		return fmt.Errorf("创建删除请求失败: %w", err)
	}
	p.setAPIAuth(deleteReq)

	deleteResp, err := p.apiClient.Do(deleteReq)
	if err != nil {
		return fmt.Errorf("API删除CT失败: %w", err)
	}
	defer deleteResp.Body.Close()

	if deleteResp.StatusCode != http.StatusOK {
		return fmt.Errorf("API删除CT失败，状态码: %d", deleteResp.StatusCode)
	}

	// 执行后续清理工作（通过SSH）
	return p.performPostDeletionCleanup(ctx, ctid, ipAddress, "container")
}

// performPostDeletionCleanup 执行删除后的清理工作
func (p *ProxmoxProvider) performPostDeletionCleanup(ctx context.Context, vmctid string, ipAddress string, instanceType string) error {
	global.APP_LOG.Info("执行删除后清理工作",
		zap.String("vmctid", vmctid),
		zap.String("type", instanceType),
		zap.String("ip", ipAddress))

	// 清理IPv6 NAT映射规则
	if err := p.cleanupIPv6NATRules(ctx, vmctid); err != nil {
		global.APP_LOG.Warn("清理IPv6 NAT规则失败", zap.String("vmctid", vmctid), zap.Error(err))
	}

	// 清理文件
	if instanceType == "vm" {
		if err := p.cleanupVMFiles(ctx, vmctid); err != nil {
			global.APP_LOG.Warn("清理VM文件失败", zap.String("vmid", vmctid), zap.Error(err))
		}
	} else {
		if err := p.cleanupCTFiles(ctx, vmctid); err != nil {
			global.APP_LOG.Warn("清理CT文件失败", zap.String("ctid", vmctid), zap.Error(err))
		}
	}

	// 更新iptables规则
	if ipAddress != "" {
		if err := p.updateIPTablesRules(ctx, ipAddress); err != nil {
			global.APP_LOG.Warn("更新iptables规则失败", zap.String("ip", ipAddress), zap.Error(err))
		}
	}

	// 重建iptables规则
	if err := p.rebuildIPTablesRules(ctx); err != nil {
		global.APP_LOG.Warn("重建iptables规则失败", zap.Error(err))
	}

	// 重启ndpresponder服务
	if err := p.restartNDPResponder(ctx); err != nil {
		global.APP_LOG.Warn("重启ndpresponder服务失败", zap.Error(err))
	}

	global.APP_LOG.Info("通过API成功删除Proxmox实例",
		zap.String("vmctid", vmctid),
		zap.String("type", instanceType))
	return nil
}

// apiListImages 通过API方式获取Proxmox镜像列表
func (p *ProxmoxProvider) apiListImages(ctx context.Context) ([]provider.Image, error) {
	url := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/storage/local/content", p.config.Host, p.node)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	// 设置认证头
	p.setAPIAuth(req)

	resp, err := p.apiClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var response map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}

	var images []provider.Image
	if data, ok := response["data"].([]interface{}); ok {
		for _, item := range data {
			if imageData, ok := item.(map[string]interface{}); ok {
				if imageData["content"].(string) == "iso" {
					image := provider.Image{
						ID:   imageData["volid"].(string),
						Name: imageData["volid"].(string),
						Tag:  "iso",
						Size: fmt.Sprintf("%.2f MB", imageData["size"].(float64)/1024/1024),
					}
					images = append(images, image)
				}
			}
		}
	}

	return images, nil
}

// apiPullImage 通过API方式拉取Proxmox镜像
func (p *ProxmoxProvider) apiPullImage(ctx context.Context, image string) error {
	// Proxmox API 拉取镜像与SSH方式一致，都是直接下载文件到文件系统
	// 因为Proxmox没有独立的镜像仓库API，所以使用SSH方式下载
	return p.sshPullImage(ctx, image)
}

// apiDeleteImage 通过API方式删除Proxmox镜像
func (p *ProxmoxProvider) apiDeleteImage(ctx context.Context, id string) error {
	url := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/storage/local/content/%s", p.config.Host, p.node, id)
	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
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
		return fmt.Errorf("failed to delete image: %d", resp.StatusCode)
	}

	return nil
}

// apiSetInstancePassword 通过API设置实例密码
func (p *ProxmoxProvider) apiSetInstancePassword(ctx context.Context, instanceID, password string) error {
	// 先查找实例的VMID和类型
	vmid, instanceType, err := p.findVMIDByNameOrID(ctx, instanceID)
	if err != nil {
		global.APP_LOG.Error("API查找Proxmox实例失败",
			zap.String("instanceID", instanceID),
			zap.Error(err))
		return fmt.Errorf("查找实例失败: %w", err)
	}

	// 检查实例状态
	var statusURL string
	switch instanceType {
	case "container":
		statusURL = fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/lxc/%s/status/current", p.config.Host, p.node, vmid)
	case "vm":
		statusURL = fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/qemu/%s/status/current", p.config.Host, p.node, vmid)
	default:
		return fmt.Errorf("未知的实例类型: %s", instanceType)
	}

	statusReq, err := http.NewRequestWithContext(ctx, "GET", statusURL, nil)
	if err != nil {
		return fmt.Errorf("创建状态查询请求失败: %w", err)
	}
	p.setAPIAuth(statusReq)

	statusResp, err := p.apiClient.Do(statusReq)
	if err != nil {
		return fmt.Errorf("查询实例状态失败: %w", err)
	}
	defer statusResp.Body.Close()

	var statusResponse map[string]interface{}
	if err := json.NewDecoder(statusResp.Body).Decode(&statusResponse); err != nil {
		return fmt.Errorf("解析状态响应失败: %w", err)
	}

	if data, ok := statusResponse["data"].(map[string]interface{}); ok {
		if status, ok := data["status"].(string); ok && status != "running" {
			return fmt.Errorf("实例 %s (VMID: %s) 未运行，当前状态: %s", instanceID, vmid, status)
		}
	}

	// 根据实例类型设置密码
	switch instanceType {
	case "container":
		// LXC容器 - 通过API执行命令设置密码
		return p.apiSetContainerPassword(ctx, vmid, password)
	case "vm":
		// QEMU虚拟机 - 通过API设置cloud-init密码
		return p.apiSetVMPassword(ctx, vmid, password)
	default:
		return fmt.Errorf("未知的实例类型: %s", instanceType)
	}
}

// apiSetContainerPassword 通过API为LXC容器设置密码
func (p *ProxmoxProvider) apiSetContainerPassword(ctx context.Context, vmid, password string) error {
	// 使用LXC容器的exec API执行chpasswd命令
	url := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/lxc/%s/exec", p.config.Host, p.node, vmid)

	// 构造执行命令的请求体
	payload := map[string]interface{}{
		"command": fmt.Sprintf("echo 'root:%s' | chpasswd", password),
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("序列化请求失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(jsonData)))
	if err != nil {
		return fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	p.setAPIAuth(req)

	resp, err := p.apiClient.Do(req)
	if err != nil {
		return fmt.Errorf("执行API请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var respData map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&respData)
		return fmt.Errorf("设置容器密码失败: status %d, response: %v", resp.StatusCode, respData)
	}

	global.APP_LOG.Info("通过API成功设置容器密码", zap.String("vmid", vmid))
	return nil
}

// apiSetVMPassword 通过API为QEMU虚拟机设置密码
func (p *ProxmoxProvider) apiSetVMPassword(ctx context.Context, vmid, password string) error {
	// 使用cloud-init设置密码
	url := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/qemu/%s/config", p.config.Host, p.node, vmid)

	// 构造cloud-init密码配置
	payload := map[string]interface{}{
		"cipassword": password,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("序列化请求失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "PUT", url, strings.NewReader(string(jsonData)))
	if err != nil {
		return fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	p.setAPIAuth(req)

	resp, err := p.apiClient.Do(req)
	if err != nil {
		return fmt.Errorf("执行API请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var respData map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&respData)
		return fmt.Errorf("设置虚拟机密码失败: status %d, response: %v", resp.StatusCode, respData)
	}

	// 重启虚拟机以应用密码更改
	restartURL := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/qemu/%s/status/reboot", p.config.Host, p.node, vmid)
	restartReq, err := http.NewRequestWithContext(ctx, "POST", restartURL, nil)
	if err != nil {
		global.APP_LOG.Warn("创建重启请求失败", zap.String("vmid", vmid), zap.Error(err))
		return nil // 密码已设置，重启失败不影响
	}
	p.setAPIAuth(restartReq)

	restartResp, err := p.apiClient.Do(restartReq)
	if err != nil {
		global.APP_LOG.Warn("重启虚拟机失败", zap.String("vmid", vmid), zap.Error(err))
		return nil // 密码已设置，重启失败不影响
	}
	defer restartResp.Body.Close()

	global.APP_LOG.Info("通过API成功设置虚拟机密码并重启", zap.String("vmid", vmid))
	return nil
}

// apiCreateContainer 通过API创建LXC容器
func (p *ProxmoxProvider) apiCreateContainer(ctx context.Context, vmid int, config provider.InstanceConfig, updateProgress func(int, string)) error {
	updateProgress(50, "通过API创建LXC容器...")

	// 获取系统镜像配置
	systemConfig := &provider.InstanceConfig{
		Image:        config.Image,
		InstanceType: config.InstanceType,
	}

	err := p.queryAndSetSystemImage(ctx, systemConfig)
	if err != nil {
		return fmt.Errorf("获取系统镜像失败: %v", err)
	}

	// 生成本地镜像文件路径
	fileName := p.generateRemoteFileName(config.Image, systemConfig.ImageURL, p.config.Architecture)
	localImagePath := filepath.Join("/var/lib/vz/template/cache", fileName)

	// 确保镜像文件存在（通过SSH下载）
	checkCmd := fmt.Sprintf("[ -f %s ] && echo 'exists' || echo 'missing'", localImagePath)
	output, err := p.sshClient.Execute(checkCmd)
	if err != nil {
		return fmt.Errorf("检查镜像文件失败: %v", err)
	}

	if strings.TrimSpace(output) == "missing" {
		updateProgress(30, "下载容器镜像...")
		_, err = p.sshClient.Execute("mkdir -p /var/lib/vz/template/cache")
		if err != nil {
			return fmt.Errorf("创建缓存目录失败: %v", err)
		}

		downloadCmd := fmt.Sprintf("curl -L -o %s %s", localImagePath, systemConfig.ImageURL)
		_, err = p.sshClient.Execute(downloadCmd)
		if err != nil {
			return fmt.Errorf("下载镜像失败: %v", err)
		}
	}

	// 获取存储配置
	var providerRecord providerModel.Provider
	if err := global.APP_DB.Where("name = ?", p.config.Name).First(&providerRecord).Error; err != nil {
		global.APP_LOG.Warn("获取Provider记录失败，使用默认存储", zap.Error(err))
	}

	storage := providerRecord.StoragePool
	if storage == "" {
		storage = "local"
	}

	// 转换参数格式
	cpuFormatted := convertCPUFormat(config.CPU)
	memoryFormatted := convertMemoryFormat(config.Memory)
	diskFormatted := convertDiskFormat(config.Disk)

	// 构造API请求创建容器
	url := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/lxc", p.config.Host, p.node)

	payload := map[string]interface{}{
		"vmid":         vmid,
		"ostemplate":   localImagePath,
		"cores":        cpuFormatted,
		"memory":       memoryFormatted,
		"swap":         "128",
		"rootfs":       fmt.Sprintf("%s:%s", storage, diskFormatted),
		"onboot":       "1",
		"features":     "nesting=1",
		"hostname":     config.Name,
		"unprivileged": "1",
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("序列化请求失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(jsonData)))
	if err != nil {
		return fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	p.setAPIAuth(req)

	resp, err := p.apiClient.Do(req)
	if err != nil {
		return fmt.Errorf("执行API请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var respData map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&respData)
		return fmt.Errorf("创建容器失败: status %d, response: %v", resp.StatusCode, respData)
	}

	updateProgress(70, "配置容器网络...")

	// 解析网络配置获取带宽限制
	networkConfig := p.parseNetworkConfigFromInstanceConfig(config)

	// 配置网络（使用VMID到IP的映射函数，包含带宽限制）
	userIP := VMIDToInternalIP(vmid)
	netConfigURL := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/lxc/%d/config", p.config.Host, p.node, vmid)

	// 构建网络配置字符串，包含 rate 参数
	netConfigStr := fmt.Sprintf("name=eth0,ip=%s/24,bridge=vmbr1,gw=%s", userIP, InternalGateway)
	if networkConfig.OutSpeed > 0 {
		// Proxmox rate 参数单位为 MB/s，配置中的 OutSpeed 单位为 Mbps，需要转换：MB/s = Mbps ÷ 8
		rateMBps := networkConfig.OutSpeed / 8
		if rateMBps < 1 {
			rateMBps = 1 // 最小1MB/s
		}
		netConfigStr = fmt.Sprintf("%s,rate=%d", netConfigStr, rateMBps)
	}

	netPayload := map[string]interface{}{
		"net0": netConfigStr,
	}

	netJsonData, _ := json.Marshal(netPayload)
	netReq, _ := http.NewRequestWithContext(ctx, "PUT", netConfigURL, strings.NewReader(string(netJsonData)))
	netReq.Header.Set("Content-Type", "application/json")
	p.setAPIAuth(netReq)

	netResp, err := p.apiClient.Do(netReq)
	if err != nil {
		global.APP_LOG.Warn("配置容器网络失败", zap.Error(err))
	} else {
		netResp.Body.Close()
	}

	updateProgress(80, "启动容器...")

	global.APP_LOG.Info("通过API成功创建LXC容器",
		zap.Int("vmid", vmid),
		zap.String("name", config.Name))

	return nil
}

// apiCreateVM 通过API创建QEMU虚拟机
func (p *ProxmoxProvider) apiCreateVM(ctx context.Context, vmid int, config provider.InstanceConfig, updateProgress func(int, string)) error {
	updateProgress(50, "通过API创建QEMU虚拟机...")

	// 获取系统镜像配置
	systemConfig := &provider.InstanceConfig{
		Image:        config.Image,
		InstanceType: config.InstanceType,
	}

	err := p.queryAndSetSystemImage(ctx, systemConfig)
	if err != nil {
		return fmt.Errorf("获取系统镜像失败: %v", err)
	}

	// 生成本地镜像文件路径
	fileName := p.generateRemoteFileName(config.Image, systemConfig.ImageURL, p.config.Architecture)
	localImagePath := fmt.Sprintf("/root/qcow/%s", fileName)

	// 确保镜像文件存在（通过SSH下载）
	checkCmd := fmt.Sprintf("[ -f %s ] && echo 'exists' || echo 'missing'", localImagePath)
	output, err := p.sshClient.Execute(checkCmd)
	if err != nil {
		return fmt.Errorf("检查镜像文件失败: %v", err)
	}

	if strings.TrimSpace(output) == "missing" {
		updateProgress(30, "下载虚拟机镜像...")
		_, err = p.sshClient.Execute("mkdir -p /root/qcow")
		if err != nil {
			return fmt.Errorf("创建目录失败: %v", err)
		}

		downloadCmd := fmt.Sprintf("curl -L -o %s %s", localImagePath, systemConfig.ImageURL)
		_, err = p.sshClient.Execute(downloadCmd)
		if err != nil {
			return fmt.Errorf("下载镜像失败: %v", err)
		}
	}

	// 检测系统架构和KVM支持
	archCmd := "uname -m"
	archOutput, _ := p.sshClient.Execute(archCmd)
	systemArch := strings.TrimSpace(archOutput)

	kvmFlag := 1
	cpuType := "host"
	kvmCheckCmd := "[ -e /dev/kvm ] && [ -r /dev/kvm ] && [ -w /dev/kvm ] && echo 'kvm_available' || echo 'kvm_unavailable'"
	kvmOutput, _ := p.sshClient.Execute(kvmCheckCmd)
	if strings.TrimSpace(kvmOutput) != "kvm_available" {
		kvmFlag = 0
		switch systemArch {
		case "aarch64", "armv7l", "armv8", "armv8l":
			cpuType = "max"
		case "i386", "i686", "x86":
			cpuType = "qemu32"
		default:
			cpuType = "qemu64"
		}
	}

	// 获取存储配置
	var providerRecord providerModel.Provider
	if err := global.APP_DB.Where("name = ?", p.config.Name).First(&providerRecord).Error; err != nil {
		global.APP_LOG.Warn("获取Provider记录失败，使用默认存储", zap.Error(err))
	}

	storage := providerRecord.StoragePool
	if storage == "" {
		storage = "local"
	}

	// 转换参数格式
	cpuFormatted := convertCPUFormat(config.CPU)
	memoryFormatted := convertMemoryFormat(config.Memory)

	// 获取IPv6配置信息
	ipv6Info, err := p.getIPv6Info(ctx)
	if err != nil {
		global.APP_LOG.Warn("获取IPv6信息失败，使用默认网络配置", zap.Error(err))
		ipv6Info = &IPv6Info{HasAppendedAddresses: false}
	}

	var net1Bridge string
	if ipv6Info.HasAppendedAddresses {
		net1Bridge = "vmbr1"
	} else {
		net1Bridge = "vmbr2"
	}

	// 获取网络配置用于带宽限制
	networkConfig := p.parseNetworkConfigFromInstanceConfig(config)

	// 通过API创建虚拟机
	url := fmt.Sprintf("https://%s:8006/api2/json/nodes/%s/qemu", p.config.Host, p.node)

	// 根据 PVE 版本决定是否使用 fstrim_cloned_disks 参数
	agentParam := "1"
	if p.supportsCloneFstrim() {
		agentParam = "1,fstrim_cloned_disks=1"
	}

	// 构建网络配置字符串，包含 rate 参数
	net0Config := "virtio,bridge=vmbr1,firewall=0"
	if networkConfig.OutSpeed > 0 {
		// Proxmox rate 参数单位为 MB/s，配置中的 OutSpeed 单位为 Mbps，需要转换：MB/s = Mbps ÷ 8
		rateMBps := networkConfig.OutSpeed / 8
		if rateMBps < 1 {
			rateMBps = 1 // 最小1MB/s
		}
		net0Config = fmt.Sprintf("%s,rate=%d", net0Config, rateMBps)
	}

	net1Config := fmt.Sprintf("virtio,bridge=%s,firewall=0", net1Bridge)

	payload := map[string]interface{}{
		"vmid":    vmid,
		"name":    config.Name,
		"agent":   agentParam,
		"scsihw":  "virtio-scsi-single",
		"serial0": "socket",
		"cores":   cpuFormatted,
		"sockets": "1",
		"cpu":     cpuType,
		"net0":    net0Config,
		"net1":    net1Config,
		"ostype":  "l26",
		"kvm":     fmt.Sprintf("%d", kvmFlag),
		"memory":  memoryFormatted,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("序列化请求失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(jsonData)))
	if err != nil {
		return fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	p.setAPIAuth(req)

	resp, err := p.apiClient.Do(req)
	if err != nil {
		return fmt.Errorf("执行API请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var respData map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&respData)
		return fmt.Errorf("创建虚拟机失败: status %d, response: %v", resp.StatusCode, respData)
	}

	updateProgress(60, "导入磁盘镜像...")

	// 导入磁盘镜像（需要通过SSH执行，因为没有直接的API）
	if systemArch == "aarch64" || systemArch == "armv7l" || systemArch == "armv8" || systemArch == "armv8l" {
		_, err = p.sshClient.Execute(fmt.Sprintf("qm set %d --bios ovmf", vmid))
		if err != nil {
			global.APP_LOG.Warn("设置ARM BIOS失败", zap.Error(err))
		}
	}

	importCmd := fmt.Sprintf("qm importdisk %d %s %s", vmid, localImagePath, storage)
	_, err = p.sshClient.Execute(importCmd)
	if err != nil {
		return fmt.Errorf("导入磁盘失败: %w", err)
	}

	updateProgress(70, "配置虚拟机磁盘和启动...")

	// 配置磁盘和启动（通过SSH，因为这些配置复杂且没有直接的简单API）
	// 这部分使用SSH来完成剩余的配置
	time.Sleep(3 * time.Second)

	// 查找并设置磁盘
	findDiskCmd := fmt.Sprintf("pvesm list %s | awk -v vmid=\"%d\" '$5 == vmid && $1 ~ /\\.raw$/ {print $1}' | tail -n 1", storage, vmid)
	diskOutput, _ := p.sshClient.Execute(findDiskCmd)
	volid := strings.TrimSpace(diskOutput)

	if volid == "" {
		findDiskCmd = fmt.Sprintf("pvesm list %s | awk -v vmid=\"%d\" '$5 == vmid {print $1}' | tail -n 1", storage, vmid)
		diskOutput, _ = p.sshClient.Execute(findDiskCmd)
		volid = strings.TrimSpace(diskOutput)
	}

	if volid != "" {
		_, _ = p.sshClient.Execute(fmt.Sprintf("qm set %d --scsihw virtio-scsi-pci --scsi0 %s", vmid, volid))
	}

	_, _ = p.sshClient.Execute(fmt.Sprintf("qm set %d --bootdisk scsi0", vmid))
	_, _ = p.sshClient.Execute(fmt.Sprintf("qm set %d --boot order=scsi0", vmid))

	// 配置云初始化
	if systemArch == "aarch64" || systemArch == "armv7l" || systemArch == "armv8" || systemArch == "armv8l" {
		_, _ = p.sshClient.Execute(fmt.Sprintf("qm set %d --scsi1 %s:cloudinit", vmid, storage))
	} else {
		_, _ = p.sshClient.Execute(fmt.Sprintf("qm set %d --ide1 %s:cloudinit", vmid, storage))
	}

	// 调整磁盘大小
	// Proxmox 不支持缩小磁盘，所以需要先检查当前磁盘大小，只在需要扩大时才resize
	diskFormatted := convertDiskFormat(config.Disk)
	if diskFormatted != "" {
		// 尝试解析目标磁盘大小（单位：GB）
		targetDiskGB := 0
		if diskNum, parseErr := strconv.Atoi(diskFormatted); parseErr == nil {
			targetDiskGB = diskNum
		}

		if targetDiskGB > 0 {
			// 获取当前磁盘大小
			getCurrentSizeCmd := fmt.Sprintf("qm config %d | grep 'scsi0' | awk -F'size=' '{print $2}' | awk '{print $1}'", vmid)
			currentSizeOutput, _ := p.sshClient.Execute(getCurrentSizeCmd)
			currentSize := strings.TrimSpace(currentSizeOutput)

			shouldResize := true
			if currentSize != "" {
				// 解析当前磁盘大小（可能是 10G, 1024M 等格式）
				currentGB := 0
				if strings.HasSuffix(currentSize, "G") {
					if num, err := strconv.Atoi(strings.TrimSuffix(currentSize, "G")); err == nil {
						currentGB = num
					}
				} else if strings.HasSuffix(currentSize, "M") {
					if num, err := strconv.Atoi(strings.TrimSuffix(currentSize, "M")); err == nil {
						currentGB = (num + 1023) / 1024 // 向上取整
					}
				}

				// 只有当目标大小大于当前大小时才resize
				if currentGB > 0 && targetDiskGB <= currentGB {
					shouldResize = false
					global.APP_LOG.Info("磁盘无需调整",
						zap.Int("vmid", vmid),
						zap.Int("current_gb", currentGB),
						zap.Int("target_gb", targetDiskGB))
				}
			}

			if shouldResize {
				resizeCmd := fmt.Sprintf("qm resize %d scsi0 %sG", vmid, diskFormatted)
				_, err := p.sshClient.Execute(resizeCmd)
				if err != nil {
					// 尝试以MB为单位重试
					diskMB := targetDiskGB * 1024
					resizeCmd = fmt.Sprintf("qm resize %d scsi0 %dM", vmid, diskMB)
					_, err = p.sshClient.Execute(resizeCmd)
					if err != nil {
						global.APP_LOG.Warn("调整磁盘大小失败", zap.Int("vmid", vmid), zap.Error(err))
					}
				}
			}
		}
	}

	// 配置IP（使用VMID到IP的映射函数）
	userIP := VMIDToInternalIP(vmid)
	_, _ = p.sshClient.Execute(fmt.Sprintf("qm set %d --ipconfig0 ip=%s/24,gw=%s", vmid, userIP, InternalGateway))

	updateProgress(80, "虚拟机配置完成...")

	// 启动虚拟机（通过API创建后不会自动启动）
	updateProgress(85, "启动虚拟机...")
	startCmd := fmt.Sprintf("qm start %d", vmid)
	_, err = p.sshClient.Execute(startCmd)
	if err != nil {
		global.APP_LOG.Warn("启动虚拟机失败", zap.Int("vmid", vmid), zap.Error(err))
		// 不返回错误，继续流程
	} else {
		updateProgress(90, "等待虚拟机启动...")

		// 等待虚拟机状态变为running
		maxWaitTime := 90 * time.Second
		checkInterval := 3 * time.Second
		startTime := time.Now()
		vmRunning := false

		for time.Since(startTime) < maxWaitTime {
			statusCmd := fmt.Sprintf("qm status %d", vmid)
			statusOutput, err := p.sshClient.Execute(statusCmd)
			if err == nil && strings.Contains(statusOutput, "status: running") {
				vmRunning = true
				global.APP_LOG.Info("虚拟机已启动",
					zap.Int("vmid", vmid),
					zap.Duration("elapsed", time.Since(startTime)))
				break
			}
			time.Sleep(checkInterval)
		}

		if !vmRunning {
			global.APP_LOG.Warn("虚拟机启动超时",
				zap.Int("vmid", vmid),
				zap.Duration("elapsed", time.Since(startTime)))
		}

		updateProgress(95, "检测Guest Agent...")

		// 智能等待QEMU Guest Agent就绪
		if vmRunning {
			// 先快速检测3次，判断镜像是否支持Guest Agent
			agentSupported := false
			for i := 0; i < 3; i++ {
				agentCmd := fmt.Sprintf("qm agent %d ping 2>/dev/null", vmid)
				_, err := p.sshClient.Execute(agentCmd)
				if err == nil {
					agentSupported = true
					global.APP_LOG.Info("检测到QEMU Guest Agent已安装并就绪",
						zap.Int("vmid", vmid))
					break
				}
				time.Sleep(2 * time.Second)
			}

			// 如果快速检测失败，进行较短的等待
			if !agentSupported {
				global.APP_LOG.Info("镜像可能未安装QEMU Guest Agent，进行短时等待...",
					zap.Int("vmid", vmid))

				agentWaitTime := 15 * time.Second
				agentStartTime := time.Now()

				for time.Since(agentStartTime) < agentWaitTime {
					agentCmd := fmt.Sprintf("qm agent %d ping 2>/dev/null", vmid)
					_, err := p.sshClient.Execute(agentCmd)
					if err == nil {
						global.APP_LOG.Info("QEMU Guest Agent已就绪",
							zap.Int("vmid", vmid),
							zap.Duration("elapsed", time.Since(agentStartTime)))
						agentSupported = true
						break
					}
					time.Sleep(3 * time.Second)
				}

				if !agentSupported {
					global.APP_LOG.Warn("镜像未安装QEMU Guest Agent或Agent启动较慢",
						zap.Int("vmid", vmid),
						zap.String("建议", "如需使用Agent功能，请在镜像中安装qemu-guest-agent软件包"))
				}
			}
		}
	}

	updateProgress(100, "虚拟机创建完成")

	global.APP_LOG.Info("通过API成功创建QEMU虚拟机",
		zap.Int("vmid", vmid),
		zap.String("name", config.Name))

	return nil
}
