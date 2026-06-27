package lxd

import "testing"

func TestLXDStartNeedsCloudInitTemplateRepair(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{
			name: "cloud init vendor data",
			text: "Error: Failed to read template file: open /var/snap/lxd/common/lxd/virtual-machines/vm/templates/cloud-init-vendor-data.tpl: no such file or directory",
			want: true,
		},
		{
			name: "hostname template",
			text: "Error: Failed to read template file: open /var/snap/lxd/common/lxd/virtual-machines/vm/templates/hostname.tpl: no such file or directory",
			want: true,
		},
		{
			name: "unrelated start error",
			text: "Error: Failed to start device eth0",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lxdStartNeedsCloudInitTemplateRepair(tt.text); got != tt.want {
				t.Fatalf("lxdStartNeedsCloudInitTemplateRepair() = %v, want %v", got, tt.want)
			}
		})
	}
}
