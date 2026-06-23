package provider

import (
	"testing"
	"time"

	providerModel "oneclickvirt/model/provider"
)

func TestProviderCreateSSHWaitTimeout(t *testing.T) {
	tests := []struct {
		name     string
		provider providerModel.Provider
		instance providerModel.Instance
		want     time.Duration
	}{
		{
			name:     "lxd vm post create ssh wait is nonblocking",
			provider: providerModel.Provider{Type: "lxd"},
			instance: providerModel.Instance{InstanceType: "vm"},
			want:     90 * time.Second,
		},
		{
			name:     "incus vm post create ssh wait is nonblocking",
			provider: providerModel.Provider{Type: "incus"},
			instance: providerModel.Instance{InstanceType: "vm"},
			want:     90 * time.Second,
		},
		{
			name:     "container wait remains short",
			provider: providerModel.Provider{Type: "lxd"},
			instance: providerModel.Instance{InstanceType: "container"},
			want:     30 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := providerCreateSSHWaitTimeout(tt.provider, tt.instance); got != tt.want {
				t.Fatalf("providerCreateSSHWaitTimeout() = %s, want %s", got, tt.want)
			}
		})
	}
}
