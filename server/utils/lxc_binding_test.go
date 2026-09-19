package utils

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type bindingCLIExecutor struct {
	ShellExecutor
	t                 *testing.T
	metadata          map[string]interface{}
	state             map[string]interface{}
	commands          []string
	legacy            bool
	failRead          bool
	failWrite         bool
	noPersist         bool
	writes            int
	dhcpStaticFailure bool
	networkSet        bool
}

func (e *bindingCLIExecutor) ExecuteWithTimeout(command string, timeout time.Duration) (string, error) {
	e.commands = append(e.commands, command)
	if timeout <= 0 {
		e.t.Fatal("binding command must have a timeout")
	}
	if strings.Contains(command, " query ") {
		if e.failRead {
			return "invalid response", nil
		}
		instance := lxcDeviceMap(e.metadata)
		delete(instance, "_network_state") // InstanceGet has no runtime state.
		var value interface{} = instance
		if strings.Contains(command, "/state'") {
			value = map[string]interface{}{"metadata": e.state}
		}
		body, err := json.Marshal(value)
		return string(body), err
	}
	if strings.Contains(command, " network set ") {
		e.networkSet = true
		return "", nil
	}
	if !strings.Contains(command, " config device ") || !strings.Contains(command, " 'guest' 'uplink' ") {
		e.t.Fatalf("binding guessed a device instead of matching the guest MAC: %s", command)
	}
	e.writes++
	if e.dhcpStaticFailure && !e.networkSet && strings.Contains(command, "ipv6.address") {
		return "Cannot specify ipv6.address when DHCP is disabled", errors.New("device validation failed")
	}
	if e.failWrite || (e.legacy && (strings.Contains(command, "ipv4.address=") || strings.Contains(command, "ipv6.address="))) {
		return "device write failed", errors.New("device write failed")
	}
	devices := e.metadata["devices"].(map[string]interface{})
	_, local := devices["uplink"]
	if strings.Contains(command, " override ") == local {
		e.t.Fatalf("incorrect local/profile device operation: %s", command)
	}
	if !e.noPersist {
		nic := lxcDeviceMap(e.metadata["expanded_devices"].(map[string]interface{})["uplink"])
		if local {
			nic = lxcDeviceMap(devices["uplink"])
		}
		if strings.Contains(command, "ipv6.address") {
			nic["ipv6.address"] = "2001:db8::10"
		} else {
			nic["ipv4.address"] = "192.0.2.10"
		}
		devices["uplink"] = nic
	}
	return "", nil
}

func TestLXCBindingEnablesStatefulDHCPForStaticIPv6(t *testing.T) {
	metadata, devices := proxyNICFixture()
	delete(devices, "ssh")
	metadata["devices"] = devices
	executor := &bindingCLIExecutor{t: t, metadata: metadata, state: metadata["_network_state"].(map[string]interface{}), dhcpStaticFailure: true}
	if err := SetLXCAddressBinding(executor, "incus", "guest", "2001:db8::10", true); err != nil {
		t.Fatalf("SetLXCAddressBinding() error = %v", err)
	}
	if !executor.networkSet || executor.writes != 2 {
		t.Fatalf("stateful DHCP fallback commands/writes = %v/%d, want network set and two device writes: %v", executor.networkSet, executor.writes, executor.commands)
	}
}

func TestLXCBindingIPv6PreservesOtherFamilyAndNICs(t *testing.T) {
	for _, runtime := range []string{"incus", "lxc"} {
		for _, scenario := range []string{"profile", "local legacy", "routed", "already bound", "conflicting", "no state", "write failure", "write not persisted"} {
			t.Run(runtime+"/"+scenario, func(t *testing.T) {
				metadata, devices := proxyNICFixture()
				delete(devices, "ssh")
				metadata["devices"] = devices
				nic := metadata["expanded_devices"].(map[string]interface{})["uplink"].(map[string]interface{})
				nic["ipv4.address"] = "192.0.2.10"
				otherNIC := map[string]string{"type": "nic", "name": "eth1", "network": "private", "ipv6.address": "fd42::20"}
				devices["private"] = otherNIC
				executor := &bindingCLIExecutor{t: t, metadata: metadata, state: metadata["_network_state"].(map[string]interface{})}
				wantError := false
				switch scenario {
				case "local legacy":
					devices["uplink"] = lxcDeviceMap(nic)
					executor.legacy = true
				case "routed", "already bound":
					delete(nic, "network")
					nic["nictype"] = "routed"
					nic["parent"] = "wan0"
					if scenario == "already bound" {
						nic["ipv6.address"] = "2001:db8::11,2001:0DB8:0:0:0:0:0:10"
						executor.state = nil // A stopped guest needs no runtime state.
					}
				case "conflicting":
					nic["ipv6.address"] = "2001:db8::99"
					wantError = true
				case "no state":
					executor.state = nil
					wantError = true
				case "write failure":
					executor.failWrite, wantError = true, true
				case "write not persisted":
					executor.noPersist, wantError = true, true
				}
				err := SetLXCAddressBinding(executor, runtime, "guest", "2001:db8::10/64", true)
				if (err != nil) != wantError {
					t.Fatalf("binding error = %v, want error=%v", err, wantError)
				}
				if !wantError {
					writes := executor.writes
					if err := SetLXCAddressBinding(executor, runtime, "guest", "2001:db8::10", true); err != nil || executor.writes != writes {
						t.Fatalf("repeat binding changed the NIC: %v, %v", err, executor.commands)
					}
				}
				if scenario == "already bound" && (executor.writes != 0 || len(executor.commands) != 2) {
					t.Fatalf("existing routed addresses were rewritten or probed: %v", executor.commands)
				}
				if !reflect.DeepEqual(devices["private"], otherNIC) || nic["ipv4.address"] != "192.0.2.10" || devices["root"] == nil {
					t.Fatalf("unrelated network or storage was modified: %v", devices)
				}
				if localNIC, ok := devices["uplink"]; ok {
					local := lxcDeviceMap(localNIC)
					if local["ipv4.address"] != "192.0.2.10" || local["mtu"] != "1400" || local["limits.ingress"] != "10Mbit" {
						t.Fatalf("binding discarded NIC properties: %v", local)
					}
				}
			})
		}
	}
}

func TestLXCBindingMatchesActualNICAndPreservesProfileSettings(t *testing.T) {
	for _, runtime := range []string{"incus", "lxc"} {
		for _, local := range []bool{false, true} {
			t.Run(runtime+map[bool]string{false: "/profile", true: "/local-legacy"}[local], func(t *testing.T) {
				metadata, devices := proxyNICFixture()
				delete(devices, "ssh")
				metadata["devices"] = devices
				expanded := metadata["expanded_devices"].(map[string]interface{})
				originalNIC := lxcDeviceMap(expanded["uplink"])
				if local {
					devices["uplink"] = lxcDeviceMap(originalNIC)
				}
				executor := &bindingCLIExecutor{t: t, metadata: metadata, state: metadata["_network_state"].(map[string]interface{}), legacy: local}
				if err := SetLXCIPv4Binding(executor, runtime, "guest", "192.0.2.10/24 (enp5s0)"); err != nil {
					t.Fatal(err)
				}
				bound := lxcDeviceMap(devices["uplink"])
				delete(bound, "ipv4.address")
				if !reflect.DeepEqual(bound, originalNIC) || !reflect.DeepEqual(expanded["uplink"], originalNIC) {
					t.Fatalf("binding lost NIC settings or modified the profile: %#v", metadata)
				}
				if devices["root"] == nil {
					t.Fatal("binding removed the root disk")
				}
				writes := executor.writes
				if err := SetLXCIPv4Binding(executor, runtime, "guest", "192.0.2.10"); err != nil || executor.writes != writes {
					t.Fatalf("repeat binding was not idempotent: %v, writes %d -> %d", err, writes, executor.writes)
				}
			})
		}
	}
}

func TestLXCBindingRejectsMissingConflictingAndFailedConfiguration(t *testing.T) {
	for _, scenario := range []string{"invalid response", "write failure", "write not persisted", "different address", "ambiguous NIC", "missing state", "MAC mismatch", "invalid address"} {
		t.Run(scenario, func(t *testing.T) {
			metadata, devices := proxyNICFixture()
			delete(devices, "ssh")
			metadata["devices"] = devices
			executor := &bindingCLIExecutor{t: t, metadata: metadata, state: metadata["_network_state"].(map[string]interface{})}
			nic := metadata["expanded_devices"].(map[string]interface{})["uplink"].(map[string]interface{})
			address := "192.0.2.10"
			switch scenario {
			case "invalid response":
				executor.failRead = true
			case "write failure":
				executor.failWrite = true
			case "write not persisted":
				executor.noPersist = true
			case "different address":
				nic["ipv4.address"] = "192.0.2.99"
			case "ambiguous NIC":
				other := lxcDeviceMap(nic)
				other["hwaddr"] = "00:16:3e:11:22:33"
				metadata["expanded_devices"].(map[string]interface{})["other"] = other
			case "missing state":
				executor.state = map[string]interface{}{"network": map[string]interface{}{}}
			case "MAC mismatch":
				nic["name"] = "enp5s0"
				nic["hwaddr"] = "00:16:3e:44:55:66"
			case "invalid address":
				address = "0.0.0.0"
			}
			if err := SetLXCIPv4Binding(executor, "incus", "guest", address); err == nil {
				t.Fatal("invalid or failed binding was accepted")
			}
			if scenario != "write failure" && scenario != "write not persisted" && executor.writes != 0 {
				t.Fatalf("binding changed a NIC without a unique valid target: %v", executor.commands)
			}
		})
	}
}
