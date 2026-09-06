package proxmox

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/provider"
)

// StartInstanceByRecoveryIdentity is the only Proxmox start path used by the
// reboot-recovery worker. The node, VMID, and type come from one authoritative
// discovery response; no name/VMID rediscovery or transport substitution is
// permitted here.
func (p *ProxmoxProvider) StartInstanceByRecoveryIdentity(ctx context.Context, identity provider.RecoveryInstanceIdentity) error {
	node := strings.TrimSpace(identity.Node)
	vmid := strings.TrimSpace(identity.ID)
	instanceType := strings.ToLower(strings.TrimSpace(identity.Type))
	if node == "" || vmid == "" || (instanceType != "vm" && instanceType != "container") {
		return fmt.Errorf("Proxmox恢复启动缺少有效node/VMID/type")
	}
	if numeric, err := strconv.Atoi(vmid); err != nil || numeric <= 0 {
		return fmt.Errorf("Proxmox恢复启动VMID无效: %q", vmid)
	}
	if !p.connected {
		return fmt.Errorf("Proxmox provider not connected")
	}

	if p.shouldUseAPI() {
		if err := p.apiStartKnownInstanceAtNode(ctx, node, vmid, instanceType); err == nil {
			return nil
		} else if fallbackErr := p.ensureSSHBeforeFallback(err, "恢复启动实例"); fallbackErr != nil {
			return fallbackErr
		}
	}
	if !p.shouldUseSSH() {
		return fmt.Errorf("Proxmox恢复启动不允许使用SSH")
	}
	// A direct SSH connection normally terminates on the managed PVE node. If
	// discovery returned another cluster node, use pvesh with the explicit node
	// path rather than silently issuing qm/pct against the wrong host.
	if configuredNode := strings.TrimSpace(p.nodeName()); configuredNode == "" || configuredNode == node {
		return p.sshStartKnownInstance(ctx, vmid, instanceType)
	}
	return p.sshStartKnownInstanceViaPvesh(ctx, node, vmid, instanceType)
}

func (p *ProxmoxProvider) sshStartKnownInstanceViaPvesh(ctx context.Context, node, vmid, instanceType string) error {
	kind := "lxc"
	if instanceType == "vm" {
		kind = "qemu"
	}
	base := fmt.Sprintf("/nodes/%s/%s/%s", shellSingleQuote(node), kind, shellSingleQuote(vmid))
	statusCommand := "pvesh get " + base + "/status/current --output-format json"
	startCommand := "pvesh create " + base + "/status/start"
	statusRunning := func(output string) bool {
		var payload struct {
			Status string `json:"status"`
		}
		if json.Unmarshal([]byte(strings.TrimSpace(output)), &payload) == nil {
			return strings.EqualFold(strings.TrimSpace(payload.Status), "running")
		}
		return strings.Contains(strings.ToLower(output), "running")
	}
	if output, err := p.sshClient.Execute(statusCommand); err == nil && statusRunning(output) {
		return nil
	}
	if _, err := p.sshClient.Execute(startCommand); err != nil {
		if output, statusErr := p.sshClient.Execute(statusCommand); statusErr == nil && statusRunning(output) {
			return nil
		}
		return fmt.Errorf("通过pvesh启动%s %s失败: %w", instanceType, vmid, err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, proxmoxStartWaitTimeout(instanceType))
	defer cancel()
	for {
		if output, err := p.sshClient.Execute(statusCommand); err == nil && statusRunning(output) {
			return nil
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("等待pvesh启动%s %s超时: %w", instanceType, vmid, waitCtx.Err())
		case <-time.After(3 * time.Second):
		}
	}
}
