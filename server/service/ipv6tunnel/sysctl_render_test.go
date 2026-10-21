package ipv6tunnel

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"oneclickvirt/utils"
)

// Shell syntax checks cannot detect escaped newlines accepted as part of a
// sysctl value. Execute the rendered writer and validate the resulting file.
func TestRenderedRoutedSysctlFileHasSeparateNumericValues(t *testing.T) {
	for _, mode := range []string{"sit", "gre"} {
		t.Run(mode, func(t *testing.T) {
			config := validTunnelConfig()
			config.Mode = mode
			tunnel := config.toModel(1)
			tunnel.ID = 42
			script := renderTunnelScript(tunnel)
			start := strings.Index(script, "  {\n    for uplink in $ra_interfaces;")
			if start < 0 {
				t.Fatal("sysctl writer missing")
			}
			ending := "  } > \"$SYSCTL_PATH\""
			end := strings.Index(script[start:], ending)
			if end < 0 {
				t.Fatal("sysctl writer incomplete")
			}
			path := filepath.Join(t.TempDir(), "tunnel.conf")
			command := "set -eu\nIFACE=gre0\nBRIDGE=oneclickvirt6\nra_interfaces=lo\nSYSCTL_PATH=" + utils.ShellSingleQuote(path) + "\n" + script[start:start+end+len(ending)]
			if out, err := exec.Command("sh", "-c", command).CombinedOutput(); err != nil {
				t.Fatalf("sysctl writer failed: %v: %s", err, out)
			}
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(body)), "\n")
			want := 7
			if _, err := os.Stat("/proc/sys/net/ipv6/conf/lo/accept_ra"); err == nil {
				want++
			}
			if len(lines) != want {
				t.Fatalf("sysctl lines = %d, want %d: %q", len(lines), want, body)
			}
			valid := regexp.MustCompile(`^net\.ipv6\.conf\.[A-Za-z0-9_.-]+=(1|2)$`)
			for _, line := range lines {
				if !valid.MatchString(line) {
					t.Fatalf("invalid sysctl entry: %q", line)
				}
			}
		})
	}
}
