package utils

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvironmentWrappersUseMatchingShellAndPreserveSystemConfiguration(t *testing.T) {
	root := t.TempDir()
	fixtures := map[string]string{
		"etc/environment":                   "export OCV_ENV_FILE=system\n",
		"etc/profile":                       "export OCV_PROFILE=profile\n",
		"etc/profile.d/fixture.sh":          "export OCV_FRAGMENT=fragment\n",
		"etc/bash.bashrc":                   "_ocv_array=(one two)\nexport OCV_RC=bash\n",
		"etc/zsh/zprofile":                  "export OCV_RC=zsh\n",
		"etc/zsh/zshrc":                     "export OCV_RC=zsh\n",
		"user/.profile":                     "export OCV_USER_PROFILE=user\n",
		"user/.bash_profile":                "export OCV_USER_RC=bash\n",
		"user/.bashrc":                      "_ocv_user_array=(one two)\nexport OCV_USER_RC=bash\n",
		"user/.zprofile":                    "export OCV_USER_RC=zsh\n",
		"user/.zshrc":                       "export OCV_USER_RC=zsh\n",
		"user/.config/environment.d/a.conf": "export OCV_USER_FRAGMENT=user-fragment\n",
	}
	for name, content := range fixtures {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Redirect only profile locations into fixtures. The actual wrapper still
	// executes in each available shell, including dash's fatal source semantics.
	replace := strings.NewReplacer("/etc/", root+"/etc/", "~/", root+"/user/")
	shells := []string{"sh"}
	for _, shell := range []string{"dash", "bash", "zsh"} {
		if _, err := exec.LookPath(shell); err == nil {
			shells = append(shells, shell)
		}
	}
	for _, shell := range shells {
		t.Run(shell, func(t *testing.T) {
			kind, err := exec.Command(shell, "-c", `if [ -n "${BASH_VERSION:-}" ]; then printf bash; elif [ -n "${ZSH_VERSION:-}" ]; then printf zsh; else printf unset; fi`).Output()
			if err != nil {
				t.Fatal(err)
			}
			for _, mode := range []string{"agent-system-only", "ssh-user", "shell-prefix"} {
				t.Run(mode, func(t *testing.T) {
					command := `printf '%s|%s|%s|%s|%s|%s|%s|%s' "$OCV_ENV_FILE" "$OCV_PROFILE" "$OCV_FRAGMENT" "${OCV_RC-unset}" "${OCV_USER_PROFILE-unset}" "${OCV_USER_RC-unset}" "${OCV_USER_FRAGMENT-unset}" "$PATH"`
					script := BuildEnvCommandNoUser(command)
					userFields := "unset|unset|unset"
					if mode != "agent-system-only" {
						script = BuildEnvCommand(command)
						userFields = "user|" + string(kind) + "|user-fragment"
						if mode == "shell-prefix" {
							script = EnvPrefixShell + command
						}
					}
					script = "unset OCV_ENV_FILE OCV_PROFILE OCV_FRAGMENT OCV_RC OCV_USER_PROFILE OCV_USER_RC OCV_USER_FRAGMENT; " + replace.Replace(script)
					out, err := exec.Command(shell, "-c", script).Output()
					if err != nil {
						t.Fatalf("environment wrapper terminated before the command: %v", err)
					}
					want := "system|profile|fragment|" + string(kind) + "|" + userFields + "|" + StandardExtendedPath
					if !strings.HasPrefix(string(out), want) {
						t.Fatalf("environment = %q, want prefix %q", out, want)
					}
				})
			}
		})
	}
}
