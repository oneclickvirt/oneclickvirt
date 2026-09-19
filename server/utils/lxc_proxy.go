package utils

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
)

// ResolveNATProxyListenIP selects a concrete address of the requested family.
// Incus 6.0 LTS and LXD reject wildcard listeners in NAT mode. PortIP remains
// preferred; a value of the other family may be followed by Host or discovery.
func ResolveNATProxyListenIP(ctx context.Context, ipv6 bool, candidates ...string) (string, error) {
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		host := strings.TrimSpace(candidate)
		if strings.Contains(host, "://") {
			parsed, err := url.Parse(host)
			if err != nil {
				continue
			}
			host = parsed.Hostname()
		} else if value, _, err := net.SplitHostPort(host); err == nil {
			host = value
		}
		host = strings.Trim(host, "[]")
		if host == "" {
			continue
		}
		if ip := net.ParseIP(host); ip != nil {
			if validNATProxyIP(ip, ipv6) {
				return ip.String(), nil
			}
			continue
		}
		family := "ip4"
		if ipv6 {
			family = "ip6"
		}
		addresses, err := net.DefaultResolver.LookupIP(ctx, family, host)
		if err != nil {
			continue
		}
		for _, address := range addresses {
			if validNATProxyIP(address, ipv6) {
				return address.String(), nil
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("NAT proxy requires a concrete host IPv%d address", map[bool]int{false: 4, true: 6}[ipv6])
}

func validNATProxyIP(ip net.IP, ipv6 bool) bool {
	return ip != nil && ip.IsGlobalUnicast() && !ip.IsLoopback() && (ip.To4() == nil) == ipv6
}

// LXCProxyConnectMatches preserves the runtime's wildcard connect feature:
// it selects a configured static NIC address of the same family. A different
// concrete target, protocol, port or range is still a conflicting device.
func LXCProxyConnectMatches(existing, expected string) bool {
	protocol, endpoint, ok := strings.Cut(existing, ":")
	wantProtocol, wantEndpoint, wantOK := strings.Cut(expected, ":")
	if !ok || !wantOK || protocol != wantProtocol {
		return false
	}
	host, port, err := net.SplitHostPort(endpoint)
	wantHost, wantPort, wantErr := net.SplitHostPort(wantEndpoint)
	ip, wantIP := net.ParseIP(host), net.ParseIP(wantHost)
	if err != nil || wantErr != nil || port != wantPort || ip == nil || wantIP == nil {
		return false
	}
	return ip.Equal(wantIP) || (ip.IsUnspecified() && (ip.To4() == nil) == (wantIP.To4() == nil))
}

func lxcDeviceMap(raw interface{}) map[string]interface{} {
	device := make(map[string]interface{})
	switch value := raw.(type) {
	case map[string]interface{}:
		for key, item := range value {
			device[key] = item
		}
	case map[string]string:
		for key, item := range value {
			device[key] = item
		}
	}
	return device
}

// Routed NICs support more than one static address. Compare parsed addresses
// so an existing reservation remains valid regardless of IPv6 formatting or
// its position in a routed NIC's address list.
func lxcNICHasAddress(raw interface{}, target string) bool {
	value, _ := raw.(string)
	want := net.ParseIP(target)
	if want == nil {
		return false
	}
	for _, entry := range strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
		ip := net.ParseIP(strings.SplitN(entry, "/", 2)[0])
		if ip != nil && ip.Equal(want) {
			return true
		}
	}
	return false
}

// BindLXCProxyNICs pins the addresses actually used by NAT proxy devices to
// their NICs. Expanded profile devices are copied into the instance override;
// the profile and unrelated device properties are never rewritten.
func BindLXCProxyNICs(metadata, devices map[string]interface{}) (bool, error) {
	targets := make(map[string]bool)
	for _, raw := range devices {
		device := lxcDeviceMap(raw)
		if device["type"] != "proxy" || (device["nat"] != "true" && device["nat"] != true) {
			continue
		}
		connect, _ := device["connect"].(string)
		_, endpoint, ok := strings.Cut(connect, ":")
		if !ok {
			return false, fmt.Errorf("invalid NAT proxy connect address %q", connect)
		}
		host, _, err := net.SplitHostPort(endpoint)
		if err != nil || net.ParseIP(host) == nil {
			return false, fmt.Errorf("invalid NAT proxy connect address %q", connect)
		}
		// Wildcard connect is a supported auto-selection feature, unlike a
		// wildcard listen address. Existing devices using it are preserved.
		if !net.ParseIP(host).IsUnspecified() {
			targets[net.ParseIP(host).String()] = true
		}
	}
	return bindLXCAddresses(metadata, devices, targets)
}

func bindLXCAddresses(metadata, devices map[string]interface{}, targets map[string]bool) (bool, error) {
	expanded, _ := metadata["expanded_devices"].(map[string]interface{})
	effective := make(map[string]interface{}, len(expanded)+len(devices))
	for name, raw := range expanded {
		effective[name] = raw
	}
	for name, raw := range devices {
		effective[name] = raw
	}
	state, _ := metadata["_network_state"].(map[string]interface{})
	network, _ := state["network"].(map[string]interface{})
	config := lxcDeviceMap(metadata["expanded_config"])
	for key, value := range lxcDeviceMap(metadata["config"]) {
		config[key] = value
	}
	changed := false
	for target := range targets {
		ip := net.ParseIP(target)
		key := "ipv4.address"
		if ip.To4() == nil {
			key = "ipv6.address"
		}
		var candidates []string
		alreadyBound := false
		for name, raw := range effective {
			device := lxcDeviceMap(raw)
			if device["type"] != "nic" {
				continue
			}
			nicType, _ := device["nictype"].(string)
			if nicType != "bridged" && nicType != "routed" && !(nicType == "" && device["network"] != nil) {
				continue
			}
			if lxcNICHasAddress(device[key], target) {
				alreadyBound = true
				break
			}
			for interfaceName, rawState := range network {
				iface, _ := rawState.(map[string]interface{})
				addresses, _ := iface["addresses"].([]interface{})
				ownsTarget := false
				for _, rawAddress := range addresses {
					address, _ := rawAddress.(map[string]interface{})
					value, _ := address["address"].(string)
					if parsed := net.ParseIP(strings.Split(value, "/")[0]); parsed != nil && parsed.Equal(ip) {
						ownsTarget = true
						break
					}
				}
				if !ownsTarget {
					continue
				}
				guestName, _ := device["name"].(string)
				mac, _ := device["hwaddr"].(string)
				if mac == "" {
					mac, _ = config["volatile."+name+".hwaddr"].(string)
				}
				runtimeMAC, _ := iface["hwaddr"].(string)
				matches := guestName == interfaceName || (guestName == "" && name == interfaceName)
				if mac != "" && runtimeMAC != "" {
					matches = strings.EqualFold(mac, runtimeMAC)
				}
				if matches {
					candidates = append(candidates, name)
					break
				}
			}
		}
		if alreadyBound {
			continue
		}
		sort.Strings(candidates)
		if len(candidates) != 1 {
			return false, fmt.Errorf("cannot uniquely match NAT proxy address %s to an instance NIC", target)
		}
		name := candidates[0]
		device := lxcDeviceMap(effective[name])
		if current, _ := device[key].(string); current != "" {
			return false, fmt.Errorf("NIC %s already configures %s=%s, refusing to replace it with %s", name, key, current, target)
		}
		device[key] = target
		devices[name] = device
		effective[name] = device
		changed = true
	}
	return changed, nil
}
