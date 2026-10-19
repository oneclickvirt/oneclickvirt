package kubevirt

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyK3sContainerNamesUseMaintainedImageRepository(t *testing.T) {
	for _, system := range []string{"ubuntu", "debian", "alpine", "almalinux", "rockylinux", "openeuler"} {
		if got := normalizeK3sImageRef("spiritlhl-" + system); got != "ghcr.io/oneclickvirt/docker:"+system {
			t.Fatalf("legacy %s resolves to %s", system, got)
		}
	}
	for _, explicit := range []string{"ghcr.io/oneclickvirt/docker:debian", "registry.example/private/custom:v2", "debian:12"} {
		if got := normalizeK3sImageRef(explicit); got != explicit {
			t.Fatalf("explicit image reference changed: %s", got)
		}
	}
}

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

func TestCtrImportSupportsOldAndNewOutputAndRequiresUsableReference(t *testing.T) {
	for _, tc := range []struct {
		name                                      string
		index, importCode, tagCode, missing, want int
	}{
		{"new saved output", 1, 0, 0, 0, 0},
		{"old unpacking output", 0, 0, 0, 0, 0},
		{"import rejected", 1, 17, 0, 0, 17},
		{"legacy tag rejected", 0, 0, 21, 0, 21},
		{"target absent after successful import", 1, 0, 0, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := `set -e
source_ref=docker.io/library/fixture:v1
reference=
fake_ctr(){
 case "$*" in
  'images import --help')
   if [ "$HAS_INDEX" = 1 ];then echo '  --index-name string';fi ;;
  "images import --index-name $target_ref $import_src")
   [ "$IMPORT_CODE" = 0 ] || return "$IMPORT_CODE"
   if [ "$MISSING" = 0 ];then reference=$target_ref;fi
   printf '%s saved\n' "$source_ref" ;;
  "images import $import_src")
   [ "$IMPORT_CODE" = 0 ] || return "$IMPORT_CODE"
   printf 'unpacking %s (sha256:fixture)...done\n' "$source_ref" ;;
  "images tag $source_ref $target_ref")
   [ "$TAG_CODE" = 0 ] || return "$TAG_CODE"
   reference=$target_ref ;;
  'images list -q') printf '%s\n' "$reference" ;;
  *) echo 'unexpected containerd arguments' >&2;return 81 ;;
 esac
}
` + "load_log=" + shellSingleQuote(filepath.Join(t.TempDir(), "loader log")) + "\ntarget_ref=localhost/oneclickvirt/fixture:latest\nimport_src='archive with spaces.tar'\n" + kubeVirtContainerImageLoaderFunction() + "\nrun_ctr_import fixture fake_ctr\n"
			cmd := exec.Command("sh", "-c", script)
			cmd.Env = append(os.Environ(), fmt.Sprintf("HAS_INDEX=%d", tc.index), fmt.Sprintf("IMPORT_CODE=%d", tc.importCode), fmt.Sprintf("TAG_CODE=%d", tc.tagCode), fmt.Sprintf("MISSING=%d", tc.missing))
			out, err := cmd.CombinedOutput()
			code := 0
			if err != nil {
				if e, ok := err.(*exec.ExitError); ok {
					code = e.ExitCode()
				} else {
					t.Fatal(err)
				}
			}
			if code != tc.want {
				t.Fatalf("exit %d want %d: %s", code, tc.want, out)
			}
		})
	}
}
