package scheduler

import (
	"context"
	"sync"
	"testing"

	"go.uber.org/zap"
	"oneclickvirt/global"
)

// A Stop racing with a new Start must close only the generation it observed.
// This is intentionally repeated because the bad ordering is a narrow window:
// Stop used to unlock before close and could then close the replacement channel.
func TestSchedulerStopStartGenerationSafety(t *testing.T) {
	global.APP_LOG = zap.NewNop()
	for _, name := range []string{"snapshot", "controller-health", "provider-health", "instance-recovery"} {
		t.Run(name, func(t *testing.T) {
			for i := 0; i < 100; i++ {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()

				var stop func()
				var start func(context.Context)
				switch name {
				case "snapshot":
					svc := NewSnapshotSchedulerService()
					stop, start = svc.Stop, svc.Start
				case "controller-health":
					svc := NewControllerPortHealthSchedulerService()
					stop, start = svc.Stop, svc.Start
				case "provider-health":
					svc := NewProviderHealthSchedulerService()
					stop, start = svc.Stop, svc.Start
				case "instance-recovery":
					svc := NewInstanceRecoverySchedulerService()
					stop, start = svc.Stop, svc.Start
				}

				start(ctx)
				var wg sync.WaitGroup
				wg.Add(2)
				go func() { defer wg.Done(); stop() }()
				go func() {
					defer wg.Done()
					start(ctx)
					stop()
				}()
				wg.Wait()
				stop()
			}
		})
	}
}
