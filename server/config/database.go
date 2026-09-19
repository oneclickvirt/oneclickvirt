package config

import (
	"os"
	"strconv"
	"strings"

	model "oneclickvirt/model/config"
	"oneclickvirt/utils/dbconnect"

	"github.com/spf13/viper"
)

// DecodeDatabase handles both historical section names without merging two
// independently configured endpoints. The canonical mysql section wins if both
// exist, regardless of the engine hint; environment overrides are applied last.
func DecodeDatabase(v *viper.Viper, cfg *Server) error {
	if !v.InConfig("mysql") && v.InConfig("mariadb") {
		legacy := v.Sub("mariadb")
		if legacy != nil {
			if err := legacy.Unmarshal(&cfg.Mysql); err != nil {
				return err
			}
		}
	}
	for name, target := range map[string]*string{
		"DB_HOST": &cfg.Mysql.Path, "DB_PORT": &cfg.Mysql.Port,
		"DB_NAME": &cfg.Mysql.Dbname, "DB_USER": &cfg.Mysql.Username,
		"DB_PASSWORD": &cfg.Mysql.Password, "DB_TYPE": &cfg.System.DbType,
	} {
		if value, exists := os.LookupEnv(name); exists {
			value = NormalizeDatabaseEnvValue(name, value)
			// Empty optional deployment variables do not erase persisted config.
			// An intentionally empty password remains supported in YAML.
			if value != "" {
				*target = value
			}
		}
	}
	NormalizeDatabase(cfg)
	return nil
}

// NormalizeDatabaseType turns the user supplied engine hint into the stable
// value used by configuration, API requests and installers. The hint is only
// a protocol/configuration selector; the live server is still authoritative
// and is detected after connecting.
func NormalizeDatabaseType(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func IsSupportedDatabaseType(value string) bool {
	switch NormalizeDatabaseType(value) {
	case "mysql", "mariadb":
		return true
	default:
		return false
	}
}

func NormalizeDatabaseEnvValue(name, value string) string {
	if name == "DB_PASSWORD" {
		return value // whitespace and literal quotes are valid password bytes
	}
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		if value[0] == '"' && value[len(value)-1] == '"' {
			if unquoted, err := strconv.Unquote(value); err == nil {
				return strings.TrimSpace(unquoted)
			}
		}
		if value[0] == '\'' && value[len(value)-1] == '\'' {
			return strings.TrimSpace(value[1 : len(value)-1])
		}
	}
	return value
}

func NormalizeDatabase(cfg *Server) {
	cfg.System.DbType = NormalizeDatabaseType(cfg.System.DbType)
	if cfg.System.DbType != "mysql" && cfg.System.DbType != "mariadb" {
		cfg.System.DbType = "mysql" // wire protocol default; detection follows connect
	}
	for target, fallback := range map[*string]string{
		&cfg.Mysql.Path: "127.0.0.1", &cfg.Mysql.Port: "3306",
		&cfg.Mysql.Dbname: "oneclickvirt", &cfg.Mysql.Username: "root",
		&cfg.Mysql.Config: dbconnect.DefaultParams, &cfg.Mysql.Engine: "InnoDB",
	} {
		if strings.TrimSpace(*target) == "" {
			*target = fallback
		}
	}
	if cfg.Mysql.MaxIdleConns <= 0 {
		cfg.Mysql.MaxIdleConns = 20
	}
	if cfg.Mysql.MaxOpenConns <= 0 {
		cfg.Mysql.MaxOpenConns = 200
	}
	if cfg.Mysql.MaxLifetime <= 0 {
		cfg.Mysql.MaxLifetime = 1800
	}
}

func (m Mysql) ConnectionConfig() model.MysqlConfig {
	return model.MysqlConfig{
		Path: m.Path, Port: m.Port, Config: m.Config, Dbname: m.Dbname,
		Username: m.Username, Password: m.Password, MaxIdleConns: m.MaxIdleConns,
		MaxOpenConns: m.MaxOpenConns, LogMode: m.LogMode, LogZap: m.LogZap,
		MaxLifetime: m.MaxLifetime, AutoCreate: m.AutoCreate,
	}
}
