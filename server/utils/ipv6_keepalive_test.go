//go:build linux || darwin

package utils

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// macOS does not ship flock(1). Use the same kernel lock on its inherited open
// descriptor instead; Linux runs the installed flock utility without a shim.
func TestIPv6KeepaliveLockHelper(t *testing.T) {
	if os.Getenv("OCV_TEST_KEEPALIVE_LOCK_HELPER") != "yes" {
		return
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := unix.Flock(9, unix.LOCK_EX|unix.LOCK_NB); err == nil {
			os.Exit(0)
		}
		time.Sleep(10 * time.Millisecond)
	}
	os.Exit(1)
}

func TestIPv6KeepalivePreservesHostJobs(t *testing.T) {
	for _, scenario := range []string{"create", "repeat", "concurrent", "custom", "symlink", "directory", "cron_dir_symlink", "lock_dir_symlink", "missing_flock", "write_failure"} {
		t.Run(scenario, func(t *testing.T) {
			directory := t.TempDir()
			cronDir := filepath.Join(directory, "cron.d")
			lockDir := filepath.Join(directory, "lock")
			binDir := filepath.Join(directory, "bin")
			for _, path := range []string{cronDir, lockDir, binDir} {
				if err := os.Mkdir(path, 0755); err != nil {
					t.Fatal(err)
				}
			}
			write := func(path, content string, mode os.FileMode) {
				t.Helper()
				if err := os.WriteFile(path, []byte(content), mode); err != nil {
					t.Fatal(err)
				}
			}
			// Invoking crontab is forbidden: the job must be separate from root's
			// spool, not a read/modify/write race disguised as preservation.
			write(filepath.Join(binDir, "crontab"), "#!/bin/sh\necho forbidden-crontab-invocation >&2\nexit 91\n", 0755)
			write(filepath.Join(binDir, "curl"), "#!/bin/sh\necho forbidden-network-request >&2\nexit 92\n", 0755)
			env := append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"))
			if _, err := exec.LookPath("flock"); err != nil {
				binary, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				write(filepath.Join(binDir, "flock"), "#!/bin/sh\nexec '"+strings.ReplaceAll(binary, "'", "'\\''")+"' -test.run='^TestIPv6KeepaliveLockHelper$'\n", 0755)
				env = append(env, "OCV_TEST_KEEPALIVE_LOCK_HELPER=yes")
				// The race runtime normally sleeps one second at process exit.
				// flock(1) does not: retaining this artificial delay would hold
				// each inherited lock after the helper has already succeeded.
				env = append(env, "GORACE="+os.Getenv("GORACE")+" atexit_sleep_ms=0")
			}
			unrelated := filepath.Join(cronDir, "user-backup")
			original := "17 3 * * * root /usr/local/bin/backup\n"
			write(unrelated, original, 0600)
			target := filepath.Join(cronDir, "oneclickvirt-ipv6-keepalive")
			command := strings.ReplaceAll(IPv6KeepaliveInstallCommand(), "/etc/cron.d", cronDir)
			command = strings.ReplaceAll(command, "/run/lock", lockDir)
			wantFailure := false
			switch scenario {
			case "custom":
				write(target, "# Managed by OneClickVirt: IPv6 path keepalive\n# custom administrator policy\n", 0600)
				wantFailure = true
			case "symlink":
				if err := os.Symlink(unrelated, target); err != nil {
					t.Fatal(err)
				}
				wantFailure = true
			case "directory":
				if err := os.Mkdir(target, 0700); err != nil {
					t.Fatal(err)
				}
				wantFailure = true
			case "cron_dir_symlink":
				if err := os.RemoveAll(cronDir); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(lockDir, cronDir); err != nil {
					t.Fatal(err)
				}
				wantFailure = true
			case "lock_dir_symlink":
				if err := os.RemoveAll(lockDir); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(cronDir, lockDir); err != nil {
					t.Fatal(err)
				}
				wantFailure = true
			case "missing_flock":
				command = strings.Replace(command, "command -v flock", "command -v ocv_missing_flock_54bdfdc", 1)
				wantFailure = true
			case "write_failure":
				write(filepath.Join(binDir, "mv"), "#!/bin/sh\nexit 93\n", 0755)
				wantFailure = true
			}
			run := func() error {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
				cmd.Env = env
				output, err := cmd.CombinedOutput()
				if err != nil {
					return fmt.Errorf("%w: %s", err, output)
				}
				return nil
			}
			if err := run(); (err != nil) != wantFailure {
				t.Fatalf("run error = %v, want failure %v", err, wantFailure)
			}
			if scenario == "repeat" {
				if err := run(); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "concurrent" {
				if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}
				var wg sync.WaitGroup
				for index := 0; index < 12; index++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						if err := run(); err != nil {
							t.Errorf("concurrent installation: %v", err)
						}
					}()
				}
				wg.Wait()
			}
			if scenario != "cron_dir_symlink" {
				if got, err := os.ReadFile(unrelated); err != nil || string(got) != original {
					t.Fatalf("unrelated cron job was changed: %q, %v", got, err)
				}
			}
			if !wantFailure {
				got, err := os.ReadFile(target)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Count(string(got), "*/1 * * * * root ") != 1 || strings.Count(string(got), " -6 ") != 2 || !strings.HasSuffix(string(got), "\n") {
					t.Fatalf("invalid or duplicated cron job: %s", got)
				}
				if info, err := os.Stat(target); err != nil || info.Mode().Perm() != 0644 {
					t.Fatalf("invalid cron file mode: %v, %v", info, err)
				}
			}
			if scenario == "custom" {
				got, _ := os.ReadFile(target)
				if !strings.Contains(string(got), "custom administrator policy") {
					t.Fatal("custom policy was replaced")
				}
			}
			if scenario == "symlink" {
				if got, err := os.Readlink(target); err != nil || got != unrelated {
					t.Fatal("symlink was replaced")
				}
			}
			if scenario == "missing_flock" || scenario == "write_failure" || scenario == "cron_dir_symlink" || scenario == "lock_dir_symlink" {
				if _, err := os.Lstat(target); !os.IsNotExist(err) {
					t.Fatal("failed install left a target file")
				}
			}
			if temporary, err := filepath.Glob(filepath.Join(cronDir, ".oneclickvirt-ipv6-keepalive.*")); err != nil || len(temporary) != 0 {
				t.Fatalf("temporary files remain: %v, %v", temporary, err)
			}
		})
	}
}
