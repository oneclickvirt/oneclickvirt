package constant

// Instance status constants - 实例状态常量
const (
	// Stable states - 稳定状态（计入 used_quota）
	InstanceStatusRunning = "running"
	InstanceStatusStopped = "stopped"
	InstanceStatusError   = "error"

	// Transitional states - 过渡状态（计入 pending_quota，不应在列表中显示为"使用中"）
	InstanceStatusCreating  = "creating"
	InstanceStatusResetting = "resetting"

	// Terminal states - 终止状态（不计入配额）
	InstanceStatusDeleting = "deleting"
	InstanceStatusDeleted  = "deleted"
	InstanceStatusFailed   = "failed"
)

// GetStableStatuses 返回所有稳定状态
// 这些状态的实例应该计入 used_quota
func GetStableStatuses() []string {
	return []string{
		InstanceStatusRunning,
		InstanceStatusStopped,
		InstanceStatusError,
	}
}

// GetTransitionalStatuses 返回所有过渡状态
// 这些状态的实例应该计入 pending_quota
func GetTransitionalStatuses() []string {
	return []string{
		InstanceStatusCreating,
		InstanceStatusResetting,
	}
}

// GetTerminalStatuses 返回所有终止状态
// 这些状态的实例不应该计入配额
func GetTerminalStatuses() []string {
	return []string{
		InstanceStatusDeleting,
		InstanceStatusDeleted,
		InstanceStatusFailed,
	}
}

// GetQuotaCountableStatuses 返回所有应该计入配额统计的状态
// 用于防止双倍计数：排除过渡状态和终止状态
// 只统计稳定状态的实例
func GetQuotaCountableStatuses() []string {
	return GetStableStatuses()
}

// IsStableStatus 判断是否为稳定状态
func IsStableStatus(status string) bool {
	for _, s := range GetStableStatuses() {
		if status == s {
			return true
		}
	}
	return false
}

// IsTransitionalStatus 判断是否为过渡状态
func IsTransitionalStatus(status string) bool {
	for _, s := range GetTransitionalStatuses() {
		if status == s {
			return true
		}
	}
	return false
}

// IsTerminalStatus 判断是否为终止状态
func IsTerminalStatus(status string) bool {
	for _, s := range GetTerminalStatuses() {
		if status == s {
			return true
		}
	}
	return false
}
