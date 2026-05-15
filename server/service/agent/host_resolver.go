package agent

import "strings"

// ResolveAgentHost returns the host used to call agent HTTP APIs.
// For agent-mode providers, Endpoint may be empty and AgentRemoteIP is the fallback.
func ResolveAgentHost(endpoint, agentRemoteIP string) string {
	host := strings.TrimSpace(endpoint)
	if host != "" {
		return host
	}
	return strings.TrimSpace(agentRemoteIP)
}
