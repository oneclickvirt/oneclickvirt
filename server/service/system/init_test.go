package system

import (
	"testing"

	appConfig "oneclickvirt/config"
	"oneclickvirt/global"
	configModel "oneclickvirt/model/config"
)

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
