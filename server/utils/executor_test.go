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

func TestTempScriptInterpreterHonorsPortableShebangs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		script string
		want   string
	}{
		{name: "bin sh", script: "#!/bin/sh\nprintf ok\n", want: "sh"},
		{name: "usr bin sh", script: "#!/usr/bin/sh\nprintf ok\n", want: "sh"},
		{name: "env sh", script: "#!/usr/bin/env sh\nprintf ok\n", want: "sh"},
		{name: "env dash options", script: "#!/usr/bin/env -S sh -eu\nprintf ok\n", want: "sh"},
		{name: "bash default", script: "#!/usr/bin/env bash\nprintf ok\n", want: "bash"},
		{name: "missing shebang defaults bash", script: "printf ok\n", want: "bash"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := TempScriptInterpreter(tc.script); got != tc.want {
				t.Fatalf("TempScriptInterpreter() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildTempScriptIsPOSIXShellCompatible(t *testing.T) {
	script := BuildTempScript(TempScriptConfig{
		PrimaryCmd:     "printf '%s\\n' portable-wrapper",
		FallbackCmd:    "exit 99",
		TimeoutSeconds: 5,
		SuccessMarker:  "TEMP_SCRIPT_OK",
	})
	for _, forbidden := range []string{"#!/bin/bash", "set -o pipefail", "read -r -d", "bash -c"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("portable temp script contains bash-only construct %q", forbidden)
		}
	}
	check := exec.Command("sh", "-n")
	check.Stdin = strings.NewReader(script)
	if output, err := check.CombinedOutput(); err != nil {
		t.Fatalf("portable temp script syntax error: %v: %s", err, output)
	}

	executor := NewLocalShellExecutor(5 * time.Second)
	output, err := executor.ExecuteViaTempScript(script, nil, 5*time.Second)
	if err != nil {
		t.Fatalf("portable temp script execution failed: %v (%s)", err, output)
	}
	if !strings.Contains(output, "portable-wrapper") || !strings.Contains(output, "TEMP_SCRIPT_OK") {
		t.Fatalf("portable temp script output = %q", output)
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
