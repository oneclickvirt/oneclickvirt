package kubevirt

import (
	"context"
	"errors"
	"strings"
	"testing"

	"oneclickvirt/utils"
)

type kubeLifecycleExecutor struct {
	utils.ShellExecutor
	commands []string
	outputs  []string
	fail     bool
}

func (e *kubeLifecycleExecutor) ExecuteContext(_ context.Context, command string) (string, error) {
	i := len(e.commands)
	e.commands = append(e.commands, command)
	if e.fail {
		return "API unavailable", errors.New("exit 1")
	}
	if i < len(e.outputs) {
		return e.outputs[i], nil
	}
	return "", nil
}
func (e *kubeLifecycleExecutor) IsHealthy() bool { return true }

func TestKubeVirtDiscoveryErrorDoesNotMutateOtherBackend(t *testing.T) {
	for _, action := range []string{"start", "stop", "restart"} {
		p := NewKubeVirtProvider().(*KubeVirtProvider)
		p.connected = true
		e := &kubeLifecycleExecutor{fail: true}
		p.sshClient.SetExecutor(e)
		var err error
		switch action {
		case "start":
			err = p.StartInstance(context.Background(), "guest")
		case "stop":
			err = p.StopInstance(context.Background(), "guest")
		case "restart":
			err = p.RestartInstance(context.Background(), "guest")
		}
		if err == nil || len(e.commands) != 1 {
			t.Fatalf("%s misrouted after discovery failure: %v, %v", action, err, e.commands)
		}
	}
}

func TestK3sStopWaitsForActualDeploymentPods(t *testing.T) {
	p := NewKubeVirtProvider().(*KubeVirtProvider)
	p.connected = true
	e := &kubeLifecycleExecutor{outputs: []string{
		"deployment.apps/guest", "scaled", `{"spec":{"selector":{"matchLabels":{"custom":"imported"}}}}`, "",
	}}
	p.sshClient.SetExecutor(e)
	if err := p.StopInstance(context.Background(), "guest"); err != nil {
		t.Fatal(err)
	}
	if len(e.commands) != 4 || !strings.Contains(e.commands[3], "'custom=imported'") {
		t.Fatalf("wrong pod selector: %v", e.commands)
	}
}

func TestDeploymentSelectorRejectsMissingSelector(t *testing.T) {
	if _, err := deploymentPodSelector(`{"spec":{}}`); err == nil {
		t.Fatal("missing selector accepted")
	}
	selector, err := deploymentPodSelector(`{"spec":{"selector":{"matchExpressions":[{"key":"role","operator":"In","values":["web","worker"]},{"key":"old","operator":"DoesNotExist"}]}}}`)
	if err != nil || selector != "!old,role in (web,worker)" {
		t.Fatalf("selector = %q, %v", selector, err)
	}
}
