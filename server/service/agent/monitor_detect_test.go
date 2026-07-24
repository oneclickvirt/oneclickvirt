package agent

import "testing"

func TestParseDetectedInterfaceRejectsPollutedOutput(t *testing.T) {
	if got, err := parseDetectedInterface("veth1234\r\n"); err != nil || got != "veth1234" {
		t.Fatalf("parseDetectedInterface() = %q, %v", got, err)
	}

	for _, output := range []string{
		"Attempting to detect interface...\nveth1234",
		"veth1234\nwarning: fallback used",
		"veth1234 veth5678",
		"ERROR: no interface",
		"veth1234;touch-/tmp/x",
	} {
		if got, err := parseDetectedInterface(output); err == nil {
			t.Fatalf("parseDetectedInterface(%q) = %q, want error", output, got)
		}
	}
}
