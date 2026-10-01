package scheduler

import (
	"context"
	"testing"
	"time"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

func TestInstanceSyncStopCancelsStartupDelay(t *testing.T) {
	previousLog := global.APP_LOG
	previousConfig := global.GetAppConfig()
	global.APP_LOG = zap.NewNop()
	config := previousConfig
	config.System.EnableInstanceSync = true
	global.SetAppConfig(config)
	defer func() {
		global.APP_LOG = previousLog
		global.SetAppConfig(previousConfig)
	}()

	service := NewInstanceSyncSchedulerService()
	service.Start(context.Background())

	started := time.Now()
	service.Stop()
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Stop waited for startup delay: %s", elapsed)
	}
	if service.IsRunning() {
		t.Fatal("instance sync scheduler still reports running after Stop")
	}
}

func TestInstanceSyncExternalContextStopsGeneration(t *testing.T) {
	previousLog := global.APP_LOG
	previousConfig := global.GetAppConfig()
	global.APP_LOG = zap.NewNop()
	config := previousConfig
	config.System.EnableInstanceSync = true
	global.SetAppConfig(config)
	defer func() {
		global.APP_LOG = previousLog
		global.SetAppConfig(previousConfig)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	service := NewInstanceSyncSchedulerService()
	service.Start(ctx)
	cancel()

	deadline := time.Now().Add(time.Second)
	for service.IsRunning() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if service.IsRunning() {
		t.Fatal("instance sync scheduler did not stop after parent context cancellation")
	}
	service.Stop()
}

func TestInstanceSyncExternalExitDrainsBackgroundWorkBeforeRestart(t *testing.T) {
	previousLog := global.APP_LOG
	previousConfig := global.GetAppConfig()
	global.APP_LOG = zap.NewNop()
	config := previousConfig
	config.System.EnableInstanceSync = true
	global.SetAppConfig(config)
	defer func() {
		global.APP_LOG = previousLog
		global.SetAppConfig(previousConfig)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	service := NewInstanceSyncSchedulerService()
	service.wg.Add(1) // A tracked interface refresh is still unwinding.
	service.Start(ctx)
	service.mu.RLock()
	oldGeneration, done := service.stopChan, service.doneChan
	service.mu.RUnlock()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("parent cancellation did not stop the scheduler loop")
	}
	service.Start(context.Background())
	service.mu.RLock()
	restarted := service.stopChan != oldGeneration || service.isRunning || !service.stopping
	service.mu.RUnlock()
	service.wg.Done()
	if restarted {
		t.Fatal("scheduler restarted while previous background work was active")
	}
	deadline := time.Now().Add(time.Second)
	for {
		service.mu.RLock()
		stopping := service.stopping
		service.mu.RUnlock()
		if !stopping {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("scheduler stayed blocked after background work drained")
		}
		time.Sleep(time.Millisecond)
	}
	service.Start(context.Background())
	service.Stop()
}
