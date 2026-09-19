package config

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestDatabaseSectionCompatibility(t *testing.T) {
	for _, key := range []string{"DB_HOST", "DB_PORT", "DB_USER", "DB_PASSWORD", "DB_TYPE", "DB_NAME"} {
		t.Setenv(key, "")
	}
	for _, tc := range []struct{ name, yaml, host, password string }{
		{"legacy", "system: {db-type: mysql}\nmariadb: {path: legacy, port: 3307, password: ' pass '}", "legacy", " pass "},
		{"wrong-label", "system: {db-type: mariadb}\nmysql: {path: canonical, password: mysql-pass}", "canonical", "mysql-pass"},
		{"both-no-mixing", "mysql: {path: canonical}\nmariadb: {path: legacy, password: private}", "canonical", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := viper.New()
			v.SetDefault("mysql.path", "default-host")
			v.SetConfigType("yaml")
			if err := v.ReadConfig(strings.NewReader(tc.yaml)); err != nil {
				t.Fatal(err)
			}
			var cfg Server
			if err := v.Unmarshal(&cfg); err != nil {
				t.Fatal(err)
			}
			if err := DecodeDatabase(v, &cfg); err != nil {
				t.Fatal(err)
			}
			if cfg.Mysql.Path != tc.host || cfg.Mysql.Password != tc.password {
				t.Fatal("selected wrong database section or mixed credentials")
			}
			t.Setenv("DB_HOST", "env-host")
			t.Setenv("DB_PASSWORD", " \"literal' password\" ")
			if err := DecodeDatabase(v, &cfg); err != nil {
				t.Fatal(err)
			}
			if cfg.Mysql.Path != "env-host" || cfg.Mysql.Password != " \"literal' password\" " {
				t.Fatal("environment precedence or password bytes changed")
			}
		})
	}
}

func TestLegacyDatabaseKeysRemainStartupOnly(t *testing.T) {
	for _, key := range []string{"mariadb.password", "mariadb.path", "mariadb.config", "mariadb.username"} {
		if !isSystemLevelConfig(key) {
			t.Fatalf("legacy key %s can leak into business configuration", key)
		}
	}
}

func TestDatabaseTypeNormalization(t *testing.T) {
	for _, tc := range []struct {
		input, want string
	}{
		{" MYSQL ", "mysql"},
		{"MariaDB", "mariadb"},
		{"", ""},
	} {
		if got := NormalizeDatabaseType(tc.input); got != tc.want {
			t.Fatalf("NormalizeDatabaseType(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
	if !IsSupportedDatabaseType(" MariaDB ") || IsSupportedDatabaseType("postgres") {
		t.Fatal("database type support check is not normalized")
	}
}
