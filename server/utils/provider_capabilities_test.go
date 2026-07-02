package utils

import (
	"reflect"
	"testing"
)

func TestNormalizeSystemImageProviderTypeMapsProxmoxAliases(t *testing.T) {
	tests := map[string]string{
		"proxmox":    "proxmox",
		"proxmoxve":  "proxmox",
		"pve":        "proxmox",
		" KubeVirt ": "kubevirt",
	}

	for input, want := range tests {
		if got := NormalizeSystemImageProviderType(input); got != want {
			t.Fatalf("NormalizeSystemImageProviderType(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSystemImageProviderTypeCandidatesPreservesRawFallback(t *testing.T) {
	got := SystemImageProviderTypeCandidates("proxmoxve")
	want := []string{"proxmox", "proxmoxve"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SystemImageProviderTypeCandidates() = %#v, want %#v", got, want)
	}
}

func TestSystemImageProviderTypeMatchesAliasesAndLists(t *testing.T) {
	tests := []struct {
		name       string
		provider   string
		imageTypes string
		want       bool
	}{
		{name: "proxmoxve uses proxmox image", provider: "proxmoxve", imageTypes: "proxmox", want: true},
		{name: "pve uses proxmox image", provider: "pve", imageTypes: "proxmox", want: true},
		{name: "comma separated list", provider: "podman", imageTypes: "docker,podman,containerd", want: true},
		{name: "different family", provider: "kubevirt", imageTypes: "qemu", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SystemImageProviderTypeMatches(tt.provider, tt.imageTypes); got != tt.want {
				t.Fatalf("SystemImageProviderTypeMatches(%q, %q) = %v, want %v", tt.provider, tt.imageTypes, got, tt.want)
			}
		})
	}
}
