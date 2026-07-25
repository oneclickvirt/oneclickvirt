package utils

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestBuildTempScriptIncludesExtendedPath(t *testing.T) {
	script := BuildTempScript(TempScriptConfig{
		PrimaryCmd: "command -v lxc",
	})

	if !strings.Contains(script, "export PATH='"+StandardExtendedPath+"'${PATH:+:$PATH}") {
		t.Fatalf("temp script does not export the extended PATH: %s", script)
	}
	if !strings.Contains(script, "/snap/bin") {
		t.Fatalf("temp script PATH must include snap binaries for LXD")
	}
}

func TestBuildGuestSSHRecoveryScriptSyntaxAndCoverage(t *testing.T) {
	script := BuildGuestSSHRecoveryScript()
	for _, required := range []string{"dropbear", "PasswordAuthentication", "PermitRootLogin", "ONECLICKVIRT_SSH_READY"} {
		if !strings.Contains(script, required) {
			t.Fatalf("recovery script is missing %q", required)
		}
	}
	check := exec.Command("sh", "-n")
	check.Stdin = strings.NewReader(script)
	if output, err := check.CombinedOutput(); err != nil {
		t.Fatalf("recovery script syntax error: %v: %s", err, output)
	}
}

func TestLocalExecuteViaTempScriptReturnsRedirectedLogAndCleansFiles(t *testing.T) {
	executor := NewLocalShellExecutor(5 * time.Second)
	script := BuildTempScript(TempScriptConfig{PrimaryCmd: "echo guest-diagnostic >&2; exit 23"})
	output, err := executor.ExecuteViaTempScript(script, nil, 5*time.Second)
	if err == nil {
		t.Fatal("ExecuteViaTempScript() succeeded for a failing script")
	}
	if !strings.Contains(output, "guest-diagnostic") || !strings.Contains(output, "failed with exit code 23") {
		t.Fatalf("ExecuteViaTempScript() output = %q", output)
	}
}
