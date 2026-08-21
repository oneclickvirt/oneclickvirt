package ipv6tunnel

import (
	"context"
	"errors"
	"strings"
	"testing"

	providerModel "oneclickvirt/model/provider"
	ipv6poolService "oneclickvirt/service/ipv6pool"

	"gorm.io/gorm"
)

func TestDeleteFreezesTunnelPoolWithoutMySQLSelfReference(t *testing.T) {
	service, db := setupTunnelTestService(t, func(_ context.Context, _ uint, _ string) (string, error) {
		return "", errors.New("node offline")
	})

	var poolUpdates []string
	if err := db.Callback().Update().After("gorm:update").Register("test:capture_tunnel_pool_updates", func(tx *gorm.DB) {
		if tx.Statement.Table == "provider_ipv6_pools" {
			poolUpdates = append(poolUpdates, tx.Dialector.Explain(tx.Statement.SQL.String(), tx.Statement.Vars...))
		}
	}); err != nil {
		t.Fatalf("register update SQL capture: %v", err)
	}

	tunnel := validTunnelConfig().toModel(1)
	tunnel.Enabled = true
	tunnel.Status = providerModel.IPv6TunnelStatusActive
	if err := db.Create(&tunnel).Error; err != nil {
		t.Fatalf("create tunnel: %v", err)
	}
	parent := providerModel.ProviderIPv6Pool{
		ProviderID: 1, Address: "2001:db8:1234:5678::/80", PrefixLength: 80,
		IsRange: true, RangeNext: "2001:db8:1234:5678::2", TunnelID: &tunnel.ID,
		Source: ipv6poolService.SourceTunnel,
	}
	if err := db.Create(&parent).Error; err != nil {
		t.Fatalf("create tunnel pool parent: %v", err)
	}
	child := providerModel.ProviderIPv6Pool{
		ProviderID: 1, Address: "2001:db8:1234:5678::2", PrefixLength: 128,
		ParentID: &parent.ID, Source: ipv6poolService.SourceRangeChild,
	}
	if err := db.Create(&child).Error; err != nil {
		t.Fatalf("create tunnel pool child: %v", err)
	}

	if err := service.Delete(context.Background(), 1, tunnel.ID); err == nil {
		t.Fatal("expected remote delete error")
	}
	if len(poolUpdates) != 1 {
		t.Fatalf("pool freeze updates = %d, want 1: %#v", len(poolUpdates), poolUpdates)
	}
	if strings.Contains(strings.ToUpper(poolUpdates[0]), "SELECT") {
		t.Fatalf("pool freeze generated a MySQL 1093 self-referencing update: %s", poolUpdates[0])
	}

	for _, id := range []uint{parent.ID, child.ID} {
		var pool providerModel.ProviderIPv6Pool
		if err := db.First(&pool, id).Error; err != nil {
			t.Fatalf("load frozen pool row %d: %v", id, err)
		}
		if !pool.PendingRetire {
			t.Fatalf("pool row %d remains allocatable after failed host cleanup: %#v", id, pool)
		}
	}
}
