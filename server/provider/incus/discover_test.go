package incus

import "testing"

func TestParseProxyAddressSupportsIPv4IPv6AndWildcard(t *testing.T) {
	p := &IncusProvider{}
	for _, test := range []struct {
		input    string
		port     int
		protocol string
	}{
		{"tcp:0.0.0.0:2200", 2200, "tcp"},
		{"udp:[::]:5353", 5353, "udp"},
		{"tcp:8080", 8080, "tcp"},
	} {
		port, protocol := p.parseProxyAddress(test.input)
		if port != test.port || protocol != test.protocol {
			t.Fatalf("parseProxyAddress(%q) = %d/%s, want %d/%s", test.input, port, protocol, test.port, test.protocol)
		}
	}
}

func TestParseLimitsSupportsFractionalValues(t *testing.T) {
	p := &IncusProvider{}
	if got := p.parseMemoryLimit("1.5GB"); got != 1536 {
		t.Fatalf("memory = %d, want 1536", got)
	}
	if got := p.parseDiskSize("1.5GB"); got != 1536 {
		t.Fatalf("disk = %d, want 1536", got)
	}
}
