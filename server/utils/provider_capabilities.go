package utils

import "strings"

// NormalizeProviderType normalizes provider type strings used across DB records,
// requests, and CSV imports.
func NormalizeProviderType(providerType string) string {
	return strings.ToLower(strings.TrimSpace(providerType))
}

func NormalizeInstanceType(instanceType string) string {
	return strings.ToLower(strings.TrimSpace(instanceType))
}

func IsLXDIncusProvider(providerType string) bool {
	switch NormalizeProviderType(providerType) {
	case "lxd", "incus":
		return true
	default:
		return false
	}
}

func SupportsLXDContainerOptions(providerType, instanceType string) bool {
	providerType = NormalizeProviderType(providerType)
	return IsLXDIncusProvider(providerType) && NormalizeInstanceType(instanceType) != "vm"
}

func SupportsContainerCopyProvider(providerType string) bool {
	providerType = NormalizeProviderType(providerType)
	return IsLXDIncusProvider(providerType) || IsDockerFamilyProvider(providerType)
}

func SupportsContainerGPUProvider(providerType, instanceType string) bool {
	providerType = NormalizeProviderType(providerType)
	return NormalizeInstanceType(instanceType) != "vm" &&
		(IsLXDIncusProvider(providerType) || IsDockerFamilyProvider(providerType))
}

func IsDockerFamilyProvider(providerType string) bool {
	switch NormalizeProviderType(providerType) {
	case "docker", "podman", "containerd", "orbstack":
		return true
	default:
		return false
	}
}

func IsKubeVirtProvider(providerType string) bool {
	return NormalizeProviderType(providerType) == "kubevirt"
}

// IsQEMUProvider returns true for the local/remote libvirt provider that can create
// both libvirt-lxc containers and QEMU/KVM virtual machines.
func IsQEMUProvider(providerType string) bool {
	return NormalizeProviderType(providerType) == "qemu"
}

// IsVMOnlyProvider returns true for providers that can only create virtual machines.
// QEMU and KubeVirt are intentionally excluded: they can create both containers and VMs.
func IsVMOnlyProvider(providerType string) bool {
	switch NormalizeProviderType(providerType) {
	case "vmware", "virtualbox", "multipass", "vagrant":
		return true
	default:
		return false
	}
}

// UsesContainerRuntimePorts returns true when provider creation must receive docker-style
// host:guest/protocol port mappings up front.
func UsesContainerRuntimePorts(providerType, instanceType string) bool {
	providerType = NormalizeProviderType(providerType)
	instanceType = NormalizeInstanceType(instanceType)
	return IsDockerFamilyProvider(providerType) || (providerType == "kubevirt" && instanceType == "container")
}

// UsesVMPositionalPorts returns true when provider creation consumes positional
// ssh/start/end ports for VM-side NodePort/forwarding setup.
func UsesVMPositionalPorts(providerType, instanceType string) bool {
	providerType = NormalizeProviderType(providerType)
	instanceType = NormalizeInstanceType(instanceType)
	return IsVMOnlyProvider(providerType) || providerType == "qemu" || (providerType == "kubevirt" && instanceType == "vm")
}
