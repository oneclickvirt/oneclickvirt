package provider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"go.uber.org/zap"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
)

func TestQueuedRedemptionCopyRevalidatesTransportAndGPUBeforeNodeAccess(t *testing.T) {
	db := finalizedSSHPortDB(t)
	if err := db.AutoMigrate(&providerModel.Provider{}); err != nil {
		t.Fatal(err)
	}
	previousDB, previousLog := global.APP_DB, global.APP_LOG
	global.APP_DB, global.APP_LOG = db, zap.NewNop()
	t.Cleanup(func() { global.APP_DB, global.APP_LOG = previousDB, previousLog })
	for idx, tc := range []struct {
		rule       string
		gpuEnabled bool
		want       string
	}{
		{rule: "auto", want: "节点设置中启用GPU"},
		{rule: " API_ONLY ", gpuEnabled: true, want: "Agent 或 SSH"},
	} {
		node := providerModel.Provider{ID: uint(idx + 1), UUID: tc.rule, Name: tc.rule, Type: "lxd", ConnectionType: "agent", AgentStatus: "online", ExecutionRule: tc.rule, GpuEnabled: tc.gpuEnabled}
		if err := db.Create(&node).Error; err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(adminModel.CreateRedemptionInstanceTaskRequest{ProviderId: node.ID, CreationMode: "copy", SourceContainer: "template", GpuEnabled: true, GpuDeviceIds: "0"})
		if err != nil {
			t.Fatal(err)
		}
		task := adminModel.Task{ID: uint(idx + 1), TaskData: string(data)}
		instance := providerModel.Instance{ID: uint(idx + 1), ProviderID: node.ID, Name: "guest", InstanceType: "container"}
		err = NewService().executeProviderCreation(context.Background(), &task, &instance)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("queued task reached provider loading/creation despite changed node configuration: %v", err)
		}
	}
}
