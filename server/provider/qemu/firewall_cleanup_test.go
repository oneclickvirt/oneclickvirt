package qemu

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"oneclickvirt/global"
	"oneclickvirt/utils"
)

type deleteFirewallFailure struct {
	utils.ShellExecutor
	commands []string
}

func (e *deleteFirewallFailure) Execute(command string) (string, error) {
	return e.ExecuteWithTimeout(command, 0)
}
func (e *deleteFirewallFailure) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	e.commands = append(e.commands, command)
	if command == "nft -j -a list ruleset" {
		return "", fmt.Errorf("injected firewall read failure")
	}
	if strings.Contains(command, "get pod") {
		return "", fmt.Errorf("not a k3s container")
	}
	if strings.Contains(command, "_fw_backend") {
		return "nft", nil
	}
	if strings.Contains(command, "command -v") {
		return "", nil
	}
	return "192.0.2.10", nil
}

func TestDeleteKeepsInstanceWhenFirewallCleanupFails(t *testing.T) {
	previousLogger := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = previousLogger })
	executor := &deleteFirewallFailure{}
	p := &QEMUProvider{connected: true, sshClient: utils.NewSafeShellExecutor(executor)}
	for _, lxc := range []bool{false, true} {
		var err error
		if lxc {
			err = p.sshDeleteLXCContainer(context.Background(), "guest")
		} else {
			err = p.sshDeleteInstance(context.Background(), "guest")
		}
		if err == nil {
			t.Fatal("firewall failure did not stop instance deletion")
		}
	}
	for _, command := range executor.commands {
		if strings.Contains(command, "undefine") || strings.Contains(command, "rm -rf") || strings.Contains(command, "kubectl delete") {
			t.Fatalf("deleted instance data after failed firewall cleanup: %s", command)
		}
	}
}
