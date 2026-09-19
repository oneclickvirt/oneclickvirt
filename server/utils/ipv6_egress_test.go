package utils

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

type ipv6EgressExecutor struct {
	output  string
	err     error
	command string
}

func (e *ipv6EgressExecutor) Execute(command string) (string, error) {
	return e.ExecuteWithTimeout(command, 0)
}
func (e *ipv6EgressExecutor) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	e.command = command
	return e.output, e.err
}
func (e *ipv6EgressExecutor) ExecuteWithLogging(command, _ string) (string, error) {
	return e.Execute(command)
}
func (e *ipv6EgressExecutor) ExecuteRaw(command string, timeout time.Duration) (string, error) {
	return e.ExecuteWithTimeout(command, timeout)
}
func (*ipv6EgressExecutor) ExecuteViaTempScript(string, []string, time.Duration) (string, error) {
	return "", nil
}
func (*ipv6EgressExecutor) UploadContent(string, string, os.FileMode) error { return nil }
func (*ipv6EgressExecutor) IsHealthy() bool                                 { return true }
func (*ipv6EgressExecutor) Reconnect() error                                { return nil }
func (*ipv6EgressExecutor) Close() error                                    { return nil }

func TestIPv6HostEgressCommandRepairsOnlyProviderManagedPaths(t *testing.T) {
	command := IPv6HostEgressCommand(IPv6EgressOptions{PersistRA: true})
	check := exec.Command("bash", "-n")
	check.Stdin = strings.NewReader(command)
	var stderr bytes.Buffer
	check.Stderr = &stderr
	if err := check.Run(); err != nil {
		t.Fatalf("IPv6HostEgressCommand() is not valid shell: %v: %s", err, stderr.String())
	}
	for _, fragment := range []string{
		"ip -6 route show default",
		"accept_ra",
		"networkctl reconfigure",
		"rdisc6 -1",
		"dhclient -6 -1",
		"curl --noproxy '*' -6 -fsS",
		"https://ipv6.ip.sb",
		"/etc/sysctl.d/99-oneclickvirt-ipv6-egress.conf",
	} {
		if !strings.Contains(command, fragment) {
			t.Fatalf("IPv6HostEgressCommand() missing %q", fragment)
		}
	}
	for _, forbidden := range []string{"ip -6 route add default via", "2001:db8", "/etc/network/interfaces"} {
		if strings.Contains(command, forbidden) {
			t.Fatalf("IPv6HostEgressCommand() guesses or overwrites network config via %q", forbidden)
		}
	}
}

func TestEnsureIPv6HostEgressFailsClosed(t *testing.T) {
	executor := &ipv6EgressExecutor{output: "no usable IPv6 default route", err: os.ErrPermission}
	if err := EnsureIPv6HostEgress(executor, IPv6EgressOptions{}); err == nil || !strings.Contains(err.Error(), "IPv6默认路由") {
		t.Fatalf("EnsureIPv6HostEgress() error = %v, want fail-closed route error", err)
	}
	if !strings.Contains(executor.command, "ipv6.ip.sb") {
		t.Fatal("preflight command did not include the external IPv6 probe")
	}
}

func TestEnsureIPv6HostEgressRequiresProbeMarker(t *testing.T) {
	executor := &ipv6EgressExecutor{output: "route exists", err: nil}
	if err := EnsureIPv6HostEgress(executor, IPv6EgressOptions{}); err == nil || !strings.Contains(err.Error(), "未返回有效结果") {
		t.Fatalf("EnsureIPv6HostEgress() error = %v, want missing marker failure", err)
	}
}

func TestEnsureIPv6HostEgressAcceptsVerifiedProbe(t *testing.T) {
	executor := &ipv6EgressExecutor{output: "ipv6-egress=2606:4700::1111\n", err: nil}
	if err := EnsureIPv6HostEgress(executor, IPv6EgressOptions{}); err != nil {
		t.Fatalf("EnsureIPv6HostEgress() error = %v", err)
	}
}

func TestEnsureIPv6HostEgressRejectsPrivateOrConflictingProbe(t *testing.T) {
	for _, output := range []string{
		"ipv6-egress=fd42::1\n",
		"ipv6-egress=2606:4700::1111\nipv6-egress=2606:4700::2222\n",
	} {
		executor := &ipv6EgressExecutor{output: output}
		if err := EnsureIPv6HostEgress(executor, IPv6EgressOptions{}); err == nil {
			t.Fatalf("EnsureIPv6HostEgress() accepted invalid probe output %q", output)
		}
	}
}

func TestIPv6HostEgressCommandRunsWithVerifiedRouteAndProbe(t *testing.T) {
	bin := t.TempDir()
	writeExecutable := func(name, script string) {
		t.Helper()
		path := bin + "/" + name
		if err := os.WriteFile(path, []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeExecutable("ip", "#!/bin/sh\ncase \"$*\" in *'route show default'*) printf '%s\\n' 'default via fe80::1 dev eth0' ;; *) exit 1 ;; esac\n")
	writeExecutable("curl", "#!/bin/sh\nprintf '%s\\n' '2606:4700::1111'\n")
	cmd := exec.Command("sh", "-c", IPv6HostEgressCommand(IPv6EgressOptions{ProbeURL: "https://probe.invalid"}))
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("IPv6HostEgressCommand() failed with a valid route/probe: %v: %s", err, output)
	}
	if !strings.Contains(string(output), "ipv6-egress=2606:4700::1111") {
		t.Fatalf("IPv6HostEgressCommand() output = %q", output)
	}
}

func TestIPv6HostEgressCommandFailsWithoutRouteOrInterface(t *testing.T) {
	bin := t.TempDir()
	for _, name := range []string{"ip", "curl"} {
		path := bin + "/" + name
		script := "#!/bin/sh\n"
		if name == "ip" {
			script += "exit 0\n"
		} else {
			script += "printf '%s\\n' '2606:4700::1111'\n"
		}
		if err := os.WriteFile(path, []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("sh", "-c", IPv6HostEgressCommand(IPv6EgressOptions{}))
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
	if output, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(output), "no global IPv6 interface") {
		t.Fatalf("IPv6HostEgressCommand() result = (%v, %q), want fail-closed missing interface", err, output)
	}
}
