package firewall

import (
	"reflect"
	"sort"
	"testing"
)

func TestMergeIPVersion(t *testing.T) {
	tests := []struct {
		current string
		next    string
		want    string
	}{
		{current: "", next: "ipv4", want: "ipv4"},
		{current: "ipv4", next: "ipv4", want: "ipv4"},
		{current: "ipv6", next: "ipv6", want: "ipv6"},
		{current: "ipv4", next: "ipv6", want: "both"},
		{current: "ipv6", next: "ipv4", want: "both"},
		{current: "ipv4", next: "both", want: "both"},
		{current: "", next: "invalid", want: "both"},
	}
	for _, tt := range tests {
		if got := mergeIPVersion(tt.current, tt.next); got != tt.want {
			t.Fatalf("mergeIPVersion(%q, %q) = %q, want %q", tt.current, tt.next, got, tt.want)
		}
	}
}

func TestProviderRuleSetDeduplicatesStringsAndApplications(t *testing.T) {
	set := newProviderRuleSet()
	if err := set.add(effectiveApplication{ApplicationID: 2, IPVersion: "ipv4", Strings: `["beta","alpha"]`}); err != nil {
		t.Fatalf("add first application: %v", err)
	}
	if err := set.add(effectiveApplication{ApplicationID: 1, IPVersion: "ipv4", Strings: `["alpha","gamma"]`}); err != nil {
		t.Fatalf("add second application: %v", err)
	}
	if err := set.add(effectiveApplication{ApplicationID: 1, IPVersion: "ipv6", Strings: `["gamma"]`}); err != nil {
		t.Fatalf("add duplicate application: %v", err)
	}

	sort.Strings(set.strings)
	sort.Slice(set.applicationIDs, func(i, j int) bool { return set.applicationIDs[i] < set.applicationIDs[j] })
	if !reflect.DeepEqual(set.strings, []string{"alpha", "beta", "gamma"}) {
		t.Fatalf("strings = %v", set.strings)
	}
	if !reflect.DeepEqual(set.applicationIDs, []uint{1, 2}) {
		t.Fatalf("application IDs = %v", set.applicationIDs)
	}
	if set.ipVersion != "both" {
		t.Fatalf("IP version = %q, want both", set.ipVersion)
	}
}

func TestProviderRuleSetRejectsInvalidStoredJSON(t *testing.T) {
	set := newProviderRuleSet()
	if err := set.add(effectiveApplication{ApplicationID: 1, Strings: `not-json`}); err == nil {
		t.Fatal("expected invalid rule JSON to fail")
	}
}
