package kubevirt

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"oneclickvirt/global"
	"oneclickvirt/utils"
)

func TestPasswordProxyPreservesCredentialsAndUsesStableTransport(t *testing.T) {
	dir := t.TempDir()
	scripts := map[string]string{
		"sshpass": "#!/bin/sh\n[ \"$1\" = -e ] || exit 10\nshift\nexec \"$@\"\n",
		"ssh": `#!/bin/sh
[ "$SSHPASS" = "$EXPECTED_AUTH" ] || exit 11
[ "$KUBECONFIG" = /etc/rancher/k3s/k3s.yaml ] || exit 12
proxy_seen=0
for argument do
 case "$argument" in
  ProxyCommand=*) sh -c "exec ${argument#ProxyCommand=}" || exit 13; proxy_seen=1 ;;
 esac
 last=$argument
done
[ "$proxy_seen" = 1 ] && [ "$last" = "$EXPECTED_REMOTE" ] || exit 14
`,
		"virtctl": `#!/bin/sh
[ "$#" = 6 ] && [ "$1" = port-forward ] && [ "$2" = --stdio=true ] && [ "$3" = -n ] && [ "$4" = kubevirt-vms ] && [ "$5" = vmi/guest-test ] && [ "$6" = 22 ] || exit 15
`,
	}
	for name, script := range scripts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	password := " ' literal $(exit 41) `exit 42` \" "
	remote := "printf '%s\\n' 'literal command'"
	cmd := exec.Command("sh", "-c", kubeVirtPasswordProxyCommand("guest-test", password, remote))
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "EXPECTED_AUTH="+password, "EXPECTED_REMOTE="+remote)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("proxy command lost credentials or argument boundaries: %v; %s", err, out)
	}
}

type passwordProxyExecutor struct {
	utils.ShellExecutor
	commands []string
	failAt   int
	proof    string
}

func (e *passwordProxyExecutor) Execute(command string) (string, error) {
	e.commands = append(e.commands, command)
	if len(e.commands) == e.failAt {
		return "", errors.New("injected SSH failure")
	}
	return e.proof, nil
}
func (e *passwordProxyExecutor) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}

func TestPasswordProxyRequiresNewPasswordVerification(t *testing.T) {
	for _, tc := range []struct {
		name        string
		failAt      int
		proof       string
		wantSuccess bool
		calls       int
	}{
		{"verified", 0, "kubevirt-password-ok\n", true, 2},
		{"update rejected", 1, "", false, 1},
		{"new password rejected", 2, "", false, 2},
		{"missing command proof", 0, "", false, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &passwordProxyExecutor{failAt: tc.failAt, proof: tc.proof}
			p := &KubeVirtProvider{sshClient: utils.NewSafeShellExecutor(e)}
			err := p.kubeVirtSetPasswordViaVirtctl(context.Background(), "guest-test", "current-secret", "new-secret")
			if (err == nil) != tc.wantSuccess || len(e.commands) != tc.calls {
				t.Fatalf("success=%v calls=%d", err == nil, len(e.commands))
			}
			if !strings.Contains(e.commands[0], "SSHPASS='current-secret'") {
				t.Fatal("current password was not used")
			}
			if len(e.commands) == 2 && !strings.Contains(e.commands[1], "SSHPASS='new-secret'") {
				t.Fatal("new password was not authenticated")
			}
		})
	}
}

type passwordDeadlineExecutor struct {
	utils.ShellExecutor
	remaining time.Duration
}

func (e *passwordDeadlineExecutor) ExecuteContext(ctx context.Context, command string) (string, error) {
	if strings.Contains(command, "kubectl get deploy") {
		return "", nil
	}
	if strings.Contains(command, "kubectl get svc") {
		deadline, ok := ctx.Deadline()
		if !ok {
			return "", errors.New("missing operation deadline")
		}
		e.remaining = time.Until(deadline)
		return "", errors.New("no NodePort for IPv6-only VM")
	}
	if strings.Contains(command, "command -v sshpass") {
		return "", nil
	}
	return "kubevirt-password-ok\n", nil
}
func TestPasswordFallbackHonorsOperationAndCallerBudgets(t *testing.T) {
	previous := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = previous })
	for _, budget := range []time.Duration{10 * time.Minute, 30 * time.Second} {
		ctx, cancel := context.WithTimeout(context.Background(), budget)
		e := &passwordDeadlineExecutor{}
		p := &KubeVirtProvider{connected: true, sshClient: utils.NewSafeShellExecutor(e)}
		err := p.sshSetPassword(ctx, "guest-test", "new", "current")
		cancel()
		expected := min(budget, kubeVirtPasswordSetTimeout)
		if err != nil || e.remaining <= 0 || e.remaining > expected || e.remaining < expected-time.Second {
			t.Fatalf("budget %v remaining %v err %v", budget, e.remaining, err)
		}
	}
}
