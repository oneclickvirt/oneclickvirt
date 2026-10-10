package proxmox

import (
	"context"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/utils"
)

func parsePVEStatusOutput(output string) string {
	state := strings.TrimSpace(utils.StripTerminalCSI(output))
	if state == "status: running" {
		return "running"
	}
	if state == "status: stopped" {
		return "stopped"
	}
	if state == "status: paused" {
		return "paused"
	}
	return "unknown"
}

func (p *ProxmoxProvider) waitStopped(ctx context.Context, vmid, instanceType string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cli := "pct"
	if instanceType == "vm" {
		cli = "qm"
	}
	for {
		output, err := utils.ExecuteShellCommandContext(ctx, p.sshClient, fmt.Sprintf("LC_ALL=C NO_COLOR=1 %s status %s", cli, vmid))
		if err != nil {
			return fmt.Errorf("verify stopped state: %w; output: %s", err, utils.TruncateString(output, 1000))
		}
		state := parsePVEStatusOutput(output)
		if state == "stopped" {
			return nil
		}
		if state == "unknown" {
			return fmt.Errorf("unexpected Proxmox status output: %q", utils.TruncateString(output, 1000))
		}
		if err := utils.SleepContext(ctx, time.Second); err != nil {
			return fmt.Errorf("stop not confirmed for %s: %w", vmid, err)
		}
	}
}
