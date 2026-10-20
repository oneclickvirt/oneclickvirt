package provider

import (
	"context"
	"encoding/json"
	"go.uber.org/zap"
	"oneclickvirt/constant"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	systemModel "oneclickvirt/model/system"
	"strings"
	"testing"
)

func TestQueuedNetworkChoiceRechecksRevokedGrantBeforeNodeAccess(t *testing.T) {
	db := finalizedSSHPortDB(t)
	if err := db.AutoMigrate(&providerModel.Provider{}, &systemModel.SystemImage{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE tasks (id INTEGER PRIMARY KEY, status TEXT, deleted_at DATETIME)").Error; err != nil {
		t.Fatal(err)
	}
	previousDB, previousLog := global.APP_DB, global.APP_LOG
	global.APP_DB, global.APP_LOG = db, zap.NewNop()
	t.Cleanup(func() { global.APP_DB, global.APP_LOG = previousDB, previousLog })
	node := providerModel.Provider{ID: 1, Name: "pve", Type: "proxmox", Status: "active", NetworkType: "nat_ipv4"}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	image := systemModel.SystemImage{ID: 1, Name: "debian", Status: "active", InstanceType: "container"}
	if err := db.Create(&image).Error; err != nil {
		t.Fatal(err)
	}
	req := adminModel.CreateInstanceTaskRequest{ProviderId: 1, ImageId: 1, NetworkType: "dedicated_ipv4_ipv6", CPUId: constant.PredefinedCPUSpecs[0].ID, MemoryId: constant.PredefinedMemorySpecs[0].ID, DiskId: constant.PredefinedDiskSpecs[0].ID, BandwidthId: constant.PredefinedBandwidthSpecs[0].ID}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	task := adminModel.Task{ID: 1, UserID: 1, TaskData: string(raw)}
	if err := db.Table("tasks").Create(map[string]interface{}{"id": 1, "status": "running"}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := NewService().prepareInstanceCreation(context.Background(), &task); err == nil || !strings.Contains(err.Error(), "未开放") {
		t.Fatalf("queued preparation bypassed revoked grant: %v", err)
	}
	instance := providerModel.Instance{ID: 1, ProviderID: 1, NetworkType: req.NetworkType}
	if err := NewService().executeProviderCreation(context.Background(), &task, &instance); err == nil || !strings.Contains(err.Error(), "未开放") {
		t.Fatalf("queued execution reached node after grant revoked: %v", err)
	}
}
