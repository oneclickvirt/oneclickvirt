//go:build firewall_integration

package testutil

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"oneclickvirt/global"
	"oneclickvirt/utils"
)

// FirewallExecutor runs only in the explicitly supplied disposable container.
// Run packages serially (-p 1): their fixtures share this network namespace.
type FirewallExecutor struct {
	utils.ShellExecutor
	container string
}

func NewFirewallExecutor(t *testing.T) *FirewallExecutor {
	t.Helper()
	previousLogger := global.APP_LOG
	if previousLogger == nil {
		global.APP_LOG = zap.NewNop()
		t.Cleanup(func() { global.APP_LOG = previousLogger })
	}
	name := os.Getenv("OCV_FIREWALL_TEST_CONTAINER")
	if !strings.HasPrefix(name, "ocv-firewall-regression-") {
		t.Fatal("OCV_FIREWALL_TEST_CONTAINER must name a disposable ocv-firewall-regression-* container; use scripts/tests/firewall_integration_test.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "docker", "inspect", name).CombinedOutput()
	if err != nil {
		t.Fatalf("inspect test container: %v: %s", err, output)
	}
	var containers []struct {
		State      struct{ Running bool }
		Mounts     []json.RawMessage
		HostConfig struct {
			NetworkMode string
			Privileged  bool
			CapAdd      []string
		}
	}
	if err := json.Unmarshal(output, &containers); err != nil || len(containers) != 1 {
		t.Fatalf("invalid container inspection: %v", err)
	}
	c := containers[0]
	if !c.State.Running || len(c.Mounts) != 0 || c.HostConfig.Privileged || c.HostConfig.NetworkMode == "host" || strings.HasPrefix(c.HostConfig.NetworkMode, "container:") {
		t.Fatal("firewall fixture requires a running container with its own network namespace, no mounts and no privileged mode")
	}
	executor := &FirewallExecutor{container: name}
	reset := func() {
		executor.MustExecute(t, "nft flush ruleset")
		// Legacy xtables rules are not visible to nft. Clear both families in
		// this dedicated namespace between fixtures as well.
		executor.MustExecute(t, `for tool in iptables ip6tables; do
    "$tool" -w 5 -t nat -F || exit 1
    "$tool" -w 5 -t filter -F || exit 1
done`)
	}
	reset()
	t.Cleanup(reset)
	return executor
}

func (e *FirewallExecutor) Execute(command string) (string, error) {
	return e.ExecuteWithTimeout(command, 30*time.Second)
}

func (e *FirewallExecutor) ExecuteWithTimeout(command string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, "docker", "exec", e.container, "sh", "-c", command).CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("container command %q: %w: %s", command, err, output)
	}
	return string(output), nil
}

func (e *FirewallExecutor) MustExecute(t *testing.T, command string) string {
	t.Helper()
	output, err := e.Execute(command)
	if err != nil {
		t.Fatal(err)
	}
	return output
}
