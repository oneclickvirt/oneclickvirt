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

func TestResolveUpdatedProviderPortIPDistinguishesOmittedAndCleared(t *testing.T) {
	existing := providerModel.Provider{PortIP: "192.0.2.10"}

	omitted := admin.UpdateProviderRequest{ProvidedFields: map[string]bool{"networkType": true}}
	if got := resolveUpdatedProviderPortIP(existing, omitted); got != existing.PortIP {
		t.Fatalf("omitted portIP = %q, want preserved %q", got, existing.PortIP)
	}

	cleared := admin.UpdateProviderRequest{ProvidedFields: map[string]bool{"portIP": true}, PortIP: "  "}
	if got := resolveUpdatedProviderPortIP(existing, cleared); got != "" {
		t.Fatalf("explicitly cleared portIP = %q, want empty", got)
	}

	changed := admin.UpdateProviderRequest{ProvidedFields: map[string]bool{"portIP": true}, PortIP: " 198.51.100.20 "}
	if got := resolveUpdatedProviderPortIP(existing, changed); got != "198.51.100.20" {
		t.Fatalf("updated portIP = %q, want trimmed address", got)
	}
}
