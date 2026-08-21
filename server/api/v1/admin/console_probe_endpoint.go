package admin

// Endpoint checks are isolated from provider runtime probes so both pieces stay
// small: the runtime layer decides which channels exist, while this layer
// validates discovered network endpoints before advertising them to the UI.

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"

	"golang.org/x/sync/singleflight"
)

const (
	consoleEndpointProbeTTL           = 10 * time.Second
	consoleEndpointProbeTimeout       = 4 * time.Second
	consoleGreetingProbeTimeout       = 350 * time.Millisecond
	consoleMappedEndpointProbeLimit   = 24
	consoleMappedEndpointProbeWorkers = 4
)

type consoleEndpointProbeState struct {
	available bool
	protocol  string
	reason    string
	checkedAt time.Time
}

// consoleEndpointProbeResult keeps the result of a live endpoint fingerprint
// separate from the set of protocols the panel can render. A detected Telnet
// or an unknown listener is still useful evidence, but it must not become a
// misleading VNC/RDP button merely because its port accepted TCP.
type consoleEndpointProbeResult struct {
	protocol string
	reason   string
}

// consoleEndpointCandidate is a TCP endpoint that belongs to the selected
// instance. Its protocol is deliberately unknown until a live probe proves it.
// Keeping the source endpoint separate from the detected capability avoids
// using guest port numbers or the stored instance type as a proxy for support.
type consoleEndpointCandidate struct {
	host      string
	port      int
	transport string
	provider  providerModel.Provider
}

var (
	consoleEndpointProbeMu    sync.Mutex
	consoleEndpointProbeCache = make(map[string]consoleEndpointProbeState)
	consoleEndpointProbeGroup singleflight.Group
	consoleEndpointProbeSlots = make(chan struct{}, 8)
)

// mappedConsoleEndpoints uses one indexed query for the selected instance to
// discover exposed TCP endpoints. It is intentionally not called from list
// APIs, so it cannot turn an instance list into an N+1 query pattern. Every
// bounded mapping is protocol-fingerprinted before it becomes a capability.
func mappedConsoleEndpoints(inst providerModel.Instance, p providerModel.Provider) []consoleEndpointCandidate {
	if inst.ID == 0 || global.APP_DB == nil {
		return nil
	}
	var ports []providerModel.Port
	if err := global.APP_DB.Select("host_port", "host_port_end", "guest_port", "guest_port_end", "port_count", "protocol", "status", "mapping_type").
		Where("instance_id = ? AND status = ?", inst.ID, "active").Order("id ASC").Find(&ports).Error; err != nil {
		return nil
	}
	return collectMappedConsoleEndpoints(ports, p)
}

func collectMappedConsoleEndpoints(ports []providerModel.Port, p providerModel.Provider) []consoleEndpointCandidate {
	host := consoleHost(p)
	if host == "" {
		return nil
	}
	candidates := make([]consoleEndpointCandidate, 0, minInt(consoleMappedEndpointProbeLimit, len(ports)))
	addCandidate := func(port int) {
		candidates = appendConsoleEndpointCandidate(candidates, consoleEndpointCandidate{
			host: host, port: port, transport: "direct", provider: p,
		})
	}
	for _, port := range ports {
		if strings.EqualFold(port.MappingType, "controller") || !(strings.EqualFold(port.Protocol, "tcp") || strings.EqualFold(port.Protocol, "both") || strings.TrimSpace(port.Protocol) == "") {
			continue
		}
		hostEnd := effectiveConsoleMappedPortEnd(port.HostPort, port.HostPortEnd, port.PortCount)
		if port.HostPort <= 0 || hostEnd < port.HostPort {
			continue
		}
		for hostPort := port.HostPort; hostPort <= hostEnd; hostPort++ {
			addCandidate(hostPort)
			if len(candidates) >= consoleMappedEndpointProbeLimit {
				break
			}
		}
	}
	return candidates
}

func effectiveConsoleMappedPortEnd(start, end, count int) int {
	if end >= start && end > 0 {
		return end
	}
	if count > 1 {
		return start + count - 1
	}
	return start
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

// mergeConsoleEndpointCandidates keeps discovery bounded across runtime data,
// dedicated instance addresses, and indexed panel rows. It takes one candidate
// from each source in turn instead of exhausting the first source. A manually
// added port range must not prevent an actual VNC/RDP/SPICE service on another
// source from being probed. No database call occurs here.
func mergeConsoleEndpointCandidates(groups ...[]consoleEndpointCandidate) []consoleEndpointCandidate {
	merged := make([]consoleEndpointCandidate, 0, consoleMappedEndpointProbeLimit)
	next := make([]int, len(groups))
	for len(merged) < consoleMappedEndpointProbeLimit {
		added := false
		for index, group := range groups {
			for next[index] < len(group) {
				candidate := group[next[index]]
				next[index]++
				before := len(merged)
				merged = appendConsoleEndpointCandidate(merged, candidate)
				if len(merged) > before {
					added = true
					break
				}
			}
			if len(merged) >= consoleMappedEndpointProbeLimit {
				return merged
			}
		}
		if !added {
			return merged
		}
	}
	return merged
}

func appendConsoleEndpointCandidate(candidates []consoleEndpointCandidate, candidate consoleEndpointCandidate) []consoleEndpointCandidate {
	if len(candidates) >= consoleMappedEndpointProbeLimit || candidate.port < 1 || candidate.port > 65535 {
		return candidates
	}
	candidate.host = strings.Trim(strings.TrimSpace(candidate.host), "[]")
	candidate.transport = strings.ToLower(strings.TrimSpace(candidate.transport))
	if candidate.host == "" || candidate.transport == "" {
		return candidates
	}
	for _, existing := range candidates {
		if existing.port == candidate.port && existing.provider.ID == candidate.provider.ID &&
			strings.EqualFold(existing.transport, candidate.transport) && consoleHostsEqual(existing.host, candidate.host) {
			return candidates
		}
	}
	return append(candidates, candidate)
}

func joinConsoleURLHostPort(host string, port int) string {
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return "[" + host + "]:" + strconv.Itoa(port)
	}
	return host + ":" + strconv.Itoa(port)
}

// detectMappedConsoleEndpoints fingerprints only the selected instance's own
// bounded TCP mappings. Candidates may come from the indexed panel mapping or
// a runtime-reported mapping; protocol and instance class are both unknown
// until the live handshake proves them. A worker pool keeps a large range from
// turning one detail request into a long serial wait.
func detectMappedConsoleEndpoints(instanceID uint, candidates []consoleEndpointCandidate) ([]consoleDiscoveredEndpoint, []nativeConsoleTarget) {
	result := detectConsoleEndpointCandidates(instanceID, candidates)
	return result.vnc, result.native
}

type detectedConsoleEndpoints struct {
	vnc    []consoleDiscoveredEndpoint
	native []nativeConsoleTarget
	reason string
}

// detectConsoleEndpointCandidates probes a bounded list in parallel and keeps
// the first useful diagnostic. The caller can surface it when none of the
// protocols is renderable, rather than returning a generic 404/info dialog.
func detectConsoleEndpointCandidates(instanceID uint, candidates []consoleEndpointCandidate) detectedConsoleEndpoints {
	if len(candidates) == 0 {
		return detectedConsoleEndpoints{}
	}
	results := make([]consoleEndpointProbeResult, len(candidates))
	workers := minInt(consoleMappedEndpointProbeWorkers, len(candidates))
	jobs := make(chan int)
	var wait sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for index := range jobs {
				candidate := candidates[index]
				results[index].protocol, results[index].reason = detectConsoleEndpointProtocol(consoleTarget{
					protocol: "auto", host: candidate.host, port: candidate.port,
					transport: candidate.transport, instanceID: instanceID, provider: candidate.provider,
				})
			}
		}()
	}
	for index := range candidates {
		jobs <- index
	}
	close(jobs)
	wait.Wait()

	result := detectedConsoleEndpoints{
		vnc:    make([]consoleDiscoveredEndpoint, 0, 1),
		native: make([]nativeConsoleTarget, 0, 2),
	}
	for index, probe := range results {
		candidate := candidates[index]
		switch probe.protocol {
		case consoleProtocolVNC:
			result.vnc = append(result.vnc, consoleDiscoveredEndpoint{
				protocol: consoleProtocolVNC, host: candidate.host, port: candidate.port, transport: candidate.transport,
			})
		case "rdp", "ssh":
			if candidate.transport != "direct" && !isPublicInstanceDirectConsoleTransport(candidate.transport) {
				// These protocols need a local native client. A listener proven only
				// through the node's private SSH/Agent channel is useful diagnostic
				// information but cannot be launched at 127.0.0.1 in the user's browser.
				if result.reason == "" {
					result.reason = fmt.Sprintf("已在实例私有地址 %s:%d 实际探测到 %s，但该协议需要客户端可直达的公网地址", candidate.host, candidate.port, strings.ToUpper(probe.protocol))
				}
				continue
			}
			result.native = append(result.native, nativeConsoleTarget{
				protocol: probe.protocol, url: probe.protocol + "://" + joinConsoleURLHostPort(candidate.host, candidate.port),
				instanceEndpoint: isPublicInstanceDirectConsoleTransport(candidate.transport),
			})
		case "spice":
			if candidate.transport != "direct" && !isPublicInstanceDirectConsoleTransport(candidate.transport) {
				if result.reason == "" {
					result.reason = fmt.Sprintf("已在实例私有地址 %s:%d 实际探测到 SPICE，但尚未配置可供浏览器使用的 SPICE 网关", candidate.host, candidate.port)
				}
				continue
			}
			// A raw SPICE TCP listener needs a native client. The browser SPICE
			// path uses a separately detected websockify adapter, so do not send
			// this endpoint through the in-panel SPICE iframe workflow.
			result.native = append(result.native, nativeConsoleTarget{
				protocol: consoleProtocolNative, url: "spice://" + joinConsoleURLHostPort(candidate.host, candidate.port),
				instanceEndpoint: isPublicInstanceDirectConsoleTransport(candidate.transport),
			})
		case "telnet":
			if candidate.transport != "direct" && !isPublicInstanceDirectConsoleTransport(candidate.transport) {
				if result.reason == "" {
					result.reason = fmt.Sprintf("已在实例私有地址 %s:%d 实际探测到 Telnet，但该协议需要客户端可直达的公网地址", candidate.host, candidate.port)
				}
				continue
			}
			result.native = append(result.native, nativeConsoleTarget{
				protocol: "telnet", url: "telnet://" + joinConsoleURLHostPort(candidate.host, candidate.port),
				instanceEndpoint: isPublicInstanceDirectConsoleTransport(candidate.transport),
			})
		default:
			if result.reason == "" && strings.TrimSpace(probe.reason) != "" {
				result.reason = fmt.Sprintf("%s:%d 实际协议探测失败: %s", candidate.host, candidate.port, probe.reason)
			}
		}
	}
	return result
}

func consoleEndpointProbeKey(target consoleTarget) string {
	return strings.Join([]string{
		target.protocol, strconv.FormatUint(uint64(target.instanceID), 10), strings.ToLower(strings.TrimSpace(target.transport)),
		strings.ToLower(strings.Trim(strings.TrimSpace(target.host), "[]")), strconv.Itoa(target.port),
	}, ":")
}

// detectConsoleEndpointProtocol proves a protocol from a real service
// handshake instead of inferring it from guest port numbers. VNC/SSH/Telnet
// send an identification first; RDP and SPICE are tested on fresh read-only
// connections because they wait for a client hello.
func detectConsoleEndpointProtocol(target consoleTarget) (string, string) {
	if target.port < 1 || target.port > 65535 || strings.TrimSpace(target.host) == "" {
		return "", "控制台地址或端口无效"
	}
	target.protocol = "auto"
	key := consoleEndpointProbeKey(target)
	if state, ok := cachedConsoleEndpointProbe(key, time.Now()); ok {
		if state.available {
			return state.protocol, ""
		}
		return "", state.reason
	}
	value, _, _ := consoleEndpointProbeGroup.Do(key, func() (interface{}, error) {
		if state, ok := cachedConsoleEndpointProbe(key, time.Now()); ok {
			return state, nil
		}
		state := consoleEndpointProbeState{reason: "控制台协议探测未返回结果", checkedAt: time.Now()}
		select {
		case consoleEndpointProbeSlots <- struct{}{}:
			state.protocol, state.reason = probeConsoleEndpointProtocol(target)
			state.available = state.protocol != ""
			<-consoleEndpointProbeSlots
		default:
			state.reason = "控制台协议探测队列繁忙，请稍后重试"
		}
		cacheConsoleEndpointProbe(key, state)
		if state.available {
			// Reuse the positive protocol result when the normal VNC/native
			// resolver immediately refreshes the same endpoint below.
			protocolTarget := target
			protocolTarget.protocol = state.protocol
			cacheConsoleEndpointProbe(consoleEndpointProbeKey(protocolTarget), state)
		}
		return state, nil
	})
	state, ok := value.(consoleEndpointProbeState)
	if !ok || !state.available {
		if ok {
			return "", state.reason
		}
		return "", "控制台协议探测未返回有效结果"
	}
	return state.protocol, ""
}

func probeConsoleEndpointProtocol(target consoleTarget) (string, string) {
	if protocol, reason := probeConsoleServerGreeting(target); protocol != "" {
		return protocol, ""
	} else if reason != "" {
		return "", reason
	}
	for _, protocol := range []string{"rdp", "spice"} {
		if available, _ := probeConsoleTargetProtocol(protocol, target, consoleEndpointProbeTimeout); available {
			return protocol, ""
		}
	}
	return "", "端口未返回受支持的 VNC、RDP、SPICE、SSH 或 Telnet 协议握手"
}

func probeConsoleServerGreeting(target consoleTarget) (string, string) {
	conn, cleanup, err := openConsoleConnWithTimeout(target, consoleGreetingProbeTimeout)
	if err != nil {
		return "", err.Error()
	}
	defer cleanup()
	_ = conn.SetDeadline(time.Now().Add(consoleGreetingProbeTimeout))
	buffer := make([]byte, 256)
	count, err := conn.Read(buffer)
	if count > 0 {
		greeting := string(buffer[:count])
		if count >= 12 && strings.HasPrefix(greeting, "RFB ") && greeting[11] == '\n' {
			return consoleProtocolVNC, ""
		}
		trimmed := strings.TrimSpace(greeting)
		if strings.HasPrefix(trimmed, "SSH-") || strings.Contains(greeting, "\nSSH-") {
			return "ssh", ""
		}
		if strings.HasPrefix(trimmed, "\xff\xfd") || strings.Contains(greeting, "\xff\xfb") || strings.Contains(greeting, "\xff\xfc") ||
			strings.Contains(strings.ToLower(greeting), "telnet") {
			return "telnet", ""
		}
	}
	if err != nil {
		if networkErr, ok := err.(net.Error); ok && networkErr.Timeout() {
			return "", ""
		}
		// A server that waits for an RDP/SPICE hello can close this first
		// connection. Continue with its protocol-specific fresh connections.
		return "", ""
	}
	return "", ""
}

func probeConsoleTargetProtocol(protocol string, target consoleTarget, timeout time.Duration) (bool, string) {
	conn, cleanup, err := openConsoleConnWithTimeout(target, timeout)
	if err != nil {
		return false, err.Error()
	}
	defer cleanup()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	return probeNativeConsoleConn(protocol, conn)
}

func cachedConsoleEndpointProbe(key string, now time.Time) (consoleEndpointProbeState, bool) {
	consoleEndpointProbeMu.Lock()
	defer consoleEndpointProbeMu.Unlock()
	for cacheKey, state := range consoleEndpointProbeCache {
		if now.Sub(state.checkedAt) > consoleEndpointProbeTTL {
			delete(consoleEndpointProbeCache, cacheKey)
		}
	}
	state, ok := consoleEndpointProbeCache[key]
	return state, ok
}

func cacheConsoleEndpointProbe(key string, state consoleEndpointProbeState) {
	consoleEndpointProbeMu.Lock()
	consoleEndpointProbeCache[key] = state
	consoleEndpointProbeMu.Unlock()
}

func invalidateConsoleEndpointProbe(instanceID uint) {
	consoleEndpointProbeMu.Lock()
	needle := ":" + strconv.FormatUint(uint64(instanceID), 10) + ":"
	for key := range consoleEndpointProbeCache {
		if strings.Contains(key, needle) {
			delete(consoleEndpointProbeCache, key)
		}
	}
	consoleEndpointProbeMu.Unlock()
}

// refreshVNCConsoleTarget proves a candidate is an RFB listener before the UI
// exposes it as available. It reads only the version banner and immediately
// closes the socket, so a capability request never starts a remote desktop.
func refreshVNCConsoleTarget(target consoleTarget) consoleTarget {
	if target.protocol != consoleProtocolVNC || !target.available || target.proxmoxVNC {
		return target
	}
	available, reason := checkVNCHealth(target)
	if available {
		return target
	}
	target.available = false
	target.reason = "VNC 实际健康检查失败: " + reason
	return target
}

func checkVNCHealth(target consoleTarget) (bool, string) {
	if target.port < 1 || target.port > 65535 || strings.TrimSpace(target.host) == "" {
		return false, "VNC 地址或端口无效"
	}
	key := consoleEndpointProbeKey(target)
	if state, ok := cachedConsoleEndpointProbe(key, time.Now()); ok {
		return state.available, state.reason
	}
	value, _, _ := consoleEndpointProbeGroup.Do(key, func() (interface{}, error) {
		if state, ok := cachedConsoleEndpointProbe(key, time.Now()); ok {
			return state, nil
		}
		state := consoleEndpointProbeState{reason: "VNC 健康检查未返回结果", checkedAt: time.Now()}
		select {
		case consoleEndpointProbeSlots <- struct{}{}:
			state.available, state.reason = probeVNCVersion(target)
			<-consoleEndpointProbeSlots
		default:
			state.reason = "VNC 健康检查队列繁忙，请稍后重试"
		}
		cacheConsoleEndpointProbe(key, state)
		return state, nil
	})
	state, ok := value.(consoleEndpointProbeState)
	if !ok {
		return false, "VNC 健康检查未返回有效结果"
	}
	return state.available, state.reason
}

func probeVNCVersion(target consoleTarget) (bool, string) {
	conn, cleanup, err := openConsoleConn(target)
	if err != nil {
		return false, err.Error()
	}
	defer cleanup()
	_ = conn.SetDeadline(time.Now().Add(consoleEndpointProbeTimeout))
	return probeRFBVersion(conn)
}

func probeRFBVersion(conn net.Conn) (bool, string) {
	version := make([]byte, 12)
	if _, err := io.ReadFull(conn, version); err != nil {
		return false, "读取 RFB 版本失败: " + err.Error()
	}
	value := string(version)
	if !strings.HasPrefix(value, "RFB ") || !strings.HasSuffix(value, "\n") {
		return false, "端口未返回 RFB 协议版本"
	}
	return true, ""
}

// refreshNativeConsoleTarget checks custom-scheme endpoints that cannot be
// rendered in the browser itself (RDP, SSH, Telnet), plus HTTP(S) native web
// consoles such as Xpra or an administrator-provided browser gateway. The URL
// was already restricted to a configured node host; every supported scheme is
// still live-probed before presenting an action.
func refreshNativeConsoleTarget(target consoleTarget) consoleTarget {
	if !target.available || strings.TrimSpace(target.nativeURL) == "" {
		return target
	}
	parsed, err := url.Parse(target.nativeURL)
	if err != nil {
		target.available = false
		target.reason = "原生控制台地址格式无效"
		return target
	}
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	if scheme == "http" || scheme == "https" {
		available, reason := probeHTTPNativeConsoleURL(parsed)
		if !available {
			target.available = false
			target.reason = strings.ToUpper(scheme) + " 原生控制台实际健康检查失败: " + reason
		}
		return target
	}
	defaultPort := 0
	switch scheme {
	case "vnc":
		// A vnc:// URI can be supplied by a node-specific/native client. Probe
		// the RFB banner first so it is not a generic TCP shortcut.
		defaultPort = 5900
	case "rdp":
		defaultPort = 3389
	case "spice":
		// SPICE listeners do not have one universally safe default port. An
		// advertised native URL must carry its observed listener port.
		defaultPort = 0
	case "ssh":
		defaultPort = 22
	case "telnet":
		defaultPort = 23
	default:
		return target
	}
	host := strings.TrimSpace(parsed.Hostname())
	port := defaultPort
	if rawPort := parsed.Port(); rawPort != "" {
		port, err = strconv.Atoi(rawPort)
	}
	if host == "" || err != nil || port < 1 || port > 65535 {
		target.available = false
		target.reason = "原生控制台地址或端口无效"
		return target
	}
	endpointTarget := consoleTarget{protocol: scheme, host: host, port: port, transport: "direct", instanceID: target.instanceID}
	key := consoleEndpointProbeKey(endpointTarget)
	if state, ok := cachedConsoleEndpointProbe(key, time.Now()); ok {
		if !state.available {
			target.available = false
			target.reason = strings.ToUpper(scheme) + " 实际健康检查失败: " + state.reason
		}
		return target
	}
	value, _, _ := consoleEndpointProbeGroup.Do(key, func() (interface{}, error) {
		if state, ok := cachedConsoleEndpointProbe(key, time.Now()); ok {
			return state, nil
		}
		state := consoleEndpointProbeState{reason: "TCP 健康检查未返回结果", checkedAt: time.Now()}
		select {
		case consoleEndpointProbeSlots <- struct{}{}:
			state.available, state.reason = probeNativeConsoleEndpoint(scheme, host, port)
			<-consoleEndpointProbeSlots
		default:
			state.reason = "TCP 健康检查队列繁忙，请稍后重试"
		}
		cacheConsoleEndpointProbe(key, state)
		return state, nil
	})
	state, ok := value.(consoleEndpointProbeState)
	if !ok || !state.available {
		target.available = false
		if ok {
			target.reason = strings.ToUpper(scheme) + " 实际健康检查失败: " + state.reason
		} else {
			target.reason = strings.ToUpper(scheme) + " 健康检查未返回有效结果"
		}
	}
	return target
}

// probeNativeConsoleEndpoint verifies the protocol greeting where the protocol
// has a bounded, non-mutating handshake. A bare TCP accept is not enough for
// RDP: arbitrary services on 3389 would otherwise create a misleading button.
func probeNativeConsoleEndpoint(scheme, host string, port int) (bool, string) {
	return probeNativeConsoleEndpointWithTimeout(scheme, host, port, consoleEndpointProbeTimeout)
}

// probeHTTPNativeConsoleURL issues a bounded same-target request without
// following redirects. A 401/403 still proves a real protected console gateway
// is present; the browser can subsequently complete its normal login flow.
// Redirects are deliberately not followed because discovery metadata must not
// turn the controller into an arbitrary secondary HTTP client.
func probeHTTPNativeConsoleURL(target *url.URL) (bool, string) {
	if target == nil || target.Hostname() == "" {
		return false, "HTTP 控制台地址无效"
	}
	request, err := http.NewRequest(http.MethodGet, target.String(), nil)
	if err != nil {
		return false, "创建 HTTP 控制台探测请求失败: " + err.Error()
	}
	request.Header.Set("Range", "bytes=0-0")
	request.Header.Set("Accept", "text/html,application/json;q=0.9,*/*;q=0.1")
	client := &http.Client{
		Timeout: consoleEndpointProbeTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return false, err.Error()
	}
	defer response.Body.Close()
	if (response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices) ||
		response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return true, ""
	}
	return false, fmt.Sprintf("HTTP %d", response.StatusCode)
}

func probeNativeConsoleEndpointWithTimeout(scheme, host string, port int, timeout time.Duration) (bool, string) {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), timeout)
	if err != nil {
		return false, err.Error()
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	return probeNativeConsoleConn(scheme, conn)
}

func probeNativeConsoleConn(scheme string, conn net.Conn) (bool, string) {
	switch scheme {
	case "vnc":
		return probeRFBVersion(conn)
	case "spice":
		return probeSPICELink(conn)
	case "rdp":
		return probeRDPHandshake(conn)
	case "ssh":
		return probeSSHBanner(conn)
	case "telnet":
		return probeTelnetGreeting(conn)
	default:
		return true, ""
	}
}

// probeTelnetGreeting verifies Telnet's IAC negotiation or a server banner.
// It deliberately sends no option negotiation and never opens a login shell.
func probeTelnetGreeting(conn net.Conn) (bool, string) {
	buffer := make([]byte, 256)
	count, err := conn.Read(buffer)
	if count > 0 {
		greeting := buffer[:count]
		if greeting[0] == 255 || strings.Contains(strings.ToLower(string(greeting)), "telnet") {
			return true, ""
		}
	}
	if err != nil {
		return false, "读取 Telnet 欢迎信息失败: " + err.Error()
	}
	return false, "端口未返回 Telnet 协商或欢迎信息"
}

// probeSPICELink opens the protocol's initial Link message and validates the
// server reply magic. It does not send a ticket, authenticate, or create a
// display channel, so it proves that the listener speaks SPICE without
// changing guest state. Native `spice://` links use the unencrypted SPICE TCP
// transport; TLS-only endpoints must be surfaced through a node adapter.
func probeSPICELink(conn net.Conn) (bool, string) {
	const (
		spiceVersionMajor = 2
		spiceVersionMinor = 2
		spiceMainChannel  = 1
		spiceLinkBodySize = 18
	)
	request := make([]byte, 16+spiceLinkBodySize)
	copy(request[:4], []byte("REDQ"))
	binary.LittleEndian.PutUint32(request[4:8], spiceVersionMajor)
	binary.LittleEndian.PutUint32(request[8:12], spiceVersionMinor)
	binary.LittleEndian.PutUint32(request[12:16], spiceLinkBodySize)
	// A zero connection ID and the main channel establish only the initial
	// protocol negotiation; zero capabilities make this probe read-only.
	binary.LittleEndian.PutUint32(request[16:20], 0)
	request[20] = spiceMainChannel
	request[21] = 0
	binary.LittleEndian.PutUint32(request[30:34], spiceLinkBodySize)
	if _, err := conn.Write(request); err != nil {
		return false, "发送 SPICE Link 探测失败: " + err.Error()
	}
	reply := make([]byte, 16)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return false, "读取 SPICE Link 响应失败: " + err.Error()
	}
	if string(reply[:4]) != "REDQ" {
		return false, "端口未返回 SPICE Link 响应"
	}
	major := binary.LittleEndian.Uint32(reply[4:8])
	minor := binary.LittleEndian.Uint32(reply[8:12])
	if major == 0 || major&0x80000000 != 0 || minor&0x80000000 != 0 {
		return false, "SPICE Link 响应版本无效"
	}
	if size := binary.LittleEndian.Uint32(reply[12:16]); size > 1024*1024 {
		return false, fmt.Sprintf("SPICE Link 响应长度无效: %d", size)
	}
	return true, ""
}

func probeRDPHandshake(conn net.Conn) (bool, string) {
	// X.224 Connection Request (TPKT). RDP servers answer with a Connection
	// Confirm before negotiating TLS/NLA, so this does not authenticate or alter
	// the guest state.
	request := []byte{0x03, 0x00, 0x00, 0x13, 0x0e, 0xe0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x08, 0x00, 0x03, 0x00, 0x00}
	if _, err := conn.Write(request); err != nil {
		return false, "发送 RDP X.224 探测失败: " + err.Error()
	}
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return false, "读取 RDP X.224 响应失败: " + err.Error()
	}
	if header[0] != 0x03 || header[1] != 0x00 {
		return false, "端口未返回 RDP TPKT 响应"
	}
	length := int(header[2])<<8 | int(header[3])
	if length < 7 || length > 4096 {
		return false, fmt.Sprintf("RDP TPKT 长度无效: %d", length)
	}
	payload := make([]byte, length-4)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return false, "读取 RDP X.224 负载失败: " + err.Error()
	}
	if len(payload) < 2 || payload[0] != 0x06 || payload[1] != 0xd0 {
		return false, "端口未返回 RDP X.224 Connection Confirm"
	}
	return true, ""
}

func probeSSHBanner(conn net.Conn) (bool, string) {
	buffer := make([]byte, 256)
	count := 0
	for count < len(buffer) {
		read, err := conn.Read(buffer[count : count+1])
		count += read
		if err != nil {
			return false, "读取 SSH banner 失败: " + err.Error()
		}
		if buffer[count-1] == '\n' {
			break
		}
	}
	if !strings.HasPrefix(strings.TrimSpace(string(buffer[:count])), "SSH-") {
		return false, "端口未返回 SSH banner"
	}
	return true, ""
}
