package cachekeepalive

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/fx"
)

type lifecycleRecorder struct {
	hooks []fx.Hook
}

func (r *lifecycleRecorder) Append(hook fx.Hook) {
	r.hooks = append(r.hooks, hook)
}

func TestBindManagerShutdownPropagatesDrainError(t *testing.T) {
	m := newTestManager(nil, nil, nil)
	m.pingInflight.Add(1)
	t.Cleanup(func() { m.pingInflight.Done() })

	lc := &lifecycleRecorder{}
	bindManagerShutdown(lc, m, nil)
	if len(lc.hooks) != 1 {
		t.Fatalf("registered hooks = %d, want 1", len(lc.hooks))
	}
	if lc.hooks[0].OnStop == nil {
		t.Fatal("OnStop = nil, want shutdown hook")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	err := lc.hooks[0].OnStop(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("OnStop() error = %v, want context deadline exceeded", err)
	}
}
