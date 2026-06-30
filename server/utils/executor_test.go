package utils

import (
	"strings"
	"testing"
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
