package dbconnect

import (
	"strings"
	"testing"
	"time"

	"oneclickvirt/model/config"
)

func TestDriverConfigPreservesCredentialsAndIPv6(t *testing.T) {
	m := config.MysqlConfig{Path: "[2001:db8::1]", Port: "3307", Username: "user:name", Password: " @:/?#&%+'\\\" ", Dbname: "db/name"}
	cfg, err := DriverConfig(m)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "[2001:db8::1]:3307" || cfg.User != m.Username || cfg.Passwd != m.Password || cfg.DBName != m.Dbname {
		t.Fatal("DSN parsing altered credentials/address/database")
	}
	if cfg.Timeout != 10*time.Second {
		t.Fatal("missing bounded dial timeout")
	}
	m.Config = "timeout=2s&readTimeout=90s&tls=true&parseTime=true"
	cfg, err = DriverConfig(m)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Timeout != 2*time.Second || cfg.ReadTimeout != 90*time.Second || cfg.TLS == nil || cfg.TLS.InsecureSkipVerify {
		t.Fatal("user timeout or TLS verification was weakened")
	}
	if cfg.TLS.ServerName != "2001:db8::1" {
		t.Fatal("TLS hostname is not bound to the actual database address")
	}
	m.Path = "database.internal"
	cfg, err = DriverConfig(m)
	if err != nil || cfg.TLS.ServerName != "database.internal" {
		t.Fatal("TLS DNS verification uses a placeholder host")
	}
}

func TestInvalidOptionsFailWithoutLeakingValues(t *testing.T) {
	for _, options := range []string{"tls=not-a-tls-profile-secret", "loc=invalid-secret-zone", "timeout=private-secret", "tls=true&tls=false", "loc=%Qsecret"} {
		_, err := DriverConfig(config.MysqlConfig{Username: "user", Config: options})
		if err == nil {
			t.Fatalf("accepted malformed options: %s", options)
		}
		if strings.Contains(err.Error(), "secret") {
			t.Fatal("error leaked option value")
		}
	}
	for _, port := range []string{"0", "-1", "65536", "3306:tcp"} {
		if _, err := DriverConfig(config.MysqlConfig{Username: "user", Port: port}); err == nil {
			t.Fatalf("accepted invalid port %s", port)
		}
	}
}
