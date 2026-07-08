package app

import (
	"context"
	"errors"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestDashboardOrchestrationReaderUsesNarrowPort(t *testing.T) {
	port := &recordingDashboardReaderPort{}
	reader := newDashboardOrchestrationReader(dashboardOrchestrationReaderParams{Reader: port})
	if reader == nil {
		t.Fatal("newDashboardOrchestrationReader() = nil, want reader")
	}

	agents, err := reader.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(agents) != 1 || agents[0].AgentID != "agent-1" {
		t.Fatalf("ListAgents() = %#v, want agent-1", agents)
	}

	snapshot, err := reader.Snapshot(context.Background(), "agent-2")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.AgentID != "agent-2" {
		t.Fatalf("Snapshot() = %#v, want agent-2", snapshot)
	}

	report, err := reader.GetReport(context.Background(), "agent-3")
	if err != nil {
		t.Fatalf("GetReport() error = %v", err)
	}
	if report.AgentID != "agent-3" {
		t.Fatalf("GetReport() = %#v, want agent-3", report)
	}

	wantCalls := []string{"ListAgents", "Snapshot:agent-2", "GetReport:agent-3"}
	if !stringSlicesEqual(port.calls, wantCalls) {
		t.Fatalf("calls = %#v, want %#v", port.calls, wantCalls)
	}
}

func TestDashboardOrchestrationReaderAllowsMissingPort(t *testing.T) {
	if got := newDashboardOrchestrationReader(dashboardOrchestrationReaderParams{}); got != nil {
		t.Fatalf("newDashboardOrchestrationReader() = %T, want nil", got)
	}
}

func TestUIStateAgentListerUsesNarrowReaderPort(t *testing.T) {
	port := &recordingDashboardReaderPort{}
	lister := provideUIStateAgentLister(uiStateAgentListerParams{Reader: port})
	if lister == nil {
		t.Fatal("provideUIStateAgentLister() = nil, want lister")
	}

	agents, err := lister.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if len(agents) != 1 || agents[0].AgentID != "agent-1" {
		t.Fatalf("ListAgents() = %#v, want agent-1", agents)
	}
	if !stringSlicesEqual(port.calls, []string{"ListAgents"}) {
		t.Fatalf("calls = %#v, want ListAgents only", port.calls)
	}
}

func TestRuntimeReporterUsesUpdateRuntimePort(t *testing.T) {
	wantErr := errors.New("update failed")
	updater := &recordingRuntimeUpdater{err: wantErr}
	reporter, err := newRuntimeReporter(runtimeReporterParams{
		Updater:    updater,
		Dependency: contract.DependencyConfig{Profile: contract.DependencyProfileProduction},
	})
	if err != nil {
		t.Fatalf("newRuntimeReporter() error = %v", err)
	}

	report := contract.RuntimeReport{AgentID: "agent-1", Provider: "codex", Port: 7777}
	err = reporter.ReportRuntime(context.Background(), report)
	if !errors.Is(err, wantErr) {
		t.Fatalf("ReportRuntime() error = %v, want %v", err, wantErr)
	}
	if updater.calls != 1 {
		t.Fatalf("UpdateRuntime calls = %d, want 1", updater.calls)
	}
	if updater.report != report {
		t.Fatalf("UpdateRuntime report = %#v, want %#v", updater.report, report)
	}
}

type recordingDashboardReaderPort struct {
	calls []string
}

func (p *recordingDashboardReaderPort) ListAgents(context.Context) ([]contract.AgentSnapshot, error) {
	p.calls = append(p.calls, "ListAgents")
	return []contract.AgentSnapshot{{AgentID: "agent-1"}}, nil
}

func (p *recordingDashboardReaderPort) Snapshot(_ context.Context, agentID string) (contract.AgentSnapshot, error) {
	p.calls = append(p.calls, "Snapshot:"+agentID)
	return contract.AgentSnapshot{AgentID: agentID}, nil
}

func (p *recordingDashboardReaderPort) GetReport(_ context.Context, agentID string) (contract.AgentReportResult, error) {
	p.calls = append(p.calls, "GetReport:"+agentID)
	return contract.AgentReportResult{AgentID: agentID}, nil
}

type recordingRuntimeUpdater struct {
	calls  int
	report contract.RuntimeReport
	err    error
}

func (u *recordingRuntimeUpdater) UpdateRuntime(_ context.Context, report contract.RuntimeReport) error {
	u.calls++
	u.report = report
	return u.err
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
