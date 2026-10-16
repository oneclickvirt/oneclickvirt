package proxmox

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

func (p *ProxmoxProvider) sshListInstances(ctx context.Context) ([]provider.Instance, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	output, err := utils.ExecuteShellCommandContext(ctx, p.sshClient, "LC_ALL=C NO_COLOR=1 pvesh get /cluster/resources --type vm --output-format json")
	if err != nil {
		return nil, fmt.Errorf("SSH获取Proxmox实例列表失败: %w", err)
	}
	resources, err := parseProxmoxResourcesJSON(output)
	if err != nil {
		return nil, fmt.Errorf("SSH解析Proxmox实例列表失败: %w", err)
	}
	instances, err := p.listInstancesFromResources(ctx, resources)
	if err != nil {
		return nil, err
	}
	global.APP_LOG.Info("通过SSH成功获取Proxmox实例列表",
		zap.Int("totalCount", len(instances)),
		zap.Int("vmCount", len(instances)-countContainers(instances)),
		zap.Int("containerCount", countContainers(instances)))
	return instances, nil
}

func (p *ProxmoxProvider) sshStartInstance(ctx context.Context, id string) error {
	if err := utils.SleepContext(ctx, 3*time.Second); err != nil {
		return err
	}

	// 先查找实例的VMID和类型
	vmid, instanceType, err := p.findVMIDByNameOrID(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to find instance %s: %w", id, err)
	}
	return p.sshStartKnownInstance(ctx, vmid, instanceType)
}

// sshStartKnownInstance starts a newly created guest by its authoritative
// VMID and type. PVE can complete a create command before pct/qm list exposes
// the new row, so rediscovery here can incorrectly report that creation
// succeeded while leaving the guest stopped.
func (p *ProxmoxProvider) sshStartKnownInstance(ctx context.Context, vmid, instanceType string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	vmid = strings.TrimSpace(vmid)
	instanceType = strings.ToLower(strings.TrimSpace(instanceType))
	numericVMID, err := strconv.Atoi(vmid)
	if err != nil || numericVMID <= 0 {
		return fmt.Errorf("invalid Proxmox VMID %q", vmid)
	}

	// 先检查实例状态
	var statusCommand, startCommand string
	switch instanceType {
	case "vm":
		statusCommand = fmt.Sprintf("LC_ALL=C NO_COLOR=1 qm status %s", vmid)
		startCommand = fmt.Sprintf("qm start %s", vmid)
	case "container":
		statusCommand = fmt.Sprintf("LC_ALL=C NO_COLOR=1 pct status %s", vmid)
		startCommand = fmt.Sprintf("pct start %s", vmid)
	default:
		return fmt.Errorf("unknown instance type: %s", instanceType)
	}

	statusOutput, err := utils.ExecuteShellCommandContext(ctx, p.sshClient, statusCommand)
	if err == nil && parsePVEStatusOutput(statusOutput) == "running" {
		// 实例已经在运行，等待3秒认为启动成功
		if err := utils.SleepContext(ctx, 3*time.Second); err != nil {
			return err
		}
		global.APP_LOG.Debug("Proxmox实例已经在运行",
			zap.String("id", vmid),
			zap.String("vmid", vmid),
			zap.String("type", instanceType))
		return nil
	}

	// 执行启动命令
	startOutput, err := utils.ExecuteShellCommandContext(ctx, p.sshClient, startCommand)
	if err != nil {
		statusAfter, statusErr := utils.ExecuteShellCommandContext(ctx, p.sshClient, statusCommand)
		if statusErr == nil && parsePVEStatusOutput(statusAfter) == "running" {
			global.APP_LOG.Debug("Proxmox启动命令失败后状态已变为运行，继续流程",
				zap.String("id", vmid),
				zap.String("vmid", vmid),
				zap.String("type", instanceType),
				zap.String("output", utils.TruncateString(startOutput, 500)))
			return nil
		}
		return fmt.Errorf("failed to start %s %s: %w; output: %s; status: %s", instanceType, vmid, err, utils.TruncateString(strings.TrimSpace(startOutput), 8000), utils.TruncateString(strings.TrimSpace(statusAfter), 2000))
	}

	global.APP_LOG.Debug("已发送启动命令，等待实例启动",
		zap.String("id", vmid),
		zap.String("vmid", vmid),
		zap.String("type", instanceType))

	// 等待实例真正启动
	maxWaitTime := proxmoxStartWaitTimeout(instanceType)
	checkInterval := 3 * time.Second
	waitCtx, cancel := context.WithTimeout(ctx, maxWaitTime)
	defer cancel()
	startTime := time.Now()
	lastStatus := strings.TrimSpace(statusOutput)
	var lastStatusErr error

	for {
		// 等待一段时间后再检查
		if err := utils.SleepContext(waitCtx, checkInterval); err != nil {
			if lastStatus == "" {
				lastStatus = "unknown"
			}
			if lastStatusErr != nil {
				return fmt.Errorf("等待%s %s启动超时（最后状态: %s，最后查询错误: %v）: %w", instanceType, vmid, lastStatus, lastStatusErr, err)
			}
			return fmt.Errorf("等待%s %s启动超时（最后状态: %s）: %w", instanceType, vmid, lastStatus, err)
		}

		// 检查实例状态
		statusOutput, err := utils.ExecuteShellCommandContext(waitCtx, p.sshClient, statusCommand)
		lastStatus = strings.TrimSpace(statusOutput)
		lastStatusErr = err
		if err == nil && parsePVEStatusOutput(statusOutput) == "running" {
			// 实例已经启动
			global.APP_LOG.Debug("Proxmox实例已成功启动",
				zap.String("id", vmid),
				zap.String("vmid", vmid),
				zap.String("type", instanceType),
				zap.Duration("wait_time", time.Since(startTime)))

			// 对于VM类型，智能检测QEMU Guest Agent（可选，不影响主流程）
			if instanceType == "vm" {
				// 快速检测2次，判断是否支持Agent
				agentSupported := false
				for i := 0; i < 2; i++ {
					agentCmd := fmt.Sprintf("qm agent %s ping 2>/dev/null", vmid)
					_, err := utils.ExecuteShellCommandContext(waitCtx, p.sshClient, agentCmd)
					if err == nil {
						agentSupported = true
						global.APP_LOG.Debug("QEMU Guest Agent已就绪",
							zap.String("vmid", vmid))
						break
					}
					if err := utils.SleepContext(waitCtx, 2*time.Second); err != nil {
						return err
					}
				}

				// 如果未检测到，进行短时等待
				if !agentSupported {
					agentWaitTime := 12 * time.Second
					agentStartTime := time.Now()
					for time.Since(agentStartTime) < agentWaitTime {
						agentCmd := fmt.Sprintf("qm agent %s ping 2>/dev/null", vmid)
						_, err := utils.ExecuteShellCommandContext(waitCtx, p.sshClient, agentCmd)
						if err == nil {
							global.APP_LOG.Debug("QEMU Guest Agent已就绪",
								zap.String("vmid", vmid),
								zap.Duration("elapsed", time.Since(agentStartTime)))
							break
						}
						if err := utils.SleepContext(waitCtx, 3*time.Second); err != nil {
							return err
						}
					}
				}
			}

			// 额外等待确保系统稳定
			if err := utils.SleepContext(waitCtx, 3*time.Second); err != nil {
				return err
			}
			return nil
		}

		global.APP_LOG.Debug("等待实例启动",
			zap.String("vmid", vmid),
			zap.Duration("elapsed", time.Since(startTime)))
	}
}

func proxmoxStartWaitTimeout(instanceType string) time.Duration {
	if strings.EqualFold(strings.TrimSpace(instanceType), "container") {
		return 60 * time.Second
	}
	return 120 * time.Second
}

func (p *ProxmoxProvider) sshStopInstance(ctx context.Context, id string) error {
	// 先查找实例的VMID和类型
	vmid, instanceType, err := p.findVMIDByNameOrID(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to find instance %s: %w", id, err)
	}

	// 根据实例类型使用对应的停止命令
	var command string
	switch instanceType {
	case "vm":
		command = fmt.Sprintf("qm stop %s", vmid)
	case "container":
		command = fmt.Sprintf("pct stop %s", vmid)
	default:
		return fmt.Errorf("unknown instance type: %s", instanceType)
	}

	// 执行停止命令
	stopOutput, err := utils.ExecuteShellCommandContext(ctx, p.sshClient, command)
	if err != nil {
		statusCommand := fmt.Sprintf("LC_ALL=C NO_COLOR=1 pct status %s", vmid)
		if instanceType == "vm" {
			statusCommand = fmt.Sprintf("LC_ALL=C NO_COLOR=1 qm status %s", vmid)
		}
		statusAfter, statusErr := utils.ExecuteShellCommandContext(ctx, p.sshClient, statusCommand)
		if statusErr == nil && parsePVEStatusOutput(statusAfter) == "stopped" {
			return nil
		}
		return fmt.Errorf("failed to stop %s %s: %w; output: %s; status: %s", instanceType, vmid, err, utils.TruncateString(strings.TrimSpace(stopOutput), 8000), utils.TruncateString(strings.TrimSpace(statusAfter), 2000))
	}

	global.APP_LOG.Info("通过SSH成功停止Proxmox实例",
		zap.String("id", utils.TruncateString(id, 50)),
		zap.String("vmid", vmid),
		zap.String("type", instanceType))
	return p.waitStopped(ctx, vmid, instanceType)
}

func (p *ProxmoxProvider) sshRestartInstance(ctx context.Context, id string) error {
	// 先查找实例的VMID和类型
	vmid, instanceType, err := p.findVMIDByNameOrID(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to find instance %s: %w", id, err)
	}

	// 根据实例类型使用对应的重启命令
	var command string
	var resetCommand string
	switch instanceType {
	case "vm":
		command = fmt.Sprintf("qm reboot %s", vmid)
		resetCommand = fmt.Sprintf("qm reset %s", vmid)
	case "container":
		command = fmt.Sprintf("pct reboot %s", vmid)
		resetCommand = fmt.Sprintf("pct stop %s && pct start %s", vmid, vmid)
	default:
		return fmt.Errorf("unknown instance type: %s", instanceType)
	}

	// 首先尝试优雅重启
	rebootOutput, err := utils.ExecuteShellCommandContext(ctx, p.sshClient, command)
	if err != nil {
		global.APP_LOG.Warn("优雅重启失败，尝试强制重启",
			zap.String("id", utils.TruncateString(id, 50)),
			zap.String("vmid", vmid),
			zap.String("type", instanceType),
			zap.String("output", utils.TruncateString(rebootOutput, 1000)),
			zap.Error(err))

		// 等待2秒后尝试强制重启
		if err := utils.SleepContext(ctx, 2*time.Second); err != nil {
			return err
		}

		// 尝试强制重启
		resetOutput, resetErr := utils.ExecuteShellCommandContext(ctx, p.sshClient, resetCommand)
		if resetErr != nil {
			return fmt.Errorf("failed to restart %s %s (both reboot and reset failed): reboot error: %w, reboot output: %s, reset error: %v, reset output: %s", instanceType, vmid, err, utils.TruncateString(strings.TrimSpace(rebootOutput), 8000), resetErr, utils.TruncateString(strings.TrimSpace(resetOutput), 8000))
		}

		global.APP_LOG.Info("通过强制重启成功重启Proxmox实例",
			zap.String("id", utils.TruncateString(id, 50)),
			zap.String("vmid", vmid),
			zap.String("type", instanceType))
	} else {
		global.APP_LOG.Info("通过SSH成功重启Proxmox实例",
			zap.String("id", utils.TruncateString(id, 50)),
			zap.String("vmid", vmid),
			zap.String("type", instanceType))
	}

	// 等待3秒让实例完成重启
	if err := utils.SleepContext(ctx, 3*time.Second); err != nil {
		return err
	}
	return nil
}

// A confirmed absent guest makes deletion idempotent. Transport/parse failures
// are deliberately different and must never release its controller allocations.
var errProxmoxInstanceNotFound = errors.New("Proxmox instance does not exist")

// findVMIDByNameOrID uses one authoritative inventory and exact names. Substring
// matching could delete a different guest after a failed create reused its VMID.
func (p *ProxmoxProvider) findVMIDByNameOrID(ctx context.Context, identifier string) (string, string, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return "", "", fmt.Errorf("实例标识为空")
	}
	output, err := utils.ExecuteShellCommandContext(ctx, p.sshClient, "LC_ALL=C NO_COLOR=1 pvesh get /cluster/resources --type vm --output-format json")
	if err != nil {
		return "", "", fmt.Errorf("读取PVE实例清单失败: %w", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(utils.StripTerminalCSI(output)), "[") {
		return "", "", fmt.Errorf("PVE实例清单不是有效数组")
	}
	rows, err := parseProxmoxResourcesJSON(output)
	if err != nil {
		return "", "", fmt.Errorf("解析PVE实例清单失败: %w", err)
	}
	var vmid, instanceType string
	for _, row := range rows {
		if row.VMID <= 0 || (row.Type != "lxc" && row.Type != "qemu") {
			return "", "", fmt.Errorf("PVE实例清单包含无效来宾记录")
		}
		current := strconv.FormatInt(row.VMID, 10)
		if current != identifier && row.Name != identifier {
			continue
		}
		if vmid != "" {
			return "", "", fmt.Errorf("PVE实例标识匹配多个来宾，请使用VMID")
		}
		vmid, instanceType = current, "vm"
		if row.Type == "lxc" {
			instanceType = "container"
		}
	}
	if vmid == "" {
		return "", "", fmt.Errorf("%w: %s", errProxmoxInstanceNotFound, identifier)
	}
	return vmid, instanceType, nil
}

// getInstanceIPAddress 获取实例IP地址
func (p *ProxmoxProvider) getInstanceIPAddress(ctx context.Context, vmid string, instanceType string) (string, error) {
	id, err := strconv.Atoi(vmid)
	if err != nil || id < MinVMID || id > MaxVMID {
		return "", fmt.Errorf("invalid PVE guest ID")
	}
	tool, key := "qm", "ipconfig"
	if instanceType == "container" {
		tool, key = "pct", "net"
	}
	output, err := utils.ExecuteShellCommandContext(ctx, p.sshClient, fmt.Sprintf("%s config %d", tool, id))
	if err != nil {
		return "", fmt.Errorf("read PVE guest network config: %w", err)
	}
	for _, line := range strings.Split(utils.StripTerminalCSI(output), "\n") {
		name, settings, found := strings.Cut(line, ":")
		if !found || !strings.HasPrefix(name, key) {
			continue
		}
		if _, err := strconv.ParseUint(strings.TrimPrefix(name, key), 10, 32); err != nil {
			continue
		}
		for _, option := range strings.Split(settings, ",") {
			if value, found := strings.CutPrefix(strings.TrimSpace(option), "ip="); found {
				if address := validProxmoxIPv4(value); address != "" {
					return address, nil
				}
			}
		}
	}
	// BusyBox hostname has no -I flag. Read structured addresses instead, and
	// never persist command diagnostics or guess an address from a VMID/ping.
	command := fmt.Sprintf("qm guest cmd %d network-get-interfaces", id)
	if instanceType == "container" {
		command = fmt.Sprintf("pct exec %d -- env LC_ALL=C NO_COLOR=1 ip -o -4 addr show scope global", id)
	}
	output, err = utils.ExecuteShellCommandContext(ctx, p.sshClient, command)
	if err != nil {
		return "", fmt.Errorf("read PVE guest IPv4 addresses: %w", err)
	}
	if instanceType != "container" {
		return proxmoxAgentAddress(output, false)
	}
	for _, line := range strings.Split(utils.StripTerminalCSI(output), "\n") {
		fields := strings.Fields(line)
		for i := 0; i+1 < len(fields); i++ {
			if fields[i] == "inet" {
				if address := validProxmoxIPv4(fields[i+1]); address != "" {
					return address, nil
				}
			}
		}
	}
	return "", fmt.Errorf("no valid IPv4 address found for %s %s", instanceType, vmid)
}

func validProxmoxIPv4(value string) string {
	value = strings.TrimSpace(value)
	if prefix, err := netip.ParsePrefix(value); err == nil {
		value = prefix.Addr().String()
	}
	address, err := netip.ParseAddr(value)
	if err != nil || !address.Is4() || !address.IsGlobalUnicast() {
		return ""
	}
	return address.String()
}
