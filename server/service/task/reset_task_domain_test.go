package task

import (
	"fmt"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	domainModel "oneclickvirt/model/domain"
	providerModel "oneclickvirt/model/provider"
)

func resetDomainTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func TestMigrateResetDomainTargetsByAddressFamily(t *testing.T) {
	db := resetDomainTestDB(t)
	if err := db.AutoMigrate(&domainModel.Domain{}); err != nil {
		t.Fatal(err)
	}
	rows := []domainModel.Domain{
		{UserID: 7, InstanceID: 11, ProviderID: 13, DomainName: "v4.example.test", InternalIP: "192.0.2.10"},
		{UserID: 7, InstanceID: 11, ProviderID: 13, DomainName: "public-v4.example.test", InternalIP: "198.51.100.10"},
		{UserID: 7, InstanceID: 11, ProviderID: 13, DomainName: "v6.example.test", InternalIP: "2001:db8::10"},
		{UserID: 7, InstanceID: 11, ProviderID: 13, DomainName: "public-v6.example.test", InternalIP: "2001:db8:1::10"},
		{UserID: 7, InstanceID: 11, ProviderID: 13, DomainName: "unchanged.example.test", InternalIP: "192.0.2.99"},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	old := providerModel.Instance{
		PrivateIP: "192.0.2.10", PublicIP: "198.51.100.10", IPv6Address: "2001:db8::10/64", PublicIPv6: "2001:db8:1::10",
	}
	if err := migrateResetDomainTargets(db, 11, old, "192.0.2.20", "198.51.100.20", "2001:db8::20", "2001:db8:1::20"); err != nil {
		t.Fatal(err)
	}
	var got []domainModel.Domain
	if err := db.Order("id").Find(&got).Error; err != nil {
		t.Fatal(err)
	}
	want := []string{"192.0.2.20", "198.51.100.20", "2001:db8::20", "2001:db8:1::20", "192.0.2.99"}
	for index := range want {
		if got[index].InternalIP != want[index] {
			t.Errorf("domain %s target = %q, want %q", got[index].DomainName, got[index].InternalIP, want[index])
		}
	}
}

func TestMigrateResetDomainTargetsLeavesOldAddressWithoutReplacement(t *testing.T) {
	db := resetDomainTestDB(t)
	if err := db.AutoMigrate(&domainModel.Domain{}); err != nil {
		t.Fatal(err)
	}
	row := domainModel.Domain{UserID: 7, InstanceID: 11, ProviderID: 13, DomainName: "v6.example.test", InternalIP: "2001:db8::10"}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateResetDomainTargets(db, 11, providerModel.Instance{IPv6Address: "2001:db8::10"}, "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	var got domainModel.Domain
	if err := db.First(&got, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.InternalIP != row.InternalIP {
		t.Fatalf("domain target changed without a replacement address: got %q want %q", got.InternalIP, row.InternalIP)
	}
}

func TestTransferResetIPv4BindingRequiresExactCurrentOwner(t *testing.T) {
	for _, change := range []string{"none", "success", "stale_address"} {
		t.Run(change, func(t *testing.T) {
			db := resetDomainTestDB(t)
			if err := db.AutoMigrate(&providerModel.ProviderIPv4Pool{}); err != nil {
				t.Fatal(err)
			}
			oldID := uint(11)
			row := providerModel.ProviderIPv4Pool{ProviderID: 13, Address: "198.51.100.10", IsAllocated: true, InstanceID: &oldID}
			if err := db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
			address := row.Address
			wantOwner := oldID
			if change == "none" {
				if err := db.Model(&row).Update("instance_id", 12).Error; err != nil {
					t.Fatal(err)
				}
				wantOwner = 12
			}
			if change == "stale_address" {
				address = "198.51.100.11"
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				return transferResetIPv4BindingInTx(tx, 13, 11, 22, row.ID, address)
			})
			if change == "success" {
				if err != nil {
					t.Fatal(err)
				}
				var got providerModel.ProviderIPv4Pool
				if err := db.First(&got, row.ID).Error; err != nil {
					t.Fatal(err)
				}
				if got.InstanceID == nil || *got.InstanceID != 22 || !got.IsAllocated {
					t.Fatalf("IPv4 allocation was not transferred: %+v", got)
				}
				return
			}
			if err == nil {
				t.Fatal("stale IPv4 allocation state was accepted")
			}
			var got providerModel.ProviderIPv4Pool
			if err := db.First(&got, row.ID).Error; err != nil {
				t.Fatal(err)
			}
			if got.InstanceID == nil || *got.InstanceID != wantOwner {
				t.Fatalf("IPv4 owner changed after rejected transfer: %+v", got)
			}
		})
	}
}

func TestResetDomainIPv6SpellingAndAddressSwapHaveConstantQueries(t *testing.T) {
	db := resetDomainTestDB(t)
	if err := db.AutoMigrate(&domainModel.Domain{}); err != nil {
		t.Fatal(err)
	}
	rows := make([]domainModel.Domain, 120)
	for index := range rows {
		address := "2001:0db8:0000:0000:0000:0000:0000:0010"
		if index%2 != 0 {
			address = "2001:db8::20"
		}
		rows[index] = domainModel.Domain{UserID: 7, InstanceID: 11, ProviderID: 13,
			DomainName: fmt.Sprintf("guest%d.example.test", index), InternalIP: address}
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	other := domainModel.Domain{UserID: 8, InstanceID: 12, ProviderID: 13, DomainName: "other.example.test", InternalIP: "2001:db8::10"}
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	reads, writes := 0, 0
	if err := db.Callback().Query().After("gorm:query").Register("test:reset_reads", func(*gorm.DB) { reads++ }); err != nil {
		t.Fatal(err)
	}
	if err := db.Callback().Update().After("gorm:update").Register("test:reset_writes", func(*gorm.DB) { writes++ }); err != nil {
		t.Fatal(err)
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		return migrateResetDomainTargets(tx, 11,
			providerModel.Instance{IPv6Address: "2001:db8::10/64", PublicIPv6: "2001:db8::20"}, "", "", "2001:db8::20", "2001:db8::10")
	})
	if err != nil {
		t.Fatal(err)
	}
	if reads != 1 || writes != 2 {
		t.Fatalf("120 domains took %d reads and %d writes, want 1 and 2", reads, writes)
	}
	var got []domainModel.Domain
	if err := db.Order("id").Find(&got).Error; err != nil {
		t.Fatal(err)
	}
	for index := range rows {
		want := "2001:db8::20"
		if index%2 != 0 {
			want = "2001:db8::10"
		}
		if got[index].InternalIP != want {
			t.Fatalf("address swap migrated domain %d twice or missed its IPv6 spelling: got %q want %q", index, got[index].InternalIP, want)
		}
	}
	if got[len(rows)].InternalIP != other.InternalIP {
		t.Fatal("reset migrated a domain belonging to another instance")
	}
}
