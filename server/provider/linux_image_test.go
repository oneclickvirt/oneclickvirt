package provider

import "testing"

func TestLegacyLinuxImageNamesAndExplicitAliases(t *testing.T) {
	for input, want := range map[string]string{
		"debian12": "debian:12", "debian-12": "debian:12", "Ubuntu_22.04": "ubuntu:22.04",
		"oraclelinux8": "oraclelinux:8", "alpine3.20": "alpine:3.20",
		"debian-12-kvm-cloud": "debian-12-kvm-cloud", "my-debian12": "my-debian12",
		"images:debian/12/cloud": "images:debian/12/cloud", "local:debian12": "local:debian12",
		"debian:12": "debian:12", "debian12.unknown": "debian12.unknown",
	} {
		if got := NormalizeLinuxImageAlias(input); got != want {
			t.Errorf("NormalizeLinuxImageAlias(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestMissingImageErrorsAcrossCLIVersions(t *testing.T) {
	for _, message := range []string{"Failed to find image", "Error: image not found", "no such image", `Error: Image "debian12" not found`, "Error: Image 'debian12' not found"} {
		if !RuntimeImageNotFound(message) {
			t.Errorf("missing-image error was not recognized: %q", message)
		}
	}
	for _, message := range []string{"Storage pool not found", "Image architecture mismatch", "Permission denied", ""} {
		if RuntimeImageNotFound(message) {
			t.Errorf("unrelated failure was treated as a missing image: %q", message)
		}
	}
}
