package admin

import "testing"

func TestExtractFamilyIPDoesNotCrossFamilies(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		ipv6   bool
		wanted string
	}{
		{name: "ipv4 requested from ipv4 endpoint", input: "198.51.100.10:8443", wanted: "198.51.100.10"},
		{name: "ipv6 requested from bracketed endpoint", input: "[2001:db8::10]:8443", ipv6: true, wanted: "2001:db8::10"},
		{name: "ipv6 rejects ipv4", input: "198.51.100.10:8443", ipv6: true},
		{name: "ipv4 rejects ipv6", input: "[2001:db8::10]:8443"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractFamilyIP(tt.input, tt.ipv6); got != tt.wanted {
				t.Fatalf("extractFamilyIP(%q, %t) = %q, want %q", tt.input, tt.ipv6, got, tt.wanted)
			}
		})
	}
}
