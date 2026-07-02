package provider

import "testing"

func TestIsStandardProviderOperableStatus(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{status: "active", want: true},
		{status: "partial", want: true},
		{status: " Partial ", want: true},
		{status: "inactive", want: false},
		{status: "error", want: false},
		{status: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			if got := isStandardProviderOperableStatus(tt.status); got != tt.want {
				t.Fatalf("isStandardProviderOperableStatus(%q) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}
