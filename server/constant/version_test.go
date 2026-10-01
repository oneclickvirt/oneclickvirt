package constant

import "testing"

func TestIsReleaseVersion(t *testing.T) {
	for _, value := range []string{
		"v20260925-052714",
		"v00000000-000000",
	} {
		if !IsReleaseVersion(value) {
			t.Fatalf("release tag %q was rejected", value)
		}
	}
	for _, value := range []string{
		"validator-ab177620a",
		"0.3.0",
		"v20260925-05271",
		"v20260925052714",
		"v20260925-052714-dev",
		"20260925-052714",
	} {
		if IsReleaseVersion(value) {
			t.Fatalf("non-release version %q was accepted", value)
		}
	}
}
