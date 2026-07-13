package proxmox

import (
	"testing"

	"oneclickvirt/global"
	coreprovider "oneclickvirt/provider"

	"go.uber.org/zap"
)

func TestApplyNATSubnet(t *testing.T) {
	p := &ProxmoxProvider{}
	if !p.applyNATSubnet("10.250.0.0/24") {
		t.Fatal("expected valid /24 subnet")
	}
	if got := p.vmidToInternalIP(100); got != "10.250.0.2" {
		t.Fatalf("vmidToInternalIP(100) = %q", got)
	}
	if got := p.getInternalGateway(); got != "10.250.0.1" {
		t.Fatalf("gateway = %q", got)
	}
}

func TestApplyNATSubnetRejectsUnsupportedNetworks(t *testing.T) {
	for _, cidr := range []string{"10.250.0.1/24", "10.250.0.0/16", "not-a-cidr", "2001:db8::/64"} {
		p := &ProxmoxProvider{}
		if p.applyNATSubnet(cidr) {
			t.Fatalf("expected %q to be rejected", cidr)
		}
	}
}

func TestInitBridgeNamesUsesConfiguredSubnetForThirdPartyInstall(t *testing.T) {
	oldLogger := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = oldLogger })
	p := &ProxmoxProvider{}
	p.initBridgeNames(coreprovider.NodeConfig{NodeInstallType: "third_party", NATSubnet: "10.251.0.0/24"})
	if got := p.vmidToInternalIP(101); got != "10.251.0.3" {
		t.Fatalf("vmidToInternalIP(101) = %q", got)
	}
}

func TestInitBridgeNamesIgnoresStaleSubnetForScriptInstall(t *testing.T) {
	oldLogger := global.APP_LOG
	global.APP_LOG = zap.NewNop()
	t.Cleanup(func() { global.APP_LOG = oldLogger })
	p := &ProxmoxProvider{}
	p.initBridgeNames(coreprovider.NodeConfig{NodeInstallType: "script", NATSubnet: "10.251.0.0/24"})
	if got := p.vmidToInternalIP(100); got != "172.16.1.2" {
		t.Fatalf("script install used stale configured subnet: %q", got)
	}
}
