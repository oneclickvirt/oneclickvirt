package qemu

import (
	"context"
	"errors"
	"strings"
	"testing"

	"oneclickvirt/utils"
)

type localeVirshExecutor struct {
	utils.ShellExecutor
	commands []string
}

func (e *localeVirshExecutor) ExecuteContext(_ context.Context, command string) (string, error) {
	e.commands = append(e.commands, command)
	if strings.Contains(command, "lxc:///") {
		return "Domain not found", errors.New("exit 1")
	}
	if strings.Contains(command, "domstate") {
		if !strings.HasPrefix(command, "LC_ALL=C NO_COLOR=1 ") {
			return "läuft", nil
		}
		return "\x1b[32mrunning\x1b[0m\r\n", nil
	}
	return "", errors.New("unexpected mutation")
}
func (e *localeVirshExecutor) IsHealthy() bool { return true }

func TestQEMUStartNormalizesLocaleWithoutRestarting(t *testing.T) {
	p := NewQEMUProvider().(*QEMUProvider)
	p.connected = true
	e := &localeVirshExecutor{}
	p.sshClient.SetExecutor(e)
	if err := p.StartInstance(context.Background(), "guest"); err != nil {
		t.Fatal(err)
	}
	if len(e.commands) != 2 {
		t.Fatalf("running VM received a mutation: %v", e.commands)
	}
}

func TestVirshStatusDoesNotAcceptAmbiguousOutput(t *testing.T) {
	if mapVirshStatus("\x1b[32mshut off\x1b[0m\n") != "stopped" {
		t.Fatal("color not removed")
	}
	for _, value := range []string{"not running", "running\nshut off", "läuft", "en cours"} {
		if mapVirshStatus(value) != "unknown" {
			t.Fatalf("ambiguous state %q accepted", value)
		}
	}
}
