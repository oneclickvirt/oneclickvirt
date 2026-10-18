package lxd

import (
	"context"
	"errors"
	"oneclickvirt/utils"
	"strings"
	"testing"
)

func TestVMRoutedIPv6RejectsInvalidAddressBeforeExecution(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "2001:db8::1; touch /tmp/injected", ""} {
		e := &recordingLXDIPv6Executor{}
		p := &LXDProvider{sshClient: utils.NewSafeShellExecutor(e)}
		if err := p.configureVMRoutedIPv6(context.Background(), "guest", address); err == nil {
			t.Fatal("accepted invalid address")
		}
		if len(e.commands) != 0 {
			t.Fatal("executed invalid address")
		}
	}
}
func TestVMRoutedIPv6PropagatesGuestFailure(t *testing.T) {
	e := &recordingLXDIPv6Executor{errors: []error{errors.New("agent unavailable")}}
	p := &LXDProvider{sshClient: utils.NewSafeShellExecutor(e)}
	if err := p.configureVMRoutedIPv6(context.Background(), "guest", "2001:db8::10"); err == nil {
		t.Fatal("guest failure reported success")
	}
	if len(e.commands) != 1 {
		t.Fatal("unexpected execution count")
	}
	if strings.Contains(e.commands[0], "network-online.target") {
		t.Fatal("IPv6 service must not wait for removed IPv4 interface")
	}
	for _, f := range []string{"timeout 5 lxc exec", "volatile.eth1.hwaddr", "/sys/class/net/*", "metric 50", "systemctl restart", "systemctl is-active --quiet", "RemainAfterExit=yes", "After=network.target"} {
		if !strings.Contains(e.commands[0], f) {
			t.Fatalf("missing %q", f)
		}
	}
}
