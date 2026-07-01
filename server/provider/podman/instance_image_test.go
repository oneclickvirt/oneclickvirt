package podman

import "testing"

func TestPodmanManagedImageName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "adds prefix", in: "debian:12", want: "oneclickvirt_debian:12"},
		{name: "preserves existing prefix", in: "oneclickvirt_spiritlhl-debian", want: "oneclickvirt_spiritlhl-debian"},
		{name: "preserves localhost existing prefix", in: "localhost/oneclickvirt_spiritlhl-debian", want: "localhost/oneclickvirt_spiritlhl-debian"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := podmanManagedImageName(tt.in); got != tt.want {
				t.Fatalf("podmanManagedImageName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
