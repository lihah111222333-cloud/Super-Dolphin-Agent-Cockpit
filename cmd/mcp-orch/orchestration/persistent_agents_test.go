package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

func TestListAgentsIncludesPersistedAgentIDAndNameWhenRuntimeEmpty(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	svc.lifecycle.agentThreads = fakeAgentThreadStore{threads: []PersistedThread{
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
	svc.lifecycle.agentThreads = fakeAgentThreadStore{threads: []PersistedThread{
		{ThreadID: "agent-1", AgentID: "agent-1", Name: "renamed", Cwd: "/db", Status: "created", UpdatedAt: 1710000100},
	}}
	now := time.Unix(1710000200, 0)
	svc.registry.agents["agent-1"] = &agentRuntime{
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

func TestGetReportReadsPersistedReportWhenRuntimeMissingByAgentID(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	cwd := t.TempDir()
	mustWritePersistedAgentReportFile(t, cwd, "agent-1", "display one", "结论：持久化\n\n修复：回读")
	svc.lifecycle.agentThreads = fakeAgentThreadStore{threads: []PersistedThread{
		{ThreadID: "agent-1", AgentID: "agent-1", Name: "display one", Cwd: cwd, Status: "created"},
	}}

	report, err := svc.GetReport(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("GetReport(agent_id) error = %v, want persisted report", err)
	}
	if report.Report != "结论：持久化\n\n修复：回读" || report.State == "" {
		t.Fatalf("GetReport(agent_id) = %#v, want persisted report and state", report)
	}
	if _, err := svc.GetReport(context.Background(), "display one"); !errors.Is(err, errAgentNotFound) {
		t.Fatalf("GetReport(name) error = %v, want agent not found", err)
	}
}

func TestGetReportFailsFastWhenPersistedReportBodyMissing(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	svc.lifecycle.agentThreads = fakeAgentThreadStore{threads: []PersistedThread{
		{ThreadID: "agent-1", AgentID: "agent-1", Name: "display one", Cwd: t.TempDir(), Status: "created"},
	}}

	if _, err := svc.GetReport(context.Background(), "agent-1"); !errors.Is(err, errAgentNotFound) {
		t.Fatalf("GetReport(agent_id) error = %v, want agent not found", err)
	}
}

func TestGetReportFallsBackToListWhenThreadIDLookupReturnsStoreNotFound(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	cwd := t.TempDir()
	mustWritePersistedAgentReportFile(t, cwd, "agent-1", "display one", "结论：persisted")
	svc.lifecycle.agentThreads = fakeAgentThreadStore{
		getErr:  platformdb.ErrNotFound,
		threads: []PersistedThread{{ThreadID: "thread-1", AgentID: "agent-1", Name: "display one", Cwd: cwd, Status: "created"}},
	}

	report, err := svc.GetReport(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("GetReport(agent_id) error = %v, want list fallback", err)
	}
	if report.Report != "结论：persisted" {
		t.Fatalf("GetReport(agent_id).Report = %q, want persisted body", report.Report)
	}
}

func TestGetReportReadsPersistedReportAfterDisplayNameChanged(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	cwd := t.TempDir()
	mustWritePersistedAgentReportFile(t, cwd, "agent-1", "old name", "结论：old filename")
	svc.lifecycle.agentThreads = fakeAgentThreadStore{threads: []PersistedThread{
		{ThreadID: "agent-1", AgentID: "agent-1", Name: "new name", Cwd: cwd, Status: "created"},
	}}

	report, err := svc.GetReport(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("GetReport(agent_id) error = %v, want persisted report selected by agent id prefix", err)
	}
	if report.Report != "结论：old filename" {
		t.Fatalf("GetReport(agent_id).Report = %q, want old filename body", report.Report)
	}
	if _, err := svc.GetReport(context.Background(), "old name"); !errors.Is(err, errAgentNotFound) {
		t.Fatalf("GetReport(old name) error = %v, want agent not found", err)
	}
}

func TestListAgentsOmitsPersistedReportBody(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	cwd := t.TempDir()
	mustWritePersistedAgentReportFile(t, cwd, "agent-1", "display one", "结论：persisted\nbody")
	svc.lifecycle.agentThreads = fakeAgentThreadStore{threads: []PersistedThread{
		{ThreadID: "agent-1", AgentID: "agent-1", Name: "display one", Cwd: cwd, Status: "created"},
	}}

	got, err := svc.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListAgents() len = %d, want 1: %#v", len(got), got)
	}
	if got[0].LastReport != "" {
		t.Fatalf("LastReport = %q, want omitted", got[0].LastReport)
	}
}

func TestListAgentsNormalizesPersistedMillisecondTimestampsForJSON(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	svc.lifecycle.agentThreads = fakeAgentThreadStore{threads: []PersistedThread{
		{
			ThreadID:  "agent-1",
			AgentID:   "agent-1",
			Name:      "display one",
			Cwd:       "/repo",
			Status:    "created",
			CreatedAt: 1710000000123,
			UpdatedAt: 1710000100456,
		},
	}}

	got, err := svc.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if _, err := json.Marshal(got); err != nil {
		t.Fatalf("json.Marshal(ListAgents()) error = %v", err)
	}
	if got[0].CreatedAt.Year() != 2024 || got[0].UpdatedAt.Year() != 2024 {
		t.Fatalf("timestamps = created:%v updated:%v, want normalized 2024 times", got[0].CreatedAt, got[0].UpdatedAt)
	}
	if got[0].CreatedAt.Nanosecond() == 0 || got[0].UpdatedAt.Nanosecond() == 0 {
		t.Fatalf("timestamps lost millisecond precision: created:%v updated:%v", got[0].CreatedAt, got[0].UpdatedAt)
	}
}

func TestListAgentsSortsNewestCreatedFirst(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	svc.lifecycle.agentThreads = fakeAgentThreadStore{threads: []PersistedThread{
		{ThreadID: "thread-parent", AgentID: "agent-parent", Name: "parent", Status: "created", CreatedAt: 1710000000, UpdatedAt: 1710000000},
		{ThreadID: "thread-child", AgentID: "agent-child", Name: "child", Status: "created", CreatedAt: 1710000100, UpdatedAt: 1710000100},
	}}

	got, err := svc.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListAgents() len = %d, want 2", len(got))
	}
	if got[0].AgentID != "agent-child" || got[1].AgentID != "agent-parent" {
		t.Fatalf("agent order = [%s %s], want child before parent by created_at desc", got[0].AgentID, got[1].AgentID)
	}
}

func TestListAgentsHonorsContextWhileRuntimeLockHeld(t *testing.T) {
	t.Parallel()
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	svc.registry.mu.Lock()
	defer svc.registry.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() { _, err := svc.ListAgents(ctx); done <- err })
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("ListAgents() error = %v, want context deadline exceeded", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("ListAgents() did not return after context deadline while runtime lock was held")
	}
}

func TestSubmitTurnRehydratesPersistedAgentRuntimeAfterPeerRestart(t *testing.T) {
	launcher := &persistedRuntimeTestLauncher{}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	svc.lifecycle.agentBindings = fakeAgentBindingStore{binding: &PersistedBinding{
		AgentID:       "agent-1",
		Provider:      "codex",
		CodexThreadID: "provider-thread-1",
		Cwd:           "/repo",
		CreatedAt:     1710000000,
		UpdatedAt:     1710000100,
	}}
	svc.lifecycle.agentThreads = fakeAgentThreadStore{threads: []PersistedThread{
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

func TestSubmitTurnRehydratesPersistedReportSeq(t *testing.T) {
	launcher := &persistedRuntimeTestLauncher{}
	svc := NewService(silentLogger(), nil, launcher, nil, nil, nil)
	cwd := t.TempDir()
	mustWriteVersionedPersistedAgentReportFile(t, cwd, "agent-1", "display one", 7, "old report")
	svc.lifecycle.agentBindings = fakeAgentBindingStore{binding: &PersistedBinding{
		AgentID:       "agent-1",
		Provider:      "codex",
		CodexThreadID: "provider-thread-1",
		Cwd:           cwd,
		CreatedAt:     1710000000,
		UpdatedAt:     1710000100,
	}}
	svc.lifecycle.agentThreads = fakeAgentThreadStore{threads: []PersistedThread{
		{
			ThreadID:  "provider-thread-1",
			AgentID:   "agent-1",
			Name:      "display one",
			Cwd:       cwd,
			Status:    "created",
			CreatedAt: 1710000000,
			UpdatedAt: 1710000100,
		},
	}}

	if err := svc.SubmitTurn(context.Background(), TurnSubmission{AgentID: "agent-1"}); err != nil {
		t.Fatalf("SubmitTurn() error = %v", err)
	}
	before, err := svc.GetReport(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("GetReport() before update error = %v", err)
	}
	if before.ReportSeq != 7 || before.Report != "old report" {
		t.Fatalf("GetReport() before update = %#v, want persisted seq 7", before)
	}
	after, err := svc.HandleReportEvent(context.Background(), ReportEvent{
		AgentID:   "agent-1",
		EventType: "turn/completed",
		Report:    "new report",
	})
	if err != nil {
		t.Fatalf("HandleReportEvent() error = %v", err)
	}
	if after.ReportSeq != 8 {
		t.Fatalf("HandleReportEvent().ReportSeq = %d, want 8 after rehydrate", after.ReportSeq)
	}
}

func TestGetReportRejectsRemoteThreadID(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	svc.registry.agents["agent-1"] = &agentRuntime{
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
	getErr  error
}

func (s fakeAgentThreadStore) ListAll(context.Context) ([]PersistedThread, error) {
	if s.err != nil {
		return nil, s.err
	}
	return append([]PersistedThread(nil), s.threads...), nil
}

func (s fakeAgentThreadStore) GetByThreadID(_ context.Context, threadID string) (*PersistedThread, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
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

type recordingAgentThreadStore struct {
	threads []PersistedThread
	update  PersistedThreadStatusUpdate
	updates int
}

func (s *recordingAgentThreadStore) ListAll(context.Context) ([]PersistedThread, error) {
	return append([]PersistedThread(nil), s.threads...), nil
}

func (s *recordingAgentThreadStore) GetByThreadID(_ context.Context, threadID string) (*PersistedThread, error) {
	for _, thread := range s.threads {
		if thread.ThreadID == threadID {
			found := thread
			return &found, nil
		}
	}
	return nil, errAgentNotFound
}

func (s *recordingAgentThreadStore) UpdateStatus(_ context.Context, update PersistedThreadStatusUpdate) error {
	s.update = update
	s.updates++
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

func mustWriteVersionedPersistedAgentReportFile(t *testing.T, cwd, agentID, name string, seq int64, report string) {
	t.Helper()
	body := fmt.Sprintf("---\nreport_seq: %d\nupdated_at: \"2026-06-18T10:30:00Z\"\n---\n\n%s", seq, report)
	mustWritePersistedAgentReportFile(t, cwd, agentID, name, body)
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

func (l *persistedRuntimeTestLauncher) Fork(context.Context, *agentRuntime, *agentRuntime, LaunchRequest) (LaunchResult, error) {
	return LaunchResult{}, errors.New("fork should not be called")
}

func (l *persistedRuntimeTestLauncher) Stop(context.Context, *agentRuntime) error {
	return nil
}

func (l *persistedRuntimeTestLauncher) Archive(context.Context, *agentRuntime) error {
	return nil
}

func (l *persistedRuntimeTestLauncher) Interrupt(context.Context, *agentRuntime, string) error {
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
	svc.lifecycle.agentThreads = fakeAgentThreadStore{threads: []PersistedThread{
		{ThreadID: "agent-1", AgentID: "agent-1", Name: "display one", Cwd: cwd, Status: "created"},
	}}
	// svc.registry.agents is empty: the in-memory runtime was lost on restart.

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
		t.Fatalf("GetReport() error = %v, want persisted report", err)
	}
	if reread.Report != "审核已完成，报告已落盘" {
		t.Fatalf("GetReport().Report = %q, want persisted report body", reread.Report)
	}
}

func TestHandleReportEventStopsPersistedThreadWhenAbortedRuntimeMissing(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	cwd := t.TempDir()
	threads := &recordingAgentThreadStore{threads: []PersistedThread{
		{ThreadID: "agent-1", AgentID: "agent-1", Name: "display one", Cwd: cwd, Status: "created"},
	}}
	svc.lifecycle.agentThreads = threads
	// svc.registry.agents is empty: the runtime was lost before the abort event arrived.

	got, err := svc.HandleReportEvent(context.Background(), ReportEvent{
		AgentID:   "agent-1",
		EventType: "turn_aborted",
		Report:    "aborted by user",
	})
	if err != nil {
		t.Fatalf("HandleReportEvent() error = %v, want fallback persist to succeed", err)
	}
	if !got.Success || got.Report != "aborted by user" {
		t.Fatalf("HandleReportEvent() = %#v, want success carrying the abort report", got)
	}
	if threads.updates != 1 {
		t.Fatalf("UpdateStatus calls = %d, want 1", threads.updates)
	}
	if threads.update.ThreadID != "agent-1" || threads.update.Status != "stopped" || threads.update.UpdatedAt == 0 {
		t.Fatalf("UpdateStatus = %#v, want agent-1 stopped with timestamp", threads.update)
	}
}

func TestHandleReportEventStopsPersistedThreadWhenRuntimeLossStopEventHasNoReport(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	threads := &recordingAgentThreadStore{threads: []PersistedThread{
		{ThreadID: "agent-1", AgentID: "agent-1", Name: "display one", Status: "created"},
	}}
	svc.lifecycle.agentThreads = threads
	// connection.dead often arrives without a final report body; it still means
	// the persisted UI thread must stop when the runtime is already gone.

	got, err := svc.HandleReportEvent(context.Background(), ReportEvent{
		AgentID:   "agent-1",
		EventType: "connection.dead",
		EventData: json.RawMessage(`{"error":"connection lost"}`),
	})
	if err != nil {
		t.Fatalf("HandleReportEvent() error = %v, want stop fallback to succeed", err)
	}
	if !got.Success || got.Report != "" {
		t.Fatalf("HandleReportEvent() = %#v, want success with empty report", got)
	}
	if threads.updates != 1 {
		t.Fatalf("UpdateStatus calls = %d, want 1", threads.updates)
	}
	if threads.update.ThreadID != "agent-1" || threads.update.Status != "stopped" || threads.update.UpdatedAt == 0 {
		t.Fatalf("UpdateStatus = %#v, want agent-1 stopped with timestamp", threads.update)
	}
}

func TestHandleReportEventDoesNotStopPersistedThreadWhenCompletedRuntimeMissing(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	cwd := t.TempDir()
	threads := &recordingAgentThreadStore{threads: []PersistedThread{
		{ThreadID: "agent-1", AgentID: "agent-1", Name: "display one", Cwd: cwd, Status: "created"},
	}}
	svc.lifecycle.agentThreads = threads
	// A completed turn can leave a reusable session idle; do not collapse it to stopped.

	got, err := svc.HandleReportEvent(context.Background(), ReportEvent{
		AgentID:   "agent-1",
		EventType: "turn/completed",
		Report:    "normal reply",
	})
	if err != nil {
		t.Fatalf("HandleReportEvent() error = %v, want fallback persist to succeed", err)
	}
	if !got.Success || got.Report != "normal reply" {
		t.Fatalf("HandleReportEvent() = %#v, want success carrying the report", got)
	}
	if threads.updates != 0 {
		t.Fatalf("UpdateStatus calls = %d, want 0 for normal turn completion", threads.updates)
	}
}
