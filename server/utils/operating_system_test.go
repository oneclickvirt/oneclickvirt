package utils

import "testing"

func TestDetectOSTypeFromCompactImageNames(t *testing.T) {
	tests := map[string]string{
		"almalinux8":         "almalinux",
		"alpinelinux_stable": "alpine",
		"centos10-stream":    "centos",
		"debian12":           "debian",
		"fedora34":           "fedora",
		"rockylinux9":        "rockylinux",
		"ubuntu24":           "ubuntu",
		"backup-ubuntu22":    "ubuntu",
	}

	for input, want := range tests {
		if got := DetectOSTypeFromText(input); got != want {
			t.Errorf("DetectOSTypeFromText(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDetectOSTypeFromTextDoesNotMatchEmbeddedWords(t *testing.T) {
	for _, input := range []string{"archipelago", "windowshade", "mydebian12", "notubuntu24"} {
		if got := DetectOSTypeFromText(input); got != "" {
			t.Errorf("DetectOSTypeFromText(%q) = %q, want empty", input, got)
		}
	}
}
