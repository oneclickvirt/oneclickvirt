package public

import "testing"

func TestDateReleaseVersionsCompareByTimestamp(t *testing.T) {
	if !isVersionNewer("v20260923-141128", "v20260923-141127") {
		t.Fatal("a later release on the same day was not detected")
	}
	if isVersionNewer("v20260923-141126", "v20260923-141127") {
		t.Fatal("an earlier release on the same day was detected as newer")
	}
}

func TestTimestampReleaseIsNewerThanLegacyMarker(t *testing.T) {
	if !isVersionNewer("v20260925-052714", "v0.3.0") {
		t.Fatal("timestamp release was not detected as newer than the legacy marker")
	}
	if isVersionNewer("v0.3.0", "v20260925-052714") {
		t.Fatal("legacy marker was detected as newer than a timestamp release")
	}
}

func TestLegacyNumericVersionsRemainSupported(t *testing.T) {
	if !isVersionNewer("v1.10.0", "v1.9.9") {
		t.Fatal("semantic release ordering regressed")
	}
}
