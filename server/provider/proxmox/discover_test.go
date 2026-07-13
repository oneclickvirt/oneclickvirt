package proxmox

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"oneclickvirt/global"
	providerCore "oneclickvirt/provider"

	"go.uber.org/zap"
)

type discoveryTestExecutor struct{}

func (discoveryTestExecutor) Execute(command string) (string, error) {
	if strings.Contains(command, "hostname") {
		return "pve9\n", nil
	}
	if strings.Contains(command, "pveversion") {
		return "pve-manager/9.0.1", nil
	}
	return "", nil
}
func (discoveryTestExecutor) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	return discoveryTestExecutor{}.Execute(command)
}
func (discoveryTestExecutor) ExecuteWithLogging(command, _ string) (string, error) {
	return discoveryTestExecutor{}.Execute(command)
}
func (discoveryTestExecutor) ExecuteRaw(command string, _ time.Duration) (string, error) {
	return discoveryTestExecutor{}.Execute(command)
}
func (discoveryTestExecutor) ExecuteViaTempScript(string, []string, time.Duration) (string, error) {
	return "", nil
}
func (discoveryTestExecutor) UploadContent(string, string, os.FileMode) error { return nil }
func (discoveryTestExecutor) IsHealthy() bool                                 { return true }
func (discoveryTestExecutor) Reconnect() error                                { return nil }
func (discoveryTestExecutor) Close() error                                    { return nil }

func TestParseResourcesJSONPVE9(t *testing.T) {
	p := &ProxmoxProvider{}
	got, err := p.parseResourcesJSON(`[
		{"id":"qemu/120","node":"pve9","name":"existing-vm","status":"running","type":"qemu","vmid":120,"maxcpu":4,"maxmem":4294967296,"maxdisk":34359738368},
		{"id":"lxc/121","node":"pve9","name":"","status":"stopped","type":"lxc","vmid":121,"maxcpu":2,"maxmem":1073741824,"maxdisk":8589934592}
	]`)
	if err != nil {
		t.Fatalf("parseResourcesJSON() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(instances) = %d, want 2", len(got))
	}

	vm := got[0]
	if vm.UUID != "proxmox-vm-120" || vm.ProviderInstanceID != "120" || vm.InstanceType != "vm" {
		t.Fatalf("unexpected VM identity: %+v", vm)
	}
	if vm.CPU != 4 || vm.Memory != 4096 || vm.Disk != 32768 {
		t.Fatalf("unexpected VM resources: cpu=%d memory=%d disk=%d", vm.CPU, vm.Memory, vm.Disk)
	}

	ct := got[1]
	if ct.UUID != "proxmox-lxc-121" || ct.ProviderInstanceID != "121" || ct.Name != "ct-121" || ct.InstanceType != "container" {
		t.Fatalf("unexpected LXC identity: %+v", ct)
	}
}

func TestParseResourcesJSONFiltersTemplates(t *testing.T) {
	p := &ProxmoxProvider{}
	got, err := p.parseResourcesJSON(`[
		{"id":"qemu/9000","name":"vm-template","type":"qemu","vmid":9000,"template":1},
		{"id":"lxc/9001","name":"ct-template","type":"lxc","vmid":9001,"template":true},
		{"id":"qemu/120","name":"existing-vm","type":"qemu","vmid":120,"template":0}
	]`)
	if err != nil {
		t.Fatalf("parseResourcesJSON() error = %v", err)
	}
	if len(got) != 1 || got[0].ProviderInstanceID != "120" {
		t.Fatalf("instances = %+v, want only VMID 120", got)
	}
}

func TestParseResourcesJSONRejectsInvalidGuestIdentity(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "missing vmid", data: `[{"id":"qemu/unknown","name":"broken","type":"qemu"}]`},
		{name: "unknown type", data: `[{"id":"openvz/120","name":"broken","type":"openvz","vmid":120}]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &ProxmoxProvider{}
			if _, err := p.parseResourcesJSON(tt.data); err == nil {
				t.Fatalf("parseResourcesJSON() accepted %s", tt.data)
			}
		})
	}
}

func TestParseProxmoxResourceEnvelopeRequiresDataArray(t *testing.T) {
	for _, payload := range []string{`{}`, `{"data":null}`, `{"data":{}}`} {
		if _, err := parseProxmoxResourceEnvelope([]byte(payload)); err == nil {
			t.Fatalf("parseProxmoxResourceEnvelope() accepted %s", payload)
		}
	}
	resources, err := parseProxmoxResourceEnvelope([]byte(`{"data":[]}`))
	if err != nil || len(resources) != 0 {
		t.Fatalf("valid empty data: resources=%v err=%v", resources, err)
	}
}

func TestParseResourcesJSONRejectsMalformedOutput(t *testing.T) {
	p := &ProxmoxProvider{}
	if _, err := p.parseResourcesJSON("not-json"); err == nil {
		t.Fatal("parseResourcesJSON() accepted malformed pvesh output")
	}
}

func TestMakeAPIRequestAuthenticatesAndChecksStatus(t *testing.T) {
	const tokenID = "oneclickvirt@pve!test"
	const tokenSecret = "secret-value"

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wantAuth := "PVEAPIToken=" + tokenID + "=" + tokenSecret
		if got := r.Header.Get("Authorization"); got != wantAuth {
			t.Errorf("Authorization = %q, want %q", got, wantAuth)
		}
		if r.URL.Path == "/denied" {
			http.Error(w, "permission denied", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	p := &ProxmoxProvider{
		config:    nodeConfigForDiscoveryTest(tokenID, tokenSecret),
		apiClient: server.Client(),
	}
	data, err := p.makeAPIRequest(context.Background(), http.MethodGet, server.URL+"/ok", nil)
	if err != nil {
		t.Fatalf("makeAPIRequest() error = %v", err)
	}
	if string(data) != `{"data":[]}` {
		t.Fatalf("response = %q", data)
	}

	_, err = p.makeAPIRequest(context.Background(), http.MethodGet, server.URL+"/denied", nil)
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("denied request error = %v, want HTTP 403", err)
	}
}

func TestNormalizeTokenConfigSplitsPersistedValue(t *testing.T) {
	p := &ProxmoxProvider{config: nodeConfigForDiscoveryTest("user@pve!token", "user@pve!token=secret")}
	p.normalizeTokenConfig()
	if p.config.TokenID != "user@pve!token" || p.config.Token != "secret" {
		t.Fatalf("normalized token = id %q secret %q", p.config.TokenID, p.config.Token)
	}
}

func TestConnectAgentNormalizesPersistedToken(t *testing.T) {
	global.APP_LOG = zap.NewNop()
	p := NewProxmoxProvider().(*ProxmoxProvider)
	config := nodeConfigForDiscoveryTest("user@pve!token", "user@pve!token=secret")
	config.NodeInstallType = "third_party"
	config.HostName = "pve9"
	if err := p.ConnectAgent(discoveryTestExecutor{}, config); err != nil {
		t.Fatalf("ConnectAgent() error = %v", err)
	}
	if p.config.TokenID != "user@pve!token" || p.config.Token != "secret" {
		t.Fatalf("agent token = id %q secret %q", p.config.TokenID, p.config.Token)
	}
}

func TestConfigureAPITLSUsesConfiguredCA(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	caPath := t.TempDir() + "/pve-ca.pem"
	if err := os.WriteFile(caPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}

	p := NewProxmoxProvider().(*ProxmoxProvider)
	config := nodeConfigForDiscoveryTest("", "")
	config.CACertPath = caPath
	if err := p.configureAPITLS(config); err != nil {
		t.Fatalf("configureAPITLS() error = %v", err)
	}
	if p.transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("configured CA still leaves InsecureSkipVerify enabled")
	}
	if p.transport.TLSClientConfig.RootCAs == nil {
		t.Fatal("configured CA did not populate RootCAs")
	}
}

func TestConfigureAPITLSFallsBackOnlyWithoutCA(t *testing.T) {
	p := NewProxmoxProvider().(*ProxmoxProvider)
	if p.transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("new transport must not disable verification before Connect configures TLS")
	}
	if err := p.configureAPITLS(nodeConfigForDiscoveryTest("", "")); err != nil {
		t.Fatalf("configureAPITLS() error = %v", err)
	}
	if !p.transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("stock self-signed PVE compatibility fallback was not enabled without a CA")
	}
}

func nodeConfigForDiscoveryTest(tokenID, token string) providerCore.NodeConfig {
	return providerCore.NodeConfig{TokenID: tokenID, Token: token}
}
