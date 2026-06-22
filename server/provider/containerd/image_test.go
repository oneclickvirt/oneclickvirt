package containerd

import "testing"

func TestContainerdRuntimeImageRef(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "single component adds library and latest", in: "oneclickvirt_spiritlhl-debian", want: "docker.io/library/oneclickvirt_spiritlhl-debian:latest"},
		{name: "single component preserves explicit tag", in: "oneclickvirt_debian:12", want: "docker.io/library/oneclickvirt_debian:12"},
		{name: "docker hub library prefix normalized", in: "docker.io/library/oneclickvirt_debian:12", want: "docker.io/library/oneclickvirt_debian:12"},
		{name: "docker hub repository path is preserved", in: "docker.io/acme/oneclickvirt_debian:12", want: "docker.io/acme/oneclickvirt_debian:12"},
		{name: "repository path is not forced under library", in: "localhost/oneclickvirt_debian:12", want: "localhost/oneclickvirt_debian:12"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containerdRuntimeImageRef(tt.in); got != tt.want {
				t.Fatalf("containerdRuntimeImageRef(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestContainerdImageReferenceVariantsIncludeRuntimeRef(t *testing.T) {
	refs := containerdImageReferenceVariants("oneclickvirt_spiritlhl-debian")
	want := "docker.io/library/oneclickvirt_spiritlhl-debian:latest"
	for _, ref := range refs {
		if ref == want {
			return
		}
	}
	t.Fatalf("containerdImageReferenceVariants missing %q: %#v", want, refs)
}
