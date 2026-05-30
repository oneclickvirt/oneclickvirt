package utils

import (
	"net"
	"net/url"
	"strconv"
	"strings"
)

func normalizeOrigin(raw string) string {
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
	originNorm := normalizeOrigin(origin)
	frontendNorm := normalizeOrigin(frontendURL)
	if originNorm == "" || frontendNorm == "" {
		return false
	}
	return originNorm == frontendNorm
}
