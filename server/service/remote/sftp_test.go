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

func TestResolveInstanceSSHTargetUsesManualHostAndPrivateKey(t *testing.T) {
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

	provider := providerModel.Provider{
		Name:           "manual-access-node",
		Endpoint:       "198.51.100.10",
		ConnectionType: "ssh",
	}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}
	instance := &providerModel.Instance{
		ID:         44,
		ProviderID: provider.ID,
		SSHHost:    "ssh.example.test",
		SSHPort:    22044,
		Username:   "admin",
		SSHKey:     "-----BEGIN PRIVATE KEY-----\nexample\n-----END PRIVATE KEY-----",
	}

	target, err := ResolveInstanceSSHTarget(instance)
	if err != nil {
		t.Fatalf("ResolveInstanceSSHTarget() error = %v", err)
	}
	if target.Host != "ssh.example.test" || target.Port != 22044 {
		t.Fatalf("target endpoint = %s:%d, want ssh.example.test:22044", target.Host, target.Port)
	}
	if target.PrivateKey != instance.SSHKey || target.Password != "" {
		t.Fatalf("target credentials = %#v, want private key only", target)
	}
}

func TestResolveInstanceSSHTargetUsesManualHostForAgentFallback(t *testing.T) {
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

	provider := providerModel.Provider{Name: "manual-agent-node", ConnectionType: "agent", Endpoint: "node.example"}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}
	instance := &providerModel.Instance{
		ID: 46, ProviderID: provider.ID, SSHHost: "2001:db8::46", SSHPort: 2222,
		Username: "root", Password: "secret", PrivateIP: "10.0.0.46",
	}
	target, err := ResolveInstanceSSHTarget(instance)
	if err != nil {
		t.Fatal(err)
	}
	if target.Host != instance.SSHHost || target.FallbackAgentTunnelHost != instance.SSHHost || target.FallbackAgentTunnelPort != 2222 {
		t.Fatalf("manual agent fallback target = %#v", target)
	}
}

func TestResolveInstanceSSHTargetManualOverrideWinsOverActiveMapping(t *testing.T) {
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

	provider := providerModel.Provider{
		Name:           "manual-mapping-node",
		Endpoint:       "198.51.100.10",
		ConnectionType: "ssh",
	}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if err := db.Create(&providerModel.Port{
		InstanceID: 45,
		ProviderID: provider.ID,
		HostPort:   22045,
		GuestPort:  22,
		Protocol:   "tcp",
		Status:     "active",
		IsSSH:      true,
	}).Error; err != nil {
		t.Fatalf("create SSH mapping: %v", err)
	}

	instance := &providerModel.Instance{
		ID:         45,
		ProviderID: provider.ID,
		SSHHost:    "instance.example.test",
		SSHPort:    2222,
		Username:   "admin",
		Password:   "manual-secret",
	}
	target, err := ResolveInstanceSSHTarget(instance)
	if err != nil {
		t.Fatalf("ResolveInstanceSSHTarget() error = %v", err)
	}
	if target.Host != "instance.example.test" || target.Port != 2222 {
		t.Fatalf("target endpoint = %s:%d, want instance.example.test:2222", target.Host, target.Port)
	}
}
