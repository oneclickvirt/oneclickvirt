package proxmox

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	systemModel "oneclickvirt/model/system"
	coreprovider "oneclickvirt/provider"

	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type proxmoxCreateCaptureTransport struct {
	createPayload map[string]interface{}
}

func (t *proxmoxCreateCaptureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/lxc") {
		if err := json.Unmarshal(body, &t.createPayload); err != nil {
			return nil, err
		}
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"data":"UPID:test"}`)),
		Request:    req,
	}, nil
}

func TestProxmoxTemplateVolumeIDUsesStorageVolumeSyntax(t *testing.T) {
	if got := proxmoxTemplateVolumeID("local", "debian_12_abcdef12.tar.xz"); got != "local:vztmpl/debian_12_abcdef12.tar.xz" {
		t.Fatalf("proxmoxTemplateVolumeID() = %q", got)
	}
	if got := proxmoxTemplateVolumeID("", "template.tar.xz"); got != "" {
		t.Fatalf("empty storage unexpectedly produced volume ID %q", got)
	}
}

func TestProxmoxTemplateVolumeIDRejectsPaths(t *testing.T) {
	for _, tc := range []struct {
		storage string
		name    string
	}{
		{storage: "local", name: "/var/lib/vz/template/cache/template.tar.xz"},
		{storage: "local", name: "../template.tar.xz"},
		{storage: "local:bad", name: "template.tar.xz"},
	} {
		if got := proxmoxTemplateVolumeID(tc.storage, tc.name); got != "" {
			t.Errorf("proxmoxTemplateVolumeID(%q, %q) = %q, want rejection", tc.storage, tc.name, got)
		}
	}
}

func TestAPIContainerSendsTemplateVolumeToProxmox(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "proxmox-template-api.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite database: %v", err)
	}
	if err := db.AutoMigrate(&providerModel.Provider{}, &systemModel.SystemImage{}); err != nil {
		t.Fatalf("migrate test tables: %v", err)
	}
	if err := db.Create(&providerModel.Provider{ID: 1, StoragePool: "local"}).Error; err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if err := db.Create(&systemModel.SystemImage{
		Name: "debian-12", URL: "https://example.test/debian-12.tar.xz",
		ProviderType: "proxmox", InstanceType: "container", Architecture: "amd64", Status: "active",
	}).Error; err != nil {
		t.Fatalf("seed system image: %v", err)
	}

	oldDB, oldLog := global.APP_DB, global.APP_LOG
	global.APP_DB = db
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() {
		global.APP_DB = oldDB
		global.APP_LOG = oldLog
	})

	provider := NewProxmoxProvider().(*ProxmoxProvider)
	provider.config = coreprovider.NodeConfig{
		ID:           1,
		Host:         "pve.test",
		Architecture: "amd64",
		TokenID:      "oneclickvirt@pve!token",
		Token:        "secret",
	}
	provider.node = "pve-node"
	provider.sshClient.SetExecutor(&ipv6CommandExecutor{output: func(command string) string {
		if strings.Contains(command, "[ -f ") {
			return "exists"
		}
		return ""
	}})
	capture := &proxmoxCreateCaptureTransport{}
	provider.apiClient = &http.Client{Transport: capture}

	config := coreprovider.InstanceConfig{
		Name:         "api-template-test",
		Image:        "debian-12",
		ImageURL:     "https://example.test/debian-12.tar.xz",
		InstanceType: "container",
		CPU:          "1",
		Memory:       "512",
		Disk:         "8",
	}
	if err := provider.apiCreateContainer(context.Background(), 100, config, func(int, string) {}); err != nil {
		t.Fatalf("apiCreateContainer() error = %v", err)
	}
	expectedTemplate := "local:vztmpl/" + provider.generateRemoteFileName(config.Image, config.ImageURL, provider.config.Architecture)
	if got := capture.createPayload["ostemplate"]; got != expectedTemplate {
		t.Fatalf("ostemplate = %v, want a local:vztmpl volume", got)
	}
	if got := capture.createPayload["storage"]; got != "local" {
		t.Fatalf("template storage = %v, want local", got)
	}
	if got, ok := capture.createPayload["ostemplate"].(string); !ok || strings.HasPrefix(got, "/") {
		t.Fatalf("ostemplate = %v, must not be an absolute path", got)
	}
}
