package lxd

import (
	"strings"
	"testing"
)

func TestPasswordExecCommandsDisableNestedPTY(t *testing.T) {
	for _, command := range buildLXDPasswordCommandCandidates("guest", "p@ss word", "sh") {
		if strings.Contains(command, "lxc exec ") && !strings.Contains(command, "lxc exec -T ") {
			t.Fatalf("automated password command may inherit the controller PTY: %q", command)
		}
		// The historical fallback deliberately pipes the password into lxc exec;
		// that stdin is intentional. Direct shell candidates must be detached
		// from the controller's stdin as well as from its PTY.
		if strings.Contains(command, "lxc exec -T ") &&
			!strings.Contains(command, "base64 -d | lxc exec") &&
			!strings.Contains(command, "</dev/null") {
			t.Fatalf("automated password command may consume the controller stdin: %q", command)
		}
	}
	legacy := buildLXDChpasswdCommand("guest", "p@ss word")
	if !strings.Contains(legacy, "lxc exec -T ") {
		t.Fatalf("legacy password fallback must disable the nested PTY: %q", legacy)
	}
}
