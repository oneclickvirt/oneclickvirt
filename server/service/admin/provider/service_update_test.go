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

func TestResolveUpdatedInstanceExpiryPolicyClearsExtendWithExplicitDelete(t *testing.T) {
	existing := providerModel.Provider{
		InstanceExpiryAction:     providerModel.InstanceExpiryActionExtend,
		InstanceExpiryExtendDays: 3,
	}
	req := admin.UpdateProviderRequest{
		ProvidedFields: map[string]bool{
			"instanceExpiryAction":     true,
			"instanceExpiryExtendDays": true,
		},
		InstanceExpiryAction:     providerModel.InstanceExpiryActionDelete,
		InstanceExpiryExtendDays: 0,
	}

	action, extendDays := resolveUpdatedInstanceExpiryPolicy(existing, req)
	if action != providerModel.InstanceExpiryActionDelete || extendDays != 0 {
		t.Fatalf("expiry policy = %s/%d, want delete/0", action, extendDays)
	}
}

func TestResolveUpdatedInstanceExpiryPolicyPreservesOmittedPolicy(t *testing.T) {
	existing := providerModel.Provider{
		InstanceExpiryAction:     providerModel.InstanceExpiryActionExtend,
		InstanceExpiryExtendDays: 3,
	}
	req := admin.UpdateProviderRequest{
		ProvidedFields: map[string]bool{
			"trafficOverLimitAction": true,
		},
		TrafficOverLimitAction: providerModel.TrafficOverLimitActionStop,
	}

	action, extendDays := resolveUpdatedInstanceExpiryPolicy(existing, req)
	if action != providerModel.InstanceExpiryActionExtend || extendDays != 3 {
		t.Fatalf("expiry policy = %s/%d, want extend/3", action, extendDays)
	}
}

func TestResolveUpdatedTrafficPolicyAllowsExplicitZeroSpeed(t *testing.T) {
	existing := providerModel.Provider{
		TrafficOverLimitAction: providerModel.TrafficOverLimitActionSpeedLimit,
		TrafficSpeedLimitKbps:  2048,
	}
	req := admin.UpdateProviderRequest{
		ProvidedFields: map[string]bool{
			"trafficOverLimitAction": true,
			"trafficSpeedLimitKbps":  true,
		},
		TrafficOverLimitAction: providerModel.TrafficOverLimitActionStop,
		TrafficSpeedLimitKbps:  0,
	}

	action, speed := resolveUpdatedTrafficOverLimitPolicy(existing, req)
	if action != providerModel.TrafficOverLimitActionStop || speed != 0 {
		t.Fatalf("traffic policy = %s/%d, want stop/0", action, speed)
	}
}
