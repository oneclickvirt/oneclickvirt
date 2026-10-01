package agent

import (
	"fmt"
	"testing"
	"time"

	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestReverseAgentMonitoringTokenFollowsInstallSecret(t *testing.T) {
	dsn := fmt.Sprintf("file:reverse_monitor_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&providerModel.Provider{}, &monitoringModel.MonitoringConfig{}); err != nil {
		t.Fatal(err)
	}
	provider := providerModel.Provider{
		UUID:           "reverse-agent-test",
		Name:           "reverse-agent-test",
		Type:           "docker",
		ConnectionType: "agent",
		AgentSecret:    "install-secret",
	}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatal(err)
	}
	config, err := GetMonitoringConfig(db, provider.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !config.AgentInstalled || config.AgentToken != provider.AgentSecret {
		t.Fatalf("new reverse config installed=%t token matches secret=%t", config.AgentInstalled, config.AgentToken == provider.AgentSecret)
	}
	if err := db.Model(config).Update("agent_token", "stale-generated-token").Error; err != nil {
		t.Fatal(err)
	}
	config, err = GetMonitoringConfig(db, provider.ID)
	if err != nil {
		t.Fatal(err)
	}
	if config.AgentToken != provider.AgentSecret {
		t.Fatal("existing reverse Agent token did not converge to installer secret")
	}
	var stored monitoringModel.MonitoringConfig
	if err := db.Where("provider_id = ?", provider.ID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.AgentToken != provider.AgentSecret {
		t.Fatal("converged reverse Agent token was not persisted")
	}
}
