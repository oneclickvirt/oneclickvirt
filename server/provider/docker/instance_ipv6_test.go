package docker

import (
	"strings"
	"testing"

	"oneclickvirt/utils"
)

func TestDockerStaticIPv6RunOptions(t *testing.T) {
	tests := []struct {
		name        string
		networkType string
		staticIPv6  string
		available   bool
		want        []string
		wantErr     string
	}{
		{
			name:        "static address is attached",
			networkType: "nat_ipv4_ipv6",
			staticIPv6:  "2001:0db8::10/64",
			available:   true,
			want:        []string{"--network='ipv6_net'", "--ip6='2001:db8::10'"},
		},
		{name: "invalid static address", networkType: "nat_ipv4_ipv6", staticIPv6: "not-an-ip", available: true, wantErr: "静态IPv6地址无效"},
		{name: "IPv6 network unavailable", networkType: "nat_ipv4_ipv6", staticIPv6: "2001:db8::10", wantErr: "节点IPv6网络不可用"},
		{name: "IPv4-only network rejects static address", networkType: "nat_ipv4", staticIPv6: "2001:db8::10", available: true, wantErr: "未启用IPv6"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selection, err := utils.ResolveContainerNetwork(test.networkType, test.staticIPv6, defaultDockerRuntime.IPv4Network, defaultDockerRuntime.IPv6Network, test.available)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("ResolveContainerNetwork() error = %v, want containing %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveContainerNetwork() error = %v", err)
			}
			command := appendDockerNetworkOptions("docker run", selection)
			for _, fragment := range test.want {
				if !strings.Contains(command, fragment) {
					t.Fatalf("command = %q, want fragment %q", command, fragment)
				}
			}
		})
	}
}
