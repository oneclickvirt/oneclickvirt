package admin

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/utils"
)

type nativeConsoleTarget struct {
	protocol         string
	url              string
	instanceEndpoint bool
}

func normalizeNativeConsoleProtocol(protocol string) string {
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	switch protocol {
	case "", "native", "console", "terminal", "native-console", consoleProtocolVNC, consoleProtocolSPICE:
		return consoleProtocolNative
	default:
		return protocol
	}
}

func (d *discoveredConsole) addNativeTarget(protocol, rawURL string) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return
	}
	protocol = normalizeNativeConsoleProtocol(protocol)
	for _, target := range d.nativeTargets {
		if target.protocol == protocol || target.url == rawURL {
			return
		}
	}
	d.nativeTargets = append(d.nativeTargets, nativeConsoleTarget{protocol: protocol, url: rawURL})
	// Retain the first target in the legacy fields while rolling out the
	// multi-protocol contract to old callers and persisted metadata readers.
	if d.nativeURL == "" {
		d.nativeProtocol = protocol
		d.nativeURL = rawURL
	}
}

func consoleTrustedURLHosts(provider providerModel.Provider) []string {
	candidates := []string{provider.Endpoint, provider.PortIP, provider.VNCHost}
	hosts := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		host := strings.TrimSpace(utils.ExtractHost(candidate))
		if host == "" {
			continue
		}
		duplicate := false
		for _, existing := range hosts {
			if consoleHostsEqual(existing, host) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			hosts = append(hosts, host)
		}
	}
	return hosts
}

func consoleHostsEqual(left, right string) bool {
	left = strings.Trim(strings.TrimSpace(left), "[]")
	right = strings.Trim(strings.TrimSpace(right), "[]")
	if left == "" || right == "" {
		return false
	}
	leftIP := net.ParseIP(left)
	rightIP := net.ParseIP(right)
	if leftIP != nil || rightIP != nil {
		return leftIP != nil && rightIP != nil && leftIP.Equal(rightIP)
	}
	return strings.EqualFold(left, right)
}

func isConsoleLoopbackHost(host string) bool {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// normalizeConsoleProxyTarget restricts server-side console connections to a
// known node transport. Discovery data is node supplied; accepting an arbitrary
// host here would turn the panel into an SSRF proxy.
func normalizeConsoleProxyTarget(p providerModel.Provider, host, transport string) (string, string, error) {
	transport = normalizeConsoleTransport(p, transport)
	switch transport {
	case "ssh", "agent", "local":
		// SSH/Agent transports run on the provider node. Only its loopback
		// listener is valid; an arbitrary remote address is never needed.
		if !isConsoleLoopbackHost(host) {
			return "", transport, fmt.Errorf("%s 控制台代理只允许节点本机回环地址", strings.ToUpper(transport))
		}
		if transport == "agent" {
			if reason := consoleAgentTransportReason(p.ID); reason != "" {
				return "", transport, fmt.Errorf("%s", reason)
			}
		}
		return strings.Trim(strings.TrimSpace(host), "[]"), transport, nil
	case "direct":
		for _, trusted := range consoleTrustedURLHosts(p) {
			if consoleHostsEqual(host, trusted) {
				return strings.Trim(strings.TrimSpace(host), "[]"), transport, nil
			}
		}
		return "", transport, fmt.Errorf("直连控制台地址不是当前节点的受信任主机")
	case consoleInstanceEndpointTransport:
		ip := net.ParseIP(strings.Trim(strings.TrimSpace(host), "[]"))
		if !isAllowedInstanceConsoleIP(ip) {
			return "", transport, fmt.Errorf("实例直连控制台地址必须是非回环、非链路本地 IP")
		}
		return ip.String(), transport, nil
	case consolePublicInstanceEndpointTransport:
		ip := net.ParseIP(strings.Trim(strings.TrimSpace(host), "[]"))
		if !isPublicConsoleIP(ip) {
			return "", transport, fmt.Errorf("实例公网控制台地址必须是可路由的公网 IP")
		}
		return ip.String(), transport, nil
	default:
		if transport == "" {
			return "", transport, fmt.Errorf("节点未配置控制台传输方式")
		}
		return "", transport, fmt.Errorf("不支持的控制台传输方式 %q", transport)
	}
}

func isAllowedInstanceConsoleIP(ip net.IP) bool {
	return ip != nil && !ip.IsUnspecified() && !ip.IsLoopback() && !ip.IsMulticast() && !ip.IsLinkLocalUnicast()
}

// validateNativeConsoleTarget applies the normal node-host restriction to
// controller metadata. Targets created from a live probe of an instance's own
// public address are the sole exception: they must still use a supported URL
// scheme and a literal routable IP, never a hostname or private address.
func validateNativeConsoleTarget(target nativeConsoleTarget, provider providerModel.Provider) (string, error) {
	if !target.instanceEndpoint {
		return validateNativeConsoleURL(target.url, provider)
	}

	rawURL := strings.TrimSpace(target.url)
	if rawURL == "" {
		return "", fmt.Errorf("原生控制台地址为空")
	}
	decoded, decodeErr := url.PathUnescape(rawURL)
	if decodeErr != nil || strings.ContainsRune(rawURL, '\\') || strings.ContainsRune(decoded, '\\') ||
		strings.ContainsAny(rawURL, "\r\n\t") || strings.ContainsAny(decoded, "\r\n\t") {
		return "", fmt.Errorf("原生控制台地址包含不安全的路径字符")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed == nil || parsed.User != nil || parsed.Hostname() == "" {
		return "", fmt.Errorf("实例原生控制台地址格式无效")
	}
	scheme := strings.ToLower(parsed.Scheme)
	switch scheme {
	case "https", "http", "spice", "vnc", "rdp", "ssh", "telnet":
	default:
		return "", fmt.Errorf("原生控制台协议 %q 不受支持", scheme)
	}
	ip := net.ParseIP(strings.Trim(parsed.Hostname(), "[]"))
	if !isPublicConsoleIP(ip) {
		return "", fmt.Errorf("实例原生控制台地址必须是可路由的公网 IP")
	}
	return rawURL, nil
}

// validateNativeConsoleURL constrains discovered native console links to the
// configured node. Discovery data is remote input and must never become an
// arbitrary browser navigation or custom-protocol launcher.
func validateNativeConsoleURL(rawURL string, provider providerModel.Provider) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", fmt.Errorf("原生控制台地址为空")
	}
	// WHATWG URL parsing in browsers treats backslashes as path separators for
	// special schemes. Reject both literal and percent-encoded forms before
	// accepting a same-origin relative path, otherwise `/\\host` can become a
	// cross-origin navigation in a browser even though net/url sees a path.
	decoded, decodeErr := url.PathUnescape(rawURL)
	if decodeErr != nil || strings.ContainsRune(rawURL, '\\') || strings.ContainsRune(decoded, '\\') ||
		strings.ContainsAny(rawURL, "\r\n\t") || strings.ContainsAny(decoded, "\r\n\t") {
		return "", fmt.Errorf("原生控制台地址包含不安全的路径字符")
	}
	if strings.HasPrefix(rawURL, "/") && !strings.HasPrefix(rawURL, "//") {
		if strings.HasPrefix(decoded, "//") || strings.HasPrefix(decoded, "/"+string('\\')) {
			return "", fmt.Errorf("原生控制台地址不能使用网络路径")
		}
		return rawURL, nil
	}

	parsed, err := url.Parse(rawURL)
	if err != nil || parsed == nil {
		return "", fmt.Errorf("原生控制台地址格式无效")
	}
	if parsed.User != nil || parsed.Hostname() == "" {
		return "", fmt.Errorf("原生控制台地址必须包含节点主机，且不能包含用户名或密码")
	}
	scheme := strings.ToLower(parsed.Scheme)
	switch scheme {
	case "https", "http", "spice", "vnc", "rdp", "ssh", "telnet":
	default:
		return "", fmt.Errorf("原生控制台协议 %q 不受支持", scheme)
	}
	for _, trusted := range consoleTrustedURLHosts(provider) {
		if consoleHostsEqual(parsed.Hostname(), trusted) {
			return rawURL, nil
		}
	}
	return "", fmt.Errorf("原生控制台地址不是当前节点的受信任主机")
}
