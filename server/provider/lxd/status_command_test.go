package lxd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLXDInstanceStatusIgnoresLocalizedInfoOutput(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is required by the LXD shell integration")
	}
	dir := t.TempDir()
	cli := filepath.Join(dir, "lxc")
	script := "#!/bin/sh\ncase \"$1\" in\n  list) printf '%s\\n' '[{\"name\":\"guest\",\"status_code\":103},{\"name\":\"guest-other\",\"status_code\":102}]' ;;\n  info) printf '\\033[31m状态: 运行中\\033[0m\\n'; exit 99 ;;\n  *) exit 99 ;;\nesac\n"
	if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sh", "-c", lxdInstanceStatusCommand("guest"))
	command.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"))
	output, err := command.CombinedOutput()
	if err != nil || strings.TrimSpace(string(output)) != "RUNNING" {
		t.Fatalf("status command = %q, err = %v", output, err)
	}
}
