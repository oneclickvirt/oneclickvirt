package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestInstanceEgressSourcesNATExcludesSharedHostPublicAddresses(t *testing.T) {
	node := &providerModel.Provider{
		Endpoint:      "203.0.113.10",
		PortIP:        "203.0.113.11",
		AgentRemoteIP: "[2001:db8::10]:443",
		NetworkType:   "nat_ipv4_ipv6",
	}
	instance := &providerModel.Instance{
		NetworkType: "nat_ipv4_ipv6",
		PrivateIP:   "10.20.0.7",
		PublicIP:    "203.0.113.10",
		IPv6Address: "fd42::7",
		PublicIPv6:  "2001:db8::10",
	}

	got, err := instanceEgressSources(instance, node)
	if err != nil {
		t.Fatalf("instanceEgressSources returned error: %v", err)
	}
	want := []string{"10.20.0.7", "fd42::7"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("instanceEgressSources() = %#v, want %#v", got, want)
	}
	for _, forbidden := range []string{node.Endpoint, node.PortIP, "2001:db8::10"} {
		if strings.Contains(strings.Join(got, ","), forbidden) {
			t.Fatalf("NAT sources unexpectedly contain host address %s: %#v", forbidden, got)
		}
	}
}

func TestInstanceEgressSourcesDedicatedUsesOneSafeIdentityPerFamily(t *testing.T) {
	node := &providerModel.Provider{Endpoint: "https://203.0.113.10:8443", NetworkType: "dedicated_ipv4_ipv6"}
	instance := &providerModel.Instance{
		NetworkType: "dedicated_ipv4_ipv6",
		PrivateIP:   "198.51.100.20",
		PublicIP:    "203.0.113.10", // unsafe host identity; fall back to the instance address
		IPv6Address: "2001:db8:1::20",
		PublicIPv6:  "2001:db8:2::20",
	}

	got, err := instanceEgressSources(instance, node)
	if err != nil {
		t.Fatalf("instanceEgressSources returned error: %v", err)
	}
	want := []string{"198.51.100.20", "2001:db8:2::20"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("instanceEgressSources() = %#v, want %#v", got, want)
	}
}

func TestMergeInstanceEgressSourcesPreservesExplicitSelectors(t *testing.T) {
	node := &providerModel.Provider{Endpoint: "203.0.113.10"}
	got, err := mergeInstanceEgressSources(
		[]string{"10.99.0.0/24"},
		[]string{"10.20.0.7", "fd42::7"},
		node,
	)
	if err != nil {
		t.Fatalf("mergeInstanceEgressSources returned error: %v", err)
	}
	want := []string{"10.99.0.0/24", "10.20.0.7/32", "fd42::7/128"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mergeInstanceEgressSources() = %#v, want %#v", got, want)
	}
}

func TestMergeInstanceEgressSourcesRejectsHostCapture(t *testing.T) {
	node := &providerModel.Provider{Endpoint: "203.0.113.10"}
	_, err := mergeInstanceEgressSources([]string{"203.0.113.0/24"}, nil, node)
	if err == nil || !strings.Contains(err.Error(), "节点管理或公网地址") {
		t.Fatalf("expected host capture rejection, got %v", err)
	}
}

func TestDesiredExplicitEgressSourcesSeparatesLegacyAutomaticAddresses(t *testing.T) {
	instance := &providerModel.Instance{
		PrivateIP:   "10.20.0.7",
		PublicIP:    "203.0.113.10",
		IPv6Address: "fd42::7",
		PublicIPv6:  "2001:db8::10",
	}
	desired := &monitoringModel.EgressDesiredBinding{
		SourcesJSON: `["10.20.0.7/32","203.0.113.10/32","10.99.0.0/24"]`,
	}

	got, err := desiredExplicitEgressSources(desired, instance)
	if err != nil {
		t.Fatalf("desiredExplicitEgressSources returned error: %v", err)
	}
	want := []string{"10.99.0.0/24"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("desiredExplicitEgressSources() = %#v, want %#v", got, want)
	}
}

func TestValidateEgressProfileTransportProtectsManagedSecrets(t *testing.T) {
	managed := true
	profile := &EgressProfileRequest{
		TunnelType: "wireguard",
		WireGuard:  &EgressWireGuardRequest{Managed: &managed},
	}
	if err := validateEgressProfileTransport(&providerModel.Provider{ConnectionType: "ssh"}, profile); err == nil {
		t.Fatal("expected managed WireGuard over SSH/HTTP to be rejected")
	}
	if err := validateEgressProfileTransport(&providerModel.Provider{ConnectionType: "agent"}, profile); err != nil {
		t.Fatalf("reverse Agent transport should be allowed: %v", err)
	}
	if err := validateEgressProfileTransport(&providerModel.Provider{ConnectionType: "agent", ExecutionRule: "api_only"}, profile); err == nil {
		t.Fatal("api_only Agent must not be treated as a reverse-Agent secret transport")
	}
	unmanaged := false
	profile.WireGuard.Managed = &unmanaged
	if err := validateEgressProfileTransport(&providerModel.Provider{ConnectionType: "ssh"}, profile); err != nil {
		t.Fatalf("preconfigured unmanaged WireGuard should be allowed: %v", err)
	}
}

func TestEgressClientRequiresInstalledAgentForAPIOnlyProvider(t *testing.T) {
	node := &providerModel.Provider{
		ID:             987654,
		Endpoint:       "127.0.0.1",
		ConnectionType: "agent",
		ExecutionRule:  "api_only",
	}
	config := &monitoringModel.MonitoringConfig{AgentInstalled: true, AgentToken: "test-token"}
	client, err := egressClient(node, config)
	if err != nil {
		t.Fatalf("installed direct Agent client = %v", err)
	}
	if client.isAgentMode {
		t.Fatal("api_only provider must use the direct Agent client, not WebSocket fallback")
	}

	config.AgentInstalled = false
	if _, err := egressClient(node, config); err == nil {
		t.Fatal("api_only provider without an installed direct Agent must not be treated as Agent-capable")
	}
}

func TestDeriveEgressCapabilitiesRequiresProvenProviderDataPlane(t *testing.T) {
	instance := &providerModel.Instance{NetworkType: "nat_ipv4", PmacctInterfaceV4: "veth-test"}
	if supported, _, _ := deriveEgressCapabilities(instance, &providerModel.Provider{Type: "kubevirt"}); supported {
		t.Fatal("KubeVirt must not claim native host-netfilter support")
	}
	if supported, recommended, _ := deriveEgressCapabilities(instance, &providerModel.Provider{Type: "kubevirt"}); recommended != "cni" || supported {
		t.Fatalf("KubeVirt capability = supported:%v mode:%s, want false/cni", supported, recommended)
	}
	if supported, _, _ := deriveEgressCapabilities(instance, &providerModel.Provider{Type: "unknown-runtime"}); supported {
		t.Fatal("unknown provider must not claim native support")
	}
	provider := &providerModel.Provider{Type: "docker", Config: `{"sriov":true}`}
	if supported, _, reasons := deriveEgressCapabilities(instance, provider); supported || len(reasons) == 0 {
		t.Fatalf("external passthrough hint must disable native support: supported=%v reasons=%v", supported, reasons)
	}
	if supported, _, _ := deriveEgressCapabilities(instance, &providerModel.Provider{Type: "docker"}); !supported {
		t.Fatal("validated Docker bridge should support native mode")
	}
	withoutInterface := &providerModel.Instance{NetworkType: "nat_ipv4"}
	if supported, _, reasons := deriveEgressCapabilities(withoutInterface, &providerModel.Provider{Type: "docker"}); supported || len(reasons) == 0 {
		t.Fatalf("provider allowlist without a host-visible interface must remain unsupported: supported=%v reasons=%v", supported, reasons)
	}
}

func TestRestoreProviderEgressUsesOneBatchAgentRequest(t *testing.T) {
	bindingCount := egressStateReadBatchSize + 1
	var requestCount atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/egress/state", func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		if r.Header.Get("x-token") != "test-token" {
			t.Errorf("x-token was not forwarded")
		}
		var req EgressStateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if len(req.Profiles) != 1 || len(req.Bindings) != bindingCount || !req.Apply {
			t.Errorf("unexpected replacement request: profiles=%d bindings=%d apply=%v", len(req.Profiles), len(req.Bindings), req.Apply)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(EgressStateResponse{
			ProfileCount: len(req.Profiles),
			BindingCount: len(req.Bindings),
			Reconcile: EgressReconcileResponse{
				Applied:    true,
				FailClosed: true,
				Errors:     []string{},
			},
		})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, portText, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatal(err)
	}
	var port int
	if _, err := fmt.Sscanf(portText, "%d", &port); err != nil {
		t.Fatal(err)
	}

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE providers (
		id integer primary key, uuid text, name text, type text, endpoint text,
		port_ip text, agent_remote_ip text, network_type text, connection_type text,
		config text, deleted_at datetime
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE instances (
		id integer primary key, uuid text, name text, provider text, provider_id integer,
		status text, network_type text, private_ip text, public_ip text,
		ipv6_address text, public_ipv6 text, pmacct_interface_v4 text,
		pmacct_interface_v6 text, egress_profile_id text, deleted_at datetime
	)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&monitoringModel.MonitoringConfig{},
		&monitoringModel.AgentMonitor{},
		&monitoringModel.EgressDesiredProfile{},
		&monitoringModel.EgressDesiredBinding{},
	); err != nil {
		t.Fatal(err)
	}
	provider := providerModel.Provider{
		ID:             910001,
		Name:           "egress-batch-test",
		Type:           "docker",
		Endpoint:       host,
		NetworkType:    "nat_ipv4",
		ConnectionType: "ssh",
	}
	if err := db.Table("providers").Create(map[string]interface{}{
		"id":              provider.ID,
		"uuid":            "00000000-0000-0000-0000-000000000010",
		"name":            provider.Name,
		"type":            provider.Type,
		"endpoint":        provider.Endpoint,
		"network_type":    provider.NetworkType,
		"connection_type": provider.ConnectionType,
		"config":          `{}`,
	}).Error; err != nil {
		t.Fatal(err)
	}
	defer RemoveClient(provider.ID)
	config := monitoringModel.MonitoringConfig{
		ProviderID:     provider.ID,
		AgentToken:     "test-token",
		AgentPort:      port,
		AgentInstalled: true,
	}
	if err := db.Create(&config).Error; err != nil {
		t.Fatal(err)
	}
	enabled := true
	failClosed := true
	profileRequest := EgressProfileRequest{
		ID:              "profile-1",
		Mode:            "native",
		TunnelType:      "gateway",
		TunnelInterface: "tun0",
		RouteTable:      100,
		Mark:            7,
		Enabled:         &enabled,
		FailClosed:      &failClosed,
	}
	encodedProfile, err := json.Marshal(profileRequest)
	if err != nil {
		t.Fatal(err)
	}
	desiredProfile := monitoringModel.EgressDesiredProfile{
		ProviderID: provider.ID,
		ProfileID:  profileRequest.ID,
		ConfigJSON: string(encodedProfile),
	}
	if err := db.Create(&desiredProfile).Error; err != nil {
		t.Fatal(err)
	}
	instanceRows := make([]map[string]interface{}, 0, bindingCount)
	desiredBindings := make([]monitoringModel.EgressDesiredBinding, 0, bindingCount)
	for index := 0; index < bindingCount; index++ {
		instanceID := uint(920001 + index)
		instanceUUID := fmt.Sprintf("00000000-0000-0000-0000-%012d", index+1)
		instanceRows = append(instanceRows, map[string]interface{}{
			"id":                  instanceID,
			"uuid":                instanceUUID,
			"name":                fmt.Sprintf("batch-instance-%03d", index),
			"provider":            provider.Name,
			"provider_id":         provider.ID,
			"status":              "running",
			"network_type":        "nat_ipv4",
			"private_ip":          "10.20.0.2",
			"pmacct_interface_v4": "veth-test",
			"egress_profile_id":   profileRequest.ID,
		})
		desiredBindings = append(desiredBindings, monitoringModel.EgressDesiredBinding{
			InstanceID:          instanceID,
			InstanceKey:         instanceUUID,
			ProviderID:          provider.ID,
			ProfileID:           profileRequest.ID,
			SourcesJSON:         `["10.20.0.2/32"]`,
			ExplicitSourcesJSON: `[]`,
			InterfaceV4:         "veth-test",
			Enabled:             true,
		})
	}
	if err := db.Table("instances").CreateInBatches(&instanceRows, egressStateWriteBatchSize).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.CreateInBatches(&desiredBindings, egressStateWriteBatchSize).Error; err != nil {
		t.Fatal(err)
	}

	restored, err := NewInstanceEgressService(db).RestoreProviderEgress(context.Background(), provider.ID, true)
	if err != nil {
		t.Fatalf("RestoreProviderEgress returned error: %v", err)
	}
	if restored != bindingCount {
		t.Fatalf("restored = %d, want %d", restored, bindingCount)
	}
	if got := requestCount.Load(); got != 1 {
		t.Fatalf("Agent request count = %d, want exactly 1", got)
	}
}
