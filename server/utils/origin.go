package utils

import (
	"net"
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
