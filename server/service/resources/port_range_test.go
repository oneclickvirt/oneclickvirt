package resources

import (
	"reflect"
	"testing"

	providerModel "oneclickvirt/model/provider"
)

func TestHostPortRangeConflictsIncludesStoredRanges(t *testing.T) {
	records := []providerModel.Port{
		{HostPort: 20000, HostPortEnd: 20002, PortCount: 3},
		{HostPort: 21000, PortCount: 2},
		{HostPort: 22000, PortCount: 1},
	}

	got := hostPortRangeConflicts(records, 19999, 21001)
	want := []int{20000, 20001, 20002, 21000, 21001}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hostPortRangeConflicts() = %v, want %v", got, want)
	}
}

func TestEffectiveStoredHostPortEndPrefersExplicitEnd(t *testing.T) {
	port := providerModel.Port{HostPort: 30000, HostPortEnd: 30004, PortCount: 2}
	if got := effectiveStoredHostPortEnd(port); got != 30004 {
		t.Fatalf("effectiveStoredHostPortEnd() = %d, want 30004", got)
	}
}
