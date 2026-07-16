package system

import (
	"os"
	"strings"
	"testing"

	appConfig "oneclickvirt/config"
	"oneclickvirt/global"
	configModel "oneclickvirt/model/config"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

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
