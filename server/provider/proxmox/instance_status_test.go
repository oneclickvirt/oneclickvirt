package proxmox

import (
	"testing"
)

func TestPVEStateParsingRejectsAmbiguousOutput(t *testing.T) {
	if parsePVEStatusOutput("\x1b[32mstatus: stopped\x1b[0m\r\n") != "stopped" {
		t.Fatal("colored state not normalized")
	}
	for _, output := range []string{"status: running\nstatus: stopped", "not status: running", "状态: 运行中", "status: läuft"} {
		if parsePVEStatusOutput(output) != "unknown" {
			t.Fatalf("ambiguous state accepted: %q", output)
		}
	}
}

func TestAgentAutomaticRoutingUsesNodeExecutor(t *testing.T) {
	p := NewProxmoxProvider().(*ProxmoxProvider)
	p.config.TokenID, p.config.Token = "user!token", "fixture"
	p.reverseAgent, p.connected = true, true
	for _, rule := range []string{"auto", " AUTO ", "ssh_only", " SSH_ONLY "} {
		p.config.ExecutionRule = rule
		if p.shouldUseAPI() || !p.shouldUseSSH() {
			t.Fatalf("Agent rule %q used controller-side API", rule)
		}
	}
	p.config.ExecutionRule = " API_ONLY "
	if !p.shouldUseAPI() || p.shouldUseSSH() {
		t.Fatal("explicit API-only rule not respected")
	}
}
