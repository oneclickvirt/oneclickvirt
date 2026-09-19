package task

import (
	"reflect"
	"testing"

	providerModel "oneclickvirt/model/provider"
)

func TestResetRuntimePortBindingsExpandRangesAndSkipController(t *testing.T) {
	got, err := resetRuntimePortBindings([]providerModel.Port{
		{HostPort: 22000, HostPortEnd: 22002, GuestPort: 8000, GuestPortEnd: 8002, PortCount: 3, Protocol: " BOTH "},
		{HostPort: 23000, GuestPort: 22, Protocol: "tcp", MappingType: "controller"},
	})
	want := []string{"0.0.0.0:22000:8000/tcp", "0.0.0.0:22001:8001/tcp", "0.0.0.0:22002:8002/tcp", "0.0.0.0:22000:8000/udp", "0.0.0.0:22001:8001/udp", "0.0.0.0:22002:8002/udp"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("bindings=%v err=%v", got, err)
	}
	for _, port := range []providerModel.Port{{HostPort: 65535, GuestPort: 80, PortCount: 2}, {HostPort: 22000, GuestPort: 80, Protocol: "sctp"}, {HostPort: 22000, GuestPort: 80, HostPortEnd: 22002, GuestPortEnd: 84}} {
		if _, err := resetRuntimePortBindings([]providerModel.Port{port}); err == nil {
			t.Fatalf("invalid mapping accepted: %+v", port)
		}
	}
}
