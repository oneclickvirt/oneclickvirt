package proxmox

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

type ipv6CommandExecutor struct {
	commands []string
	fail     func(string) error
}

func (e *ipv6CommandExecutor) Execute(command string) (string, error) {
	e.commands = append(e.commands, command)
	if e.fail != nil {
		return "", e.fail(command)
	}
	return "", nil
}

func (e *ipv6CommandExecutor) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}

func (e *ipv6CommandExecutor) ExecuteWithLogging(command, _ string) (string, error) {
	return e.Execute(command)
}

func (e *ipv6CommandExecutor) ExecuteRaw(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}

func (e *ipv6CommandExecutor) ExecuteViaTempScript(string, []string, time.Duration) (string, error) {
	return "", nil
}

func (e *ipv6CommandExecutor) UploadContent(string, string, os.FileMode) error { return nil }
func (e *ipv6CommandExecutor) IsHealthy() bool                                 { return true }
func (e *ipv6CommandExecutor) Reconnect() error                                { return nil }
func (e *ipv6CommandExecutor) Close() error                                    { return nil }

func TestExecuteIPv6NetworkCommandFallsBackWithoutRate(t *testing.T) {
	executor := &ipv6CommandExecutor{fail: func(command string) error {
		if strings.Contains(command, "rate=") {
			return errors.New("unsupported rate")
		}
		return nil
	}}
	provider := NewProxmoxProvider().(*ProxmoxProvider)
	provider.sshClient.SetExecutor(executor)

	err := provider.executeIPv6NetworkCommand(
		"qm set 101 --net0 virtio,bridge=vmbr2,rate=10",
		"qm set 101 --net0 virtio,bridge=vmbr2",
		"configure net0",
	)
	if err != nil {
		t.Fatalf("executeIPv6NetworkCommand() error = %v", err)
	}
	if len(executor.commands) != 2 || strings.Contains(executor.commands[1], "rate=") {
		t.Fatalf("commands = %#v, want one no-rate fallback", executor.commands)
	}
}

func TestExecuteIPv6NetworkCommandReturnsFallbackFailure(t *testing.T) {
	executor := &ipv6CommandExecutor{fail: func(string) error { return errors.New("command failed") }}
	provider := NewProxmoxProvider().(*ProxmoxProvider)
	provider.sshClient.SetExecutor(executor)

	err := provider.executeIPv6NetworkCommand("primary", "fallback", "configure net0")
	if err == nil || len(executor.commands) != 2 {
		t.Fatalf("error = %v, commands = %#v", err, executor.commands)
	}
}

func TestSetupNATMappingUsesIdempotentRulesAndPersistence(t *testing.T) {
	executor := &ipv6CommandExecutor{}
	provider := NewProxmoxProvider().(*ProxmoxProvider)
	provider.sshClient.SetExecutor(executor)

	if err := provider.setupNATMapping(context.Background(), "2001:db8:1::101/64", "2001:db8:2::101/128"); err != nil {
		t.Fatalf("setupNATMapping() error = %v", err)
	}
	joined := strings.Join(executor.commands, "\n")
	for _, fragment := range []string{
		"ip6tables -t nat -C PREROUTING",
		"|| ip6tables -t nat -A PREROUTING",
		"ip6tables -t nat -C POSTROUTING",
		"|| ip6tables -t nat -A POSTROUTING",
	} {
		if !strings.Contains(joined, fragment) {
			t.Fatalf("commands missing %q:\n%s", fragment, joined)
		}
	}
	persistCount := 0
	for _, command := range executor.commands {
		if strings.Contains(command, "grep -Fqx --") {
			persistCount++
		}
	}
	if persistCount != 2 {
		t.Fatalf("persistence command count = %d, want 2", persistCount)
	}
}

func TestSetupNATMappingPropagatesRuleFailure(t *testing.T) {
	executor := &ipv6CommandExecutor{fail: func(command string) error {
		if strings.Contains(command, "-C PREROUTING") {
			return errors.New("ip6tables unavailable")
		}
		return nil
	}}
	provider := NewProxmoxProvider().(*ProxmoxProvider)
	provider.sshClient.SetExecutor(executor)

	err := provider.setupNATMapping(context.Background(), "2001:db8:1::101", "2001:db8:2::101")
	if err == nil || !strings.Contains(err.Error(), "DNAT") {
		t.Fatalf("setupNATMapping() error = %v, want DNAT failure", err)
	}
	for _, command := range executor.commands {
		if strings.Contains(command, "POSTROUTING") || strings.Contains(command, "grep -Fqx") {
			t.Fatalf("continued after DNAT failure: %#v", executor.commands)
		}
	}
}

func TestSetupNATMappingRejectsInvalidAddressBeforeRemoteCommands(t *testing.T) {
	executor := &ipv6CommandExecutor{}
	provider := NewProxmoxProvider().(*ProxmoxProvider)
	provider.sshClient.SetExecutor(executor)

	if err := provider.setupNATMapping(context.Background(), "not-an-ip", "2001:db8::1"); err == nil {
		t.Fatal("expected invalid internal IPv6 to fail")
	}
	if len(executor.commands) != 0 {
		t.Fatalf("commands = %#v, want no remote commands", executor.commands)
	}
}
