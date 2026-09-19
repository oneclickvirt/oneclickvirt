package lxd

import (
	"context"
	"net/http"
	"strings"
	"testing"

	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
)

func TestLXDProviderAPIResetAppliesReservedMappingsAndRecovers(t *testing.T) {
	for _, scenario := range []string{"success", "cancel-update", "conflict"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			tr := &lxdIPv4MetadataTransport{t: t}
			if scenario == "cancel-update" {
				tr.cancelOnUpdate = cancel
			}
			if scenario == "conflict" {
				tr.conflictOnUpdate = true
			}
			p := NewLXDProvider().(*LXDProvider)
			p.config = provider.NodeConfig{Host: "127.0.0.1", PortIP: "198.51.100.10", ExecutionRule: "api_only"}
			p.apiClient = &http.Client{Transport: tr}
			// No DB is required and no SSH executor is installed. Unlike the
			// create-time loader, this path must apply non-active reservations.
			err := p.ConfigurePortMappingsAPI(ctx, "guest", []providerModel.Port{{ID: 7, HostPort: 22000, GuestPort: 22, Protocol: "tcp", MappingMethod: "device_proxy", Status: "restoring"}})
			if scenario == "success" && err != nil {
				t.Fatal(err)
			}
			if scenario != "success" && (err == nil || !tr.recovered || tr.stopped) {
				t.Fatalf("failed mapping recovery: err=%v recovered=%v stopped=%v", err, tr.recovered, tr.stopped)
			}
			if !strings.Contains(strings.Join(tr.requests, ","), "PUT /1.0/instances/guest") {
				t.Fatalf("mapping was silently skipped: %v", tr.requests)
			}
		})
	}
}

func TestLXDProviderAPIOnlyIPv4AndManualMappingNeverUseSSH(t *testing.T) {
	p := NewLXDProvider().(*LXDProvider)
	p.config = provider.NodeConfig{Host: "127.0.0.1", PortIP: "198.51.100.10", ExecutionRule: "api_only"}
	tr := &lxdIPv4MetadataTransport{t: t}
	p.apiClient = &http.Client{Transport: tr}
	if ip, err := p.GetInstanceIPv4(context.Background(), "guest"); err != nil || ip != "192.0.2.10" {
		t.Fatalf("API-only IPv4=(%s,%v)", ip, err)
	}
	if err := p.SetupPortMappingWithIP(context.Background(), "guest", 22000, 22, "tcp", "device_proxy", "192.0.2.10"); err != nil {
		t.Fatal(err)
	}
}
