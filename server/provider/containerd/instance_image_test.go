package containerd

import "testing"

func TestContainerdManagedImageName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "adds prefix", in: "debian:12", want: "oneclickvirt_debian:12"},
		{name: "preserves existing prefix", in: "oneclickvirt_spiritlhl-debian", want: "oneclickvirt_spiritlhl-debian"},
		{name: "preserves namespaced existing prefix", in: "docker.io/library/oneclickvirt_spiritlhl-debian", want: "docker.io/library/oneclickvirt_spiritlhl-debian"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containerdManagedImageName(tt.in); got != tt.want {
				t.Fatalf("containerdManagedImageName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
