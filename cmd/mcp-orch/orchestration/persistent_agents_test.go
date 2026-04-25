package orchestration

import (
	"context"
	"errors"
	"testing"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

func TestListAgentsIncludesPersistedAgentIDAndNameWhenRuntimeEmpty(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	svc.agentThreads = fakeAgentThreadStore{threads: []threadstore.Thread{
		{
			ThreadID:  "agent-1",
			AgentID:   "agent-1",
			Name:      "display one",
			Prompt:    "legacy prompt",
			Cwd:       "/repo",
			Status:    "created",
			CreatedAt: 1710000000,
			UpdatedAt: 1710000100,
		},
	}}

	got, err := svc.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListAgents() len = %d, want 1: %#v", len(got), got)
	}
	if got[0].ID != "agent-1" || got[0].AgentID != "agent-1" {
		t.Fatalf("agent identity = id:%q agent_id:%q, want agent-1", got[0].ID, got[0].AgentID)
	}
	if got[0].Name != "display one" {
		t.Fatalf("Name = %q, want persisted display name", got[0].Name)
	}
	if got[0].ThreadID != "agent-1" || got[0].Cwd != "/repo" || got[0].State != agentdto.StateIdle {
		t.Fatalf("snapshot = %#v, want persisted thread projection", got[0])
	}
}

func TestListAgentsOverlaysRuntimeOnPersistedIdentity(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	svc.agentThreads = fakeAgentThreadStore{threads: []threadstore.Thread{
		{ThreadID: "agent-1", AgentID: "agent-1", Name: "renamed", Cwd: "/db", Status: "created", UpdatedAt: 1710000100},
	}}
	now := time.Unix(1710000200, 0)
	svc.agents["agent-1"] = &agentRuntime{
		id:        "agent-1",
		name:      "launch name",
		cwd:       "/runtime",
		state:     agentdto.StateTurnRunning,
		updatedAt: now,
	}

	got, err := svc.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListAgents() len = %d, want 1: %#v", len(got), got)
	}
	if got[0].Name != "renamed" {
		t.Fatalf("Name = %q, want persisted display name to override launch name", got[0].Name)
	}
	if got[0].State != agentdto.StateTurnRunning {
		t.Fatalf("State = %q, want runtime state", got[0].State)
	}
}

func TestGetReportFallsBackByAgentIDOnly(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	svc.agentThreads = fakeAgentThreadStore{threads: []threadstore.Thread{
		{ThreadID: "agent-1", AgentID: "agent-1", Name: "display one", Status: "created"},
	}}

	got, err := svc.GetReport(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("GetReport(agent_id) error = %v", err)
	}
	if got.AgentID != "agent-1" || got.State != agentdto.StateIdle {
		t.Fatalf("GetReport(agent_id) = %#v, want persisted agent identity/state", got)
	}
	if _, err := svc.GetReport(context.Background(), "display one"); !errors.Is(err, errAgentNotFound) {
		t.Fatalf("GetReport(name) error = %v, want agent not found", err)
	}
}

func TestGetReportRejectsRemoteThreadID(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	svc.agents["agent-1"] = &agentRuntime{
		id:             "agent-1",
		remoteThreadID: "provider-thread-1",
		lastReport:     "done",
		state:          agentdto.StateIdle,
	}

	if _, err := svc.GetReport(context.Background(), "provider-thread-1"); !errors.Is(err, errAgentNotFound) {
		t.Fatalf("GetReport(remote_thread_id) error = %v, want agent not found", err)
	}
}

type fakeAgentThreadStore struct {
	threads []threadstore.Thread
	err     error
}

func (s fakeAgentThreadStore) ListAll(context.Context) ([]threadstore.Thread, error) {
	if s.err != nil {
		return nil, s.err
	}
	return append([]threadstore.Thread(nil), s.threads...), nil
}

func (s fakeAgentThreadStore) GetByThreadID(_ context.Context, threadID string) (*threadstore.Thread, error) {
	if s.err != nil {
		return nil, s.err
	}
	for _, thread := range s.threads {
		if thread.ThreadID == threadID {
			found := thread
			return &found, nil
		}
	}
	return nil, errAgentNotFound
}
