package proxmox

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"oneclickvirt/global"
	coreprovider "oneclickvirt/provider"

	"go.uber.org/zap"
)

type natDataPlaneExecutor struct {
	mu       sync.Mutex
	commands []string
}

func (e *natDataPlaneExecutor) Execute(command string) (string, error) {
	e.mu.Lock()
	e.commands = append(e.commands, command)
	e.mu.Unlock()
	return "ONECLICKVIRT_PVE_NAT_READY\n", nil
}

func (e *natDataPlaneExecutor) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}

func (e *natDataPlaneExecutor) ExecuteWithLogging(command, _ string) (string, error) {
	return e.Execute(command)
}

func (e *natDataPlaneExecutor) ExecuteRaw(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}

func (e *natDataPlaneExecutor) ExecuteViaTempScript(string, []string, time.Duration) (string, error) {
	return "", nil
}

func (e *natDataPlaneExecutor) UploadContent(string, string, os.FileMode) error { return nil }
func (e *natDataPlaneExecutor) IsHealthy() bool                                 { return true }
func (e *natDataPlaneExecutor) Reconnect() error                                { return nil }
func (e *natDataPlaneExecutor) Close() error                                    { return nil }

func (e *natDataPlaneExecutor) commandCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.commands)
}

func natDataPlaneScriptFromInstallCommand(t *testing.T, command string) string {
	t.Helper()
	const marker = "printf '%s' '"
	start := strings.Index(command, marker)
	if start < 0 {
		t.Fatalf("NAT install command has no encoded script: %s", command)
	}
	remainder := command[start+len(marker):]
	end := strings.Index(remainder, "' | base64 -d > \"${script_path}.tmp.$$")
	if end < 0 {
		t.Fatalf("NAT install command has no script terminator: %s", command)
	}
	script, err := base64.StdEncoding.DecodeString(remainder[:end])
	if err != nil {
		t.Fatalf("decode NAT script: %v", err)
	}
	return string(script)
}

func natDataPlaneUnitFromInstallCommand(t *testing.T, command string) string {
	t.Helper()
	const marker = "printf '%s' '"
	start := strings.LastIndex(command, marker)
	if start < 0 {
		t.Fatalf("NAT install command has no encoded unit: %s", command)
	}
	remainder := command[start+len(marker):]
	end := strings.Index(remainder, "' | base64 -d > ")
	if end < 0 {
		t.Fatalf("NAT install command has no unit terminator: %s", command)
	}
	unit, err := base64.StdEncoding.DecodeString(remainder[:end])
	if err != nil {
		t.Fatalf("decode NAT unit: %v", err)
	}
	return string(unit)
}

func TestBuildNATIPv4DataPlaneCommandUsesGenericEgressAndPersistentUnit(t *testing.T) {
	command := buildNATIPv4DataPlaneCommand("vmbr1", "172.16.1.1/24", "172.16.1.0/24")
	script := natDataPlaneScriptFromInstallCommand(t, command)
	unit := natDataPlaneUnitFromInstallCommand(t, command)
	for _, want := range []string{
		"oneclickvirt-pve-nat.service",
		"oneclickvirt_nat",
		"ip address replace",
		"ip saddr \"$subnet\" ip daddr != \"$subnet\" masquerade",
	} {
		if want == "oneclickvirt-pve-nat.service" {
			if !strings.Contains(command, want) {
				t.Fatalf("NAT install command missing %q:\n%s", want, command)
			}
			continue
		}
		if !strings.Contains(script, want) {
			t.Fatalf("NAT script missing %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "oifname") || strings.Contains(script, "/etc/nftables.conf") {
		t.Fatalf("NAT script must not bind to a guessed uplink or reload global nftables: %s", script)
	}
	if !strings.Contains(unit, "Before=pve-guests.service oneclickvirt-agent.service") {
		t.Fatalf("NAT unit must start before guests and Agent: %s", unit)
	}
	for _, want := range []string{
		"systemctl enable \"$unit_name\" >/dev/null 2>&1",
		"if ! systemctl is-active --quiet \"$unit_name\"; then",
		"systemctl start \"$unit_name\"",
		"\"$script_path\"",
		"systemctl is-active --quiet \"$unit_name\"",
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("NAT install command missing readiness confirmation %q:\n%s", want, command)
		}
	}
	for name, shell := range map[string]string{"install": command, "script": script} {
		if output, err := exec.Command("sh", "-n", "-c", shell).CombinedOutput(); err != nil {
			t.Fatalf("%s shell syntax error: %v\n%s", name, err, output)
		}
	}
}

func TestEnsureNATIPv4DataPlaneCoalescesConcurrentCreates(t *testing.T) {
	oldLogger := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = oldLogger })

	executor := &natDataPlaneExecutor{}
	p := NewProxmoxProvider().(*ProxmoxProvider)
	p.sshClient.SetExecutor(executor)
	p.initBridgeNames(coreprovider.NodeConfig{NetworkType: "nat_ipv4_ipv6"})

	config := coreprovider.InstanceConfig{Metadata: map[string]string{"network_type": "nat_ipv4_ipv6"}}
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < cap(errs); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- p.ensureNATIPv4DataPlane(context.Background(), config)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := executor.commandCount(); got != 1 {
		t.Fatalf("NAT reconciliation commands = %d, want 1 for concurrent creates", got)
	}
}

func TestNATIPv4DataPlaneSkipsDedicatedAndIPv6OnlyNetworks(t *testing.T) {
	p := NewProxmoxProvider().(*ProxmoxProvider)
	for _, networkType := range []string{"dedicated_ipv4", "dedicated_ipv4_ipv6", "ipv6_only"} {
		if p.requiresNATIPv4DataPlane(coreprovider.InstanceConfig{Metadata: map[string]string{"network_type": networkType}}) {
			t.Fatalf("network type %q unexpectedly requires NAT IPv4", networkType)
		}
	}
}
