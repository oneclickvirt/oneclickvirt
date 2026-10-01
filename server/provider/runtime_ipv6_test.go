package provider

import (
	"encoding/json"
	"testing"
)

func TestRuntimeIPv6SelectionIsStableAndPrefersPublicGuestAddress(t *testing.T) {
	var state map[string]interface{}
	if err := json.Unmarshal([]byte(`{"network":{
		"eth0":{"addresses":[{"family":"inet6","scope":"global","address":"fd00::10"},{"family":"inet6","address":"fe80::1"}]},
		"eth1":{"addresses":[{"family":"inet6","scope":"global","address":"2606:4700::20/64"}]},
		"eth2":{"addresses":[{"family":"inet6","scope":"global","address":"2606:4700::10"},{"family":"inet6","address":"::ffff:192.0.2.1"}]}
	}}`), &state); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 30; attempt++ {
		if got := InstanceIPv6FromRuntimeState(state); got != "2606:4700::10" {
			t.Fatalf("IPv6 target changed with map iteration order: %q", got)
		}
	}
	delete(state["network"].(map[string]interface{}), "eth1")
	delete(state["network"].(map[string]interface{}), "eth2")
	if got := InstanceIPv6FromRuntimeState(state); got != "fd00::10" {
		t.Fatalf("private guest IPv6 fallback = %q", got)
	}
	if got := InstanceIPv6FromRuntimeState(nil); got != "" {
		t.Fatalf("empty runtime state returned %q", got)
	}
}
