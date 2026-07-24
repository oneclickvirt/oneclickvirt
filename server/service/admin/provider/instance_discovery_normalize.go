package provider

import (
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"

	providerCore "oneclickvirt/provider"
	"oneclickvirt/utils"

	"github.com/google/uuid"
)

func normalizeDiscoveredInstances(providerID uint, providerType string, instances []providerCore.DiscoveredInstance) []providerCore.DiscoveredInstance {
	result := make([]providerCore.DiscoveredInstance, 0, len(instances))
	for _, instance := range instances {
		remoteUUID := strings.TrimSpace(instance.UUID)
		remoteID := strings.TrimSpace(instance.ProviderInstanceID)
		instance.Name = strings.TrimSpace(instance.Name)
		instance.InstanceType = normalizeDiscoveredInstanceType(providerType, instance.InstanceType)
		if instance.InstanceType == "" {
			continue
		}

		identity := remoteUUID
		if identity == "" {
			identity = remoteID
		}
		if identity == "" {
			identity = instance.InstanceType + "/" + instance.Name
		}
		if strings.TrimSpace(identity) == "/" || strings.TrimSpace(identity) == "" {
			continue
		}

		providerType = strings.ToLower(strings.TrimSpace(providerType))
		instance.UUID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("oneclickvirt:%d:%s:%s", providerID, providerType, identity))).String()
		if remoteID == "" {
			if remoteUUID != "" {
				instance.ProviderInstanceID = remoteUUID
			} else {
				instance.ProviderInstanceID = instance.Name
			}
		}
		if instance.Name == "" {
			shortID := strings.ReplaceAll(instance.UUID, "-", "")
			if len(shortID) > 12 {
				shortID = shortID[:12]
			}
			instance.Name = fmt.Sprintf("%s-%s-%s", providerType, instance.InstanceType, shortID)
		}
		if len(instance.Name) > 128 {
			instance.Name = instance.Name[:128]
		}

		instance.Status = normalizeDiscoveredStatus(instance.Status)
		if instance.CPU < 0 {
			instance.CPU = 0
		}
		if instance.Memory < 0 {
			instance.Memory = 0
		}
		if instance.Disk < 0 {
			instance.Disk = 0
		}
		instance.PrivateIP = normalizeDiscoveredIP(instance.PrivateIP)
		instance.PublicIP = normalizeDiscoveredIP(instance.PublicIP)
		instance.IPv6Address = normalizeDiscoveredIP(instance.IPv6Address)
		instance.OSType = utils.NormalizeOSType(instance.OSType)
		instance.PortMappings, instance.SSHPort, instance.ExtraPorts = normalizeDiscoveredPorts(instance.PortMappings, instance.SSHPort, instance.ExtraPorts)
		instance.RawData = sanitizeDiscoveredRawData(instance.RawData)
		result = append(result, instance)
	}
	sort.SliceStable(result, func(i, j int) bool {
		return discoveredInstanceCanonicalKey(result[i]) < discoveredInstanceCanonicalKey(result[j])
	})
	return result
}

func discoveredInstanceCanonicalKey(instance providerCore.DiscoveredInstance) string {
	return strings.Join([]string{
		strings.ToLower(strings.TrimSpace(instance.Name)),
		strings.TrimSpace(instance.ProviderInstanceID),
		strings.TrimSpace(instance.UUID),
	}, "\x00")
}

func normalizeDiscoveredInstanceType(providerType, instanceType string) string {
	value := strings.ToLower(strings.TrimSpace(instanceType))
	switch value {
	case "vm", "virtual-machine", "virtual_machine", "virtualmachine", "qemu", "kvm":
		return "vm"
	case "container", "ct", "lxc":
		return "container"
	case "":
		switch strings.ToLower(strings.TrimSpace(providerType)) {
		case "docker", "orbstack", "podman", "containerd":
			return "container"
		case "qemu", "kubevirt", "vmware", "virtualbox", "multipass", "vagrant":
			return "vm"
		}
	}
	return ""
}

func normalizeDiscoveredStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "up", "active", "started":
		return "running"
	case "stopped", "down", "inactive", "exited", "shut off", "shutoff", "poweroff", "powered off":
		return "stopped"
	case "paused", "suspended":
		return "paused"
	case "frozen":
		return "frozen"
	case "failed", "dead", "error", "crashed":
		return "failed"
	case "creating", "pending", "starting", "stopping", "restarting":
		return strings.ToLower(strings.TrimSpace(status))
	default:
		return "unknown"
	}
}

func normalizeDiscoveredIP(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if ip, _, err := net.ParseCIDR(value); err == nil {
		return ip.String()
	}
	if ip := net.ParseIP(strings.Trim(value, "[]")); ip != nil {
		return ip.String()
	}
	return ""
}

func normalizeDiscoveredPorts(mappings []providerCore.DiscoveredPortMapping, sshPort int, extraPorts []int) ([]providerCore.DiscoveredPortMapping, int, []int) {
	type mappingKey struct {
		host  int
		guest int
		ssh   bool
	}
	byKey := make(map[mappingKey]providerCore.DiscoveredPortMapping)
	for _, mapping := range mappings {
		if !validDiscoveredPort(mapping.HostPort) || !validDiscoveredPort(mapping.GuestPort) {
			continue
		}
		mapping.Protocol = normalizeDiscoveredProtocol(mapping.Protocol)
		mapping.MappingMethod = normalizeDiscoveredMappingMethod(mapping.MappingMethod)
		key := mappingKey{host: mapping.HostPort, guest: mapping.GuestPort, ssh: mapping.IsSSH || mapping.GuestPort == 22}
		mapping.IsSSH = key.ssh
		if existing, ok := byKey[key]; ok {
			if existing.Protocol != mapping.Protocol {
				existing.Protocol = "both"
			}
			if existing.MappingMethod == "" {
				existing.MappingMethod = mapping.MappingMethod
			}
			byKey[key] = existing
			continue
		}
		byKey[key] = mapping
	}

	result := make([]providerCore.DiscoveredPortMapping, 0, len(byKey))
	for _, mapping := range byKey {
		result = append(result, mapping)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].HostPort != result[j].HostPort {
			return result[i].HostPort < result[j].HostPort
		}
		if result[i].GuestPort != result[j].GuestPort {
			return result[i].GuestPort < result[j].GuestPort
		}
		return result[i].Protocol < result[j].Protocol
	})

	if !validDiscoveredPort(sshPort) {
		sshPort = 0
	}
	mappedSSHPort := 0
	extraSet := make(map[int]struct{})
	for _, mapping := range result {
		if mapping.IsSSH && mappedSSHPort == 0 {
			mappedSSHPort = mapping.HostPort
		}
		if !mapping.IsSSH {
			extraSet[mapping.HostPort] = struct{}{}
		}
	}
	if mappedSSHPort > 0 {
		sshPort = mappedSSHPort
	}
	for _, port := range extraPorts {
		if validDiscoveredPort(port) && port != sshPort {
			extraSet[port] = struct{}{}
		}
	}
	extras := make([]int, 0, len(extraSet))
	for port := range extraSet {
		extras = append(extras, port)
	}
	sort.Ints(extras)
	return result, sshPort, extras
}

func normalizeDiscoveredMappingMethod(method string) string {
	switch strings.ToLower(strings.TrimSpace(method)) {
	case "device_proxy", "proxy":
		return "device_proxy"
	case "iptables", "nft", "nftables", "firewall":
		return "iptables"
	case "native":
		return "native"
	default:
		return ""
	}
}

func validDiscoveredPort(port int) bool {
	return port > 0 && port <= 65535
}

func normalizeDiscoveredProtocol(protocol string) string {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "tcp":
		return "tcp"
	case "udp":
		return "udp"
	case "both", "tcp/udp", "udp/tcp", "":
		return "both"
	default:
		return "both"
	}
}

func marshalSanitizedDiscoveredData(raw interface{}) (string, error) {
	if raw == nil {
		return "", nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return "", err
	}
	var decoded interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return "", err
	}
	sanitized, err := json.Marshal(redactDiscoveredValue(decoded))
	if err != nil {
		return "", err
	}
	return string(sanitized), nil
}

// sanitizeDiscoveredRawData keeps provider diagnostics useful without exposing
// credentials through the discovery endpoint before an administrator imports it.
func sanitizeDiscoveredRawData(raw interface{}) interface{} {
	if raw == nil {
		return nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var decoded interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil
	}
	return redactDiscoveredValue(decoded)
}

func redactDiscoveredValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{}, len(typed))
		for key, child := range typed {
			if isSensitiveDiscoveryKey(key) {
				result[key] = "[REDACTED]"
			} else {
				result[key] = redactDiscoveredValue(child)
			}
		}
		return result
	case []interface{}:
		result := make([]interface{}, 0, len(typed))
		for _, child := range typed {
			if text, ok := child.(string); ok {
				if key, _, found := strings.Cut(text, "="); found && isSensitiveDiscoveryKey(key) {
					result = append(result, key+"=[REDACTED]")
					continue
				}
			}
			result = append(result, redactDiscoveredValue(child))
		}
		return result
	default:
		return value
	}
}

func isSensitiveDiscoveryKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	replacer := strings.NewReplacer("_", "", "-", "", ".", "", " ", "")
	key = replacer.Replace(key)
	if key == "userdescription" || key == "description" {
		return true
	}
	for _, fragment := range []string{"password", "passwd", "token", "secret", "privatekey", "authorization", "userdata", "apikey", "accesskey"} {
		if strings.Contains(key, fragment) {
			return true
		}
	}
	return false
}
