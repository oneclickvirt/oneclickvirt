// Package dbconnect is the common connection path for startup, recovery and the
// initialization API. A configured engine label is a hint, never a dialect switch.
package dbconnect

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/model/config"

	driver "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

const DefaultParams = "charset=utf8mb4&parseTime=True&loc=Asia%2FShanghai&time_zone=%27%2B08%3A00%27"

type Info struct {
	Type    string   `json:"type"`
	Version string   `json:"version"`
	Repairs []string `json:"repairs,omitempty"`
	Params  string   `json:"-"`
}

// DriverConfig uses the driver's parser only for options. Credentials and IPv6
// addresses must not be interpolated into a DSN and re-parsed as syntax.
func DriverConfig(m config.MysqlConfig) (*driver.Config, error) {
	if m.Username == "" {
		return nil, fmt.Errorf("数据库用户名不能为空")
	}
	host := strings.TrimSpace(m.Path)
	if host == "" {
		host = "127.0.0.1"
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}
	if host == "" || strings.ContainsAny(host, " \t\r\n/?#@()[]") {
		return nil, fmt.Errorf("数据库地址必须是主机名或 IP，不包含协议和端口")
	}
	port := strings.TrimSpace(m.Port)
	if port == "" {
		port = "3306"
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return nil, fmt.Errorf("数据库端口必须在 1-65535 范围内")
	}
	params := m.Config
	if strings.TrimSpace(params) == "" {
		params = DefaultParams
	}
	// Reject malformed escapes/duplicates instead of silently losing options.
	values, err := url.ParseQuery(params)
	if err != nil {
		return nil, fmt.Errorf("数据库高级连接参数格式错误")
	}
	for key, value := range values {
		if len(value) != 1 {
			return nil, fmt.Errorf("数据库连接参数 %q 重复", key)
		}
	}
	// Parse the real address so automatic TLS ServerName verification is bound
	// to this host, not a placeholder. Credentials are still assigned separately.
	address := net.JoinHostPort(host, port)
	cfg, err := driver.ParseDSN("tcp(" + address + ")/?" + params)
	if err != nil {
		// Driver parser errors may contain option values (including TLS secrets).
		return nil, fmt.Errorf("数据库高级连接参数无效，请检查 TLS、时区和超时设置")
	}
	cfg.User, cfg.Passwd = m.Username, m.Password
	cfg.Net, cfg.Addr = "tcp", address
	cfg.DBName = m.Dbname
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	return cfg, nil
}

func openSQL(cfg *driver.Config) (*sql.DB, error) {
	connector, err := driver.NewConnector(cfg)
	if err != nil {
		return nil, fmt.Errorf("数据库连接参数无效")
	}
	return sql.OpenDB(connector), nil
}

// GORM's version probe uses context.Background internally. Bound just that
// initialization query, without imposing a short read timeout on long runtime
// queries or leaving a canceled startup context attached to the returned pool.
type initializationPool struct {
	*sql.DB
	ctx context.Context
}

func (p *initializationPool) QueryRowContext(_ context.Context, query string, args ...interface{}) *sql.Row {
	return p.DB.QueryRowContext(p.ctx, query, args...)
}

// Open detects the actual server using a short-lived, read-only probe. Only
// session parameters are withheld from that probe; TLS and authentication stay
// unchanged. The final pool MUST apply every configured (or explicitly repaired)
// parameter successfully before it is returned. No query-time detection / N+1.
func Open(ctx context.Context, m config.MysqlConfig, gormConfig *gorm.Config) (*gorm.DB, Info, error) {
	info := Info{Params: m.Config}
	if strings.TrimSpace(info.Params) == "" {
		info.Params = DefaultParams
	}
	if m.Dbname == "" && m.AutoCreate {
		m.Dbname = "oneclickvirt"
	}
	cfg, err := DriverConfig(m)
	if err != nil {
		return nil, info, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	probeConfig := cfg.Clone()
	probeConfig.DBName, probeConfig.Collation, probeConfig.Params = "", "", nil
	probe, err := openSQL(probeConfig)
	if err != nil {
		return nil, info, err
	}
	defer probe.Close()
	probe.SetMaxOpenConns(1)
	if err = probe.QueryRowContext(ctx, "SELECT VERSION()").Scan(&info.Version); err != nil {
		return nil, info, fmt.Errorf("检测数据库服务端失败: %w", err)
	}
	info.Type = "mysql"
	if strings.Contains(strings.ToLower(info.Version), "mariadb") {
		info.Type = "mariadb"
	}
	if err = repairOptions(ctx, probe, cfg, &info); err != nil {
		return nil, info, err
	}
	pool, err := openSQL(cfg)
	if err != nil {
		return nil, info, err
	}
	keep := false
	defer func() {
		if !keep {
			pool.Close()
		}
	}()
	err = pool.PingContext(ctx)
	if dbErr, ok := err.(*driver.MySQLError); ok && dbErr.Number == 1049 && m.AutoCreate {
		// A missing database is the only reason to attempt DDL. Never recreate or
		// delete data, and do not swallow permissions/authentication errors.
		if !regexp.MustCompile(`^[a-zA-Z0-9_]{1,64}$`).MatchString(m.Dbname) {
			return nil, info, fmt.Errorf("自动创建的数据库名称只允许 1-64 位字母、数字和下划线")
		}
		_, err = probe.ExecContext(ctx, "CREATE DATABASE IF NOT EXISTS `"+m.Dbname+"` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci")
		if err == nil {
			err = pool.PingContext(ctx)
		}
	}
	if err != nil {
		return nil, info, fmt.Errorf("应用数据库连接配置失败: %w", err)
	}
	if gormConfig == nil {
		gormConfig = &gorm.Config{}
	}
	ormOptions := *gormConfig
	ormOptions.DisableAutomaticPing = true // already verified with PingContext
	dialect := mysql.New(mysql.Config{
		Conn: &initializationPool{DB: pool, ctx: ctx}, ServerVersion: info.Version, DefaultStringSize: 191,
	})
	db, err := gorm.Open(dialect, &ormOptions)
	if err != nil {
		return nil, info, fmt.Errorf("初始化数据库方言失败: %w", err)
	}
	// Not yet published, so replacing the initialization-only wrapper is safe.
	db.ConnPool = pool
	db.Statement.ConnPool = pool
	dialect.(*mysql.Dialector).Conn = pool
	maxIdle, maxOpen, lifetime := m.MaxIdleConns, m.MaxOpenConns, m.MaxLifetime
	if maxIdle <= 0 {
		maxIdle = 20
	}
	if maxOpen <= 0 {
		maxOpen = 200
	}
	if lifetime <= 0 {
		lifetime = 1800
	}
	pool.SetMaxOpenConns(maxOpen)
	pool.SetMaxIdleConns(maxIdle)
	pool.SetConnMaxLifetime(time.Duration(lifetime) * time.Second)
	pool.SetConnMaxIdleTime(10 * time.Minute)
	keep = true
	return db, info, nil
}

func repairOptions(ctx context.Context, probe *sql.DB, cfg *driver.Config, info *Info) error {
	params, _ := url.ParseQuery(info.Params) // validated by DriverConfig
	for _, pair := range [][2]string{{"transaction_isolation", "tx_isolation"}, {"transaction_read_only", "tx_read_only"}} {
		if a, ok := cfg.Params[pair[0]]; ok {
			if b, exists := cfg.Params[pair[1]]; exists && a != b {
				return fmt.Errorf("数据库参数 %s 与 %s 冲突", pair[0], pair[1])
			}
		}
		for i, name := range pair {
			value, configured := cfg.Params[name]
			if !configured {
				continue
			}
			var found, setting string
			// SHOW placeholders are not supported by MariaDB's prepared protocol.
			// Names come exclusively from the fixed alias pairs above, never input.
			err := probe.QueryRowContext(ctx, "SHOW SESSION VARIABLES LIKE '"+name+"'").Scan(&found, &setting)
			if err == nil {
				continue
			}
			if err != sql.ErrNoRows {
				return fmt.Errorf("检测数据库会话参数失败: %w", err)
			}
			alias := pair[1-i]
			if err = probe.QueryRowContext(ctx, "SHOW SESSION VARIABLES LIKE '"+alias+"'").Scan(&found, &setting); err != nil {
				return fmt.Errorf("服务端不支持数据库会话参数 %s", name)
			}
			if other, exists := cfg.Params[alias]; exists && other != value {
				return fmt.Errorf("数据库参数 %s 与 %s 冲突", name, alias)
			}
			delete(cfg.Params, name)
			cfg.Params[alias] = value
			params.Del(name)
			params.Set(alias, value)
			info.Repairs = append(info.Repairs, name+" -> "+alias)
		}
		// The driver emits unknown DSN parameters as `SET name = value`.
		// Isolation names are SQL strings, so accept the common unquoted form
		// from hand-written config files and quote it before every pooled
		// connection is opened. Numeric read-only values remain unchanged.
		for _, name := range pair {
			value, configured := cfg.Params[name]
			if !configured {
				continue
			}
			quoted := quoteKnownSessionValue(name, value)
			if quoted == value {
				continue
			}
			cfg.Params[name] = quoted
			params.Set(name, quoted)
			info.Repairs = append(info.Repairs, name+" quoted")
		}
	}
	// Only known engine-default, case-insensitive UTF8MB4 collations are mapped.
	// Unknown collations, SQL modes, TLS and auth options are never discarded.
	for _, key := range []string{"collation", "collation_connection"} {
		value := params.Get(key)
		name := strings.Trim(value, "'\"")
		switch name {
		case "utf8mb4_0900_ai_ci", "utf8mb4_uca1400_ai_ci", "uca1400_ai_ci":
		default:
			continue
		}
		var count int
		if err := probe.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.COLLATIONS WHERE COLLATION_NAME = ?", name).Scan(&count); err != nil {
			return fmt.Errorf("检测数据库字符排序规则失败: %w", err)
		}
		supported := count != 0
		// MariaDB's UCA aliases need not appear in information_schema. Verify on
		// the disposable session before replacing a setting the server supports.
		if !supported {
			if _, err := probe.ExecContext(ctx, "SET collation_connection = ?", name); err == nil {
				supported = true
			} else {
				var dbErr *driver.MySQLError
				if !errors.As(err, &dbErr) || dbErr.Number != 1273 {
					return fmt.Errorf("验证数据库排序规则失败: %w", err)
				}
			}
		}
		if supported {
			if key == "collation_connection" {
				quoted := quoteKnownSessionValue(key, params.Get(key))
				if quoted != params.Get(key) {
					cfg.Params[key] = quoted
					params.Set(key, quoted)
					info.Repairs = append(info.Repairs, key+" quoted")
				}
			}
			if key == "collation" && strings.Contains(name, "uca1400") {
				// The server supports this MariaDB alias, but the Go driver's
				// handshake collation table does not. Apply it as a session option
				// on EVERY pooled connection instead of changing its semantics.
				if existing := params.Get("collation_connection"); existing != "" && strings.Trim(existing, "'\"") != name {
					return fmt.Errorf("collation 与 collation_connection 配置冲突")
				}
				cfg.Collation = ""
				if cfg.Params == nil {
					cfg.Params = make(map[string]string)
				}
				cfg.Params["collation_connection"] = "'" + name + "'"
				params.Del("collation")
				params.Set("collation_connection", "'"+name+"'")
				info.Repairs = append(info.Repairs, "collation -> collation_connection ("+name+")")
			}
			continue
		}
		replacement := "utf8mb4_unicode_ci"
		if key == "collation" {
			cfg.Collation = replacement
		} else {
			replacement = "'" + replacement + "'"
			cfg.Params[key] = replacement
		}
		params.Set(key, replacement)
		info.Repairs = append(info.Repairs, key+": "+name+" -> utf8mb4_unicode_ci")
	}
	if len(info.Repairs) != 0 {
		info.Params = params.Encode()
	}
	return nil
}

// quoteKnownSessionValue handles only identifier-shaped values for the small
// set of known string session settings. Unknown options are deliberately left
// to the driver's/server's validation path and are never silently transformed.
func quoteKnownSessionValue(name, value string) string {
	if name != "collation_connection" && name != "transaction_isolation" && name != "tx_isolation" {
		return value
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || strings.HasPrefix(trimmed, "'") || strings.HasPrefix(trimmed, "\"") {
		return value
	}
	if !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`).MatchString(trimmed) {
		return value
	}
	return "'" + trimmed + "'"
}
