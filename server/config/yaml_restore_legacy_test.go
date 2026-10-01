package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestRestoreConfigFromDatabaseSkipsLegacyLevelLimits(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:legacy-level-limits?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&SystemConfig{}); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{
		`{"1":{"max-instances":2}}`,
		`map[1:map[max-instances:1]]`,
	} {
		if err := db.Create(&SystemConfig{Key: "quota.level-limits", Value: value}).Error; err != nil {
			t.Fatal(err)
		}
	}
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldDir) })
	if err := os.WriteFile("config.yaml", []byte("quota:\n  level-limits: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cm := NewConfigManager(db, zap.NewNop())
	if err := cm.RestoreConfigFromDatabase(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join("config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var restored struct {
		Quota struct {
			LevelLimits map[int]struct {
				MaxInstances int `yaml:"max-instances"`
			} `yaml:"level-limits"`
		} `yaml:"quota"`
	}
	if err := yaml.Unmarshal(content, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Quota.LevelLimits[1].MaxInstances != 2 {
		t.Fatalf("restored level limit = %d, want 2", restored.Quota.LevelLimits[1].MaxInstances)
	}
}

func TestSystemConfigWritesWithoutUniqueIndex(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:system-config-upsert?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&SystemConfig{}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := db.Create(&SystemConfig{Key: "quota.level-limits", Value: "old"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		value := SystemConfig{Key: "quota.level-limits", Value: `{"1":{"max-instances":2}}`}
		if err := upsertSystemConfig(tx, value); err != nil {
			return err
		}
		return insertMissingSystemConfig(tx, value)
	}); err != nil {
		t.Fatal(err)
	}
	var rows []SystemConfig
	if err := db.Where("`key` = ?", "quota.level-limits").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("row count = %d, want 2 existing rows", len(rows))
	}
	for _, row := range rows {
		if row.Value != `{"1":{"max-instances":2}}` {
			t.Fatalf("row %d retained stale value", row.ID)
		}
	}
}

func TestSystemConfigBatchPersistenceUsesBoundedQueries(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:system-config-batch?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&SystemConfig{}); err != nil {
		t.Fatal(err)
	}

	var queryCount, createCount, updateCount int
	if err := db.Callback().Query().Before("gorm:query").Register("test:count_config_queries", func(tx *gorm.DB) {
		if tx.Statement.Table == "system_configs" {
			queryCount++
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Callback().Create().Before("gorm:create").Register("test:count_config_creates", func(tx *gorm.DB) {
		if tx.Statement.Table == "system_configs" {
			createCount++
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Callback().Update().Before("gorm:update").Register("test:count_config_updates", func(tx *gorm.DB) {
		if tx.Statement.Table == "system_configs" {
			updateCount++
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer db.Callback().Query().Remove("test:count_config_queries")
	defer db.Callback().Create().Remove("test:count_config_creates")
	defer db.Callback().Update().Remove("test:count_config_updates")

	configs := make([]SystemConfig, 250)
	for index := range configs {
		configs[index] = SystemConfig{
			Category: "quota",
			Key:      fmt.Sprintf("quota.key-%03d", index),
			Value:    fmt.Sprintf("old-%03d", index),
		}
	}
	if err := persistSystemConfigBatch(db, configs, false); err != nil {
		t.Fatal(err)
	}
	if queryCount > 3 || createCount > 3 || updateCount != 0 {
		t.Fatalf("insert queries=%d creates=%d updates=%d; want at most 3 reads and 3 inserts", queryCount, createCount, updateCount)
	}

	queryCount, createCount, updateCount = 0, 0, 0
	for index := range configs {
		configs[index].Value = fmt.Sprintf("new-%03d", index)
		configs[index].IsPublic = index%2 == 0
	}
	if err := persistSystemConfigBatch(db, configs, false); err != nil {
		t.Fatal(err)
	}
	if queryCount > 3 || updateCount > 3 || createCount != 0 {
		t.Fatalf("update queries=%d updates=%d creates=%d; want at most 3 reads and 3 updates", queryCount, updateCount, createCount)
	}

	queryCount, createCount, updateCount = 0, 0, 0
	if err := persistSystemConfigBatch(db, configs, true); err != nil {
		t.Fatal(err)
	}
	if queryCount > 3 || createCount != 0 || updateCount != 0 {
		t.Fatalf("insert-only queries=%d creates=%d updates=%d; existing rows must be preserved", queryCount, createCount, updateCount)
	}

	var rows []SystemConfig
	if err := db.Order("key").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(configs) {
		t.Fatalf("stored rows=%d, want %d", len(rows), len(configs))
	}
	for index, row := range rows {
		if !strings.HasSuffix(row.Key, fmt.Sprintf("%03d", index)) || row.Value != fmt.Sprintf("new-%03d", index) || row.IsPublic != (index%2 == 0) {
			t.Fatalf("row %d = key %q value %q public %v", index, row.Key, row.Value, row.IsPublic)
		}
	}
}

func TestPrepareConfigForDBNormalizesNumericYAMLMaps(t *testing.T) {
	cm := &ConfigManager{logger: zap.NewNop()}
	config, err := cm.prepareConfigForDB("quota.level-limits", map[interface{}]interface{}{
		1: map[interface{}]interface{}{"max-instances": 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.Value == "" || config.Value[:1] != "{" {
		t.Fatalf("numeric YAML map was not persisted as JSON: %q", config.Value)
	}
	var decoded map[string]map[string]int
	if err := json.Unmarshal([]byte(config.Value), &decoded); err != nil {
		t.Fatalf("persisted config is not JSON: %v", err)
	}
	if decoded["1"]["max-instances"] != 2 {
		t.Fatalf("decoded config = %#v", decoded)
	}
}
