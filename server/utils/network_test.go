package utils

import "testing"

func TestBuildEndpointURLBracketsIPv6Hosts(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		want     string
	}{
		{name: "bare ipv6", endpoint: "2001:db8::10", want: "https://[2001:db8::10]:8443/1.0"},
		{name: "bracketed ipv6 with ssh port", endpoint: "[2001:db8::10]:22", want: "https://[2001:db8::10]:8443/1.0"},
		{name: "ipv4", endpoint: "192.0.2.10", want: "https://192.0.2.10:8443/1.0"},
		{name: "url endpoint", endpoint: "https://[2001:db8::10]:9443/ssh", want: "https://[2001:db8::10]:8443/1.0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := BuildEndpointURL("https", test.endpoint, 8443, "1.0"); got != test.want {
				t.Fatalf("BuildEndpointURL() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestBuildEndpointURLRejectsInvalidPortOrHost(t *testing.T) {
	if got := BuildEndpointURL("https", "", 8443, "/1.0"); got != "" {
		t.Fatalf("empty host produced %q", got)
	}
	if got := BuildEndpointURL("https", "2001:db8::1", 0, "/1.0"); got != "" {
		t.Fatalf("invalid port produced %q", got)
	}
}
