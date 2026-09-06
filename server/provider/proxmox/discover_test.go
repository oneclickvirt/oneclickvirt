package proxmox

import (
	"context"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"oneclickvirt/global"
	providerCore "oneclickvirt/provider"

	"go.uber.org/zap"
)

type discoveryTestExecutor struct{}

type discoveryRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn discoveryRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

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

type mappingDiscoveryExecutor struct {
	targetIP string
}

type recoveryDiscoveryExecutor struct {
	discoveryTestExecutor
	commands []string
}

func (e *recoveryDiscoveryExecutor) Execute(command string) (string, error) {
	e.commands = append(e.commands, command)
	if strings.Contains(command, "/cluster/resources") {
		return `[
			{"id":"qemu/120","node":"pve9","name":"recovered-vm","status":"running","type":"qemu","vmid":120,"maxcpu":2,"maxmem":1073741824,"maxdisk":8589934592},
			{"id":"lxc/121","node":"pve9","name":"recovered-ct","status":"running","type":"lxc","vmid":121,"maxcpu":1,"maxmem":536870912,"maxdisk":4294967296}
		]`, nil
	}
	if strings.Contains(command, "OCVREC") {
		return "OCVREC\t0\t{\"net0\":\"name=eth0,bridge=vmbr1,ip=10.42.0.120/24,ip6=2001:db8:42::120/64\"}\n" +
			"OCVREC\t1\t{\"net0\":\"name=eth0,bridge=vmbr1,ip=10.42.0.121/24\"}\n", nil
	}
	return "", nil
}

func (e *recoveryDiscoveryExecutor) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}

func (e *recoveryDiscoveryExecutor) ExecuteWithLogging(command, _ string) (string, error) {
	return e.Execute(command)
}

func (e *recoveryDiscoveryExecutor) ExecuteRaw(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}

func (e mappingDiscoveryExecutor) Execute(command string) (string, error) {
	switch {
	case strings.Contains(command, "nft -a list ruleset"):
		return "tcp dport 22022 dnat to " + e.targetIP + ":22\n", nil
	case strings.Contains(command, "iptables-save -t nat"):
		return "-A PREROUTING -p tcp --dport 22022 -j DNAT --to-destination " + e.targetIP + ":22\n" +
			"-A PREROUTING -p udp --dport 22022 -j DNAT --to-destination " + e.targetIP + ":22\n", nil
	case strings.Contains(command, "ping -c"):
		return "unreachable\n", nil
	case strings.Contains(command, "qm config"), strings.Contains(command, "qm guest cmd"):
		return "", errors.New("guest has no reported address")
	default:
		return "", nil
	}
}
func (e mappingDiscoveryExecutor) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}
func (e mappingDiscoveryExecutor) ExecuteWithLogging(command, _ string) (string, error) {
	return e.Execute(command)
}
func (e mappingDiscoveryExecutor) ExecuteRaw(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}
func (mappingDiscoveryExecutor) ExecuteViaTempScript(string, []string, time.Duration) (string, error) {
	return "", nil
}
func (mappingDiscoveryExecutor) UploadContent(string, string, os.FileMode) error { return nil }
func (mappingDiscoveryExecutor) IsHealthy() bool                                 { return true }
func (mappingDiscoveryExecutor) Reconnect() error                                { return nil }
func (mappingDiscoveryExecutor) Close() error                                    { return nil }

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

func TestAPIDiscoveryBatchesGuestDescriptionsByNodeWithoutSSH(t *testing.T) {
	responses := map[string]string{
		"/api2/json/cluster/resources": `{"data":[
			{"id":"qemu/120","node":"pve-a","name":"existing-vm","status":"running","type":"qemu","vmid":120,"maxcpu":2,"maxmem":1073741824,"maxdisk":8589934592},
			{"id":"lxc/121","node":"pve-a","name":"existing-ct","status":"stopped","type":"lxc","vmid":121,"maxcpu":1,"maxmem":536870912,"maxdisk":4294967296}
		]}`,
		"/api2/json/nodes/pve-a/execute": `{"data":[
			{"status":200,"data":{"description":"# 用户名-username admin\n# 密码-password VMSecret\n# SSH端口 22001"}},
			{"status":200,"data":{"description":"# root密码-password CTSecret\n# SSH端口 22002"}}
		]}`,
	}
	var requestCount atomic.Int32
	var directConfigReads atomic.Int32
	client := &http.Client{Transport: discoveryRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requestCount.Add(1)
		if strings.HasSuffix(request.URL.Path, "/config") {
			directConfigReads.Add(1)
		}
		if strings.HasSuffix(request.URL.Path, "/execute") {
			requestBody, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("read execute request: %v", err)
			}
			bodyText := string(requestBody)
			if !strings.Contains(bodyText, `qemu/120/config`) || !strings.Contains(bodyText, `lxc/121/config`) {
				t.Errorf("execute request does not contain both guest configs: %s", bodyText)
			}
		}
		body, ok := responses[request.URL.Path]
		status := http.StatusOK
		if !ok {
			status = http.StatusNotFound
			body = `{"data":null}`
		}
		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    request,
		}, nil
	})}
	p := &ProxmoxProvider{
		config:    nodeConfigForDiscoveryTest("user@pve!token", "secret"),
		apiClient: client,
	}

	instances, err := p.apiDiscoverInstances(context.Background())
	if err != nil {
		t.Fatalf("apiDiscoverInstances() error = %v", err)
	}
	if len(instances) != 2 {
		t.Fatalf("instances = %#v, want two guests", instances)
	}
	for _, instance := range instances {
		resource, ok := instance.RawData.(proxmoxDiscoveredResource)
		if !ok || !strings.Contains(resource.Description, "密码-password") {
			t.Fatalf("guest %s description was not enriched: %#v", instance.ProviderInstanceID, instance.RawData)
		}
	}
	if requestCount.Load() != 2 || directConfigReads.Load() != 0 {
		t.Fatalf("requests = %d direct config reads = %d, want cluster + one node batch", requestCount.Load(), directConfigReads.Load())
	}
}

func TestRecoveryDiscoveryBatchesAPINetworkConfigWithoutPerGuestFallback(t *testing.T) {
	responses := map[string]string{
		"/api2/json/cluster/resources": `{"data":[
			{"id":"qemu/120","node":"pve-a","name":"recovered-vm","status":"running","type":"qemu","vmid":120,"maxcpu":2,"maxmem":1073741824,"maxdisk":8589934592},
			{"id":"lxc/121","node":"pve-a","name":"recovered-ct","status":"running","type":"lxc","vmid":121,"maxcpu":1,"maxmem":536870912,"maxdisk":4294967296}
		]}`,
		"/api2/json/nodes/pve-a/execute": `{"data":[
			{"status":200,"data":{"net0":"name=eth0,bridge=vmbr1,ip=10.42.0.120/24,ip6=2001:db8:42::120/64"}},
			{"status":200,"data":{"net0":"name=eth0,bridge=vmbr1,ip=10.42.0.121/24"}}
		]}`,
	}
	var requestCount atomic.Int32
	var directConfigReads atomic.Int32
	client := &http.Client{Transport: discoveryRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requestCount.Add(1)
		if strings.HasSuffix(request.URL.Path, "/config") {
			directConfigReads.Add(1)
		}
		if strings.HasSuffix(request.URL.Path, "/execute") {
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("read execute request: %v", err)
			}
			if !strings.Contains(string(body), `qemu/120/config`) || !strings.Contains(string(body), `lxc/121/config`) {
				t.Errorf("recovery execute request does not batch both guest configs: %s", body)
			}
		}
		body, ok := responses[request.URL.Path]
		status := http.StatusOK
		if !ok {
			status = http.StatusNotFound
			body = `{"data":null}`
		}
		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    request,
		}, nil
	})}
	p := &ProxmoxProvider{
		connected: true,
		config:    nodeConfigForDiscoveryTest("user@pve!token", "secret"),
		apiClient: client,
	}
	p.config.ExecutionRule = "api_only"
	instances, err := p.DiscoverInstancesForRecovery(context.Background())
	if err != nil {
		t.Fatalf("DiscoverInstancesForRecovery() error = %v", err)
	}
	if len(instances) != 2 {
		t.Fatalf("instances = %#v, want two guests", instances)
	}
	if instances[0].PrivateIP != "10.42.0.120" || instances[0].IPv6Address != "2001:db8:42::120" {
		t.Fatalf("VM recovery network = %#v", instances[0])
	}
	if instances[1].PrivateIP != "10.42.0.121" || instances[1].IPv6Address != "" {
		t.Fatalf("LXC recovery network = %#v", instances[1])
	}
	if requestCount.Load() != 2 || directConfigReads.Load() != 0 {
		t.Fatalf("requests = %d direct config reads = %d, want cluster + one bounded execute batch", requestCount.Load(), directConfigReads.Load())
	}
}

func TestRecoveryDiscoveryUsesTwoSSHCallsForAllGuestConfigs(t *testing.T) {
	executor := &recoveryDiscoveryExecutor{}
	p := NewProxmoxProvider().(*ProxmoxProvider)
	p.connected = true
	p.config.ExecutionRule = "ssh_only"
	p.sshClient.SetExecutor(executor)

	instances, err := p.DiscoverInstancesForRecovery(context.Background())
	if err != nil {
		t.Fatalf("DiscoverInstancesForRecovery() error = %v", err)
	}
	if len(instances) != 2 {
		t.Fatalf("instances = %#v, want two guests", instances)
	}
	if len(executor.commands) != 2 {
		t.Fatalf("SSH calls = %d, want cluster resources plus one batched config command", len(executor.commands))
	}
	if strings.Contains(executor.commands[1], "qm guest cmd") || strings.Contains(executor.commands[1], "pct exec") {
		t.Fatalf("recovery config command must not add per-guest runtime probes: %s", executor.commands[1])
	}
	if !strings.Contains(executor.commands[1], "qemu/120/config") || !strings.Contains(executor.commands[1], "lxc/121/config") {
		t.Fatalf("batched SSH config command is missing guests: %s", executor.commands[1])
	}
	if instances[0].PrivateIP != "10.42.0.120" || instances[0].IPv6Address != "2001:db8:42::120" {
		t.Fatalf("VM recovery network = %#v", instances[0])
	}
}

func TestAPIDiscoveryFallsBackWhenNodeBatchIsDenied(t *testing.T) {
	responses := map[string]string{
		"/api2/json/cluster/resources": `{"data":[
			{"id":"qemu/120","node":"pve-a","name":"existing-vm","status":"running","type":"qemu","vmid":120,"maxcpu":2,"maxmem":1073741824,"maxdisk":8589934592}
		]}`,
		"/api2/json/nodes/pve-a/qemu/120/config": `{"data":{"description":"# 用户名-username admin\n# 密码-password VMSecret\n# SSH端口 22001"}}`,
	}
	client := &http.Client{Transport: discoveryRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/api2/json/nodes/pve-a/execute" {
			return &http.Response{
				StatusCode: http.StatusForbidden,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"data":null}`)),
				Request:    request,
			}, nil
		}
		body, ok := responses[request.URL.Path]
		status := http.StatusOK
		if !ok {
			status = http.StatusNotFound
			body = `{"data":null}`
		}
		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    request,
		}, nil
	})}
	p := &ProxmoxProvider{
		config:    nodeConfigForDiscoveryTest("user@pve!token", "secret"),
		apiClient: client,
	}

	instances, err := p.apiDiscoverInstances(context.Background())
	if err != nil {
		t.Fatalf("apiDiscoverInstances() error = %v", err)
	}
	resource, ok := instances[0].RawData.(proxmoxDiscoveredResource)
	if !ok || !strings.Contains(resource.Description, "VMSecret") {
		t.Fatalf("individual fallback did not preserve metadata: %#v", instances)
	}
}

func TestEnrichDiscoveredInstancesUsesFirewallWhenGuestIPUnavailable(t *testing.T) {
	p := NewProxmoxProvider().(*ProxmoxProvider)
	p.connected = true
	p.config.ExecutionRule = "auto"
	targetIP := p.vmidToInternalIP(120)
	p.sshClient.SetExecutor(mappingDiscoveryExecutor{targetIP: targetIP})

	instances := []providerCore.DiscoveredInstance{{
		ProviderInstanceID: "120", Name: "odd / duplicate name", InstanceType: "vm", Status: "stopped",
	}}
	got := p.enrichDiscoveredInstances(context.Background(), instances)
	if len(got) != 1 || got[0].PrivateIP != targetIP {
		t.Fatalf("firewall target did not recover private IP: %#v", got)
	}
	if len(got[0].PortMappings) != 2 {
		t.Fatalf("nft/iptables rules were not merged and deduplicated: %#v", got[0].PortMappings)
	}
	protocols := map[string]bool{}
	for _, mapping := range got[0].PortMappings {
		protocols[mapping.Protocol] = true
		if mapping.HostPort != 22022 || mapping.GuestPort != 22 || !mapping.IsSSH {
			t.Fatalf("unexpected mapping: %#v", mapping)
		}
	}
	if !protocols["tcp"] || !protocols["udp"] {
		t.Fatalf("protocols = %#v, want tcp+udp", protocols)
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
	// Keep the fake transport's host explicit.  API URL construction now uses
	// net.JoinHostPort so a missing host must not silently turn into a valid
	// request with an empty path (which the transport would report as 404).
	return providerCore.NodeConfig{Host: "pve.test", TokenID: tokenID, Token: token}
}
