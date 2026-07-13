package kubevirt

import "testing"

func TestFilterPortMappingsUsesInstanceNameBoundary(t *testing.T) {
	services := []svcItem{
		{Name: "vm1-ssh", Ports: servicePorts(30022, 22, "TCP")},
		{Name: "vm10-ssh", Ports: servicePorts(30122, 22, "TCP")},
		{Name: "vm1-wrong-selector", TargetName: "vm10", Ports: servicePorts(30222, 22, "TCP")},
		{Name: "arbitrary-service", TargetName: "vm1", Ports: servicePorts(30322, 22, "TCP")},
		{Name: "vm1-invalid", Ports: servicePorts(0, 22, "TCP")},
	}
	mappings := filterPortMappings(services, "vm1")
	if len(mappings) != 2 || mappings[0].HostPort != 30022 || mappings[1].HostPort != 30322 || !mappings[0].IsSSH {
		t.Fatalf("unexpected mappings: %#v", mappings)
	}
}

func TestParseCPUStringRoundsFractionalLimitsUp(t *testing.T) {
	for input, want := range map[string]int{"500m": 1, "1500m": 2, "1.5": 2, "2": 2, "bad": 0} {
		if got := parseCPUString(input); got != want {
			t.Fatalf("parseCPUString(%q) = %d, want %d", input, got, want)
		}
	}
}

func TestParseKubernetesMemoryAndStorageQuantities(t *testing.T) {
	for input, want := range map[string]int64{"1Gi": 1024, "1.5Gi": 1536, "512Mi": 512, "1048576Ki": 1024, "1Ti": 1024 * 1024} {
		if got := parseMemoryString(input); got != want {
			t.Fatalf("parseMemoryString(%q) = %d, want %d", input, got, want)
		}
		if got := parseStorageString(input); got != want {
			t.Fatalf("parseStorageString(%q) = %d, want %d", input, got, want)
		}
	}
}

func servicePorts(nodePort, targetPort int, protocol string) []svcPort {
	return []svcPort{{Name: "ssh", NodePort: nodePort, TargetPort: targetPort, Protocol: protocol}}
}
