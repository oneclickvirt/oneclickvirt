package proxmox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/provider"

	"go.uber.org/zap"
)

// proxmoxAPICreateMayExistError marks an API create path after PVE may have
// accepted the request. SSH fallback is safe for read operations, but creating
// a second guest after an ambiguous POST or a post-create network error is not.
type proxmoxAPICreateMayExistError struct {
	VMID int
	err  error
}

func (e *proxmoxAPICreateMayExistError) Error() string {
	return fmt.Sprintf("PVE API 创建可能已生成 VMID %d，已阻止 SSH 回退以避免重复实例: %v", e.VMID, e.err)
}

func (e *proxmoxAPICreateMayExistError) Unwrap() error { return e.err }

func proxmoxAPICreateMutationError(vmid int, err error) error {
	if err == nil {
		return nil
	}
	return &proxmoxAPICreateMayExistError{VMID: vmid, err: err}
}

// proxmoxAPICreateRequestError is used for the initial create POST.  A clear
// client-side rejection (4xx) means PVE did not accept the mutation, so the
// auto execution mode may still safely try the SSH implementation.  Transport
// failures, 5xx responses, malformed success bodies, and task failures remain
// ambiguous and are wrapped to prevent a duplicate guest.
func proxmoxAPICreateRequestError(vmid int, err error) error {
	if err == nil {
		return nil
	}
	var responseErr *proxmoxAPIResponseError
	if errors.As(err, &responseErr) && responseErr.StatusCode >= http.StatusBadRequest && responseErr.StatusCode < http.StatusInternalServerError {
		return err
	}
	return proxmoxAPICreateMutationError(vmid, err)
}

func proxmoxAPICreateMayHaveMutated(err error) bool {
	var mutationErr *proxmoxAPICreateMayExistError
	return errors.As(err, &mutationErr)
}

// proxmoxAPICreateFallbackBlocked is deliberately separate from the generic
// API-to-SSH fallback policy. A create error may describe a guest that already
// exists remotely, so retrying over SSH would create a duplicate VM/CT.
func proxmoxAPICreateFallbackBlocked(err error) bool {
	return proxmoxAPICreateMayHaveMutated(err)
}

// apiListInstances 通过API方式获取Proxmox实例列表
func (p *ProxmoxProvider) apiListInstances(ctx context.Context) ([]provider.Instance, error) {
	var instances []provider.Instance

	// 获取虚拟机列表
	vmURL := p.apiEndpoint(fmt.Sprintf("/api2/json/nodes/%s/qemu", p.nodeName()))
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
						if vmStatus, _ := vmData["status"].(string); vmStatus == "running" {
							status = "running"
						}

						vmName, _ := vmData["name"].(string)
						vmMem, _ := vmData["mem"].(float64)

						instance := provider.Instance{
							ID:     fmt.Sprintf("%v", vmData["vmid"]),
							Name:   vmName,
							Status: status,
							Type:   "vm",
							CPU:    fmt.Sprintf("%v", vmData["cpus"]),
							Memory: fmt.Sprintf("%.0f MB", vmMem/1024/1024),
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
	ctURL := p.apiEndpoint(fmt.Sprintf("/api2/json/nodes/%s/lxc", p.nodeName()))
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
							if ctStatus, _ := ctData["status"].(string); ctStatus == "running" {
								status = "running"
							}

							ctName, _ := ctData["name"].(string)
							ctMem, _ := ctData["mem"].(float64)

							instance := provider.Instance{
								ID:     fmt.Sprintf("%v", ctData["vmid"]),
								Name:   ctName,
								Status: status,
								Type:   "container",
								CPU:    fmt.Sprintf("%v", ctData["cpus"]),
								Memory: fmt.Sprintf("%.0f MB", ctMem/1024/1024),
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

	// 容器创建直接使用模板缓存；VM/API 创建路径会自行准备 qcow2 或 ISO。
	// 避免在两个不同目录中重复下载同一个 VM 镜像。
	if config.InstanceType == "container" {
		if err := p.prepareImage(ctx, config.Image, config.InstanceType, config.ImageURL, config.UseCDN); err != nil {
			return fmt.Errorf("准备镜像失败: %w", err)
		}
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

	// The create implementations already embed the complete NAT IPv4
	// interface.  A second /config mutation immediately after the create task
	// races PVE's short-lived per-guest lock and can report a false failure.
	// IPv6, dedicated, and IPv6-only modes still require their follow-up
	// address/route configuration.
	networkConfig := p.parseNetworkConfigFromInstanceConfig(config)
	if proxmoxNeedsPostCreateNetworkConfig(networkConfig.NetworkType) {
		if err := p.configureInstanceNetwork(ctx, vmid, config); err != nil {
			if requestedProxmoxIPv6(config) != "" {
				return proxmoxAPICreateMutationError(vmid, fmt.Errorf("配置控制面静态IPv6网络失败: %w", err))
			}
			global.APP_LOG.Warn("网络配置失败", zap.Int("vmid", vmid), zap.Error(err))
		}
	} else {
		global.APP_LOG.Debug("普通NAT IPv4网络已在创建请求中配置，跳过重复网络变更",
			zap.Int("vmid", vmid))
	}

	// 创建接口已经返回了唯一的 VMID 与实例类型。直接使用它们启动，避免
	// 刚创建的 LXC/QEMU 因 SSH 列表尚未刷新而被误判为不存在；启动失败也
	// 不能只记录告警后继续，否则调用方会收到“创建成功”但实例仍是 stopped。
	if err := p.apiStartKnownInstance(ctx, fmt.Sprintf("%d", vmid), config.InstanceType); err != nil {
		return proxmoxAPICreateMutationError(vmid, fmt.Errorf("启动已创建实例失败: %w", err))
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
	p.persistCreatedRuntimeID(config.Name, vmid)

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

	endpoint, err := p.apiGuestEndpoint(instanceType, vmid, "status/start")
	if err != nil {
		return err
	}
	if err := p.submitProxmoxAPITaskAndWaitWithLockRetry(ctx, http.MethodPost, endpoint, nil, "启动"+instanceType); err != nil {
		return fmt.Errorf("启动%s失败: %w", instanceType, err)
	}

	global.APP_LOG.Debug("已发送启动命令，等待实例启动",
		zap.String("id", id),
		zap.String("vmid", vmid),
		zap.String("type", instanceType))

	// 等待实例真正启动
	maxWaitTime := proxmoxStartWaitTimeout(instanceType)
	checkInterval := 3 * time.Second
	startTime := time.Now()

	for {
		// 检查是否超时
		if time.Since(startTime) > maxWaitTime {
			return fmt.Errorf("等待实例启动超时 (%s)", maxWaitTime)
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

// apiStartKnownInstance starts a guest whose VMID and type were returned by a
// successful create request.  It intentionally does not rediscover the guest
// through `pct list`/`qm list`: PVE may expose a completed create task before
// those SSH commands observe the new row.
func (p *ProxmoxProvider) apiStartKnownInstance(ctx context.Context, vmid, instanceType string) error {
	return p.apiStartKnownInstanceAtNode(ctx, p.nodeName(), vmid, instanceType)
}

// apiStartKnownInstanceAtNode starts a guest using an explicit PVE node. It
// is reserved for recovery and create paths where the VMID/type were already
// established by the caller and must not be rediscovered.
func (p *ProxmoxProvider) apiStartKnownInstanceAtNode(ctx context.Context, node, vmid, instanceType string) error {
	vmid = strings.TrimSpace(vmid)
	instanceType = strings.TrimSpace(instanceType)
	if vmid == "" {
		return fmt.Errorf("启动PVE实例缺少VMID")
	}

	if strings.TrimSpace(node) == "" {
		return fmt.Errorf("启动PVE实例缺少节点")
	}
	status, err := p.apiGuestStatusAtNode(ctx, node, instanceType, vmid)
	if err != nil {
		return fmt.Errorf("读取%s %s启动前状态失败: %w", instanceType, vmid, err)
	}
	if status == "running" {
		return nil
	}

	endpoint, err := p.apiGuestEndpointAtNode(node, instanceType, vmid, "status/start")
	if err != nil {
		return err
	}
	if err := p.submitProxmoxAPITaskAndWaitWithLockRetry(ctx, http.MethodPost, endpoint, nil, "启动"+instanceType); err != nil {
		// A start task can race a concurrent successful start.  Confirm its final
		// state before returning an error so a running guest is never reported as
		// failed solely because PVE rejected the duplicate request.
		if currentStatus, statusErr := p.apiGuestStatusAtNode(ctx, node, instanceType, vmid); statusErr == nil && currentStatus == "running" {
			return nil
		}
		return fmt.Errorf("启动%s失败: %w", instanceType, err)
	}

	if err := p.waitForAPIGuestRunningAtNode(ctx, node, vmid, instanceType); err != nil {
		return err
	}
	global.APP_LOG.Debug("Proxmox实例已通过API启动",
		zap.String("vmid", vmid),
		zap.String("type", instanceType))
	return nil
}

func (p *ProxmoxProvider) apiGuestStatus(ctx context.Context, instanceType, vmid string) (string, error) {
	return p.apiGuestStatusAtNode(ctx, p.nodeName(), instanceType, vmid)
}

func (p *ProxmoxProvider) apiGuestStatusAtNode(ctx context.Context, node, instanceType, vmid string) (string, error) {
	endpoint, err := p.apiGuestEndpointAtNode(node, instanceType, vmid, "status/current")
	if err != nil {
		return "", err
	}
	data, err := p.submitProxmoxAPIRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	var status struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		return "", fmt.Errorf("解析PVE实例状态失败: %w", err)
	}
	status.Status = strings.ToLower(strings.TrimSpace(status.Status))
	if status.Status == "" {
		return "", fmt.Errorf("PVE实例状态响应缺少status")
	}
	return status.Status, nil
}

func (p *ProxmoxProvider) waitForAPIGuestRunning(ctx context.Context, vmid, instanceType string) error {
	return p.waitForAPIGuestRunningAtNode(ctx, p.nodeName(), vmid, instanceType)
}

func (p *ProxmoxProvider) waitForAPIGuestRunningAtNode(ctx context.Context, node, vmid, instanceType string) error {
	waitCtx, cancel := context.WithTimeout(ctx, proxmoxStartWaitTimeout(instanceType))
	defer cancel()

	for {
		status, err := p.apiGuestStatusAtNode(waitCtx, node, instanceType, vmid)
		if err != nil {
			return fmt.Errorf("查询%s %s启动状态失败: %w", instanceType, vmid, err)
		}
		if status == "running" {
			return nil
		}

		select {
		case <-waitCtx.Done():
			return fmt.Errorf("等待%s %s启动超时（最后状态: %s）: %w", instanceType, vmid, status, waitCtx.Err())
		case <-time.After(3 * time.Second):
		}
	}
}

// apiStopInstance 通过API方式停止Proxmox实例
func (p *ProxmoxProvider) apiStopInstance(ctx context.Context, id string) error {
	vmid, instanceType, err := p.findVMIDByNameOrID(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to find instance %s: %w", id, err)
	}
	endpoint, err := p.apiGuestEndpoint(instanceType, vmid, "status/stop")
	if err != nil {
		return err
	}
	if err := p.submitProxmoxAPITaskAndWait(ctx, http.MethodPost, endpoint, nil, "停止"+instanceType); err != nil {
		return fmt.Errorf("停止%s失败: %w", instanceType, err)
	}
	return nil
}

// apiRestartInstance 通过API方式重启Proxmox实例
func (p *ProxmoxProvider) apiRestartInstance(ctx context.Context, id string) error {
	vmid, instanceType, err := p.findVMIDByNameOrID(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to find instance %s: %w", id, err)
	}
	endpoint, err := p.apiGuestEndpoint(instanceType, vmid, "status/reboot")
	if err != nil {
		return err
	}
	if err := p.submitProxmoxAPITaskAndWait(ctx, http.MethodPost, endpoint, nil, "重启"+instanceType); err != nil {
		return fmt.Errorf("重启%s失败: %w", instanceType, err)
	}
	return nil
}
