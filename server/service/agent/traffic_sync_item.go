package agent

import (
	"time"

	"oneclickvirt/global"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"

	"go.uber.org/zap"
)

// trafficSyncItem is an Agent counter snapshot prepared before the short
// database write transaction. Keeping this calculation separate makes the
// controller's counter invariants independently testable.
type trafficSyncItem struct {
	monitor           *monitoringModel.AgentMonitor
	mappedIP          string
	currentTraffic    uint64
	currentTrafficIn  uint64
	currentTrafficOut uint64
	deltaBytesIn      uint64
	deltaBytesOut     uint64
	rxMB              float64
	txMB              float64
	alignedTime       time.Time
}

// agentTrafficMappedIP keeps the legacy pmacct-compatible snapshot address a
// user-facing address, never a host veth/tap name. Agent monitors identify
// traffic by bindings, but consumers of pmacct_traffic_records still use this
// field when displaying an instance's address. Prefer public IPv4 so it
// matches the historical pmacct path; IPv6-only instances fall back to their
// public IPv6 address.
func agentTrafficMappedIP(instance *providerModel.Instance, monitor *monitoringModel.AgentMonitor) string {
	if instance != nil {
		for _, candidate := range []string{
			instance.PublicIP,
			instance.PrivateIP,
			instance.PublicIPv6,
			instance.IPv6Address,
		} {
			if address := normalizeTrafficAddress(candidate); address != "" {
				return address
			}
		}
	}
	if monitor != nil {
		if address := normalizeTrafficAddress(monitor.InnerIP); address != "" {
			return address
		}
	}
	return "agent"
}

func buildTrafficSyncItem(monitor *monitoringModel.AgentMonitor, info *InfoResponse, now time.Time) trafficSyncItem {
	currentTrafficIn := info.UsedTrafficIn
	currentTrafficOut := info.UsedTrafficOut
	currentTraffic := totalAgentTraffic(currentTrafficIn, currentTrafficOut)
	if info.UsedTraffic != currentTraffic && global.APP_LOG != nil {
		global.APP_LOG.Warn("Agent total traffic does not match directional counters; using directional total",
			zap.Uint("instance_id", monitor.InstanceID),
			zap.Int64("agent_monitor_id", monitor.AgentMonitorID),
			zap.Uint64("reported_total", info.UsedTraffic),
			zap.Uint64("directional_total", currentTraffic))
	}

	deltaBytesIn := currentTrafficIn
	if currentTrafficIn >= monitor.LastTrafficBytesIn {
		deltaBytesIn -= monitor.LastTrafficBytesIn
	} else if global.APP_LOG != nil {
		global.APP_LOG.Warn("Agent inbound traffic counter reset detected",
			zap.Uint("instance_id", monitor.InstanceID),
			zap.Uint64("last_in", monitor.LastTrafficBytesIn),
			zap.Uint64("current_in", currentTrafficIn))
	}

	deltaBytesOut := currentTrafficOut
	if currentTrafficOut >= monitor.LastTrafficBytesOut {
		deltaBytesOut -= monitor.LastTrafficBytesOut
	} else if global.APP_LOG != nil {
		global.APP_LOG.Warn("Agent outbound traffic counter reset detected",
			zap.Uint("instance_id", monitor.InstanceID),
			zap.Uint64("last_out", monitor.LastTrafficBytesOut),
			zap.Uint64("current_out", currentTrafficOut))
	}

	minute := (now.Minute() / 5) * 5
	return trafficSyncItem{
		monitor:           monitor,
		currentTraffic:    currentTraffic,
		currentTrafficIn:  currentTrafficIn,
		currentTrafficOut: currentTrafficOut,
		deltaBytesIn:      deltaBytesIn,
		deltaBytesOut:     deltaBytesOut,
		rxMB:              float64(deltaBytesIn) / 1048576.0,
		txMB:              float64(deltaBytesOut) / 1048576.0,
		alignedTime:       time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), minute, 0, 0, now.Location()),
	}
}

func totalAgentTraffic(inbound, outbound uint64) uint64 {
	if ^uint64(0)-inbound < outbound {
		return ^uint64(0)
	}
	return inbound + outbound
}
