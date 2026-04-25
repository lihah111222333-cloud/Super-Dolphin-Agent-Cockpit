package orchestration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	cwd := t.TempDir()
	mustWritePersistedAgentReportFile(t, cwd, "agent-1", "display one", "结论：持久化\n\n修复：回读")
	svc.agentThreads = fakeAgentThreadStore{threads: []threadstore.Thread{
		{ThreadID: "agent-1", AgentID: "agent-1", Name: "display one", Cwd: cwd, Status: "created"},
	}}

	got, err := svc.GetReport(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("GetReport(agent_id) error = %v", err)
	}
	if got.AgentID != "agent-1" || got.State != agentdto.StateIdle {
		t.Fatalf("GetReport(agent_id) = %#v, want persisted agent identity/state", got)
	}
	if got.Report != "结论：持久化\n\n修复：回读" {
		t.Fatalf("GetReport(agent_id).Report = %q, want persisted report body", got.Report)
	}
	if _, err := svc.GetReport(context.Background(), "display one"); !errors.Is(err, errAgentNotFound) {
		t.Fatalf("GetReport(name) error = %v, want agent not found", err)
	}
}

func TestGetReportErrorsWhenPersistedReportBodyMissing(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	svc.agentThreads = fakeAgentThreadStore{threads: []threadstore.Thread{
		{ThreadID: "agent-1", AgentID: "agent-1", Name: "display one", Cwd: t.TempDir(), Status: "created"},
	}}

	_, err := svc.GetReport(context.Background(), "agent-1")
	if !errors.Is(err, errAgentReportNotFound) {
		t.Fatalf("GetReport(agent_id) error = %v, want persisted report body missing", err)
	}
}

func TestGetReportUsesAgentIDWhenPersistedNameChanges(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	cwd := t.TempDir()
	mustWritePersistedAgentReportFile(t, cwd, "agent-1", "old name", "结论：old filename")
	svc.agentThreads = fakeAgentThreadStore{threads: []threadstore.Thread{
		{ThreadID: "agent-1", AgentID: "agent-1", Name: "new name", Cwd: cwd, Status: "created"},
	}}

	got, err := svc.GetReport(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("GetReport(agent_id) error = %v", err)
	}
	if got.Report != "结论：old filename" {
		t.Fatalf("GetReport(agent_id).Report = %q, want report from agent_id-prefixed file", got.Report)
	}
	if _, err := svc.GetReport(context.Background(), "old name"); !errors.Is(err, errAgentNotFound) {
		t.Fatalf("GetReport(old name) error = %v, want agent not found", err)
	}
}

func TestListAgentsIncludesPersistedReportBody(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	cwd := t.TempDir()
	mustWritePersistedAgentReportFile(t, cwd, "agent-1", "display one", "结论：persisted\nbody")
	svc.agentThreads = fakeAgentThreadStore{threads: []threadstore.Thread{
		{ThreadID: "agent-1", AgentID: "agent-1", Name: "display one", Cwd: cwd, Status: "created"},
	}}

	got, err := svc.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListAgents() len = %d, want 1: %#v", len(got), got)
	}
	if got[0].LastReport != "结论：persisted\nbody" {
		t.Fatalf("LastReport = %q, want persisted report body", got[0].LastReport)
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

func mustWritePersistedAgentReportFile(t *testing.T, cwd, agentID, name, report string) {
	t.Helper()
	path := filepath.Join(cwd, ".agnet", "report", agentID+"+"+name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(persisted report dir) error = %v", err)
	}
	if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
		t.Fatalf("WriteFile(persisted report) error = %v", err)
	}
}
