package hooks

import (
	"context"
	"io"
	"log/slog"
	"testing"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"go.uber.org/fx"
)

type lifecycleRecorder struct {
	hooks []fx.Hook
}

func (r *lifecycleRecorder) Append(hook fx.Hook) {
	r.hooks = append(r.hooks, hook)
}

func TestRegisterRecoveryLifecycle_OnStartCallsRecoverOnStartup(t *testing.T) {
	t.Parallel()

	store := &stubHookReviewStore{
		recoverOnStartupFunc: func(ctx context.Context) ([]mcp.PendingHookReview, error) {
			if ctx == nil {
				t.Fatal("RecoverOnStartup() ctx = nil, want non-nil")
			}
			return []mcp.PendingHookReview{{HookCallID: "call-1"}}, nil
		},
	}
	resolver := mustNewHookResolver(t, store)
	registry := NewHookRegistry()
	manager := mustNewManager(t, registry, mustNewHookDispatcher(t, registry, stubPeerCallback{}), resolver)
	recorder := &lifecycleRecorder{}

	registerRecoveryLifecycle(recorder, recoveryLifecycleIn{
		Resolver: resolver,
		Manager:  manager,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	if len(recorder.hooks) != 1 {
		t.Fatalf("registered hooks = %d, want 1", len(recorder.hooks))
	}
	if recorder.hooks[0].OnStart == nil {
		t.Fatal("OnStart = nil, want recovery hook")
	}
	if err := recorder.hooks[0].OnStart(context.Background()); err != nil {
		t.Fatalf("OnStart() error = %v", err)
	}
	if store.recoverCalls != 1 {
		t.Fatalf("RecoverOnStartup() calls = %d, want 1", store.recoverCalls)
	}
}
