package provider

import (
	"testing"

	"oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
)

func TestResolveUpdatedProviderCapabilitiesPreservesOmittedFields(t *testing.T) {
	existing := providerModel.Provider{
		Type:                  "lxd",
		ContainerEnabled:      true,
		VirtualMachineEnabled: false,
	}
	req := admin.UpdateProviderRequest{
		ProvidedFields: map[string]bool{
			"instanceExpiryAction": true,
		},
	}

	containerEnabled, vmEnabled := resolveUpdatedProviderCapabilities(existing, req)
	if !containerEnabled || vmEnabled {
		t.Fatalf("capabilities = container:%v vm:%v, want container:true vm:false", containerEnabled, vmEnabled)
	}
}

func TestResolveUpdatedProviderCapabilitiesAllowsExplicitChange(t *testing.T) {
	existing := providerModel.Provider{
		Type:                  "lxd",
		ContainerEnabled:      true,
		VirtualMachineEnabled: true,
	}
	req := admin.UpdateProviderRequest{
		ProvidedFields: map[string]bool{
			"container_enabled": true,
			"vm_enabled":        true,
		},
		ContainerEnabled:      true,
		VirtualMachineEnabled: false,
	}

	containerEnabled, vmEnabled := resolveUpdatedProviderCapabilities(existing, req)
	if !containerEnabled || vmEnabled {
		t.Fatalf("capabilities = container:%v vm:%v, want container:true vm:false", containerEnabled, vmEnabled)
	}
}

func TestNormalizeProviderInstanceTypeCapabilitiesKeepsDualProviderUsable(t *testing.T) {
	containerEnabled, vmEnabled := normalizeProviderInstanceTypeCapabilities("lxd", false, false)
	if !containerEnabled || !vmEnabled {
		t.Fatalf("lxd capabilities = container:%v vm:%v, want both true", containerEnabled, vmEnabled)
	}
}
