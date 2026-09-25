package kubevirt

import (
	"errors"
	"strings"
	"testing"
	"time"

	"oneclickvirt/utils"
)

type kubeVirtDeleteExecutor struct{ utils.ShellExecutor }

func (kubeVirtDeleteExecutor) Execute(command string) (string, error) {
	return "api server is temporarily unavailable", errKubeVirtDeleteTest
}

func (kubeVirtDeleteExecutor) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	return kubeVirtDeleteExecutor{}.Execute(command)
}

var errKubeVirtDeleteTest = errors.New("connection refused")

type kubeVirtNADDeleteExecutor struct {
	utils.ShellExecutor
	commands    []string
	crdOutput   string
	crdError    error
	deleteError error
}

func (e *kubeVirtNADDeleteExecutor) Execute(command string) (string, error) {
	e.commands = append(e.commands, command)
	if strings.Contains(command, "kubectl get crd network-attachment-definitions") {
		return e.crdOutput, e.crdError
	}
	if strings.Contains(command, "kubectl delete network-attachment-definition") {
		return "", e.deleteError
	}
	return "", errors.New("unexpected command")
}

func (e *kubeVirtNADDeleteExecutor) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}

func TestKubeVirtNADCleanupSkipsMissingOptionalCRD(t *testing.T) {
	executor := &kubeVirtNADDeleteExecutor{}
	p := &KubeVirtProvider{sshClient: utils.NewSafeShellExecutor(executor)}
	if err := p.deleteRoutedKubeVirtNADByInstance("guest"); err != nil {
		t.Fatal(err)
	}
	if len(executor.commands) != 1 || !strings.Contains(executor.commands[0], "kubectl get crd") {
		t.Fatalf("missing CRD triggered a NAD deletion: %#v", executor.commands)
	}
}

func TestKubeVirtNADCleanupDeletesWhenCRDExists(t *testing.T) {
	executor := &kubeVirtNADDeleteExecutor{crdOutput: "customresourcedefinition.apiextensions.k8s.io/network-attachment-definitions.k8s.cni.cncf.io"}
	p := &KubeVirtProvider{sshClient: utils.NewSafeShellExecutor(executor)}
	if err := p.deleteRoutedKubeVirtNAD(routedKubeVirtIPv6Plan{NADName: "guest-v6"}); err != nil {
		t.Fatal(err)
	}
	if len(executor.commands) != 2 || !strings.Contains(executor.commands[1], "kubectl delete network-attachment-definition 'guest-v6'") {
		t.Fatalf("existing CRD did not delete its NAD: %#v", executor.commands)
	}
}

func TestKubeVirtNADCleanupDoesNotMaskAPIOutage(t *testing.T) {
	executor := &kubeVirtNADDeleteExecutor{crdError: errKubeVirtDeleteTest}
	p := &KubeVirtProvider{sshClient: utils.NewSafeShellExecutor(executor)}
	if err := p.deleteRoutedKubeVirtNADByInstance("guest"); !errors.Is(err, errKubeVirtDeleteTest) {
		t.Fatalf("CRD lookup outage was masked: %v", err)
	}
	if len(executor.commands) != 1 {
		t.Fatalf("CRD lookup failure proceeded to delete: %#v", executor.commands)
	}
}

func TestKubeVirtDeleteResourceDoesNotTreatConnectionFailureAsNotFound(t *testing.T) {
	p := &KubeVirtProvider{sshClient: utils.NewSafeShellExecutor(kubeVirtDeleteExecutor{})}
	err := p.deleteKubeVirtResource("kubectl delete vm guest --ignore-not-found=true", "delete VM")
	if err == nil || !strings.Contains(err.Error(), "delete VM失败") {
		t.Fatalf("expected connection failure, got %v", err)
	}
}

func TestKubeVirtNotFoundRequiresNotFoundEvidence(t *testing.T) {
	if kubeVirtNotFound("api server is temporarily unavailable", errors.New("connection refused")) {
		t.Fatal("connection failure was classified as not found")
	}
	if !kubeVirtNotFound("Error from server (NotFound): virtualmachines.kubevirt.io", errors.New("request failed")) {
		t.Fatal("NotFound response was not classified as not found")
	}
}
