package provider

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	providerCore "oneclickvirt/provider"
)

func TestParseProxmoxCommentMetadataAndApply(t *testing.T) {
	metadata := parseProxmoxCommentMetadata(`
# VMID 101
# 用户名-username admin
# 密码-password TestPass123
# SSH端口 22001
# 80端口 22080
# 443端口 22443
# 外网端口起-port-start 30001
# 外网端口止-port-end 30003
# 系统-system debian12
# 内网IP-internal-ip 172.16.1.101
# 独立IPV6地址-ipv6_address 2001:db8::101
`)
	if metadata.Username != "admin" || metadata.Password != "TestPass123" || metadata.SSHPort != 22001 {
		t.Fatalf("unexpected credentials metadata: %#v", metadata)
	}
	if metadata.PrivateIP != "172.16.1.101" || metadata.IPv6Address != "2001:db8::101" || metadata.OSType != "debian12" {
		t.Fatalf("unexpected address/system metadata: %#v", metadata)
	}
	if len(metadata.PortMappings) != 6 {
		t.Fatalf("metadata mappings = %#v, want SSH, web ports, and three range ports", metadata.PortMappings)
	}
	if !hasDiscoveredSSHMapping(metadata.PortMappings, 22001) {
		t.Fatalf("PVE SSH metadata did not produce a WebSSH mapping: %#v", metadata.PortMappings)
	}

	instance := providerCore.DiscoveredInstance{SSHPort: 22}
	applyDiscoveredImportMetadata(&instance, metadata)
	if instance.Username != "admin" || instance.Password != "TestPass123" || instance.SSHPort != 22001 {
		t.Fatalf("metadata was not applied to import instance: %#v", instance)
	}
	if len(instance.PortMappings) != 6 {
		t.Fatalf("applied mappings = %#v", instance.PortMappings)
	}
}

func TestParseProxmoxScriptMetadataForLXC(t *testing.T) {
	// This is the exact field order emitted by pve/scripts/buildct.sh before it
	// prepends the lines to /etc/pve/lxc/<CTID>.conf.
	metadata := parseProxmoxCommentMetadata(`
# CTID 102
# root密码-password CtPass123
# CPU核数-CPU 1
# 内存-memory 512
# 硬盘-disk 5
# SSH端口 22002
# 80端口 22080
# 443端口 22443
# 外网端口起-port-start 30010
# 外网端口止-port-end 30012
# 系统-system debian12
# 存储盘-storage local
# 独立IPV6地址-ipv6_address 2001:db8::102
arch: amd64
`)
	if metadata.Username != "root" || metadata.Password != "CtPass123" || metadata.SSHPort != 22002 {
		t.Fatalf("unexpected LXC credentials metadata: %#v", metadata)
	}
	if metadata.OSType != "debian12" || metadata.IPv6Address != "2001:db8::102" {
		t.Fatalf("unexpected LXC system/address metadata: %#v", metadata)
	}
	if len(metadata.PortMappings) != 6 {
		t.Fatalf("metadata mappings = %#v, want SSH, web ports, and range", metadata.PortMappings)
	}
}

func TestParseProxmoxDedicatedAddressMetadata(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		wantPublic string
		wantIPv6   string
	}{
		{
			name: "dedicated IPv4 VM",
			content: `
# VMID 103
# 用户名-username root
# 密码-password DedicatedPass123
# 系统-system debian12
# 外网IP地址-ipv4 192.0.2.103
`,
			wantPublic: "192.0.2.103",
		},
		{
			name: "IPv6-only VM",
			content: `
# VMID 104
# 用户名-username root
# 密码-password IPv6OnlyPass123
# 系统-system debian12
# 外网IPV6-ipv6 2001:db8::104
`,
			wantIPv6: "2001:db8::104",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metadata := parseProxmoxCommentMetadata(tt.content)
			if metadata.Username != "root" || metadata.Password == "" {
				t.Fatalf("credentials metadata = %#v", metadata)
			}
			if metadata.PublicIP != tt.wantPublic || metadata.IPv6Address != tt.wantIPv6 {
				t.Fatalf("address metadata = %#v", metadata)
			}
			if metadata.SSHPort != 0 || len(metadata.PortMappings) != 0 {
				t.Fatalf("dedicated address unexpectedly created NAT mappings: %#v", metadata)
			}
		})
	}
}

func TestParseProxmoxPanelDescriptionMetadata(t *testing.T) {
	metadata := parseProxmoxCommentMetadata("description: 用户名-username%20admin%0A密码-password%20PanelPass123%0ASSH端口:%2022001%0A80端口:%2022080%0A443端口:%2022443%0A外网端口起-port-start:%2030010%0A外网端口止-port-end:%2030011%0A系统-system:%20debian12")
	if metadata.Username != "admin" || metadata.Password != "PanelPass123" || metadata.SSHPort != 22001 {
		t.Fatalf("unexpected panel description credentials: %#v", metadata)
	}
	if metadata.OSType != "debian12" || len(metadata.PortMappings) != 5 {
		t.Fatalf("unexpected panel description metadata: %#v", metadata)
	}
	if !hasDiscoveredSSHMapping(metadata.PortMappings, 22001) {
		t.Fatalf("panel description did not produce a WebSSH mapping: %#v", metadata.PortMappings)
	}
}

func hasDiscoveredSSHMapping(mappings []providerCore.DiscoveredPortMapping, hostPort int) bool {
	for _, mapping := range mappings {
		if mapping.IsSSH && mapping.HostPort == hostPort && mapping.GuestPort == 22 {
			return true
		}
	}
	return false
}

func TestMetadataDoesNotOverrideRuntimeSSHMapping(t *testing.T) {
	instance := providerCore.DiscoveredInstance{
		SSHPort: 22009,
		PortMappings: []providerCore.DiscoveredPortMapping{{
			HostPort: 22009, GuestPort: 22, Protocol: "tcp", IsSSH: true,
		}},
	}
	applyDiscoveredImportMetadata(&instance, discoveredImportMetadata{
		Username: "root", Password: "TestPass123", SSHPort: 22001,
		PortMappings: []providerCore.DiscoveredPortMapping{discoveredMetadataMapping(22001, 22, "tcp")},
	})
	if instance.SSHPort != 22009 || len(instance.PortMappings) != 1 {
		t.Fatalf("metadata overrode runtime SSH mapping: %#v", instance)
	}
	if instance.Username != "root" || instance.Password != "TestPass123" {
		t.Fatalf("credentials were not retained: %#v", instance)
	}
}

func TestMetadataDoesNotOverrideRuntimeSSHPortWithoutMapping(t *testing.T) {
	instance := providerCore.DiscoveredInstance{SSHPort: 22009}
	applyDiscoveredImportMetadata(&instance, discoveredImportMetadata{
		Username: "root", Password: "TestPass123", SSHPort: 22001,
		PortMappings: []providerCore.DiscoveredPortMapping{discoveredMetadataMapping(22001, 22, "tcp")},
	})
	if instance.SSHPort != 22009 || len(instance.PortMappings) != 0 {
		t.Fatalf("metadata overrode runtime SSH port: %#v", instance)
	}
	if instance.Username != "root" || instance.Password != "TestPass123" {
		t.Fatalf("credentials were not retained: %#v", instance)
	}
}

func TestProxmoxRawDescriptionMetadataIsAppliedAndRedacted(t *testing.T) {
	instance := providerCore.DiscoveredInstance{
		RawData: map[string]interface{}{
			"description": "# 用户名-username admin\n# 密码-password TestPass123\n# SSH端口 22001\n",
		},
	}
	metadata := parseProxmoxCommentMetadata(discoveredUserDescription(instance.RawData))
	applyDiscoveredImportMetadata(&instance, metadata)
	if instance.Username != "admin" || instance.Password != "TestPass123" || instance.SSHPort != 22001 {
		t.Fatalf("Proxmox description metadata was not applied: %#v", instance)
	}

	sanitized := sanitizeDiscoveredRawData(instance.RawData)
	data, err := json.Marshal(sanitized)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "TestPass123") || !strings.Contains(string(data), "[REDACTED]") {
		t.Fatalf("Proxmox description was not redacted: %s", data)
	}
}

func TestShellMetadataFormats(t *testing.T) {
	tests := []struct {
		name  string
		parse func(string, string) (discoveredImportMetadata, bool)
		line  string
		want  string
	}{
		{
			name:  "containerd podman docker record",
			parse: parseShellContainerMetadata,
			line:  "ct1 25001 Pass123 1 512 35001 35003 10",
			want:  "root",
		},
		{
			name:  "qemu record",
			parse: parseQEMURecordMetadata,
			line:  "vm1 25001 Pass123 1 512 10 35001 35003 debian12 192.0.2.10 mac=00:11",
			want:  "root",
		},
		{
			name:  "kubevirt record",
			parse: parseKubeVirtRecordMetadata,
			line:  "vm1 root@192.0.2.10:25001 密码: Pass123 端口范围: 35001-35003 系统: debian12 CPU: 1核",
			want:  "root",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metadata, ok := tt.parse(tt.line, "ct1")
			if strings.HasPrefix(tt.name, "qemu") || strings.HasPrefix(tt.name, "kubevirt") {
				metadata, ok = tt.parse(tt.line, "vm1")
			}
			if !ok || metadata.Username != tt.want || metadata.Password != "Pass123" || metadata.SSHPort != 25001 {
				t.Fatalf("metadata = %#v, ok=%v", metadata, ok)
			}
			if len(metadata.PortMappings) != 4 {
				t.Fatalf("mappings = %#v, want SSH plus range", metadata.PortMappings)
			}
		})
	}
}

func TestDescriptionMetadataRequiresExactInstanceName(t *testing.T) {
	if _, ok := parseDescriptionMetadata("ct1 22001 Pass123 30001 30002", "ct2"); ok {
		t.Fatal("metadata for a different instance name was accepted")
	}
	metadata, ok := parseDescriptionMetadata("ct1 22001 Pass123 30001 30002", "ct1")
	if !ok || metadata.Username != "root" || metadata.SSHPort != 22001 || len(metadata.PortMappings) != 3 {
		t.Fatalf("description metadata = %#v, ok=%v", metadata, ok)
	}
}

func TestMetadataRangeAndCredentialsAreBounded(t *testing.T) {
	if mappings := discoveredMetadataRangeMappings(30000, 30129); len(mappings) != 0 {
		t.Fatalf("oversized range created %d mappings", len(mappings))
	}
	if got := normalizeDiscoveredMetadataPassword(strings.Repeat("x", maxDiscoveredMetadataPasswordLength+1)); got != "" {
		t.Fatalf("oversized password was accepted: %q", got)
	}
	if got := parseDiscoveredMetadataPort("22001\n22002"); got != 0 {
		t.Fatalf("multiline port was accepted: %d", got)
	}
}

func TestDiscoveredCredentialsAndRawDescriptionAreNotSerialized(t *testing.T) {
	instance := providerCore.DiscoveredInstance{Username: "root", Password: "TestPass123"}
	data, err := json.Marshal(instance)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "TestPass123") {
		t.Fatalf("discovery JSON leaked password: %s", data)
	}

	sanitized := sanitizeDiscoveredRawData(map[string]interface{}{
		"config": map[string]string{"user.description": "ct1 22001 TestPass123"},
	})
	data, err = json.Marshal(sanitized)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "TestPass123") || !strings.Contains(string(data), "[REDACTED]") {
		t.Fatalf("raw description was not redacted: %s", data)
	}
}

func TestMetadataLogLookupCommandQuotesInstanceNames(t *testing.T) {
	command := buildMetadataLogLookupCommand([]providerCore.DiscoveredInstance{{Name: "ct'; touch /tmp/pwned; echo '"}}, []string{"/root/ctlog"})
	if !strings.Contains(command, "'ct'\"'\"'; touch /tmp/pwned; echo '\"'\"''") {
		t.Fatalf("instance name was not safely quoted: %s", command)
	}
	if strings.Contains(command, "Pass123") {
		t.Fatalf("lookup command unexpectedly contains a credential: %s", command)
	}
}

func TestMetadataLogLookupCommandIsValidShell(t *testing.T) {
	command := buildMetadataLogLookupCommand([]providerCore.DiscoveredInstance{{Name: "ct1"}}, []string{"/root/ctlog", "$HOME/ctlog"})
	check := exec.Command("bash", "-n")
	check.Stdin = strings.NewReader(command)
	if output, err := check.CombinedOutput(); err != nil {
		t.Fatalf("generated metadata lookup command is invalid shell: %v: %s\n%s", err, command, output)
	}
}

func TestProxmoxMetadataReadCommandIsValidShell(t *testing.T) {
	command := buildProxmoxMetadataReadCommand([]providerCore.DiscoveredInstance{
		{ProviderInstanceID: "101", InstanceType: "vm"},
		{ProviderInstanceID: "102", InstanceType: "lxc"},
	})
	if !strings.Contains(command, "/etc/pve/lxc/102.conf") {
		t.Fatalf("LXC metadata path missing from command: %s", command)
	}
	check := exec.Command("bash", "-n")
	check.Stdin = strings.NewReader(command)
	if output, err := check.CombinedOutput(); err != nil {
		t.Fatalf("generated Proxmox metadata command is invalid shell: %v: %s\n%s", err, command, output)
	}
}

func TestKubeVirtMetadataReadCommandUsesOneListRequest(t *testing.T) {
	command := buildKubeVirtMetadataReadCommand()
	if !strings.Contains(command, "kubectl get vm -n 'kubevirt-vms' -o json") {
		t.Fatalf("KubeVirt metadata command is not a namespace list request: %s", command)
	}
	if strings.Contains(command, "vm1") || strings.Contains(command, "vm2") {
		t.Fatalf("KubeVirt metadata command unexpectedly embeds instance names: %s", command)
	}
	check := exec.Command("bash", "-n")
	check.Stdin = strings.NewReader(command)
	if output, err := check.CombinedOutput(); err != nil {
		t.Fatalf("generated KubeVirt metadata lookup command is invalid shell: %v: %s\n%s", err, command, output)
	}
}
