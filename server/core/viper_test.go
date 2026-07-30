package core

import (
	"strings"
	"testing"

	appConfig "oneclickvirt/config"

	"github.com/spf13/viper"
)

func TestNormalizeDatabaseConfigReplacesExplicitBlankValues(t *testing.T) {
	v := viper.New()
	setDefaults(v)
	v.SetConfigType("yaml")
	if err := v.ReadConfig(strings.NewReader(`
system:
  db-type: ""
mysql:
  path: ""
  port: " "
  config: ""
  db-name: ""
  username: ""
  password: ""
  engine: ""
  max-idle-conns: 0
  max-open-conns: -1
  max-lifetime: 0
`)); err != nil {
		t.Fatalf("ReadConfig() error = %v", err)
	}

	var cfg appConfig.Server
	if err := v.Unmarshal(&cfg); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	normalizeDatabaseConfig(&cfg)

	expectedStrings := map[string]string{
		"system.db-type": cfg.System.DbType,
		"mysql.path":     cfg.Mysql.Path,
		"mysql.port":     cfg.Mysql.Port,
		"mysql.db-name":  cfg.Mysql.Dbname,
		"mysql.username": cfg.Mysql.Username,
		"mysql.engine":   cfg.Mysql.Engine,
	}
	expectedValues := map[string]string{
		"system.db-type": "mysql",
		"mysql.path":     "127.0.0.1",
		"mysql.port":     "3306",
		"mysql.db-name":  "oneclickvirt",
		"mysql.username": "root",
		"mysql.engine":   "InnoDB",
	}
	for key, got := range expectedStrings {
		if want := expectedValues[key]; got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
	if got := cfg.Mysql.Password; got != "" {
		t.Fatalf("mysql.password = %q, want an intentionally blank password", got)
	}
	if got := cfg.Mysql.MaxIdleConns; got != 20 {
		t.Fatalf("mysql.max-idle-conns = %d, want 20", got)
	}
	if got := cfg.Mysql.MaxOpenConns; got != 200 {
		t.Fatalf("mysql.max-open-conns = %d, want 200", got)
	}
	if got := cfg.Mysql.MaxLifetime; got != 1800 {
		t.Fatalf("mysql.max-lifetime = %d, want 1800", got)
	}
	if got := v.GetString("mysql.path"); got != "" {
		t.Fatalf("normalization mutated Viper precedence: mysql.path = %q", got)
	}
}

func TestApplyEnvOverridesDatabaseConfig(t *testing.T) {
	t.Setenv("DB_HOST", "db.internal")
	t.Setenv("DB_PORT", "3307")
	t.Setenv("DB_NAME", "oneclickvirt_test")
	t.Setenv("DB_USER", "oneclickvirt")
	t.Setenv("DB_PASSWORD", "test-password")
	t.Setenv("DB_TYPE", "mariadb")
	t.Setenv("SERVER_PORT", "8888")

	v := viper.New()
	applyEnvOverrides(v)

	expected := map[string]string{
		"mysql.path":     "db.internal",
		"mysql.port":     "3307",
		"mysql.db-name":  "oneclickvirt_test",
		"mysql.username": "oneclickvirt",
		"mysql.password": "test-password",
		"system.db-type": "mariadb",
		"system.addr":    "8888",
	}
	for key, want := range expected {
		if got := v.GetString(key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestApplyEnvOverridesIgnoresEmptyDeploymentVariables(t *testing.T) {
	t.Setenv("DB_HOST", "")
	t.Setenv("DB_PORT", "   ")
	t.Setenv("DB_NAME", "")
	t.Setenv("DB_USER", "")
	t.Setenv("DB_PASSWORD", "")
	t.Setenv("DB_TYPE", "")
	t.Setenv("SERVER_PORT", "")

	v := viper.New()
	v.Set("mysql.path", "persisted-db")
	v.Set("mysql.port", "3307")
	v.Set("mysql.db-name", "persisted-name")
	v.Set("mysql.username", "persisted-user")
	v.Set("mysql.password", "persisted-password")
	v.Set("system.db-type", "mariadb")
	v.Set("system.addr", "20562")

	applyEnvOverrides(v)

	if got := v.GetString("mysql.path"); got != "persisted-db" {
		t.Fatalf("mysql.path = %q, want persisted-db", got)
	}
	if got := v.GetString("mysql.password"); got != "persisted-password" {
		t.Fatalf("mysql.password = %q, want persisted-password", got)
	}
	if got := v.GetString("system.db-type"); got != "mariadb" {
		t.Fatalf("system.db-type = %q, want mariadb", got)
	}
	if got := v.GetString("system.addr"); got != "20562" {
		t.Fatalf("system.addr = %q, want 20562", got)
	}
}

func TestApplyEnvOverridesRemovesLiteralDeploymentQuotes(t *testing.T) {
	t.Setenv("DB_HOST", "'db.internal'")
	t.Setenv("DB_PORT", `"3306"`)
	t.Setenv("DB_NAME", `"oneclickvirt"`)
	t.Setenv("DB_USER", "'root'")
	t.Setenv("DB_PASSWORD", `"literal-password-quotes-are-valid"`)
	t.Setenv("DB_TYPE", `"mariadb"`)
	t.Setenv("SERVER_PORT", "'8888'")

	v := viper.New()
	applyEnvOverrides(v)

	expected := map[string]string{
		"mysql.path":     "db.internal",
		"mysql.port":     "3306",
		"mysql.db-name":  "oneclickvirt",
		"mysql.username": "root",
		"mysql.password": `"literal-password-quotes-are-valid"`,
		"system.db-type": "mariadb",
		"system.addr":    "8888",
	}
	for key, want := range expected {
		if got := v.GetString(key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}
