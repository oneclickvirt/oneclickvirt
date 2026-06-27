package utils

import (
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func NormalizeOrigin(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" {
		return ""
	}

	scheme := strings.ToLower(parsed.Scheme)
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return ""
	}

	port := parsed.Port()
	if isDefaultPortForScheme(scheme, port) {
		port = ""
	}

	hostPort := host
	if port != "" {
		hostPort = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		hostPort = "[" + host + "]"
	}

	return scheme + "://" + hostPort
}

func isDefaultPortForScheme(scheme, port string) bool {
	if port == "" {
		return false
	}

	portNum, err := strconv.Atoi(port)
	if err != nil {
		return false
	}

	return (scheme == "http" && portNum == 80) || (scheme == "https" && portNum == 443)
}

// OriginMatchesFrontend compares Origin and frontend URL after canonicalization.
// It normalizes scheme/host and removes default ports (http:80, https:443).
func OriginMatchesFrontend(origin, frontendURL string) bool {
	originNorm := NormalizeOrigin(origin)
	frontendNorm := NormalizeOrigin(frontendURL)
	if originNorm == "" || frontendNorm == "" {
		return false
	}
	return originNorm == frontendNorm
}

// OriginMatchesHost compares an Origin value with a request host under a scheme.
// It is intended for reverse-proxy and port-mapped deployments where the public
// host/port is only known from the current request.
func OriginMatchesHost(origin, scheme, host string) bool {
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	host = strings.TrimSpace(host)
	if origin == "" || scheme == "" || host == "" {
		return false
	}
	if strings.Contains(host, ",") {
		host = strings.TrimSpace(strings.Split(host, ",")[0])
	}
	if host == "" {
		return false
	}
	return NormalizeOrigin(origin) == NormalizeOrigin(scheme+"://"+host)
}

func OriginMatchesAnyHost(origin string, schemes, hosts []string) bool {
	for _, host := range hosts {
		for _, scheme := range schemes {
			if OriginMatchesHost(origin, scheme, host) {
				return true
			}
		}
	}
	return false
}

func IsLoopbackOrigin(origin string) bool {
	normalized := NormalizeOrigin(origin)
	return strings.HasPrefix(normalized, "http://localhost:") ||
		strings.HasPrefix(normalized, "https://localhost:") ||
		strings.HasPrefix(normalized, "http://127.0.0.1:") ||
		strings.HasPrefix(normalized, "https://127.0.0.1:")
}

func OriginAllowedForRequest(r *http.Request, origin string, frontendURL string, whitelist []string) bool {
	originNorm := NormalizeOrigin(origin)
	if originNorm == "" {
		return false
	}
	if OriginMatchesFrontend(origin, frontendURL) {
		return true
	}
	for _, item := range whitelist {
		if originNorm == NormalizeOrigin(item) {
			return true
		}
	}
	if IsLoopbackOrigin(origin) {
		return true
	}
	schemes, hosts := RequestOriginCandidates(r)
	return OriginMatchesAnyHost(origin, schemes, hosts)
}

func RequestOriginCandidates(r *http.Request) ([]string, []string) {
	schemes := make([]string, 0, 6)
	hosts := make([]string, 0, 8)
	ports := make([]string, 0, 3)

	schemes = appendCommaHeaderValues(schemes, r.Header.Get("X-Forwarded-Proto"))
	if strings.EqualFold(r.Header.Get("X-Forwarded-Ssl"), "on") {
		schemes = append(schemes, "https")
	}
	if r.URL != nil && r.URL.Scheme != "" {
		schemes = append(schemes, r.URL.Scheme)
	}
	if r.TLS != nil {
		schemes = append(schemes, "https")
	}

	hosts = appendCommaHeaderValues(hosts, r.Header.Get("X-Forwarded-Host"))
	hosts = appendCommaHeaderValues(hosts, r.Header.Get("X-Original-Host"))
	hosts = appendCommaHeaderValues(hosts, r.Header.Get("X-Host"))
	ports = appendCommaHeaderValues(ports, r.Header.Get("X-Forwarded-Port"))
	ports = appendCommaHeaderValues(ports, r.Header.Get("X-Original-Port"))
	if r.Host != "" {
		hosts = append(hosts, r.Host)
	}

	forwardedProto, forwardedHost := parseForwardedOriginHeader(r.Header.Get("Forwarded"))
	if forwardedProto != "" {
		schemes = append(schemes, forwardedProto)
	}
	if forwardedHost != "" {
		hosts = append(hosts, forwardedHost)
	}
	hosts = appendForwardedPortOriginHosts(hosts, ports)
	schemes = append(schemes, "http", "https")

	return uniqueNonEmpty(schemes), uniqueNonEmpty(hosts)
}

func appendCommaHeaderValues(values []string, header string) []string {
	for _, item := range strings.Split(header, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			values = append(values, item)
		}
	}
	return values
}

func parseForwardedOriginHeader(header string) (proto string, host string) {
	if header == "" {
		return "", ""
	}
	first := strings.Split(header, ",")[0]
	for _, part := range strings.Split(first, ";") {
		keyValue := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(keyValue) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(keyValue[0]))
		value := strings.Trim(strings.TrimSpace(keyValue[1]), `"`)
		switch key {
		case "proto":
			proto = value
		case "host":
			host = value
		}
	}
	return proto, host
}

func appendForwardedPortOriginHosts(hosts []string, ports []string) []string {
	originalHosts := append([]string(nil), hosts...)
	for _, host := range originalHosts {
		for _, port := range ports {
			if candidate := joinOriginHostPort(host, port); candidate != "" {
				hosts = append(hosts, candidate)
			}
		}
	}
	return hosts
}

func joinOriginHostPort(host, port string) string {
	host = strings.TrimSpace(host)
	port = strings.TrimSpace(port)
	if host == "" || port == "" || originHostHasExplicitPort(host) {
		return ""
	}
	if strings.HasPrefix(host, "[") && strings.Contains(host, "]") {
		host = strings.TrimPrefix(strings.Split(host, "]")[0], "[")
	}
	return net.JoinHostPort(host, port)
}

func originHostHasExplicitPort(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	if _, _, err := net.SplitHostPort(host); err == nil {
		return true
	}
	lastColon := strings.LastIndex(host, ":")
	return lastColon > -1 &&
		lastColon == strings.Index(host, ":") &&
		lastColon < len(host)-1
}

func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	return result
}
