package utils

import "testing"

func TestBuildSSHAddressSupportsIPv6AndExplicitPorts(t *testing.T) {
	for _, tc := range []struct {
		name string
		host string
		port int
		want string
	}{
		{name: "hostname", host: "node.example", port: 22, want: "node.example:22"},
		{name: "ipv4", host: "198.51.100.10", port: 22022, want: "198.51.100.10:22022"},
		{name: "bare ipv6", host: "2001:db8::42", port: 22022, want: "[2001:db8::42]:22022"},
		{name: "bracketed ipv6", host: "[2001:db8::42]", port: 22, want: "[2001:db8::42]:22"},
		{name: "explicit ipv6 port", host: "[2001:db8::42]:22022", port: 22, want: "[2001:db8::42]:22022"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildSSHAddress(tc.host, tc.port); got != tc.want {
				t.Fatalf("buildSSHAddress(%q, %d) = %q, want %q", tc.host, tc.port, got, tc.want)
			}
		})
	}
}
