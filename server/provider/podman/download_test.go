package podman

import (
	"errors"
	"strings"
	"testing"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

func TestEnsureSSHScriptsRefreshesStaleRevision(t *testing.T) {
	oldLogger := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = oldLogger })

	staleRevision := errors.New("script revision is stale")
	executor := &routedPodmanExecutor{
		errors: []error{staleRevision, nil, nil, nil, staleRevision},
	}
	p := NewPodmanProvider().(*PodmanProvider)
	p.sshClient.SetExecutor(executor)

	if err := p.ensureSSHScriptsAvailable("US"); err != nil {
		t.Fatalf("ensureSSHScriptsAvailable() error = %v", err)
	}

	if len(executor.tempScripts) != 2 {
		t.Fatalf("downloaded script count = %d, want 2", len(executor.tempScripts))
	}
	for _, script := range []string{"ssh_bash.sh", "ssh_sh.sh"} {
		found := false
		for _, download := range executor.tempScripts {
			if strings.Contains(download, "/scripts/"+script) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("stale %s was not refreshed: %#v", script, executor.tempScripts)
		}
	}

	marker := "# oneclickvirt-ssh-init-revision: " + sshScriptRevision
	markerChecks := 0
	for _, command := range executor.commands {
		if strings.Contains(command, "grep -Fqx") && strings.Contains(command, marker) {
			markerChecks++
		}
	}
	if markerChecks != 4 {
		t.Fatalf("revision marker checks = %d, want 4; commands = %#v", markerChecks, executor.commands)
	}
}

func TestEnsureSSHScriptsFallsBackWhenCDNIsStale(t *testing.T) {
	oldLogger := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = oldLogger })

	staleRevision := errors.New("script revision is stale")
	executor := &routedPodmanExecutor{
		errors: []error{staleRevision, nil, nil, nil, staleRevision},
	}
	p := NewPodmanProvider().(*PodmanProvider)
	p.sshClient.SetExecutor(executor)

	if err := p.ensureSSHScriptsAvailable("CN"); err != nil {
		t.Fatalf("ensureSSHScriptsAvailable() error = %v", err)
	}
	if len(executor.tempScripts) != 2 {
		t.Fatalf("download count = %d, want CDN retry plus direct fallback", len(executor.tempScripts))
	}
	if !strings.Contains(executor.tempScripts[0], "cdn") {
		t.Fatalf("first download did not use CDN: %q", executor.tempScripts[0])
	}
	if !strings.Contains(executor.tempScripts[1], "https://raw.githubusercontent.com/oneclickvirt/podman/main/scripts/ssh_bash.sh") {
		t.Fatalf("second download did not use the raw fallback: %q", executor.tempScripts[1])
	}
}
