package global

import (
	model "oneclickvirt/model/config"
	"strings"
)

// ReconcileDatabaseConfig publishes detection only while the probed endpoint and
// options are still current. A slow connection to A cannot overwrite a switch to
// B, nor unrelated concurrent configuration updates. No credentials go to logs.
func ReconcileDatabaseConfig(source model.MysqlConfig, engine, params string) {
	for {
		previous := APP_CONFIG.Load()
		if previous == nil {
			return
		}
		m := previous.Mysql
		if strings.TrimSpace(m.Path) != strings.TrimSpace(source.Path) ||
			strings.TrimSpace(m.Port) != strings.TrimSpace(source.Port) ||
			m.Dbname != source.Dbname || m.Username != source.Username ||
			m.Password != source.Password || m.Config != source.Config {
			return
		}
		next := *previous
		next.System.DbType = engine
		next.Mysql.Config = params
		if APP_CONFIG.CompareAndSwap(previous, &next) {
			return
		}
	}
}
