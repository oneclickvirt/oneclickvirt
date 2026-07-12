package task

import (
	"testing"

	"oneclickvirt/constant"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
)

func TestExpandPortEndpoints(t *testing.T) {
	tests := []struct {
		name      string
		port      providerModel.Port
		want      []portEndpoint
		wantError bool
	}{
		{
			name: "legacy single port count",
			port: providerModel.Port{HostPort: 22001, GuestPort: 22},
			want: []portEndpoint{{host: 22001, guest: 22}},
		},
		{
			name: "three port range",
			port: providerModel.Port{HostPort: 25000, GuestPort: 8000, PortCount: 3},
			want: []portEndpoint{{host: 25000, guest: 8000}, {host: 25001, guest: 8001}, {host: 25002, guest: 8002}},
		},
		{
			name:      "range exceeds port limit",
			port:      providerModel.Port{HostPort: 65535, GuestPort: 80, PortCount: 2},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := expandPortEndpoints(tt.port)
			if (err != nil) != tt.wantError {
				t.Fatalf("expandPortEndpoints() error = %v, wantError %v", err, tt.wantError)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("endpoint %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestNormalizePortMappingMethod(t *testing.T) {
	tests := map[string]string{
		"lxd-device-proxy": "device_proxy",
		"nftables":         "iptables",
		"iptables-nat":     "iptables",
		"docker-native":    "native",
		"":                 "",
	}
	for input, want := range tests {
		if got := normalizePortMappingMethod(input); got != want {
			t.Fatalf("normalizePortMappingMethod(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestRepairPortMappingSkipReason(t *testing.T) {
	running := &providerModel.Instance{ID: 1, Status: constant.InstanceStatusRunning, PrivateIP: "10.0.0.2"}
	stopped := &providerModel.Instance{ID: 1, Status: constant.InstanceStatusStopped, PrivateIP: "10.0.0.2"}
	basePort := providerModel.Port{Status: "active", MappingType: "node", HostPort: 22001, GuestPort: 22, PortCount: 1}

	tests := []struct {
		name     string
		provider providerModel.Provider
		instance *providerModel.Instance
		port     providerModel.Port
		want     string
	}{
		{name: "qemu node mapping", provider: providerModel.Provider{Type: "qemu", Status: "active", NetworkType: "nat_ipv4"}, instance: running, port: basePort},
		{name: "stopped docker requires no restart", provider: providerModel.Provider{Type: "docker", Status: "active", NetworkType: "nat_ipv4"}, instance: stopped, port: basePort, want: "container_not_running"},
		{name: "no mapping network", provider: providerModel.Provider{Type: "qemu", Status: "active", NetworkType: "no_port_mapping"}, instance: running, port: basePort, want: "network_mode_has_no_node_mapping"},
		{name: "inactive provider", provider: providerModel.Provider{Type: "qemu", Status: "inactive", NetworkType: "nat_ipv4"}, instance: running, port: basePort, want: "provider_unavailable"},
		{name: "missing instance", provider: providerModel.Provider{Type: "qemu", Status: "active", NetworkType: "nat_ipv4"}, port: basePort, want: "instance_missing"},
		{name: "controller mapping", provider: providerModel.Provider{Type: "docker", Status: "active", NetworkType: "no_port_mapping"}, instance: running, port: providerModel.Port{Status: "active", MappingType: "controller", HostPort: 30000, GuestPort: 22, PortCount: 1, Protocol: "tcp"}},
		{name: "legacy controller range", provider: providerModel.Provider{Type: "docker", Status: "active"}, instance: running, port: providerModel.Port{Status: "active", MappingType: "controller", HostPort: 30000, GuestPort: 22, PortCount: 2}, want: "controller_range_unsupported"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := repairPortMappingSkipReason(tt.provider, tt.instance, tt.port); got != tt.want {
				t.Fatalf("repairPortMappingSkipReason() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseContainerRuntimePortBindings(t *testing.T) {
	bindings, err := parseContainerRuntimePortBindings(`warning line
{"22/tcp":[{"HostIp":"0.0.0.0","HostPort":"12022"}],"53/udp":[{"HostIp":"0.0.0.0","HostPort":"12053"}]}
trailing warning line`)
	if err != nil {
		t.Fatalf("parseContainerRuntimePortBindings returned error: %v", err)
	}
	if _, exists := bindings["22/tcp"][12022]; !exists {
		t.Fatal("expected tcp binding 12022 -> 22")
	}
	if _, exists := bindings["53/udp"][12053]; !exists {
		t.Fatal("expected udp binding 12053 -> 53")
	}
}

func TestCompactRepairPreviewProviders(t *testing.T) {
	preview := &adminModel.RepairPortMappingsPreviewResponse{
		ProviderCount: 3,
		Providers: []adminModel.RepairProviderPortMappingsPreview{
			{ProviderID: 1, ProviderName: "empty"},
			{ProviderID: 2, ProviderName: "candidate", CandidateCount: 1},
			{ProviderID: 3, ProviderName: "skipped", SkippedCount: 1},
		},
	}

	compactRepairPreviewProviders(preview)

	if preview.ProviderCount != 2 || len(preview.Providers) != 2 {
		t.Fatalf("provider count = %d, providers = %d, want 2", preview.ProviderCount, len(preview.Providers))
	}
	if preview.Providers[0].ProviderID != 2 || preview.Providers[1].ProviderID != 3 {
		t.Fatalf("unexpected providers after compaction: %+v", preview.Providers)
	}
}

func TestParseContainerRuntimePortBindingsRejectsInvalidJSON(t *testing.T) {
	if _, err := parseContainerRuntimePortBindings("not-json"); err == nil {
		t.Fatal("expected invalid runtime binding JSON to fail")
	}
}
