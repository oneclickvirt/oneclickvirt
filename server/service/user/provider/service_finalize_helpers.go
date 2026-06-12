package provider

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider/incus"
	"oneclickvirt/provider/lxd"
	providerService "oneclickvirt/service/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

// waitForInstanceSSHReady 智能等待实例SSH服务就绪
// 通过轮询检查SSH端口是否可连接，而不是盲目等待固定时间
func (s *Service) waitForInstanceSSHReady(ctx context.Context, instanceID, providerID, taskID uint, maxWaitTime time.Duration) error {
	return s.waitForInstanceSSHReadyInRange(ctx, instanceID, providerID, taskID, maxWaitTime, 75, 79)
}

func providerCreateSSHWaitTimeout(provider providerModel.Provider, instance providerModel.Instance) time.Duration {
	providerType := utils.NormalizeProviderType(provider.Type)
	instanceType := utils.NormalizeInstanceType(instance.InstanceType)
	if providerType == "proxmox" {
		if instanceType == "vm" {
			if provider.PveKvmAvailable != nil && !*provider.PveKvmAvailable {
				return 360 * time.Second
			}
			return 240 * time.Second
		}
		return 90 * time.Second
	}
	if instanceType != "vm" {
		return 30 * time.Second
	}
	switch {
	case providerType == "lxd" || providerType == "incus":
		return 180 * time.Second
	case providerType == "qemu" || providerType == "kubevirt" || utils.IsVMOnlyProvider(providerType):
		return 360 * time.Second
	default:
		return 180 * time.Second
	}
}

func (s *Service) waitForInstanceSSHReadyInRange(ctx context.Context, instanceID, providerID, taskID uint, maxWaitTime time.Duration, progressStart, progressEnd int) error {
	if progressEnd < progressStart {
		progressEnd = progressStart
	}
	// 获取实例信息
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		return fmt.Errorf("获取实例信息失败: %w", err)
	}

	// 获取Provider信息
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, providerID).Error; err != nil {
		return fmt.Errorf("获取Provider信息失败: %w", err)
	}

	// 获取SSH端口映射
	var sshPort int
	var sshPortMapping providerModel.Port
	hasPortMapping := global.APP_DB.Where("instance_id = ? AND is_ssh = true AND status = 'active'", instanceID).First(&sshPortMapping).Error == nil
	if hasPortMapping {
		sshPort = sshPortMapping.HostPort
	} else {
		sshPort = instance.SSHPort
		if sshPort == 0 {
			sshPort = 22 // 默认端口
		}
	}

	// 确定SSH连接地址：根据网络类型选择合适的IP
	var sshHost string
	networkType := provider.NetworkType

	// 独立IP模式：实例有独立公网IP，直接连实例
	if networkType == "dedicated_ipv4" || networkType == "dedicated_ipv4_ipv6" {
		if instance.PublicIP != "" {
			sshHost = instance.PublicIP
		} else if instance.PrivateIP != "" {
			// fallback: 某些独立IP场景下 private_ip 即为公网可达地址
			sshHost = instance.PrivateIP
		}
	}

	// NAT 模式：通过端口映射访问，使用 Provider 的公网IP
	if sshHost == "" {
		if provider.PortIP != "" {
			sshHost = provider.PortIP
		} else {
			sshHost = provider.Endpoint
		}
	}

	// 无端口映射且非独立IP模式（如纯IPv6、或未配置端口映射的NAT）：跳过SSH就绪检测
	if !hasPortMapping && networkType != "dedicated_ipv4" && networkType != "dedicated_ipv4_ipv6" {
		global.APP_LOG.Debug("无端口映射且非独立IP模式，跳过SSH就绪检测",
			zap.Uint("instanceId", instanceID),
			zap.String("instanceName", instance.Name),
			zap.String("networkType", networkType))
		s.updateTaskProgress(taskID, progressEnd, "step.skipSSHDetection")
		return nil
	}

	// 如果sshHost包含端口，去掉端口部分
	if colonIndex := strings.LastIndex(sshHost, ":"); colonIndex > 0 {
		if strings.Count(sshHost, ":") == 1 || strings.HasPrefix(sshHost, "[") {
			sshHost = sshHost[:colonIndex]
		}
	}

	global.APP_LOG.Debug("开始等待实例 SSH服务就绪",
		zap.Uint("instanceId", instanceID),
		zap.String("instanceName", instance.Name),
		zap.String("sshHost", sshHost),
		zap.Int("sshPort", sshPort),
		zap.Duration("maxWaitTime", maxWaitTime))

	checkInterval := 5 * time.Second
	startTime := time.Now()
	attemptCount := 0

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("等待SSH服务已取消: %w", ctx.Err())
		default:
		}

		attemptCount++
		elapsed := time.Since(startTime)

		// 检查是否超时
		if elapsed >= maxWaitTime {
			return fmt.Errorf("等待SSH服务超时 (%v), 尝试次数: %d", maxWaitTime, attemptCount)
		}

		// 计算当前进度（75-79%范围内）
		progressPercent := float64(elapsed) / float64(maxWaitTime)
		currentProgress := progressStart + int(float64(progressEnd-progressStart)*progressPercent)
		if currentProgress > progressEnd {
			currentProgress = progressEnd
		}

		// 更新进度和消息
		s.updateTaskProgress(taskID, currentProgress, "step.waitingSSHReady")

		// 仅检测TCP端口连通性（不尝试密码认证，因为此时密码尚未写入实例）
		address := net.JoinHostPort(sshHost, fmt.Sprintf("%d", sshPort))
		conn, err := net.DialTimeout("tcp", address, 5*time.Second)
		if err == nil {
			conn.Close()
			global.APP_LOG.Debug("实例SSH端口已开放",
				zap.Uint("instanceId", instanceID),
				zap.String("instanceName", instance.Name),
				zap.Duration("waitTime", elapsed),
				zap.Int("attempts", attemptCount))
			s.updateTaskProgress(taskID, progressEnd, "step.sshPortOpened")
			return nil
		}

		// 连接失败，记录日志并等待重试
		global.APP_LOG.Debug("等待实例SSH就绪",
			zap.Uint("instanceId", instanceID),
			zap.String("instanceName", instance.Name),
			zap.Int("attempt", attemptCount),
			zap.Duration("elapsed", elapsed),
			zap.String("error", err.Error()))

		// 等待后重试
		select {
		case <-ctx.Done():
			return fmt.Errorf("等待SSH服务已取消: %w", ctx.Err())
		case <-time.After(checkInterval):
		}
	}
}

// ensureInstanceRunnableAfterCreate verifies that the provider still reports the
// instance as runnable after creation. This catches cases where the provider
// command returned success or partial success, but the LXD/Incus container ended
// up STOPPED/FROZEN and later password/network steps would only fail with a
// generic "container not running" error.
func (s *Service) ensureInstanceRunnableAfterCreate(ctx context.Context, instanceID, providerID, taskID uint, progress int) error {
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		return fmt.Errorf("获取实例信息失败: %w", err)
	}

	var dbProvider providerModel.Provider
	if err := global.APP_DB.First(&dbProvider, providerID).Error; err != nil {
		return fmt.Errorf("获取Provider信息失败: %w", err)
	}

	providerSvc := providerService.GetProviderService()
	providerInstance, exists := providerSvc.GetProviderByID(providerID)
	if !exists || providerInstance == nil || !providerInstance.IsConnected() {
		return fmt.Errorf("Provider %d 未连接，无法确认实例 %s 创建后状态", providerID, instance.Name)
	}

	actual, err := providerInstance.GetInstance(ctx, instance.Name)
	if err != nil {
		return fmt.Errorf("创建后获取实例 %s 状态失败: %w", instance.Name, err)
	}
	if actual == nil {
		return fmt.Errorf("创建后Provider未返回实例 %s 的状态", instance.Name)
	}

	statusRaw := strings.TrimSpace(actual.Status)
	status := strings.ToLower(statusRaw)
	if status == "" {
		return nil
	}
	if status == "running" || status == "active" || status == "started" {
		return nil
	}

	badStatus := map[string]bool{
		"stopped": true,
		"stop":    true,
		"exited":  true,
		"created": true,
		"paused":  true,
		"frozen":  true,
		"failed":  true,
		"error":   true,
		"aborted": true,
	}
	if !badStatus[status] {
		global.APP_LOG.Warn("Provider返回未知实例状态，继续后处理但记录详情",
			zap.Uint("taskId", taskID),
			zap.Uint("instanceId", instanceID),
			zap.String("instanceName", instance.Name),
			zap.String("status", statusRaw))
		utils.AppendTaskLog(taskID, progress, "warn", fmt.Sprintf("step.instanceUnknownStatus:%s", statusRaw))
		return nil
	}

	diag := ""
	switch dbProvider.Type {
	case "lxd":
		if output, diagErr := providerInstance.ExecuteSSHCommand(ctx, fmt.Sprintf("lxc info %s --show-log", shellSingleQuote(instance.Name))); diagErr == nil {
			diag = output
		} else {
			diag = diagErr.Error()
		}
	case "incus":
		if output, diagErr := providerInstance.ExecuteSSHCommand(ctx, fmt.Sprintf("incus info %s --show-log", shellSingleQuote(instance.Name))); diagErr == nil {
			diag = output
		} else {
			diag = diagErr.Error()
		}
	}

	baseErr := fmt.Errorf("实例 %s 创建后未处于运行状态，Provider状态: %s", instance.Name, statusRaw)
	if strings.TrimSpace(diag) != "" {
		baseErr = fmt.Errorf("%w; diagnostics: %s", baseErr, utils.TruncateString(diag, 2000))
	}
	utils.AppendTaskError(taskID, progress, "step.instanceNotRunnable", baseErr)
	return baseErr
}

func (s *Service) ensureInstanceNetworkAddresses(ctx context.Context, instanceID, providerID uint) error {
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		return fmt.Errorf("获取实例信息失败: %w", err)
	}

	var dbProvider providerModel.Provider
	if err := global.APP_DB.First(&dbProvider, providerID).Error; err != nil {
		return fmt.Errorf("获取Provider信息失败: %w", err)
	}

	providerSvc := providerService.GetProviderService()
	providerInstance, exists := providerSvc.GetProviderByID(providerID)
	if !exists {
		return fmt.Errorf("provider %d 未初始化", providerID)
	}

	deadline := time.Now().Add(90 * time.Second)
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("实例网络地址补齐已取消: %w", ctx.Err())
		default:
		}

		updates := map[string]interface{}{}

		switch dbProvider.Type {
		case "lxd":
			if lxdProvider, ok := providerInstance.(*lxd.LXDProvider); ok {
				if ip, err := lxdProvider.GetInstanceIPv4(ctx, instance.Name); err == nil && ip != "" {
					updates["private_ip"] = ip
				}
				if ipv6, err := lxdProvider.GetInstanceIPv6(instance.Name); err == nil && ipv6 != "" {
					updates["ipv6_address"] = ipv6
				}
				if publicIPv6, err := lxdProvider.GetInstancePublicIPv6(instance.Name); err == nil && publicIPv6 != "" {
					updates["public_ipv6"] = publicIPv6
				}
			}
		case "incus":
			if incusProvider, ok := providerInstance.(*incus.IncusProvider); ok {
				if ip, err := incusProvider.GetInstanceIPv4(ctx, instance.Name); err == nil && ip != "" {
					updates["private_ip"] = ip
				}
				if ipv6, err := incusProvider.GetInstanceIPv6(ctx, instance.Name); err == nil && ipv6 != "" {
					updates["ipv6_address"] = ipv6
				}
				if publicIPv6, err := incusProvider.GetInstancePublicIPv6(ctx, instance.Name); err == nil && publicIPv6 != "" {
					updates["public_ipv6"] = publicIPv6
				}
			}
		case "proxmox", "proxmoxve":
			if proxmoxProvider, ok := providerInstance.(interface {
				GetInstanceIPv4(context.Context, string) (string, error)
				GetInstanceIPv6(context.Context, string) (string, error)
				GetInstancePublicIPv6(context.Context, string) (string, error)
			}); ok {
				if ip, err := proxmoxProvider.GetInstanceIPv4(ctx, instance.Name); err == nil && ip != "" {
					updates["private_ip"] = ip
					if dbProvider.NetworkType == "dedicated_ipv4" || dbProvider.NetworkType == "dedicated_ipv4_ipv6" {
						updates["public_ip"] = ip
					}
				}
				if ipv6, err := proxmoxProvider.GetInstanceIPv6(ctx, instance.Name); err == nil && ipv6 != "" {
					updates["ipv6_address"] = ipv6
				}
				if publicIPv6, err := proxmoxProvider.GetInstancePublicIPv6(ctx, instance.Name); err == nil && publicIPv6 != "" {
					updates["public_ipv6"] = publicIPv6
				}
			}
		case "qemu", "kubevirt", "vmware", "virtualbox", "multipass", "vagrant":
			if actualInstance, err := providerInstance.GetInstance(ctx, instance.Name); err == nil && actualInstance != nil {
				if actualInstance.PrivateIP != "" {
					updates["private_ip"] = actualInstance.PrivateIP
				} else if actualInstance.IP != "" {
					updates["private_ip"] = actualInstance.IP
				}
			}
		case "docker", "podman", "containerd", "orbstack":
			// 容器类 Provider 在实例创建时已将 private_ip 写入数据库，
			// 此处仅做幂等补齐，若已有则直接返回。
			if instance.PrivateIP != "" {
				return nil
			}
		}

		if len(updates) > 0 {
			if err := global.APP_DB.Model(&providerModel.Instance{}).Where("id = ?", instanceID).Updates(updates).Error; err != nil {
				return fmt.Errorf("更新实例网络地址失败: %w", err)
			}
			if privateIP, ok := updates["private_ip"].(string); ok && privateIP != "" {
				return nil
			}
		}

		if time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("实例网络地址补齐已取消: %w", ctx.Err())
		case <-time.After(10 * time.Second):
		}
	}

	return fmt.Errorf("实例内网IP在重试窗口内仍未就绪")
}

// verifySSHPasswordAuth 在密码设置完成后，通过密码认证验证SSH可用性（最多重试3次）
func (s *Service) verifySSHPasswordAuth(instanceID uint, sshHost string, sshPort int, username, password string) bool {
	address := net.JoinHostPort(sshHost, fmt.Sprintf("%d", sshPort))
	config := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	for i := 0; i < 3; i++ {
		client, err := ssh.Dial("tcp", address, config)
		if err == nil {
			client.Close()
			global.APP_LOG.Info("SSH密码认证验证成功",
				zap.Uint("instanceId", instanceID),
				zap.String("address", address))
			return true
		}
		global.APP_LOG.Debug("SSH密码认证验证失败，等待重试",
			zap.Uint("instanceId", instanceID),
			zap.Int("attempt", i+1),
			zap.Error(err))
		if i < 2 {
			time.Sleep(5 * time.Second)
		}
	}
	global.APP_LOG.Warn("SSH密码认证验证失败（密码可能未正确设置）",
		zap.Uint("instanceId", instanceID),
		zap.String("address", address))
	return false
}

// 辅助函数：创建 bool 指针
func boolPtr(b bool) *bool {
	return &b
}

// 辅助函数：创建 string 指针
func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// 辅助函数：创建 int 指针
func intPtr(i int) *int {
	if i == 0 {
		return nil
	}
	return &i
}
