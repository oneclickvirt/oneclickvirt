package resources

import providerModel "oneclickvirt/model/provider"

func effectiveStoredHostPortEnd(port providerModel.Port) int {
	if port.HostPortEnd >= port.HostPort {
		return port.HostPortEnd
	}
	if port.PortCount > 1 {
		return port.HostPort + port.PortCount - 1
	}
	return port.HostPort
}

func collectOccupiedHostPorts(records []providerModel.Port, startPort, endPort int) map[int]struct{} {
	occupied := make(map[int]struct{})
	for _, record := range records {
		recordStart := record.HostPort
		recordEnd := effectiveStoredHostPortEnd(record)
		if recordEnd < startPort || recordStart > endPort {
			continue
		}
		if recordStart < startPort {
			recordStart = startPort
		}
		if recordEnd > endPort {
			recordEnd = endPort
		}
		for port := recordStart; port <= recordEnd; port++ {
			occupied[port] = struct{}{}
		}
	}
	return occupied
}

func hostPortRangeConflicts(records []providerModel.Port, startPort, endPort int) []int {
	occupied := collectOccupiedHostPorts(records, startPort, endPort)
	conflicts := make([]int, 0, len(occupied))
	for port := startPort; port <= endPort; port++ {
		if _, exists := occupied[port]; exists {
			conflicts = append(conflicts, port)
		}
	}
	return conflicts
}
