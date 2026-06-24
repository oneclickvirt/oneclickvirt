package incus

import "testing"

func TestImageURLAliasSuffix(t *testing.T) {
	tests := []struct {
		name         string
		alias        string
		instanceType string
		want         string
	}{
		{
			name:         "vm alias with url hash suffix",
			alias:        "oneclickvirt_debian-11-kvm-cloud_vm_amd64-6c84df04",
			instanceType: "vm",
			want:         "_vm_amd64-6c84df04",
		},
		{
			name:         "container alias with url hash suffix",
			alias:        "oneclickvirt_debian-12_container_amd64-ABCDEF12",
			instanceType: "container",
			want:         "_container_amd64-ABCDEF12",
		},
		{
			name:         "wrong instance type",
			alias:        "oneclickvirt_debian-11_vm_amd64-6c84df04",
			instanceType: "container",
			want:         "",
		},
		{
			name:         "missing url hash",
			alias:        "oneclickvirt_debian-11_vm_amd64",
			instanceType: "vm",
			want:         "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := imageURLAliasSuffix(tt.alias, tt.instanceType); got != tt.want {
				t.Fatalf("imageURLAliasSuffix() = %q, want %q", got, tt.want)
			}
		})
	}
}
