package health

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

type managerTestChecker struct{}

func (managerTestChecker) CheckHealth(context.Context) (*HealthResult, error) {
	return &HealthResult{Status: HealthStatusHealthy, Timestamp: time.Now()}, nil
}
func (managerTestChecker) GetHealthStatus() HealthStatus { return HealthStatusHealthy }
func (managerTestChecker) SetConfig(HealthConfig)        {}

func TestHealthManagerConcurrentRegistryAndChecks(t *testing.T) {
	hm := NewHealthManager(zap.NewNop())
	checker := managerTestChecker{}
	hm.RegisterChecker("initial", checker)

	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "checker"
			if i%2 == 0 {
				id = "initial"
			}
			hm.RegisterChecker(id, checker)
			_, _ = hm.CheckHealth(context.Background(), id)
			_, _ = hm.CheckAllHealth(context.Background())
			_, _ = hm.GetChecker(id)
			_ = hm.ListCheckers()
			if i%3 == 0 {
				hm.RemoveChecker(id)
			}
		}(i)
	}
	wg.Wait()
	if err := hm.Close(); err != nil {
		t.Fatalf("close health manager: %v", err)
	}
	if ids := hm.ListCheckers(); len(ids) != 0 {
		t.Fatalf("closed manager retained checkers: %v", ids)
	}
}

func TestHealthManagerHandlesNilLoggerAndChecker(t *testing.T) {
	hm := NewHealthManager(nil)
	hm.RegisterChecker("nil", nil)
	if _, err := hm.CheckHealth(context.Background(), "nil"); err == nil {
		t.Fatal("expected nil checker lookup to fail")
	}
	results, err := hm.CheckAllHealth(context.Background())
	if err != nil {
		t.Fatalf("check all health: %v", err)
	}
	if got := results["nil"]; got == nil || got.Status != HealthStatusUnhealthy {
		t.Fatalf("expected nil checker to produce unhealthy result, got %#v", got)
	}
	hm.RegisterChecker("healthy", managerTestChecker{})
	if _, err := hm.CheckAllHealth(context.Background()); err != nil {
		t.Fatalf("check healthy checker with nil logger: %v", err)
	}
}
