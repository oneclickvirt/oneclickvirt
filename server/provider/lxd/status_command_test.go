package lxd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestLXDInstanceStatusWithoutOptionalNodeTools(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	awk, err := exec.LookPath("awk")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, rows, want string
		cliExit          int
		wantError        bool
	}{
		{name: "running exact name", rows: "guest-other,STOPPED\nguest,RUNNING\n", want: "RUNNING"},
		{name: "stopped", rows: "guest,STOPPED\n", want: "STOPPED"},
		{name: "transition", rows: "guest,STARTING\n", want: "OTHER"},
		{name: "missing", rows: "guest-other,RUNNING\n", wantError: true},
		{name: "duplicate", rows: "guest,RUNNING\nguest,STOPPED\n", wantError: true},
		{name: "empty state", rows: "guest,\n", wantError: true},
		{name: "lxc failure keeps diagnostics", cliExit: 23, want: "COLOURED_ERROR", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Symlink(awk, filepath.Join(dir, "awk")); err != nil {
				t.Fatal(err)
			}
			// The lxc fixture rejects nonexistent status-code columns (S is
			// snapshot count), and PATH intentionally contains neither jq nor
			// python. Diagnostics remain available even with translated text.
			script := "#!/bin/sh\n" +
				"[ \"$*\" = 'list guest --format csv -c ns' ] || { echo bad-cli-arguments >&2; exit 91; }\n" +
				"[ \"$LC_ALL\" = C ] && [ \"$NO_COLOR\" = 1 ] || exit 92\n" +
				"if [ \"$OCV_STATUS_EXIT\" != 0 ]; then printf '\\033[31mCOLOURED_ERROR\\033[0m\\n' >&2; exit \"$OCV_STATUS_EXIT\"; fi\n" +
				"printf '%s' \"$OCV_STATUS_ROWS\"\n"
			if err := os.WriteFile(filepath.Join(dir, "lxc"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(shell, "-c", lxdInstanceStatusCommand("guest"))
			command.Env = append(os.Environ(), "PATH="+dir, "OCV_STATUS_ROWS="+tc.rows,
				"OCV_STATUS_EXIT="+strconv.Itoa(tc.cliExit))
			output, err := command.CombinedOutput()
			if (err != nil) != tc.wantError {
				t.Fatalf("status command = %q, err = %v", output, err)
			}
			if tc.wantError {
				if tc.want != "" && !strings.Contains(string(output), tc.want) {
					t.Fatalf("missing diagnostics: %q", output)
				}
			} else if strings.TrimSpace(string(output)) != tc.want {
				t.Fatalf("status = %q; want %q", output, tc.want)
			}
		})
	}
}
