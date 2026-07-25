package proxmox

import "testing"

func TestParseProxmoxVMIDOutputTriesValidLines(t *testing.T) {
	for _, output := range []string{"101\r\n", "Looking up guest...\n101", "101\n102"} {
		if got, err := parseProxmoxVMIDOutput(output); err != nil || got != "101" {
			t.Fatalf("parseProxmoxVMIDOutput(%q) = %q, %v", output, got, err)
		}
	}
	for _, output := range []string{
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
