package thread

import (
	"testing"
	"time"

	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
)

func TestNewThreadStartedEventCarriesAuthoritativeAgentBoard(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 7, 28, 16, 0, 0, 0, time.UTC)
	raw := newThreadEvent(threadEventStartedKind, "thread-1", threadEventFields{State: threadState{
		PublicThreadID: "thread-1",
		AgentID:        "agent-1",
		ParentAgentID:  "agent-root",
		Name:           "bootstrap worker",
		Prompt:         "修复浏览器 bootstrap",
		CreatedAt:      createdAt.UnixMilli(),
		PendingLaunch:  true,
	}})
	event, ok := raw.(threaddto.Started)
	if !ok {
		t.Fatalf("newThreadEvent() = %T, want thread.Started", raw)
	}
	if event.Board == nil {
		t.Fatal("thread.Started board = nil")
	}
	if err := event.Board.Validate(); err != nil {
		t.Fatalf("thread.Started board invalid: %v (%#v)", err, event.Board)
	}
	if event.Board.Progress.Status != string(agentdto.StateProvisioning) {
		t.Fatalf("progress status = %q, want provisioning", event.Board.Progress.Status)
	}
	if event.Board.Assignment == nil || event.Board.Assignment.Description != "修复浏览器 bootstrap" {
		t.Fatalf("assignment = %#v, want authoritative prompt", event.Board.Assignment)
	}
}
