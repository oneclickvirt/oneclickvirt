package utils

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
)

// IPv6DefaultGatewayIsLinkLocalJSON reads iproute2's JSON output without
// depending on translated or colored terminal text. A host with multiple
// default routes uses this mode only when every gateway is link local.
func IPv6DefaultGatewayIsLinkLocalJSON(output string) (bool, error) {
	var routes []struct {
		Destination string `json:"dst"`
		Gateway     string `json:"gateway"`
		NextHops    []struct {
			Gateway string `json:"gateway"`
		} `json:"nexthops"`
		Multipath []struct {
			Gateway string `json:"gateway"`
		} `json:"multipath"`
	}
	output = terminalCSISequence.ReplaceAllString(strings.TrimSpace(output), "")
	if err := json.Unmarshal([]byte(output), &routes); err != nil || routes == nil {
		return false, fmt.Errorf("无法解析宿主机IPv6默认路由JSON")
	}
	found := false
	for _, route := range routes {
		// Some older iproute2 versions omit dst for a command already filtered
		// to the default route.
		if route.Destination != "" && route.Destination != "default" {
			continue
		}
		gateways := []string{route.Gateway}
		for _, hop := range route.NextHops {
			gateways = append(gateways, hop.Gateway)
		}
		for _, hop := range route.Multipath {
			gateways = append(gateways, hop.Gateway)
		}
		for _, rawGateway := range gateways {
			if rawGateway == "" {
				continue
			}
			gateway, err := netip.ParseAddr(rawGateway)
			if err != nil || !gateway.Is6() {
				return false, fmt.Errorf("宿主机IPv6默认网关无效")
			}
			found = true
			if !gateway.IsLinkLocalUnicast() {
				return false, nil
			}
		}
	}
	return found, nil
}
