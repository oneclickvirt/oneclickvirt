package lxd

import (
	"context"
	"errors"
	"go.uber.org/zap"
	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/utils"
	"strings"
	"testing"
)

func TestAutostartForVMAndContainerWithCLIFallback(t *testing.T) {
	previous := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	defer func() { global.APP_LOG = previous }()
	for _, kind := range []string{"vm", "container"} {
		for _, legacy := range []bool{false, true} {
			e := &recordingLXDIPv6Executor{}
			if legacy {
				e.errors = []error{errors.New("old CLI needs separate key and value")}
			}
			p := &LXDProvider{sshClient: utils.NewSafeShellExecutor(e)}
			p.config.ExecutionRule = "ssh_only"
			p.connected = true
			if err := p.configureInstanceSystem(context.Background(), provider.InstanceConfig{Name: "guest", InstanceType: kind}); err != nil {
				t.Fatal(err)
			}
			all := strings.Join(e.commands, "\n")
			if !strings.Contains(all, "'boot.autostart'='true'") {
				t.Fatalf("missing autostart for %s: %s", kind, all)
			}
			if legacy && !strings.Contains(all, "'boot.autostart' 'true'") {
				t.Fatal("old CLI fallback was not attempted")
			}
		}
	}
}

func TestAutostartFailureIsNotIgnored(t *testing.T) {
	previous := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	defer func() { global.APP_LOG = previous }()
	e := &recordingLXDIPv6Executor{errors: []error{errors.New("write denied"), errors.New("write denied")}}
	p := &LXDProvider{sshClient: utils.NewSafeShellExecutor(e)}
	p.config.ExecutionRule = "ssh_only"
	p.connected = true
	if err := p.configureInstanceSystem(context.Background(), provider.InstanceConfig{Name: "guest", InstanceType: "vm"}); err == nil {
		t.Fatal("failed persistence was reported as configured")
	}
	if len(e.commands) != 2 {
		t.Fatal("continued optional writes after mandatory failure")
	}
}
