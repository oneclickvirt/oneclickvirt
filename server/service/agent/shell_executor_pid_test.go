package agent

import "testing"

func TestParseAgentPIDRejectsShellSyntax(t *testing.T) {
	for _, raw := range []string{"", "0", "-1", "123; touch /tmp/pwned", "123\n456", "pid=123"} {
		if got, err := parseAgentPID(raw); err == nil || got != "" {
			t.Fatalf("parseAgentPID(%q) = (%q, %v), want rejection", raw, got, err)
		}
	}
}

func TestParseAgentPIDNormalizesPositiveInteger(t *testing.T) {
	got, err := parseAgentPID("  0042 \n")
	if err != nil || got != "42" {
		t.Fatalf("parseAgentPID() = (%q, %v), want 42", got, err)
	}
}
