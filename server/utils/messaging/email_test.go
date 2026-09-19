package messaging

import (
	"testing"
)

func TestSMTPAddressSupportsIPv4AndIPv6(t *testing.T) {
	for _, test := range []struct {
		name string
		host string
		port int
		want string
	}{
		{name: "ipv4", host: "192.0.2.10", port: 587, want: "192.0.2.10:587"},
		{name: "ipv6", host: "2001:db8::10", port: 587, want: "[2001:db8::10]:587"},
		{name: "bracketed ipv6", host: "[2001:db8::10]", port: 465, want: "[2001:db8::10]:465"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := smtpAddress(test.host, test.port)
			if got != test.want {
				t.Fatalf("address = %q, want %q", got, test.want)
			}
		})
	}
}
