//go:build dbintegration

package dbconnect_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	appconfig "oneclickvirt/config"
	"oneclickvirt/global"
	"oneclickvirt/initialize"
	model "oneclickvirt/model/config"
	"oneclickvirt/service/system"
	"oneclickvirt/utils/dbcompat"
	"oneclickvirt/utils/dbconnect"

	"github.com/spf13/viper"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func liveSettings(t *testing.T) (model.MysqlConfig, string) {
	t.Helper()
	address, engine := os.Getenv("OCV_TEST_DB_ADDR"), os.Getenv("OCV_TEST_DB_ENGINE")
	if address == "" || (engine != "mysql" && engine != "mariadb") {
		t.Fatal("dbintegration requires a real isolated database; run scripts/tests/database_compat_integration_test.sh")
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	return model.MysqlConfig{Path: host, Port: port, Username: "oneclickvirt", Password: os.Getenv("OCV_TEST_DB_PASSWORD"), Dbname: "ocv_dbcompat", Config: dbconnect.DefaultParams, MaxIdleConns: 3, MaxOpenConns: 7, MaxLifetime: 73}, engine
}

func closeDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err = pool.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLiveConnectionCompatibility(t *testing.T) {
	base, engine := liveSettings(t)
	for _, tc := range []struct {
		name, extra string
		repair      bool
	}{
		{"defaults", "", false},
		{"mysql-isolation-name", "&transaction_isolation=%27READ-COMMITTED%27", engine == "mariadb"},
		{"maria-isolation-name", "&tx_isolation=%27READ-COMMITTED%27", engine == "mysql"},
		{"unquoted-isolation-name", "&transaction_isolation=READ-COMMITTED", engine == "mariadb"},
		{"mysql-readonly-name", "&transaction_read_only=0", engine == "mariadb"},
		{"maria-readonly-name", "&tx_read_only=0", engine == "mysql"},
		{"mysql-default-collation", "&collation=utf8mb4_0900_ai_ci", engine == "mariadb"},
		{"maria-default-collation", "&collation=utf8mb4_uca1400_ai_ci", engine == "mysql"},
		{"session-mysql-collation", "&collation_connection=%27utf8mb4_0900_ai_ci%27", engine == "mariadb"},
		{"session-maria-collation", "&collation_connection=%27utf8mb4_uca1400_ai_ci%27", engine == "mysql"},
		{"session-unquoted-collation", "&collation_connection=utf8mb4_0900_ai_ci", engine == "mariadb"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := base
			m.Config += tc.extra
			db, info, err := dbconnect.Open(context.Background(), m, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			if err != nil {
				t.Fatal(err)
			}
			defer closeDB(t, db)
			if info.Type != engine {
				t.Fatalf("detected %s instead of %s", info.Type, engine)
			}
			// MariaDB versions differ in collation aliases: require a valid connection,
			// not a repair when that release already supports the requested setting.
			// Some MariaDB releases expose both legacy and modern aliases. A
			// repair is required only when the server actually lacks the name;
			// successful direct use is equally valid compatibility.
			pool, _ := db.DB()
			if pool.Stats().MaxOpenConnections != 7 {
				t.Fatal("pool tuning overwritten")
			}
			for i := 0; i < 3; i++ {
				conn, err := pool.Conn(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				var selected string
				if err = conn.QueryRowContext(context.Background(), "SELECT DATABASE()").Scan(&selected); err != nil || selected != m.Dbname {
					t.Fatalf("new pool connection failed: %v", err)
				}
				if strings.Contains(tc.name, "isolation") {
					variable := "transaction_isolation"
					if engine == "mariadb" {
						variable = "tx_isolation"
					}
					if err = conn.QueryRowContext(context.Background(), "SELECT @@session."+variable).Scan(&selected); err != nil || selected != "READ-COMMITTED" {
						t.Fatalf("new connection lost requested isolation: %q, %v", selected, err)
					}
				}
			}
			if err = db.Exec("CREATE TABLE IF NOT EXISTS compatibility_probe (id INT PRIMARY KEY, n INT NOT NULL)").Error; err != nil {
				t.Fatal(err)
			}
			if err = dbcompat.Exec(db, "INSERT INTO compatibility_probe VALUES (1, 1) ON DUPLICATE KEY UPDATE n=VALUES(n)", "INSERT INTO compatibility_probe VALUES (1, 1) AS _new_row ON DUPLICATE KEY UPDATE n=_new_row.n").Error; err != nil {
				t.Fatal(err)
			}
			if err = db.Transaction(func(tx *gorm.DB) error {
				return dbcompat.Exec(tx, "INSERT INTO compatibility_probe VALUES (1, 2) ON DUPLICATE KEY UPDATE n=VALUES(n)", "INSERT INTO compatibility_probe VALUES (1, 2) AS _new_row ON DUPLICATE KEY UPDATE n=_new_row.n").Error
			}); err != nil {
				t.Fatal(err)
			}
			t.Logf("actual=%s version=%s repairs=%v", info.Type, info.Version, info.Repairs)
		})
	}
}

func TestLiveInvalidConfigurationIsNotSilentlyIgnored(t *testing.T) {
	base, _ := liveSettings(t)
	baseline, _, err := dbconnect.Open(context.Background(), base, nil)
	if err != nil {
		t.Fatalf("negative tests require a working baseline connection: %v", err)
	}
	closeDB(t, baseline)
	for _, tc := range []struct{ name, extra string }{
		{"unknown-variable", "&ocv_nonexistent_option=1"},
		{"invalid-sql-mode", "&sql_mode=%27OCV_INVALID_MODE%27"},
		{"unknown-collation", "&collation_connection=%27ocv_nonexistent_collation%27"},
		{"conflicting-isolation", "&tx_isolation=%27READ-COMMITTED%27&transaction_isolation=%27SERIALIZABLE%27"},
		{"untrusted-tls", "&tls=true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := base
			m.Config += tc.extra
			db, _, err := dbconnect.Open(context.Background(), m, nil)
			if err == nil {
				closeDB(t, db)
				t.Fatal("unsafe or invalid config silently accepted")
			}
		})
	}
	t.Run("wrong-password", func(t *testing.T) {
		m := base
		m.Password += "incorrect"
		db, _, err := dbconnect.Open(context.Background(), m, nil)
		if err == nil {
			closeDB(t, db)
			t.Fatal("wrong password accepted")
		}
	})
	t.Run("missing-database-without-create", func(t *testing.T) {
		m := base
		m.Dbname = "ocv_missing_database"
		db, _, err := dbconnect.Open(context.Background(), m, nil)
		if err == nil {
			closeDB(t, db)
			t.Fatal("missing database silently changed")
		}
	})
	t.Run("cancelled-context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		db, _, err := dbconnect.Open(ctx, base, nil)
		if err == nil {
			closeDB(t, db)
			t.Fatal("cancellation ignored")
		}
	})
}

func TestLiveAutoCreateMissingDatabase(t *testing.T) {
	m, engine := liveSettings(t)
	m.Username, m.Password = "root", "ocv-disposable-test-root"
	m.Dbname, m.AutoCreate = "ocv_auto_created", true
	db, info, err := dbconnect.Open(context.Background(), m, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB(t, db)
	if info.Type != engine {
		t.Fatal("autocreate used wrong engine")
	}
	var name string
	if err = db.Raw("SELECT DATABASE()").Scan(&name).Error; err != nil || name != m.Dbname {
		t.Fatal("missing database was not created/selected")
	}
}

func TestLiveStartupInitializationAndLegacyReload(t *testing.T) {
	m, engine := liveSettings(t)
	oldConfig, oldLogger, oldVP, oldDB := global.GetAppConfig(), global.APP_LOG, global.APP_VP, global.APP_DB
	t.Cleanup(func() {
		global.SetAppConfig(oldConfig)
		global.APP_LOG = oldLogger
		global.APP_VP = oldVP
		global.APP_DB = oldDB
	})
	global.APP_LOG = zap.NewNop()
	for _, key := range []string{"DB_HOST", "DB_PORT", "DB_USER", "DB_PASSWORD", "DB_TYPE", "DB_NAME"} {
		t.Setenv(key, "")
	}
	for _, hint := range []string{"mysql", "mariadb"} {
		t.Run(hint, func(t *testing.T) {
			cfg := appconfig.Server{Mysql: appconfig.Mysql{Path: m.Path, Port: m.Port, Username: m.Username, Password: m.Password, Dbname: m.Dbname, Config: m.Config, MaxIdleConns: 3, MaxOpenConns: 7, MaxLifetime: 73}}
			cfg.System.DbType = hint
			global.SetAppConfig(cfg)
			db, err := initialize.GormMysqlConnect(m)
			if err != nil {
				t.Fatal(err)
			}
			closeDB(t, db)
			if global.GetAppConfig().System.DbType != engine {
				t.Fatal("startup trusted wrong engine label")
			}
			port, _ := strconv.Atoi(m.Port)
			request := model.DatabaseConfig{Type: hint, Host: m.Path, Port: port, Username: m.Username, Password: m.Password, Database: m.Dbname}
			info, err := (&system.InitService{}).DetectDatabaseConnection(request)
			if err != nil || info.Type != engine {
				t.Fatalf("initialization detection mismatch: %v", err)
			}
			for _, section := range []string{"mysql", "mariadb"} {
				t.Run(section, func(t *testing.T) {
					data, err := yaml.Marshal(map[string]interface{}{"system": map[string]string{"db-type": hint}, section: cfg.Mysql})
					if err != nil {
						t.Fatal(err)
					}
					path := filepath.Join(t.TempDir(), "custom-config.yml")
					if err = os.WriteFile(path, data, 0600); err != nil {
						t.Fatal(err)
					}
					v := viper.New()
					v.SetConfigFile(path)
					global.APP_VP = v
					if err = (&system.InitService{}).ReinitializeDatabase(); err != nil {
						t.Fatal(err)
					}
					if global.GetAppConfig().System.DbType != engine || global.GetAppConfig().Mysql.MaxOpenConns != 7 {
						t.Fatal("reload lost actual engine or advanced options")
					}
					closeDB(t, global.APP_DB)
					global.APP_DB = nil
					if err = (&system.InitService{}).UpdateDatabaseConfig(request); err != nil {
						t.Fatal(err)
					}
					if global.GetAppConfig().Mysql.MaxLifetime != 73 {
						t.Fatal("initialization save overwrote advanced settings")
					}
					if _, err = os.Stat(path + ".backup"); err != nil {
						t.Fatal(fmt.Errorf("custom config path was not used: %w", err))
					}
				})
			}
		})
	}
}
