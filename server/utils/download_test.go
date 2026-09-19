package utils

import (
	"os/exec"
	"strings"
	"testing"
)

func TestBuildRemoteDownloadScriptUsesUniqueTemporaryFile(t *testing.T) {
	script := BuildRemoteDownloadScript(
		"https://example.test/image.tar",
		"/var/lib/oneclickvirt/image.tar.tmp",
		"/var/lib/oneclickvirt/image.tar",
	)
	for _, fragment := range []string{
		`tmp_base='/var/lib/oneclickvirt/image.tar.tmp'`,
		`tmp="$(mktemp "${tmp_base}.XXXXXX")"`,
		`trap 'rm -f -- "$tmp"' EXIT`,
		`mv -f "$tmp" "$dst"`,
	} {
		if !strings.Contains(script, fragment) {
			t.Fatalf("download script missing %q:\n%s", fragment, script)
		}
	}
	if strings.Contains(script, `tmp='/var/lib/oneclickvirt/image.tar.tmp'`) {
		t.Fatalf("download script reverted to a shared fixed temporary path:\n%s", script)
	}
	check := exec.Command("bash", "-n")
	check.Stdin = strings.NewReader(script)
	if output, err := check.CombinedOutput(); err != nil {
		t.Fatalf("generated download script is not valid bash: %v\n%s\n%s", err, output, script)
	}
}
