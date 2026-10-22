package ipv6pool

import (
	"strings"
	"testing"
)

func TestIPv6RangeKeepsCIDRLazyAndDeduplicates(t *testing.T) {
	entries, invalid, err := parseIPv6PoolText(7, "2001:db8::1 - 2001:db8::3\n2001:db8::2,2001:db8:1::/64;2001:db8::4 # comment", SourceManual)
	if err != nil || len(entries) != 5 || len(invalid) != 0 {
		t.Fatalf("%v %v %v", entries, invalid, err)
	}
	if entries[0].Address != "2001:db8::1" || entries[2].Address != "2001:db8::3" || !entries[3].IsRange || entries[3].RangeNext != "2001:db8:1::1" || entries[4].Address != "2001:db8::4" {
		t.Fatalf("range or CIDR changed: %v", entries)
	}
	for _, entry := range entries {
		if entry.ProviderID != 7 || entry.Source != SourceManual {
			t.Fatal("range lost ownership")
		}
	}
}

func TestIPv6InvalidRangesDoNotPartlyAppend(t *testing.T) {
	for _, input := range []string{":: - ::2", "2001:db8::3-2001:db8::1", "2001:db8::1-2001:db8::1001", "2001:db8::1-192.0.2.1"} {
		t.Run(input, func(t *testing.T) {
			entries, invalid, err := parseIPv6PoolText(1, input+"\n2001:db8:1::9", SourceManual)
			if err != nil || len(entries) != 1 || len(invalid) != 1 || entries[0].Address != "2001:db8:1::9" {
				t.Fatalf("%v %v %v", entries, invalid, err)
			}
		})
	}
	if entries, _, err := parseIPv6PoolText(1, "2001:db8::1-2001:db8::1000\n2001:db8:1::1-2001:db8:1::1000", SourceManual); err == nil || entries != nil || !strings.Contains(err.Error(), "5000") {
		t.Fatalf("unbounded range expansion: %d %v", len(entries), err)
	}
	for _, input := range []string{"2001:db8::1 - 2001:db8::3", "2001:db8::1-2001:db8::3"} {
		if entries, _, err := parseIPv6PoolText(1, input, SourceNodeFile); err == nil || entries != nil {
			t.Fatal("node-file format unexpectedly changed")
		}
	}
}
