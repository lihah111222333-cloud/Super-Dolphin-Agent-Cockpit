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

func TestTerminalReportEventWithoutBodyWritesFallbackReport(t *testing.T) {
	svc := newTestFacadeServiceWithAgents(&agentRuntime{id: "agent-1", state: agentdto.StateTurnRunning})

	if _, err := svc.HandleReportEvent(context.Background(), ReportEvent{AgentID: "agent-1", EventType: "completion"}); err != nil {
		t.Fatalf("HandleReportEvent() error = %v", err)
	}

	got, err := svc.GetReport(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("GetReport() error = %v", err)
	}
	if got.Report == "" || !strings.Contains(got.Report, "without producing") {
		t.Fatalf("GetReport().Report = %q, want explicit no-report fallback", got.Report)
	}
	if got.ReportSeq != 1 || got.UpdatedAt.IsZero() {
		t.Fatalf("GetReport() seq/updated_at = %d/%v, want fallback write metadata", got.ReportSeq, got.UpdatedAt)
	}
}

func TestReportEventIncrementsReportSeq(t *testing.T) {
	firstAt := time.Date(2026, 6, 18, 9, 0, 0, 0, time.UTC)
	secondAt := firstAt.Add(time.Minute)
	svc := newTestFacadeServiceWithAgents(&agentRuntime{id: "agent-1", state: agentdto.StateTurnRunning})

	first, err := svc.HandleReportEvent(withEventTime(context.Background(), firstAt), ReportEvent{
		AgentID:   "agent-1",
		EventType: "turn/completed",
		Report:    "first report",
	})
	if err != nil {
		t.Fatalf("first HandleReportEvent() error = %v", err)
	}
	second, err := svc.HandleReportEvent(withEventTime(context.Background(), secondAt), ReportEvent{
		AgentID:   "agent-1",
		EventType: "turn/completed",
		Report:    "second report",
	})
	if err != nil {
		t.Fatalf("second HandleReportEvent() error = %v", err)
	}

	got, err := svc.GetReport(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("GetReport() error = %v", err)
	}
	if first.ReportSeq != 1 || second.ReportSeq != 2 || got.ReportSeq != 2 {
		t.Fatalf("report seqs = first:%d second:%d get:%d, want 1/2/2", first.ReportSeq, second.ReportSeq, got.ReportSeq)
	}
	if !got.UpdatedAt.Equal(secondAt) {
		t.Fatalf("GetReport().UpdatedAt = %s, want %s", got.UpdatedAt, secondAt)
	}
}

func TestProcessExitFailureWithoutReportWritesFallbackReport(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.launchSeq = 1
	svc.registry.agents[agent.id] = agent

	svc.handleProcessExit(context.Background(), agent.id, 1, errors.New("process crashed"))

	got, err := svc.GetReport(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("GetReport() error = %v", err)
	}
	if got.State != string(agentdto.StateFailed) || !strings.Contains(got.Report, "process crashed") {
		t.Fatalf("GetReport() = %#v, want failed-state fallback carrying exit error", got)
	}
}

func TestHandleReportEventRunsConservativeReportGC(t *testing.T) {
	cwd := t.TempDir()
	oldTime := time.Now().Add(-31 * 24 * time.Hour)
	recentTime := time.Now().Add(-24 * time.Hour)

	oldStopped := mustWriteReportFileWithModTime(t, cwd, "stopped-agent", "old done", "old stopped", oldTime)
	oldArchived := mustWriteReportFileWithModTime(t, cwd, "archived-agent", "old archived", "old archived", oldTime)
	oldActive := mustWriteReportFileWithModTime(t, cwd, "active-agent", "old active", "old active", oldTime)
	oldOrphan := mustWriteReportFileWithModTime(t, cwd, "orphan-agent", "missing", "old orphan", oldTime)
	recentStopped := mustWriteReportFileWithModTime(t, cwd, "recent-agent", "recent done", "recent stopped", recentTime)

	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	setTestAgentThreads(svc, fakeAgentThreadStore{threads: []PersistedThread{
		{ThreadID: "trigger-agent", AgentID: "trigger-agent", Name: "trigger", Cwd: cwd, Status: "created"},
		{ThreadID: "stopped-agent", AgentID: "stopped-agent", Name: "old done", Cwd: cwd, Status: "stopped"},
		{ThreadID: "archived-agent", AgentID: "archived-agent", Name: "old archived", Cwd: cwd, Status: "archived"},
		{ThreadID: "active-agent", AgentID: "active-agent", Name: "old active", Cwd: cwd, Status: "created"},
		{ThreadID: "recent-agent", AgentID: "recent-agent", Name: "recent done", Cwd: cwd, Status: "stopped"},
	}})
	svc.registry.agents["trigger-agent"] = &agentRuntime{id: "trigger-agent", name: "trigger", cwd: cwd}

	got, err := svc.HandleReportEvent(context.Background(), ReportEvent{
		AgentID:   "trigger-agent",
		EventType: "turn/completed",
		Report:    "new report",
	})
	if err != nil {
		t.Fatalf("HandleReportEvent() error = %v", err)
	}
	if got.Report != "new report" {
		t.Fatalf("HandleReportEvent().Report = %q, want new report", got.Report)
	}

	assertReportFileMissing(t, oldStopped)
	assertReportFileMissing(t, oldArchived)
	assertReportFileExists(t, oldActive)
	assertReportFileExists(t, oldOrphan)
	assertReportFileExists(t, recentStopped)
	assertReportFileExists(t, filepath.Join(cwd, ".agnet", "report", "trigger-agent+trigger"))
}

func mustWriteReportFileWithModTime(t *testing.T, cwd, agentID, name, report string, modTime time.Time) string {
	t.Helper()
	path := filepath.Join(cwd, ".agnet", "report", agentID+"+"+name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(report dir) error = %v", err)
	}
	if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
		t.Fatalf("WriteFile(report) error = %v", err)
	}
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("Chtimes(report) error = %v", err)
	}
	return path
}

func assertReportFileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Stat(%s) error = %v, want file to exist", path, err)
	}
}

func assertReportFileMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Stat(%s) error = %v, want file to be removed", path, err)
	}
}
