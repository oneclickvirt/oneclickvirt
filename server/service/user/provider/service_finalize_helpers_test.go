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

func TestShouldDefaultInstanceSSHPortTo22(t *testing.T) {
	tests := []struct {
		name         string
		providerType string
		instanceType string
		want         bool
	}{
		{
			name:         "lxd vm keeps default direct ssh",
			providerType: "lxd",
			instanceType: "vm",
			want:         true,
		},
		{
			name:         "kubevirt vm uses allocated nodeport mapping",
			providerType: "kubevirt",
			instanceType: "vm",
			want:         false,
		},
		{
			name:         "qemu vm uses allocated positional mapping",
			providerType: "qemu",
			instanceType: "vm",
			want:         false,
		},
		{
			name:         "docker container keeps runtime mapping",
			providerType: "docker",
			instanceType: "container",
			want:         false,
		},
		{
			name:         "kubevirt container keeps runtime mapping",
			providerType: "kubevirt",
			instanceType: "container",
			want:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldDefaultInstanceSSHPortTo22(tt.providerType, tt.instanceType); got != tt.want {
				t.Fatalf("shouldDefaultInstanceSSHPortTo22(%q, %q) = %v, want %v", tt.providerType, tt.instanceType, got, tt.want)
			}
		})
	}
}
