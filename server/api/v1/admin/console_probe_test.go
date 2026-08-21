package admin

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	providerModel "oneclickvirt/model/provider"
	consoleService "oneclickvirt/service/console"
)

type scriptedConsoleProbeExecutor struct {
	output string
	err    error
	seen   string
}

func (e *scriptedConsoleProbeExecutor) Execute(string) (string, error) { return e.output, e.err }
func (e *scriptedConsoleProbeExecutor) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	e.seen = command
	return e.output, e.err
}
func (e *scriptedConsoleProbeExecutor) ExecuteWithLogging(string, string) (string, error) {
	return e.output, e.err
}
func (e *scriptedConsoleProbeExecutor) ExecuteRaw(string, time.Duration) (string, error) {
	return e.output, e.err
}
func (e *scriptedConsoleProbeExecutor) ExecuteViaTempScript(string, []string, time.Duration) (string, error) {
	return e.output, e.err
}
func (e *scriptedConsoleProbeExecutor) UploadContent(string, string, os.FileMode) error { return nil }
func (e *scriptedConsoleProbeExecutor) IsHealthy() bool                                 { return true }
func (e *scriptedConsoleProbeExecutor) Reconnect() error                                { return nil }
func (e *scriptedConsoleProbeExecutor) Close() error                                    { return nil }

func TestFindProxmoxConsoleRuntimeUsesObservedTypeInsteadOfStoredType(t *testing.T) {
	resources := []proxmoxConsoleResource{
		{VMID: 400, Name: "actual-qemu", Type: "qemu", Node: "pve-a"},
		{VMID: 401, Name: "actual-lxc", Type: "lxc", Node: "pve-a"},
	}
	for _, tc := range []struct {
		name     string
		instance providerModel.Instance
		wantID   string
		wantType string
	}{
		{
			name: "stored container is actual qemu", instance: providerModel.Instance{Name: "actual-qemu", ProviderVMID: "actual-qemu", InstanceType: "container"},
			wantID: "400", wantType: "qemu",
		},
		{
			name: "stored vm is actual lxc", instance: providerModel.Instance{Name: "actual-lxc", ProviderVMID: "actual-lxc", InstanceType: "vm"},
			wantID: "401", wantType: "lxc",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime, err := findProxmoxConsoleRuntime(resources, tc.instance, "pve-a")
			if err != nil {
				t.Fatalf("findProxmoxConsoleRuntime() error = %v", err)
			}
			if runtime.ID != tc.wantID || runtime.Type != tc.wantType {
				t.Fatalf("runtime = %#v, want (%s, %s)", runtime, tc.wantID, tc.wantType)
			}
		})
	}
}

func TestProxmoxConsoleConfigOnlySelectsLiveSerialProbeCandidates(t *testing.T) {
	if got := proxmoxSerialProbeInterfaces(map[string]interface{}{"serial0": "none", "serial2": "pty"}); strings.Join(got, ",") != "serial2" {
		t.Fatalf("configured serial candidates = %q, want serial2", got)
	}
	if got := proxmoxSerialProbeInterfaces(map[string]interface{}{"serial0": "none"}); strings.Join(got, ",") != "serial0" {
		t.Fatalf("disabled config should retain live serial0 fallback, got %q", got)
	}
}

func TestParseLXDLikeConsoleInfoUsesLiveType(t *testing.T) {
	for _, tc := range []struct {
		raw         string
		wantKind    string
		wantRunning bool
	}{
		{raw: `{"type":"virtual-machine","status":"Running"}`, wantKind: "vm", wantRunning: true},
		{raw: `[{"type":"container","status":"Stopped"}]`, wantKind: "container", wantRunning: false},
		{raw: `{"metadata":{"type":"container","status":"Running"}}`, wantKind: "container", wantRunning: true},
		{raw: `{"status":"Running"}`, wantKind: "", wantRunning: true},
	} {
		kind, running, err := parseLXDLikeConsoleInfo(tc.raw)
		if err != nil || kind != tc.wantKind || running != tc.wantRunning {
			t.Fatalf("parseLXDLikeConsoleInfo(%s) = (%q, %v, %v), want (%q, %v, nil)", tc.raw, kind, running, err, tc.wantKind, tc.wantRunning)
		}
	}
}

func TestRuntimeProbeParsersUseObservedProviderState(t *testing.T) {
	state, port := parseVMwareConsoleProbe("ONECLICKVIRT_CONSOLE\tstate\trunning\nONECLICKVIRT_CONSOLE\tvnc\t5901\n")
	if state != "running" || port != 5901 {
		t.Fatalf("VMware probe = (%q, %d), want (running, 5901)", state, port)
	}

	state, vrde, address, port := parseVirtualBoxConsoleInfo("VMState=\"running\"\nvrde=\"on\"\nvrdeaddress=\"0.0.0.0\"\nvrdeport=\"3389\"\n")
	if state != "running" || !vrde || address != "0.0.0.0" || port != 3389 {
		t.Fatalf("VirtualBox probe = (%q, %v, %q, %d)", state, vrde, address, port)
	}

	state, err := parseMultipassConsoleState(`{"info":{"guest":{"state":"RUNNING"}}}`)
	if err != nil || state != "running" {
		t.Fatalf("Multipass state = (%q, %v), want (running, nil)", state, err)
	}
	if got := parseVagrantConsoleState("1710000000,default,state,running\n"); got != "running" {
		t.Fatalf("Vagrant state = %q, want running", got)
	}
	candidates := collectContainerRuntimeConsoleEndpoints("5900/tcp -> 0.0.0.0:15900\n3389/tcp -> :::13389\n80/tcp -> 0.0.0.0:18080\n", providerModel.Provider{Endpoint: "node.example.test:22"})
	got := make(map[int]consoleEndpointCandidate, len(candidates))
	for _, candidate := range candidates {
		got[candidate.port] = candidate
	}
	for _, port := range []int{15900, 13389, 18080} {
		candidate, ok := got[port]
		if !ok || candidate.host != "node.example.test" || candidate.transport != "direct" {
			t.Fatalf("runtime port %d was not collected as a direct candidate: %#v", port, candidates)
		}
	}
}

func TestLiveProtocolParsersKeepGraphicalAndTerminalChannelsSeparate(t *testing.T) {
	if !containerRuntimeAttachEnabled("true 420 true") || containerRuntimeAttachEnabled("true 420 false") {
		t.Fatal("container Attach should require a running container with OpenStdin")
	}
	pod, ok := kubeVirtRunningPod(`{"items":[{"metadata":{"name":"guest-a"},"status":{"phase":"Running"},"spec":{"containers":[{"name":"main","stdin":true}]}}]}`)
	if !ok || pod.name != "guest-a" || !pod.stdin || pod.attachContainer != "main" {
		t.Fatalf("KubeVirt running pod = %#v, %v", pod, ok)
	}
	if state, serial, display := parseLibvirtConsoleProbe("ONECLICKVIRT_CONSOLE\tstate\trunning\nONECLICKVIRT_CONSOLE\tserial\t/dev/pts/4\nONECLICKVIRT_CONSOLE\tdisplay\tvnc://127.0.0.1:5902\n"); state != "running" || serial == "" || display == "" {
		t.Fatalf("Libvirt probe = (%q, %q, %q)", state, serial, display)
	}
	spice, ok := parseLibvirtSPICEDisplay("spice://node.example.test:5903", providerModel.Provider{Endpoint: "node.example.test:22"})
	if !ok || spice.protocol != consoleProtocolNative || spice.url != "spice://node.example.test:5903" {
		t.Fatalf("Libvirt SPICE target = %#v, %v", spice, ok)
	}
	if _, ok := parseLibvirtSPICEDisplay("spice://127.0.0.1:5903", providerModel.Provider{Endpoint: "node.example.test:22"}); ok {
		t.Fatal("loopback-only SPICE endpoint must not be emitted as a user native link")
	}
}

func TestLiveLXDLikeProxyConfigurationUsesHandshakeDiscovery(t *testing.T) {
	listener := newConsoleProbeListener(t, func(conn net.Conn) {
		_, _ = conn.Write([]byte("RFB 003.008\n"))
	})
	defer listener.Close()
	host, port := consoleProbeListenerAddress(t, listener)

	raw := `{"metadata":{"expanded_devices":{"desktop":{"type":"proxy","listen":"tcp:0.0.0.0:` + strconv.Itoa(port) + `"}}}}`
	candidates, err := collectLXDLikeProxyConsoleEndpoints(raw, providerModel.Provider{
		Endpoint: joinConsoleURLHostPort(host, 22), ConnectionType: "local",
	})
	if err != nil || len(candidates) != 1 {
		t.Fatalf("live LXD proxy candidates = %#v, %v", candidates, err)
	}
	vnc, native := detectMappedConsoleEndpoints(730004, candidates)
	if len(vnc) != 1 || vnc[0].port != port || len(native) != 0 {
		t.Fatalf("live LXD proxy protocol discovery = vnc:%#v native:%#v", vnc, native)
	}
}

func TestLXDLikeProxyCandidatesKeepDirectAndNodeLocalPathsSeparate(t *testing.T) {
	provider := providerModel.Provider{
		Endpoint: "node.example.test:22", Username: "root", ConnectionType: "ssh",
	}
	candidates := lxdLikeProxyConsoleEndpointCandidates("tcp:0.0.0.0:15900", provider)
	if len(candidates) != 2 {
		t.Fatalf("wildcard proxy candidates = %#v, want direct and SSH paths", candidates)
	}
	got := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		got[candidate.transport+":"+candidate.host+":"+strconv.Itoa(candidate.port)] = true
	}
	if !got["direct:node.example.test:15900"] || !got["ssh:127.0.0.1:15900"] {
		t.Fatalf("wildcard proxy candidates = %#v", candidates)
	}
	if unexpected := lxdLikeProxyConsoleEndpointCandidates("tcp:192.0.2.77:15900", provider); len(unexpected) != 0 {
		t.Fatalf("untrusted node listener became a console candidate: %#v", unexpected)
	}
}

func TestConsoleEndpointMergeKeepsEveryLiveSourceInProbeBudget(t *testing.T) {
	provider := providerModel.Provider{ID: 730009, Endpoint: "node.example.test:22"}
	newCandidates := func(host, transport string, start int) []consoleEndpointCandidate {
		result := make([]consoleEndpointCandidate, 0, consoleMappedEndpointProbeLimit)
		for offset := 0; offset < consoleMappedEndpointProbeLimit; offset++ {
			result = append(result, consoleEndpointCandidate{
				host: host, port: start + offset, transport: transport, provider: provider,
			})
		}
		return result
	}
	runtime := newCandidates("127.0.0.1", "ssh", 12000)
	instance := newCandidates("198.51.100.42", consolePublicInstanceEndpointTransport, 22000)
	mapped := newCandidates("node.example.test", "direct", 32000)

	merged := mergeConsoleEndpointCandidates(runtime, instance, mapped)
	if len(merged) != consoleMappedEndpointProbeLimit {
		t.Fatalf("merged candidate count = %d, want %d", len(merged), consoleMappedEndpointProbeLimit)
	}
	counts := map[string]int{}
	for _, candidate := range merged {
		switch candidate.transport {
		case "ssh":
			counts["runtime"]++
		case consolePublicInstanceEndpointTransport:
			counts["instance"]++
		case "direct":
			counts["mapped"]++
		}
	}
	for _, source := range []string{"runtime", "instance", "mapped"} {
		if counts[source] != consoleMappedEndpointProbeLimit/3 {
			t.Fatalf("%s candidates = %d, want %d from round-robin merge: %#v", source, counts[source], consoleMappedEndpointProbeLimit/3, merged)
		}
	}
}

func TestInstanceConsoleCandidatesProbeGraphicalPortsAcrossAllAddressesFirst(t *testing.T) {
	instance := providerModel.Instance{
		PublicIP:    "198.51.100.10",
		PublicIPv6:  "2001:db8::10",
		PrivateIP:   "10.0.0.10",
		IPv6Address: "fd00::10",
	}
	candidates := collectInstanceConsoleEndpointCandidates(instance, providerModel.Provider{ID: 730010})
	if len(candidates) < 4 {
		t.Fatalf("instance candidates = %#v, want at least one graphical probe per address", candidates)
	}
	wantHosts := []string{"198.51.100.10", "2001:db8::10", "10.0.0.10", "fd00::10"}
	for index, wantHost := range wantHosts {
		candidate := candidates[index]
		if candidate.port != 3389 || candidate.host != wantHost {
			t.Fatalf("candidate %d = %#v, want RDP probe for %s", index, candidate, wantHost)
		}
	}
}

func TestLibvirtRuntimeObservationUsesLiveConnection(t *testing.T) {
	observation := parseLibvirtConsoleObservation("ONECLICKVIRT_CONSOLE\turi\tlxc:///\nONECLICKVIRT_CONSOLE\tkind\tcontainer\nONECLICKVIRT_CONSOLE\tstate\trunning\n")
	if observation.uri != "lxc:///" || observation.kind != "container" || observation.state != "running" {
		t.Fatalf("Libvirt observation = %#v", observation)
	}
	command := libvirtConsoleProbeCommand("guest-a")
	check := exec.Command("sh", "-n")
	check.Stdin = strings.NewReader(command)
	if output, err := check.CombinedOutput(); err != nil {
		t.Fatalf("Libvirt probe command has invalid shell syntax: %v\n%s\n%s", err, output, command)
	}
	if !strings.Contains(command, "qemu:///system") || !strings.Contains(command, "lxc:///") {
		t.Fatalf("Libvirt probe did not test both live connections: %s", command)
	}
}

func TestLifecycleConsoleInvalidationClearsEveryRuntimeCache(t *testing.T) {
	const instanceID uint = 730005
	consoleRuntimeProbeMu.Lock()
	consoleRuntimeProbeCache[strconv.FormatUint(uint64(instanceID), 10)+":runtime"] = consoleRuntimeProbeCacheEntry{resolvedAt: time.Now()}
	consoleRuntimeProbeMu.Unlock()
	consoleEndpointProbeMu.Lock()
	consoleEndpointProbeCache["vnc:"+strconv.FormatUint(uint64(instanceID), 10)+":direct:127.0.0.1:5900"] = consoleEndpointProbeState{checkedAt: time.Now()}
	consoleEndpointProbeMu.Unlock()
	proxmoxConsoleRuntimeMu.Lock()
	proxmoxConsoleRuntimeCache[strconv.FormatUint(uint64(instanceID), 10)+":pve"] = proxmoxConsoleRuntimeCacheEntry{updatedAt: time.Now()}
	proxmoxConsoleRuntimeMu.Unlock()
	spiceHealthMu.Lock()
	spiceHealthCache[instanceID] = spiceHealthState{checkedAt: time.Now()}
	spiceHealthMu.Unlock()

	consoleService.InvalidateInstanceConsoleCaches(instanceID)

	consoleRuntimeProbeMu.Lock()
	_, runtimeCached := consoleRuntimeProbeCache[strconv.FormatUint(uint64(instanceID), 10)+":runtime"]
	consoleRuntimeProbeMu.Unlock()
	consoleEndpointProbeMu.Lock()
	_, endpointCached := consoleEndpointProbeCache["vnc:"+strconv.FormatUint(uint64(instanceID), 10)+":direct:127.0.0.1:5900"]
	consoleEndpointProbeMu.Unlock()
	proxmoxConsoleRuntimeMu.Lock()
	_, proxmoxCached := proxmoxConsoleRuntimeCache[strconv.FormatUint(uint64(instanceID), 10)+":pve"]
	proxmoxConsoleRuntimeMu.Unlock()
	spiceHealthMu.Lock()
	_, spiceCached := spiceHealthCache[instanceID]
	spiceHealthMu.Unlock()
	if runtimeCached || endpointCached || proxmoxCached || spiceCached {
		t.Fatalf("lifecycle invalidation left caches: runtime=%v endpoint=%v proxmox=%v spice=%v", runtimeCached, endpointCached, proxmoxCached, spiceCached)
	}
}

func TestVMwareConsoleProbeCommandAndTerminalPlansAreShellValid(t *testing.T) {
	commands := []string{
		vmwareConsoleProbeCommand("guest-a", "/var/lib/oneclickvirt/vmware"),
		kubeVirtPodTerminalCommand("pod-a", "sh"),
		kubeVirtPodAttachCommand("pod-a"),
		multipassExecConsoleCommand("guest-a", "sh"),
	}
	for _, command := range commands {
		check := exec.Command("sh", "-n")
		check.Stdin = strings.NewReader(command)
		if output, err := check.CombinedOutput(); err != nil {
			t.Fatalf("console command has invalid shell syntax: %v\n%s\n%s", err, output, command)
		}
	}
}

func TestObservedPVERuntimeCarriesActualNodeAndStatus(t *testing.T) {
	runtime, err := findProxmoxConsoleRuntime([]proxmoxConsoleResource{{VMID: 900, Name: "migrated", Type: "qemu", Node: "pve-b", Status: "running"}}, providerModel.Instance{Name: "migrated", ProviderVMID: "900", InstanceType: "container"}, "pve-a")
	if err != nil || runtime.Type != "qemu" || runtime.Node != "pve-b" || runtime.Status != "running" {
		t.Fatalf("PVE observed runtime = %#v, %v", runtime, err)
	}
}

func TestResolveObservedConsoleTerminalPlanRejectsUnprobedProtocol(t *testing.T) {
	probe := consoleRuntimeProbe{
		runtimeID:     "400",
		terminalPlans: []InstanceConsoleTerminalPlan{{Protocol: consoleProtocolSerial, Command: "qm terminal '400'"}},
	}
	plan, err := resolveObservedConsoleTerminalPlan(probe, consoleProtocolSerial)
	if err != nil || plan.Command != "qm terminal '400'" {
		t.Fatalf("serial plan = %#v, %v", plan, err)
	}
	if _, err := resolveObservedConsoleTerminalPlan(probe, consoleProtocolExec); err == nil {
		t.Fatal("unprobed exec protocol unexpectedly resolved")
	}
}

func TestConsoleProbeBoundedCommandIsShellValid(t *testing.T) {
	command := consoleProbeBoundedCommand("lxc exec 'guest one' -- sh -c ':'") + " >/dev/null 2>&1 && printf sh"
	check := exec.Command("sh", "-n")
	check.Stdin = strings.NewReader(command)
	if output, err := check.CombinedOutput(); err != nil {
		t.Fatalf("bounded probe command has invalid shell syntax: %v\n%s", err, output)
	}
}

func TestInteractiveConsoleProbeRequiresNodeSideTimeoutCompletion(t *testing.T) {
	command := consoleInteractiveProbeCommand("qm terminal '400' --iface 'serial0'")
	check := exec.Command("sh", "-n")
	check.Stdin = strings.NewReader(command)
	if output, err := check.CombinedOutput(); err != nil {
		t.Fatalf("interactive probe command has invalid shell syntax: %v\n%s\n%s", err, output, command)
	}
	if !strings.Contains(command, "command -v timeout") || !strings.Contains(command, consoleInteractiveProbeTimedOut) {
		t.Fatalf("interactive probe omitted safe timeout markers: %s", command)
	}

	for _, tc := range []struct {
		name      string
		output    string
		err       error
		available bool
	}{
		{name: "kept interactive until node timeout", output: consoleInteractiveProbeMarker + "\n" + consoleInteractiveProbeTimedOut + "\n", available: true},
		{name: "immediate clean exit", output: consoleInteractiveProbeMarker + "\n", available: false},
		{name: "immediate PVE usage error", output: consoleInteractiveProbeMarker + "\n400 Parameter verification failed.\n", err: errors.New("exit status 1"), available: false},
		{name: "outer executor deadline", output: consoleInteractiveProbeMarker + "\n", err: errors.New("command execution timeout after 6s"), available: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executor := &scriptedConsoleProbeExecutor{output: tc.output, err: tc.err}
			available, reason := probeInteractiveConsole(executor, "qm terminal '400' --iface 'serial0'", "PVE QEMU 串口")
			if available != tc.available {
				t.Fatalf("probeInteractiveConsole() = (%v, %q), want available=%v", available, reason, tc.available)
			}
			if !strings.Contains(executor.seen, consoleInteractiveProbeMarker) {
				t.Fatalf("probe did not use node-side marker command: %s", executor.seen)
			}
		})
	}
}

func TestProxmoxSerialProbeCandidatesComeFromLiveConfigButAlwaysHaveLiveFallback(t *testing.T) {
	if got := proxmoxSerialProbeInterfaces(map[string]interface{}{"serial0": "none", "serial2": "socket"}); strings.Join(got, ",") != "serial2" {
		t.Fatalf("configured candidates = %#v, want serial2", got)
	}
	if got := proxmoxSerialProbeInterfaces(map[string]interface{}{"serial0": "none"}); strings.Join(got, ",") != "serial0" {
		t.Fatalf("missing serial config candidates = %#v, want serial0 live fallback", got)
	}
}

func TestLXDLikeInteractiveConsoleProbeIsShellValid(t *testing.T) {
	for _, command := range []string{
		consoleInteractiveProbeCommand("incus console 'guest-a' --type=console"),
		lxdLikeSPICESocketProbeCommand("guest-a"),
		spiceSetupCommand("guest-a", 42, providerModel.Provider{}),
	} {
		check := exec.Command("sh", "-n")
		check.Stdin = strings.NewReader(command)
		if output, err := check.CombinedOutput(); err != nil {
			t.Fatalf("console probe has invalid shell syntax: %v\n%s\n%s", err, output, command)
		}
	}
	command := lxdLikeSPICESocketProbeCommand("guest-a")
	if !strings.Contains(command, "qemu.spice") || !strings.Contains(command, "ONECLICKVIRT_SPICE_SOCKET") {
		t.Fatalf("SPICE socket probe omitted required discovery markers: %s", command)
	}
	for _, identifier := range []string{"guest-a", "guest.a", "Guest_2", "guest..a"} {
		if !isLXDLikeConsoleInstanceName(identifier) {
			t.Fatalf("safe LXD/Incus instance identifier rejected: %q", identifier)
		}
	}
	for _, identifier := range []string{"", "../guest", "guest/child", "guest\\child", "guest\nname"} {
		if isLXDLikeConsoleInstanceName(identifier) {
			t.Fatalf("unsafe LXD/Incus instance identifier accepted: %q", identifier)
		}
	}
}

func TestLiveSerialAndKubeVirtAttachProbesRequireInteractiveSession(t *testing.T) {
	for _, tc := range []struct {
		name  string
		probe func(*scriptedConsoleProbeExecutor) (bool, string)
		wants []string
	}{
		{
			name: "incus serial",
			probe: func(executor *scriptedConsoleProbeExecutor) (bool, string) {
				return probeLXDLikeSerialConsole(executor, "incus", "guest-a")
			},
			wants: []string{"incus console", "guest-a", "--type=console", consoleInteractiveProbeMarker},
		},
		{
			name: "kubevirt attach",
			probe: func(executor *scriptedConsoleProbeExecutor) (bool, string) {
				return probeKubeVirtPodAttach(executor, kubeVirtPodConsoleInfo{name: "pod-a", stdin: true, attachContainer: "main"})
			},
			wants: []string{"kubectl attach --stdin=false --tty=false", "kubevirt-vms", "main", "pod-a", consoleInteractiveProbeMarker},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			executor := &scriptedConsoleProbeExecutor{output: consoleInteractiveProbeMarker + "\n" + consoleInteractiveProbeTimedOut + "\n"}
			available, reason := tc.probe(executor)
			if !available || reason != "" {
				t.Fatalf("probe = (%v, %q)", available, reason)
			}
			for _, want := range tc.wants {
				if !strings.Contains(executor.seen, want) {
					t.Fatalf("probe omitted %q: %s", want, executor.seen)
				}
			}
			if strings.Contains(executor.seen, "--show-log") {
				t.Fatalf("probe did not start the real interactive command: %s", executor.seen)
			}
		})
	}
}

func TestLibvirtConsoleRequiresObservedState(t *testing.T) {
	for _, tc := range []struct {
		state       string
		wantRunning bool
		wantReason  bool
	}{
		{state: "running", wantRunning: true},
		{state: "shut off", wantReason: true},
		{state: "", wantReason: true},
	} {
		running, reason := libvirtConsoleRunningState(tc.state)
		if running != tc.wantRunning || (reason != "") != tc.wantReason {
			t.Fatalf("libvirtConsoleRunningState(%q) = (%v, %q), want running=%v reason=%v", tc.state, running, reason, tc.wantRunning, tc.wantReason)
		}
	}
}

func TestRefreshVNCConsoleTargetRequiresRFBBanner(t *testing.T) {
	listener := newConsoleProbeListener(t, func(conn net.Conn) {
		_, _ = conn.Write([]byte("RFB 003.008\n"))
	})
	defer listener.Close()
	host, port := consoleProbeListenerAddress(t, listener)
	target := refreshVNCConsoleTarget(consoleTarget{
		protocol: consoleProtocolVNC, host: host, port: port, transport: "direct", available: true, instanceID: 710001,
	})
	if !target.available || target.reason != "" {
		t.Fatalf("healthy RFB target = %#v", target)
	}

	invalid := newConsoleProbeListener(t, func(conn net.Conn) {
		_, _ = conn.Write([]byte("HTTP/1.1 200"))
	})
	defer invalid.Close()
	host, port = consoleProbeListenerAddress(t, invalid)
	target = refreshVNCConsoleTarget(consoleTarget{
		protocol: consoleProtocolVNC, host: host, port: port, transport: "direct", available: true, instanceID: 710002,
	})
	if target.available || !strings.Contains(target.reason, "RFB") {
		t.Fatalf("non-RFB target = %#v", target)
	}
}

func TestNativeVNCAndSPICEProbesRequireProtocolReplies(t *testing.T) {
	for _, tc := range []struct {
		name      string
		scheme    string
		response  []byte
		available bool
	}{
		{name: "native VNC", scheme: "vnc", response: []byte("RFB 003.008\n"), available: true},
		{name: "native SPICE", scheme: "spice", response: spiceLinkReplyForTest(2, 2, 0), available: true},
		{name: "wrong VNC endpoint", scheme: "vnc", response: []byte("HTTP/1.1 200"), available: false},
		{name: "wrong SPICE endpoint", scheme: "spice", response: []byte("RFB 003.008\n"), available: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()
			_ = client.SetDeadline(time.Now().Add(time.Second))
			_ = server.SetDeadline(time.Now().Add(time.Second))
			go func() {
				if tc.scheme == "spice" {
					request := make([]byte, 34)
					_, _ = io.ReadFull(server, request)
				}
				_, _ = server.Write(tc.response)
			}()
			available, reason := probeNativeConsoleConn(tc.scheme, client)
			if available != tc.available {
				t.Fatalf("probeNativeConsoleEndpoint() = (%v, %q), want available=%v", available, reason, tc.available)
			}
		})
	}
}

func spiceLinkReplyForTest(major, minor, size uint32) []byte {
	reply := make([]byte, 16)
	copy(reply[:4], []byte("REDQ"))
	binary.LittleEndian.PutUint32(reply[4:8], major)
	binary.LittleEndian.PutUint32(reply[8:12], minor)
	binary.LittleEndian.PutUint32(reply[12:16], size)
	return reply
}

func TestRefreshVNCConsoleTargetCoalescesConcurrentProbe(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	listener := newConsoleProbeListener(t, func(conn net.Conn) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		_, _ = conn.Write([]byte("RFB 003.008\n"))
	})
	defer listener.Close()
	host, port := consoleProbeListenerAddress(t, listener)
	target := consoleTarget{protocol: consoleProtocolVNC, host: host, port: port, transport: "direct", available: true, instanceID: 710003}

	const callers = 8
	var wg sync.WaitGroup
	results := make(chan consoleTarget, callers)
	for index := 0; index < callers; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- refreshVNCConsoleTarget(target)
		}()
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("RFB probe did not reach listener")
	}
	close(release)
	wg.Wait()
	close(results)
	for result := range results {
		if !result.available {
			t.Fatalf("concurrent VNC probe reported unavailable: %#v", result)
		}
	}
	if got := listener.accepts.Load(); got != 1 {
		t.Fatalf("VNC health probe connections = %d, want 1", got)
	}
}

func TestNativeConsoleProbesValidateProtocolHandshakes(t *testing.T) {
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		request := make([]byte, 19)
		_, _ = io.ReadFull(server, request)
		_, _ = server.Write([]byte{0x03, 0x00, 0x00, 0x0b, 0x06, 0xd0, 0x00, 0x00, 0x12, 0x34, 0x00})
	}()
	if available, reason := probeRDPHandshake(client); !available || reason != "" {
		t.Fatalf("valid RDP handshake = (%v, %q)", available, reason)
	}
	_ = client.Close()

	client, server = net.Pipe()
	go func() {
		defer server.Close()
		_, _ = server.Write([]byte("SSH-2.0-test-server\r\n"))
	}()
	if available, reason := probeSSHBanner(client); !available || reason != "" {
		t.Fatalf("valid SSH banner = (%v, %q)", available, reason)
	}
	_ = client.Close()

	client, server = net.Pipe()
	go func() {
		defer server.Close()
		request := make([]byte, 19)
		_, _ = io.ReadFull(server, request)
		_, _ = server.Write([]byte("HTTP/1.1 200 OK\r\n"))
	}()
	if available, reason := probeRDPHandshake(client); available || !strings.Contains(reason, "TPKT") {
		t.Fatalf("invalid RDP handshake = (%v, %q)", available, reason)
	}
	_ = client.Close()
}

func TestMappedConsoleCandidatesIncludeNonStandardTCPMappings(t *testing.T) {
	ports := []providerModel.Port{
		{HostPort: 18443, GuestPort: 8443, Protocol: "tcp", Status: "active"},
		{HostPort: 19010, HostPortEnd: 19011, GuestPort: 9010, GuestPortEnd: 9011, Protocol: "tcp", Status: "active"},
		{HostPort: 13389, GuestPort: 3389, Protocol: "tcp", Status: "active"},
		{HostPort: 19999, GuestPort: 5999, Protocol: "tcp", Status: "active", MappingType: "controller"},
	}
	ports = append(ports, providerModel.Port{HostPort: 19012, GuestPort: 0, Protocol: "tcp", Status: "active"})
	candidates := collectMappedConsoleEndpoints(ports, providerModel.Provider{Endpoint: "node.example.test:22"})
	got := make(map[int]bool, len(candidates))
	for _, candidate := range candidates {
		got[candidate.port] = candidate.host == "node.example.test" && candidate.transport == "direct"
	}
	for _, port := range []int{18443, 19010, 19011, 13389, 19012} {
		if !got[port] {
			t.Fatalf("non-standard mapped endpoint %d was not collected: %#v", port, candidates)
		}
	}
	if got[19999] {
		t.Fatalf("controller-owned mapping must not become an instance console candidate: %#v", candidates)
	}
}

func TestRuntimePortMappingUsesHandshakeInsteadOfGuestPort(t *testing.T) {
	listener := newConsoleProbeListener(t, func(conn net.Conn) {
		_, _ = conn.Write([]byte("RFB 003.008\n"))
	})
	defer listener.Close()
	host, port := consoleProbeListenerAddress(t, listener)
	provider := providerModel.Provider{Endpoint: joinConsoleURLHostPort(host, 22)}
	candidates := collectContainerRuntimeConsoleEndpoints("8443/tcp -> 0.0.0.0:"+strconv.Itoa(port)+"\n", provider)
	if len(candidates) != 1 || candidates[0].port != port {
		t.Fatalf("non-standard runtime candidate = %#v", candidates)
	}
	vnc, native := detectMappedConsoleEndpoints(730003, candidates)
	if len(vnc) != 1 || vnc[0].port != port || len(native) != 0 {
		t.Fatalf("non-standard runtime VNC discovery = vnc:%#v native:%#v", vnc, native)
	}
}

func TestRuntimeLoopbackMappingUsesConfiguredNodeTransport(t *testing.T) {
	candidates := collectContainerRuntimeConsoleEndpoints(
		"8443/tcp -> 127.0.0.1:15900\n",
		providerModel.Provider{Endpoint: "node.example.test:22", Username: "root", ConnectionType: "ssh"},
	)
	if len(candidates) != 1 || candidates[0].host != "127.0.0.1" || candidates[0].transport != "ssh" {
		t.Fatalf("loopback runtime mapping = %#v, want SSH tunnel candidate", candidates)
	}
}

func TestMappedConsoleProtocolDiscoveryUsesActualHandshake(t *testing.T) {
	vncListener := newConsoleProbeListener(t, func(conn net.Conn) {
		_, _ = conn.Write([]byte("RFB 003.008\n"))
	})
	defer vncListener.Close()
	vncHost, vncPort := consoleProbeListenerAddress(t, vncListener)

	rdpListener := newConsoleProbeListener(t, func(conn net.Conn) {
		request := make([]byte, 19)
		count, _ := io.ReadFull(conn, request)
		if count == len(request) && request[0] == 0x03 {
			_, _ = conn.Write([]byte{0x03, 0x00, 0x00, 0x0b, 0x06, 0xd0, 0x00, 0x00, 0x12, 0x34, 0x00})
		}
	})
	defer rdpListener.Close()
	rdpHost, rdpPort := consoleProbeListenerAddress(t, rdpListener)

	vnc, native := detectMappedConsoleEndpoints(730001, []consoleEndpointCandidate{
		{host: vncHost, port: vncPort, transport: "direct"},
		{host: rdpHost, port: rdpPort, transport: "direct"},
	})
	if len(vnc) != 1 || vnc[0].port != vncPort {
		t.Fatalf("live VNC discovery = %#v", vnc)
	}
	wantRDP := "rdp://" + joinConsoleURLHostPort(rdpHost, rdpPort)
	if len(native) != 1 || native[0].protocol != "rdp" || native[0].url != wantRDP {
		t.Fatalf("live RDP discovery = %#v, want %q", native, wantRDP)
	}
}

func TestMappedTelnetProtocolUsesLiveGreeting(t *testing.T) {
	listener := newConsoleProbeListener(t, func(conn net.Conn) {
		_, _ = conn.Write([]byte{255, 251, 1})
	})
	defer listener.Close()
	host, port := consoleProbeListenerAddress(t, listener)

	_, native := detectMappedConsoleEndpoints(730006, []consoleEndpointCandidate{{host: host, port: port, transport: "direct"}})
	want := "telnet://" + joinConsoleURLHostPort(host, port)
	if len(native) != 1 || native[0].protocol != "telnet" || native[0].url != want {
		t.Fatalf("live Telnet discovery = %#v, want %q", native, want)
	}
}

func TestPrivateInstanceNativeEndpointStaysDiagnosticOnly(t *testing.T) {
	listener := newConsoleProbeListener(t, func(conn net.Conn) {
		_, _ = conn.Write([]byte("SSH-2.0-test\\r\\n"))
	})
	defer listener.Close()
	host, port := consoleProbeListenerAddress(t, listener)

	result := detectConsoleEndpointCandidates(730008, []consoleEndpointCandidate{{
		host: host, port: port, transport: consoleInstanceEndpointTransport,
	}})
	if len(result.native) != 0 || !strings.Contains(result.reason, "实例私有地址") {
		t.Fatalf("private instance SSH endpoint = %#v", result)
	}
}

func TestEndpointProbeKeepsFirstLiveFailureDiagnostic(t *testing.T) {
	listener := newConsoleProbeListener(t, func(conn net.Conn) {
		_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\n\r\n"))
	})
	defer listener.Close()
	host, port := consoleProbeListenerAddress(t, listener)

	result := detectConsoleEndpointCandidates(730007, []consoleEndpointCandidate{{host: host, port: port, transport: "direct"}})
	if len(result.vnc) != 0 || len(result.native) != 0 || !strings.Contains(result.reason, "实际协议探测失败") {
		t.Fatalf("endpoint probe result = %#v", result)
	}
}

func TestProxmoxDirectRuntimeStatusParsesEnvelopeAndFlatResponse(t *testing.T) {
	for _, raw := range []string{
		`{"status":"running"}`,
		`{"data":{"status":"stopped"}}`,
	} {
		status, ok := parseProxmoxConsoleDirectStatus(raw)
		if !ok || status == "" {
			t.Fatalf("direct PVE status = (%q, %v) for %s", status, ok, raw)
		}
	}
	command := proxmoxConsoleDirectStatusCommand("pve-a", "qemu", "900")
	if !strings.Contains(command, "/nodes/pve-a/qemu/900/status/current") {
		t.Fatalf("direct PVE status command = %s", command)
	}
}

func TestMappedSPICEProtocolUsesNativeConsoleAction(t *testing.T) {
	listener := newConsoleProbeListener(t, func(conn net.Conn) {
		header := make([]byte, 4)
		count, _ := io.ReadFull(conn, header)
		switch {
		case count == len(header) && header[0] == 0x03:
			// Reject the preceding RDP hello so the detector moves to SPICE.
			_, _ = io.ReadFull(conn, make([]byte, 15))
			_, _ = conn.Write([]byte("not-rdp"))
		case count == len(header) && string(header) == "REDQ":
			_, _ = io.ReadFull(conn, make([]byte, 30))
			_, _ = conn.Write(spiceLinkReplyForTest(2, 2, 0))
		}
	})
	defer listener.Close()
	host, port := consoleProbeListenerAddress(t, listener)

	_, native := detectMappedConsoleEndpoints(730002, []consoleEndpointCandidate{{host: host, port: port, transport: "direct"}})
	want := "spice://" + joinConsoleURLHostPort(host, port)
	if len(native) != 1 || native[0].protocol != consoleProtocolNative || native[0].url != want {
		t.Fatalf("live SPICE discovery = %#v, want native %q", native, want)
	}
}

type consoleProbeListener struct {
	net.Listener
	accepts atomic.Int32
}

func newConsoleProbeListener(t *testing.T, handle func(net.Conn)) *consoleProbeListener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	wrapped := &consoleProbeListener{Listener: listener}
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			wrapped.accepts.Add(1)
			go func() {
				defer conn.Close()
				handle(conn)
			}()
		}
	}()
	return wrapped
}

func consoleProbeListenerAddress(t *testing.T, listener net.Listener) (string, int) {
	t.Helper()
	host, rawPort, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("listener address: %v", err)
	}
	port, err := strconv.Atoi(rawPort)
	if err != nil {
		t.Fatalf("listener port: %v", err)
	}
	return host, port
}
