package utils

import "testing"

func TestIPv6DefaultGatewayIsLinkLocalJSON(t *testing.T) {
	for _, test := range []struct {
		name   string
		output string
		want   bool
		err    bool
	}{
		{"link local", "\x1b[32m" + `[{"dst":"default","gateway":"fe80::1","dev":"eth0"}]` + "\x1b[0m", true, false},
		{"other link local range", `[{"dst":"default","gateway":"febf::1","dev":"eth0"}]`, true, false},
		{"global", `[{"dst":"default","gateway":"2001:db8::1","dev":"eth0"}]`, false, false},
		{"multipath link local", `[{"dst":"default","multipath":[{"gateway":"fe80::1","dev":"eth0"},{"gateway":"fe80::2","dev":"eth1"}]}]`, true, false},
		{"multipath mixed", `[{"dst":"default","multipath":[{"gateway":"fe80::1","dev":"eth0"},{"gateway":"2001:db8::1","dev":"eth1"}]}]`, false, false},
		{"no gateway", `[]`, false, false},
		{"omitted destination", `[{"gateway":"fe80::1","dev":"eth0"}]`, true, false},
		{"German diagnostic", `Ungültiges IPv6-Gateway`, false, true},
		{"French diagnostic", `Passerelle IPv6 non valide`, false, true},
		{"Chinese diagnostic", `IPv6 网关无效`, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := IPv6DefaultGatewayIsLinkLocalJSON(test.output)
			if (err != nil) != test.err || got != test.want {
				t.Fatalf("got %v, %v; want %v, error=%v", got, err, test.want, test.err)
			}
		})
	}
}
