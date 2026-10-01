package images

import "testing"

func TestImageURLWithCDN(t *testing.T) {
	tests := []struct {
		name, origin, endpoint, want string
		enabled                      bool
	}{
		{"https enabled", "https://example.com/image.qcow2", "https://cdn.example/", "https://cdn.example/https://example.com/image.qcow2", true},
		{"http official template", "http://download.proxmox.com/template.tar.zst", "https://cdn.example/", "http://download.proxmox.com/template.tar.zst", true},
		{"disabled", "https://example.com/image.qcow2", "https://cdn.example/", "https://example.com/image.qcow2", false},
		{"empty endpoint", "https://example.com/image.qcow2", "", "https://example.com/image.qcow2", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := imageURLWithCDN(tt.origin, tt.enabled, tt.endpoint); got != tt.want {
				t.Fatalf("imageURLWithCDN() = %q, want %q", got, tt.want)
			}
		})
	}
}
