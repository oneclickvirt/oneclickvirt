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

func TestParsePodmanImageList(t *testing.T) {
	output := "docker.io/library/alpine|latest|28bd5fe8b56d|8.71MB|2026-07-01 16:30:30 +0800 CST\n" +
		"REPOSITORY|TAG|IMAGE ID|SIZE|CREATED\n" +
		"table alpine\\tlatest\\tbad\\t0B\\t2026-07-01 16:30:30 +0800 CST\n"

	images := parsePodmanImageList(output)
	if len(images) != 1 {
		t.Fatalf("expected 1 parsed image, got %d: %#v", len(images), images)
	}
	if images[0].Name != "docker.io/library/alpine" || images[0].Tag != "latest" ||
		images[0].ID != "28bd5fe8b56d" || images[0].Size != "8.71MB" {
		t.Fatalf("unexpected parsed image: %#v", images[0])
	}
}
