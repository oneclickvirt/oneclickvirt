package admin

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/utils"

	"github.com/gin-gonic/gin"
)

func TestParseVNCDiscoveredPortSupportsNestedMetadata(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want int
	}{
		{name: "top level", raw: `{"vncPort": 5907}`, want: 5907},
		{name: "console object", raw: `{"console":{"vnc":{"port":5908}}}`, want: 5908},
		{name: "console port", raw: `{"console":{"protocol":"vnc","port":"5909"}}`, want: 5909},
		{name: "spice port is not legacy vnc", raw: `{"console":{"protocol":"spice","port":6100,"spice":{"port":6100}}}`, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseVNCDiscoveredPort(tc.raw); got != tc.want {
				t.Fatalf("parseVNCDiscoveredPort() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestParseDiscoveredConsoleKeepsNativeProtocolAlongsideSpice(t *testing.T) {
	got := parseDiscoveredConsole(`{"console":{"protocol":"spice","spice":{"port":6100},"serial":{"url":"https://node.example/serial"}}}`)
	if got.protocol != consoleProtocolSPICE {
		t.Fatalf("protocol = %q, want spice", got.protocol)
	}
	if got.spicePort != 6100 {
		t.Fatalf("spice port = %d, want 6100", got.spicePort)
	}
	if got.nativeProtocol != "serial" || got.nativeURL != "https://node.example/serial" {
		t.Fatalf("native metadata = (%q, %q), want serial URL", got.nativeProtocol, got.nativeURL)
	}
}

func TestParseDiscoveredConsoleKeepsVNCAndSPICESeparate(t *testing.T) {
	got := parseDiscoveredConsole(`{"console":{"protocol":"vnc","host":"127.0.0.1","port":5901,"spice":{"host":"127.0.0.1","port":6101,"transport":"ssh","managed":true}}}`)
	if got.protocol != consoleProtocolVNC || got.port != 5901 {
		t.Fatalf("VNC metadata = (%q, %d), want (vnc, 5901)", got.protocol, got.port)
	}
	if got.spicePort != 6101 || got.spiceHost != "127.0.0.1" || got.spiceTransport != "ssh" || !got.spiceManaged {
		t.Fatalf("SPICE metadata not retained: %#v", got)
	}
}

func TestRewriteSpiceHTMLBindsToSelectedConsoleScope(t *testing.T) {
	body := []byte(`<script>spice_query_var('path', 'websockify')</script>`)
	rewritten := string(rewriteSpiceHTMLWithWebSocketPath(body, "/api/v1/admin/instances/42/console/spice-ws"))
	if !strings.Contains(rewritten, "/api/v1/admin/instances/42/console/spice-ws") {
		t.Fatalf("rewritten SPICE HTML = %q", rewritten)
	}
	if strings.Contains(rewritten, "'websockify'") {
		t.Fatalf("rewritten SPICE HTML retained node websockify path: %q", rewritten)
	}
}

func TestValidateNativeConsoleURLRejectsBrowserPathReinterpretation(t *testing.T) {
	provider := providerModel.Provider{Endpoint: "https://node.example.test:8443"}
	for _, rawURL := range []string{
		"/\\evil.example.test/console",
		`/\\evil.example.test/console`,
		`/%5cevil.example.test/console`,
		`/%5c%5cevil.example.test/console`,
		`https://evil.example.test/console`,
		`https://user@node.example.test/console`,
		`javascript:alert(1)`,
	} {
		if _, err := validateNativeConsoleURL(rawURL, provider); err == nil {
			t.Fatalf("validateNativeConsoleURL(%q) unexpectedly succeeded", rawURL)
		}
	}
	if got, err := validateNativeConsoleURL("https://node.example.test:8443/serial", provider); err != nil || got == "" {
		t.Fatalf("trusted node URL rejected: url=%q err=%v", got, err)
	}
}

func TestRefreshSPICEConsoleTargetDetectsStaleMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/spice_auto.html" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	host, port := testConsoleHostPort(t, server.URL)
	target := consoleTarget{
		protocol: consoleProtocolSPICE, host: host, port: port, available: true,
		instanceID: 990001, provider: providerModel.Provider{Type: "incus"},
	}
	invalidateSPICEHealth(target.instanceID)
	if got := refreshSPICEConsoleTarget(target); !got.available || got.repairable {
		t.Fatalf("healthy SPICE target = %#v", got)
	}
	server.Close()
	invalidateSPICEHealth(target.instanceID)
	if got := refreshSPICEConsoleTarget(target); got.available || !got.repairable || got.repairStatus != "stale" || !strings.Contains(got.reason, "健康检查失败") {
		t.Fatalf("stale SPICE target was not made repairable: %#v", got)
	}
}

func TestRefreshSPICEConsoleTargetCoalescesConcurrentHealthChecks(t *testing.T) {
	var requests atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	host, port := testConsoleHostPort(t, server.URL)
	target := consoleTarget{
		protocol: consoleProtocolSPICE, host: host, port: port, available: true,
		instanceID: 990002, provider: providerModel.Provider{Type: "incus"},
	}
	invalidateSPICEHealth(target.instanceID)

	const callers = 8
	var wg sync.WaitGroup
	results := make(chan consoleTarget, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- refreshSPICEConsoleTarget(target)
		}()
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("SPICE health request did not reach test server")
	}
	close(release)
	wg.Wait()
	close(results)
	for got := range results {
		if !got.available {
			t.Fatalf("coalesced health check reported unavailable: %#v", got)
		}
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("SPICE health requests = %d, want 1", got)
	}
}

func testConsoleHostPort(t *testing.T, rawURL string) (string, int) {
	t.Helper()
	host, rawPort, err := net.SplitHostPort(strings.TrimPrefix(rawURL, "http://"))
	if err != nil {
		t.Fatalf("parse test console URL %q: %v", rawURL, err)
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil {
		t.Fatalf("parse test console port %q: %v", rawPort, err)
	}
	return host, port
}

func TestConsoleTargetInfoPrefersAvailableProtocolOverRepairable(t *testing.T) {
	info := consoleTargetInfo([]consoleTarget{
		{protocol: consoleProtocolSPICE, repairable: true, reason: "repair"},
		{protocol: consoleProtocolVNC, available: true},
	})
	if info["protocol"] != consoleProtocolVNC {
		t.Fatalf("selected protocol = %v, want vnc", info["protocol"])
	}
	if info["available"] != true || info["repairable"] != true {
		t.Fatalf("availability flags = (%v, %v), want true/true", info["available"], info["repairable"])
	}
}

func TestConsoleStateCachesPruneExpiredEntries(t *testing.T) {
	oldRepairStates := consoleRepairs
	oldHealthStates := spiceHealthCache
	oldAssetTargets := spiceAssetTargetCache
	oldAssetFlights := spiceAssetTargetFlights
	consoleRepairs = map[uint]consoleRepairState{
		1: {status: "ready", updatedAt: time.Now().Add(-consoleRepairStateTTL - time.Second)},
		2: {status: "running", updatedAt: time.Now().Add(-consoleRepairStateTTL - time.Second)},
	}
	spiceHealthCache = map[uint]spiceHealthState{
		3: {available: true, checkedAt: time.Now().Add(-spiceHealthCacheTTL - time.Second)},
		4: {available: true, checkedAt: time.Now()},
	}
	spiceAssetTargetCache = map[spiceAssetTargetCacheKey]spiceAssetTargetCacheEntry{
		{instanceID: 5, userID: 7}: {target: consoleTarget{instanceID: 5}, resolvedAt: time.Now().Add(-spiceAssetTargetCacheTTL - time.Second)},
		{instanceID: 6, userID: 7}: {target: consoleTarget{instanceID: 6}, resolvedAt: time.Now()},
	}
	spiceAssetTargetFlights = make(map[spiceAssetTargetCacheKey]*spiceAssetTargetFlight)
	t.Cleanup(func() {
		consoleRepairs = oldRepairStates
		spiceHealthCache = oldHealthStates
		spiceAssetTargetCache = oldAssetTargets
		spiceAssetTargetFlights = oldAssetFlights
	})

	consoleRepairMu.Lock()
	pruneConsoleRepairsLocked(time.Now())
	_, oldRepairRemains := consoleRepairs[1]
	_, runningRepairRemains := consoleRepairs[2]
	consoleRepairMu.Unlock()
	if oldRepairRemains || !runningRepairRemains {
		t.Fatalf("repair-state pruning kept expired or removed running state: %#v", consoleRepairs)
	}

	spiceHealthMu.Lock()
	pruneSPICEHealthLocked(time.Now())
	_, oldHealthRemains := spiceHealthCache[3]
	_, freshHealthRemains := spiceHealthCache[4]
	spiceHealthMu.Unlock()
	if oldHealthRemains || !freshHealthRemains {
		t.Fatalf("SPICE health pruning retained expired or removed fresh state: %#v", spiceHealthCache)
	}
	if _, cached := cachedSPICEAssetTarget(spiceAssetTargetCacheKey{instanceID: 5, userID: 7}, time.Now()); cached {
		t.Fatal("expired SPICE asset target remained cached")
	}
	if target, cached := cachedSPICEAssetTarget(spiceAssetTargetCacheKey{instanceID: 6, userID: 7}, time.Now()); !cached || target.instanceID != 6 {
		t.Fatalf("fresh SPICE asset target = (%#v, %v)", target, cached)
	}
	invalidateSPICEAssetTargets(6)
	if _, cached := cachedSPICEAssetTarget(spiceAssetTargetCacheKey{instanceID: 6, userID: 7}, time.Now()); cached {
		t.Fatal("invalidated SPICE asset target remained cached")
	}
}

func TestSpiceSetupCommandIsShellValidAndReusesManagedProxy(t *testing.T) {
	command := spiceSetupCommand("guest", 42, providerModel.Provider{VNCBasePort: 6100})
	for _, expected := range []string{
		"STATEFILE=", "LOCKDIR=", "is_websockify_pid()", "EXISTING_PID=",
		"ONECLICKVIRT_SPICE", "websockify --web",
	} {
		if !strings.Contains(command, expected) {
			t.Fatalf("spice setup command missing %q", expected)
		}
	}
	check := exec.Command("sh", "-n")
	check.Stdin = strings.NewReader(command)
	if output, err := check.CombinedOutput(); err != nil {
		t.Fatalf("spice setup command has invalid shell syntax: %v\n%s", err, output)
	}
}

func TestConsoleTerminalPlanUsesProviderRuntimeIdentifier(t *testing.T) {
	instance := providerModel.Instance{Name: "renamed-in-panel", ProviderVMID: "302"}
	plan, err := ResolveInstanceConsoleTerminalPlan("proxmox", "container", instance.ProviderInstanceIdentifier(), "exec")
	if err != nil {
		t.Fatalf("ResolveInstanceConsoleTerminalPlan() error = %v", err)
	}
	if plan.Command != "pct enter '302'" {
		t.Fatalf("terminal command = %q, want provider VMID rather than display name", plan.Command)
	}

	if plans := consoleTerminalPlans("qemu", "container", "unexpected"); len(plans) != 0 {
		t.Fatalf("QEMU container plans = %#v, want none because generic libvirt LXC cannot be assumed", plans)
	}

	kubePlans := consoleTerminalPlans("kubevirt", "container", "worker-a")
	if len(kubePlans) != 2 || kubePlans[0].Protocol != consoleProtocolExec || kubePlans[1].Protocol != consoleProtocolAttach {
		t.Fatalf("KubeVirt container plans = %#v", kubePlans)
	}
	for _, plan := range kubePlans {
		check := exec.Command("sh", "-n")
		check.Stdin = strings.NewReader(plan.Command)
		if output, err := check.CombinedOutput(); err != nil {
			t.Fatalf("KubeVirt %s terminal command has invalid shell syntax: %v\n%s", plan.Protocol, err, output)
		}
	}
}

func TestConsoleTerminalPlansCoverRuntimeAttachNamespaceAndSerial(t *testing.T) {
	for _, tc := range []struct {
		name         string
		providerType string
		instanceType string
		want         []string
	}{
		{name: "docker", providerType: "docker", instanceType: "container", want: []string{consoleProtocolExec, consoleProtocolAttach, consoleProtocolNamespace}},
		{name: "podman", providerType: "podman", instanceType: "container", want: []string{consoleProtocolExec, consoleProtocolAttach, consoleProtocolNamespace}},
		{name: "containerd", providerType: "containerd", instanceType: "container", want: []string{consoleProtocolExec, consoleProtocolAttach, consoleProtocolNamespace}},
		{name: "lxd container", providerType: "lxd", instanceType: "container", want: []string{consoleProtocolExec, consoleProtocolSerial}},
		{name: "incus container", providerType: "incus", instanceType: "container", want: []string{consoleProtocolExec, consoleProtocolSerial}},
		{name: "pve container", providerType: "proxmox", instanceType: "container", want: []string{consoleProtocolExec, consoleProtocolSerial}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plans := consoleTerminalPlans(tc.providerType, tc.instanceType, "unsafe; name")
			if len(plans) != len(tc.want) {
				t.Fatalf("plans = %#v, want %d entries", plans, len(tc.want))
			}
			for index, wantProtocol := range tc.want {
				plan := plans[index]
				if plan.Protocol != wantProtocol {
					t.Fatalf("plan %d protocol = %q, want %q", index, plan.Protocol, wantProtocol)
				}
				check := exec.Command("sh", "-n")
				check.Stdin = strings.NewReader(plan.Command)
				if output, err := check.CombinedOutput(); err != nil {
					t.Fatalf("%s command has invalid shell syntax: %v\n%s", plan.Protocol, err, output)
				}
				if !strings.Contains(plan.Command, utils.ShellSingleQuote("unsafe; name")) {
					t.Fatalf("%s command does not preserve a shell-quoted identifier: %s", plan.Protocol, plan.Command)
				}
			}
		})
	}
}

func TestConsoleTerminalPlansTreatLegacyVMTypeAsVirtualMachine(t *testing.T) {
	plans := consoleTerminalPlans("proxmox", " Virtual-Machine ", "100")
	if len(plans) != 1 || plans[0].Protocol != consoleProtocolSerial || plans[0].Command != "qm terminal '100'" {
		t.Fatalf("legacy PVE VM plans = %#v, want qm serial console", plans)
	}
}

func TestIsTerminalConsoleProtocol(t *testing.T) {
	for _, protocol := range []string{consoleProtocolExec, consoleProtocolAttach, consoleProtocolNamespace, consoleProtocolSerial} {
		if !isTerminalConsoleProtocol(protocol) {
			t.Fatalf("%q should be a terminal protocol", protocol)
		}
	}
	for _, protocol := range []string{"", consoleProtocolVNC, consoleProtocolSPICE, consoleProtocolNative, "rdp"} {
		if isTerminalConsoleProtocol(protocol) {
			t.Fatalf("%q should not be a terminal protocol", protocol)
		}
	}
}

func TestConsoleTerminalTransportRequiresUsableProviderConnection(t *testing.T) {
	for _, tc := range []struct {
		name          string
		provider      providerModel.Provider
		wantTransport string
		wantReason    bool
	}{
		{name: "ssh", provider: providerModel.Provider{ConnectionType: "ssh", Endpoint: "node.example:22", Username: "root"}, wantTransport: "ssh"},
		{name: "offline agent", provider: providerModel.Provider{ID: ^uint(0) - 1, ConnectionType: "agent", AgentStatus: "online"}, wantTransport: "agent", wantReason: true},
		{name: "missing ssh credentials", provider: providerModel.Provider{ConnectionType: "ssh", Endpoint: "node.example"}, wantTransport: "ssh", wantReason: true},
		{name: "unsupported", provider: providerModel.Provider{ConnectionType: "api"}, wantTransport: "api", wantReason: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport, reason := consoleTerminalTransport(tc.provider)
			if transport != tc.wantTransport || (reason != "") != tc.wantReason {
				t.Fatalf("consoleTerminalTransport() = (%q, %q), want (%q, reason=%v)", transport, reason, tc.wantTransport, tc.wantReason)
			}
		})
	}
}

func TestNormalizeVNCConsoleTransportPreservesLegacyDirectAccess(t *testing.T) {
	provider := providerModel.Provider{ConnectionType: "ssh", Endpoint: "node.example"}
	if got := normalizeVNCConsoleTransport(provider, ""); got != "direct" {
		t.Fatalf("implicit VNC transport = %q, want direct", got)
	}
	if got := normalizeVNCConsoleTransport(provider, "ssh"); got != "ssh" {
		t.Fatalf("explicit SSH VNC transport = %q, want ssh", got)
	}
}

func TestNormalizeConsoleProxyTargetRejectsUntrustedHosts(t *testing.T) {
	sshProvider := providerModel.Provider{ConnectionType: "ssh", Endpoint: "node.example"}
	if host, transport, err := normalizeConsoleProxyTarget(sshProvider, "127.0.0.1", ""); err != nil || host != "127.0.0.1" || transport != "ssh" {
		t.Fatalf("loopback SSH target = (%q, %q, %v)", host, transport, err)
	}
	if _, _, err := normalizeConsoleProxyTarget(sshProvider, "169.254.169.254", ""); err == nil {
		t.Fatal("SSH console proxy unexpectedly accepted a non-loopback host")
	}

	directProvider := providerModel.Provider{Endpoint: "https://node.example:8443"}
	if host, transport, err := normalizeConsoleProxyTarget(directProvider, "node.example", "direct"); err != nil || host != "node.example" || transport != "direct" {
		t.Fatalf("trusted direct target = (%q, %q, %v)", host, transport, err)
	}
	if _, _, err := normalizeConsoleProxyTarget(directProvider, "attacker.example", "direct"); err == nil {
		t.Fatal("direct console proxy unexpectedly accepted an untrusted host")
	}
}

func TestSpiceAssetPathAndResponseHeadersAreConstrained(t *testing.T) {
	for _, raw := range []string{"/../secret", "/%2e%2e/secret", "/assets\\evil.js", "/assets%0d%0aHost:evil"} {
		if _, err := cleanSpiceAssetPath(raw); err == nil {
			t.Fatalf("cleanSpiceAssetPath(%q) unexpectedly succeeded", raw)
		}
	}
	if clean, err := cleanSpiceAssetPath("/assets/spice.css"); err != nil || clean != "/assets/spice.css" {
		t.Fatalf("cleanSpiceAssetPath() = (%q, %v)", clean, err)
	}

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	response := &http.Response{Header: http.Header{
		"Content-Type": {"text/html"},
		"Set-Cookie":   {"node-session=unsafe"},
		"Location":     {"https://attacker.example"},
	}}
	copySafeSpiceResponseHeaders(ctx, response)
	if got := recorder.Header().Get("Set-Cookie"); got != "" {
		t.Fatalf("proxied response retained node cookie %q", got)
	}
	if got := recorder.Header().Get("Location"); got != "" {
		t.Fatalf("proxied response retained node redirect %q", got)
	}
	if got := recorder.Header().Get("Content-Security-Policy"); !strings.Contains(got, "connect-src 'self'") {
		t.Fatalf("missing constrained CSP: %q", got)
	}

	body := []byte(`<script>var path = spice_query_var('path', 'websockify');</script>`)
	rewritten := string(rewriteSpiceHTML(body, 42))
	if !strings.Contains(rewritten, "/api/v1/user/instances/42/console/spice-ws") || strings.Contains(rewritten, "'websockify'") {
		t.Fatalf("SPICE websocket path was not forced: %s", rewritten)
	}
}
