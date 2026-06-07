package provider

import providerModel "oneclickvirt/model/provider"

func normalizeTrafficOverLimitPolicy(action string, speedLimitKbps int) (string, int) {
	switch action {
	case providerModel.TrafficOverLimitActionSpeedLimit:
		if speedLimitKbps <= 0 {
			speedLimitKbps = 1024
		}
		return action, speedLimitKbps
	case providerModel.TrafficOverLimitActionFreeze,
		providerModel.TrafficOverLimitActionMarkOnly,
		providerModel.TrafficOverLimitActionStop:
		return action, speedLimitKbps
	default:
		if speedLimitKbps <= 0 {
			speedLimitKbps = 1024
		}
		return providerModel.TrafficOverLimitActionStop, speedLimitKbps
	}
}

func normalizeInstanceExpiryPolicy(action string, extendDays int) (string, int) {
	switch action {
	case providerModel.InstanceExpiryActionExtend:
		if extendDays <= 0 {
			extendDays = 1
		}
		return action, extendDays
	case providerModel.InstanceExpiryActionFreeze,
		providerModel.InstanceExpiryActionStop,
		providerModel.InstanceExpiryActionDelete:
		return action, 0
	default:
		return providerModel.InstanceExpiryActionDelete, 0
	}
}
