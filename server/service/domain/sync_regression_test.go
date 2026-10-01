package domain

import (
	"errors"
	"fmt"
	"testing"

	"gorm.io/gorm"
	domainModel "oneclickvirt/model/domain"
	providerModel "oneclickvirt/model/provider"
)

func TestDomainTargetSnapshotHasConstantReadCount(t *testing.T) {
	db := setupDomainServiceTestDB(t)
	for _, ddl := range []string{
		"CREATE TABLE providers (id INTEGER PRIMARY KEY, name TEXT, type TEXT, endpoint TEXT)",
		"CREATE TABLE instances (id INTEGER PRIMARY KEY, name TEXT, provider_id INTEGER, user_id INTEGER, status TEXT, private_ip TEXT, public_ip TEXT, ipv6_address TEXT, public_ipv6 TEXT, network_type TEXT, deleted_at DATETIME)",
	} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}

	p := providerModel.Provider{ID: 1, Name: "snapshot", Type: "incus", Endpoint: "192.0.2.1"}
	if err := db.Table("providers").Create(map[string]interface{}{"id": p.ID, "name": p.Name, "type": p.Type, "endpoint": p.Endpoint}).Error; err != nil {
		t.Fatal(err)
	}
	var instances []providerModel.Instance
	for i := 0; i < 100; i++ {
		instances = append(instances, providerModel.Instance{ID: uint(i + 1), Name: fmt.Sprint("guest-", i), ProviderID: p.ID, UserID: 1, Status: "running", PrivateIP: "10.0.0.2"})
	}
	for _, instance := range instances {
		if err := db.Table("instances").Create(map[string]interface{}{"id": instance.ID, "name": instance.Name, "provider_id": p.ID, "user_id": 1, "status": "running", "private_ip": instance.PrivateIP}).Error; err != nil {
			t.Fatal(err)
		}
	}
	reads := 0
	db.Callback().Query().After("gorm:query").Register("test:count", func(tx *gorm.DB) { reads++ })
	defer db.Callback().Query().Remove("test:count")
	provider, snapshot, err := loadDomainTargets(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, instance := range instances {
		row := domainModel.Domain{ProviderID: p.ID, InstanceID: instance.ID, UserID: 1, InternalIP: "10.0.0.2"}
		if err := validateDomainTargetRow(&row, snapshot[row.InstanceID], provider); err != nil {
			t.Fatal(err)
		}
	}
	if reads != 2 {
		t.Fatalf("100 targets used %d reads, want 2", reads)
	}
}

func TestTransientDomainReadFailurePreservesRoute(t *testing.T) {
	db := setupDomainServiceTestDB(t)
	row := domainModel.Domain{ProviderID: 1, InstanceID: 2, UserID: 1, DomainName: "test.example.com", Status: "active", OwnershipVerified: true}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	db.Callback().Query().Before("gorm:query").Register("test:unavailable", func(tx *gorm.DB) {
		if tx.Statement.Table == "instances" {
			tx.AddError(errors.New("temporary database failure"))
		}
	})
	defer db.Callback().Query().Remove("test:unavailable")
	if err := applyDomainProxy(&row); !errors.Is(err, errDomainTargetUnavailable) {
		t.Fatalf("temporary error: %v", err)
	}
	if err := db.First(&row, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if row.Status != "active" || row.ErrorMsg != "" {
		t.Fatal("transient read error damaged route")
	}
}

func TestDomainReloadRejectsMovedAndDeletedRows(t *testing.T) {
	db := setupDomainServiceTestDB(t)
	row := domainModel.Domain{ProviderID: 1, DomainName: "moved.example.com"}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&domainModel.Domain{}).Where("id = ?", row.ID).Update("provider_id", 2).Error; err != nil {
		t.Fatal(err)
	}
	if err := reloadLockedDomain(&row); err == nil {
		t.Fatal("acted under the old provider lock")
	}
	if err := db.Delete(&domainModel.Domain{}, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := reloadLockedDomain(&row); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("deleted domain: %v", err)
	}
}
