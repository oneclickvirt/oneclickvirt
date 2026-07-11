package core

import (
	"testing"

	"github.com/spf13/viper"
)

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
