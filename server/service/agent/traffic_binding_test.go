package agent

import (
	"reflect"
	"testing"

	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
)

func TestBuildTrafficBindingsSeparateIncusNICs(t *testing.T) {
	instance := &providerModel.Instance{
		PrivateIP:   "10.22.65.213",
		IPv6Address: "fd42::10",
		PublicIPv6:  "2605:52c0::10/128",
	}
	got := buildTrafficBindings(instance, &InstanceInterfaces{V4: "veth4", V6: "veth6"})
	want := []TrafficBinding{
		{Interface: "veth4", Addresses: []string{"10.22.65.213"}, Families: []string{"ipv4"}},
		{Interface: "veth6", Addresses: []string{"2605:52c0::10", "fd42::10"}, Families: []string{"ipv6"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildTrafficBindings() = %#v, want %#v", got, want)
	}
}

func TestBuildTrafficBindingsMergesSingleNICDualStack(t *testing.T) {
	instance := &providerModel.Instance{PrivateIP: "172.17.0.2", PublicIPv6: "2001:db8::2"}
	got := buildTrafficBindings(instance, &InstanceInterfaces{V4: "veth0", V6: "veth0"})
	want := []TrafficBinding{{Interface: "veth0", Addresses: []string{"172.17.0.2", "2001:db8::2"}, Families: []string{"ipv4", "ipv6"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildTrafficBindings() = %#v, want %#v", got, want)
	}
}

func TestBuildTrafficBindingsPVERoutedIPv6UsesSecondTap(t *testing.T) {
	instance := &providerModel.Instance{
		NetworkType: "nat_ipv4_ipv6",
		PrivateIP:   "10.0.0.102",
		PublicIPv6:  "2001:470:6d:69d::102/128",
	}
	got := buildTrafficBindings(instance, &InstanceInterfaces{V4: "tap102i0", V6: "tap102i1"})
	want := []TrafficBinding{
		{Interface: "tap102i0", Addresses: []string{"10.0.0.102"}, Families: []string{"ipv4"}},
		{Interface: "tap102i1", Addresses: []string{"2001:470:6d:69d::102"}, Families: []string{"ipv6"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildTrafficBindings() = %#v, want %#v", got, want)
	}
}

func TestBuildTrafficBindingsPVERoutedIPv6UsesSecondVethForContainer(t *testing.T) {
	instance := &providerModel.Instance{
		NetworkType: "nat_ipv4_ipv6",
		PrivateIP:   "172.16.1.100",
		IPv6Address: "2001:470:6d:69d::100/64",
	}
	got := buildTrafficBindings(instance, &InstanceInterfaces{V4: "veth100i0", V6: "veth100i1"})
	want := []TrafficBinding{
		{Interface: "veth100i0", Addresses: []string{"172.16.1.100"}, Families: []string{"ipv4"}},
		{Interface: "veth100i1", Addresses: []string{"2001:470:6d:69d::100"}, Families: []string{"ipv6"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildTrafficBindings() = %#v, want %#v", got, want)
	}
}

func TestBuildTrafficBindingsIPv6OnlyDoesNotCreateEmptyIPv4Binding(t *testing.T) {
	instance := &providerModel.Instance{NetworkType: "ipv6_only", PublicIPv6: "2605:52c0::3"}
	got := buildTrafficBindings(instance, &InstanceInterfaces{V4: "veth4", V6: "veth6"})
	want := []TrafficBinding{{Interface: "veth6", Addresses: []string{"2605:52c0::3"}, Families: []string{"ipv6"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildTrafficBindings() = %#v, want %#v", got, want)
	}
}

func TestBuildTrafficBindingsKeepsExpectedDualStackInterfaceWithoutAddress(t *testing.T) {
	instance := &providerModel.Instance{
		NetworkType: "nat_ipv4_ipv6",
		PrivateIP:   "10.0.0.2",
	}
	got := buildTrafficBindings(instance, &InstanceInterfaces{V4: "veth4", V6: "veth6"})
	want := []TrafficBinding{
		{Interface: "veth4", Addresses: []string{"10.0.0.2"}, Families: []string{"ipv4"}},
		{Interface: "veth6", Addresses: []string{}, Families: []string{"ipv6"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildTrafficBindings() = %#v, want %#v", got, want)
	}
}

func TestBuildTrafficBindingsKeepsMissingFamilyOnSharedNIC(t *testing.T) {
	instance := &providerModel.Instance{
		NetworkType: "nat_ipv4_ipv6",
		PrivateIP:   "10.0.0.2",
	}
	got := buildTrafficBindings(instance, &InstanceInterfaces{V4: "veth0", V6: "veth0"})
	want := []TrafficBinding{{
		Interface: "veth0",
		Addresses: []string{"10.0.0.2"},
		Families:  []string{"ipv4", "ipv6"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildTrafficBindings() = %#v, want %#v", got, want)
	}
}

func TestResolveMonitorBindingInterfacesUsesAddressFamilies(t *testing.T) {
	instance := &providerModel.Instance{NetworkType: "nat_ipv4_ipv6"}
	monitor := &monitoringModel.AgentMonitor{
		Interfaces: "veth6,veth4",
		Bindings: marshalTrafficBindings([]TrafficBinding{
			{Interface: "veth6", Addresses: []string{"2001:db8::2"}},
			{Interface: "veth4", Addresses: []string{"10.0.0.2"}},
		}),
	}
	v4, v6 := resolveMonitorBindingInterfaces(instance, monitor)
	if v4 != "veth4" || v6 != "veth6" {
		t.Fatalf("resolveMonitorBindingInterfaces() = (%q, %q), want (veth4, veth6)", v4, v6)
	}
}

func TestResolveMonitorBindingInterfacesUsesDeclaredFamilyWithoutAddress(t *testing.T) {
	instance := &providerModel.Instance{NetworkType: "nat_ipv4_ipv6"}
	monitor := &monitoringModel.AgentMonitor{
		Bindings: marshalTrafficBindings([]TrafficBinding{
			{Interface: "veth4", Families: []string{"ipv4"}},
			{Interface: "veth6", Families: []string{"ipv6"}},
		}),
	}
	v4, v6 := resolveMonitorBindingInterfaces(instance, monitor)
	if v4 != "veth4" || v6 != "veth6" {
		t.Fatalf("resolveMonitorBindingInterfaces() = (%q, %q), want (veth4, veth6)", v4, v6)
	}
}
