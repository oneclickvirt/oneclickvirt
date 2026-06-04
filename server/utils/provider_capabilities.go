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
	case "qemu", "kubevirt", "vmware":
		return true
	default:
		return false
	}
}
