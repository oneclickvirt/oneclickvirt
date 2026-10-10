package redemption

import (
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
)

func TestRedemptionCopyRejectsUnsupportedConfigurationBeforeCreatingTasks(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "redemption.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&providerModel.Provider{}); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	previousDB, previousLog := global.APP_DB, global.APP_LOG
	global.APP_DB, global.APP_LOG = db, zap.NewNop()
	t.Cleanup(func() {
		global.APP_DB, global.APP_LOG = previousDB, previousLog
		_ = sqlDB.Close()
	})
	service := NewService(nil)
	for idx, tc := range []struct {
		platform, rule string
		gpuEnabled     bool
		requestGPU     bool
		want           string
	}{
		{platform: "lxd", rule: "auto", requestGPU: true, want: "节点设置中启用GPU"},
		{platform: "incus", rule: "auto", requestGPU: true, want: "节点设置中启用GPU"},
		{platform: "lxd", rule: " API_ONLY ", gpuEnabled: true, want: "Agent 或 SSH"},
		{platform: "incus", rule: "api_only", gpuEnabled: true, want: "Agent 或 SSH"},
	} {
		node := providerModel.Provider{ID: uint(idx + 1), UUID: tc.platform + tc.rule, Name: tc.platform + tc.rule, Type: tc.platform, Status: "active", ConnectionType: "agent", AgentStatus: "online", ExecutionRule: tc.rule, GpuEnabled: tc.gpuEnabled}
		if err := db.Create(&node).Error; err != nil {
			t.Fatal(err)
		}
		err := service.BatchCreate(adminModel.BatchCreateRedemptionCodesRequest{ProviderID: node.ID, CreationMode: "copy", SourceContainer: "template", Count: 2, GpuEnabled: tc.requestGPU}, 1, 0)
		// No tasks/reservations/code tables exist: a missing-table error would
		// reveal that invalid requests got past the creation preflight.
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s/%s: invalid copy reached task creation: %v", tc.platform, tc.rule, err)
		}
	}
}
