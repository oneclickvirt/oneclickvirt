package provider

import (
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"reflect"
	"testing"
)

func TestUserNetworkPolicySurvivesSaveAndExplicitClear(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	if err := db.AutoMigrate(&Provider{}); err != nil {
		t.Fatal(err)
	}
	node := Provider{Name: "pve", Type: "proxmox", NetworkType: "nat_ipv4"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := node.ResolveUserNetworkType("dedicated_ipv4_ipv6"); err == nil {
		t.Fatal("upgraded node granted unconfigured public addresses")
	}
	node.AllowedNetworkTypes, err = NormalizeAllowedNetworkTypes([]string{" dedicated_ipv4_ipv6 ", "ipv6_only", "dedicated_ipv4_ipv6"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Save(&node).Error; err != nil {
		t.Fatal(err)
	}
	var loaded Provider
	if err := db.First(&loaded, node.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got := loaded.UserNetworkTypes(); !reflect.DeepEqual(got, []string{"nat_ipv4", "dedicated_ipv4_ipv6", "ipv6_only"}) {
		t.Fatalf("stored choices = %v", got)
	}
	if got, err := loaded.ResolveUserNetworkType("dedicated_ipv4_ipv6"); err != nil || got != "dedicated_ipv4_ipv6" {
		t.Fatalf("authorized override = %q, %v", got, err)
	}
	if _, err := loaded.ResolveUserNetworkType("no_port_mapping"); err == nil {
		t.Fatal("ungranted mode bypassed policy")
	}
	loaded.AllowedNetworkTypes = []string{}
	if err := db.Save(&loaded).Error; err != nil {
		t.Fatal(err)
	}
	var cleared Provider
	if err := db.First(&cleared, node.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got := cleared.UserNetworkTypes(); !reflect.DeepEqual(got, []string{"nat_ipv4"}) {
		t.Fatalf("clear did not revoke choices: %v", got)
	}
	if _, err := cleared.ResolveUserNetworkType("dedicated_ipv4_ipv6"); err == nil {
		t.Fatal("revoked mode still allowed")
	}
	if got, err := cleared.ResolveUserNetworkType(""); err != nil || got != "nat_ipv4" {
		t.Fatalf("legacy request = %q, %v", got, err)
	}
}

func TestNetworkPolicyRejectsUnknownModesAndKeepsSafeLegacyDefault(t *testing.T) {
	if _, err := NormalizeAllowedNetworkTypes([]string{"dedicated_ipv4_ipv6", "arbitrary"}); err == nil {
		t.Fatal("unknown mode accepted")
	}
	node := Provider{}
	if got, err := node.ResolveUserNetworkType(""); err != nil || got != "nat_ipv4" {
		t.Fatalf("legacy default = %q, %v", got, err)
	}
}
