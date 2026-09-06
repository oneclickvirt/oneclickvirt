package agent

import (
	"context"
	"time"

	"oneclickvirt/global"
)

// agentShutdownContext returns the process-wide cancellation context when the
// runtime has initialized it. Tests and early startup paths may call helpers
// before initialization, so a background context is the safe fallback.
func agentShutdownContext() context.Context {
	if global.APP_SHUTDOWN_CONTEXT != nil {
		return global.APP_SHUTDOWN_CONTEXT
	}
	return context.Background()
}

// waitForAgentShutdown performs a bounded delay that can be interrupted during
// graceful shutdown. It returns false when the delay was cancelled.
func waitForAgentShutdown(delay time.Duration) bool {
	if delay <= 0 {
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-agentShutdownContext().Done():
		return false
	}
}
