package admin

// This file discovers LXD/Incus proxy-device listeners from the live expanded
// instance configuration. A proxy device is only an endpoint candidate: the
// common TCP fingerprinting layer still has to prove VNC/RDP/SPICE/SSH before
// the UI receives a control option.

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"

	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/utils"
)

func probeLXDLikeProxyConsoleEndpoints(executor utils.ShellExecutor, cli, identifier string, p providerModel.Provider) ([]consoleEndpointCandidate, string) {
	command := fmt.Sprintf("%s config show %s --expanded --format json", cli, utils.ShellSingleQuote(identifier))
	output, err := executor.ExecuteWithTimeout(command, consoleRuntimeProbeTimeout)
	if err != nil {
		reason := fmt.Sprintf("读取 %s 实例实时 proxy 设备失败: %v", strings.ToUpper(cli), err)
		if trimmed := strings.TrimSpace(output); trimmed != "" {
			reason += "；远端输出: " + utils.TruncateString(trimmed, 400)
		}
		return nil, reason
	}
	endpoints, err := collectLXDLikeProxyConsoleEndpoints(output, p)
	if err != nil {
		return nil, "解析实时 proxy 设备失败: " + err.Error()
	}
	return endpoints, ""
}

// collectLXDLikeProxyConsoleEndpoints accepts both direct CLI objects and the
// API-style metadata envelope. Expanded devices take precedence because that
// is the concrete listener configuration after profiles have been applied.
func collectLXDLikeProxyConsoleEndpoints(raw string, p providerModel.Provider) ([]consoleEndpointCandidate, error) {
	var root map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &root); err != nil {
		return nil, err
	}
	if metadata, ok := root["metadata"].(map[string]interface{}); ok {
		root = metadata
	}
	devices, ok := root["expanded_devices"].(map[string]interface{})
	if !ok || len(devices) == 0 {
		devices, _ = root["devices"].(map[string]interface{})
	}
	if len(devices) == 0 {
		return nil, nil
	}

	result := make([]consoleEndpointCandidate, 0, 2)
	for _, rawDevice := range devices {
		device, ok := rawDevice.(map[string]interface{})
		if !ok || !strings.EqualFold(strings.TrimSpace(fmt.Sprint(device["type"])), "proxy") {
			continue
		}
		listen := strings.TrimSpace(fmt.Sprint(device["listen"]))
		if listen == "" || listen == "<nil>" {
			continue
		}
		for _, candidate := range lxdLikeProxyConsoleEndpointCandidates(listen, p) {
			result = appendConsoleEndpointCandidate(result, candidate)
			if len(result) >= consoleMappedEndpointProbeLimit {
				return result, nil
			}
		}
	}
	return result, nil
}

func lxdLikeProxyConsoleEndpointCandidates(listen string, p providerModel.Provider) []consoleEndpointCandidate {
	protocol, address, ok := strings.Cut(strings.TrimSpace(listen), ":")
	if !ok || !strings.EqualFold(strings.TrimSpace(protocol), "tcp") {
		return nil
	}
	host, ports, ok := splitLXDLikeProxyListenAddress(address)
	if !ok {
		return nil
	}
	targets := lxdLikeProxyConsoleHosts(host, p)
	if len(targets) == 0 {
		return nil
	}

	result := make([]consoleEndpointCandidate, 0, len(ports)*len(targets))
	for _, target := range targets {
		for _, port := range ports {
			result = append(result, consoleEndpointCandidate{
				host: target.host, port: port, transport: target.transport, provider: p,
			})
		}
	}
	return result
}

func splitLXDLikeProxyListenAddress(value string) (string, []int, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil, false
	}
	host, rawPorts, err := net.SplitHostPort(value)
	if err != nil {
		index := strings.LastIndex(value, ":")
		if index < 0 {
			return "", nil, false
		}
		host = strings.Trim(strings.TrimSpace(value[:index]), "[]")
		rawPorts = strings.TrimSpace(value[index+1:])
	}
	ports := expandLXDLikeProxyPortRange(rawPorts)
	if len(ports) == 0 {
		return "", nil, false
	}
	return strings.Trim(strings.TrimSpace(host), "[]"), ports, true
}

func expandLXDLikeProxyPortRange(value string) []int {
	value = strings.TrimSpace(value)
	startText, endText, ranged := strings.Cut(value, "-")
	start, err := strconv.Atoi(strings.TrimSpace(startText))
	if err != nil || start < 1 || start > 65535 {
		return nil
	}
	end := start
	if ranged {
		end, err = strconv.Atoi(strings.TrimSpace(endText))
		if err != nil || end < start || end > 65535 {
			return nil
		}
	}
	// An imported proxy device can contain a large range. The shared endpoint
	// cap keeps a one-instance details request bounded even in that case.
	if end-start+1 > consoleMappedEndpointProbeLimit {
		end = start + consoleMappedEndpointProbeLimit - 1
	}
	ports := make([]int, 0, end-start+1)
	for port := start; port <= end; port++ {
		ports = append(ports, port)
	}
	return ports
}

type lxdLikeProxyConsoleHost struct {
	host      string
	transport string
}

// lxdLikeProxyConsoleHosts turns one live listener into the bounded set of
// reachable paths. Wildcard listeners may be both public-node endpoints and
// node-local tunnel endpoints; each is independently handshake-probed before
// it reaches the UI. This lets an RDP action keep its direct public address
// while a VNC action can fall back to the panel SSH/Agent tunnel.
func lxdLikeProxyConsoleHosts(host string, p providerModel.Provider) []lxdLikeProxyConsoleHost {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	nodeTransport := normalizeConsoleTransport(p, "")
	isWildcard := host == "" || host == "0.0.0.0" || host == "::" || host == "*"
	result := make([]lxdLikeProxyConsoleHost, 0, 2)
	add := func(candidateHost, transport string) {
		if candidateHost == "" || transport == "" {
			return
		}
		for _, existing := range result {
			if existing.transport == transport && consoleHostsEqual(existing.host, candidateHost) {
				return
			}
		}
		result = append(result, lxdLikeProxyConsoleHost{host: candidateHost, transport: transport})
	}

	if isWildcard {
		// A wildcard proxy often listens on the public node address. Probe that
		// path independently so native RDP/SPICE links are available when they
		// are genuinely reachable from the panel.
		add(lxdLikeProxyDirectHost(p), "direct")
		if nodeTransport == "ssh" || nodeTransport == "agent" || nodeTransport == "local" {
			add("127.0.0.1", nodeTransport)
		}
		return result
	}
	if isConsoleLoopbackHost(host) {
		if nodeTransport == "ssh" || nodeTransport == "agent" || nodeTransport == "local" {
			add("127.0.0.1", nodeTransport)
		}
		return result
	}
	for _, trusted := range consoleTrustedURLHosts(p) {
		if !isConsoleLoopbackHost(host) && consoleHostsEqual(host, trusted) {
			add(host, "direct")
			break
		}
	}
	return result
}

func lxdLikeProxyDirectHost(p providerModel.Provider) string {
	for _, raw := range []string{p.VNCHost, p.PortIP, p.Endpoint} {
		host := strings.Trim(strings.TrimSpace(utils.ExtractHost(raw)), "[]")
		if host != "" && !isConsoleLoopbackHost(host) {
			return host
		}
	}
	return ""
}
