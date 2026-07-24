package proxmox

import "testing"

func TestParseProxmoxVMIDOutputRejectsPollution(t *testing.T) {
	if got, err := parseProxmoxVMIDOutput("101\r\n"); err != nil || got != "101" {
		t.Fatalf("parseProxmoxVMIDOutput() = %q, %v", got, err)
	}
	for _, output := range []string{
		"Looking up guest...\n101",
		"101\n102",
		"101 warning",
		"0",
		"-1",
		"vm101",
	} {
		if got, err := parseProxmoxVMIDOutput(output); err == nil {
			t.Fatalf("parseProxmoxVMIDOutput(%q) = %q, want error", output, got)
		}
	}
}
