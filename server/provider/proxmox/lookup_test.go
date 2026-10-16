package proxmox

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"
	"oneclickvirt/global"
)

func TestPVELookupRequiresExactIdentifierAndConfirmedInventory(t *testing.T) {
	oldLog := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = oldLog })
	for _, tc := range []struct {
		inventory, identifier, wantID string
		absent                        bool
	}{
		{`[{"vmid":100,"type":"lxc","name":"guest-new"}]`, "guest", "", true},
		{`[{"vmid":100,"type":"lxc","name":"guest-new"}]`, "guest-new", "100", false},
		{`[{"vmid":100,"type":"lxc","name":"guest-new"}]`, "100", "100", false},
		{`[]`, "guest", "", true},
		{`null`, "guest", "", false},
		{``, "guest", "", false},
		{`failure`, "guest", "", false},
		{`[{"vmid":100,"type":"qemu","name":"guest"},{"vmid":101,"type":"lxc","name":"guest"}]`, "guest", "", false},
	} {
		executor := &ipv6CommandExecutor{output: func(string) string { return tc.inventory }}
		p := NewProxmoxProvider().(*ProxmoxProvider)
		p.sshClient.SetExecutor(executor)
		id, _, err := p.findVMIDByNameOrID(context.Background(), tc.identifier)
		if id != tc.wantID || errors.Is(err, errProxmoxInstanceNotFound) != tc.absent || (tc.wantID == "" && err == nil) || (tc.wantID != "" && err != nil) {
			t.Fatalf("%s %s: id=%s err=%v", tc.inventory, tc.identifier, id, err)
		}
		if len(executor.commands) != 1 {
			t.Fatalf("lookup performed per-guest reads: %v", executor.commands)
		}
	}
	for _, api := range []bool{false, true} {
		for _, failed := range []bool{false, true} {
			executor := &ipv6CommandExecutor{output: func(string) string { return `[]` }, fail: func(string) error {
				if failed {
					return errors.New("inventory transport unavailable")
				}
				return nil
			}}
			p := NewProxmoxProvider().(*ProxmoxProvider)
			p.sshClient.SetExecutor(executor)
			var err error
			if api {
				err = p.apiDeleteInstance(context.Background(), "guest")
			} else {
				err = p.sshDeleteInstance(context.Background(), "guest")
			}
			if (err != nil) != failed {
				t.Fatalf("api=%v failed=%v: %v", api, failed, err)
			}
			for _, command := range executor.commands {
				if strings.Contains(command, "destroy") {
					t.Fatal("destroyed an unconfirmed guest")
				}
			}
		}
	}
}
