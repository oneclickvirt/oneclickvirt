package provider

import "testing"

func TestIsRuntimeInfrastructureContainer(t *testing.T) {
	for _, name := range []string{"ndpresponder", "/ndpresponder", "\x1b[36m/ndpresponder\x1b[0m\r\n"} {
		if !IsRuntimeInfrastructureContainer(name) {
			t.Fatalf("%q was exposed as a user instance", name)
		}
	}
	for _, name := range []string{"user-ndpresponder", "ndpresponder-demo", "normal"} {
		if IsRuntimeInfrastructureContainer(name) {
			t.Fatalf("%q was hidden", name)
		}
	}
}

func TestIsRuntimeInfrastructureImage(t *testing.T) {
	for _, image := range []string{"docker.io/spiritlhl/ndpresponder_x86", "localhost/oneclickvirt-ndpresponder", "ndpresponder"} {
		if !IsRuntimeInfrastructureImage(image) {
			t.Fatalf("%q was offered as a guest image", image)
		}
	}
	if IsRuntimeInfrastructureImage("localhost/ndpresponder-demo") {
		t.Fatal("unrelated image was hidden")
	}
}
