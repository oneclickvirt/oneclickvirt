package lxd

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

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	"oneclickvirt/utils"
)

func TestLXDProxyRemovalWithoutIPKeepsOtherFamily(t *testing.T) {
	for _, ipv6 := range []bool{false, true} {
		executor := &recordingLXDIPv6Executor{}
		p := &LXDProvider{sshClient: utils.NewSafeShellExecutor(executor)}
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

func TestLXDProxyRemovalPropagatesUnexpectedFailure(t *testing.T) {
	executor := &failingLXDPortsExecutor{failOn: "lxc config device remove"}
	p := &LXDProvider{sshClient: utils.NewSafeShellExecutor(executor)}
	if err := p.RemovePortMappingForFamily("guest", 22000, 22, 22000, 22, 1, "tcp", "device_proxy", "", false); err == nil {
		t.Fatal("proxy cleanup failure was swallowed")
	}
}

func TestLXDAPIPortDevicesKeepWildcardConnectAndSeparateAddressFamilies(t *testing.T) {
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

type failingLXDPortsExecutor struct {
	commands []string
	failOn   string
}

func (e *failingLXDPortsExecutor) Execute(command string) (string, error) {
	e.commands = append(e.commands, command)
	if e.failOn != "" && strings.Contains(command, e.failOn) {
		return "remote command failed", errors.New("remote command failed")
	}
	if strings.Contains(command, "lxc query ") {
		return lxdBoundNICFixture, nil
	}
	return "", nil
}

const lxdBoundNICFixture = `{"metadata":{"devices":{"eth0":{"type":"nic","network":"lxdbr0","name":"eth0","ipv4.address":"192.0.2.10","ipv6.address":"2001:db8::10"}}}}`

func (e *failingLXDPortsExecutor) ExecuteWithTimeout(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}
func (e *failingLXDPortsExecutor) ExecuteWithLogging(command, _ string) (string, error) {
	return e.Execute(command)
}
func (e *failingLXDPortsExecutor) ExecuteRaw(command string, _ time.Duration) (string, error) {
	return e.Execute(command)
}
func (*failingLXDPortsExecutor) ExecuteViaTempScript(string, []string, time.Duration) (string, error) {
	return "", nil
}
func (*failingLXDPortsExecutor) UploadContent(string, string, os.FileMode) error { return nil }
func (*failingLXDPortsExecutor) IsHealthy() bool                                 { return true }
func (*failingLXDPortsExecutor) Reconnect() error                                { return nil }
func (*failingLXDPortsExecutor) Close() error                                    { return nil }

func TestLXDProxyEndpointBracketsIPv6(t *testing.T) {
	if got := lxdProxyEndpoint("tcp", "2001:db8::10", 22000); got != "tcp:[2001:db8::10]:22000" {
		t.Fatalf("lxdProxyEndpoint() = %q", got)
	}
	if got := lxdProxyEndpointRange("udp", "[2001:db8::10]", 22000, 22009); got != "udp:[2001:db8::10]:22000-22009" {
		t.Fatalf("lxdProxyEndpointRange() = %q", got)
	}
}

func TestLXDPersistsManagedNATIPv6BeforeStoppedPortMapping(t *testing.T) {
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

	p := &LXDProvider{config: provider.NodeConfig{ID: 7}}
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

func TestLXDDeviceProxyUsesIPv6ListenerForIPv6Guest(t *testing.T) {
	executor := &recordingLXDIPv6Executor{}
	p := &LXDProvider{config: provider.NodeConfig{PortIP: "198.51.100.10", Host: "2001:db8::1"}, sshClient: utils.NewSafeShellExecutor(executor)}
	if err := p.setupDeviceProxyMappingWithIP("guest", 22000, 22, "tcp", "2001:db8::10"); err != nil {
		t.Fatalf("setupDeviceProxyMappingWithIP() error = %v", err)
	}
	command := lastLXDCommandContaining(executor.commands, "config device add")
	if command == "" {
		t.Fatalf("commands = %#v, want one device command", executor.commands)
	}
	if !strings.Contains(command, "proxy-v6-tcp-22000") || !strings.Contains(command, "listen='tcp:[2001:db8::1]:22000'") || !strings.Contains(command, "connect='tcp:[2001:db8::10]:22'") {
		t.Fatalf("IPv6 proxy command = %s", command)
	}
}

func lastLXDCommandContaining(commands []string, fragment string) string {
	for index := len(commands) - 1; index >= 0; index-- {
		if strings.Contains(commands[index], fragment) {
			return commands[index]
		}
	}
	return ""
}

func TestLXDProxyBindsNICWithoutSSHHost(t *testing.T) {
	for _, address := range []string{"192.0.2.10", "2001:db8::10"} {
		for _, mapping := range []string{"single", "range", "nat-range"} {
			t.Run(address+"/"+mapping, func(t *testing.T) {
				executor := &recordingLXDIPv6Executor{}
				listenIP := "198.51.100.10"
				if strings.Contains(address, ":") {
					listenIP = "2001:db8::1"
				}
				p := &LXDProvider{config: provider.NodeConfig{PortIP: listenIP}, sshClient: utils.NewSafeShellExecutor(executor)}
				var err error
				switch mapping {
				case "single":
					err = p.setupDeviceProxyMappingWithIP("guest", 22000, 22, "both", address)
				case "range":
					err = p.setupDeviceProxyRangeMapping("guest", 22000, 22009, "both", address)
				case "nat-range":
					err = p.setupNATPortRangeDeviceProxyWithIP("guest", 22000, 22009, address)
				}
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(executor.commands[0], "lxc query ") {
					t.Fatalf("missing NIC binding before proxy: %v", executor.commands)
				}
				if mapping == "range" && strings.Contains(address, ":") && !strings.Contains(strings.Join(executor.commands, "\n"), "v6-tcp-range-") {
					t.Fatalf("IPv6 mapping lost its family prefix: %v", executor.commands)
				}
			})
		}
	}
}

func TestLXDProxyBindingFailureDoesNotAddDevice(t *testing.T) {
	executor := &failingLXDPortsExecutor{failOn: "lxc query "}
	p := &LXDProvider{config: provider.NodeConfig{PortIP: "198.51.100.10"}, sshClient: utils.NewSafeShellExecutor(executor)}
	if err := p.setupDeviceProxyMappingWithIP("guest", 22000, 22, "tcp", "192.0.2.10"); err == nil {
		t.Fatal("binding failure was ignored")
	}
	if lastLXDCommandContaining(executor.commands, "config device add") != "" {
		t.Fatalf("device added despite failed binding: %v", executor.commands)
	}
}

func TestLXDBothRangeFailureRollsBackTCP(t *testing.T) {
	executor := &failingLXDPortsExecutor{failOn: "connect='udp:"}
	p := &LXDProvider{config: provider.NodeConfig{PortIP: "198.51.100.10"}, sshClient: utils.NewSafeShellExecutor(executor)}
	ports := []providerModel.Port{{HostPort: 22000, GuestPort: 22000, Protocol: "both"}, {HostPort: 22001, GuestPort: 22001, Protocol: "both"}}
	if err := p.setupPortRangeMappingWithIP("guest", ports, "device_proxy", "192.0.2.10"); err == nil {
		t.Fatal("UDP failure was ignored")
	}
	if got := lastLXDCommandContaining(executor.commands, "config device remove"); !strings.Contains(got, "'tcp-range-22000-22001'") {
		t.Fatalf("TCP device was left after UDP failure: %v", executor.commands)
	}
}

func TestLXDNATRangeUsesGuestIPv4AsConnectTarget(t *testing.T) {
	executor := &recordingLXDIPv6Executor{}
	p := &LXDProvider{config: provider.NodeConfig{PortIP: "198.51.100.10", Host: "2001:db8::1"}, sshClient: utils.NewSafeShellExecutor(executor)}
	if err := p.setupNATPortRangeDeviceProxyWithIP("guest", 22000, 22009, "192.0.2.10"); err != nil {
		t.Fatalf("setupNATPortRangeDeviceProxyWithIP() error = %v", err)
	}
	joined := strings.Join(executor.commands, "\n")
	if !strings.Contains(joined, "connect='tcp:192.0.2.10:22000-22009'") || !strings.Contains(joined, "connect='udp:192.0.2.10:22000-22009'") {
		t.Fatalf("NAT proxy commands = %s, want the guest IPv4 as connect target", joined)
	}
	if strings.Contains(joined, "connect='tcp:0.0.0.0") || strings.Contains(joined, "connect='udp:0.0.0.0") {
		t.Fatalf("NAT proxy still uses unspecified connect target: %s", joined)
	}
}

func TestLXDNATRangeUsesBracketedGuestIPv6AsConnectTarget(t *testing.T) {
	executor := &recordingLXDIPv6Executor{}
	p := &LXDProvider{config: provider.NodeConfig{PortIP: "198.51.100.10", Host: "2001:db8::1"}, sshClient: utils.NewSafeShellExecutor(executor)}
	if err := p.setupNATPortRangeDeviceProxyWithIP("guest", 22000, 22009, "2001:db8::10"); err != nil {
		t.Fatalf("setupNATPortRangeDeviceProxyWithIP() error = %v", err)
	}
	joined := strings.Join(executor.commands, "\n")
	if !strings.Contains(joined, "listen='tcp:[2001:db8::1]:22000-22009'") || !strings.Contains(joined, "connect='tcp:[2001:db8::10]:22000-22009'") {
		t.Fatalf("IPv6 NAT proxy commands = %s", joined)
	}
}

func TestLXDNATRangeRejectsInvalidGuestIP(t *testing.T) {
	executor := &recordingLXDIPv6Executor{}
	p := &LXDProvider{config: provider.NodeConfig{PortIP: "198.51.100.10", Host: "2001:db8::1"}, sshClient: utils.NewSafeShellExecutor(executor)}
	if err := p.setupNATPortRangeDeviceProxyWithIP("guest", 22000, 22009, "not-an-ip"); err == nil {
		t.Fatal("setupNATPortRangeDeviceProxyWithIP() accepted an invalid guest IP")
	}
	if len(executor.commands) != 0 {
		t.Fatalf("invalid guest IP caused remote commands: %#v", executor.commands)
	}
}

func TestLXDGetInstanceIPv6UsesSavedAllocationWhileStopped(t *testing.T) {
	executor := &recordingLXDIPv6Executor{outputs: []string{"2001:db8::10\n"}}
	p := &LXDProvider{sshClient: utils.NewSafeShellExecutor(executor)}
	got, err := p.GetInstanceIPv6("guest")
	if err != nil || got != "2001:db8::10" {
		t.Fatalf("GetInstanceIPv6() = (%q, %v)", got, err)
	}
	if len(executor.commands) != 1 || !strings.Contains(executor.commands[0], "guest_v6") {
		t.Fatalf("commands = %#v, want saved allocation lookup only", executor.commands)
	}
}

func TestLXDRemoveRangeProxyCleansRangeDeviceNames(t *testing.T) {
	executor := &recordingLXDIPv6Executor{}
	p := &LXDProvider{sshClient: utils.NewSafeShellExecutor(executor)}
	if err := p.RemovePortMappingWithDetails("guest", 20000, 8000, 20009, 8009, 10, "both", "device_proxy", "192.0.2.10"); err != nil {
		t.Fatalf("RemovePortMappingWithDetails() error = %v", err)
	}
	for _, fragment := range []string{"tcp-range-20000-20009", "udp-range-20000-20009"} {
		found := false
		for _, command := range executor.commands {
			if strings.Contains(command, fragment) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("commands missing %q: %#v", fragment, executor.commands)
		}
	}
}

func TestLXDPortRangeMappingPropagatesDeviceFailure(t *testing.T) {
	executor := &failingLXDPortsExecutor{failOn: "lxc config device add"}
	p := &LXDProvider{
		config:    provider.NodeConfig{PortIP: "198.51.100.10", Host: "2001:db8::1"},
		sshClient: utils.NewSafeShellExecutor(executor),
	}
	err := p.setupPortRangeMappingWithIP("guest", []providerModel.Port{
		{HostPort: 22000, GuestPort: 22000, Protocol: "tcp"},
		{HostPort: 22001, GuestPort: 22001, Protocol: "tcp"},
	}, "device_proxy", "192.0.2.10")
	if err == nil || !strings.Contains(err.Error(), "创建LXD端口区间映射失败") {
		t.Fatalf("setupPortRangeMappingWithIP() error = %v, want propagated device failure", err)
	}
}

func TestLXDControllerPortMappingsAreNotInstalledOnNode(t *testing.T) {
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
	if err := db.Exec(`INSERT INTO providers (id, ipv4_port_mapping_method) VALUES (1, 'device_proxy')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO instances (id, name, provider_id, private_ip) VALUES (1, 'guest', 1, '192.0.2.10')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO ports (id, instance_id, host_port, guest_port, protocol, status, mapping_type, mapping_method, is_ssh) VALUES (1, 1, 22000, 22, 'tcp', 'active', 'controller', 'device_proxy', 1)`).Error; err != nil {
		t.Fatal(err)
	}
	executor := &recordingLXDIPv6Executor{}
	p := &LXDProvider{config: provider.NodeConfig{ID: 1, PortIP: "198.51.100.10"}, sshClient: utils.NewSafeShellExecutor(executor)}
	if err := p.configurePortMappingsWithIP("guest", NetworkConfig{NetworkType: "nat_ipv4", IPv4PortMappingMethod: "device_proxy"}, "192.0.2.10"); err != nil {
		t.Fatalf("configurePortMappingsWithIP() error = %v", err)
	}
	if len(executor.commands) != 0 {
		t.Fatalf("controller mapping unexpectedly installed on node: %#v", executor.commands)
	}
}

func TestLXDSSHPortMappingsExpandAutomaticDualStackRow(t *testing.T) {
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
	if err := db.Exec(`INSERT INTO providers (id, ipv4_port_mapping_method, ipv6_port_mapping_method) VALUES (1, 'device_proxy', 'device_proxy')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO instances (id, name, provider_id, private_ip, ipv6_address) VALUES (1, 'guest', 1, '192.0.2.10', '2001:db8::10')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO ports (id, instance_id, host_port, guest_port, protocol, status, mapping_type, mapping_method, is_ssh, is_automatic, port_type, ipv6_enabled) VALUES (1, 1, 22000, 22, 'tcp', 'active', 'node', 'device_proxy', 1, 1, 'range_mapped', 1)`).Error; err != nil {
		t.Fatal(err)
	}
	executor := &recordingLXDIPv6Executor{}
	p := &LXDProvider{config: provider.NodeConfig{ID: 1, PortIP: "198.51.100.10", Host: "2001:db8::1"}, sshClient: utils.NewSafeShellExecutor(executor)}
	if err := p.configurePortMappingsWithIP("guest", NetworkConfig{
		NetworkType: "nat_ipv4_ipv6", IPv4PortMappingMethod: "device_proxy", IPv6PortMappingMethod: "device_proxy",
	}, "192.0.2.10"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(executor.commands, "\n")
	if !strings.Contains(joined, "proxy-tcp-22000") || !strings.Contains(joined, "proxy-v6-tcp-22000") {
		t.Fatalf("dual-stack automatic row did not install both families: %s", joined)
	}

	initialExecutor := &recordingLXDIPv6Executor{}
	initial := &LXDProvider{config: p.config, sshClient: utils.NewSafeShellExecutor(initialExecutor)}
	if err := initial.configureInitialPortMappingsWithIP("guest", NetworkConfig{
		NetworkType: "nat_ipv4_ipv6", IPv4PortMappingMethod: "device_proxy", IPv6PortMappingMethod: "device_proxy",
	}, "192.0.2.10"); err != nil {
		t.Fatal(err)
	}
	initialCommands := strings.Join(initialExecutor.commands, "\n")
	if !strings.Contains(initialCommands, "proxy-tcp-22000") || strings.Contains(initialCommands, "proxy-v6-tcp-22000") {
		t.Fatalf("initial phase crossed address families: %s", initialCommands)
	}

	ipv6Executor := &recordingLXDIPv6Executor{}
	ipv6Phase := &LXDProvider{config: p.config, sshClient: utils.NewSafeShellExecutor(ipv6Executor)}
	if err := ipv6Phase.configurePortMappingFamiliesWithIP("guest", NetworkConfig{
		NetworkType: "ipv6_only", IPv4PortMappingMethod: "device_proxy", IPv6PortMappingMethod: "device_proxy",
	}, "", false, true); err != nil {
		t.Fatal(err)
	}
	ipv6Commands := strings.Join(ipv6Executor.commands, "\n")
	if strings.Contains(ipv6Commands, "proxy-tcp-22000") || !strings.Contains(ipv6Commands, "proxy-v6-tcp-22000") {
		t.Fatalf("IPv6 phase crossed address families: %s", ipv6Commands)
	}
}

func TestLXDSecurityNestingIsAppliedAndFailuresAreVisible(t *testing.T) {
	executor := &recordingLXDIPv6Executor{}
	p := &LXDProvider{
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

	failing := &failingLXDPortsExecutor{failOn: "security.nesting"}
	p.sshClient = utils.NewSafeShellExecutor(failing)
	if err := p.configureInstanceSecurity(context.Background(), provider.InstanceConfig{Name: "guest", InstanceType: "container", AllowNesting: &allow}); err == nil {
		t.Fatal("configureInstanceSecurity() succeeded after security.nesting write failure")
	}
}

func TestLXDAPIConfiguresPortMappingsWithoutReplacingInstanceConfig(t *testing.T) {
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
	if err := db.Exec(`INSERT INTO providers (id, name, type) VALUES (1, 'lxd-test', 'lxd')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO instances (id, name, provider_id, private_ip) VALUES (1, 'guest', 1, '192.0.2.10')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO ports (id, instance_id, provider_id, host_port, guest_port, port_count, protocol, status, mapping_type) VALUES (1, 1, 1, 22000, 22, 1, 'tcp', 'active', 'node')`).Error; err != nil {
		t.Fatal(err)
	}
	p := NewLXDProvider().(*LXDProvider)
	p.config = provider.NodeConfig{ID: 1, Host: "127.0.0.1", PortIP: "198.51.100.10"}
	transport := &lxdIPv4MetadataTransport{t: t}
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
		tr := &lxdIPv4MetadataTransport{t: t, expectIPv6: true}
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
		tr = &lxdIPv4MetadataTransport{t: t}
		p.apiClient = &http.Client{Transport: tr}
		if err := p.apiConfigurePortMappings(context.Background(), provider.InstanceConfig{Name: "guest", Metadata: map[string]string{"network_type": "nat_ipv4_ipv6"}}); err != nil {
			t.Fatal(err)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failing := &lxdIPv4MetadataTransport{t: t, cancelOnUpdate: cancel}
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
			tr := &lxdIPv4MetadataTransport{t: t}
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

func TestLXDAPIPortDevicesSupportBothAndRanges(t *testing.T) {
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

func TestLXDAPIPortDevicesRejectInvalidRangeAndConflict(t *testing.T) {
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

func TestLXDAPIPortDevicesSupportIPv6Targets(t *testing.T) {
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
