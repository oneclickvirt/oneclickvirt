package config

import (
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestRegisterChangeCallbackOnceDeduplicatesLifecycleCallback(t *testing.T) {
	cm := NewConfigManager(nil, zap.NewNop())
	callback := func(string, interface{}, interface{}) error { return nil }

	cm.RegisterChangeCallbackOnce(callback)
	cm.RegisterChangeCallbackOnce(callback)

	if len(cm.changeCallbacks) != 1 {
		t.Fatalf("callback count = %d, want 1", len(cm.changeCallbacks))
	}
}

func TestSyncToGlobalConfigUsesFullCacheSnapshot(t *testing.T) {
	cm := &ConfigManager{
		logger: zap.NewNop(),
		configCache: map[string]interface{}{
			"captcha.enabled":                 true,
			"auth.enable-public-registration": false,
		},
	}

	triggered := map[string]interface{}{}
	cm.changeCallbacks = append(cm.changeCallbacks, func(key string, oldValue, newValue interface{}) error {
		triggered[key] = newValue
		return nil
	})

	if err := cm.syncToGlobalConfig(map[string]interface{}{"captcha": map[string]interface{}{"enabled": true}}); err != nil {
		t.Fatalf("syncToGlobalConfig returned error: %v", err)
	}

	captchaConfig, ok := triggered["captcha"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected captcha callback payload, got %T", triggered["captcha"])
	}
	if enabled, ok := captchaConfig["enabled"].(bool); !ok || !enabled {
		t.Fatalf("expected captcha.enabled=true in callback payload, got %#v", captchaConfig["enabled"])
	}

	authConfig, ok := triggered["auth"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected auth callback payload, got %T", triggered["auth"])
	}
	if registrationEnabled, ok := authConfig["enable-public-registration"].(bool); !ok || registrationEnabled {
		t.Fatalf("expected auth.enable-public-registration=false in callback payload, got %#v", authConfig["enable-public-registration"])
	}
}

func TestUpdateConfigKeepsLanguageRevisionOnUnchangedSave(t *testing.T) {
	t.Chdir(t.TempDir())
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "config.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&SystemConfig{}); err != nil {
		t.Fatal(err)
	}

	const key = "other.default-language"
	seededAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seed := SystemConfig{Key: key, Value: "zh", IsPublic: true, CreatedAt: seededAt, UpdatedAt: seededAt}
	if err := db.Create(&seed).Error; err != nil {
		t.Fatal(err)
	}
	cm := NewConfigManager(db, zap.NewNop())
	cm.configCache[key] = "zh"
	update := map[string]interface{}{"other": map[string]interface{}{"defaultLanguage": "en-US"}}
	read := func() SystemConfig {
		t.Helper()
		var rows []SystemConfig
		if err := db.Where("`key` = ?", key).Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].ID != seed.ID || rows[0].Value != "en-US" {
			t.Fatalf("unexpected language rows: %+v", rows)
		}
		return rows[0]
	}
	if err := cm.UpdateConfig(update); err != nil {
		t.Fatal(err)
	}
	revision := read().UpdatedAt
	if !revision.After(seededAt) {
		t.Fatal("changing the language should advance the revision")
	}
	if err := cm.UpdateConfig(update); err != nil {
		t.Fatal(err)
	}
	if !read().UpdatedAt.Equal(revision) {
		t.Fatal("saving the same language should retain the revision")
	}
}
