package agent

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	providerCore "oneclickvirt/provider"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// monitorTestProvider embeds the interface so the test only needs to model
// the provider capabilities used by Docker veth detection.
type monitorTestProvider struct {
	providerCore.Provider
}

func (monitorTestProvider) GetType() string { return "docker" }

func (monitorTestProvider) GetName() string { return "monitor-test" }

func (monitorTestProvider) ExecuteSSHCommand(context.Context, string) (string, error) {
	return "vethmt\n", nil
}

func TestRegisterMonitorConcurrentServicesCreateOneMapping(t *testing.T) {
	var addCalls atomic.Int32
	addStarted := make(chan struct{})
	releaseAdd := make(chan struct{})
	var addOnce sync.Once

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/add":
			addCalls.Add(1)
			addOnce.Do(func() { close(addStarted) })
			select {
			case <-releaseAdd:
			case <-time.After(5 * time.Second):
				t.Fatalf("timed out waiting for test release")
			}
			_, _ = w.Write([]byte(`{"id":42,"interface":["vethmt"],"healthy":true}`))
		case "/api/v1/info":
			_, _ = w.Write([]byte(`{"id":42,"interface":["vethmt"],"used_traffic":0}`))
		case "/api/v1/update":
			_, _ = w.Write([]byte(`{"id":42,"interface":["vethmt"],"healthy":true}`))
		case "/api/v1/delete":
			_, _ = w.Write([]byte(`{"id":42,"deleted":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	host, portText, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split test server address: %v", err)
	}
	var port int
	if _, err := fmt.Sscanf(portText, "%d", &port); err != nil {
		t.Fatalf("parse test server port: %v", err)
	}

	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:monitor_registration_%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&monitoringModel.AgentMonitor{}); err != nil {
		t.Fatalf("migrate monitor table: %v", err)
	}
	// Provider and Instance both declare a generic idx_frozen index, which is
	// legal in MySQL (indexes are table-scoped) but collides in SQLite's schema
	// namespace.  The service path under test only needs these columns, so keep
	// the fixture schema minimal and exercise the real GORM queries/updates.
	if err := db.Exec(`CREATE TABLE providers (
		id INTEGER PRIMARY KEY,
		uuid TEXT NOT NULL,
		name TEXT NOT NULL,
		type TEXT NOT NULL,
		endpoint TEXT,
		agent_remote_ip TEXT,
		connection_type TEXT,
		execution_rule TEXT,
		deleted_at DATETIME
	)`).Error; err != nil {
		t.Fatalf("create provider fixture table: %v", err)
	}
	if err := db.Exec(`INSERT INTO providers
		(id, uuid, name, type, endpoint, connection_type, execution_rule, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, NULL)`,
		701, "monitor-test-provider", "monitor-test-provider", "docker", host, "ssh", "ssh", nil).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if err := db.Exec(`CREATE TABLE instances (
		id INTEGER PRIMARY KEY,
		uuid TEXT NOT NULL,
		created_at DATETIME,
		updated_at DATETIME,
		name TEXT NOT NULL,
		provider TEXT,
		provider_id INTEGER NOT NULL,
		status TEXT,
		network_type TEXT,
		private_ip TEXT,
		public_ip TEXT,
		instance_type TEXT,
		user_id INTEGER,
		pmacct_interface_v4 TEXT,
		pmacct_interface_v6 TEXT,
		deleted_at DATETIME
	)`).Error; err != nil {
		t.Fatalf("create instance fixture table: %v", err)
	}
	instance := &providerModel.Instance{
		ID:           702,
		UUID:         "monitor-test-instance",
		Name:         "monitor-test-instance",
		Provider:     "monitor-test-provider",
		ProviderID:   701,
		Status:       "running",
		NetworkType:  "nat_ipv4",
		PrivateIP:    "10.0.0.7",
		PublicIP:     "198.51.100.7",
		InstanceType: "container",
		UserID:       1,
	}
	if err := db.Exec(`INSERT INTO instances
		(id, uuid, created_at, updated_at, name, provider, provider_id, status, network_type, private_ip,
		 public_ip, instance_type, user_id, pmacct_interface_v4, pmacct_interface_v6, deleted_at)
		VALUES (?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
		instance.ID, instance.UUID, instance.Name, instance.Provider, instance.ProviderID,
		instance.Status, instance.NetworkType, instance.PrivateIP, instance.PublicIP,
		instance.InstanceType, instance.UserID, "", "").Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	config := &monitoringModel.MonitoringConfig{ProviderID: 701, AgentPort: port, AgentToken: "test-token"}
	fakeProvider := monitorTestProvider{}
	serviceA := NewMonitorService(context.Background(), db)
	serviceB := NewMonitorService(context.Background(), db)
	RemoveClient(701)

	type result struct {
		monitor *monitoringModel.AgentMonitor
		err     error
	}
	results := make(chan result, 2)
	go func() {
		monitor, callErr := serviceA.RegisterMonitor(fakeProvider, instance, config, "")
		results <- result{monitor: monitor, err: callErr}
	}()
	select {
	case <-addStarted:
	case <-time.After(5 * time.Second):
		select {
		case got := <-results:
			t.Fatalf("first registration did not reach Agent add endpoint: %v", got.err)
		default:
			t.Fatal("first registration did not reach Agent add endpoint")
		}
	}
	go func() {
		monitor, callErr := serviceB.RegisterMonitor(fakeProvider, instance, config, "")
		results <- result{monitor: monitor, err: callErr}
	}()
	close(releaseAdd)

	for i := 0; i < 2; i++ {
		got := <-results
		if got.err != nil {
			t.Fatalf("concurrent registration failed: %v", got.err)
		}
		if got.monitor == nil || got.monitor.AgentMonitorID != 42 {
			t.Fatalf("unexpected monitor result: %#v", got.monitor)
		}
	}
	if got := addCalls.Load(); got != 1 {
		t.Fatalf("Agent add endpoint called %d times, want exactly once", got)
	}
	var mappings []monitoringModel.AgentMonitor
	if err := db.Where("instance_id = ?", instance.ID).Find(&mappings).Error; err != nil {
		t.Fatalf("load monitor mappings: %v", err)
	}
	if len(mappings) != 1 {
		t.Fatalf("got %d active mappings, want exactly one", len(mappings))
	}
}

func TestRemoveDuplicateMonitorMappingsKeepsUnknownAgentMonitor(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:monitor_duplicate_%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&monitoringModel.AgentMonitor{}); err != nil {
		t.Fatalf("migrate monitor table: %v", err)
	}
	rows := []monitoringModel.AgentMonitor{
		{ID: 1, InstanceID: 8, ProviderID: 9, UserID: 1, AgentMonitorID: 20, IsEnabled: true},
		{ID: 2, InstanceID: 8, ProviderID: 9, UserID: 1, AgentMonitorID: 21, IsEnabled: true},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create duplicate mappings: %v", err)
	}
	service := NewMonitorService(context.Background(), db)
	if err := service.removeDuplicateMonitorMappings(8, rows[1:], nil, rows[0].AgentMonitorID); err != nil {
		t.Fatalf("remove duplicate mappings: %v", err)
	}
	var remaining []monitoringModel.AgentMonitor
	if err := db.Find(&remaining).Error; err != nil {
		t.Fatalf("load remaining mappings: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ID != 1 {
		t.Fatalf("unexpected remaining mappings: %#v", remaining)
	}
}
