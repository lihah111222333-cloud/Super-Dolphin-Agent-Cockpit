package app

import (
	"context"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/module/thread"
)

func TestNoopThreadOrchestrationFacadeDoesNotBlockDesktopThreadLifecycle(t *testing.T) {
	facade := newThreadOrchestrationFacade(threadOrchestrationParams{})
	if facade == nil {
		t.Fatal("newThreadOrchestrationFacade() returned nil")
	}

	ctx := context.Background()
	if err := facade.LaunchAgent(ctx, thread.LaunchAgentRequest{AgentID: "agent-1"}); err != nil {
		t.Fatalf("LaunchAgent() error = %v, want nil", err)
	}
	if err := facade.StopAgent(ctx, "agent-1"); err != nil {
		t.Fatalf("StopAgent() error = %v, want nil", err)
	}
	if err := facade.Recover(ctx, "agent-1"); err != nil {
		t.Fatalf("Recover() error = %v, want nil", err)
	}
	if err := facade.BindSessionGeneration(ctx, "agent-1", 1); err != nil {
		t.Fatalf("BindSessionGeneration() error = %v, want nil", err)
	}
}
