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
	requireAgentList(t, agents, "agent-1")

	snapshot, err := reader.Snapshot(context.Background(), "agent-2")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	requireAgentSnapshot(t, snapshot, "agent-2")
	requireStringCalls(t, port.calls, []string{"ListAgents", "Snapshot:agent-2"})
}

func TestDashboardOrchestrationReportReaderUsesNarrowPort(t *testing.T) {
	port := &recordingDashboardReaderPort{}
	reader := newDashboardOrchestrationReportReader(dashboardOrchestrationReportReaderParams{Reader: port})
	if reader == nil {
		t.Fatal("newDashboardOrchestrationReportReader() = nil, want reader")
	}

	report, err := reader.GetReport(context.Background(), "agent-3")
	if err != nil {
		t.Fatalf("GetReport() error = %v", err)
	}
	requireAgentReport(t, report, "agent-3")
	requireStringCalls(t, port.calls, []string{"GetReport:agent-3"})
}

func TestDashboardOrchestrationReaderAllowsMissingPort(t *testing.T) {
	if got := newDashboardOrchestrationReader(dashboardOrchestrationReaderParams{}); got != nil {
		t.Fatalf("newDashboardOrchestrationReader() = %T, want nil", got)
	}
	if got := newDashboardOrchestrationReportReader(dashboardOrchestrationReportReaderParams{}); got != nil {
		t.Fatalf("newDashboardOrchestrationReportReader() = %T, want nil", got)
	}
}

func TestDashboardOrchestrationReaderPortComposesNarrowPorts(t *testing.T) {
	state := &recordingAgentStateReader{}
	port := provideDashboardOrchestrationReaderPort(dashboardOrchestrationReaderPortParams{
		State: state,
	})
	if port == nil {
		t.Fatal("provideDashboardOrchestrationReaderPort() = nil, want port")
	}

	agents, err := port.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	requireAgentList(t, agents, "agent-1")

	snapshot, err := port.Snapshot(context.Background(), "agent-2")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	requireAgentSnapshot(t, snapshot, "agent-2")
	requireStringCalls(t, state.calls, []string{"ListAgents", "Snapshot:agent-2"})
}

func TestDashboardOrchestrationReportReaderPortUsesNarrowPort(t *testing.T) {
	reports := &recordingAgentReportPort{}
	port := provideDashboardOrchestrationReportReaderPort(dashboardOrchestrationReportReaderPortParams{
		Reports: reports,
	})
	if port == nil {
		t.Fatal("provideDashboardOrchestrationReportReaderPort() = nil, want port")
	}
	report, err := port.GetReport(context.Background(), "agent-3")
	if err != nil {
		t.Fatalf("GetReport() error = %v", err)
	}
	requireAgentReport(t, report, "agent-3")
	requireStringCalls(t, reports.calls, []string{"GetReport:agent-3"})
}

func TestDashboardOrchestrationReaderPortAllowsMissingNarrowPort(t *testing.T) {
	reports := &recordingAgentReportPort{}
	if got := provideDashboardOrchestrationReaderPort(dashboardOrchestrationReaderPortParams{}); got != nil {
		t.Fatalf("provideDashboardOrchestrationReaderPort() = %T, want nil", got)
	}
	if got := provideDashboardOrchestrationReportReaderPort(dashboardOrchestrationReportReaderPortParams{}); got != nil {
		t.Fatalf("provideDashboardOrchestrationReportReaderPort() = %T, want nil", got)
	}
	if got := provideDashboardOrchestrationReportReaderPort(dashboardOrchestrationReportReaderPortParams{Reports: reports}); got == nil {
		t.Fatal("provideDashboardOrchestrationReportReaderPort() = nil, want port")
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
	requireAgentList(t, agents, "agent-1")
	requireStringCalls(t, port.calls, []string{"ListAgents"})
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

func TestRuntimeUpdaterProviderUsesAgentRuntimePort(t *testing.T) {
	runtimePort := &recordingAgentRuntimePort{}
	updater := provideRuntimeUpdater(runtimeUpdaterParams{Runtime: runtimePort})
	if updater == nil {
		t.Fatal("provideRuntimeUpdater() = nil, want updater")
	}

	report := contract.RuntimeReport{AgentID: "agent-1", Provider: "codex", Port: 7777}
	if err := updater.UpdateRuntime(context.Background(), report); err != nil {
		t.Fatalf("UpdateRuntime() error = %v", err)
	}
	if runtimePort.calls != 1 {
		t.Fatalf("UpdateRuntime calls = %d, want 1", runtimePort.calls)
	}
	if runtimePort.bindCalls != 0 {
		t.Fatalf("BindSessionGeneration calls = %d, want 0", runtimePort.bindCalls)
	}
	if runtimePort.report != report {
		t.Fatalf("UpdateRuntime report = %#v, want %#v", runtimePort.report, report)
	}
}

func TestRuntimeUpdaterProviderAllowsMissingPort(t *testing.T) {
	if got := provideRuntimeUpdater(runtimeUpdaterParams{}); got != nil {
		t.Fatalf("provideRuntimeUpdater() = %T, want nil", got)
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

type recordingAgentStateReader struct {
	calls []string
}

func (p *recordingAgentStateReader) ListAgents(context.Context) ([]contract.AgentSnapshot, error) {
	p.calls = append(p.calls, "ListAgents")
	return []contract.AgentSnapshot{{AgentID: "agent-1"}}, nil
}

func (p *recordingAgentStateReader) Snapshot(_ context.Context, agentID string) (contract.AgentSnapshot, error) {
	p.calls = append(p.calls, "Snapshot:"+agentID)
	return contract.AgentSnapshot{AgentID: agentID}, nil
}

func (p *recordingAgentStateReader) GetState(_ context.Context, agentID string) (contract.AgentStateResult, error) {
	p.calls = append(p.calls, "GetState:"+agentID)
	return contract.AgentStateResult{}, nil
}

type recordingAgentReportPort struct {
	calls []string
}

func (p *recordingAgentReportPort) GetReport(_ context.Context, agentID string) (contract.AgentReportResult, error) {
	p.calls = append(p.calls, "GetReport:"+agentID)
	return contract.AgentReportResult{AgentID: agentID}, nil
}

func (p *recordingAgentReportPort) RememberReportRequest(context.Context, contract.RememberReportRequest) (contract.RememberReportRequestResult, error) {
	p.calls = append(p.calls, "RememberReportRequest")
	return contract.RememberReportRequestResult{}, nil
}

func (p *recordingAgentReportPort) HandleReportEvent(context.Context, contract.ReportEvent) (contract.ReportEventResult, error) {
	p.calls = append(p.calls, "HandleReportEvent")
	return contract.ReportEventResult{}, nil
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

type recordingAgentRuntimePort struct {
	calls     int
	bindCalls int
	report    contract.RuntimeReport
}

func (p *recordingAgentRuntimePort) UpdateRuntime(_ context.Context, report contract.RuntimeReport) error {
	p.calls++
	p.report = report
	return nil
}

func (p *recordingAgentRuntimePort) BindSessionGeneration(context.Context, string, uint64) error {
	p.bindCalls++
	return nil
}

func requireAgentList(t *testing.T, agents []contract.AgentSnapshot, wantAgentID string) {
	t.Helper()
	if len(agents) != 1 {
		t.Fatalf("ListAgents() = %#v, want one agent %q", agents, wantAgentID)
	}
	if agents[0].AgentID != wantAgentID {
		t.Fatalf("ListAgents()[0].AgentID = %q, want %q", agents[0].AgentID, wantAgentID)
	}
}

func requireAgentSnapshot(t *testing.T, snapshot contract.AgentSnapshot, wantAgentID string) {
	t.Helper()
	if snapshot.AgentID != wantAgentID {
		t.Fatalf("Snapshot() = %#v, want agent %q", snapshot, wantAgentID)
	}
}

func requireAgentReport(t *testing.T, report contract.AgentReportResult, wantAgentID string) {
	t.Helper()
	if report.AgentID != wantAgentID {
		t.Fatalf("GetReport() = %#v, want agent %q", report, wantAgentID)
	}
}

func requireStringCalls(t *testing.T, got, want []string) {
	t.Helper()
	if !stringSlicesEqual(got, want) {
		t.Fatalf("calls = %#v, want %#v", got, want)
	}
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
