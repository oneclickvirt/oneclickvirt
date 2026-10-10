package lxd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"oneclickvirt/provider"
)

type unmappedNetworkExecutor struct {
	networkStopExecutor
	state           string
	readErr         error
	cancelAfterRead context.CancelFunc
}

func (e *unmappedNetworkExecutor) Execute(command string) (string, error) {
	e.commands = append(e.commands, command)
	switch command {
	case "lxc query '/1.0/instances/guest/state'":
		if e.cancelAfterRead != nil {
			e.cancelAfterRead()
		}
		return e.state, e.readErr
	case "lxc config device list 'guest'":
		return "eth0\ngpu0\nshare-host\nroot\n", nil
	case "lxc config device override 'guest' 'eth0' limits.egress=300Mbit limits.ingress=300Mbit limits.max=300Mbit":
		return "", nil
	case "lxc query '/1.0/instances/guest'":
		return `{"devices":{"eth0":{"type":"nic","name":"eth0","network":"lxdbr0","ipv4.address":"192.0.2.10"},"gpu0":{"type":"gpu","id":"0"},"share-host":{"type":"disk","source":"/root/sharefile","path":"/root/sharefile"},"root":{"type":"disk","path":"/","pool":"default"}}}`, nil
	default:
		return "", fmt.Errorf("unexpected network mutation: %s", command)
	}
}

func (e *unmappedNetworkExecutor) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}

func TestLXDUnmappedCopyNetworkDoesNotCycleGuest(t *testing.T) {
	const running = `{"status":"Running","network":{"eth0":{"addresses":[{"family":"inet","scope":"global","address":"192.0.2.10"}]}}}`
	for _, tc := range []struct {
		name, state                   string
		readErr                       error
		cancelBefore, cancelAfterRead bool
		wantErr                       bool
	}{
		{name: "running GPU clone", state: running},
		{name: "coloured state", state: "\x1b[32m" + running + "\x1b[0m"},
		{name: "API envelope", state: `{"metadata":` + running + `}`},
		{name: "stopped", state: `{"status":"Stopped"}`, wantErr: true},
		{name: "malformed state", state: "not JSON", wantErr: true},
		{name: "read failure", readErr: errors.New("Agent disconnected"), wantErr: true},
		{name: "cancel before", state: running, cancelBefore: true, wantErr: true},
		{name: "cancel after read", state: running, cancelAfterRead: true, wantErr: true},
		{name: "cancel waiting for address", state: `{"status":"Running","network":{}}`, cancelAfterRead: true, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := NewLXDProvider().(*LXDProvider)
			p.reverseAgent.Store(true)
			transport := &createRouteTransport{}
			p.apiClient = &http.Client{Transport: transport}
			executor := &unmappedNetworkExecutor{state: tc.state, readErr: tc.readErr}
			p.sshClient.SetExecutor(executor)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancelBefore {
				cancel()
			}
			if tc.cancelAfterRead {
				executor.cancelAfterRead = cancel
			}
			cfg := provider.InstanceConfig{Name: "guest", InstanceType: "container", CopyMode: true, CopySourceName: "mother", GpuEnabled: true, GpuDeviceIds: "0"}
			err := p.configureInstanceNetwork(ctx, cfg, NetworkConfig{NetworkType: "no_port_mapping", InSpeed: 300, OutSpeed: 300})
			if (err != nil) != tc.wantErr {
				t.Fatalf("network error = %v", err)
			}
			if (tc.cancelBefore || tc.cancelAfterRead) && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation error = %v", err)
			}
			if transport.calls != 0 {
				t.Fatalf("Agent create used controller API %d times", transport.calls)
			}
			for _, command := range executor.commands {
				if strings.Contains(command, " stop ") || strings.Contains(command, " restart ") || strings.Contains(command, " start ") || strings.Contains(command, " delete ") || strings.Contains(command, "mother") || strings.Contains(command, " profile ") || strings.Contains(command, "firewall") || strings.Contains(command, "ufw") {
					t.Fatalf("network setup cycled a guest or changed shared state: %s", command)
				}
			}
			if tc.wantErr {
				for _, command := range executor.commands {
					if strings.Contains(command, " config device ") {
						t.Fatalf("failed/cancelled network setup mutated guest: %s", command)
					}
				}
			} else if len(executor.commands) != 4 {
				t.Fatalf("missing guest bandwidth/address configuration: %v", executor.commands)
			}
		})
	}
}
