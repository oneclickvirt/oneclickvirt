package task

import (
	"errors"
	"gorm.io/gorm"
	providerModel "oneclickvirt/model/provider"
	"testing"
)

func TestDeleteIPv4ReleaseIsExactAndTransactional(t *testing.T) {
	db := resetDomainTestDB(t)
	if err := db.AutoMigrate(&providerModel.ProviderIPv4Pool{}); err != nil {
		t.Fatal(err)
	}
	first, second := uint(11), uint(12)
	rows := []providerModel.ProviderIPv4Pool{
		{ProviderID: 7, Address: "198.51.100.11", IsAllocated: true, InstanceID: &first},
		{ProviderID: 7, Address: "198.51.100.12", IsAllocated: true, InstanceID: &second},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("rollback deletion")
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := releaseDeletedInstanceIPv4(tx, first); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	var got providerModel.ProviderIPv4Pool
	if err := db.First(&got, rows[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	if !got.IsAllocated || got.InstanceID == nil || *got.InstanceID != first {
		t.Fatal("rollback released a live allocation")
	}
	for i := 0; i < 2; i++ {
		if err := releaseDeletedInstanceIPv4(db, first); err != nil {
			t.Fatal(err)
		}
	}
	var after []providerModel.ProviderIPv4Pool
	if err := db.Order("id").Find(&after).Error; err != nil {
		t.Fatal(err)
	}
	if after[0].IsAllocated || after[0].InstanceID != nil {
		t.Fatal("deleted guest kept allocation")
	}
	if !after[1].IsAllocated || after[1].InstanceID == nil || *after[1].InstanceID != second {
		t.Fatal("another guest allocation was released")
	}
}
