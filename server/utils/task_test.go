package utils

import "testing"

func TestGetCreateTaskTimeout(t *testing.T) {
	tests := []struct {
		name         string
		providerType string
		instanceType string
		want         int
	}{
		{name: "docker container", providerType: "docker", instanceType: "container", want: 1800},
		{name: "incus container", providerType: "incus", instanceType: "container", want: 3600},
		{name: "incus vm", providerType: "incus", instanceType: "vm", want: 7200},
		{name: "lxd vm mixed case", providerType: "LXD", instanceType: "VM", want: 7200},
		{name: "kubevirt vm", providerType: "kubevirt", instanceType: "vm", want: 7200},
		{name: "kubevirt container", providerType: "kubevirt", instanceType: "container", want: 3600},
		{name: "generic vm", providerType: "cloud", instanceType: "vm", want: 3600},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetCreateTaskTimeout(tt.providerType, tt.instanceType); got != tt.want {
				t.Fatalf("GetCreateTaskTimeout(%q, %q) = %d, want %d", tt.providerType, tt.instanceType, got, tt.want)
			}
		})
	}
}
