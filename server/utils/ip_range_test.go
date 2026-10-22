package utils

import "testing"

func TestParseIPRange(t *testing.T) {
	for _, tc := range []struct {
		input       string
		ipv6        bool
		count       int
		first, last string
	}{
		{"192.168.1.1 - 192.168.1.100", false, 100, "192.168.1.1", "192.168.1.100"},
		{"192.0.2.255-192.0.3.1", false, 3, "192.0.2.255", "192.0.3.1"},
		{"255.255.255.255 - 255.255.255.255", false, 1, "255.255.255.255", "255.255.255.255"},
		{"10.0.0.0-10.0.15.255", false, 4096, "10.0.0.0", "10.0.15.255"},
		{"2001:db8::ff - 2001:db8::101", true, 3, "2001:db8::ff", "2001:db8::101"},
		{"ffff:ffff:ffff:ffff:ffff:ffff:ffff:fffe-ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", true, 2, "ffff:ffff:ffff:ffff:ffff:ffff:ffff:fffe", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			addresses, err := ParseIPRange(tc.input, tc.ipv6)
			if err != nil || len(addresses) != tc.count {
				t.Fatalf("got %d addresses, err=%v", len(addresses), err)
			}
			if addresses[0] != tc.first || addresses[len(addresses)-1] != tc.last {
				t.Fatalf("lost inclusive bounds: %v", addresses)
			}
		})
	}
}

func TestParseIPRangeRejectsWholeInvalidRange(t *testing.T) {
	for _, tc := range []struct {
		input string
		ipv6  bool
	}{
		{"10.0.0.2-10.0.0.1", false}, {"10.0.0.0-10.0.16.0", false},
		{"10.0.0.1 - 2001:db8::1", false}, {"10.0.0.1 - 10.0.0.2", true},
		{"::ffff:192.0.2.1-::ffff:192.0.2.2", true}, {"fe80::1%eth0-fe80::2%eth0", true},
		{"2001:db8::1-2001:db8:ffff::1", true}, {"2001:db8::2-2001:db8::1", true},
		{"192.0.2.1-", false}, {"-192.0.2.1", false}, {"192.0.2.1-192.0.2.2-192.0.2.3", false},
		{"bad-192.0.2.1", false}, {"192.0.2.1/32-192.0.2.2", false},
	} {
		t.Run(tc.input, func(t *testing.T) {
			addresses, err := ParseIPRange(tc.input, tc.ipv6)
			if err == nil || addresses != nil {
				t.Fatalf("invalid range was partly accepted: %v, %v", addresses, err)
			}
		})
	}
}
