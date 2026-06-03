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

	v := viper.New()
	applyEnvOverrides(v)

	expected := map[string]string{
		"mysql.path":     "db.internal",
		"mysql.port":     "3307",
		"mysql.db-name":  "oneclickvirt_test",
		"mysql.username": "oneclickvirt",
		"mysql.password": "test-password",
		"system.db-type": "mariadb",
	}
	for key, want := range expected {
		if got := v.GetString(key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}
