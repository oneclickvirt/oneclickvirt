package proxmox

import (
	"encoding/json"
	"fmt"
	"net/netip"

	"oneclickvirt/utils"
)

// QGA returns JSON, not grep-friendly lines. Ignore loopback, link-local,
// unspecified and malformed values and keep public as well as private IPv4.
func proxmoxAgentAddress(output string, ipv6 bool) (string, error) {
	var interfaces []struct {
		Name      string `json:"name"`
		Addresses []struct {
			Address string `json:"ip-address"`
		} `json:"ip-addresses"`
	}
	if err := json.Unmarshal([]byte(utils.StripTerminalCSI(output)), &interfaces); err != nil {
		return "", fmt.Errorf("invalid PVE guest-agent address response")
	}
	for _, item := range interfaces {
		if item.Name == "lo" {
			continue
		}
		for _, candidate := range item.Addresses {
			address, err := netip.ParseAddr(candidate.Address)
			if err == nil && address.Is6() == ipv6 && !address.Is4In6() && address.IsGlobalUnicast() {
				return address.String(), nil
			}
		}
	}
	return "", fmt.Errorf("PVE guest-agent returned no usable address for the requested family")
}
