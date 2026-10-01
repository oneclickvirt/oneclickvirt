package task

const (
	mainTaskStatusPending    = "pending"
	mainTaskStatusProcessing = "processing"
	mainTaskStatusRunning    = "running"
	mainTaskStatusCancelling = "cancelling"
	mainTaskStatusCompleted  = "completed"
	mainTaskStatusFailed     = "failed"
	mainTaskStatusCancelled  = "cancelled"
	mainTaskStatusTimeout    = "timeout"
	taskTimeoutCancelReason  = "任务执行超时 - 超过配置的执行时限"
)

var (
	mainTaskExecutableStatuses = []string{mainTaskStatusPending, mainTaskStatusProcessing, mainTaskStatusRunning}
	mainTaskInFlightStatuses   = []string{mainTaskStatusProcessing, mainTaskStatusRunning, mainTaskStatusCancelling}
	mainTaskTerminalStatuses   = []string{mainTaskStatusCompleted, mainTaskStatusFailed, mainTaskStatusCancelled, mainTaskStatusTimeout}
)

func isMainTaskActiveStatus(status string) bool {
	for _, s := range mainTaskExecutableStatuses {
		if status == s {
			return true
		}
	}
	return false
}

func isMainTaskRunningLikeStatus(status string) bool {
	return status == mainTaskStatusRunning || status == mainTaskStatusProcessing
}

func isMainTaskTerminalStatus(status string) bool {
	for _, terminal := range mainTaskTerminalStatuses {
		if status == terminal {
			return true
		}
	}
	return false
}
