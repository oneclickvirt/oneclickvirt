package resources

import (
	"fmt"
	"testing"
	"time"

	"go.uber.org/zap"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestResolveManualIPv6Enabled(t *testing.T) {
	trueValue, falseValue := true, false
	tests := []struct {
		name        string
		networkType string
		mappingType string
		requested   *bool
		want        bool
	}{
		{name: "dual stack defaults to both families", networkType: "nat_ipv4_ipv6", mappingType: "node", want: true},
		{name: "legacy whitespace and case are normalized", networkType: " NAT_IPV4_IPV6 ", mappingType: " NODE ", want: true},
		{name: "explicit false disables IPv6", networkType: "nat_ipv4_ipv6", mappingType: "node", requested: &falseValue, want: false},
		{name: "explicit true cannot enable IPv6 on v4 only", networkType: "nat_ipv4", mappingType: "node", requested: &trueValue, want: false},
		{name: "controller never persists node IPv6", networkType: "nat_ipv4_ipv6", mappingType: "controller", requested: &trueValue, want: false},
		{name: "IPv6 only has no manual node mapping", networkType: "ipv6_only", mappingType: "node", requested: &trueValue, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveManualIPv6Enabled(tt.networkType, tt.mappingType, tt.requested); got != tt.want {
				t.Fatalf("resolveManualIPv6Enabled(%q, %q, %v) = %t, want %t", tt.networkType, tt.mappingType, tt.requested, got, tt.want)
			}
		})
	}
}

func TestCreatePortMappingPersistsRequestedIPv6Flag(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:manual_ipv6_%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&providerModel.Provider{}); err != nil {
		t.Fatal(err)
	}
	// SQLite keeps index names global, while production databases scope them
	// by table. Provider and Instance intentionally share idx_status/idx_frozen,
	// so remove the first table's test-only indexes before migrating the second.
	if err := db.Exec("DROP INDEX IF EXISTS idx_status").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DROP INDEX IF EXISTS idx_frozen").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&providerModel.Instance{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DROP INDEX IF EXISTS idx_provider_id").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&providerModel.Port{}, &adminModel.SystemConfig{}); err != nil {
		t.Fatal(err)
	}
	previousDB, previousLog := global.APP_DB, global.APP_LOG
	global.APP_DB, global.APP_LOG = db, zap.NewNop()
	t.Cleanup(func() { global.APP_DB, global.APP_LOG = previousDB, previousLog })

	provider := providerModel.Provider{
		Name:                  "dual-stack-provider",
		Type:                  "incus",
		NetworkType:           "nat_ipv4_ipv6",
		PortRangeStart:        20000,
		PortRangeEnd:          21000,
		NextAvailablePort:     20000,
		IPv4PortMappingMethod: "device_proxy",
	}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatal(err)
	}
	instance := providerModel.Instance{Name: "dual-stack-instance", Provider: provider.Name, ProviderID: provider.ID, Status: "running", PrivateIP: "10.0.0.10"}
	if err := db.Create(&instance).Error; err != nil {
		t.Fatal(err)
	}
	requested := true
	portID, taskData, err := (&PortMappingService{}).CreatePortMappingWithTask(adminModel.CreatePortMappingRequest{
		InstanceID:  instance.ID,
		GuestPort:   20080,
		HostPort:    20000,
		Protocol:    "tcp",
		MappingType: "node",
		IPv6Enabled: &requested,
	})
	if err != nil {
		t.Fatal(err)
	}
	if taskData == nil || portID == 0 {
		t.Fatalf("manual node mapping returned port=%d task=%#v", portID, taskData)
	}
	var persisted providerModel.Port
	if err := db.First(&persisted, portID).Error; err != nil {
		t.Fatal(err)
	}
	if !persisted.IPv6Enabled {
		t.Fatalf("ipv6_enabled was not persisted: %#v", persisted)
	}
}

func TestAllocateControllerPortAccountsForProviderNodeMappings(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:controller_allocate_%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&providerModel.Port{}); err != nil {
		t.Fatal(err)
	}
	previous := global.APP_DB
	global.APP_DB = db
	t.Cleanup(func() { global.APP_DB = previous })
	if err := db.Create(&providerModel.Port{ProviderID: 7, HostPort: 10000, GuestPort: 80, MappingType: "node", PortCount: 1}).Error; err != nil {
		t.Fatal(err)
	}
	allocated, err := (&PortMappingService{}).allocateControllerPortWithDB(db, 7, 10000, 10002, 1)
	if err != nil {
		t.Fatal(err)
	}
	if allocated != 10001 {
		t.Fatalf("allocated controller port = %d, want 10001", allocated)
	}
}
