package provider

import (
	"testing"

	providerModel "oneclickvirt/model/provider"
)

func TestDedicatedIPv4FinalizationPreservesAllocatedEndpoint(t *testing.T) {
	db := finalizedSSHPortDB(t)
	if err := db.AutoMigrate(&providerModel.ProviderIPv4Pool{}); err != nil {
		t.Fatal(err)
	}
	id := uint(19)
	allocation := providerModel.ProviderIPv4Pool{ProviderID: 7, InstanceID: &id, Address: "198.51.100.198", IsAllocated: true}
	if err := db.Create(&allocation).Error; err != nil {
		t.Fatal(err)
	}
	instance := providerModel.Instance{ID: id, ProviderID: 7, NetworkType: "dedicated_ipv4_ipv6"}
	updates := map[string]interface{}{"public_ip": "198.51.100.196"}
	if err := applyAllocatedPublicIPv4(db, instance, providerModel.Provider{}, updates); err != nil {
		t.Fatal(err)
	}
	if updates["public_ip"] != allocation.Address {
		t.Fatalf("allocated endpoint was replaced by the node: %#v", updates)
	}
	instance.ProviderID = 8
	if err := applyAllocatedPublicIPv4(db, instance, providerModel.Provider{}, updates); err == nil {
		t.Fatal("accepted another provider's allocation")
	}
	instance.NetworkType = "nat_ipv4"
	updates["public_ip"] = "198.51.100.196"
	if err := applyAllocatedPublicIPv4(db, instance, providerModel.Provider{}, updates); err != nil {
		t.Fatal(err)
	}
	if updates["public_ip"] != "198.51.100.196" {
		t.Fatal("changed NAT endpoint")
	}
}
