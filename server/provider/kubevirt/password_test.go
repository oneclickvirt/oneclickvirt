package kubevirt

import (
	"reflect"
	"testing"
)

func TestKubeVirtPasswordCandidatesIncludesDesiredThenDefault(t *testing.T) {
	got := kubeVirtPasswordCandidates("NewPass123!")
	want := []string{"NewPass123!", kubeVirtDefaultGuestPassword}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("kubeVirtPasswordCandidates() = %#v, want %#v", got, want)
	}
}

func TestKubeVirtPasswordCandidatesDeduplicatesDefault(t *testing.T) {
	got := kubeVirtPasswordCandidates(kubeVirtDefaultGuestPassword)
	want := []string{kubeVirtDefaultGuestPassword}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("kubeVirtPasswordCandidates() = %#v, want %#v", got, want)
	}
}
