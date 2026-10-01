package agent

import (
	"errors"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	providerModel "oneclickvirt/model/provider"
	"path/filepath"
	"sync"
	"testing"
)

func TestPendingDiscoveryCanBeClaimedOnlyOnce(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "discovery.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	defer sqlDB.Close()
	if err := db.AutoMigrate(&providerModel.Provider{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&providerModel.Provider{ID: 1, Name: "discover", PendingDiscovery: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE discoveries (id INTEGER PRIMARY KEY, provider_id INTEGER)").Error; err != nil {
		t.Fatal(err)
	}
	failed := func(tx *gorm.DB, p *providerModel.Provider) error {
		if err := tx.Exec("INSERT INTO discoveries(provider_id) VALUES (?)", p.ID).Error; err != nil {
			return err
		}
		return errors.New("injected task failure")
	}
	if err := completePendingDiscovery(db, 1, failed); err == nil {
		t.Fatal("expected failure")
	}
	var pending bool
	db.Table("providers").Select("pending_discovery").Where("id = 1").Scan(&pending)
	var count int64
	db.Table("discoveries").Count(&count)
	if !pending || count != 0 {
		t.Fatalf("failed handoff consumed marker or task: pending=%v count=%d", pending, count)
	}
	if err := completePendingDiscovery(db, 1, nil); err == nil {
		t.Fatal("missing handler consumed discovery")
	}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := completePendingDiscovery(db, 1, func(tx *gorm.DB, p *providerModel.Provider) error {
				return tx.Exec("INSERT INTO discoveries(provider_id) VALUES (?)", p.ID).Error
			}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	db.Table("discoveries").Count(&count)
	db.Table("providers").Select("pending_discovery").Where("id = 1").Scan(&pending)
	if pending || count != 1 {
		t.Fatalf("handoff not atomic: pending=%v count=%d", pending, count)
	}
}
