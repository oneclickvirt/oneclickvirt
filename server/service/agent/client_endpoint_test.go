package agent

import (
	"net/url"
	"testing"
)

func TestAgentBaseURLSupportsIPv4AndIPv6(t *testing.T) {
	tests := []struct {
		name string
		host string
		port int
		want string
	}{
		{name: "ipv4", host: "192.0.2.10", port: 23782, want: "http://192.0.2.10:23782"},
		{name: "ipv6", host: "2001:db8::10", port: 23782, want: "http://[2001:db8::10]:23782"},
		{name: "bracketed ipv6", host: "[2001:db8::10]", port: 23782, want: "http://[2001:db8::10]:23782"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := agentBaseURL(tt.host, tt.port)
			if got != tt.want {
				t.Fatalf("agentBaseURL(%q, %d) = %q, want %q", tt.host, tt.port, got, tt.want)
			}
			parsed, err := url.Parse(got)
			if err != nil {
				t.Fatalf("url.Parse(%q): %v", got, err)
			}
			if parsed.Hostname() != "2001:db8::10" && tt.name != "ipv4" {
				t.Fatalf("parsed IPv6 hostname = %q", parsed.Hostname())
			}
		})
	}
}
