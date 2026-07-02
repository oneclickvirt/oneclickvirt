package qemu

import (
	"reflect"
	"testing"
)

func TestQEMUPasswordCandidatesIncludesDesiredThenDefault(t *testing.T) {
	got := qemuPasswordCandidates("NewPass123!")
	want := []string{"NewPass123!", qemuDefaultGuestPassword}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("qemuPasswordCandidates() = %#v, want %#v", got, want)
	}
}

func TestQEMUPasswordCandidatesDeduplicatesDefault(t *testing.T) {
	got := qemuPasswordCandidates(qemuDefaultGuestPassword)
	want := []string{qemuDefaultGuestPassword}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("qemuPasswordCandidates() = %#v, want %#v", got, want)
	}
}
