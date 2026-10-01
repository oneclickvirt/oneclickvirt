package provider

import (
	"errors"
	"testing"
)

func TestProviderHealthRequiresAUsableConnection(t *testing.T) {
	failed := errors.New("probe failed")
	for _, tt := range []struct {
		name, connection, rule, ssh, api, want string
		err                                    error
	}{
		{"unprobed API cannot rescue failed SSH", "ssh", "auto", "offline", "unknown", "inactive", failed},
		{"both unknown", "ssh", "auto", "unknown", "unknown", "inactive", failed},
		{"unsupported API and failed SSH", "ssh", "auto", "offline", "N/A", "inactive", failed},
		{"SSH works with unprobed API", "ssh", "auto", "online", "unknown", "active", nil},
		{"API works while SSH fails", "ssh", "auto", "offline", "online", "partial", failed},
		{"SSH works while API fails", "ssh", "auto", "online", "offline", "partial", failed},
		{"API only ignores working SSH", "ssh", "api_only", "online", "offline", "inactive", failed},
		{"API only ignores failed SSH", "agent", "api_only", "offline", "online", "active", nil},
		{"SSH only ignores working API", "ssh", "ssh_only", "offline", "online", "inactive", failed},
		{"local runtime failed", "local", "auto", "N/A", "N/A", "inactive", failed},
		{"local runtime works", "local", "auto", "N/A", "N/A", "active", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := providerHealthStatus(tt.connection, tt.rule, tt.ssh, tt.api, tt.err); got != tt.want {
				t.Fatalf("status = %q, want %q", got, tt.want)
			}
		})
	}
}
