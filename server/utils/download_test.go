package utils

import (
	"os"
	"os/exec"
	"path/filepath"
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
		`finish() {`,
		`printf 'TEMP_SCRIPT_FAILED\n' > "${MARKER_FILE:-$0.marker}"`,
		`trap finish 0`,
		`rm -f -- "$tmp" 2>/dev/null || true`,
		`mv -f "$tmp" "$dst"`,
		`echo TEMP_SCRIPT_OK > "${MARKER_FILE:-$0.marker}"`,
	} {
		if !strings.Contains(script, fragment) {
			t.Fatalf("download script missing %q:\n%s", fragment, script)
		}
	}
	if strings.Contains(script, `tmp='/var/lib/oneclickvirt/image.tar.tmp'`) {
		t.Fatalf("download script reverted to a shared fixed temporary path:\n%s", script)
	}
	if !strings.HasPrefix(script, "#!/bin/sh\n") {
		t.Fatalf("generated download script must use the portable shell interpreter: %s", script)
	}
	check := exec.Command("sh", "-n")
	check.Stdin = strings.NewReader(script)
	if output, err := check.CombinedOutput(); err != nil {
		t.Fatalf("generated download script is not valid bash: %v\n%s\n%s", err, output, script)
	}
}

func TestBuildRemoteDownloadScriptRunsWithPortableShellAndWgetFallback(t *testing.T) {
	directory := t.TempDir()
	bin := filepath.Join(directory, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// Force the first method to fail and make the POSIX wget fallback write a
	// deterministic payload without depending on the network in unit tests.
	if err := os.WriteFile(filepath.Join(bin, "curl"), []byte("#!/bin/sh\nexit 127\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	wget := "#!/bin/sh\nout=''\nwhile [ \"$#\" -gt 0 ]; do\n  if [ \"$1\" = '-O' ]; then shift; out=$1; fi\n  shift\ndone\nprintf portable-download > \"$out\"\n"
	if err := os.WriteFile(filepath.Join(bin, "wget"), []byte(wget), 0o755); err != nil {
		t.Fatal(err)
	}
	tmpPath := filepath.Join(directory, "payload.txt.tmp")
	dstPath := filepath.Join(directory, "payload.txt")
	markerPath := filepath.Join(directory, "payload.marker")
	logPath := filepath.Join(directory, "payload.log")
	scriptPath := filepath.Join(directory, "download.sh")
	script := BuildRemoteDownloadScript("https://example.test/payload", tmpPath, dstPath)
	// StandardExtendedPath is deliberately prepended by the production script;
	// put the test doubles first while retaining the system utilities.
	script = strings.Replace(script,
		"export PATH='"+StandardExtendedPath+"'",
		"export PATH='"+bin+":"+StandardExtendedPath+"'", 1)
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sh", scriptPath)
	command.Env = append(os.Environ(), "MARKER_FILE="+markerPath, "LOG_FILE="+logPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated POSIX download script failed: %v\n%s", err, output)
	}
	marker, err := os.ReadFile(markerPath)
	if err != nil || strings.TrimSpace(string(marker)) != "TEMP_SCRIPT_OK" {
		t.Fatalf("success marker = %q, err=%v", marker, err)
	}
	payload, err := os.ReadFile(dstPath)
	if err != nil || string(payload) != "portable-download" {
		t.Fatalf("downloaded payload = %q, err=%v", payload, err)
	}
}
