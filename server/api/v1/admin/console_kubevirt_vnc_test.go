package admin

import (
	"os/exec"
	"strings"
	"testing"
)

func TestKubeVirtConsoleAccessProbeCommands(t *testing.T) {
	for _, subresource := range []string{"vnc", "console"} {
		command, ok := kubeVirtConsoleAccessProbeCommand(subresource)
		if !ok || !strings.Contains(command, "command -v virtctl") || !strings.Contains(command, "virtualmachineinstances/"+subresource) {
			t.Fatalf("KubeVirt %s access probe = (%q, %v)", subresource, command, ok)
		}
		check := exec.Command("sh", "-n")
		check.Stdin = strings.NewReader(command)
		if output, err := check.CombinedOutput(); err != nil {
			t.Fatalf("KubeVirt %s access probe has invalid shell syntax: %v\n%s", subresource, err, output)
		}
	}
	if command, ok := kubeVirtConsoleAccessProbeCommand("invalid"); ok || command != "" {
		t.Fatalf("unsupported KubeVirt console subresource = (%q, %v)", command, ok)
	}
}

func TestKubeVirtPodDiscoveryPrefersScopedLabelAndRejectsAmbiguity(t *testing.T) {
	selectors := kubeVirtPodSelectors("guest-a")
	if len(selectors) != 2 || selectors[0].labelKey != "oneclickvirt.io/instance" || selectors[1].labelKey != "app" {
		t.Fatalf("KubeVirt Pod selector priority = %#v", selectors)
	}
	command := kubeVirtPodListCommand("oneclickvirt.io/instance=unsafe; guest")
	if !strings.Contains(command, "'oneclickvirt.io/instance=unsafe; guest'") {
		t.Fatalf("KubeVirt Pod label was not shell quoted: %s", command)
	}

	raw := `{"items":[
		{"metadata":{"name":"wrong","labels":{"app":"guest-a"}},"status":{"phase":"Running"},"spec":{"containers":[{"stdin":false}]}},
		{"metadata":{"name":"right","labels":{"oneclickvirt.io/instance":"guest-a"}},"status":{"phase":"Running"},"spec":{"containers":[{"stdin":true}]}}
	]}`
	pod, ok := kubeVirtRunningPodForLabel(raw, "oneclickvirt.io/instance", "guest-a")
	if !ok || pod.name != "right" || !pod.stdin {
		t.Fatalf("scoped KubeVirt Pod = %#v, %v", pod, ok)
	}
	duplicate := `{"items":[
		{"metadata":{"name":"one","labels":{"oneclickvirt.io/instance":"guest-a"}},"status":{"phase":"Running"}},
		{"metadata":{"name":"two","labels":{"oneclickvirt.io/instance":"guest-a"}},"status":{"phase":"Running"}}
	]}`
	if _, ok := kubeVirtRunningPodForLabel(duplicate, "oneclickvirt.io/instance", "guest-a"); ok {
		t.Fatal("ambiguous KubeVirt Pod selector unexpectedly chose one Pod")
	}
}

func TestKubeVirtVNCSessionParsingAndLockValidation(t *testing.T) {
	valid := "ONECLICKVIRT_KUBEVIRT_VNC\t123\t6200\t/run/oneclickvirt-kubevirt-vnc/session-123-1\n"
	session, err := parseKubeVirtVNCSession(valid)
	if err != nil || session.pid != 123 || session.port != 6200 {
		t.Fatalf("valid KubeVirt VNC session = %#v, %v", session, err)
	}
	for _, value := range []string{
		"/run/oneclickvirt-kubevirt-vnc/session-123-1/child",
		"/run/oneclickvirt-kubevirt-vnc/session-../child",
		"/run/oneclickvirt-kubevirt-vnc/session-123.1",
		"/tmp/oneclickvirt-kubevirt-vnc/session-123-1/",
		"/tmp/oneclickvirt-kubevirt-vnc/session-",
	} {
		if isKubeVirtVNCLockDir(value) {
			t.Fatalf("unsafe KubeVirt VNC lock path accepted: %q", value)
		}
		if _, err := parseKubeVirtVNCSession("ONECLICKVIRT_KUBEVIRT_VNC\t123\t6200\t" + value); err == nil {
			t.Fatalf("unsafe KubeVirt VNC session marker accepted: %q", value)
		}
	}
}

func TestKubeVirtVNCCommandsAreShellValidAndUseListenerFallback(t *testing.T) {
	start := kubeVirtVNCStartCommand("guest-a", "123-1")
	stop := kubeVirtVNCStopCommand(kubeVirtVNCSession{pid: 123, port: 6200, lockDir: "/run/oneclickvirt-kubevirt-vnc/session-123-1"})
	for _, command := range []string{start, stop} {
		check := exec.Command("sh", "-n")
		check.Stdin = strings.NewReader(command)
		if output, err := check.CombinedOutput(); err != nil {
			t.Fatalf("KubeVirt VNC command has invalid shell syntax: %v\n%s\n%s", err, output, command)
		}
	}
	for _, expected := range []string{"command -v ss", "command -v netstat", "netstat -ltn", "port_is_listening"} {
		if !strings.Contains(start, expected) {
			t.Fatalf("KubeVirt VNC start command omitted %q", expected)
		}
	}
	if !strings.Contains(stop, "SESSION=\"${LOCK##*/}\"") || !strings.Contains(stop, "*[!A-Za-z0-9_-]*)") {
		t.Fatalf("KubeVirt VNC stop command did not validate a single session segment: %s", stop)
	}
}
