package proxmox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"oneclickvirt/global"
	"oneclickvirt/utils"
)

type portCleanupExecutor struct {
	utils.ShellExecutor
	commands []string
	rules    map[string][]string
}

func (e *portCleanupExecutor) Execute(command string) (string, error) {
	return e.ExecuteWithTimeout(command, 0)
}

func (e *portCleanupExecutor) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	e.commands = append(e.commands, command)
	switch {
	case strings.Contains(command, "proxmox_fw_backend"):
		return "iptables", nil
	case strings.HasPrefix(command, "command -v nft"):
		return "", fmt.Errorf("nft absent")
	case strings.HasPrefix(command, "command -v"):
		return "", nil
	case strings.Contains(command, "-save"):
		binary := strings.Fields(command)[0]
		rules, ok := e.rules[binary]
		if !ok {
			return "", fmt.Errorf("missing NAT table")
		}
		return "*nat\n" + strings.Join(rules, "\n") + "\nCOMMIT\n", nil
	case strings.Contains(command, " -S"):
		binary := strings.Fields(command)[0]
		rules, ok := e.rules[binary]
		if !ok {
			return "", fmt.Errorf("missing NAT table")
		}
		if strings.Contains(command, " -t nat") {
			return strings.Join(rules, "\n"), nil
		}
		return "", nil
	case strings.Contains(command, " '-D' "):
		binary := strings.Fields(command)[0]
		old := e.rules[binary]
		for i, row := range old {
			if strings.Contains(row, "pm:guest:22000:") {
				e.rules[binary] = append(old[:i], old[i+1:]...)
				break
			}
		}
		return "", nil
	default:
		return "", fmt.Errorf("unexpected command %q", command)
	}
}

func TestPortCleanupUsesOnlyTheRecordedFamily(t *testing.T) {
	oldLog := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = oldLog })
	for _, tool := range []string{"iptables", "ip6tables"} {
		t.Run(tool, func(t *testing.T) {
			other := "ip6tables"
			address := "192.0.2.20:22"
			if tool == "ip6tables" {
				other, address = "iptables", "[2001:db8::20]:22"
			}
			row := "-A PREROUTING -p tcp --dport 22000 -m comment --comment pm:guest:22000:22 -j DNAT --to-destination " + address
			executor := &portCleanupExecutor{rules: map[string][]string{tool: {
				row, strings.Replace(row, "pm:guest:", "pm:another:", 1),
			}}}
			p := &ProxmoxProvider{sshClient: utils.NewSafeShellExecutor(executor)}
			if err := p.RemovePortMappingForFamily("guest", 22000, "tcp", "", tool == "ip6tables"); err != nil {
				t.Fatal(err)
			}
			if len(executor.rules[tool]) != 1 || !strings.Contains(executor.rules[tool][0], "pm:another:") {
				t.Fatalf("wrong rules remain: %v", executor.rules)
			}
			for _, command := range executor.commands {
				if strings.Contains(command, other) && !strings.Contains(command, "proxmox_fw_backend") {
					t.Fatalf("touched unrelated family with %q", command)
				}
			}
		})
	}
}
