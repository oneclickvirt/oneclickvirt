package docker

import "testing"

func TestDockerManagedImageName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "adds prefix", in: "debian:12", want: "oneclickvirt_debian:12"},
		{name: "preserves existing prefix", in: "oneclickvirt_spiritlhl-debian", want: "oneclickvirt_spiritlhl-debian"},
		{name: "preserves namespaced existing prefix", in: "localhost/oneclickvirt_spiritlhl-debian", want: "localhost/oneclickvirt_spiritlhl-debian"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dockerManagedImageName(tt.in); got != tt.want {
				t.Fatalf("dockerManagedImageName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
