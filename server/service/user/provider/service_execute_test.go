package provider

import (
	"reflect"
	"testing"

	providerModel "oneclickvirt/model/provider"
	rootProvider "oneclickvirt/provider"
)

func TestBuildCopyInstanceResourceUpdatesSkipsUnsetLimits(t *testing.T) {
	updates := buildCopyInstanceResourceUpdates(2, 0, 1024)

	if updates["cpu"] != 2 {
		t.Fatalf("expected CPU limit to be copied, got %#v", updates["cpu"])
	}
	if _, ok := updates["memory"]; ok {
		t.Fatalf("expected unset memory limit to be skipped, got %#v", updates["memory"])
	}
	if updates["disk"] != int64(1024) {
		t.Fatalf("expected disk limit to be copied, got %#v", updates["disk"])
	}
}

func TestBuildCopyResourceUsageUpdatesUsesPositiveDeltas(t *testing.T) {
	instance := &providerModel.Instance{
		CPU:    2,
		Memory: 512,
		Disk:   2048,
	}

	updates := buildCopyResourceUsageUpdates(instance, 2, 1024, 0)

	if updates.cpuDelta != 0 {
		t.Fatalf("expected unchanged CPU to avoid duplicate usage, got %d", updates.cpuDelta)
	}
	if updates.memoryDelta != 512 {
		t.Fatalf("expected memory usage delta to be recorded, got %d", updates.memoryDelta)
	}
	if updates.diskDelta != 0 {
		t.Fatalf("expected unset disk limit to avoid subtracting usage, got %d", updates.diskDelta)
	}
}

func TestBuildVMPositionalPortsUsesAllocatedSSHAndRange(t *testing.T) {
	ports := []providerModel.Port{
		{HostPort: 20002, GuestPort: 20002, Protocol: "tcp"},
		{HostPort: 20000, GuestPort: 22, IsSSH: true, Protocol: "tcp"},
		{HostPort: 20001, GuestPort: 20001, Protocol: "tcp"},
	}

	got := buildVMPositionalPorts(ports)
	want := []string{"20000", "20001", "20002"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildVMPositionalPorts() = %#v, want %#v", got, want)
	}
}

func TestApplyPreallocatedPortMappingsToConfigKubeVirtVM(t *testing.T) {
	cfg := rootProvider.InstanceConfig{}
	ports := []providerModel.Port{
		{HostPort: 20000, GuestPort: 22, IsSSH: true, Protocol: "tcp"},
		{HostPort: 20001, GuestPort: 20001, Protocol: "tcp"},
	}

	mode := applyPreallocatedPortMappingsToConfig(&cfg, "kubevirt", "vm", ports)
	if mode != "vm_positional" {
		t.Fatalf("mode = %q, want vm_positional", mode)
	}
	want := []string{"20000", "20001", "20001"}
	if !reflect.DeepEqual(cfg.Ports, want) {
		t.Fatalf("cfg.Ports = %#v, want %#v", cfg.Ports, want)
	}
}

func TestApplyPreallocatedPortMappingsToConfigKubeVirtContainer(t *testing.T) {
	cfg := rootProvider.InstanceConfig{}
	ports := []providerModel.Port{
		{HostPort: 20000, GuestPort: 22, IsSSH: true, Protocol: "tcp"},
		{HostPort: 20001, GuestPort: 80, Protocol: "both"},
	}

	mode := applyPreallocatedPortMappingsToConfig(&cfg, "kubevirt", "container", ports)
	if mode != "container_runtime" {
		t.Fatalf("mode = %q, want container_runtime", mode)
	}
	want := []string{
		"0.0.0.0:20000:22/tcp",
		"0.0.0.0:20001:80/tcp",
		"0.0.0.0:20001:80/udp",
	}
	if !reflect.DeepEqual(cfg.Ports, want) {
		t.Fatalf("cfg.Ports = %#v, want %#v", cfg.Ports, want)
	}
}
