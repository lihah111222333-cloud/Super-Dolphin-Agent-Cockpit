package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/module/thread"
)

func TestThreadOrchestrationFacadeRequiresService(t *testing.T) {
	facade := newThreadOrchestrationFacade(threadOrchestrationParams{})
	if facade == nil {
		t.Fatal("newThreadOrchestrationFacade() returned nil")
	}

	ctx := context.Background()
	assertMissingOrchestrationService(t, "LaunchAgent", facade.LaunchAgent(ctx, thread.LaunchAgentRequest{AgentID: "agent-1"}))
	assertMissingOrchestrationService(t, "StopAgent", facade.StopAgent(ctx, "agent-1"))
	assertMissingOrchestrationService(t, "Recover", facade.Recover(ctx, "agent-1"))
	assertMissingOrchestrationService(t, "BindSessionGeneration", facade.BindSessionGeneration(ctx, "agent-1", 1))
}

func assertMissingOrchestrationService(t *testing.T, op string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s() error = nil, want missing orchestration service error", op)
	}
	if !errors.Is(err, errOrchestrationServiceUnavailable) {
		t.Fatalf("%s() error = %v, want errOrchestrationServiceUnavailable", op, err)
	}
	if !strings.Contains(err.Error(), "orchestration service") {
		t.Fatalf("%s() error = %q, want orchestration service failure", op, err.Error())
	}
}
