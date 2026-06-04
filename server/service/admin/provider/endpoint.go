package provider

import (
	"strings"

	"oneclickvirt/utils"
)

func normalizeProviderEndpointAndPort(endpoint string, sshPort int) (string, int) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "", sshPort
	}

	host, parsedPort := utils.ParseEndpoint(endpoint, sshPort)
	if host != "" {
		endpoint = host
	}
	if parsedPort > 0 {
		sshPort = parsedPort
	}
	if sshPort == 0 {
		sshPort = 22
	}
	return endpoint, sshPort
}
