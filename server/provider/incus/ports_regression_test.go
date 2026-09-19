package incus

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	"oneclickvirt/utils"
)

func TestIncusProxyRemovalWithoutIPKeepsOtherFamily(t *testing.T) {
	for _, ipv6 := range []bool{false, true} {
		executor := &recordingIncusPortsExecutor{}
		p := &IncusProvider{sshClient: utils.NewSafeShellExecutor(executor)}
		if err := p.RemovePortMappingForFamily("guest", 22000, 22, 22009, 31, 10, "both", "device_proxy", "", ipv6); err != nil {
			t.Fatal(err)
		}
		if len(executor.commands) == 0 {
			t.Fatal("proxy cleanup did not run")
		}
		for _, command := range executor.commands {
			v6 := strings.Contains(command, "proxy-v6-") || strings.Contains(command, "'v6-")
			if v6 != ipv6 {
				t.Fatalf("cleanup crossed address families: %s", command)
			}
			if !strings.Contains(command, "'guest'") {
				t.Fatalf("cleanup omitted instance identity: %s", command)
			}
		}
	}
}

func TestIncusProxyRemovalPropagatesUnexpectedFailure(t *testing.T) {
	executor := &failingIncusPortsExecutor{failOn: "incus config device remove"}
	p := &IncusProvider{sshClient: utils.NewSafeShellExecutor(executor)}
	if err := p.RemovePortMappingForFamily("guest", 22000, 22, 22000, 22, 1, "tcp", "device_proxy", "", false); err == nil {
		t.Fatal("proxy cleanup failure was swallowed")
	}
}

func TestIncusAPIPortDevicesKeepWildcardConnectAndSeparateAddressFamilies(t *testing.T) {
	devices := map[string]interface{}{"proxy-tcp-22000": map[string]interface{}{
		"type": "proxy", "nat": "true", "listen": "tcp:198.51.100.10:22000", "connect": "tcp:0.0.0.0:22",
	}}
	ports := []providerModel.Port{
		{HostPort: 22000, GuestPort: 22, Protocol: "tcp"},
		{HostPort: 22000, GuestPort: 22, Protocol: "tcp", IPv6Enabled: true},
	}
	if err := buildAPIPortMappingDevicesWithTargets(ports, "device_proxy", "192.0.2.10", "2001:db8::10", "198.51.100.10", "2001:db8::1", devices); err != nil {
		t.Fatal(err)
	}
	if len(devices) != 2 || devices["proxy-tcp-22000"].(map[string]interface{})["connect"] != "tcp:0.0.0.0:22" {
		t.Fatalf("existing wildcard or dual-stack device names were lost: %#v", devices)
	}
	v6, ok := devices["proxy-v6-tcp-22000"].(map[string]string)
	if !ok || v6["connect"] != "tcp:[2001:db8::10]:22" {
		t.Fatalf("IPv6 mapping collided with IPv4: %#v", devices)
	}
}

type failingIncusPortsExecutor struct {
	commands []string
	failOn   string
}

func (e *failingIncusPortsExecutor) Execute(command string) (string, error) {
	e.commands = append(e.commands, command)
	if e.failOn != "" && strings.Contains(command, e.failOn) {
		return "remote command failed", errors.New("remote command failed")
	}
	if strings.Contains(command, "incus query ") {
		return incusBoundNICFixture, nil
	}
	return "", nil
}
func (e *failingIncusPortsExecutor) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}
func (e *failingIncusPortsExecutor) ExecuteWithLogging(command, _ string) (string, error) {
	return e.Execute(command)
}
func (e *failingIncusPortsExecutor) ExecuteRaw(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}
func (*failingIncusPortsExecutor) ExecuteViaTempScript(string, []string, time.Duration) (string, error) {
	return "", nil
}
func (*failingIncusPortsExecutor) UploadContent(string, string, os.FileMode) error { return nil }
func (*failingIncusPortsExecutor) IsHealthy() bool                                 { return true }
func (*failingIncusPortsExecutor) Reconnect() error                                { return nil }
func (*failingIncusPortsExecutor) Close() error                                    { return nil }

type recordingIncusPortsExecutor struct{ commands []string }

const incusBoundNICFixture = `{"metadata":{"devices":{"eth0":{"type":"nic","network":"incusbr0","name":"eth0","ipv4.address":"192.0.2.10","ipv6.address":"2001:db8::10"}}}}`

func (e *recordingIncusPortsExecutor) Execute(command string) (string, error) {
	e.commands = append(e.commands, command)
	// Proxy setup now pins the DHCP address before creating the NAT device.
	// Keep this fake representative of an Incus daemon: query responses expose
	// both families as already-bound addresses, so the proxy assertions remain
	// focused while the binding transition itself is covered by utils tests.
	if strings.Contains(command, "incus query ") {
		return incusBoundNICFixture, nil
	}
	return "", nil
}
func (e *recordingIncusPortsExecutor) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}
func (e *recordingIncusPortsExecutor) ExecuteWithLogging(command, _ string) (string, error) {
	return e.Execute(command)
}
func (e *recordingIncusPortsExecutor) ExecuteRaw(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}
func (*recordingIncusPortsExecutor) ExecuteViaTempScript(string, []string, time.Duration) (string, error) {
	return "", nil
}
func (*recordingIncusPortsExecutor) UploadContent(string, string, os.FileMode) error { return nil }
func (*recordingIncusPortsExecutor) IsHealthy() bool                                 { return true }
func (*recordingIncusPortsExecutor) Reconnect() error                                { return nil }
func (*recordingIncusPortsExecutor) Close() error                                    { return nil }

func TestIncusProxyEndpointBracketsIPv6(t *testing.T) {
	if got := proxyEndpoint("tcp", "2001:db8::10", 22000); got != "tcp:[2001:db8::10]:22000" {
		t.Fatalf("proxyEndpoint() = %q", got)
	}
	if got := proxyEndpointRange("udp", "[2001:db8::10]", 22000, 22009); got != "udp:[2001:db8::10]:22000-22009" {
		t.Fatalf("proxyEndpointRange() = %q", got)
	}
}

func TestIncusDeviceProxyUsesIPv6ListenerForIPv6Guest(t *testing.T) {
	executor := &recordingIncusPortsExecutor{}
	p := &IncusProvider{config: provider.NodeConfig{PortIP: "198.51.100.10", Host: "2001:db8::1"}, sshClient: utils.NewSafeShellExecutor(executor)}
	if err := p.setupDeviceProxyMappingWithIP("guest", 22000, 22, "tcp", "2001:db8::10"); err != nil {
		t.Fatalf("setupDeviceProxyMappingWithIP() error = %v", err)
	}
	command := lastCommandContaining(executor.commands, "config device add")
	if command == "" {
		t.Fatalf("commands = %#v, want one device command", executor.commands)
	}
	if !strings.Contains(command, "proxy-v6-tcp-22000") || !strings.Contains(command, "listen='tcp:[2001:db8::1]:22000'") || !strings.Contains(command, "connect='tcp:[2001:db8::10]:22'") {
		t.Fatalf("IPv6 proxy command = %s", command)
	}
}

func TestIncusDeviceProxyUsesGuestIPv4AsConnectTarget(t *testing.T) {
	executor := &recordingIncusPortsExecutor{}
	p := &IncusProvider{config: provider.NodeConfig{PortIP: "198.51.100.10", Host: "2001:db8::1"}, sshClient: utils.NewSafeShellExecutor(executor)}
	if err := p.setupDeviceProxyMappingWithIP("guest", 22000, 22, "tcp", "192.0.2.10"); err != nil {
		t.Fatalf("setupDeviceProxyMappingWithIP() error = %v", err)
	}
	if command := lastCommandContaining(executor.commands, "config device add"); !strings.Contains(command, "connect='tcp:192.0.2.10:22'") {
		t.Fatalf("IPv4 proxy command = %#v, want the guest IPv4 as connect target", executor.commands)
	}
}

func lastCommandContaining(commands []string, fragment string) string {
	for index := len(commands) - 1; index >= 0; index-- {
		if strings.Contains(commands[index], fragment) {
			return commands[index]
		}
	}
	return ""
}

func TestIncusDeviceProxyRangeUsesGuestIPv4AsConnectTarget(t *testing.T) {
	executor := &recordingIncusPortsExecutor{}
	p := &IncusProvider{config: provider.NodeConfig{PortIP: "198.51.100.10", Host: "2001:db8::1"}, sshClient: utils.NewSafeShellExecutor(executor)}
	if err := p.setupPortRangeMapping("guest", 22000, 22009, 22000, 22009, "tcp", "192.0.2.10"); err != nil {
		t.Fatalf("setupPortRangeMapping() error = %v", err)
	}
	joined := strings.Join(executor.commands, "\n")
	if !strings.Contains(joined, "connect='tcp:192.0.2.10:22000-22009'") {
		t.Fatalf("IPv4 range proxy commands = %s, want the guest IPv4 as connect target", joined)
	}
}

func TestIncusDeviceProxyRangePreservesGuestPortOffset(t *testing.T) {
	for _, address := range []string{"192.0.2.10", "2001:db8::10"} {
		t.Run(address, func(t *testing.T) {
			executor := &recordingIncusPortsExecutor{}
			listenIP := "198.51.100.10"
			if strings.Contains(address, ":") {
				listenIP = "2001:db8::1"
			}
			// An Agent executor can have PortIP without an SSH Host.
			p := &IncusProvider{config: provider.NodeConfig{PortIP: listenIP}, sshClient: utils.NewSafeShellExecutor(executor)}
			ports := []providerModel.Port{{HostPort: 22000, GuestPort: 8000, Protocol: "both"}, {HostPort: 22001, GuestPort: 8001, Protocol: "both"}}
			if err := p.setupPortRangeMappingWithIP("guest", ports, "device_proxy", address); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(executor.commands[0], "incus query ") {
				t.Fatalf("missing NIC binding before proxy: %v", executor.commands)
			}
			for _, protocol := range []string{"tcp", "udp"} {
				want := "connect='" + proxyEndpointRange(protocol, address, 8000, 8001) + "'"
				if !strings.Contains(strings.Join(executor.commands, "\n"), want) {
					t.Fatalf("guest port offset lost: want %s in %v", want, executor.commands)
				}
			}
		})
	}
}

func TestIncusProxyBindingFailureDoesNotAddDevice(t *testing.T) {
	executor := &failingIncusPortsExecutor{failOn: "incus query "}
	p := &IncusProvider{config: provider.NodeConfig{PortIP: "198.51.100.10"}, sshClient: utils.NewSafeShellExecutor(executor)}
	if err := p.setupDeviceProxyMappingWithIP("guest", 22000, 22, "tcp", "192.0.2.10"); err == nil {
		t.Fatal("binding failure was ignored")
	}
	if lastCommandContaining(executor.commands, "config device add") != "" {
		t.Fatalf("device added despite failed binding: %v", executor.commands)
	}
}

func TestIncusGetInstanceIPv6UsesSavedAllocationWhileStopped(t *testing.T) {
	// The recording executor returns an empty output by default. Use a small
	// purpose-built executor so the first (saved allocation) command succeeds.
	addressExecutor := &incusAddressFileExecutor{output: "2001:db8::10\n"}
	p := &IncusProvider{sshClient: utils.NewSafeShellExecutor(addressExecutor)}
	got, err := p.GetInstanceIPv6(context.Background(), "guest")
	if err != nil || got != "2001:db8::10" {
		t.Fatalf("GetInstanceIPv6() = (%q, %v)", got, err)
	}
	if len(addressExecutor.commands) != 1 || !strings.Contains(addressExecutor.commands[0], "guest_v6") {
		t.Fatalf("commands = %#v, want saved allocation lookup only", addressExecutor.commands)
	}
}

func TestIncusPersistsManagedNATIPv6BeforeStoppedPortMapping(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s_%d?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"), time.Now().UnixNano())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&providerModel.Instance{}); err != nil {
		t.Fatal(err)
	}
	instance := providerModel.Instance{Name: "guest", ProviderID: 7}
	if err := db.Create(&instance).Error; err != nil {
		t.Fatal(err)
	}
	previousDB := global.APP_DB
	global.APP_DB = db
	t.Cleanup(func() { global.APP_DB = previousDB })

	p := &IncusProvider{config: provider.NodeConfig{ID: 7}}
	if err := p.persistManagedNATIPv6Target("guest", "fd42:1234::10"); err != nil {
		t.Fatal(err)
	}
	var stored providerModel.Instance
	if err := db.First(&stored, instance.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.IPv6Address != "fd42:1234::10" {
		t.Fatalf("IPv6Address = %q", stored.IPv6Address)
	}
	if err := p.persistManagedNATIPv6Target("other", "fd42:1234::11"); err == nil {
		t.Fatal("missing owned instance was silently accepted")
	}
}

type incusAddressFileExecutor struct {
	output   string
	commands []string
}

func (e *incusAddressFileExecutor) Execute(command string) (string, error) {
	e.commands = append(e.commands, command)
	return e.output, nil
}
func (e *incusAddressFileExecutor) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}
func (e *incusAddressFileExecutor) ExecuteWithLogging(command, _ string) (string, error) {
	return e.Execute(command)
}
func (e *incusAddressFileExecutor) ExecuteRaw(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}
func (*incusAddressFileExecutor) ExecuteViaTempScript(string, []string, time.Duration) (string, error) {
	return "", nil
}
func (*incusAddressFileExecutor) UploadContent(string, string, os.FileMode) error { return nil }
func (*incusAddressFileExecutor) IsHealthy() bool                                 { return true }
func (*incusAddressFileExecutor) Reconnect() error                                { return nil }
func (*incusAddressFileExecutor) Close() error                                    { return nil }

func TestIncusPortRangeRequiresOneToOneGuestPorts(t *testing.T) {
	p := &IncusProvider{}
	ranges := p.findPortRanges([]providerModel.Port{
		{HostPort: 20000, GuestPort: 8000, Protocol: "tcp"},
		{HostPort: 20001, GuestPort: 9000, Protocol: "tcp"},
	})
	if len(ranges) != 2 {
		t.Fatalf("findPortRanges() returned %d ranges, want 2", len(ranges))
	}
}

func TestIncusPortRangeMappingPropagatesDeviceFailure(t *testing.T) {
	executor := &failingIncusPortsExecutor{failOn: "incus config device add"}
	p := &IncusProvider{
		config:    provider.NodeConfig{PortIP: "198.51.100.10", Host: "2001:db8::1"},
		sshClient: utils.NewSafeShellExecutor(executor),
	}
	err := p.setupPortRangeMappingWithIP("guest", []providerModel.Port{
		{HostPort: 22000, GuestPort: 22000, Protocol: "tcp"},
		{HostPort: 22001, GuestPort: 22001, Protocol: "tcp"},
	}, "device_proxy", "192.0.2.10")
	if err == nil || !strings.Contains(err.Error(), "设置端口范围映射失败") {
		t.Fatalf("setupPortRangeMappingWithIP() error = %v, want propagated device failure", err)
	}
}

func TestIncusControllerPortMappingsAreNotInstalledOnNode(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s_%d?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"), time.Now().UnixNano())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	for _, schema := range []string{
		`CREATE TABLE providers (id INTEGER PRIMARY KEY, ipv4_port_mapping_method TEXT)`,
		`CREATE TABLE instances (id INTEGER PRIMARY KEY, name TEXT, provider_id INTEGER, private_ip TEXT, deleted_at DATETIME)`,
		`CREATE TABLE ports (id INTEGER PRIMARY KEY, instance_id INTEGER, host_port INTEGER, guest_port INTEGER, protocol TEXT, status TEXT, mapping_type TEXT, mapping_method TEXT, is_ssh BOOLEAN, deleted_at DATETIME)`,
	} {
		if err := db.Exec(schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	previousDB := global.APP_DB
	global.APP_DB = db
	t.Cleanup(func() { global.APP_DB = previousDB })
	previousLog := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = previousLog })
	if err := db.Exec(`INSERT INTO providers (id, ipv4_port_mapping_method) VALUES (1, 'device_proxy')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO instances (id, name, provider_id, private_ip) VALUES (1, 'guest', 1, '192.0.2.10')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO ports (id, instance_id, host_port, guest_port, protocol, status, mapping_type, mapping_method, is_ssh) VALUES (1, 1, 22000, 22, 'tcp', 'active', 'controller', 'device_proxy', 1)`).Error; err != nil {
		t.Fatal(err)
	}
	executor := &recordingIncusPortsExecutor{}
	p := &IncusProvider{config: provider.NodeConfig{ID: 1, PortIP: "198.51.100.10"}, sshClient: utils.NewSafeShellExecutor(executor)}
	if err := p.configurePortMappingsWithIP(context.Background(), "guest", NetworkConfig{NetworkType: "nat_ipv4", IPv4PortMappingMethod: "device_proxy"}, "192.0.2.10"); err != nil {
		t.Fatalf("configurePortMappingsWithIP() error = %v", err)
	}
	if len(executor.commands) != 0 {
		t.Fatalf("controller mapping unexpectedly installed on node: %#v", executor.commands)
	}
}

func TestIncusSSHPortMappingsExpandAutomaticDualStackRow(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s_%d?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"), time.Now().UnixNano())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	for _, schema := range []string{
		`CREATE TABLE providers (id INTEGER PRIMARY KEY, ipv4_port_mapping_method TEXT, ipv6_port_mapping_method TEXT)`,
		`CREATE TABLE instances (id INTEGER PRIMARY KEY, name TEXT, provider_id INTEGER, private_ip TEXT, ipv6_address TEXT, deleted_at DATETIME)`,
		`CREATE TABLE ports (id INTEGER PRIMARY KEY, instance_id INTEGER, host_port INTEGER, guest_port INTEGER, protocol TEXT, status TEXT, mapping_type TEXT, mapping_method TEXT, is_ssh BOOLEAN, is_automatic BOOLEAN, port_type TEXT, ipv6_enabled BOOLEAN, ipv6_address TEXT, deleted_at DATETIME)`,
	} {
		if err := db.Exec(schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	previousDB := global.APP_DB
	global.APP_DB = db
	t.Cleanup(func() { global.APP_DB = previousDB })
	previousLog := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = previousLog })
	if err := db.Exec(`INSERT INTO providers (id, ipv4_port_mapping_method, ipv6_port_mapping_method) VALUES (1, 'device_proxy', 'device_proxy')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO instances (id, name, provider_id, private_ip, ipv6_address) VALUES (1, 'guest', 1, '192.0.2.10', '2001:db8::10')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO ports (id, instance_id, host_port, guest_port, protocol, status, mapping_type, mapping_method, is_ssh, is_automatic, port_type, ipv6_enabled) VALUES (1, 1, 22000, 22, 'tcp', 'active', 'node', 'device_proxy', 1, 1, 'range_mapped', 1)`).Error; err != nil {
		t.Fatal(err)
	}
	executor := &recordingIncusPortsExecutor{}
	p := &IncusProvider{config: provider.NodeConfig{ID: 1, PortIP: "198.51.100.10", Host: "2001:db8::1"}, sshClient: utils.NewSafeShellExecutor(executor)}
	if err := p.configurePortMappingsWithIP(context.Background(), "guest", NetworkConfig{
		NetworkType: "nat_ipv4_ipv6", IPv4PortMappingMethod: "device_proxy", IPv6PortMappingMethod: "device_proxy",
	}, "192.0.2.10"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(executor.commands, "\n")
	if !strings.Contains(joined, "proxy-tcp-22000") || !strings.Contains(joined, "proxy-v6-tcp-22000") {
		t.Fatalf("dual-stack automatic row did not install both families: %s", joined)
	}

	initialExecutor := &recordingIncusPortsExecutor{}
	initial := &IncusProvider{config: p.config, sshClient: utils.NewSafeShellExecutor(initialExecutor)}
	if err := initial.configureInitialPortMappingsWithIP(context.Background(), "guest", NetworkConfig{
		NetworkType: "nat_ipv4_ipv6", IPv4PortMappingMethod: "device_proxy", IPv6PortMappingMethod: "device_proxy",
	}, "192.0.2.10"); err != nil {
		t.Fatal(err)
	}
	initialCommands := strings.Join(initialExecutor.commands, "\n")
	if !strings.Contains(initialCommands, "proxy-tcp-22000") || strings.Contains(initialCommands, "proxy-v6-tcp-22000") {
		t.Fatalf("initial phase crossed address families: %s", initialCommands)
	}

	ipv6Executor := &recordingIncusPortsExecutor{}
	ipv6Phase := &IncusProvider{config: p.config, sshClient: utils.NewSafeShellExecutor(ipv6Executor)}
	if err := ipv6Phase.configurePortMappingFamiliesWithIP(context.Background(), "guest", NetworkConfig{
		NetworkType: "ipv6_only", IPv4PortMappingMethod: "device_proxy", IPv6PortMappingMethod: "device_proxy",
	}, "", false, true); err != nil {
		t.Fatal(err)
	}
	ipv6Commands := strings.Join(ipv6Executor.commands, "\n")
	if strings.Contains(ipv6Commands, "proxy-tcp-22000") || !strings.Contains(ipv6Commands, "proxy-v6-tcp-22000") {
		t.Fatalf("IPv6 phase crossed address families: %s", ipv6Commands)
	}
}

func TestIncusSecurityNestingIsAppliedAndFailuresAreVisible(t *testing.T) {
	executor := &recordingIncusPortsExecutor{}
	p := &IncusProvider{
		connected: true,
		config:    provider.NodeConfig{ExecutionRule: "ssh_only"},
		sshClient: utils.NewSafeShellExecutor(executor),
	}
	allow := true
	if err := p.configureInstanceSecurity(context.Background(), provider.InstanceConfig{Name: "guest", InstanceType: "container", AllowNesting: &allow}); err != nil {
		t.Fatalf("configureInstanceSecurity() error = %v", err)
	}
	found := false
	for _, command := range executor.commands {
		if strings.Contains(command, "security.nesting") && strings.Contains(command, "true") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("security.nesting was not written: %#v", executor.commands)
	}

	failing := &failingIncusPortsExecutor{failOn: "security.nesting"}
	p.sshClient = utils.NewSafeShellExecutor(failing)
	if err := p.configureInstanceSecurity(context.Background(), provider.InstanceConfig{Name: "guest", InstanceType: "container", AllowNesting: &allow}); err == nil {
		t.Fatal("configureInstanceSecurity() succeeded after security.nesting write failure")
	}
}

func TestIncusRemoveRangeProxyCleansSingleAndRangeDevices(t *testing.T) {
	executor := &recordingIncusPortsExecutor{}
	p := &IncusProvider{sshClient: utils.NewSafeShellExecutor(executor)}
	if err := p.RemovePortMappingWithDetails("guest", 20000, 8000, 20009, 8009, 10, "both", "device_proxy", "192.0.2.10"); err != nil {
		t.Fatalf("RemovePortMappingWithDetails() error = %v", err)
	}
	joined := strings.Join(executor.commands, "\n")
	for _, fragment := range []string{"proxy-tcp-20000-20009", "proxy-udp-20000-20009"} {
		if !strings.Contains(joined, fragment) {
			t.Fatalf("commands missing %q: %s", fragment, joined)
		}
	}
}

func TestIncusAPIConfiguresPortMappingsWithoutReplacingInstanceConfig(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s_%d?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"), time.Now().UnixNano())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Exec(`CREATE TABLE providers (id INTEGER PRIMARY KEY, name TEXT, type TEXT, ipv4_port_mapping_method TEXT, ipv6_port_mapping_method TEXT, deleted_at DATETIME)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE instances (id INTEGER PRIMARY KEY, name TEXT, provider_id INTEGER, provider_vm_id TEXT, private_ip TEXT, deleted_at DATETIME)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE ports (id INTEGER PRIMARY KEY, instance_id INTEGER, provider_id INTEGER, host_port INTEGER, host_port_end INTEGER, guest_port INTEGER, guest_port_end INTEGER, port_count INTEGER, protocol TEXT, status TEXT, mapping_type TEXT, mapping_method TEXT, deleted_at DATETIME)`).Error; err != nil {
		t.Fatal(err)
	}
	previousDB := global.APP_DB
	global.APP_DB = db
	t.Cleanup(func() { global.APP_DB = previousDB })
	if err := db.Exec(`INSERT INTO providers (id, name, type) VALUES (1, 'incus-test', 'incus')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO instances (id, name, provider_id, private_ip) VALUES (1, 'guest', 1, '192.0.2.10')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO ports (id, instance_id, provider_id, host_port, guest_port, port_count, protocol, status, mapping_type) VALUES (1, 1, 1, 22000, 22, 1, 'tcp', 'active', 'node')`).Error; err != nil {
		t.Fatal(err)
	}
	p := NewIncusProvider().(*IncusProvider)
	p.config = provider.NodeConfig{ID: 1, Host: "127.0.0.1", PortIP: "198.51.100.10"}
	transport := &incusIPv4MetadataTransport{t: t}
	p.apiClient = &http.Client{Transport: transport}
	if err := p.apiConfigurePortMappings(context.Background(), provider.InstanceConfig{Name: "guest"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"GET /1.0/instances/guest/state", "GET /1.0/instances/guest", "PUT /1.0/instances/guest/state", "GET /1.0/instances/guest", "PUT /1.0/instances/guest", "PUT /1.0/instances/guest/state"}
	if !reflect.DeepEqual(transport.requests, want) {
		t.Fatalf("API mutation sequence = %v, want %v", transport.requests, want)
	}
	t.Run("automatic dual-stack database row", func(t *testing.T) {
		for _, column := range []string{"ipv6_enabled BOOLEAN", "is_automatic BOOLEAN", "port_type TEXT"} {
			if err := db.Exec("ALTER TABLE ports ADD COLUMN " + column).Error; err != nil {
				t.Fatal(err)
			}
		}
		if err := db.Exec("UPDATE ports SET ipv6_enabled = 1, is_automatic = 1, port_type = 'range_mapped'").Error; err != nil {
			t.Fatal(err)
		}
		p.config.Host = "2001:db8::1"
		t.Cleanup(func() {
			p.config.Host = "127.0.0.1"
			if err := db.Exec("UPDATE ports SET ipv6_enabled = 0, is_automatic = 0, port_type = ''").Error; err != nil {
				t.Fatal(err)
			}
		})
		tr := &incusIPv4MetadataTransport{t: t, expectIPv6: true}
		p.apiClient = &http.Client{Transport: tr}
		if err := p.apiConfigurePortMappings(context.Background(), provider.InstanceConfig{Name: "guest", Metadata: map[string]string{"network_type": "nat_ipv4_ipv6"}}); err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("UPDATE providers SET ipv6_port_mapping_method = 'native'").Error; err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := db.Exec("UPDATE providers SET ipv6_port_mapping_method = ''").Error; err != nil {
				t.Fatal(err)
			}
		})
		// Native IPv6 does not need a NAT device or an IPv6 DHCP lease.
		tr = &incusIPv4MetadataTransport{t: t}
		p.apiClient = &http.Client{Transport: tr}
		if err := p.apiConfigurePortMappings(context.Background(), provider.InstanceConfig{Name: "guest", Metadata: map[string]string{"network_type": "nat_ipv4_ipv6"}}); err != nil {
			t.Fatal(err)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failing := &incusIPv4MetadataTransport{t: t, cancelOnUpdate: cancel}
	p.apiClient = &http.Client{Transport: failing}
	if err := p.apiConfigurePortMappings(ctx, provider.InstanceConfig{Name: "guest"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled update error = %v", err)
	}
	if !failing.recovered {
		t.Fatal("canceled device update left the instance stopped")
	}
	for _, scenario := range []string{"cancel after stop", "refresh fails", "device conflict after stop", "profile conflict after stop", "ETag conflict", "cancel start"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			tr := &incusIPv4MetadataTransport{t: t}
			switch scenario {
			case "cancel after stop":
				tr.cancelAfterStop = cancel
			case "refresh fails":
				tr.failRefresh = true
			case "device conflict after stop":
				tr.conflictAfterStop = true
			case "profile conflict after stop":
				tr.conflictAfterStop = true
				tr.profileConflict = true
			case "ETag conflict":
				tr.conflictOnUpdate = true
			case "cancel start":
				tr.cancelOnStart = cancel
			}
			p.apiClient = &http.Client{Transport: tr}
			if err := p.apiConfigurePortMappings(ctx, provider.InstanceConfig{Name: "guest"}); err == nil {
				t.Fatal("device configuration failure was swallowed")
			}
			if !tr.recovered || tr.stopped {
				t.Fatalf("failed mapping left the instance stopped: requests=%v", tr.requests)
			}
			if strings.Contains(scenario, "conflict after stop") {
				for _, request := range tr.requests {
					if request == "PUT /1.0/instances/guest" {
						t.Fatal("concurrent conflicting proxy was overwritten")
					}
				}
			}
		})
	}
}

func TestIncusAPIPortDevicesSupportBothAndRanges(t *testing.T) {
	devices := map[string]interface{}{}
	ports := []providerModel.Port{{
		HostPort: 22000, HostPortEnd: 22009, GuestPort: 22, GuestPortEnd: 31,
		PortCount: 10, Protocol: "both", MappingType: "node",
	}}
	if err := buildAPIPortMappingDevices(ports, "device_proxy", "192.0.2.10", "198.51.100.10", devices); err != nil {
		t.Fatalf("buildAPIPortMappingDevices() error = %v", err)
	}
	for _, name := range []string{"proxy-tcp-22000-22009", "proxy-udp-22000-22009"} {
		if _, ok := devices[name]; !ok {
			t.Fatalf("devices = %#v, missing %s", devices, name)
		}
	}
}

func TestIncusAPIPortDevicesRejectInvalidRangeAndConflict(t *testing.T) {
	if err := buildAPIPortMappingDevices([]providerModel.Port{{HostPort: 22000, GuestPort: 22, HostPortEnd: 22002, GuestPortEnd: 23, Protocol: "tcp"}}, "device_proxy", "192.0.2.10", "198.51.100.10", map[string]interface{}{}); err == nil {
		t.Fatal("invalid non-one-to-one range was accepted")
	}
	devices := map[string]interface{}{"proxy-tcp-22000": map[string]interface{}{
		"type": "proxy", "listen": "tcp:0.0.0.0:22000", "connect": "tcp:192.0.2.99:22", "nat": "true",
	}}
	if err := buildAPIPortMappingDevices([]providerModel.Port{{HostPort: 22000, GuestPort: 22, Protocol: "tcp"}}, "device_proxy", "192.0.2.10", "198.51.100.10", devices); err == nil {
		t.Fatal("conflicting existing proxy device was accepted")
	}
}

func TestIncusAPIPortDevicesSupportIPv6Targets(t *testing.T) {
	devices := map[string]interface{}{}
	ports := []providerModel.Port{{HostPort: 22443, GuestPort: 443, Protocol: "tcp", IPv6Enabled: true}}
	if err := buildAPIPortMappingDevicesWithTargets(ports, "device_proxy", "", "2001:db8::10", "", "2001:db8::1", devices); err != nil {
		t.Fatalf("IPv6 device build error = %v", err)
	}
	device, ok := devices["proxy-v6-tcp-22443"].(map[string]string)
	if !ok || device["listen"] != "tcp:[2001:db8::1]:22443" || device["connect"] != "tcp:[2001:db8::10]:443" {
		t.Fatalf("unexpected IPv6 proxy device: %#v", devices)
	}
}
