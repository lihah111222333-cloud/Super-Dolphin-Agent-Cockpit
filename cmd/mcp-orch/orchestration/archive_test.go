package orchestration

import (
	"context"
	"testing"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
)

func TestArchiveAgentMarksPersistedThreadAndBindingArchivedWhenRuntimeMissing(t *testing.T) {
	threads := &archiveAgentThreadStore{threads: []PersistedThread{
		{ThreadID: "provider-thread-1", AgentID: "agent-1", Status: "created"},
	}}
	bindings := &archiveAgentBindingStore{bindings: map[string]PersistedBinding{
		"agent-1": {AgentID: "agent-1", Provider: "codex", CodexThreadID: "provider-thread-1"},
	}}
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	svc.agentThreads = threads
	svc.agentBindings = bindings

	if err := svc.ArchiveAgent(context.Background(), " agent-1 "); err != nil {
		t.Fatalf("ArchiveAgent() error = %v", err)
	}
	if threads.updated.ThreadID != "provider-thread-1" || threads.updated.Status != "archived" {
		t.Fatalf("thread status update = %#v, want provider-thread-1 archived", threads.updated)
	}
	if bindings.archived.AgentID != "agent-1" || !bindings.archived.Archived {
		t.Fatalf("binding archive update = %#v, want agent-1 archived", bindings.archived)
	}
}

func TestArchiveAgentAcceptsProviderThreadIDAndArchivesOwningAgent(t *testing.T) {
	threads := &archiveAgentThreadStore{threads: []PersistedThread{
		{ThreadID: "provider-thread-1", AgentID: "agent-1", Status: "created"},
	}}
	bindings := &archiveAgentBindingStore{bindings: map[string]PersistedBinding{
		"agent-1": {AgentID: "agent-1", Provider: "codex", CodexThreadID: "provider-thread-1"},
	}}
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	svc.agentThreads = threads
	svc.agentBindings = bindings

	if err := svc.ArchiveAgent(context.Background(), "provider-thread-1"); err != nil {
		t.Fatalf("ArchiveAgent(provider thread) error = %v", err)
	}
	if threads.updated.ThreadID != "provider-thread-1" || threads.updated.Status != "archived" {
		t.Fatalf("thread status update = %#v, want provider-thread-1 archived", threads.updated)
	}
	if bindings.archived.AgentID != "agent-1" || !bindings.archived.Archived {
		t.Fatalf("binding archive update = %#v, want owning agent archived", bindings.archived)
	}
}

func TestArchiveAgentStopsOwningRuntimeWhenCalledWithProviderThreadID(t *testing.T) {
	threads := &archiveAgentThreadStore{threads: []PersistedThread{
		{ThreadID: "provider-thread-1", AgentID: "agent-1", Status: "created"},
	}}
	bindings := &archiveAgentBindingStore{bindings: map[string]PersistedBinding{
		"agent-1": {AgentID: "agent-1", Provider: "codex", CodexThreadID: "provider-thread-1"},
	}}
	launcher := &archiveAgentLauncher{}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	svc.agentThreads = threads
	svc.agentBindings = bindings
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateIdle
	agent.remoteThreadID = "provider-thread-1"
	agent.launchSeq = 1
	svc.agents[agent.id] = agent

	if err := svc.ArchiveAgent(context.Background(), "provider-thread-1"); err != nil {
		t.Fatalf("ArchiveAgent(provider thread) error = %v", err)
	}
	if launcher.stoppedAgentID != "agent-1" {
		t.Fatalf("stopped agent = %q, want owning agent-1", launcher.stoppedAgentID)
	}
	if bindings.archived.AgentID != "agent-1" || !bindings.archived.Archived {
		t.Fatalf("binding archive update = %#v, want owning agent archived", bindings.archived)
	}
}

type archiveAgentThreadStore struct {
	threads []PersistedThread
	updated PersistedThreadStatusUpdate
}

func (s *archiveAgentThreadStore) ListAll(context.Context) ([]PersistedThread, error) {
	return append([]PersistedThread(nil), s.threads...), nil
}

func (s *archiveAgentThreadStore) GetByThreadID(_ context.Context, threadID string) (*PersistedThread, error) {
	for _, thread := range s.threads {
		if thread.ThreadID == threadID {
			found := thread
			return &found, nil
		}
	}
	return nil, errAgentNotFound
}

func (s *archiveAgentThreadStore) UpdateStatus(_ context.Context, params PersistedThreadStatusUpdate) error {
	s.updated = params
	return nil
}

type archiveAgentBindingStore struct {
	bindings map[string]PersistedBinding
	archived PersistedBindingArchiveUpdate
}

func (s *archiveAgentBindingStore) GetByAgentID(_ context.Context, agentID string) (*PersistedBinding, error) {
	binding, ok := s.bindings[agentID]
	if !ok {
		return nil, errAgentNotFound
	}
	return &binding, nil
}

func (s *archiveAgentBindingStore) SetArchived(_ context.Context, params PersistedBindingArchiveUpdate) error {
	s.archived = params
	return nil
}

type archiveAgentLauncher struct {
	stoppedAgentID string
}

func (l *archiveAgentLauncher) Launch(context.Context, *agentRuntime, LaunchRequest) (LaunchResult, error) {
	return LaunchResult{}, nil
}

func (l *archiveAgentLauncher) Stop(_ context.Context, agent *agentRuntime) error {
	if agent != nil {
		l.stoppedAgentID = agent.id
	}
	return nil
}

func (l *archiveAgentLauncher) SubmitTurn(context.Context, *agentRuntime, TurnSubmission) (string, error) {
	return "", nil
}

func (l *archiveAgentLauncher) IsRunning(_ context.Context, agent *agentRuntime) bool {
	return agent != nil && agent.remoteThreadID != ""
}
