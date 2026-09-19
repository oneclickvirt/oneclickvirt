package task

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"go.uber.org/zap"
	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
)

type resetPortCall struct {
	name, protocol, method, address string
	host, guest                     int
}

type resetPortProvider struct {
	calls      []resetPortCall
	ipv4, ipv6 string
	ipv4Reads  int
	ipv6Reads  int
	saves      int
	failPort   int
	saveErr    error
}

func (p *resetPortProvider) GetInstanceIPv4(context.Context, string) (string, error) {
	p.ipv4Reads++
	return p.ipv4, nil
}

func (p *resetPortProvider) GetInstanceIPv6(context.Context, string) (string, error) {
	p.ipv6Reads++
	return p.ipv6, nil
}

func (p *resetPortProvider) SetupPortMappingWithIP(_ context.Context, name string, host, guest int, protocol, method, address string) error {
	p.calls = append(p.calls, resetPortCall{name, protocol, method, address, host, guest})
	if host == p.failPort {
		return fmt.Errorf("injected mapping failure on %d", host)
	}
	return nil
}

func (p *resetPortProvider) SaveIptablesRules() error {
	p.saves++
	return p.saveErr
}

func resetPortTestLogger(t *testing.T) {
	t.Helper()
	previous := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = previous })
}

func TestResetPortMappingsPreserveDualStackRangesAndInstanceNetwork(t *testing.T) {
	resetPortTestLogger(t)
	for _, providerType := range []string{"incus", "lxd"} {
		t.Run(providerType, func(t *testing.T) {
			p := &resetPortProvider{ipv4: "192.0.2.20", ipv6: "2001:db8::20"}
			resetCtx := &ResetTaskContext{
				Provider:        providerModel.Provider{Type: providerType, NetworkType: "nat_ipv4", IPv4PortMappingMethod: "iptables", IPv6PortMappingMethod: "device_proxy"},
				Instance:        providerModel.Instance{NetworkType: "nat_ipv4_ipv6", PrivateIP: "192.0.2.10", IPv6Address: "2001:db8::10"},
				OldInstanceName: "guest", NewProviderInstanceID: "rebuilt-guest",
				OldPortMappings: []providerModel.Port{{HostPort: 22000, GuestPort: 22, HostPortEnd: 22002, GuestPortEnd: 24, PortCount: 3, Protocol: "tcp", IsAutomatic: true, PortType: "range_mapped", IPv6Enabled: true, MappingMethod: "iptables"}},
			}
			if err := (&TaskService{}).configureProviderPortMappings(context.Background(), p, resetCtx); err != nil {
				t.Fatal(err)
			}
			var want []resetPortCall
			for _, family := range []struct{ method, address string }{{"iptables", p.ipv4}, {"device_proxy", p.ipv6}} {
				for n := 0; n < 3; n++ {
					want = append(want, resetPortCall{"rebuilt-guest", "tcp", family.method, family.address, 22000 + n, 22 + n})
				}
			}
			if !reflect.DeepEqual(p.calls, want) {
				t.Fatalf("restored mappings = %#v, want %#v", p.calls, want)
			}
			if p.ipv4Reads != 1 || p.ipv6Reads != 1 || p.saves != 1 {
				t.Fatalf("expected one address lookup per family and one save, got %+v", p)
			}
		})
	}
}

func TestResetIPv6OnlyAndNativeMappingsDoNotRequireIPv4(t *testing.T) {
	resetPortTestLogger(t)
	for _, method := range []string{"device_proxy", "native"} {
		t.Run(method, func(t *testing.T) {
			p := &resetPortProvider{ipv6: "2001:db8::20"}
			resetCtx := &ResetTaskContext{
				Provider: providerModel.Provider{Type: "incus", NetworkType: "nat_ipv4", IPv4PortMappingMethod: "iptables", IPv6PortMappingMethod: method},
				Instance: providerModel.Instance{NetworkType: "ipv6_only"}, OldInstanceName: "guest",
				OldPortMappings: []providerModel.Port{{HostPort: 22443, GuestPort: 443, Protocol: "tcp"}},
			}
			if err := (&TaskService{}).configureProviderPortMappings(context.Background(), p, resetCtx); err != nil {
				t.Fatal(err)
			}
			if p.ipv4Reads != 0 || p.saves != 0 {
				t.Fatalf("IPv6 proxy/native triggered IPv4 or firewall persistence: %+v", p)
			}
			if method == "native" {
				if len(p.calls) != 0 || p.ipv6Reads != 0 {
					t.Fatalf("native mapping must not change node NAT: %+v", p)
				}
			} else if len(p.calls) != 1 || p.calls[0].address != p.ipv6 {
				t.Fatalf("IPv6-only mapping was lost: %+v", p.calls)
			}
		})
	}
}

func TestResetPortMappingFailuresAreReportedWithoutStoppingOtherPorts(t *testing.T) {
	resetPortTestLogger(t)
	p := &resetPortProvider{ipv4: "192.0.2.20", failPort: 22000, saveErr: errors.New("injected save failure")}
	resetCtx := &ResetTaskContext{
		Provider: providerModel.Provider{Type: "lxd", IPv4PortMappingMethod: "device_proxy"}, OldInstanceName: "guest",
		OldPortMappings: []providerModel.Port{
			{HostPort: 22000, GuestPort: 22, Protocol: "tcp", MappingMethod: " NFTABLES "},
			{HostPort: 22001, GuestPort: 80, Protocol: "tcp", MappingMethod: "device_proxy"},
			{HostPort: 22002, GuestPort: 81, Protocol: "tcp", MappingType: "controller"},
		},
	}
	err := (&TaskService{}).configureProviderPortMappings(context.Background(), p, resetCtx)
	if err == nil || !strings.Contains(err.Error(), "injected mapping failure") || !strings.Contains(err.Error(), "injected save failure") {
		t.Fatalf("mapping/save failures disappeared: %v", err)
	}
	if len(p.calls) != 2 || p.calls[0].method != "iptables" || p.calls[1].method != "device_proxy" || p.saves != 1 {
		t.Fatalf("mapping methods, best effort or controller isolation lost: %+v", p)
	}
}

func TestResetPortMappingsHonorCancellationBeforeRemoteIO(t *testing.T) {
	resetPortTestLogger(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &resetPortProvider{ipv4: "192.0.2.20"}
	resetCtx := &ResetTaskContext{Provider: providerModel.Provider{Type: "incus"}, OldInstanceName: "guest", OldPortMappings: []providerModel.Port{{HostPort: 22000, GuestPort: 22}}}
	if err := (&TaskService{}).configureProviderPortMappings(ctx, p, resetCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation was ignored: %v", err)
	}
	if len(p.calls) != 0 || p.ipv4Reads != 0 || p.saves != 0 {
		t.Fatalf("canceled reset performed remote IO: %+v", p)
	}
}
