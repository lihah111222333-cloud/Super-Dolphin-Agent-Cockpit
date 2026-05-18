package orchestration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
)

func TestListAgentsIncludesPersistedAgentIDAndNameWhenRuntimeEmpty(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	svc.agentThreads = fakeAgentThreadStore{threads: []PersistedThread{
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
	if got[0].ThreadID != "agent-1" || got[0].Cwd != "/repo" || got[0].State != string(agentdto.StateIdle) {
		t.Fatalf("snapshot = %#v, want persisted thread projection", got[0])
	}
}

func TestListAgentsOverlaysRuntimeOnPersistedIdentity(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	svc.agentThreads = fakeAgentThreadStore{threads: []PersistedThread{
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
	if got[0].State != string(agentdto.StateTurnRunning) {
		t.Fatalf("State = %q, want runtime state", got[0].State)
	}
}

func TestGetReportFallsBackByAgentIDOnly(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	cwd := t.TempDir()
	mustWritePersistedAgentReportFile(t, cwd, "agent-1", "display one", "结论：持久化\n\n修复：回读")
	svc.agentThreads = fakeAgentThreadStore{threads: []PersistedThread{
		{ThreadID: "agent-1", AgentID: "agent-1", Name: "display one", Cwd: cwd, Status: "created"},
	}}

	got, err := svc.GetReport(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("GetReport(agent_id) error = %v", err)
	}
	if got.AgentID != "agent-1" || got.State != string(agentdto.StateIdle) {
		t.Fatalf("GetReport(agent_id) = %#v, want persisted agent identity/state", got)
	}
	if got.Report != "结论：持久化\n\n修复：回读" {
		t.Fatalf("GetReport(agent_id).Report = %q, want persisted report body", got.Report)
	}
	if _, err := svc.GetReport(context.Background(), "display one"); !errors.Is(err, errAgentNotFound) {
		t.Fatalf("GetReport(name) error = %v, want agent not found", err)
	}
}

func TestGetReportFallsBackToStateWhenPersistedReportBodyMissing(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	svc.agentThreads = fakeAgentThreadStore{threads: []PersistedThread{
		{ThreadID: "agent-1", AgentID: "agent-1", Name: "display one", Cwd: t.TempDir(), Status: "created"},
	}}

	got, err := svc.GetReport(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("GetReport(agent_id) error = %v, want a fallback report instead of an error", err)
	}
	if got.AgentID != "agent-1" || got.State != string(agentdto.StateIdle) {
		t.Fatalf("GetReport(agent_id) = %#v, want persisted identity/state", got)
	}
	if !strings.Contains(got.Report, "without producing a turn report") {
		t.Fatalf("GetReport(agent_id).Report = %q, want a no-report fallback text", got.Report)
	}
}

func TestGetReportUsesAgentIDWhenPersistedNameChanges(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	cwd := t.TempDir()
	mustWritePersistedAgentReportFile(t, cwd, "agent-1", "old name", "结论：old filename")
	svc.agentThreads = fakeAgentThreadStore{threads: []PersistedThread{
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
	svc.agentThreads = fakeAgentThreadStore{threads: []PersistedThread{
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

func TestSubmitTurnRehydratesPersistedAgentRuntimeAfterPeerRestart(t *testing.T) {
	launcher := &persistedRuntimeTestLauncher{}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	svc.agentBindings = fakeAgentBindingStore{binding: &PersistedBinding{
		AgentID:       "agent-1",
		Provider:      "codex",
		CodexThreadID: "provider-thread-1",
		Cwd:           "/repo",
		CreatedAt:     1710000000,
		UpdatedAt:     1710000100,
	}}
	svc.agentThreads = fakeAgentThreadStore{threads: []PersistedThread{
		{
			ThreadID:  "provider-thread-1",
			AgentID:   "agent-1",
			Name:      "display one",
			Cwd:       "/repo",
			Status:    "created",
			CreatedAt: 1710000000,
			UpdatedAt: 1710000100,
		},
	}}

	if err := svc.SubmitTurn(context.Background(), TurnSubmission{AgentID: "agent-1"}); err != nil {
		t.Fatalf("SubmitTurn() error = %v", err)
	}
	if launcher.submittedRemoteThreadID != "provider-thread-1" {
		t.Fatalf("submitted remote thread = %q, want provider-thread-1", launcher.submittedRemoteThreadID)
	}
	snapshot, err := svc.Snapshot(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.State != string(agentdto.StateTurnRunning) || snapshot.ActiveTurnID != "turn-1" {
		t.Fatalf("Snapshot() = %#v, want rehydrated running turn", snapshot)
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
	threads []PersistedThread
	err     error
}

func (s fakeAgentThreadStore) ListAll(context.Context) ([]PersistedThread, error) {
	if s.err != nil {
		return nil, s.err
	}
	return append([]PersistedThread(nil), s.threads...), nil
}

func (s fakeAgentThreadStore) GetByThreadID(_ context.Context, threadID string) (*PersistedThread, error) {
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

func (s fakeAgentThreadStore) UpdateStatus(context.Context, PersistedThreadStatusUpdate) error {
	return nil
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

type fakeAgentBindingStore struct {
	binding *PersistedBinding
	err     error
}

func (s fakeAgentBindingStore) GetByAgentID(_ context.Context, agentID string) (*PersistedBinding, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.binding == nil || s.binding.AgentID != agentID {
		return nil, errAgentNotFound
	}
	binding := *s.binding
	return &binding, nil
}

func (s fakeAgentBindingStore) SetArchived(context.Context, PersistedBindingArchiveUpdate) error {
	return nil
}

type persistedRuntimeTestLauncher struct {
	submittedAgentID        string
	submittedRemoteThreadID string
}

func (l *persistedRuntimeTestLauncher) Launch(context.Context, *agentRuntime, LaunchRequest) (LaunchResult, error) {
	return LaunchResult{}, nil
}

func (l *persistedRuntimeTestLauncher) Stop(context.Context, *agentRuntime) error {
	return nil
}

func (l *persistedRuntimeTestLauncher) Archive(context.Context, *agentRuntime) error {
	return nil
}

func (l *persistedRuntimeTestLauncher) SubmitTurn(_ context.Context, agent *agentRuntime, _ TurnSubmission) (string, error) {
	l.submittedAgentID = agent.id
	l.submittedRemoteThreadID = agent.remoteThreadID
	return "turn-1", nil
}

func (l *persistedRuntimeTestLauncher) IsRunning(_ context.Context, agent *agentRuntime) bool {
	return agent != nil && agent.remoteThreadID != ""
}

func (l *persistedRuntimeTestLauncher) SupportsPersistedRuntimeRehydrate() bool {
	return true
}

// TestHandleReportEventPersistsReportWhenRuntimeMissing locks the report
// path for the R3 failure mode: mcp-orch restarts mid-turn and loses the
// in-memory runtime, so the turn-completed event arrives with no runtime
// to lock. HandleReportEvent must still persist the report to disk via
// the thread snapshot, otherwise the completed turn's report is silently
// dropped and the parent agent's get_agent_report degrades to "not found".
func TestHandleReportEventPersistsReportWhenRuntimeMissing(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	cwd := t.TempDir()
	svc.agentThreads = fakeAgentThreadStore{threads: []PersistedThread{
		{ThreadID: "agent-1", AgentID: "agent-1", Name: "display one", Cwd: cwd, Status: "created"},
	}}
	// svc.agents is empty: the in-memory runtime was lost on restart.

	got, err := svc.HandleReportEvent(context.Background(), ReportEvent{
		AgentID:   "agent-1",
		EventType: "turn/completed",
		Report:    "审核已完成，报告已落盘",
	})
	if err != nil {
		t.Fatalf("HandleReportEvent() error = %v, want fallback persist to succeed", err)
	}
	if !got.Success || got.Report != "审核已完成，报告已落盘" {
		t.Fatalf("HandleReportEvent() = %#v, want success carrying the report body", got)
	}
	reread, err := svc.GetReport(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("GetReport() error = %v", err)
	}
	if reread.Report != "审核已完成，报告已落盘" {
		t.Fatalf("GetReport().Report = %q, want the persisted report body", reread.Report)
	}
}
