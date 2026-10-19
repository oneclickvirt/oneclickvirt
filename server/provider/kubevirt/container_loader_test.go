package kubevirt

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestContainerImageLoaderPropagatesFailureAndSupportsFallback(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		wantCode   int
	}{
		{"failed import", `run_loader primary sh -c 'printf "bad archive\n"; exit 17'`, 17},
		{"verified fallback", `run_loader primary sh -c 'exit 17' || run_loader fallback sh -c 'printf "Loaded image: fixture\n"'`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := "set -e\nload_log=" + shellSingleQuote(filepath.Join(t.TempDir(), "import log")) + "\n" + kubeVirtContainerImageLoaderFunction() + "\n" + tc.body + "\n"
			out, err := exec.Command("sh", "-c", script).CombinedOutput()
			code := 0
			if err != nil {
				if status, ok := err.(*exec.ExitError); ok {
					code = status.ExitCode()
				} else {
					t.Fatal(err)
				}
			}
			if code != tc.wantCode {
				t.Fatalf("loader exit %d, want %d; %s", code, tc.wantCode, out)
			}
			if !strings.Contains(string(out), "rc=17") {
				t.Fatalf("original loader status was lost: %s", out)
			}
		})
	}
}
