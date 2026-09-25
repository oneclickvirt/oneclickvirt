package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
)

// LXCIPv4FromNetworkLeases resolves a guest address when the instance state
// cannot report it (notably while a VM's guest agent is still unavailable).
// A lease is usable only when its MAC matches exactly one attached bridged NIC.
// The returned state has the same shape as /instances/<name>/state so NAT proxy
// binding can verify which NIC owns the address before changing its config.
func LXCIPv4FromNetworkLeases(ctx context.Context, metadata map[string]interface{}, lookup func(context.Context, string) ([]map[string]interface{}, error)) (string, map[string]interface{}, error) {
	// A DHCP lease can survive a stopped guest. Do not use one if the API
	// explicitly reports that the instance is not running.
	if status, ok := metadata["status"].(string); ok && status != "" && !strings.EqualFold(status, "Running") {
		return "", nil, nil
	}
	type nic struct {
		name, guestName, network, mac string
	}
	config := lxcDeviceMap(metadata["expanded_config"])
	for key, value := range lxcDeviceMap(metadata["config"]) {
		config[key] = value
	}
	expanded, _ := metadata["expanded_devices"].(map[string]interface{})
	var nics []nic
	for name, raw := range expanded {
		device := lxcDeviceMap(raw)
		if device["type"] != "nic" {
			continue
		}
		nicType, _ := device["nictype"].(string)
		if nicType != "" && nicType != "bridged" {
			continue
		}
		network, _ := device["network"].(string)
		if network == "" {
			network, _ = device["parent"].(string)
		}
		if network == "" {
			continue
		}
		mac, _ := device["hwaddr"].(string)
		if mac == "" {
			mac, _ = config["volatile."+name+".hwaddr"].(string)
		}
		parsedMAC, err := net.ParseMAC(mac)
		if err != nil {
			continue
		}
		guestName, _ := device["name"].(string)
		if guestName == "" {
			guestName = name
		}
		nics = append(nics, nic{name: name, guestName: guestName, network: network, mac: parsedMAC.String()})
	}
	sort.Slice(nics, func(a, b int) bool { return nics[a].name < nics[b].name })
	if len(nics) == 0 {
		return "", nil, nil
	}

	leasesByNetwork := make(map[string][]map[string]interface{})
	var foundIP string
	var foundNIC nic
	var lookupErr error
	for _, candidate := range nics {
		leases, ok := leasesByNetwork[candidate.network]
		if !ok {
			if err := ctx.Err(); err != nil {
				return "", nil, err
			}
			var err error
			leases, err = lookup(ctx, candidate.network)
			if err != nil {
				// Unmanaged bridges do not expose a lease endpoint. The caller
				// still has /state and will report a real timeout if needed.
				lookupErr = fmt.Errorf("读取网络 %s 的租约失败: %w", candidate.network, err)
				continue
			}
			leasesByNetwork[candidate.network] = leases
		}
		for _, lease := range leases {
			leaseMAC, _ := lease["hwaddr"].(string)
			parsedMAC, err := net.ParseMAC(leaseMAC)
			if err != nil || parsedMAC.String() != candidate.mac {
				continue
			}
			address, _ := lease["address"].(string)
			ip := net.ParseIP(address)
			if ip == nil || ip.To4() == nil || !ip.IsGlobalUnicast() || ip.IsLinkLocalUnicast() {
				continue
			}
			if foundIP != "" && (foundIP != ip.String() || foundNIC.name != candidate.name) {
				return "", nil, fmt.Errorf("实例网卡匹配到多个不同的IPv4租约")
			}
			foundIP, foundNIC = ip.String(), candidate
		}
	}
	if foundIP == "" {
		return "", nil, lookupErr
	}
	state := map[string]interface{}{
		"network": map[string]interface{}{
			foundNIC.guestName: map[string]interface{}{
				"hwaddr": foundNIC.mac,
				"addresses": []interface{}{map[string]interface{}{
					"family": "inet", "scope": "global", "address": foundIP,
				}},
			},
		},
	}
	return foundIP, state, nil
}

// LXCNetworkLeases decodes the metadata returned by a managed network's
// /leases endpoint. Reject malformed responses rather than guessing an IP.
func LXCNetworkLeases(metadata interface{}) ([]map[string]interface{}, error) {
	raw, ok := metadata.([]interface{})
	if !ok {
		return nil, fmt.Errorf("网络租约响应格式无效")
	}
	leases := make([]map[string]interface{}, 0, len(raw))
	for _, value := range raw {
		lease, ok := value.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("网络租约条目格式无效")
		}
		leases = append(leases, lease)
	}
	return leases, nil
}

// FetchLXCNetworkLeases reads a managed network's lease list through the
// provider's existing authenticated API client.
func FetchLXCNetworkLeases(ctx context.Context, client *http.Client, endpoint string) ([]map[string]interface{}, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("读取网络租约失败: status %d", resp.StatusCode)
	}
	var envelope struct {
		Metadata interface{} `json:"metadata"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, err
	}
	return LXCNetworkLeases(envelope.Metadata)
}
