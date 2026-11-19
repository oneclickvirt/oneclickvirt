package kubevirt

import (
	"errors"
	"strings"
	"testing"

	"oneclickvirt/utils"
)

type versionExecutor struct {
	utils.ShellExecutor
	observed, client string
	observedError    error
	clientCalls      int
}

func (e *versionExecutor) Execute(command string) (string, error) {
	if strings.Contains(command, "kubectl get kubevirt") {
		return e.observed, e.observedError
	}
	if strings.Contains(command, "virtctl version --client") {
		e.clientCalls++
		return e.client, nil
	}
	return "", errors.New("unexpected version command")
}

func TestKubeVirtVersionIsSafeToPersist(t *testing.T) {
	client := `Client Version: version.Info{Major:"1", Minor:"9", GitVersion:"v1.9.1", GitCommit:"abc", GitTreeState:"clean", BuildDate:"2026-11-17", GoVersion:"go1.26.10", Compiler:"gc", Platform:"linux/amd64"}`
	for _, tc := range []struct {
		name, observed, client, want string
		observedError                error
		fallback                     bool
	}{
		{name: "cluster version", observed: "v1.9.1-rc.0+abc\r\n", want: "v1.9.1-rc.0+abc"},
		{name: "failed kubectl", observedError: errors.New("kube API unavailable"), client: client, want: "v1.9.1", fallback: true},
		{name: "missing status", client: client, want: "v1.9.1", fallback: true},
		{name: "plain client version", client: "Client Version: v1.9.1", want: "v1.9.1", fallback: true},
		{name: "oversized tag", client: "v1.9.1-" + strings.Repeat("a", 40), want: "unknown", fallback: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executor := &versionExecutor{observed: tc.observed, observedError: tc.observedError, client: tc.client}
			p := &KubeVirtProvider{sshClient: utils.NewSafeShellExecutor(executor)}
			if err := p.getKubeVirtVersion(); (err != nil) != (tc.want == "unknown") {
				t.Fatalf("version query error = %v", err)
			}
			if got := p.GetVersion(); got != tc.want || len(got) > 32 {
				t.Fatalf("stored version = %q, want %q", got, tc.want)
			}
			if (executor.clientCalls == 1) != tc.fallback {
				t.Fatalf("client version called %d times", executor.clientCalls)
			}
		})
	}
}

func TestKubeVirtDataVolumeYAMLUsesDefaultStorageClass(t *testing.T) {
	yaml := kubeVirtDataVolumeYAML("vm-1-dv", "vm-1", "https://example.com/image.qcow2", 20, "")

	if !strings.Contains(yaml, "storageClassName: \"local-path\"") {
		t.Fatalf("DataVolume YAML should use default local-path storage class, got:\n%s", yaml)
	}
	if strings.Contains(yaml, "storage.bind.immediate.requested") {
		t.Fatalf("DataVolume YAML should not request immediate binding for local-path:\n%s", yaml)
	}
}

func TestKubeVirtDataVolumeYAMLUsesExplicitStorageClass(t *testing.T) {
	yaml := kubeVirtDataVolumeYAML("vm-1-dv", "vm-1", "https://example.com/image.qcow2", 20, "fast-local")

	if !strings.Contains(yaml, "storageClassName: \"fast-local\"") {
		t.Fatalf("DataVolume YAML should use explicit storage class, got:\n%s", yaml)
	}
}

func TestKubeVirtSSHServiceYAMLUsesStableVMNameSelector(t *testing.T) {
	yaml := kubeVirtSSHServiceYAML("vm-1", 30122)

	if !strings.Contains(yaml, "name: \"vm-1-ssh\"") {
		t.Fatalf("SSH service YAML should name the service after the VM, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "kubevirt.io/vm: \"vm-1\"") {
		t.Fatalf("SSH service YAML should select KubeVirt launcher pods by VM name label, got:\n%s", yaml)
	}
	if strings.Contains(yaml, "kubevirt.io/domain:") {
		t.Fatalf("SSH service YAML should not use KubeVirt version-dependent selector labels, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "nodePort: 30122") {
		t.Fatalf("SSH service YAML should preserve the allocated nodePort, got:\n%s", yaml)
	}
}

func TestKubeVirtVMCloudInitUserDataUnlocksRootPasswordLogin(t *testing.T) {
	userData := kubeVirtVMCloudInitUserData("vm-1", "Passw0rd!\n")

	required := []string{
		"disable_root: false",
		"ssh_pwauth: true",
		"users:",
		"- name: root",
		"lock_passwd: false",
		"PermitRootLogin yes",
		"PasswordAuthentication yes",
		"root:Passw0rd!",
	}
	for _, item := range required {
		if !strings.Contains(userData, item) {
			t.Fatalf("cloud-init user-data missing %q:\n%s", item, userData)
		}
	}
	if strings.Contains(userData, "\nPassw0rd!\n") {
		t.Fatalf("cloud-init user-data should strip password newlines:\n%s", userData)
	}
}

func TestWithKubeVirtKubeconfigPrefixesCommand(t *testing.T) {
	got := withKubeVirtKubeconfig("virtctl stop vm-1")
	want := "KUBECONFIG='/etc/rancher/k3s/k3s.yaml' virtctl stop vm-1"
	if got != want {
		t.Fatalf("withKubeVirtKubeconfig() = %q, want %q", got, want)
	}
}

func TestKubeVirtContainerResourcesUseSmallRequestsAndConfiguredLimits(t *testing.T) {
	yaml := buildKubeVirtContainerResourcesYAML("2", 2048)

	required := []string{
		`requests:`,
		`cpu: "100m"`,
		`memory: "128Mi"`,
		`limits:`,
		`cpu: "2"`,
		`memory: "2048Mi"`,
	}
	for _, item := range required {
		if !strings.Contains(yaml, item) {
			t.Fatalf("container resources YAML missing %q:\n%s", item, yaml)
		}
	}
}

func TestKubeVirtContainerStartupUsesPersistentPasswordEnvironment(t *testing.T) {
	script := buildKubeVirtContainerStartupScript()
	if !strings.Contains(script, `${ONECLICKVIRT_ROOT_PASSWORD:-password}`) {
		t.Fatalf("container startup script should read the persisted password environment variable:\n%s", script)
	}
	if strings.Contains(script, "Passw0rd!") {
		t.Fatalf("container startup script should not embed a concrete password:\n%s", script)
	}
}

func TestSSHServiceDoesNotAdvertiseUnconfiguredGuestIPv6(t *testing.T) {
	s := kubeVirtSSHServiceYAML("vm-1", 30122)
	if !strings.Contains(s, "ipFamilyPolicy: SingleStack") || !strings.Contains(s, "ipFamilies: [IPv4]") {
		t.Fatal("IPv4 guest must use IPv4 endpoints")
	}
}
