package system

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	appConfig "oneclickvirt/config"
	"oneclickvirt/global"
	configModel "oneclickvirt/model/config"
	"oneclickvirt/utils/dbconnect"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

func TestDatabaseConfigWritePreservesPersistentSymlink(t *testing.T) {
	dir := t.TempDir()
	target, link := filepath.Join(dir, "persisted.yml"), filepath.Join(dir, "config.yml")
	if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := writeDatabaseConfigFile(link, []byte("new")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("persistent configuration symlink was replaced")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "new" {
		t.Fatal("persistent target was not updated")
	}
}

func useTemporaryConfigDirectory(t *testing.T, content string) string {
	t.Helper()

	originalWorkingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalWorkingDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	dir := t.TempDir()
	if err := os.WriteFile(dir+"/config.yaml", []byte(content), 0600); err != nil {
		t.Fatalf("write temporary config: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("change working directory: %v", err)
	}
	return dir
}

func useTestLogger(t *testing.T) {
	t.Helper()
	originalLogger := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = originalLogger })
}

func TestResolveDatabaseConfigCredentialsUsesLoadedDeploymentPassword(t *testing.T) {
	oldConfig := global.GetAppConfig()
	t.Cleanup(func() { global.SetAppConfig(oldConfig) })

	configured := appConfig.Server{}
	configured.Mysql.Path = "127.0.0.1"
	configured.Mysql.Port = "3306"
	configured.Mysql.Dbname = "oneclickvirt"
	configured.Mysql.Username = "root"
	configured.Mysql.Password = "generated-or-env-password"
	global.SetAppConfig(configured)

	request := configModel.DatabaseConfig{
		Type:     "mariadb",
		Host:     "localhost",
		Port:     3306,
		Database: "oneclickvirt",
		Username: "root",
	}
	resolved := ResolveDatabaseConfigCredentials(request)
	if resolved.Password != configured.Mysql.Password {
		t.Fatalf("resolved password = %q, want loaded deployment password", resolved.Password)
	}
}

func TestDatabaseRequestConfigNormalizesEngineHint(t *testing.T) {
	oldConfig := global.GetAppConfig()
	t.Cleanup(func() { global.SetAppConfig(oldConfig) })
	global.SetAppConfig(appConfig.Server{Mysql: appConfig.Mysql{Config: dbconnect.DefaultParams}})
	m, err := databaseRequestConfig(configModel.DatabaseConfig{
		Type: "  MariaDB ", Host: "127.0.0.1", Port: 3306,
		Database: "oneclickvirt", Username: "root",
	})
	if err != nil {
		t.Fatalf("databaseRequestConfig rejected a normalized engine hint: %v", err)
	}
	if m.Path != "127.0.0.1" || m.Port != "3306" {
		t.Fatalf("database request was not preserved: %+v", m)
	}
	if _, err := databaseRequestConfig(configModel.DatabaseConfig{Type: "postgres"}); err == nil {
		t.Fatal("unknown database engine was accepted")
	}
}

func TestResolveDatabaseConfigCredentialsDoesNotLeakToAnotherEndpoint(t *testing.T) {
	oldConfig := global.GetAppConfig()
	t.Cleanup(func() { global.SetAppConfig(oldConfig) })

	configured := appConfig.Server{}
	configured.Mysql.Path = "mysql.internal"
	configured.Mysql.Port = "3306"
	configured.Mysql.Dbname = "oneclickvirt"
	configured.Mysql.Username = "root"
	configured.Mysql.Password = "deployment-secret"
	global.SetAppConfig(configured)

	tests := []configModel.DatabaseConfig{
		{Host: "other.internal", Port: 3306, Database: "oneclickvirt", Username: "root"},
		{Host: "mysql.internal", Port: 3307, Database: "oneclickvirt", Username: "root"},
		{Host: "mysql.internal", Port: 3306, Database: "other", Username: "root"},
		{Host: "mysql.internal", Port: 3306, Database: "oneclickvirt", Username: "other"},
	}
	for _, request := range tests {
		if resolved := ResolveDatabaseConfigCredentials(request); resolved.Password != "" {
			t.Fatalf("password leaked to mismatched request %+v", request)
		}
	}
}

func TestResolveDatabaseConfigCredentialsPreservesExplicitPassword(t *testing.T) {
	oldConfig := global.GetAppConfig()
	t.Cleanup(func() { global.SetAppConfig(oldConfig) })

	configured := appConfig.Server{}
	configured.Mysql.Path = "127.0.0.1"
	configured.Mysql.Port = "3306"
	configured.Mysql.Dbname = "oneclickvirt"
	configured.Mysql.Username = "root"
	configured.Mysql.Password = "deployment-secret"
	global.SetAppConfig(configured)

	request := configModel.DatabaseConfig{
		Host: "127.0.0.1", Port: 3306, Database: "oneclickvirt", Username: "root", Password: "user-entered",
	}
	if resolved := ResolveDatabaseConfigCredentials(request); resolved.Password != "user-entered" {
		t.Fatalf("explicit password changed to %q", resolved.Password)
	}
}

func TestUpdateDatabaseConfigBypassesRuntimeConfigManager(t *testing.T) {
	useTestLogger(t)
	dir := useTemporaryConfigDirectory(t, "system: {}\nmysql: {}\n")

	originalConfig := global.GetAppConfig()
	t.Cleanup(func() { global.SetAppConfig(originalConfig) })

	// Reproduce the failing state from the initialization page: a ConfigManager
	// already exists, but database connection settings still need to be changed.
	// The old implementation called UpdateConfig and was rejected because every
	// system/mysql key is deliberately protected from runtime API updates.
	appConfig.PreInitializeConfigManager(nil, zap.NewNop(), nil)
	if appConfig.GetConfigManager() == nil {
		t.Fatal("expected ConfigManager to be initialized for regression test")
	}

	databaseConfig := configModel.DatabaseConfig{
		Type:     "mysql",
		Host:     "127.0.0.1",
		Port:     2102,
		Database: "user1",
		Username: "user1",
		Password: "database-secret",
	}
	if err := (&InitService{}).UpdateDatabaseConfig(databaseConfig); err != nil {
		t.Fatalf("UpdateDatabaseConfig returned error: %v", err)
	}

	updatedData, err := os.ReadFile(dir + "/config.yaml")
	if err != nil {
		t.Fatalf("read updated config: %v", err)
	}
	var updated appConfig.Server
	if err := yaml.Unmarshal(updatedData, &updated); err != nil {
		t.Fatalf("parse updated config: %v", err)
	}

	if updated.System.DbType != databaseConfig.Type {
		t.Fatalf("system.db-type = %q, want %q", updated.System.DbType, databaseConfig.Type)
	}
	if updated.Mysql.Path != databaseConfig.Host || updated.Mysql.Port != "2102" {
		t.Fatalf("mysql endpoint = %s:%s, want %s:%d", updated.Mysql.Path, updated.Mysql.Port, databaseConfig.Host, databaseConfig.Port)
	}
	if updated.Mysql.Dbname != databaseConfig.Database || updated.Mysql.Username != databaseConfig.Username || updated.Mysql.Password != databaseConfig.Password {
		t.Fatalf("mysql credentials/database were not persisted correctly: %+v", updated.Mysql)
	}
	if updated.Mysql.MaxIdleConns != 10 || updated.Mysql.MaxOpenConns != 100 || updated.Mysql.MaxLifetime != 3600 {
		t.Fatalf("mysql defaults were not created correctly: %+v", updated.Mysql)
	}

	runtimeConfig := global.GetAppConfig()
	if runtimeConfig.System.DbType != databaseConfig.Type || runtimeConfig.Mysql.Port != "2102" || runtimeConfig.Mysql.Dbname != databaseConfig.Database {
		t.Fatalf("runtime database config was not synchronized: %+v", runtimeConfig)
	}

	backupInfo, err := os.Stat(dir + "/config.yaml.backup")
	if err != nil {
		t.Fatalf("stat config backup: %v", err)
	}
	if mode := backupInfo.Mode().Perm(); mode != 0600 {
		t.Fatalf("config backup mode = %o, want 600", mode)
	}
}

func TestUpdateDatabaseConfigKeepsLegacyMariaDBSection(t *testing.T) {
	useTestLogger(t)
	dir := useTemporaryConfigDirectory(t, "system: {}\nmariadb: {}\n")

	originalConfig := global.GetAppConfig()
	t.Cleanup(func() { global.SetAppConfig(originalConfig) })

	databaseConfig := configModel.DatabaseConfig{
		Type:     "mariadb",
		Host:     "db.internal",
		Port:     3307,
		Database: "oneclickvirt",
		Username: "ocv",
		Password: "secret",
	}
	if err := (&InitService{}).UpdateDatabaseConfig(databaseConfig); err != nil {
		t.Fatalf("UpdateDatabaseConfig returned error: %v", err)
	}

	updatedData, err := os.ReadFile(dir + "/config.yaml")
	if err != nil {
		t.Fatalf("read updated config: %v", err)
	}
	var updated map[string]interface{}
	if err := yaml.Unmarshal(updatedData, &updated); err != nil {
		t.Fatalf("parse updated config: %v", err)
	}
	if _, exists := updated["mysql"]; exists {
		t.Fatal("legacy mariadb config was unexpectedly duplicated into a mysql section")
	}
	mariaDB, ok := updated["mariadb"].(map[string]interface{})
	if !ok {
		t.Fatalf("mariadb section missing or invalid: %#v", updated["mariadb"])
	}
	if mariaDB["path"] != databaseConfig.Host || mariaDB["port"] != "3307" || mariaDB["db-name"] != databaseConfig.Database {
		t.Fatalf("legacy mariadb section was not updated correctly: %#v", mariaDB)
	}
}

func TestPersistDetectedDatabaseConfigRepairsHintAndOptionsWithoutMixingSections(t *testing.T) {
	useTestLogger(t)
	useTemporaryConfigDirectory(t, "system:\n  db-type: mysql\nmysql:\n  path: db.internal\n  port: '3306'\n  db-name: oneclickvirt\n  username: ocv\n  password: private\n  config: charset=utf8mb4\n  max-open-conns: 17\nmariadb:\n  path: wrong.internal\n  password: must-not-be-copied\n")

	oldConfig, oldVP := global.GetAppConfig(), global.APP_VP
	t.Cleanup(func() {
		global.SetAppConfig(oldConfig)
		global.APP_VP = oldVP
	})
	source := configModel.MysqlConfig{Path: "db.internal", Port: "3306", Dbname: "oneclickvirt", Username: "ocv", Password: "private", Config: "charset=utf8mb4"}
	global.SetAppConfig(appConfig.Server{System: appConfig.System{DbType: "mysql"}, Mysql: appConfig.Mysql{
		Path: source.Path, Port: source.Port, Dbname: source.Dbname, Username: source.Username, Password: source.Password, Config: source.Config,
	}})
	global.APP_VP = nil

	info := dbconnect.Info{Type: "mariadb", Params: "charset=utf8mb4&tx_isolation=%27READ-COMMITTED%27", Repairs: []string{"transaction_isolation -> tx_isolation"}}
	if err := (&InitService{}).PersistDetectedDatabaseConfig(source, "mysql", info); err != nil {
		t.Fatalf("PersistDetectedDatabaseConfig returned error: %v", err)
	}

	data, err := os.ReadFile("config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var updated map[string]interface{}
	if err := yaml.Unmarshal(data, &updated); err != nil {
		t.Fatal(err)
	}
	if updated["system"].(map[string]interface{})["db-type"] != "mariadb" {
		t.Fatalf("database type was not repaired: %#v", updated["system"])
	}
	mysqlSection := updated["mysql"].(map[string]interface{})
	if mysqlSection["config"] != info.Params || mysqlSection["password"] != "private" || mysqlSection["max-open-conns"].(int) != 17 {
		t.Fatalf("mysql section was not repaired without losing tuning: %#v", mysqlSection)
	}
	mariaSection := updated["mariadb"].(map[string]interface{})
	if mariaSection["path"] != "wrong.internal" || mariaSection["password"] != "must-not-be-copied" {
		t.Fatalf("legacy section was mixed into the canonical section: %#v", mariaSection)
	}
	backupInfo, err := os.Stat("config.yaml.backup")
	if err != nil {
		t.Fatal(err)
	}
	if backupInfo.Mode().Perm() != 0600 {
		t.Fatalf("repair backup mode = %o, want 600", backupInfo.Mode().Perm())
	}
}

func TestPersistDetectedDatabaseConfigRepairsMissingOrNonCanonicalEngineHint(t *testing.T) {
	useTestLogger(t)
	for _, tc := range []struct {
		name, yaml, want string
	}{
		{
			name: "missing-system-hint",
			yaml: "mysql:\n  path: db.internal\n  port: '3306'\n  db-name: oneclickvirt\n  username: ocv\n  password: private\n",
			want: "mysql",
		},
		{
			name: "uppercase-hint",
			yaml: "system:\n  db-type: MYSQL\nmysql:\n  path: db.internal\n  port: '3306'\n  db-name: oneclickvirt\n  username: ocv\n  password: private\n",
			want: "mysql",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useTemporaryConfigDirectory(t, tc.yaml)
			oldConfig, oldVP := global.GetAppConfig(), global.APP_VP
			t.Cleanup(func() {
				global.SetAppConfig(oldConfig)
				global.APP_VP = oldVP
			})
			source := configModel.MysqlConfig{Path: "db.internal", Port: "3306", Dbname: "oneclickvirt", Username: "ocv", Password: "private", Config: dbconnect.DefaultParams}
			global.SetAppConfig(appConfig.Server{System: appConfig.System{DbType: "mysql"}, Mysql: appConfig.Mysql{
				Path: source.Path, Port: source.Port, Dbname: source.Dbname, Username: source.Username, Password: source.Password, Config: source.Config,
			}})
			global.APP_VP = nil
			if err := (&InitService{}).PersistDetectedDatabaseConfig(source, "mysql", dbconnect.Info{Type: "mysql", Params: source.Config}); err != nil {
				t.Fatalf("PersistDetectedDatabaseConfig returned error: %v", err)
			}
			data, err := os.ReadFile("config.yaml")
			if err != nil {
				t.Fatal(err)
			}
			var updated map[string]interface{}
			if err := yaml.Unmarshal(data, &updated); err != nil {
				t.Fatal(err)
			}
			system, ok := updated["system"].(map[string]interface{})
			if !ok || system["db-type"] != tc.want {
				t.Fatalf("database type was not normalized: %#v", updated["system"])
			}
		})
	}
}

func TestPersistDetectedDatabaseConfigIgnoresStaleProbe(t *testing.T) {
	useTestLogger(t)
	useTemporaryConfigDirectory(t, "system:\n  db-type: mysql\nmysql:\n  path: db-a\n  port: '3306'\n  db-name: oneclickvirt\n  username: ocv\n  password: private\n  config: charset=utf8mb4\n")

	oldConfig, oldVP := global.GetAppConfig(), global.APP_VP
	t.Cleanup(func() {
		global.SetAppConfig(oldConfig)
		global.APP_VP = oldVP
	})
	source := configModel.MysqlConfig{Path: "db-a", Port: "3306", Dbname: "oneclickvirt", Username: "ocv", Password: "private", Config: "charset=utf8mb4"}
	global.SetAppConfig(appConfig.Server{System: appConfig.System{DbType: "mysql"}, Mysql: appConfig.Mysql{Path: "db-b", Port: "3306", Dbname: "oneclickvirt", Username: "ocv", Password: "new", Config: source.Config}})
	global.APP_VP = nil
	if err := (&InitService{}).PersistDetectedDatabaseConfig(source, "mysql", dbconnect.Info{Type: "mariadb", Params: "repaired", Repairs: []string{"tx"}}); err != nil {
		t.Fatalf("stale probe returned error: %v", err)
	}
	data, err := os.ReadFile("config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "mariadb") || strings.Contains(string(data), "repaired") {
		t.Fatal("stale probe rewrote the newer configuration")
	}
}

func TestReinitializeDatabaseDoesNotReloadThroughOldConfigManager(t *testing.T) {
	useTestLogger(t)
	useTemporaryConfigDirectory(t, "system: {}\nmysql: {}\n")

	appConfig.PreInitializeConfigManager(nil, zap.NewNop(), nil)
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("ReinitializeDatabase panicked through stale ConfigManager: %v", recovered)
		}
	}()

	err := (&InitService{}).ReinitializeDatabase()
	if err == nil || !strings.Contains(err.Error(), "数据库配置不完整") {
		t.Fatalf("ReinitializeDatabase error = %v, want controlled incomplete-config error", err)
	}
}
