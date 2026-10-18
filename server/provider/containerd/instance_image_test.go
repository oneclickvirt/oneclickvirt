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

func TestContainerdCachedImageNeedsKeepAlive(t *testing.T) {
	for _, test := range []struct {
		name, config  string
		want, wantErr bool
	}{
		{"legacy raw shell", `{"ImageConfig":{"config":{"Cmd":["bash"]}}}`, true, false},
		{"managed entrypoint", `{"ImageConfig":{"config":{"Entrypoint":["/entrypoint.sh"],"Cmd":["bash"]}}}`, false, false},
		{"daemon", `{"ImageConfig":{"config":{"Cmd":["/usr/sbin/sshd","-D"]}}}`, false, false},
		{"recorded fallback", `{"Image":{"Labels":{"oneclickvirt.registry-fallback":"true"}},"ImageConfig":{"config":{"Entrypoint":["/entrypoint.sh"]}}}`, true, false},
		{"alias list", `[{"ImageConfig":{"config":{"Cmd":["bash"]}}},{"ImageConfig":{"config":{"Cmd":["bash"]}}}]`, true, false},
		{"malformed", `not json`, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := containerdCachedImageNeedsKeepAlive(test.config)
			if got != test.want || (err != nil) != test.wantErr {
				t.Fatalf("got %v %v", got, err)
			}
		})
	}
}
