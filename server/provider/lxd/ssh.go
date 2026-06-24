package lxd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

func (l *LXDProvider) sshListInstances(ctx context.Context) ([]provider.Instance, error) {
	// 原有逻辑：使用 CSV 格式获取基本实例信息（兼容性最好）
	output, err := l.sshClient.Execute("lxc list --format csv -c n,s,t")
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	var instances []provider.Instance

	for _, line := range lines {
		if line == "" {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) < 3 {
			continue
		}

		instance := provider.Instance{
			ID:     fields[0],
			Name:   fields[0],
			Status: strings.ToLower(fields[1]),
			Type:   fields[2],
		}
		instances = append(instances, instance)
	}

	// 补充逻辑：尝试通过 JSON 格式获取 IP 地址信息（如果支持的话）
	l.enrichInstancesWithIPAddresses(&instances)

	global.APP_LOG.Debug("通过SSH成功获取LXD实例列表", zap.Int("count", len(instances)))
	return instances, nil
}

// enrichInstancesWithIPAddresses 补充获取实例的IP地址信息
func (l *LXDProvider) enrichInstancesWithIPAddresses(instances *[]provider.Instance) {
	// 尝试使用 JSON 格式获取详细信息（包含 IP 地址）
	output, err := l.sshClient.Execute("lxc list --format json")
	if err != nil {
		// JSON 格式不支持，跳过IP地址获取
		global.APP_LOG.Debug("lxc list --format json 不支持，跳过IP地址获取",
			zap.Error(err))
		return
	}

	// 解析 JSON 输出
	var lxdInstances []map[string]interface{}
	if err := json.Unmarshal([]byte(output), &lxdInstances); err != nil {
		global.APP_LOG.Debug("解析 lxc list JSON 输出失败",
			zap.Error(err))
		return
	}

	// 构建实例名称到JSON数据的映射
	instanceMap := make(map[string]map[string]interface{})
	for _, inst := range lxdInstances {
		if name, ok := inst["name"].(string); ok {
			instanceMap[name] = inst
		}
	}

	// 遍历实例列表，补充 IP 地址信息
	for idx := range *instances {
		instance := &(*instances)[idx]
		inst, exists := instanceMap[instance.Name]
		if !exists {
			continue
		}

		// 从 state.network 提取网络信息
		if state, ok := inst["state"].(map[string]interface{}); ok {
			if network, ok := state["network"].(map[string]interface{}); ok {
				// 遍历网络接口，通常是 eth0, eth1 等
				for ifaceName, ifaceData := range network {
					if ifaceMap, ok := ifaceData.(map[string]interface{}); ok {
						if addresses, ok := ifaceMap["addresses"].([]interface{}); ok {
							for _, addr := range addresses {
								if addrMap, ok := addr.(map[string]interface{}); ok {
									family, _ := addrMap["family"].(string)
									scope, _ := addrMap["scope"].(string)
									address, _ := addrMap["address"].(string)

									// IPv4 地址
									if family == "inet" {
										if scope == "global" || scope == "link" {
											if instance.PrivateIP == "" {
												instance.PrivateIP = address
												instance.IP = address
												global.APP_LOG.Debug("获取到内网IPv4地址",
													zap.String("instance", instance.Name),
													zap.String("interface", ifaceName),
													zap.String("ip", address))
											}
										}
									}

									// IPv6 地址
									if family == "inet6" && scope == "global" {
										if instance.IPv6Address == "" {
											instance.IPv6Address = address
											global.APP_LOG.Debug("获取到IPv6地址",
												zap.String("instance", instance.Name),
												zap.String("interface", ifaceName),
												zap.String("ipv6", address))
										}
									}
								}
							}
						}
					}
				}

				// 补充逻辑1：如果没有获取到内网IPv4，尝试从 eth0 明确获取
				if instance.PrivateIP == "" {
					if eth0, ok := network["eth0"].(map[string]interface{}); ok {
						if addresses, ok := eth0["addresses"].([]interface{}); ok {
							for _, addr := range addresses {
								if addrMap, ok := addr.(map[string]interface{}); ok {
									family, _ := addrMap["family"].(string)
									scope, _ := addrMap["scope"].(string)
									address, _ := addrMap["address"].(string)

									if family == "inet" && scope == "global" {
										instance.PrivateIP = address
										instance.IP = address
										global.APP_LOG.Debug("从eth0补充获取到内网IPv4地址",
											zap.String("instance", instance.Name),
											zap.String("ip", address))
										break
									}
								}
							}
						}
					}
				}

				// 补充逻辑2：处理IPv6地址，优先使用公网IPv6
				if instance.IPv6Address != "" && strings.HasPrefix(instance.IPv6Address, "fd") {
					// 当前IPv6是ULA地址，尝试从eth1获取公网IPv6
					if eth1, ok := network["eth1"].(map[string]interface{}); ok {
						if addresses, ok := eth1["addresses"].([]interface{}); ok {
							for _, addr := range addresses {
								if addrMap, ok := addr.(map[string]interface{}); ok {
									family, _ := addrMap["family"].(string)
									scope, _ := addrMap["scope"].(string)
									address, _ := addrMap["address"].(string)

									if family == "inet6" && scope == "global" && !strings.HasPrefix(address, "fd") {
										instance.IPv6Address = address
										global.APP_LOG.Debug("从eth1替换为公网IPv6地址",
											zap.String("instance", instance.Name),
											zap.String("ipv6", address))
										break
									}
								}
							}
						}
					}
				} else if instance.IPv6Address == "" {
					// 如果没有获取到任何IPv6，尝试从eth1获取
					if eth1, ok := network["eth1"].(map[string]interface{}); ok {
						if addresses, ok := eth1["addresses"].([]interface{}); ok {
							for _, addr := range addresses {
								if addrMap, ok := addr.(map[string]interface{}); ok {
									family, _ := addrMap["family"].(string)
									scope, _ := addrMap["scope"].(string)
									address, _ := addrMap["address"].(string)

									if family == "inet6" && scope == "global" {
										if !strings.HasPrefix(address, "fd") {
											instance.IPv6Address = address
											global.APP_LOG.Debug("从eth1补充获取到公网IPv6地址",
												zap.String("instance", instance.Name),
												zap.String("ipv6", address))
											break
										} else if instance.IPv6Address == "" {
											instance.IPv6Address = address
										}
									}
								}
							}
						}
					}
				}
			}
		}

		// 补充逻辑3：如果 state.network 中仍然没有获取到 IPv6，尝试从 devices 配置中获取
		if instance.IPv6Address == "" {
			if devices, ok := inst["devices"].(map[string]interface{}); ok {
				if eth1, ok := devices["eth1"].(map[string]interface{}); ok {
					if ipv6Addr, ok := eth1["ipv6.address"].(string); ok && ipv6Addr != "" {
						instance.IPv6Address = ipv6Addr
						global.APP_LOG.Debug("从devices配置获取到IPv6地址",
							zap.String("instance", instance.Name),
							zap.String("ipv6", ipv6Addr))
					}
				}
			}
		}
	}
}

func (l *LXDProvider) instanceExists(name string) (bool, error) {
	output, err := l.sshClient.Execute(fmt.Sprintf("lxc list %s --format csv", shellSingleQuote(name)))
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(output) != "", nil
}

func (l *LXDProvider) validateCopyModeSource(config provider.InstanceConfig) error {
	if config.InstanceType != "container" {
		return fmt.Errorf("复制模式仅支持容器实例")
	}
	if !utils.IsValidLXDInstanceName(config.CopySourceName) {
		return fmt.Errorf("源容器名称格式无效: %s", config.CopySourceName)
	}
	output, err := l.sshClient.Execute(fmt.Sprintf("lxc list %s --format csv -c n,s,t", shellSingleQuote(config.CopySourceName)))
	if err != nil {
		return fmt.Errorf("检查源容器失败: %w", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		parts := strings.Split(line, ",")
		if len(parts) < 3 || strings.TrimSpace(parts[0]) != config.CopySourceName {
			continue
		}
		status := strings.TrimSpace(parts[1])
		instanceType := strings.TrimSpace(parts[2])
		if !strings.EqualFold(status, "STOPPED") {
			return fmt.Errorf("源容器 %s 必须处于 STOPPED 状态，当前状态: %s", config.CopySourceName, status)
		}
		if !strings.EqualFold(instanceType, "CONTAINER") && !strings.EqualFold(instanceType, "container") {
			return fmt.Errorf("源实例 %s 不是容器类型，当前类型: %s", config.CopySourceName, instanceType)
		}
		return nil
	}
	return fmt.Errorf("源容器 %s 不存在", config.CopySourceName)
}

func (l *LXDProvider) sshStartInstance(ctx context.Context, id string) error {
	if l.sshInstanceRunning(id) {
		global.APP_LOG.Debug("LXD实例已在运行，跳过启动", zap.String("id", id))
		return nil
	}

	startCmd := fmt.Sprintf("lxc start %s", shellSingleQuote(id))
	var startErr error
	var output string
	for attempt := 1; attempt <= 3; attempt++ {
		output, startErr = l.sshClient.Execute(startCmd)
		if startErr == nil {
			break
		}

		// 如果错误提示实例已在运行，不视为错误
		errMsg := output + "\n" + startErr.Error()
		if lxdAlreadyRunningMessage(errMsg) || l.sshInstanceRunning(id) {
			global.APP_LOG.Debug("LXD实例已在运行", zap.String("id", id))
			return nil
		}

		if attempt < 3 {
			global.APP_LOG.Warn("LXD启动实例首次失败，准备重试",
				zap.String("id", id),
				zap.String("output", utils.TruncateString(output, 500)),
				zap.Error(startErr))
			time.Sleep(time.Duration(attempt*3) * time.Second)
		}
	}

	if startErr != nil {
		if l.sshInstanceRunning(id) {
			global.APP_LOG.Debug("LXD实例启动命令失败后状态已变为运行，继续流程", zap.String("id", id))
			return nil
		}
		diagOutput, diagErr := l.collectStartDiagnostics(id)
		details := []string{}
		if trimmed := strings.TrimSpace(output); trimmed != "" {
			details = append(details, "start output: "+utils.TruncateString(trimmed, 8000))
		}
		if cleanDiag := strings.TrimSpace(diagOutput); cleanDiag != "" {
			details = append(details, "diagnostics: "+utils.TruncateString(cleanDiag, 8000))
		}
		if diagErr != nil {
			details = append(details, fmt.Sprintf("获取实例诊断日志失败: %v", diagErr))
		}
		if len(details) == 0 {
			return fmt.Errorf("failed to start instance: %w", startErr)
		}
		return fmt.Errorf("failed to start instance: %w; %s", startErr, strings.Join(details, "; "))
	}

	global.APP_LOG.Debug("已发送启动命令，等待实例启动", zap.String("id", id))

	// 等待实例真正启动 - 最多等待90秒
	maxWaitTime := 90 * time.Second
	checkInterval := 10 * time.Second
	startTime := time.Now()

	for {
		// 检查是否超时
		if time.Since(startTime) > maxWaitTime {
			return fmt.Errorf("等待实例启动超时 (90秒)")
		}

		// 等待一段时间后再检查
		time.Sleep(checkInterval)

		// 检查实例状态
		statusOutput, err := l.sshClient.Execute(fmt.Sprintf("lxc info %s | grep \"Status:\" | awk '{print $2}'", shellSingleQuote(id)))
		if err == nil {
			status := strings.TrimSpace(statusOutput)
			if status == "RUNNING" || status == "Running" {
				// 实例已经启动，再等待额外的时间确保系统完全就绪
				time.Sleep(3 * time.Second)
				global.APP_LOG.Info("LXD实例已成功启动并就绪",
					zap.String("id", utils.TruncateString(id, 50)),
					zap.Duration("wait_time", time.Since(startTime)))
				return nil
			}
		}

		global.APP_LOG.Debug("等待实例启动",
			zap.String("id", id),
			zap.Duration("elapsed", time.Since(startTime)))
	}
}

func lxdAlreadyRunningMessage(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "already running") || strings.Contains(lower, "instance is already running")
}

func (l *LXDProvider) sshInstanceRunning(id string) bool {
	statusOutput, err := l.sshClient.Execute(fmt.Sprintf("lxc info %s | awk -F': ' '/^Status:/{print $2; exit}'", shellSingleQuote(id)))
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(statusOutput), "running")
}

func (l *LXDProvider) sshInstanceStopped(id string) bool {
	statusOutput, err := l.sshClient.Execute(fmt.Sprintf("lxc info %s | awk -F': ' '/^Status:/{print $2; exit}'", shellSingleQuote(id)))
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(statusOutput), "stopped")
}

func (l *LXDProvider) collectStartDiagnostics(id string) (string, error) {
	commands := []struct {
		name string
		cmd  string
	}{
		{"lxc info --show-log", fmt.Sprintf("lxc info %s --show-log", shellSingleQuote(id))},
		{"lxc info", fmt.Sprintf("lxc info %s", shellSingleQuote(id))},
		{"lxc config show --expanded", fmt.Sprintf("lxc config show %s --expanded", shellSingleQuote(id))},
		{"lxc list", fmt.Sprintf("lxc list %s --format csv -c n,s,t,4,6", shellSingleQuote(id))},
	}
	var parts []string
	var errs []string
	for _, command := range commands {
		output, err := l.sshClient.Execute(command.cmd)
		if trimmed := strings.TrimSpace(output); trimmed != "" {
			parts = append(parts, fmt.Sprintf("[%s]\n%s", command.name, trimmed))
		}
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", command.name, err))
		}
	}
	if len(errs) > 0 {
		return strings.Join(parts, "\n\n"), fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return strings.Join(parts, "\n\n"), nil
}

func (l *LXDProvider) sshStopInstance(ctx context.Context, id string) error {
	output, err := l.sshClient.Execute(fmt.Sprintf("lxc stop %s", shellSingleQuote(id)))
	if err != nil {
		if l.sshInstanceStopped(id) {
			return nil
		}
		return fmt.Errorf("failed to stop instance: %w; output: %s", err, utils.TruncateString(strings.TrimSpace(output), 8000))
	}

	global.APP_LOG.Info("通过SSH成功停止LXD实例", zap.String("id", utils.TruncateString(id, 50)))
	return nil
}

func (l *LXDProvider) sshRestartInstance(ctx context.Context, id string) error {
	output, err := l.sshClient.Execute(fmt.Sprintf("lxc restart %s", shellSingleQuote(id)))
	if err != nil {
		return fmt.Errorf("failed to restart instance: %w; output: %s", err, utils.TruncateString(strings.TrimSpace(output), 8000))
	}

	global.APP_LOG.Info("通过SSH成功重启LXD实例", zap.String("id", utils.TruncateString(id, 50)))
	return nil
}

func (l *LXDProvider) sshDeleteInstance(ctx context.Context, id string) error {
	output, err := l.sshClient.Execute(fmt.Sprintf("lxc delete %s --force", shellSingleQuote(id)))
	if err != nil {
		// 检查是否是实例不存在的错误
		if strings.Contains(output, "Instance not found") || strings.Contains(output, "not found") {
			global.APP_LOG.Debug("实例已不存在，视为删除成功", zap.String("id", utils.TruncateString(id, 50)))
			return nil // 实例不存在，视为删除成功
		}
		return fmt.Errorf("failed to delete instance: %w", err)
	}

	global.APP_LOG.Info("通过SSH成功删除LXD实例", zap.String("id", utils.TruncateString(id, 50)))
	return nil
}

func (l *LXDProvider) sshListImages(ctx context.Context) ([]provider.Image, error) {
	output, err := l.sshClient.Execute("lxc image list --format csv -c l,f,s,u")
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	var images []provider.Image

	for _, line := range lines {
		if line == "" {
			continue
		}
		fields := strings.Split(line, ",")
		if len(fields) < 4 {
			continue
		}

		image := provider.Image{
			ID:   fields[1][:12], // fingerprint
			Name: fields[0],      // alias
			Tag:  "latest",
			Size: fields[2], // size
		}
		images = append(images, image)
	}

	global.APP_LOG.Debug("通过SSH成功获取LXD镜像列表", zap.Int("count", len(images)))
	return images, nil
}

func (l *LXDProvider) sshPullImage(ctx context.Context, image string) error {
	_, err := l.sshClient.Execute(fmt.Sprintf("lxc image copy %s local:", shellSingleQuote("images:"+image)))
	if err != nil {
		return fmt.Errorf("failed to pull image: %w", err)
	}

	global.APP_LOG.Info("通过SSH成功拉取LXD镜像", zap.String("image", utils.TruncateString(image, 100)))
	return nil
}

func (l *LXDProvider) sshDeleteImage(ctx context.Context, id string) error {
	_, err := l.sshClient.Execute(fmt.Sprintf("lxc image delete %s", shellSingleQuote(id)))
	if err != nil {
		return fmt.Errorf("failed to delete image: %w", err)
	}

	global.APP_LOG.Info("通过SSH成功删除LXD镜像", zap.String("id", utils.TruncateString(id, 50)))
	return nil
}

// sshSetInstancePassword 通过SSH设置实例密码
func (l *LXDProvider) sshSetInstancePassword(ctx context.Context, instanceID, password string) error {
	if err := l.setLXDInstancePasswordWithRetry(instanceID, password, "sh"); err != nil {
		global.APP_LOG.Error("设置LXD实例密码失败",
			zap.String("instanceID", instanceID),
			zap.Error(err))
		return fmt.Errorf("设置实例密码失败: %w", err)
	}

	global.APP_LOG.Info("LXD实例密码设置成功(SSH)",
		zap.String("instanceID", utils.TruncateString(instanceID, 12)))

	return nil
}

// configureInstanceNetworkSettings 配置实例网络设置
func (l *LXDProvider) configureInstanceNetworkSettings(ctx context.Context, config provider.InstanceConfig) error {
	// 解析网络配置
	networkConfig := l.parseNetworkConfigFromInstanceConfig(config)

	// 配置网络
	if err := l.configureInstanceNetwork(ctx, config, networkConfig); err != nil {
		return fmt.Errorf("配置实例网络失败: %w", err)
	}

	return nil
}
