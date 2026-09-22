package provider

import (
	"reflect"
	"testing"

	providerModel "oneclickvirt/model/provider"
	rootProvider "oneclickvirt/provider"
)

func TestUsesControllerIPv6PoolDistinguishesIncusLXDManagedNAT(t *testing.T) {
	tests := []struct {
		providerType string
		networkType  string
		method       string
		want         bool
	}{
		{providerType: "incus", networkType: "nat_ipv4_ipv6", method: "device_proxy", want: false},
		{providerType: "LXD", networkType: " NAT_IPV4_IPV6 ", method: " IPTABLES ", want: false},
		{providerType: "incus", networkType: "nat_ipv4_ipv6", method: "native", want: true},
		{providerType: "lxd", networkType: "nat_ipv4_ipv6", method: " NATIVE ", want: true},
		{providerType: "incus", networkType: "dedicated_ipv4_ipv6", want: true},
		{providerType: "lxd", networkType: "ipv6_only", want: true},
		{providerType: "qemu", networkType: "nat_ipv4_ipv6", want: true},
		{providerType: "proxmox", networkType: "nat_ipv4_ipv6", want: true},
		{providerType: "incus", networkType: "nat_ipv4", want: false},
	}
	for _, test := range tests {
		if got := usesControllerIPv6Pool(test.providerType, test.networkType, test.method); got != test.want {
			t.Fatalf("usesControllerIPv6Pool(%q, %q, %q) = %t, want %t", test.providerType, test.networkType, test.method, got, test.want)
		}
	}
}

func TestRequiresConfiguredIPv6PoolOnlyForIncusLXDNativeDualStack(t *testing.T) {
	for _, test := range []struct {
		providerType string
		networkType  string
		method       string
		want         bool
	}{
		{providerType: "incus", networkType: "nat_ipv4_ipv6", method: "native", want: true},
		{providerType: "LXD", networkType: " NAT_IPV4_IPV6 ", method: " NATIVE ", want: true},
		{providerType: "incus", networkType: "nat_ipv4_ipv6", method: "device_proxy", want: false},
		{providerType: "lxd", networkType: "ipv6_only", method: "native", want: false},
		{providerType: "qemu", networkType: "nat_ipv4_ipv6", method: "native", want: false},
	} {
		if got := requiresConfiguredIPv6Pool(test.providerType, test.networkType, test.method); got != test.want {
			t.Fatalf("requiresConfiguredIPv6Pool(%q, %q, %q) = %t, want %t", test.providerType, test.networkType, test.method, got, test.want)
		}
	}
}

func TestValidateProviderIPv6NetworkAcceptsRoutedGuestBackends(t *testing.T) {
	for _, test := range []struct {
		providerType string
		networkType  string
	}{
		{providerType: "proxmox", networkType: "nat_ipv4_ipv6"},
		{providerType: "qemu", networkType: "ipv6_only"},
		{providerType: "vmware", networkType: "nat_ipv4"},
		{providerType: "vmware", networkType: "nat_ipv4_ipv6"},
		{providerType: "virtualbox", networkType: "nat_ipv4_ipv6"},
		{providerType: "multipass", networkType: "nat_ipv4_ipv6"},
		{providerType: "vagrant", networkType: "nat_ipv4_ipv6"},
	} {
		if err := validateProviderIPv6Network(test.providerType, test.networkType); err != nil {
			t.Fatalf("validateProviderIPv6Network(%q, %q) error = %v", test.providerType, test.networkType, err)
		}
	}
}

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

func TestBuildContainerRuntimePortsNeverPublishesControllerMappings(t *testing.T) {
	ports := []providerModel.Port{
		{HostPort: 20000, GuestPort: 22, Protocol: "tcp", MappingType: "controller", Status: "active"},
		{HostPort: 20001, GuestPort: 80, Protocol: "tcp", MappingType: "node", Status: "active"},
	}

	want := []string{"0.0.0.0:20001:80/tcp"}
	if got := buildContainerRuntimePorts(ports); !reflect.DeepEqual(got, want) {
		t.Fatalf("buildContainerRuntimePorts() = %#v, want %#v", got, want)
	}
}
