package constant

// Instance status constants - 实例状态常量
const (
	// Stable states - 稳定状态（计入 used_quota）
	InstanceStatusRunning = "running"
	InstanceStatusStopped = "stopped"
	InstanceStatusError   = "error"

	// Operational transition states - 操作中过渡状态（仍计入 used_quota）
	InstanceStatusStarting   = "starting"
	InstanceStatusStopping   = "stopping"
	InstanceStatusRestarting = "restarting"
	InstanceStatusRebuilding = "rebuilding"

	// Provisioning transition states - 创建/重置过渡状态（计入 pending_quota）
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

// GetOperationalTransitionStatuses 返回仍占用既有资源的操作中过渡状态
func GetOperationalTransitionStatuses() []string {
	return []string{
		InstanceStatusStarting,
		InstanceStatusStopping,
		InstanceStatusRestarting,
		InstanceStatusRebuilding,
	}
}

// GetTransitionalStatuses 返回所有创建/重置过渡状态
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
// 用于防止双倍计数：排除创建/重置待确认状态和终止状态
// 统计稳定状态与 start/stop/restart/rebuild 这类仍占用既有资源的操作中状态
func GetQuotaCountableStatuses() []string {
	statuses := GetStableStatuses()
	statuses = append(statuses, GetOperationalTransitionStatuses()...)
	return statuses
}

// GetDisplayableStatuses 返回用户列表中应显示的状态
// 仅隐藏删除/失败等终止态，避免操作中的实例从列表中短暂消失
func GetDisplayableStatuses() []string {
	statuses := GetQuotaCountableStatuses()
	statuses = append(statuses, GetTransitionalStatuses()...)
	return statuses
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

// IsOperationalTransitionStatus 判断是否为仍占用既有资源的操作中过渡状态
func IsOperationalTransitionStatus(status string) bool {
	for _, s := range GetOperationalTransitionStatuses() {
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
