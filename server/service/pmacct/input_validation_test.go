package pmacct

import "testing"

func TestValidatePmacctInstanceName(t *testing.T) {
	for _, name := range []string{"docker-ab12", "node_1", "vm.example"} {
		if err := validatePmacctInstanceName(name); err != nil {
			t.Fatalf("valid name %q rejected: %v", name, err)
		}
	}
	for _, name := range []string{"", "../escape", "node/name", "node name", "node\nname", "-leading"} {
		if err := validatePmacctInstanceName(name); err == nil {
			t.Fatalf("unsafe name %q accepted", name)
		}
	}
}

func TestNormalizePmacctQueryIP(t *testing.T) {
	tests := map[string]string{
		"192.0.2.10":      "192.0.2.10",
		"2001:db8::10/64": "2001:db8::10",
		" 198.51.100.4 ":  "198.51.100.4",
	}
	for input, want := range tests {
		got, err := normalizePmacctQueryIP(input)
		if err != nil || got != want {
			t.Fatalf("normalizePmacctQueryIP(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"", "not-an-ip", "10.0.0.1; touch /tmp/pwn", "2001:db8::1/not-a-prefix"} {
		if _, err := normalizePmacctQueryIP(input); err == nil {
			t.Fatalf("invalid address %q accepted", input)
		}
	}
}
