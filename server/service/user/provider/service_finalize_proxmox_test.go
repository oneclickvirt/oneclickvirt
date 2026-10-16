package provider

import (
	"context"
	"net"
	"testing"
	"time"

	"go.uber.org/zap"
	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	coreprovider "oneclickvirt/provider"
)

type finalizationPVE struct {
	coreprovider.Provider
	ipv4, ipv6       string
	v4Calls, v6Calls int
}

func TestSSHPasswordVerificationCancelsSilentHandshake(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	previous := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	defer func() { global.APP_LOG = previous }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	completed := make(chan bool, 1)
	go func() {
		completed <- (&Service{}).verifySSHPasswordAuth(ctx, 1, "127.0.0.1", listener.Addr().(*net.TCPAddr).Port, "guest", "fixture-password")
	}()
	select {
	case conn := <-accepted:
		defer conn.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("verification never connected to the silent peer")
	}
	cancel()
	select {
	case ok := <-completed:
		if ok {
			t.Fatal("silent peer authenticated")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("task cancellation did not interrupt SSH handshake")
	}
}

func (p *finalizationPVE) GetInstanceIPv4(context.Context, string) (string, error) {
	p.v4Calls++
	return p.ipv4, nil
}
func (p *finalizationPVE) GetInstanceIPv6(context.Context, string) (string, error) {
	p.v6Calls++
	return p.ipv6, nil
}
func (p *finalizationPVE) GetInstancePublicIPv6(context.Context, string) (string, error) {
	return p.ipv6, nil
}

func TestProxmoxFinalizationHonorsInstanceNetworkOverride(t *testing.T) {
	for _, mode := range []string{"ipv6_only", "nat_ipv4_ipv6", "nat_ipv4", "dedicated_ipv4", "dedicated_ipv4_ipv6"} {
		t.Run(mode, func(t *testing.T) {
			p := &finalizationPVE{ipv4: "198.51.100.9", ipv6: "2606:4700:1::9"}
			updates := map[string]interface{}{"public_ip": "198.51.100.1"}
			(&Service{}).gatherProxmoxNetworkInfo(context.Background(), p, &providerModel.Instance{NetworkType: mode}, &providerModel.Provider{Type: "proxmox", NetworkType: "dedicated_ipv4_ipv6"}, updates)
			if mode == "ipv6_only" {
				if p.v4Calls != 0 || updates["private_ip"] != "" || updates["public_ip"] != "" {
					t.Fatalf("IPv6-only performed IPv4 work: %#v, calls=%d", updates, p.v4Calls)
				}
			} else if updates["private_ip"] != "198.51.100.9" {
				t.Fatalf("missing IPv4: %#v", updates)
			}
			if mode == "nat_ipv4" || mode == "nat_ipv4_ipv6" {
				if updates["public_ip"] != "198.51.100.1" {
					t.Fatalf("NAT public IP overwritten by guest IP: %#v", updates)
				}
			}
			if mode == "nat_ipv4" || mode == "dedicated_ipv4" {
				if p.v6Calls != 0 || updates["public_ipv6"] != "" {
					t.Fatalf("IPv4-only discovered IPv6: %#v", updates)
				}
			} else if updates["public_ipv6"] != p.ipv6 || updates["ipv6_address"] != p.ipv6 {
				t.Fatalf("lost guest public IPv6: %#v", updates)
			}
		})
	}
}

func TestProxmoxFinalizationRejectsCommandUsageAsAddress(t *testing.T) {
	p := &finalizationPVE{ipv4: "hostname: unrecognized option I\nBusyBox usage", ipv6: "error: unavailable"}
	updates := map[string]interface{}{}
	(&Service{}).gatherProxmoxNetworkInfo(context.Background(), p, &providerModel.Instance{NetworkType: "dedicated_ipv4_ipv6"}, &providerModel.Provider{Type: "proxmox"}, updates)
	if len(updates) != 0 {
		t.Fatalf("persisted diagnostics: %#v", updates)
	}
}

func TestLocalProxmoxNATVerificationUsesGuestBridge(t *testing.T) {
	instance := providerModel.Instance{NetworkType: "nat_ipv4", PrivateIP: "172.16.1.9"}
	node := providerModel.Provider{Type: "proxmox", Endpoint: "127.0.0.1", PortIP: "198.51.100.1"}
	mapping := &providerModel.Port{HostPort: 20001}
	if host, port, ok := passwordVerificationEndpoint(instance, node, mapping); !ok || host != instance.PrivateIP || port != 22 {
		t.Fatalf("local PVE NAT probe: %s:%d available=%v", host, port, ok)
	}
	for _, endpoint := range []string{"198.51.100.1", "localhost", "pve.example.com"} {
		node.Endpoint = endpoint
		if host, port, ok := passwordVerificationEndpoint(instance, node, mapping); !ok || host != node.PortIP || port != mapping.HostPort {
			t.Fatalf("remote endpoint %q used local bridge: %s:%d available=%v", endpoint, host, port, ok)
		}
	}
	node.Endpoint = "127.0.0.1"
	for _, address := range []string{"127.0.0.1", "198.51.100.9", "172.16.1.9 diagnostics"} {
		instance.PrivateIP = address
		if host, port, ok := passwordVerificationEndpoint(instance, node, mapping); !ok || host != node.PortIP || port != mapping.HostPort {
			t.Fatalf("unsafe private address %q used port 22: %s:%d", address, host, port)
		}
	}
	if _, _, ok := passwordVerificationEndpoint(instance, node, nil); ok {
		t.Fatal("missing NAT mapping must not probe node port 22")
	}
}
