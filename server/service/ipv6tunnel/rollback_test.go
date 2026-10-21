package ipv6tunnel

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"oneclickvirt/utils"
)

func TestFailedTunnelStartCleansStagedInterfaceBeforeRestoringFiles(t *testing.T) {
	for _, previous := range []bool{false, true} {
		t.Run(fmt.Sprintf("previous=%t", previous), func(t *testing.T) {
			root := t.TempDir()
			config := validTunnelConfig().toModel(1)
			config.ID = 42
			command := buildApplyCommand(config)
			start := strings.Index(command, "rollback() {")
			if start < 0 {
				t.Fatal("rollback function missing")
			}
			end := strings.Index(command[start:], "trap rollback EXIT")
			if end < 0 {
				t.Fatal("rollback function missing")
			}
			cleanup := filepath.Join(root, "cleanup")
			marker := filepath.Join(root, "interface")
			if err := os.WriteFile(marker, []byte("staged"), 0600); err != nil {
				t.Fatal(err)
			}
			var setup strings.Builder
			setup.WriteString("set -eu\nunit=fixture\ntunnel_interface=fixture0\nstaged_script=1\n")
			setup.WriteString("systemctl() { :; }\nreload_networkd() { :; }\nip() { return 1; }\n")
			for _, name := range []string{"unit", "script", "neighbor", "network"} {
				path := filepath.Join(root, name)
				body := "staged"
				if name == "script" {
					body = "#!/bin/sh\n[ \"$1\" = down ] || exit 2\nrm -f " + utils.ShellSingleQuote(marker) + "\nprintf 'down\\n' > " + utils.ShellSingleQuote(cleanup) + "\n"
				}
				if err := os.WriteFile(path, []byte(body), 0700); err != nil {
					t.Fatal(err)
				}
				flag := 0
				if previous {
					flag = 1
					if err := os.WriteFile(path+".backup", []byte("previous-"+name), 0600); err != nil {
						t.Fatal(err)
					}
				}
				fmt.Fprintf(&setup, "%s_path=%s\n%s_backup=%s\nhad_%s=%d\n", name, utils.ShellSingleQuote(path), name, utils.ShellSingleQuote(path+".backup"), name, flag)
			}
			setup.WriteString("was_active=0\n")
			setup.WriteString(command[start : start+end])
			setup.WriteString("trap rollback EXIT\n(exit 7)\n")
			out, err := exec.Command("sh", "-c", setup.String()).CombinedOutput()
			if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 7 {
				t.Fatalf("original failure was lost: %v: %s", err, out)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("failed start left an orphan interface: %v", err)
			}
			if body, err := os.ReadFile(cleanup); err != nil || string(body) != "down\n" {
				t.Fatalf("staged cleanup was not executed: %q: %v", body, err)
			}
			for _, name := range []string{"unit", "script", "neighbor", "network"} {
				body, err := os.ReadFile(filepath.Join(root, name))
				if previous {
					if err != nil || string(body) != "previous-"+name {
						t.Fatalf("previous %s was not restored: %q: %v", name, body, err)
					}
				} else if !os.IsNotExist(err) {
					t.Fatalf("new %s file survived rollback: %v", name, err)
				}
			}
		})
	}
}
