package provider

import (
	"strings"
	"testing"

	providerCore "oneclickvirt/provider"
)

func TestNormalizeDiscoveredInstancesScopesStableIdentity(t *testing.T) {
	remote := []providerCore.DiscoveredInstance{{UUID: "same-remote-id", Name: " guest ", InstanceType: "ct", Status: "UP"}}
	first := normalizeDiscoveredInstances(10, "docker", remote)
	again := normalizeDiscoveredInstances(10, "docker", remote)
	otherProvider := normalizeDiscoveredInstances(11, "docker", remote)
	if len(first) != 1 || first[0].UUID != again[0].UUID {
		t.Fatalf("identity is not stable: %#v %#v", first, again)
	}
	if first[0].UUID == otherProvider[0].UUID {
		t.Fatal("same remote identifier on different providers must be scoped")
	}
	if first[0].Name != "guest" || first[0].InstanceType != "container" || first[0].Status != "running" {
		t.Fatalf("unexpected normalization: %#v", first[0])
	}
}

func TestNormalizeDiscoveredPortsValidatesDeduplicatesAndMergesProtocols(t *testing.T) {
	mappings, sshPort, extras := normalizeDiscoveredPorts([]providerCore.DiscoveredPortMapping{
		{HostPort: 2200, GuestPort: 22, Protocol: "tcp", IsSSH: true, MappingMethod: "nftables"},
		{HostPort: 2200, GuestPort: 22, Protocol: "udp", IsSSH: true},
		{HostPort: 8080, GuestPort: 80, Protocol: "tcp"},
		{HostPort: 70000, GuestPort: 80, Protocol: "tcp"},
	}, 70000, []int{8080, 0, 8080})
	if len(mappings) != 2 || mappings[0].Protocol != "both" {
		t.Fatalf("unexpected mappings: %#v", mappings)
	}
	if mappings[0].MappingMethod != "iptables" {
		t.Fatalf("mapping method not normalized: %#v", mappings[0])
	}
	if sshPort != 2200 || len(extras) != 1 || extras[0] != 8080 {
		t.Fatalf("unexpected ports: ssh=%d extras=%v", sshPort, extras)
	}
}

func TestMarshalSanitizedDiscoveredDataRedactsSecrets(t *testing.T) {
	raw := map[string]interface{}{
		"Config":               map[string]interface{}{"Password": "plain", "image": "debian"},
		"Env":                  []string{"NORMAL=value", "API_TOKEN=secret", "DB_PASSWORD=hidden"},
		"cloud-init.user-data": "sensitive",
	}
	got, err := marshalSanitizedDiscoveredData(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"plain", "secret", "hidden", "sensitive"} {
		if strings.Contains(got, secret) {
			t.Fatalf("sanitized data leaked %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, "NORMAL=value") || !strings.Contains(got, "debian") {
		t.Fatalf("non-sensitive data was lost: %s", got)
	}
}

func TestHealthAutoRecoveryFrozenReasonDoesNotAuthorizeManualFreeze(t *testing.T) {
	for _, reason := range []struct {
		value string
		want  bool
	}{
		{value: "健康检查连续失败 20 次（约 60 分钟），自动冻结", want: true},
		{value: "Agent 反向连接连续断开 20 次，健康检查连续失败，自动冻结", want: true},
		{value: "manual", want: false},
		{value: "expired", want: false},
	} {
		if got := isHealthAutoRecoveryFrozenReason(reason.value); got != reason.want {
			t.Fatalf("isHealthAutoRecoveryFrozenReason(%q) = %v, want %v", reason.value, got, reason.want)
		}
	}
}
