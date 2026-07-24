package ipv6tunnel

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"

	providerModel "oneclickvirt/model/provider"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var tunnelTestDBSequence atomic.Uint64

func setupTunnelTestService(t *testing.T, executor remoteExecutor) (*Service, *gorm.DB) {
	t.Helper()
	dsn := fmt.Sprintf("file:ipv6tunnel_%d?mode=memory&cache=shared", tunnelTestDBSequence.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sqlite handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.Exec("CREATE TABLE providers (id integer PRIMARY KEY)").Error; err != nil {
		t.Fatalf("create providers: %v", err)
	}
	if err := db.AutoMigrate(&providerModel.ProviderIPv6Tunnel{}); err != nil {
		t.Fatalf("migrate tunnels: %v", err)
	}
	if err := db.Exec("INSERT INTO providers (id) VALUES (1)").Error; err != nil {
		t.Fatalf("insert provider: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return &Service{db: db, execute: executor}, db
}

func validTunnelConfig() Config {
	return Config{
		Name: "HE Los Angeles", Mode: "sit", Interface: "he-ipv6",
		LocalIPv4: "192.0.2.10", RemoteIPv4: "198.51.100.1",
		LocalIPv6: "2001:db8:0:1::2/64", RemoteIPv6: "2001:db8:0:1::1",
		RoutedCIDR: "2001:db8:1234:5678::10/80", MTU: 1480, TTL: 255,
		RouteMetric: 100, DefaultRoute: true,
	}
}

func TestNormalizeConfigCanonicalizesIPv6AndDefaults(t *testing.T) {
	config := validTunnelConfig()
	config.RoutedCIDR = "2001:db8:1234:5678:abcd::10/80"
	config.MTU = 0
	config.TTL = 0
	config.RouteMetric = 0

	normalized, err := normalizeConfig(config)
	if err != nil {
		t.Fatalf("normalizeConfig() error = %v", err)
	}
	if normalized.LocalIPv6 != "2001:db8:0:1::2/64" {
		t.Fatalf("LocalIPv6 = %q", normalized.LocalIPv6)
	}
	if normalized.RoutedCIDR != "2001:db8:1234:5678:abcd::/80" {
		t.Fatalf("RoutedCIDR = %q", normalized.RoutedCIDR)
	}
	if normalized.MTU != defaultMTU || normalized.TTL != defaultTTL || normalized.RouteMetric != defaultRouteMetric {
		t.Fatalf("defaults not applied: %#v", normalized)
	}
}

func TestNormalizeConfigRejectsCommandInjectionAndPollutedValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "newline", mutate: func(config *Config) { config.Interface = "he0\nip link delete eth0" }},
		{name: "ansi", mutate: func(config *Config) { config.Name = "HE\x1b[31m" }},
		{name: "shell metacharacter", mutate: func(config *Config) { config.Interface = "he0;reboot" }},
		{name: "IPv6 diagnostic output", mutate: func(config *Config) { config.LocalIPv6 = "inet6 2001:db8::2/64 scope global" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := validTunnelConfig()
			test.mutate(&config)
			if _, err := normalizeConfig(config); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestGeneratedTunnelCommandsPassShellSyntaxCheck(t *testing.T) {
	config, err := normalizeConfig(validTunnelConfig())
	if err != nil {
		t.Fatalf("normalizeConfig() error = %v", err)
	}
	tunnel := config.toModel(1)
	tunnel.ID = 42
	commands := map[string]string{
		"script":  renderTunnelScript(tunnel),
		"apply":   buildApplyCommand(tunnel),
		"disable": buildDisableCommand(tunnel),
		"delete":  buildDeleteCommand([]providerModel.ProviderIPv6Tunnel{tunnel}),
		"check":   buildCheckCommand([]providerModel.ProviderIPv6Tunnel{tunnel}),
	}
	for name, command := range commands {
		t.Run(name, func(t *testing.T) {
			if output, err := exec.Command("sh", "-n", "-c", command).CombinedOutput(); err != nil {
				t.Fatalf("shell syntax error: %v\n%s", err, output)
			}
		})
	}
}

func TestCreateEnabledTunnelUsesOneRemoteCallAndPersistsActive(t *testing.T) {
	var calls atomic.Int32
	var command string
	service, db := setupTunnelTestService(t, func(_ context.Context, providerID uint, remoteCommand string) (string, error) {
		calls.Add(1)
		if providerID != 1 {
			t.Fatalf("providerID = %d", providerID)
		}
		command = remoteCommand
		return "applied\n", nil
	})

	tunnel, err := service.Create(context.Background(), 1, CreateRequest{Config: validTunnelConfig(), Enabled: true})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("remote calls = %d, want 1", calls.Load())
	}
	if tunnel.Status != providerModel.IPv6TunnelStatusActive || !tunnel.Enabled {
		t.Fatalf("tunnel state = %#v", tunnel)
	}
	if !strings.Contains(command, "oneclickvirt-ipv6-tunnel-1.service") || strings.Contains(command, "systemctl restart networking") {
		t.Fatalf("unexpected apply command: %s", command)
	}
	if !strings.Contains(command, "refusing to replace an unmanaged network interface") {
		t.Fatal("apply command does not protect pre-existing unmanaged interfaces")
	}
	var stored providerModel.ProviderIPv6Tunnel
	if err := db.First(&stored, tunnel.ID).Error; err != nil {
		t.Fatalf("read tunnel: %v", err)
	}
	if stored.Status != providerModel.IPv6TunnelStatusActive {
		t.Fatalf("stored status = %q", stored.Status)
	}
}

func TestCheckAllUsesOneRemoteCallForMultipleTunnels(t *testing.T) {
	var calls atomic.Int32
	service, db := setupTunnelTestService(t, func(_ context.Context, _ uint, _ string) (string, error) {
		calls.Add(1)
		return "login banner\nTUNNEL|1|1|1|1|1|1\nTUNNEL|2|0|0|0|0|1\n", nil
	})
	first := validTunnelConfig().toModel(1)
	first.Enabled = true
	first.Status = providerModel.IPv6TunnelStatusPending
	secondConfig := validTunnelConfig()
	secondConfig.Name = "Disabled"
	secondConfig.Interface = "tb-ipv6"
	second := secondConfig.toModel(1)
	second.Enabled = false
	second.Status = providerModel.IPv6TunnelStatusPending
	if err := db.Create(&first).Error; err != nil {
		t.Fatalf("create first: %v", err)
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatalf("create second: %v", err)
	}

	tunnels, err := service.CheckAll(context.Background(), 1)
	if err != nil {
		t.Fatalf("CheckAll() error = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("remote calls = %d, want 1", calls.Load())
	}
	if len(tunnels) != 2 || tunnels[0].Status != providerModel.IPv6TunnelStatusActive || tunnels[1].Status != providerModel.IPv6TunnelStatusInactive {
		t.Fatalf("states = %#v", tunnels)
	}
	var stored []providerModel.ProviderIPv6Tunnel
	if err := db.Order("id ASC").Find(&stored).Error; err != nil {
		t.Fatalf("read batched states: %v", err)
	}
	if len(stored) != 2 || stored[0].Status != providerModel.IPv6TunnelStatusActive || stored[1].Status != providerModel.IPv6TunnelStatusInactive {
		t.Fatalf("batched stored states = %#v", stored)
	}
	if stored[0].LastCheckedAt == nil || stored[1].LastCheckedAt == nil {
		t.Fatalf("batched check timestamps were not stored: %#v", stored)
	}
}

func TestActiveUpdateFailureRestoresPreviousDatabaseConfig(t *testing.T) {
	var calls atomic.Int32
	service, db := setupTunnelTestService(t, func(_ context.Context, _ uint, _ string) (string, error) {
		if calls.Add(1) == 1 {
			return "applied", nil
		}
		return "", errors.New("systemctl start failed")
	})
	created, err := service.Create(context.Background(), 1, CreateRequest{Config: validTunnelConfig(), Enabled: true})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	updated := validTunnelConfig()
	updated.Name = "Replacement"
	updated.Interface = "he-new"
	result, err := service.Update(context.Background(), 1, created.ID, updated)
	if err == nil {
		t.Fatal("expected remote apply error")
	}
	if result.Interface != "he-ipv6" || result.Name != "HE Los Angeles" {
		t.Fatalf("returned config was not restored: %#v", result)
	}
	var stored providerModel.ProviderIPv6Tunnel
	if err := db.First(&stored, created.ID).Error; err != nil {
		t.Fatalf("read tunnel: %v", err)
	}
	if stored.Interface != "he-ipv6" || stored.Status != providerModel.IPv6TunnelStatusError || !strings.Contains(stored.LastError, "systemctl start failed") {
		t.Fatalf("stored tunnel = %#v", stored)
	}
}

func TestDeleteFailureKeepsControllerRecord(t *testing.T) {
	service, db := setupTunnelTestService(t, func(_ context.Context, _ uint, _ string) (string, error) {
		return "", errors.New("node offline")
	})
	tunnel := validTunnelConfig().toModel(1)
	tunnel.Status = providerModel.IPv6TunnelStatusInactive
	if err := db.Create(&tunnel).Error; err != nil {
		t.Fatalf("create tunnel: %v", err)
	}
	if err := service.Delete(context.Background(), 1, tunnel.ID); err == nil {
		t.Fatal("expected delete error")
	}
	var count int64
	if err := db.Model(&providerModel.ProviderIPv6Tunnel{}).Where("id = ?", tunnel.ID).Count(&count).Error; err != nil {
		t.Fatalf("count tunnel: %v", err)
	}
	if count != 1 {
		t.Fatalf("record count = %d, want 1", count)
	}
}

func TestCleanupProviderRemoteBatchesAllTunnels(t *testing.T) {
	var calls atomic.Int32
	var command string
	service, db := setupTunnelTestService(t, func(_ context.Context, providerID uint, remoteCommand string) (string, error) {
		calls.Add(1)
		if providerID != 1 {
			t.Fatalf("providerID = %d", providerID)
		}
		command = remoteCommand
		return "deleted\n", nil
	})

	first := validTunnelConfig().toModel(1)
	secondConfig := validTunnelConfig()
	secondConfig.Name = "Backup tunnel"
	secondConfig.Interface = "he-backup"
	second := secondConfig.toModel(1)
	if err := db.Create(&first).Error; err != nil {
		t.Fatalf("create first tunnel: %v", err)
	}
	if err := db.Create(&second).Error; err != nil {
		t.Fatalf("create second tunnel: %v", err)
	}

	if err := service.CleanupProviderRemote(context.Background(), 1, false); err != nil {
		t.Fatalf("CleanupProviderRemote() error = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("remote calls = %d, want 1", calls.Load())
	}
	for _, tunnel := range []providerModel.ProviderIPv6Tunnel{first, second} {
		if !strings.Contains(command, unitName(tunnel.ID)) || !strings.Contains(command, tunnel.Interface) {
			t.Fatalf("cleanup command does not include tunnel %#v: %s", tunnel, command)
		}
	}
	var count int64
	if err := db.Model(&providerModel.ProviderIPv6Tunnel{}).Where("provider_id = ?", 1).Count(&count).Error; err != nil {
		t.Fatalf("count tunnels: %v", err)
	}
	if count != 2 {
		t.Fatalf("controller rows = %d, want 2 for caller transaction", count)
	}
}

func TestCleanupProviderRemoteForceModeSkipsHost(t *testing.T) {
	var calls atomic.Int32
	service, db := setupTunnelTestService(t, func(_ context.Context, _ uint, _ string) (string, error) {
		calls.Add(1)
		return "", errors.New("remote executor must not run")
	})
	tunnel := validTunnelConfig().toModel(1)
	if err := db.Create(&tunnel).Error; err != nil {
		t.Fatalf("create tunnel: %v", err)
	}

	if err := service.CleanupProviderRemote(context.Background(), 1, true); err != nil {
		t.Fatalf("CleanupProviderRemote(force) error = %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("remote calls = %d, want 0", calls.Load())
	}
}
