package agent

import (
	"testing"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"

	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAgentInfoPersistsWithinHeartbeatThrottleWindow(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Exec(`CREATE TABLE providers (
		id integer primary key, deleted_at datetime, updated_at datetime,
		connection_type text, endpoint text, agent_status text, agent_last_seen datetime,
		agent_remote_ip text, agent_hostname text, agent_version text
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO providers (id, connection_type, endpoint) VALUES (1, 'agent', '')`).Error; err != nil {
		t.Fatal(err)
	}
	oldDB, oldLog := global.APP_DB, global.APP_LOG
	global.APP_DB, global.APP_LOG = db, zap.NewNop()
	t.Cleanup(func() { global.APP_DB, global.APP_LOG = oldDB, oldLog })
	updates := 0
	if err := db.Callback().Update().After("gorm:update").Register("count_agent_status_updates", func(tx *gorm.DB) {
		updates++
	}); err != nil {
		t.Fatal(err)
	}
	hub := &AgentHub{statusPersistMemo: make(map[uint]agentStatusPersistState)}
	now := time.Now()
	remote := "[2001:db8::1]:23456"
	hub.updateProviderAgentStatus(1, "online", &now, remote, "")
	hub.updateProviderAgentStatusWithVersion(1, "online", &now, remote, "node-one", "0.4.0")
	var provider providerModel.Provider
	if err := db.First(&provider, 1).Error; err != nil {
		t.Fatal(err)
	}
	if provider.AgentVersion != "0.4.0" || provider.AgentHostname != "node-one" || provider.Endpoint != "2001:db8::1" {
		t.Fatalf("first info frame was lost to heartbeat throttling: %#v", provider)
	}
	updatesAfterInfo := updates
	for i := 0; i < 10; i++ {
		hub.updateProviderAgentStatus(1, "online", &now, remote, "")
		hub.updateProviderAgentStatusWithVersion(1, "online", &now, remote, "node-one", "0.4.0")
	}
	if updates != updatesAfterInfo {
		t.Fatalf("unchanged heartbeats/info bypassed throttling: %d -> %d writes", updatesAfterInfo, updates)
	}
	hub.updateProviderAgentStatusWithVersion(1, "online", &now, remote, "node-renamed", "0.5.0")
	if err := db.First(&provider, 1).Error; err != nil {
		t.Fatal(err)
	}
	if provider.AgentVersion != "0.5.0" || provider.AgentHostname != "node-renamed" {
		t.Fatal("changed Agent metadata was not persisted immediately")
	}
	hub.updateProviderAgentStatus(1, "offline", nil, "", "")
	if err := db.First(&provider, 1).Error; err != nil {
		t.Fatal(err)
	}
	if provider.AgentStatus != "offline" || provider.AgentVersion != "0.5.0" {
		t.Fatal("offline transition was throttled or erased known Agent metadata")
	}
}
