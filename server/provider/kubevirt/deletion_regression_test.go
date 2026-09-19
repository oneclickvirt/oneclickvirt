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
