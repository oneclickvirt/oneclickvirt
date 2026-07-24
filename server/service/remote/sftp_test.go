package remote

import (
	"fmt"
	"testing"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestResolveImportedInstanceSSHTargetUsesDiscoveredMappingAndCredentials(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if err := db.AutoMigrate(&providerModel.Provider{}, &providerModel.Port{}); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}

	previousDB := global.APP_DB
	global.APP_DB = db
	t.Cleanup(func() { global.APP_DB = previousDB })

	node := providerModel.Provider{
		Name:           "import-node",
		Type:           "proxmox",
		Endpoint:       "198.51.100.10",
		PortIP:         "203.0.113.10",
		ConnectionType: "ssh",
	}
	if err := db.Create(&node).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}
	instance := &providerModel.Instance{
		ID:         42,
		ProviderID: node.ID,
		Username:   "root",
		Password:   "ImportedSecret",
		PrivateIP:  "172.16.1.42",
		SSHPort:    22042,
	}
	sshMapping := providerModel.Port{
		InstanceID: 42,
		ProviderID: node.ID,
		HostPort:   22042,
		GuestPort:  22,
		Protocol:   "tcp",
		Status:     "active",
		IsSSH:      true,
	}
	if err := db.Create(&sshMapping).Error; err != nil {
		t.Fatalf("create SSH mapping: %v", err)
	}

	target, err := ResolveInstanceSSHTarget(instance)
	if err != nil {
		t.Fatalf("ResolveInstanceSSHTarget() error = %v", err)
	}
	if target.Host != "203.0.113.10" || target.Port != 22042 {
		t.Fatalf("target endpoint = %s:%d, want 203.0.113.10:22042", target.Host, target.Port)
	}
	if target.Username != "root" || target.Password != "ImportedSecret" {
		t.Fatalf("target credentials were not preserved: %#v", target)
	}
}

func TestResolveImportedIPv6OnlyInstanceSSHTarget(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if err := db.AutoMigrate(&providerModel.Provider{}, &providerModel.Port{}); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}

	previousDB := global.APP_DB
	global.APP_DB = db
	t.Cleanup(func() { global.APP_DB = previousDB })

	instance := &providerModel.Instance{
		ID:          43,
		Username:    "root",
		Password:    "ImportedIPv6Secret",
		IPv6Address: "2001:db8::43",
		SSHPort:     22,
	}
	target, err := ResolveInstanceSSHTarget(instance)
	if err != nil {
		t.Fatalf("ResolveInstanceSSHTarget() error = %v", err)
	}
	if target.Host != "2001:db8::43" || target.Port != 22 {
		t.Fatalf("target endpoint = %s:%d, want [2001:db8::43]:22", target.Host, target.Port)
	}
	if target.Username != "root" || target.Password != "ImportedIPv6Secret" {
		t.Fatalf("target credentials were not preserved: %#v", target)
	}
}
