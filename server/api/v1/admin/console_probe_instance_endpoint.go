package admin

// This file discovers console services on an instance's own routed addresses.
// Dedicated-IP guests do not necessarily have a Port row, so relying only on
// port mappings would hide a real RDP/VNC/SSH listener. Every candidate below
// is still protocol-fingerprinted before it reaches the capability response.

import (
	"net"
	"strings"

	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/utils"
)

const consoleInstanceEndpointTransport = "instance-direct"
const consolePublicInstanceEndpointTransport = "instance-direct-public"

// Standard management/display ports are a bounded discovery hint, not a
// capability declaration. Keep graphical ports first so an RDP/VNC/SPICE
// listener on a second IPv4/IPv6 address is not crowded out by SSH/Telnet;
// non-standard ports remain discoverable through live runtime mappings and
// indexed Port rows.
var consoleInstanceProbePorts = []int{3389, 5900, 5930, 5901, 5902, 5903, 5904, 5905, 6100, 22, 23}

type consoleInstanceEndpointAddress struct {
	host      string
	transport string
}

func collectInstanceConsoleEndpointCandidates(inst providerModel.Instance, p providerModel.Provider) []consoleEndpointCandidate {
	addresses := collectInstanceConsoleEndpointAddresses(inst)
	if len(addresses) == 0 {
		return nil
	}
	candidates := make([]consoleEndpointCandidate, 0, consoleMappedEndpointProbeLimit)
	// Interleave addresses by port. With the request-wide probe cap, walking
	// every port on the first address would otherwise hide a desktop listener on
	// a second dedicated IPv4/IPv6 address.
	for _, port := range consoleInstanceProbePorts {
		for _, address := range addresses {
			candidates = appendConsoleEndpointCandidate(candidates, consoleEndpointCandidate{
				host: address.host, port: port, transport: address.transport, provider: p,
			})
			if len(candidates) >= consoleMappedEndpointProbeLimit {
				return candidates
			}
		}
	}
	return candidates
}

func collectInstanceConsoleEndpointAddresses(inst providerModel.Instance) []consoleInstanceEndpointAddress {
	values := []struct {
		raw      string
		publicIP bool
	}{
		{raw: inst.PublicIP, publicIP: true},
		{raw: inst.PublicIPv6, publicIP: true},
		{raw: inst.PrivateIP},
		{raw: inst.IPv6Address},
	}
	addresses := make([]consoleInstanceEndpointAddress, 0, len(values))
	for _, value := range values {
		host := normalizeInstanceConsoleIP(value.raw)
		if host == "" {
			continue
		}
		transport := consoleInstanceEndpointTransport
		if value.publicIP && isPublicConsoleIP(net.ParseIP(host)) {
			transport = consolePublicInstanceEndpointTransport
		}
		duplicate := false
		for _, existing := range addresses {
			if existing.transport == transport && consoleHostsEqual(existing.host, host) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			addresses = append(addresses, consoleInstanceEndpointAddress{host: host, transport: transport})
		}
	}
	return addresses
}

func normalizeInstanceConsoleIP(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if host, _, err := net.ParseCIDR(raw); err == nil {
		raw = host.String()
	}
	raw = strings.Trim(strings.TrimSpace(utils.ExtractHost(raw)), "[]")
	ip := net.ParseIP(raw)
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() || ip.IsLinkLocalUnicast() {
		return ""
	}
	return ip.String()
}

func isPublicConsoleIP(ip net.IP) bool {
	return ip != nil && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast()
}

func isInstanceDirectConsoleTransport(transport string) bool {
	transport = strings.ToLower(strings.TrimSpace(transport))
	return transport == consoleInstanceEndpointTransport || transport == consolePublicInstanceEndpointTransport
}

func isPublicInstanceDirectConsoleTransport(transport string) bool {
	return strings.EqualFold(strings.TrimSpace(transport), consolePublicInstanceEndpointTransport)
}
