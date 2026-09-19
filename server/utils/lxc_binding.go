package utils

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// SetLXCIPv4Binding runs before the guest is stopped so its NIC can be matched
// by the actual address and MAC, even when the device and guest names differ.
// Only the address property is changed; the CLI preserves the other device
// settings and copies profile devices into instance-local overrides.
func SetLXCIPv4Binding(client ShellExecutor, runtime, instanceName, address string) error {
	return SetLXCAddressBinding(client, runtime, instanceName, address, false)
}

// SetLXCAddressBinding pins the current guest address to its matching NIC.
// NAT proxy devices reject DHCP-only targets, for both IPv4 and IPv6.
func SetLXCAddressBinding(client ShellExecutor, runtime, instanceName, address string, ipv6 bool) error {
	if runtime != "incus" && runtime != "lxc" {
		return fmt.Errorf("unsupported LXC runtime %q", runtime)
	}
	cleanIP := strings.TrimSpace(strings.SplitN(address, "(", 2)[0])
	cleanIP = strings.SplitN(cleanIP, "/", 2)[0]
	ip := net.ParseIP(cleanIP)
	if !validNATProxyIP(ip, ipv6) {
		return fmt.Errorf("无效的实例IP绑定地址 %q", address)
	}
	cleanIP = ip.String()
	family, key := "IPv4", "ipv4.address"
	if ipv6 {
		family, key = "IPv6", "ipv6.address"
	}
	if client == nil {
		return fmt.Errorf("SSH client不可用，无法固定实例IP地址")
	}
	query := func(resource string) (map[string]interface{}, error) {
		output, err := client.ExecuteWithTimeout(runtime+" query "+ShellSingleQuote("/1.0/instances/"+url.PathEscape(instanceName)+resource), 15*time.Second)
		if err != nil {
			return nil, fmt.Errorf("读取实例网络配置失败: %w", err)
		}
		var result map[string]interface{}
		if err := json.Unmarshal([]byte(CleanCommandOutput(output)), &result); err != nil {
			return nil, fmt.Errorf("实例网络配置响应无效: %w", err)
		}
		if metadata, ok := result["metadata"].(map[string]interface{}); ok {
			result = metadata
		}
		if len(result) == 0 || result["error"] != nil {
			return nil, fmt.Errorf("实例网络配置响应为空或包含错误")
		}
		return result, nil
	}
	metadata, err := query("")
	if err != nil {
		return err
	}
	original := lxcDeviceMap(metadata["devices"])
	devices := lxcDeviceMap(metadata["devices"])
	targets := map[string]bool{cleanIP: true}
	if changed, err := bindLXCAddresses(metadata, devices, targets); err == nil && !changed {
		return nil // Already statically bound; no runtime state or write needed.
	}
	state, err := query("/state")
	if err != nil {
		return err
	}
	metadata["_network_state"] = state
	devices = lxcDeviceMap(metadata["devices"])
	changed, err := bindLXCAddresses(metadata, devices, targets)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	for name, raw := range devices {
		device := lxcDeviceMap(raw)
		if device["type"] != "nic" || !lxcNICHasAddress(device[key], cleanIP) || lxcNICHasAddress(lxcDeviceMap(original[name])[key], cleanIP) {
			continue
		}
		args := ShellSingleQuote(instanceName) + " " + ShellSingleQuote(name)
		verb := "set"
		if _, local := original[name]; !local {
			verb = "override"
		}
		command := runtime + " config device " + verb + " " + args + " " + key + "=" + ShellSingleQuote(cleanIP)
		output, err := client.ExecuteWithTimeout(command, 30*time.Second)
		// Incus/LXD managed bridges reject a static IPv6 reservation while
		// stateful DHCP is disabled.  This is a recoverable host configuration
		// problem, not a reason to leave a proxy target in a DHCP-only state:
		// enable stateful DHCP on the matched managed bridge and retry the exact
		// device mutation.  Only the explicit daemon validation diagnostic
		// triggers this fallback; unrelated write failures remain fail-closed.
		if err != nil && ipv6 && strings.Contains(strings.ToLower(output), "dhcp") && strings.Contains(strings.ToLower(output), "ipv6.address") {
			device := lxcDeviceMap(devices[name])
			network, _ := device["network"].(string)
			if strings.TrimSpace(network) != "" && device["nictype"] != "routed" {
				networkCommand := runtime + " network set " + ShellSingleQuote(network) + " ipv6.dhcp.stateful=true"
				if _, networkErr := client.ExecuteWithTimeout(networkCommand, 30*time.Second); networkErr == nil {
					output, err = client.ExecuteWithTimeout(command, 30*time.Second)
				}
			}
		}
		if err != nil && verb == "set" {
			// Older daemons use the separate key/value form. Never fall back to
			// a guessed eth0 or overwrite another NIC when the matched one fails.
			command = runtime + " config device set " + args + " " + key + " " + ShellSingleQuote(cleanIP)
			output, err = client.ExecuteWithTimeout(command, 30*time.Second)
		}
		if err != nil {
			return fmt.Errorf("固定网卡 %s 的%s地址失败: %w: %s", name, family, err, CleanCommandOutput(output))
		}
		verified, err := query("")
		if err != nil {
			return err
		}
		verifiedDevices := lxcDeviceMap(verified["expanded_devices"])
		for deviceName, value := range lxcDeviceMap(verified["devices"]) {
			verifiedDevices[deviceName] = value
		}
		if !lxcNICHasAddress(lxcDeviceMap(verifiedDevices[name])[key], cleanIP) {
			return fmt.Errorf("网卡 %s 的%s地址绑定未生效", name, family)
		}
		return nil
	}
	return fmt.Errorf("未找到需要固定%s地址的实例网卡", family)
}
