package vmcli

import (
	"context"
	"errors"
	"testing"

	"oneclickvirt/provider"
)

type lifecycleCancellationExecutor struct {
	vmcliDiscoveryExecutor
	calls  int
	cancel context.CancelFunc
}

func (e *lifecycleCancellationExecutor) ExecuteContext(ctx context.Context, _ string) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	e.calls++
	if e.cancel != nil {
		e.cancel()
		return "", ctx.Err()
	}
	return "", nil
}
func TestVMCLICancellationPreventsFollowupActions(t *testing.T) {
	for _, spec := range []Spec{VirtualBoxSpec(), MultipassSpec(), VagrantSpec()} {
		t.Run(spec.Type, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			e := &lifecycleCancellationExecutor{cancel: cancel}
			p := &Provider{spec: spec, executor: e, connected: true}
			if err := p.RestartInstance(ctx, "guest"); !errors.Is(err, context.Canceled) || e.calls != 1 {
				t.Fatalf("restart after cancellation: %v, calls=%d", err, e.calls)
			}
			if err := p.CreateInstance(ctx, provider.InstanceConfig{Name: "guest"}); !errors.Is(err, context.Canceled) || e.calls != 1 {
				t.Fatalf("create after cancellation: %v, calls=%d", err, e.calls)
			}
		})
	}
}
