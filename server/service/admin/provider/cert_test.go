package provider

import (
	"testing"

	"oneclickvirt/global"
	"oneclickvirt/model/common"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestGenerateProviderCertRejectsUnsupportedRuntimeAsValidation(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	previous := global.APP_DB
	global.APP_DB = db
	t.Cleanup(func() { global.APP_DB = previous })
	if err := db.Exec("CREATE TABLE providers (id INTEGER PRIMARY KEY, type TEXT, deleted_at DATETIME)").Error; err != nil {
		t.Fatal(err)
	}
	for index, runtime := range []string{"docker", "podman", "containerd", "qemu", "kubevirt"} {
		id := index + 1
		if err := db.Exec("INSERT INTO providers (id, type) VALUES (?, ?)", id, runtime).Error; err != nil {
			t.Fatal(err)
		}
		_, err := NewService().GenerateProviderCert(uint(id))
		if err == nil || common.ClassifyError(err).Code != common.CodeValidationError {
			t.Fatalf("%s certificate error=%v, want validation error", runtime, err)
		}
	}
}
