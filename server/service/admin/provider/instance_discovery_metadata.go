package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net"
	"net/url"
	"strconv"
	"strings"

	"oneclickvirt/global"
	providerCore "oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

const (
	maxDiscoveredMetadataPasswordLength = 128
	maxDiscoveredMetadataUsernameLength = 64
	maxMetadataRangePorts               = 128
	maxMetadataLogLines                 = 50000
)

// discoveredImportMetadata is intentionally kept separate from RawData. It can
// carry a credential in memory while a discovery response and DiscoveredData
// remain redacted.
type discoveredImportMetadata struct {
	Username     string
	Password     string
	PrivateIP    string
	PublicIP     string
	IPv6Address  string
	OSType       string
	SSHPort      int
	PortMappings []providerCore.DiscoveredPortMapping
}

// enrichDiscoveredInstanceMetadata runs before normalization and before the
// import transaction. Every supported runtime uses a bounded batch read rather
// than one remote command per instance, so a large import does not become N+1.
func (s *Service) enrichDiscoveredInstanceMetadata(ctx context.Context, providerType string, remote providerCore.Provider, instances []providerCore.DiscoveredInstance) []providerCore.DiscoveredInstance {
	if len(instances) == 0 || remote == nil || ctx.Err() != nil {
		return instances
	}

	switch strings.ToLower(strings.TrimSpace(providerType)) {
	case "proxmox", "proxmoxve", "pve":
		return enrichProxmoxDiscoveredMetadata(ctx, remote, instances)
	case "lxd", "incus":
		instances = enrichDescriptionDiscoveredMetadata(instances)
		// Batch scripts also retain the same compact record in /root/log. Use it
		// as a fallback when user.description is absent or incomplete; applying it
		// again is harmless because runtime/description values remain authoritative.
		return enrichLogDiscoveredMetadata(ctx, remote, instances, []string{"/root/log", "$HOME/log"}, parseDescriptionMetadata)
	case "docker", "orbstack":
		return enrichLogDiscoveredMetadata(ctx, remote, instances, []string{"/root/dclog", "$HOME/dclog"}, parseShellContainerMetadata)
	case "podman", "containerd":
		return enrichLogDiscoveredMetadata(ctx, remote, instances, []string{"/root/ctlog", "$HOME/ctlog"}, parseShellContainerMetadata)
	case "qemu":
		return enrichLogDiscoveredMetadata(ctx, remote, instances, []string{"/root/vmlog", "$HOME/vmlog"}, parseQEMURecordMetadata)
	case "kubevirt":
		instances = enrichKubeVirtAnnotationMetadata(ctx, remote, instances)
		return enrichLogDiscoveredMetadata(ctx, remote, instances, []string{"/root/vmlog", "$HOME/vmlog"}, parseKubeVirtRecordMetadata)
	default:
		return instances
	}
}

func enrichDescriptionDiscoveredMetadata(instances []providerCore.DiscoveredInstance) []providerCore.DiscoveredInstance {
	for index := range instances {
		description := discoveredUserDescription(instances[index].RawData)
		if description == "" {
			continue
		}
		if metadata, ok := parseDescriptionMetadata(description, instances[index].Name); ok {
			applyDiscoveredImportMetadata(&instances[index], metadata)
		}
	}
	return instances
}

func discoveredUserDescription(raw interface{}) string {
	if raw == nil {
		return ""
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return ""
	}
	var decoded struct {
		Description    string            `json:"description"`
		Config         map[string]string `json:"config"`
		ExpandedConfig map[string]string `json:"expanded_config"`
	}
	if json.Unmarshal(data, &decoded) != nil {
		return ""
	}
	if value := strings.TrimSpace(decoded.Config["user.description"]); value != "" {
		return value
	}
	if value := strings.TrimSpace(decoded.ExpandedConfig["user.description"]); value != "" {
		return value
	}
	return strings.TrimSpace(decoded.Description)
}

func enrichProxmoxDiscoveredMetadata(ctx context.Context, remote providerCore.Provider, instances []providerCore.DiscoveredInstance) []providerCore.DiscoveredInstance {
	// API-backed discovery can provide the PVE config note in RawData. Apply it
	// first, then let the one-shot host read fill any metadata the API omitted.
	for index := range instances {
		if description := discoveredUserDescription(instances[index].RawData); description != "" {
			applyDiscoveredImportMetadata(&instances[index], parseProxmoxCommentMetadata(description))
		}
	}

	command := buildProxmoxMetadataReadCommand(instances)
	if command == "" {
		return instances
	}
	output, err := remote.ExecuteSSHCommand(ctx, command)
	if err != nil {
		// Some executor implementations include partial command output in errors.
		// Config comments may contain a password, so never attach that error to logs.
		global.APP_LOG.Debug("读取Proxmox实例备注失败，跳过自动凭据回填", zap.String("providerType", remote.GetType()))
		return instances
	}

	byRemoteID := make(map[string]*providerCore.DiscoveredInstance, len(instances))
	for index := range instances {
		key := proxmoxMetadataKey(instances[index].InstanceType, instances[index].ProviderInstanceID)
		if key != "" {
			byRemoteID[key] = &instances[index]
		}
	}

	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 1024), 256*1024)
	var currentKey string
	var content strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "__OCV_IMPORT_META__\t") {
			parts := strings.Split(line, "\t")
			if len(parts) == 3 {
				currentKey = proxmoxMetadataKey(parts[1], parts[2])
				content.Reset()
			}
			continue
		}
		if line == "__OCV_IMPORT_META_END__" {
			if instance := byRemoteID[currentKey]; instance != nil {
				applyDiscoveredImportMetadata(instance, parseProxmoxCommentMetadata(content.String()))
			}
			currentKey = ""
			content.Reset()
			continue
		}
		if currentKey != "" {
			content.WriteString(line)
			content.WriteByte('\n')
		}
	}
	if err := scanner.Err(); err != nil {
		global.APP_LOG.Debug("解析Proxmox实例备注失败，跳过自动凭据回填", zap.Error(err))
	}
	return instances
}

func buildProxmoxMetadataReadCommand(instances []providerCore.DiscoveredInstance) string {
	var builder strings.Builder
	for _, instance := range instances {
		id, err := strconv.Atoi(strings.TrimSpace(instance.ProviderInstanceID))
		if err != nil || id <= 0 {
			continue
		}
		kind := "vm"
		path := fmt.Sprintf("/etc/pve/qemu-server/%d.conf", id)
		if isProxmoxContainerInstanceType(instance.InstanceType) {
			kind = "container"
			path = fmt.Sprintf("/etc/pve/lxc/%d.conf", id)
		}
		quotedPath := utils.ShellSingleQuote(path)
		fmt.Fprintf(&builder, "if [ -r %s ]; then printf '__OCV_IMPORT_META__\\t%s\\t%d\\n'; sed -n '1,160p' %s 2>/dev/null; printf '__OCV_IMPORT_META_END__\\n'; fi\n", quotedPath, kind, id, quotedPath)
	}
	return builder.String()
}

func isProxmoxContainerInstanceType(instanceType string) bool {
	switch strings.ToLower(strings.TrimSpace(instanceType)) {
	case "container", "ct", "lxc":
		return true
	default:
		return false
	}
}

func proxmoxMetadataKey(instanceType, id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	if isProxmoxContainerInstanceType(instanceType) {
		return "container\x00" + id
	}
	return "vm\x00" + id
}

func enrichLogDiscoveredMetadata(ctx context.Context, remote providerCore.Provider, instances []providerCore.DiscoveredInstance, paths []string, parse func(string, string) (discoveredImportMetadata, bool)) []providerCore.DiscoveredInstance {
	command := buildMetadataLogLookupCommand(instances, paths)
	if command == "" {
		return instances
	}
	output, err := remote.ExecuteSSHCommand(ctx, command)
	if err != nil {
		// A transport failure can carry partial log output; do not risk logging it.
		global.APP_LOG.Debug("读取实例信息文件失败，跳过自动凭据回填", zap.String("providerType", remote.GetType()))
		return instances
	}
	byName := make(map[string]*providerCore.DiscoveredInstance, len(instances))
	for index := range instances {
		name := strings.TrimSpace(instances[index].Name)
		if name != "" {
			byName[name] = &instances[index]
		}
	}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		instance := byName[fields[0]]
		if instance == nil {
			continue
		}
		if metadata, ok := parse(line, instance.Name); ok {
			applyDiscoveredImportMetadata(instance, metadata)
		}
	}
	return instances
}

func buildMetadataLogLookupCommand(instances []providerCore.DiscoveredInstance, paths []string) string {
	patterns := make([]string, 0, len(instances))
	seen := make(map[string]struct{})
	for _, instance := range instances {
		name := strings.TrimSpace(instance.Name)
		if name == "" || strings.ContainsAny(name, "\r\n\x00") {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		patterns = append(patterns, utils.ShellSingleQuote(name))
	}
	if len(patterns) == 0 || len(paths) == 0 {
		return ""
	}

	quotedPaths := make([]string, 0, len(paths))
	for _, path := range paths {
		if strings.TrimSpace(path) != "" {
			quotedPaths = append(quotedPaths, quoteMetadataPath(path))
		}
	}
	if len(quotedPaths) == 0 {
		return ""
	}

	return fmt.Sprintf(`for metadata_file in %s; do
  [ -r "$metadata_file" ] || continue
  metadata_count=0
  while IFS= read -r metadata_line || [ -n "$metadata_line" ]; do
    metadata_count=$((metadata_count + 1))
    [ "$metadata_count" -le %d ] || break
    metadata_name=${metadata_line%%[[:space:]]*}
    case "$metadata_name" in
      %s) printf '%%s\n' "$metadata_line" ;;
    esac
  done < "$metadata_file"
done`, strings.Join(quotedPaths, " "), maxMetadataLogLines, strings.Join(patterns, "|"))
}

func quoteMetadataPath(path string) string {
	switch path {
	case "$HOME/log", "$HOME/dclog", "$HOME/ctlog", "$HOME/vmlog":
		// These are fixed lookup paths from the supported scripts; keep $HOME
		// expandable for rootless runtimes without accepting arbitrary input.
		return `"` + path + `"`
	default:
		return utils.ShellSingleQuote(path)
	}
}

func enrichKubeVirtAnnotationMetadata(ctx context.Context, remote providerCore.Provider, instances []providerCore.DiscoveredInstance) []providerCore.DiscoveredInstance {
	hasNames := false
	for _, instance := range instances {
		if name := strings.TrimSpace(instance.Name); name != "" && !strings.ContainsAny(name, "\r\n\x00") {
			hasNames = true
			break
		}
	}
	if !hasNames {
		return instances
	}
	// Read one list object and match names locally. `kubectl get vm name1 name2`
	// is not a portable batch form: depending on the kubectl version it returns
	// a single object or rejects the argument list, leaving annotation metadata
	// unavailable for all but one imported VM.
	command := buildKubeVirtMetadataReadCommand()
	output, err := remote.ExecuteSSHCommand(ctx, command)
	if err != nil || strings.TrimSpace(output) == "" {
		return instances
	}
	var payload struct {
		Items []struct {
			Metadata struct {
				Name        string            `json:"name"`
				Annotations map[string]string `json:"annotations"`
				Labels      map[string]string `json:"labels"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		global.APP_LOG.Debug("解析KubeVirt实例注解失败，跳过自动凭据回填", zap.Error(err))
		return instances
	}
	byName := make(map[string]*providerCore.DiscoveredInstance, len(instances))
	for index := range instances {
		byName[instances[index].Name] = &instances[index]
	}
	for _, item := range payload.Items {
		if instance := byName[item.Metadata.Name]; instance != nil {
			applyDiscoveredImportMetadata(instance, parseKubeVirtAnnotationMetadata(item.Metadata.Annotations, item.Metadata.Labels))
		}
	}
	return instances
}

func buildKubeVirtMetadataReadCommand() string {
	return "KUBECONFIG='/etc/rancher/k3s/k3s.yaml' kubectl get vm -n 'kubevirt-vms' -o json 2>/dev/null || true"
}

func parseProxmoxCommentMetadata(content string) discoveredImportMetadata {
	metadata := discoveredImportMetadata{}
	var rangeStart, rangeEnd int
	for _, line := range proxmoxMetadataLines(content) {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		label := strings.Trim(fields[0], ":：")
		value := strings.TrimSpace(strings.TrimPrefix(line, label))
		value = strings.TrimLeft(value, ":： ")
		normalizedLabel := strings.ToLower(strings.NewReplacer("_", "", "-", "", ".", "", " ", "").Replace(label))
		switch {
		case strings.Contains(normalizedLabel, "password") || strings.Contains(label, "密码"):
			metadata.Password = normalizeDiscoveredMetadataPassword(value)
		case strings.Contains(normalizedLabel, "username") || strings.Contains(label, "用户名"):
			metadata.Username = normalizeDiscoveredMetadataUsername(value)
		case strings.Contains(normalizedLabel, "ssh") && (strings.Contains(normalizedLabel, "port") || strings.Contains(label, "端口")):
			metadata.SSHPort = parseDiscoveredMetadataPort(value)
		case strings.Contains(label, "80端口"):
			metadata.PortMappings = append(metadata.PortMappings, discoveredMetadataMapping(parseDiscoveredMetadataPort(value), 80, "tcp"))
		case strings.Contains(label, "443端口"):
			metadata.PortMappings = append(metadata.PortMappings, discoveredMetadataMapping(parseDiscoveredMetadataPort(value), 443, "tcp"))
		case strings.Contains(normalizedLabel, "portstart") || strings.Contains(label, "端口起"):
			rangeStart = parseDiscoveredMetadataPort(value)
		case strings.Contains(normalizedLabel, "portend") || strings.Contains(label, "端口止"):
			rangeEnd = parseDiscoveredMetadataPort(value)
		case strings.Contains(normalizedLabel, "ipv6") || strings.Contains(label, "IPv6"):
			metadata.IPv6Address = parseDiscoveredMetadataIP(value, true)
		case strings.Contains(normalizedLabel, "internalip") || strings.Contains(label, "内网IP"):
			metadata.PrivateIP = parseDiscoveredMetadataIP(value, false)
		case strings.Contains(normalizedLabel, "ipv4") || strings.Contains(label, "外网IP"):
			metadata.PublicIP = parseDiscoveredMetadataIP(value, false)
		case strings.Contains(normalizedLabel, "system") || strings.Contains(label, "系统"):
			metadata.OSType = normalizeDiscoveredMetadataOSType(value)
		}
	}
	// The PVE remark records the external SSH port separately from the ordinary
	// port range. Persist it as an explicit mapping so import creates the row
	// that ResolveInstanceSSHTarget uses for WebSSH, rather than treating it as
	// a port on the guest private address.
	if metadata.SSHPort > 0 {
		metadata.PortMappings = append(metadata.PortMappings, discoveredMetadataMapping(metadata.SSHPort, 22, "tcp"))
	}
	metadata.PortMappings = append(metadata.PortMappings, discoveredMetadataRangeMappings(rangeStart, rangeEnd)...)
	if metadata.Username == "" && metadata.Password != "" {
		metadata.Username = "root"
	}
	return metadata
}

// proxmoxMetadataLines accepts both formats produced by supported PVE paths:
// the older scripts prepend "# label value" records to the config, while the
// panel/API can store the same payload in an escaped description field.
func proxmoxMetadataLines(content string) []string {
	var lines []string
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			lines = append(lines, strings.TrimSpace(strings.TrimPrefix(line, "#")))
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if found && strings.EqualFold(strings.TrimSpace(key), "description") {
			lines = append(lines, strings.Split(decodeProxmoxDescription(value), "\n")...)
		}
	}

	// API discovery can already provide the description value itself instead of
	// a complete PVE config. Treat it as metadata only when no config wrapper
	// was found; individual lines still have to match a recognized label below.
	if len(lines) == 0 {
		return strings.Split(decodeProxmoxDescription(content), "\n")
	}
	return lines
}

func decodeProxmoxDescription(value string) string {
	value = strings.TrimSpace(value)
	if decoded, err := url.PathUnescape(value); err == nil {
		value = decoded
	}
	value = html.UnescapeString(value)
	for _, marker := range []string{"<br />", "<br/>", "<br>"} {
		value = strings.ReplaceAll(value, marker, "\n")
	}
	return value
}

func parseDescriptionMetadata(description, expectedName string) (discoveredImportMetadata, bool) {
	fields := strings.Fields(description)
	if (len(fields) != 3 && len(fields) != 5) || fields[0] != expectedName {
		return discoveredImportMetadata{}, false
	}
	metadata := discoveredImportMetadata{
		Username: "root",
		Password: normalizeDiscoveredMetadataPassword(fields[2]),
		SSHPort:  parseDiscoveredMetadataPort(fields[1]),
	}
	if metadata.Password == "" || metadata.SSHPort == 0 {
		return discoveredImportMetadata{}, false
	}
	metadata.PortMappings = append(metadata.PortMappings, discoveredMetadataMapping(metadata.SSHPort, 22, "tcp"))
	if len(fields) == 5 {
		metadata.PortMappings = append(metadata.PortMappings, discoveredMetadataRangeMappings(parseDiscoveredMetadataPort(fields[3]), parseDiscoveredMetadataPort(fields[4]))...)
	}
	return metadata, true
}

func parseShellContainerMetadata(line, expectedName string) (discoveredImportMetadata, bool) {
	fields := strings.Fields(line)
	if len(fields) < 7 || fields[0] != expectedName {
		return discoveredImportMetadata{}, false
	}
	metadata := discoveredImportMetadata{
		Username: "root",
		Password: normalizeDiscoveredMetadataPassword(fields[2]),
		SSHPort:  parseDiscoveredMetadataPort(fields[1]),
	}
	if metadata.Password == "" || metadata.SSHPort == 0 {
		return discoveredImportMetadata{}, false
	}
	metadata.PortMappings = append(metadata.PortMappings, discoveredMetadataMapping(metadata.SSHPort, 22, "tcp"))
	metadata.PortMappings = append(metadata.PortMappings, discoveredMetadataRangeMappings(parseDiscoveredMetadataPort(fields[5]), parseDiscoveredMetadataPort(fields[6]))...)
	return metadata, true
}

func parseQEMURecordMetadata(line, expectedName string) (discoveredImportMetadata, bool) {
	fields := strings.Fields(line)
	if len(fields) < 10 || fields[0] != expectedName {
		return discoveredImportMetadata{}, false
	}
	metadata := discoveredImportMetadata{
		Username:  "root",
		Password:  normalizeDiscoveredMetadataPassword(fields[2]),
		SSHPort:   parseDiscoveredMetadataPort(fields[1]),
		OSType:    normalizeDiscoveredMetadataOSType(fields[8]),
		PrivateIP: parseDiscoveredMetadataIP(fields[9], false),
	}
	if metadata.Password == "" || metadata.SSHPort == 0 {
		return discoveredImportMetadata{}, false
	}
	metadata.PortMappings = append(metadata.PortMappings, discoveredMetadataMapping(metadata.SSHPort, 22, "tcp"))
	metadata.PortMappings = append(metadata.PortMappings, discoveredMetadataRangeMappings(parseDiscoveredMetadataPort(fields[6]), parseDiscoveredMetadataPort(fields[7]))...)
	return metadata, true
}

func parseKubeVirtRecordMetadata(line, expectedName string) (discoveredImportMetadata, bool) {
	fields := strings.Fields(line)
	if len(fields) < 4 || fields[0] != expectedName || !strings.HasPrefix(fields[1], "root@") {
		return discoveredImportMetadata{}, false
	}
	endpoint := strings.TrimPrefix(fields[1], "root@")
	separator := strings.LastIndex(endpoint, ":")
	if separator <= 0 {
		return discoveredImportMetadata{}, false
	}
	metadata := discoveredImportMetadata{
		Username: "root",
		PublicIP: parseDiscoveredMetadataIP(strings.Trim(endpoint[:separator], "[]"), false),
		SSHPort:  parseDiscoveredMetadataPort(endpoint[separator+1:]),
	}
	for index := 2; index+1 < len(fields); index++ {
		switch fields[index] {
		case "密码:":
			metadata.Password = normalizeDiscoveredMetadataPassword(fields[index+1])
		case "端口范围:":
			rangeParts := strings.SplitN(fields[index+1], "-", 2)
			if len(rangeParts) == 2 {
				metadata.PortMappings = append(metadata.PortMappings, discoveredMetadataRangeMappings(parseDiscoveredMetadataPort(rangeParts[0]), parseDiscoveredMetadataPort(rangeParts[1]))...)
			}
		case "系统:":
			metadata.OSType = normalizeDiscoveredMetadataOSType(fields[index+1])
		}
	}
	if metadata.Password == "" || metadata.SSHPort == 0 {
		return discoveredImportMetadata{}, false
	}
	metadata.PortMappings = append(metadata.PortMappings, discoveredMetadataMapping(metadata.SSHPort, 22, "tcp"))
	return metadata, true
}

func parseKubeVirtAnnotationMetadata(annotations, labels map[string]string) discoveredImportMetadata {
	metadata := discoveredImportMetadata{
		Username: "root",
		SSHPort:  parseDiscoveredMetadataPort(annotations["kubevirt.io/ssh-port"]),
		OSType:   normalizeDiscoveredMetadataOSType(labels["vm-system"]),
	}
	if strings.EqualFold(strings.TrimSpace(annotations["kubevirt.io/password-stored"]), "true") {
		metadata.Password = normalizeDiscoveredMetadataPassword(annotations["kubevirt.io/password"])
	}
	if metadata.SSHPort > 0 {
		metadata.PortMappings = append(metadata.PortMappings, discoveredMetadataMapping(metadata.SSHPort, 22, "tcp"))
	}
	metadata.PortMappings = append(metadata.PortMappings, discoveredMetadataRangeMappings(
		parseDiscoveredMetadataPort(annotations["kubevirt.io/start-port"]),
		parseDiscoveredMetadataPort(annotations["kubevirt.io/end-port"]),
	)...)
	return metadata
}

func applyDiscoveredImportMetadata(instance *providerCore.DiscoveredInstance, metadata discoveredImportMetadata) {
	if instance == nil {
		return
	}
	if instance.Username == "" {
		instance.Username = metadata.Username
	}
	if instance.Password == "" {
		instance.Password = metadata.Password
	}
	if instance.PrivateIP == "" {
		instance.PrivateIP = metadata.PrivateIP
	}
	if instance.PublicIP == "" {
		instance.PublicIP = metadata.PublicIP
	}
	if instance.IPv6Address == "" {
		instance.IPv6Address = metadata.IPv6Address
	}
	if instance.OSType == "" {
		instance.OSType = metadata.OSType
	}

	hasSSHMappings := false
	for _, mapping := range instance.PortMappings {
		if mapping.IsSSH || mapping.GuestPort == 22 {
			hasSSHMappings = true
			break
		}
	}
	// A provider may have a concrete runtime SSHPort but no detailed mapping
	// row. Treat it as authoritative too; a stale script log must not replace
	// the endpoint WebSSH will use.
	if !hasSSHMappings && instance.SSHPort > 22 {
		hasSSHMappings = true
	}
	if !hasSSHMappings && metadata.SSHPort > 0 && (instance.SSHPort == 0 || instance.SSHPort == 22) {
		instance.SSHPort = metadata.SSHPort
	}

	for _, mapping := range metadata.PortMappings {
		if mapping.HostPort == 0 || mapping.GuestPort == 0 || discoveredMetadataMappingConflicts(instance.PortMappings, mapping) {
			continue
		}
		// Runtime inspection is authoritative for SSH. A stale log must not add a
		// second SSH endpoint that could make WebSSH select the wrong port.
		if (mapping.IsSSH || mapping.GuestPort == 22) && hasSSHMappings {
			continue
		}
		instance.PortMappings = append(instance.PortMappings, mapping)
		if mapping.IsSSH || mapping.GuestPort == 22 {
			if !hasSSHMappings {
				instance.SSHPort = mapping.HostPort
				hasSSHMappings = true
			}
			continue
		}
		instance.ExtraPorts = append(instance.ExtraPorts, mapping.HostPort)
	}
}

func discoveredMetadataMappingConflicts(existing []providerCore.DiscoveredPortMapping, candidate providerCore.DiscoveredPortMapping) bool {
	for _, mapping := range existing {
		if mapping.HostPort == candidate.HostPort {
			return true
		}
	}
	return false
}

func discoveredMetadataMapping(hostPort, guestPort int, protocol string) providerCore.DiscoveredPortMapping {
	if hostPort == 0 || guestPort == 0 {
		return providerCore.DiscoveredPortMapping{}
	}
	return providerCore.DiscoveredPortMapping{
		HostPort:      hostPort,
		GuestPort:     guestPort,
		Protocol:      protocol,
		IsSSH:         guestPort == 22,
		MappingMethod: "iptables",
	}
}

func discoveredMetadataRangeMappings(start, end int) []providerCore.DiscoveredPortMapping {
	if start <= 0 || end < start || end-start+1 > maxMetadataRangePorts {
		return nil
	}
	mappings := make([]providerCore.DiscoveredPortMapping, 0, end-start+1)
	for port := start; port <= end; port++ {
		mappings = append(mappings, discoveredMetadataMapping(port, port, "both"))
	}
	return mappings
}

func parseDiscoveredMetadataPort(value string) int {
	value = strings.TrimSpace(value)
	if strings.ContainsAny(value, "\r\n\x00") {
		return 0
	}
	port, err := strconv.Atoi(value)
	if err != nil || port <= 0 || port > 65535 {
		return 0
	}
	return port
}

func parseDiscoveredMetadataIP(value string, wantIPv6 bool) string {
	value = strings.Trim(strings.TrimSpace(value), "[]")
	if strings.ContainsAny(value, "\r\n\x00") {
		return ""
	}
	if ip, _, err := net.ParseCIDR(value); err == nil {
		if wantIPv6 == (ip.To4() == nil) {
			return ip.String()
		}
		return ""
	}
	ip := net.ParseIP(value)
	if ip == nil || wantIPv6 != (ip.To4() == nil) {
		return ""
	}
	return ip.String()
}

func normalizeDiscoveredMetadataUsername(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxDiscoveredMetadataUsernameLength || strings.ContainsAny(value, "\r\n\x00 \t") {
		return ""
	}
	return value
}

func normalizeDiscoveredMetadataPassword(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > maxDiscoveredMetadataPasswordLength || strings.ContainsAny(value, "\r\n\x00") {
		return ""
	}
	return value
}

func normalizeDiscoveredMetadataOSType(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 || strings.ContainsAny(value, "\r\n\x00") {
		return ""
	}
	return value
}
