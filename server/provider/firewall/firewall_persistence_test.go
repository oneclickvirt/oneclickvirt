package firewall

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveRulesDistributionLayouts(t *testing.T) {
	for _, tt := range []struct {
		id, like, nft, v4, v6 string
	}{
		{"debian", "", "/etc/nftables.conf", "/etc/iptables/rules.v4", "/etc/iptables/rules.v6"},
		{"ubuntu", "debian", "/etc/nftables.conf", "/etc/iptables/rules.v4", "/etc/iptables/rules.v6"},
		{"fedora", "", "/etc/sysconfig/nftables.conf", "/etc/sysconfig/iptables", "/etc/sysconfig/ip6tables"},
		{"centos", "rhel fedora", "/etc/sysconfig/nftables.conf", "/etc/sysconfig/iptables", "/etc/sysconfig/ip6tables"},
		{"rocky", "rhel", "/etc/sysconfig/nftables.conf", "/etc/sysconfig/iptables", "/etc/sysconfig/ip6tables"},
		{"almalinux", "rhel", "/etc/sysconfig/nftables.conf", "/etc/sysconfig/iptables", "/etc/sysconfig/ip6tables"},
		{"amzn", "fedora", "/etc/sysconfig/nftables.conf", "/etc/sysconfig/iptables", "/etc/sysconfig/ip6tables"},
		{"arch", "", "/etc/nftables.conf", "/etc/iptables/iptables.rules", "/etc/iptables/ip6tables.rules"},
		{"manjaro", "arch", "/etc/nftables.conf", "/etc/iptables/iptables.rules", "/etc/iptables/ip6tables.rules"},
		{"alpine", "", "/etc/nftables.nft", "/etc/iptables/rules-save", "/etc/iptables/rules6-save"},
		{"derived", "rhel fedora", "/etc/sysconfig/nftables.conf", "/etc/sysconfig/iptables", "/etc/sysconfig/ip6tables"},
		{"unknown", "", "/etc/nftables.conf", "/etc/iptables/rules.v4", "/etc/iptables/rules.v6"},
	} {
		t.Run(tt.id, func(t *testing.T) {
			var writes []string
			queries := 0
			e := &cleanupExecutor{run: func(command string) (string, error) {
				switch {
				case command == persistenceDistributionCommand:
					queries++
					return tt.id + "\n" + tt.like + "\ncomplete\n", nil
				case strings.HasPrefix(command, "for tool in"):
					return "nft\niptables\nip6tables\ncomplete\n", nil
				case command == "nft -j list tables":
					return `{"nftables":[{"table":{"family":"ip","name":"ocvtest"}}]}`, nil
				case strings.HasPrefix(command, "nft list table"):
					return "table ip ocvtest {}", nil
				case command == "iptables-save", command == "ip6tables-save":
					return "*nat\nCOMMIT\n", nil
				case strings.HasPrefix(command, "set -e\numask 077"):
					writes = append(writes, command)
				case strings.HasPrefix(command, "set -e\nocv_nft_config="):
					if !strings.Contains(command, "ocv_nft_config="+shellQuote(tt.nft)) {
						t.Fatalf("wrong nft boot path: %s", command)
					}
				default:
					if strings.Contains(command, " save") || strings.Contains(command, " restart") || strings.Contains(command, " reload") || strings.Contains(command, " start") {
						t.Fatalf("unsafe second save/reload: %s", command)
					}
					// A minimal host may not provide optional boot services.
					return "", errors.New("optional service is absent")
				}
				return "", nil
			}}
			m := NewManager(e, "ocvtest", "")
			m.backend, m.detected = BackendNft, true
			if err := m.SaveRules(); err != nil {
				t.Fatal(err)
			}
			if queries != 1 || len(writes) != 3 {
				t.Fatalf("distribution queries=%d writes=%d", queries, len(writes))
			}
			for i, path := range []string{"/etc/nftables.d/ocvtest.nft", tt.v4, tt.v6} {
				if !strings.Contains(writes[i], "ocv_rule_path="+shellQuote(path)) {
					t.Fatalf("snapshot %d uses wrong path: %s", i, writes[i])
				}
			}
		})
	}
}

func TestPersistenceDistributionFailureDoesNotWrite(t *testing.T) {
	for _, output := range []string{"", "debian\n", "debian\n\n", "debian\n\ncomplete\ntrailing"} {
		t.Run(strings.ReplaceAll(output, "\n", "_"), func(t *testing.T) {
			e := &cleanupExecutor{run: func(command string) (string, error) {
				if strings.HasPrefix(command, "for tool in") {
					return "iptables\ncomplete\n", nil
				}
				if command == persistenceDistributionCommand {
					return output, nil
				}
				t.Fatalf("operation after incomplete detection: %s", command)
				return "", nil
			}}
			m := NewManager(e, "ocvtest", "")
			m.backend, m.detected = BackendIptables, true
			if err := m.SaveRules(); err == nil {
				t.Fatal("incomplete detection succeeded")
			}
		})
	}
}

func TestPersistRulesFilePreservesFilesAndFailures(t *testing.T) {
	for _, scenario := range []string{"existing", "new", "symlink", "dangling", "directory", "copy failure", "rename failure"} {
		t.Run(scenario, func(t *testing.T) {
			directory := t.TempDir()
			target := filepath.Join(directory, "rules file")
			resolved := target
			if scenario == "symlink" || scenario == "dangling" {
				resolved = filepath.Join(directory, "real rules")
				if err := os.Symlink(resolved, target); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "directory" {
				if err := os.Mkdir(target, 0700); err != nil {
					t.Fatal(err)
				}
			} else if scenario != "new" && scenario != "dangling" {
				if err := os.WriteFile(resolved, []byte("old rules\n"), 0640); err != nil {
					t.Fatal(err)
				}
			}
			var prefix string
			if scenario == "copy failure" {
				prefix = "cp() { return 42; };\n"
			} else if scenario == "rename failure" {
				prefix = "mv() { return 43; };\n"
			}
			e := &cleanupExecutor{run: func(command string) (string, error) {
				output, err := exec.Command("sh", "-c", prefix+command).CombinedOutput()
				return string(output), err
			}}
			m := NewManager(e, "ocvtest", "")
			content := "# literal ' $(false)\n*nat\nCOMMIT\n"
			err := m.persistRulesFile(target, content)
			wantError := scenario == "dangling" || scenario == "directory" || scenario == "copy failure" || scenario == "rename failure"
			if (err != nil) != wantError {
				t.Fatalf("err=%v, wantError=%v", err, wantError)
			}
			if scenario == "symlink" || scenario == "dangling" {
				if _, err := os.Readlink(target); err != nil {
					t.Fatalf("symlink replaced: %v", err)
				}
			}
			if scenario == "directory" {
				if info, err := os.Stat(target); err != nil || !info.IsDir() {
					t.Fatal("directory replaced")
				}
			} else if scenario == "dangling" {
				if _, err := os.Stat(resolved); !os.IsNotExist(err) {
					t.Fatal("dangling target created")
				}
			} else {
				want := content
				if wantError {
					want = "old rules\n"
				}
				if data, err := os.ReadFile(resolved); err != nil || string(data) != want {
					t.Fatalf("wrong saved rules: %q, %v", data, err)
				}
				wantMode := os.FileMode(0640)
				if scenario == "new" {
					wantMode = 0600
				}
				if info, err := os.Stat(resolved); err != nil || info.Mode().Perm() != wantMode {
					t.Fatalf("file permissions not preserved: %v %v", info, err)
				}
			}
			if leftovers, _ := filepath.Glob(filepath.Join(directory, "*.tmp.*")); len(leftovers) != 0 {
				t.Fatalf("temporary files retained: %v", leftovers)
			}
		})
	}
}
