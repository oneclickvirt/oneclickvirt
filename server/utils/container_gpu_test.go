package utils

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

type gpuFixtureExecutor struct {
	ShellExecutor
	cli       string
	local     map[string]map[string]string
	profile   map[string]map[string]string
	commands  []string
	queryBody string
	failOn    string
	noApply   bool
	cancel    context.CancelFunc
	cancelOn  string
}

var errGPUFixture = errors.New("GPU fixture failure")

func (e *gpuFixtureExecutor) Execute(command string) (string, error) {
	return e.ExecuteContext(context.Background(), command)
}

func (e *gpuFixtureExecutor) ExecuteContext(ctx context.Context, command string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	e.commands = append(e.commands, command)
	if e.failOn != "" && strings.Contains(command, e.failOn) {
		return "device operation failed", errGPUFixture
	}
	if command == e.cli+" query '/1.0/instances/guest'" {
		if e.queryBody != "" {
			return e.queryBody, nil
		}
		expanded := make(map[string]map[string]string)
		for name, device := range e.profile {
			expanded[name] = device
		}
		for name, device := range e.local {
			expanded[name] = device
		}
		body, err := json.Marshal(map[string]interface{}{"metadata": containerGPUDevices{Devices: e.local, ExpandedDevices: expanded}})
		return string(body), err
	}
	parts := strings.Fields(command)
	if len(parts) < 6 || parts[0] != e.cli || parts[1] != "config" || parts[2] != "device" || parts[4] != "'guest'" {
		return "", fmt.Errorf("unexpected command (must only mutate guest devices): %s", command)
	}
	name := strings.Trim(parts[5], "'")
	if !e.noApply {
		switch parts[3] {
		case "remove":
			if _, exists := e.local[name]; !exists {
				return "no local device", errGPUFixture
			}
			delete(e.local, name)
		case "add":
			if len(parts) < 7 {
				return "", errGPUFixture
			}
			if _, exists := e.local[name]; exists {
				return "device already exists", errGPUFixture
			}
			device := map[string]string{"type": parts[6]}
			if len(parts) == 8 {
				device["id"] = strings.TrimPrefix(strings.Trim(parts[7], "'"), "id=")
			}
			e.local[name] = device
		default:
			return "", fmt.Errorf("unexpected device mutation: %s", command)
		}
	}
	if e.cancel != nil && strings.Contains(command, e.cancelOn) {
		e.cancel()
	}
	return "", nil
}

func newGPUFixture(cli string) *gpuFixtureExecutor {
	return &gpuFixtureExecutor{
		cli: cli,
		local: map[string]map[string]string{
			"root": {"type": "disk", "pool": "local", "path": "/"},
			"eth0": {"type": "nic", "network": "bridge", "name": "eth0"},
			"ssh":  {"type": "proxy", "listen": "tcp:0.0.0.0:22000", "connect": "tcp:127.0.0.1:22"},
		},
		profile: map[string]map[string]string{},
	}
}

func TestContainerGPUKeepsCopiedSelectionAndUnrelatedDevices(t *testing.T) {
	for _, cli := range []string{"lxc", "incus"} {
		for _, all := range []bool{false, true} {
			e := newGPUFixture(cli)
			ids := "1,0,1"
			if all {
				e.local["render"] = map[string]string{"type": "gpu", "gid": "44"}
				ids = ""
			} else {
				e.local["gpu0"] = map[string]string{"type": "gpu", "id": "0", "gid": "44"}
				e.profile["gpu1"] = map[string]string{"type": "gpu", "id": "1"}
			}
			before, _ := json.Marshal(e.local)
			if err := ConfigureContainerGPU(context.Background(), e, cli, "guest", true, ids); err != nil {
				t.Fatal(err)
			}
			after, _ := json.Marshal(e.local)
			if len(e.commands) != 1 || string(before) != string(after) {
				t.Fatalf("compatible copied GPU devices were changed: %v", e.commands)
			}
		}
	}
}

func TestContainerGPUSelectionReplacesBroadAndInheritedDevicesOnGuestOnly(t *testing.T) {
	for _, cli := range []string{"lxc", "incus"} {
		e := newGPUFixture(cli)
		e.local["all"] = map[string]string{"type": "gpu"}
		e.profile["gpu0"] = map[string]string{"type": "gpu", "id": "0"}
		profileBefore, _ := json.Marshal(e.profile)
		root, nic, proxy := e.local["root"], e.local["eth0"], e.local["ssh"]
		if err := ConfigureContainerGPU(context.Background(), e, cli, "guest", true, "1,3"); err != nil {
			t.Fatal(err)
		}
		if e.local["all"]["type"] != "none" || e.local["gpu0"]["id"] != "1" || e.local["gpu1"]["id"] != "3" {
			t.Fatalf("copied GPU selection leaked or lost a requested device: %+v", e.local)
		}
		profileAfter, _ := json.Marshal(e.profile)
		if string(profileBefore) != string(profileAfter) || !reflect.DeepEqual(root, e.local["root"]) || !reflect.DeepEqual(nic, e.local["eth0"]) || !reflect.DeepEqual(proxy, e.local["ssh"]) {
			t.Fatal("GPU selection changed a shared profile or an unrelated device")
		}
		previousCount := len(e.commands)
		if err := ConfigureContainerGPU(context.Background(), e, cli, "guest", true, "3,1"); err != nil || len(e.commands) != previousCount+1 {
			t.Fatalf("repeat GPU configuration was not idempotent: %v, %v", e.commands, err)
		}
	}
}

func TestContainerGPUConventionalNameCollisionPreservesNonGPUDevice(t *testing.T) {
	e := newGPUFixture("lxc")
	e.local["gpu0"] = map[string]string{"type": "disk", "path": "/data", "source": "/data"}
	if err := ConfigureContainerGPU(context.Background(), e, "lxc", "guest", true, "0"); err != nil {
		t.Fatal(err)
	}
	if e.local["gpu0"]["type"] != "disk" || e.local["oneclickvirt-gpu-0-0"]["id"] != "0" {
		t.Fatalf("name collision replaced the disk: %#v", e.local)
	}
}

func TestContainerGPUDisableMasksProfileShadowedByLocalDevice(t *testing.T) {
	e := newGPUFixture("incus")
	e.local["gpu"] = map[string]string{"type": "gpu", "id": "1"}
	e.profile["gpu"] = map[string]string{"type": "gpu"}
	if err := ConfigureContainerGPU(context.Background(), e, "incus", "guest", false, ""); err != nil {
		t.Fatal(err)
	}
	if e.local["gpu"]["type"] != "none" || e.profile["gpu"]["type"] != "gpu" {
		t.Fatal("local removal exposed the inherited GPU or changed the shared profile")
	}
}

func TestContainerGPUInvalidResponsesAndInputsCannotMutateDevices(t *testing.T) {
	for _, body := range []string{"{", "null", `{}`, `{"metadata":null}`, `{"devices":null}`, `{"devices":{}}`, `{"expanded_devices":{}}`, `{"devices":{"gpu":null},"expanded_devices":{}}`, `{"devices":{"gpu":{"id":"0"}},"expanded_devices":{}}`, `{"devices":[],"expanded_devices":{}}`} {
		e := newGPUFixture("lxc")
		e.queryBody = body
		if err := ConfigureContainerGPU(context.Background(), e, "lxc", "guest", true, "0"); err == nil || len(e.commands) != 1 {
			t.Fatalf("malformed response enabled a mutation: %q, commands=%v, error=%v", body, e.commands, err)
		}
	}
	for _, id := range []string{"0,,1", "0;touch /tmp/unwanted", "$(true)", "0\n1"} {
		e := newGPUFixture("lxc")
		if err := ConfigureContainerGPU(context.Background(), e, "lxc", "guest", true, id); err == nil || len(e.commands) != 0 {
			t.Fatalf("invalid GPU selector reached node: %q, %v", id, err)
		}
	}
}

func TestContainerGPUFailedMutationCancellationAndNoEffectCannotSucceed(t *testing.T) {
	for _, mode := range []string{"failed read", "failed attach", "no effect", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			e := newGPUFixture("lxc")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "failed read":
				e.failOn = " query "
			case "failed attach":
				e.failOn = "'gpu1'"
			case "no effect":
				e.noApply = true
			case "cancelled":
				e.cancel, e.cancelOn = cancel, "'gpu0'"
			}
			err := ConfigureContainerGPU(ctx, e, "lxc", "guest", true, "0,1")
			if err == nil {
				t.Fatalf("GPU configuration succeeded after %s", mode)
			}
			if mode == "failed read" || mode == "failed attach" {
				if !errors.Is(err, errGPUFixture) {
					t.Fatalf("lost original device error: %v", err)
				}
			}
			if mode == "cancelled" && (!errors.Is(err, context.Canceled) || len(e.commands) != 2) {
				t.Fatalf("cancellation allowed another mutation: %v, %v", e.commands, err)
			}
		})
	}
}
