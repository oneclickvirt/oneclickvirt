package kubevirt

import (
	"reflect"
	"strings"
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

func TestKubeVirtNodePortSSHHostsPrefersProviderAndNodeAddresses(t *testing.T) {
	got := kubeVirtNodePortSSHHosts("38.60.216.86", "10.42.0.1\n38.60.216.86\n<none>\n")
	want := []string{"38.60.216.86", "10.42.0.1", "127.0.0.1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("kubeVirtNodePortSSHHosts() = %#v, want %#v", got, want)
	}
}

func TestKubeVirtNodePortSSHHostsFallsBackToLoopback(t *testing.T) {
	got := kubeVirtNodePortSSHHosts("", "")
	want := []string{"127.0.0.1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("kubeVirtNodePortSSHHosts() = %#v, want %#v", got, want)
	}
}

func TestKubeVirtK3sChpasswdCommandUsesKubectlStdin(t *testing.T) {
	cmd := kubeVirtK3sChpasswdCommand("kubevirt-vms", "ct-abc-123", "NewPass123!")
	for _, want := range []string{
		"printf 'root:%s\\n'",
		"| kubectl exec -i -n 'kubevirt-vms' 'ct-abc-123' -- chpasswd",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("command %q does not contain %q", cmd, want)
		}
	}
	if strings.Contains(cmd, "/bin/sh -c") {
		t.Fatalf("command should pipe chpasswd input directly, got %q", cmd)
	}
}
