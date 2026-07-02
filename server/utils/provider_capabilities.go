package utils

import "strings"

// NormalizeProviderType normalizes provider type strings used across DB records,
// requests, and CSV imports.
func NormalizeProviderType(providerType string) string {
	return strings.ToLower(strings.TrimSpace(providerType))
}

// NormalizeSystemImageProviderType returns the provider_type stored by system
// image seed data. Some runtime provider names are aliases for the same image
// family, for example proxmoxve/pve use proxmox images.
func NormalizeSystemImageProviderType(providerType string) string {
	switch NormalizeProviderType(providerType) {
	case "pve", "proxmoxve":
		return "proxmox"
	default:
		return NormalizeProviderType(providerType)
	}
}

// SystemImageProviderTypeCandidates returns the preferred system image provider
// type plus the raw provider type as a compatibility fallback.
func SystemImageProviderTypeCandidates(providerType string) []string {
	raw := NormalizeProviderType(providerType)
	normalized := NormalizeSystemImageProviderType(raw)
	if raw == "" {
		return nil
	}
	if raw == normalized {
		return []string{normalized}
	}
	return []string{normalized, raw}
}

// SystemImageProviderTypeMatches reports whether a runtime provider type can use
// a system-image provider_type value. imageProviderType may be a comma-separated
// compatibility list.
func SystemImageProviderTypeMatches(providerType, imageProviderType string) bool {
	raw := NormalizeProviderType(providerType)
	if raw == "" {
		return false
	}
	normalized := NormalizeSystemImageProviderType(raw)
	candidates := map[string]struct{}{}
	for _, candidate := range SystemImageProviderTypeCandidates(providerType) {
		candidates[candidate] = struct{}{}
	}

	for _, item := range strings.Split(imageProviderType, ",") {
		supportedRaw := NormalizeProviderType(item)
		if supportedRaw == "" {
			continue
		}
		supportedNormalized := NormalizeSystemImageProviderType(supportedRaw)
		if supportedRaw == raw || supportedNormalized == normalized {
			return true
		}
		if _, ok := candidates[supportedRaw]; ok {
			return true
		}
		if _, ok := candidates[supportedNormalized]; ok {
			return true
		}
	}
	return false
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
