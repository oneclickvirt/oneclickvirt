package proxmox

import (
	"os/exec"
	"strings"
	"testing"
)

func TestUnprivilegedTemplateNativeArchiveCompatibility(t *testing.T) {
	version, err := exec.Command("tar", "--version").Output()
	if err != nil || !strings.Contains(string(version), "GNU tar") {
		t.Skip("native archive verification requires GNU tar (Linux CI)")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("native archive verification requires Python 3")
	}
	output, err := exec.Command("python3", "testdata/unprivileged_template_test.py").CombinedOutput()
	if err != nil {
		t.Fatalf("template archive regression: %v\n%s", err, output)
	}
}
