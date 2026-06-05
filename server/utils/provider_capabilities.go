package utils

import "strings"

// NormalizeProviderType normalizes provider type strings used across DB records,
// requests, and CSV imports.
func NormalizeProviderType(providerType string) string {
	return strings.ToLower(strings.TrimSpace(providerType))
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
	return IsLXDIncusProvider(providerType) && NormalizeProviderType(instanceType) != "vm"
}

func SupportsContainerCopyProvider(providerType string) bool {
	return IsLXDIncusProvider(providerType) || IsDockerFamilyProvider(providerType)
}

func SupportsContainerGPUProvider(providerType, instanceType string) bool {
	return NormalizeProviderType(instanceType) != "vm" &&
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

func IsVMOnlyProvider(providerType string) bool {
	switch NormalizeProviderType(providerType) {
	case "qemu", "kubevirt", "vmware", "virtualbox", "multipass", "vagrant":
		return true
	default:
		return false
	}
}
