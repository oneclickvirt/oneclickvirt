package kubevirt

import (
	"strings"
	"testing"

	"oneclickvirt/provider"

	"gopkg.in/yaml.v3"
)

func routedKubeVirtConfig(networkType string) provider.InstanceConfig {
	return provider.InstanceConfig{Metadata: map[string]string{
		"network_type":          networkType,
		"static_ipv6":           "2001:db8::2",
		"static_ipv6_cidr":      "2001:db8::/126",
		"static_ipv6_gateway":   "2001:db8::1",
		"static_ipv6_bridge":    "oneclickvirt6",
		"static_ipv6_tunnel_id": "17",
	}}
}

func TestKubeVirtRoutedIPv6NADUsesStaticIPAMAndTunnelBridge(t *testing.T) {
	plan, err := resolveKubeVirtIPv6Plan(routedKubeVirtConfig("nat_ipv4_ipv6"))
	if err != nil {
		t.Fatalf("resolveKubeVirtIPv6Plan() error = %v", err)
	}
	nad, err := kubeVirtRoutedNADYAML(plan)
	if err != nil {
		t.Fatalf("kubeVirtRoutedNADYAML() error = %v", err)
	}
	for _, want := range []string{"NetworkAttachmentDefinition", "oneclickvirt6", "bridge", "2001:db8::2/126", "2001:db8::1", "type\\\":\\\"static"} {
		if !strings.Contains(nad, want) {
			t.Fatalf("NAD YAML missing %q:\n%s", want, nad)
		}
	}
	if strings.Contains(nad, "macvlan") {
		t.Fatalf("NAD must use a bridge veth, not macvlan: %s", nad)
	}
	annotation := kubeVirtRoutedNetworkAnnotation(plan)
	if !strings.Contains(annotation, plan.NADName) || !strings.Contains(annotation, "oneclickvirt6") {
		t.Fatalf("unexpected Multus annotation: %s", annotation)
	}
	if !strings.Contains(kubeVirtVMRoutedNetworkData(plan), "2001:db8::2/126") {
		t.Fatalf("VM network-data does not contain the allocated IPv6 address")
	}
}

func TestKubeVirtVMYAMLIsValidForRoutedAndNativeNetworks(t *testing.T) {
	routedPlan, err := resolveKubeVirtIPv6Plan(routedKubeVirtConfig("nat_ipv4_ipv6"))
	if err != nil {
		t.Fatalf("resolveKubeVirtIPv6Plan() error = %v", err)
	}
	for _, test := range []struct {
		name  string
		plan  routedKubeVirtIPv6Plan
		want  []string
		avoid []string
	}{
		{
			name: "routed dual stack",
			plan: routedPlan,
			want: []string{"tunnelv6", "networkData:", "2001:db8::2/126", "masquerade: {}"},
		},
		{
			name:  "native IPv4",
			plan:  routedKubeVirtIPv6Plan{NetworkType: "nat_ipv4"},
			want:  []string{"masquerade: {}"},
			avoid: []string{"k8s.v1.cni.cncf.io/networks", "networkData:", "tunnelv6"},
		},
		{
			name:  "routed IPv6 only",
			plan:  func() routedKubeVirtIPv6Plan { plan := routedPlan; plan.NetworkType = "ipv6_only"; return plan }(),
			want:  []string{"tunnelv6", "networkData:"},
			avoid: []string{"masquerade: {}", "- name: default\n          pod: {}"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			rendered := kubeVirtVMYAML("vm-test", 1, 512, "vm-test-dv", "password", test.plan)
			if strings.Contains(rendered, "\t") {
				t.Fatalf("VM YAML contains a tab indentation: %s", rendered)
			}
			var parsed map[string]interface{}
			if err := yaml.Unmarshal([]byte(rendered), &parsed); err != nil {
				t.Fatalf("VM YAML is invalid: %v\n%s", err, rendered)
			}
			for _, want := range test.want {
				if !strings.Contains(rendered, want) {
					t.Fatalf("VM YAML missing %q:\n%s", want, rendered)
				}
			}
			for _, avoid := range test.avoid {
				if strings.Contains(rendered, avoid) {
					t.Fatalf("VM YAML unexpectedly contains %q:\n%s", avoid, rendered)
				}
			}
		})
	}
}

func TestResolveKubeVirtIPv6PlanRejectsMissingMultusMetadata(t *testing.T) {
	config := provider.InstanceConfig{Metadata: map[string]string{
		"network_type": "nat_ipv4_ipv6",
		"static_ipv6":  "2001:db8::2",
	}}
	if _, err := resolveKubeVirtIPv6Plan(config); err == nil {
		t.Fatal("KubeVirt should reject a static address without routed metadata")
	}
}

func TestResolveKubeVirtIPv6PlanRejectsMissingAllocation(t *testing.T) {
	config := provider.InstanceConfig{Metadata: map[string]string{"network_type": "nat_ipv4_ipv6"}}
	if _, err := resolveKubeVirtIPv6Plan(config); err == nil {
		t.Fatal("KubeVirt dual-stack request without a routed allocation should be rejected")
	}
}

func TestKubeVirtNodeNameFromPreflightOutput(t *testing.T) {
	output := "bridge ready\n  ONECLICKVIRT_K8S_NODE=worker-01  \n"
	if got := kubeVirtNodeNameFromPreflightOutput(output); got != "worker-01" {
		t.Fatalf("kubeVirtNodeNameFromPreflightOutput() = %q, want worker-01", got)
	}
}

func TestKubeVirtContainerDeploymentYAMLIsValidForRoutedAndNativeNetworks(t *testing.T) {
	routedPlan, err := resolveKubeVirtIPv6Plan(routedKubeVirtConfig("nat_ipv4_ipv6"))
	if err != nil {
		t.Fatalf("resolveKubeVirtIPv6Plan() error = %v", err)
	}
	routedPlan.NodeName = "worker-01"
	ports := []kubeVirtContainerPort{{Name: "p22-tcp", Protocol: "TCP", HostPort: 30222, ContainerPort: 22}}
	for _, test := range []struct {
		name  string
		plan  routedKubeVirtIPv6Plan
		want  []string
		avoid []string
	}{
		{
			name: "routed dual stack",
			plan: routedPlan,
			want: []string{"k8s.v1.cni.cncf.io/networks", "oneclickvirt6", "worker-01", "containerPort: 22"},
		},
		{
			name:  "native IPv4",
			plan:  routedKubeVirtIPv6Plan{NetworkType: "nat_ipv4"},
			want:  []string{"containers:", "containerPort: 22"},
			avoid: []string{"k8s.v1.cni.cncf.io/networks", "nodeSelector:"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			rendered := kubeVirtContainerDeploymentYAML("container-test", "alpine:latest", "password", "1", 256, ports, test.plan)
			if strings.Contains(rendered, "\t") {
				t.Fatalf("Deployment YAML contains a tab indentation: %s", rendered)
			}
			var parsed map[string]interface{}
			if err := yaml.Unmarshal([]byte(rendered), &parsed); err != nil {
				t.Fatalf("Deployment YAML is invalid: %v\n%s", err, rendered)
			}
			for _, want := range test.want {
				if !strings.Contains(rendered, want) {
					t.Fatalf("Deployment YAML missing %q:\n%s", want, rendered)
				}
			}
			for _, avoid := range test.avoid {
				if strings.Contains(rendered, avoid) {
					t.Fatalf("Deployment YAML unexpectedly contains %q:\n%s", avoid, rendered)
				}
			}
		})
	}
}
