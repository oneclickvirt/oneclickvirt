package initialize

import (
	"testing"

	"gorm.io/gorm"
)

func TestDatabaseManagerNotifiesConnectionRestoredHandler(t *testing.T) {
	dm := &DatabaseManager{}
	wantDB := &gorm.DB{}
	var gotDB *gorm.DB
	dm.SetConnectionRestoredHandler(func(db *gorm.DB) {
		gotDB = db
	})

	dm.notifyConnectionRestored(wantDB)
	if gotDB != wantDB {
		t.Fatalf("handler received %p, want %p", gotDB, wantDB)
	}
}
