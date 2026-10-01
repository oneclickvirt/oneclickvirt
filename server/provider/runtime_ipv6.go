package provider

import (
	"fmt"
	"net"
	"sort"
	"strings"

	"oneclickvirt/utils"
)

// InstanceIPv6FromRuntimeState selects one stable guest address from an
// Incus/LXD state response. Map iteration order must not make a refresh switch
// between a private eth0 address and a routed eth1 address.
func InstanceIPv6FromRuntimeState(state map[string]interface{}) string {
	network, _ := state["network"].(map[string]interface{})
	type candidate struct {
		address string
		global  bool
		public  bool
	}
	var candidates []candidate
	for _, raw := range network {
		iface, _ := raw.(map[string]interface{})
		addresses, _ := iface["addresses"].([]interface{})
		for _, rawAddress := range addresses {
			address, _ := rawAddress.(map[string]interface{})
			if address["family"] != "inet6" {
				continue
			}
			ip := net.ParseIP(strings.Split(fmt.Sprint(address["address"]), "/")[0])
			if ip == nil || ip.To4() != nil || !ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			scope, _ := address["scope"].(string)
			candidates = append(candidates, candidate{address: ip.String(), global: scope == "global" || scope == "", public: utils.IsPublicIPv6(ip.String())})
		}
	}
	sort.Slice(candidates, func(a, b int) bool {
		if candidates[a].public != candidates[b].public {
			return candidates[a].public
		}
		if candidates[a].global != candidates[b].global {
			return candidates[a].global
		}
		return candidates[a].address < candidates[b].address
	})
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0].address
}
