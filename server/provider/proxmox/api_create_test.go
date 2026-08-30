package proxmox

import (
	"context"
	"encoding/json"
	"fmt"
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
	createPayload       map[string]interface{}
	networkPayload      map[string]interface{}
	requestOrder        []string
	createTaskStatuses  []proxmoxAPITaskStatus
	configTaskStatuses  []proxmoxAPITaskStatus
	configResponseCodes []int
	createReturnsSync   bool
	configReturnsTask   bool
	configCalls         int
}

func (t *proxmoxCreateCaptureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	var err error
	if req.Body != nil {
		body, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
	}

	response := func(status int, payload string) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(payload)),
			Request:    req,
		}, nil
	}

	if req.Method == http.MethodPost && (strings.HasSuffix(req.URL.Path, "/lxc") || strings.HasSuffix(req.URL.Path, "/qemu")) {
		if err := json.Unmarshal(body, &t.createPayload); err != nil {
			return nil, err
		}
		t.requestOrder = append(t.requestOrder, "create")
		if t.createReturnsSync {
			return response(http.StatusOK, `{"data":null}`)
		}
		return response(http.StatusOK, `{"data":"UPID:create"}`)
	}
	if req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/tasks/UPID:create/status") {
		t.requestOrder = append(t.requestOrder, "create-status")
		return response(http.StatusOK, proxmoxTaskStatusJSON(nextTaskStatus(&t.createTaskStatuses)))
	}
	if req.Method == http.MethodPut && strings.HasSuffix(req.URL.Path, "/config") {
		if err := json.Unmarshal(body, &t.networkPayload); err != nil {
			return nil, err
		}
		t.requestOrder = append(t.requestOrder, "config")
		t.configCalls++
		responseCode := http.StatusOK
		if len(t.configResponseCodes) > 0 {
			responseCode = t.configResponseCodes[0]
			t.configResponseCodes = t.configResponseCodes[1:]
		}
		if responseCode != http.StatusOK {
			return response(responseCode, `{"data":null,"message":"can't lock file '/run/lock/lxc/pve-config-100.lock' - got timeout"}`)
		}
		if t.configReturnsTask {
			return response(http.StatusOK, `{"data":"UPID:config"}`)
		}
		return response(http.StatusOK, `{"data":null}`)
	}
	if req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/tasks/UPID:config/status") {
		t.requestOrder = append(t.requestOrder, "config-status")
		return response(http.StatusOK, proxmoxTaskStatusJSON(nextTaskStatus(&t.configTaskStatuses)))
	}
	return response(http.StatusNotFound, `{"data":null,"message":"unexpected request"}`)
}

func nextTaskStatus(statuses *[]proxmoxAPITaskStatus) proxmoxAPITaskStatus {
	if len(*statuses) == 0 {
		return proxmoxAPITaskStatus{Status: "stopped", ExitStatus: "OK"}
	}
	status := (*statuses)[0]
	*statuses = (*statuses)[1:]
	return status
}

func proxmoxTaskStatusJSON(status proxmoxAPITaskStatus) string {
	payload, _ := json.Marshal(map[string]interface{}{
		"data": map[string]string{
			"status":     status.Status,
			"exitstatus": status.ExitStatus,
		},
	})
	return string(payload)
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

func TestAPIContainerEmbedsBaseNetworkInCreateRequest(t *testing.T) {
	db, oldDB, oldLog := newProxmoxAPITestDB(t)
	global.APP_DB = db
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() {
		global.APP_DB = oldDB
		global.APP_LOG = oldLog
	})
	if err := db.Create(&providerModel.Provider{ID: 1, StoragePool: "local"}).Error; err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if err := db.Create(&systemModel.SystemImage{
		Name: "debian-12", URL: "https://example.test/debian-12.tar.xz",
		ProviderType: "proxmox", InstanceType: "container", Architecture: "amd64", Status: "active",
	}).Error; err != nil {
		t.Fatalf("seed system image: %v", err)
	}

	provider := NewProxmoxProvider().(*ProxmoxProvider)
	provider.config = coreprovider.NodeConfig{ID: 1, Host: "pve.test", Architecture: "amd64"}
	provider.initBridgeNames(provider.config)
	provider.node = "pve-node"
	provider.sshClient.SetExecutor(&ipv6CommandExecutor{output: func(command string) string {
		if strings.Contains(command, "[ -f ") {
			return "exists"
		}
		return ""
	}})
	capture := &proxmoxCreateCaptureTransport{
		createTaskStatuses: []proxmoxAPITaskStatus{
			{Status: "running"},
			{Status: "stopped", ExitStatus: "OK"},
		},
	}
	provider.apiClient = &http.Client{Transport: capture}

	oldPoll := proxmoxAPITaskPollInterval
	proxmoxAPITaskPollInterval = 0
	t.Cleanup(func() { proxmoxAPITaskPollInterval = oldPoll })

	config := coreprovider.InstanceConfig{
		Name:         "wait-for-create-task",
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
	wantOrder := []string{"create", "create-status", "create-status"}
	if strings.Join(capture.requestOrder, ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("request order = %v, want %v", capture.requestOrder, wantOrder)
	}
	net0, ok := capture.createPayload["net0"].(string)
	if !ok || !strings.Contains(net0, "name=eth0,ip=172.16.1.2/24,bridge=vmbr1,gw=172.16.1.1") {
		t.Fatalf("create net0 = %v, want an embedded vmbr1 NAT configuration", capture.createPayload["net0"])
	}
	if capture.configCalls != 0 {
		t.Fatalf("create path sent %d post-create config mutation(s), want none", capture.configCalls)
	}
}

func TestProxmoxCreateAcceptsSynchronousSuccessResponse(t *testing.T) {
	oldLog := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = oldLog })

	provider := NewProxmoxProvider().(*ProxmoxProvider)
	provider.config = coreprovider.NodeConfig{Host: "pve.test"}
	provider.node = "pve-node"
	capture := &proxmoxCreateCaptureTransport{createReturnsSync: true}
	provider.apiClient = &http.Client{Transport: capture}

	upid, err := provider.submitProxmoxAPITask(
		context.Background(),
		http.MethodPost,
		"https://pve.test:8006/api2/json/nodes/pve-node/lxc",
		[]byte(`{"vmid":100}`),
	)
	if err != nil {
		t.Fatalf("submitProxmoxAPITask() error = %v", err)
	}
	if upid != "" {
		t.Fatalf("synchronous create returned UPID %q", upid)
	}
	if strings.Join(capture.requestOrder, ",") != "create" {
		t.Fatalf("request order = %v, want only create request", capture.requestOrder)
	}
}

func TestProxmoxQEMUCreateTaskWaitsBeforeFollowup(t *testing.T) {
	oldLog := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = oldLog })

	provider := NewProxmoxProvider().(*ProxmoxProvider)
	provider.config = coreprovider.NodeConfig{Host: "pve.test"}
	provider.node = "pve-node"
	capture := &proxmoxCreateCaptureTransport{
		createTaskStatuses: []proxmoxAPITaskStatus{
			{Status: "running"},
			{Status: "stopped", ExitStatus: "OK"},
		},
	}
	provider.apiClient = &http.Client{Transport: capture}

	oldPoll := proxmoxAPITaskPollInterval
	proxmoxAPITaskPollInterval = 0
	t.Cleanup(func() { proxmoxAPITaskPollInterval = oldPoll })

	upid, err := provider.submitProxmoxAPITask(
		context.Background(),
		http.MethodPost,
		"https://pve.test:8006/api2/json/nodes/pve-node/qemu",
		[]byte(`{"vmid":100}`),
	)
	if err != nil {
		t.Fatalf("submitProxmoxAPITask() error = %v", err)
	}
	if err := provider.waitForProxmoxAPITask(context.Background(), upid, "创建虚拟机"); err != nil {
		t.Fatalf("waitForProxmoxAPITask() error = %v", err)
	}
	wantOrder := []string{"create", "create-status", "create-status"}
	if strings.Join(capture.requestOrder, ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("request order = %v, want %v", capture.requestOrder, wantOrder)
	}
}

func TestProxmoxConfigLockErrorRecognizesLXCAndQEMUPaths(t *testing.T) {
	for _, message := range []string{
		"can't lock file '/run/lock/lxc/pve-config-100.lock' - got timeout",
		"can't lock file '/var/lock/qemu-server/lock-100.conf' - got timeout",
		"cannot lock file '/run/lock/qemu-server/lock-100.conf': timed out",
	} {
		if !isProxmoxConfigLockError(fmt.Errorf("%s", message)) {
			t.Errorf("isProxmoxConfigLockError(%q) = false", message)
		}
	}
	for _, message := range []string{
		"can't lock file '/var/lock/qemu-server/lock-100.conf': permission denied",
		"PVE validation failed",
	} {
		if isProxmoxConfigLockError(fmt.Errorf("%s", message)) {
			t.Errorf("isProxmoxConfigLockError(%q) = true, want false", message)
		}
	}
}

func TestProxmoxConfigTaskWaitsWhenPVESendsUPID(t *testing.T) {
	oldLog := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = oldLog })

	provider := NewProxmoxProvider().(*ProxmoxProvider)
	provider.config = coreprovider.NodeConfig{Host: "pve.test"}
	provider.node = "pve-node"
	capture := &proxmoxCreateCaptureTransport{
		configReturnsTask: true,
		configTaskStatuses: []proxmoxAPITaskStatus{
			{Status: "running"},
			{Status: "stopped", ExitStatus: "OK"},
		},
	}
	provider.apiClient = &http.Client{Transport: capture}

	oldPoll := proxmoxAPITaskPollInterval
	proxmoxAPITaskPollInterval = 0
	t.Cleanup(func() { proxmoxAPITaskPollInterval = oldPoll })

	if err := provider.submitProxmoxConfigTaskWithRetry(
		context.Background(),
		"https://pve.test:8006/api2/json/nodes/pve-node/lxc/100/config",
		[]byte(`{"net0":"name=eth0"}`),
		"配置容器网络",
	); err != nil {
		t.Fatalf("submitProxmoxConfigTaskWithRetry() error = %v", err)
	}
	wantOrder := []string{"config", "config-status", "config-status"}
	if strings.Join(capture.requestOrder, ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("request order = %v, want %v", capture.requestOrder, wantOrder)
	}
}

func TestProxmoxConfigTaskRetriesLockReportedByAsyncTask(t *testing.T) {
	oldLog := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = oldLog })

	provider := NewProxmoxProvider().(*ProxmoxProvider)
	provider.config = coreprovider.NodeConfig{Host: "pve.test"}
	provider.node = "pve-node"
	capture := &proxmoxCreateCaptureTransport{
		configReturnsTask: true,
		configTaskStatuses: []proxmoxAPITaskStatus{
			{Status: "stopped", ExitStatus: "can't lock file '/run/lock/lxc/pve-config-100.lock' - got timeout"},
			{Status: "stopped", ExitStatus: "OK"},
		},
	}
	provider.apiClient = &http.Client{Transport: capture}

	oldDelay := proxmoxAPIConfigLockRetryDelay
	proxmoxAPIConfigLockRetryDelay = 0
	t.Cleanup(func() { proxmoxAPIConfigLockRetryDelay = oldDelay })

	if err := provider.submitProxmoxConfigTaskWithRetry(
		context.Background(),
		"https://pve.test:8006/api2/json/nodes/pve-node/lxc/100/config",
		[]byte(`{"net0":"name=eth0"}`),
		"配置容器网络",
	); err != nil {
		t.Fatalf("submitProxmoxConfigTaskWithRetry() error = %v", err)
	}
	if capture.configCalls != 2 {
		t.Fatalf("config call count = %d, want 2 after asynchronous PVE lock", capture.configCalls)
	}
}

func TestAPIContainerCreatesWithNetworkWhenPostCreateConfigWouldLock(t *testing.T) {
	db, oldDB, oldLog := newProxmoxAPITestDB(t)
	global.APP_DB = db
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() {
		global.APP_DB = oldDB
		global.APP_LOG = oldLog
	})
	if err := db.Create(&providerModel.Provider{ID: 1, StoragePool: "local"}).Error; err != nil {
		t.Fatalf("seed provider: %v", err)
	}
	if err := db.Create(&systemModel.SystemImage{
		Name: "debian-12", URL: "https://example.test/debian-12.tar.xz",
		ProviderType: "proxmox", InstanceType: "container", Architecture: "amd64", Status: "active",
	}).Error; err != nil {
		t.Fatalf("seed system image: %v", err)
	}

	provider := NewProxmoxProvider().(*ProxmoxProvider)
	provider.config = coreprovider.NodeConfig{ID: 1, Host: "pve.test", Architecture: "amd64"}
	provider.node = "pve-node"
	provider.sshClient.SetExecutor(&ipv6CommandExecutor{output: func(command string) string {
		if strings.Contains(command, "[ -f ") {
			return "exists"
		}
		return ""
	}})
	capture := &proxmoxCreateCaptureTransport{configResponseCodes: []int{http.StatusInternalServerError}}
	provider.apiClient = &http.Client{Transport: capture}

	config := coreprovider.InstanceConfig{
		Name:         "retry-config-lock",
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
	if capture.configCalls != 0 {
		t.Fatalf("config call count = %d, want no post-create config request", capture.configCalls)
	}
	if _, ok := capture.createPayload["net0"]; !ok {
		t.Fatalf("create payload did not include net0: %#v", capture.createPayload)
	}
}

func TestProxmoxPostCreateNetworkConfigPolicy(t *testing.T) {
	tests := []struct {
		networkType string
		want        bool
	}{
		{networkType: "", want: false}, // legacy providers default to NAT IPv4
		{networkType: "nat_ipv4", want: false},
		{networkType: " NAT_IPV4 ", want: false},
		{networkType: "nat_ipv4_ipv6", want: true},
		{networkType: "dedicated_ipv4", want: true},
		{networkType: "dedicated_ipv4_ipv6", want: true},
		{networkType: "ipv6_only", want: true},
	}
	for _, test := range tests {
		if got := proxmoxNeedsPostCreateNetworkConfig(test.networkType); got != test.want {
			t.Errorf("proxmoxNeedsPostCreateNetworkConfig(%q) = %t, want %t", test.networkType, got, test.want)
		}
	}
}

func newProxmoxAPITestDB(t *testing.T) (*gorm.DB, *gorm.DB, *zap.Logger) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "proxmox-api-task.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite database: %v", err)
	}
	if err := db.AutoMigrate(&providerModel.Provider{}, &systemModel.SystemImage{}); err != nil {
		t.Fatalf("migrate test tables: %v", err)
	}
	return db, global.APP_DB, global.APP_LOG
}
