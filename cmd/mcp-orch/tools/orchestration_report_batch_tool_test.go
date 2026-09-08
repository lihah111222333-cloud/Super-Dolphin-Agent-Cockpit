package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/testutil/golden"
)

func TestGetAgentReportsHandlerReturnsCompletedSnapshots(t *testing.T) {
	handler := HandleGetAgentReports(&golden.OrchestrationStub{
		GetReportFunc: func(_ context.Context, agentID string) (contract.AgentReportResult, error) {
			return contract.AgentReportResult{
				AgentID:   agentID,
				State:     "idle",
				Report:    "report for " + agentID,
				ReportSeq: 2,
			}, nil
		},
	})

	result, err := handler(context.Background(), mustReportWaitJSON(t, map[string]any{
		"agent_ids": []string{"agent-a", "agent-b"},
	}))
	if err != nil {
		t.Fatalf("HandleGetAgentReports() error = %v", err)
	}
	got := result.(agentReportsResult)
	if got.Status != "completed" || got.Completed != 2 || got.Pending != 0 || got.TimedOut {
		t.Fatalf("HandleGetAgentReports() = %#v, want completed two snapshots", got)
	}
	if len(got.Results) != 2 || got.Results[0].AgentID != "agent-a" || got.Results[1].AgentID != "agent-b" {
		t.Fatalf("HandleGetAgentReports().Results = %#v, want input order", got.Results)
	}
}

func TestGetAgentReportsHandlerWaitsForAllReports(t *testing.T) {
	ready := make(chan struct{})
	handler := HandleGetAgentReports(&golden.OrchestrationStub{
		GetReportFunc: func(_ context.Context, agentID string) (contract.AgentReportResult, error) {
			if agentID == "agent-a" {
				return contract.AgentReportResult{AgentID: agentID, State: "idle", Report: "a done", ReportSeq: 1}, nil
			}
			select {
			case <-ready:
				return contract.AgentReportResult{AgentID: agentID, State: "idle", Report: "b done", ReportSeq: 1}, nil
			default:
				return contract.AgentReportResult{AgentID: agentID, State: "turn_running"}, nil
			}
		},
	})
	time.AfterFunc(75*time.Millisecond, func() { close(ready) })

	started := time.Now()
	result, err := handler(context.Background(), mustReportWaitJSON(t, map[string]any{
		"agent_ids":                 []string{"agent-a", "agent-b"},
		"wait":                      true,
		"after_report_seq_by_agent": map[string]int64{"agent-a": 0, "agent-b": 0},
		"timeout_ms":                500,
	}))
	if err != nil {
		t.Fatalf("HandleGetAgentReports() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed < 50*time.Millisecond {
		t.Fatalf("HandleGetAgentReports() returned after %s, want all-agent wait", elapsed)
	}
	got := result.(agentReportsResult)
	if got.Status != "completed" || got.Completed != 2 || got.Pending != 0 {
		t.Fatalf("HandleGetAgentReports() = %#v, want completed after waiting for all reports", got)
	}
}

func TestGetAgentReportsHandlerKeepsNotFoundItemWithOtherResults(t *testing.T) {
	handler := HandleGetAgentReports(&golden.OrchestrationStub{
		GetReportFunc: func(_ context.Context, agentID string) (contract.AgentReportResult, error) {
			if agentID == "agent-missing" {
				return contract.AgentReportResult{}, fmt.Errorf("lookup %s: %w", agentID, contract.ErrAgentNotFound)
			}
			return contract.AgentReportResult{AgentID: agentID, State: "idle", Report: "done", ReportSeq: 1}, nil
		},
	})

	result, err := handler(context.Background(), mustReportWaitJSON(t, map[string]any{
		"agent_ids":                 []string{"agent-a", "agent-missing"},
		"wait":                      true,
		"after_report_seq_by_agent": map[string]int64{"agent-a": 0, "agent-missing": 0},
	}))
	if err != nil {
		t.Fatalf("HandleGetAgentReports() error = %v", err)
	}
	got := result.(agentReportsResult)
	if got.Status != "partial" || got.Completed != 1 || got.Pending != 0 {
		t.Fatalf("HandleGetAgentReports() = %#v, want partial with one completed item", got)
	}
	missing := got.Results[1]
	if missing.AgentID != "agent-missing" || missing.OK || !strings.Contains(missing.Error, "agent not found") {
		t.Fatalf("missing result = %#v, want not-found error item", missing)
	}
}

func TestGetAgentReportsHandlerRejectsTerminalEmptyReport(t *testing.T) {
	handler := HandleGetAgentReports(&golden.OrchestrationStub{
		GetReportFunc: func(_ context.Context, agentID string) (contract.AgentReportResult, error) {
			return contract.AgentReportResult{AgentID: agentID, State: "stopped"}, nil
		},
	})

	result, err := handler(context.Background(), mustReportWaitJSON(t, map[string]any{
		"agent_ids":                 []string{"agent-stopped"},
		"wait":                      true,
		"after_report_seq_by_agent": map[string]int64{"agent-stopped": 0},
	}))
	if err != nil {
		t.Fatalf("HandleGetAgentReports() error = %v", err)
	}
	got := result.(agentReportsResult)
	if got.Status != "partial" || got.Completed != 0 {
		t.Fatalf("HandleGetAgentReports() = %#v, want partial terminal failure", got)
	}
	item := got.Results[0]
	if item.OK || item.State != "stopped" || item.Report != "" || !strings.Contains(item.Error, "without producing a turn report") {
		t.Fatalf("stopped result = %#v, want terminal error without fabricated report", item)
	}
}

func TestGetAgentReportsHandlerAfterSeqRejectsTerminalEmptyReport(t *testing.T) {
	handler := HandleGetAgentReports(&golden.OrchestrationStub{
		GetReportFunc: func(_ context.Context, agentID string) (contract.AgentReportResult, error) {
			return contract.AgentReportResult{AgentID: agentID, State: "stopped", ReportSeq: 3}, nil
		},
	})

	result, err := handler(context.Background(), mustReportWaitJSON(t, map[string]any{
		"agent_ids": []string{"agent-stopped"},
		"wait":      true,
		"after_report_seq_by_agent": map[string]int64{
			"agent-stopped": 3,
		},
		"timeout_ms": 500,
	}))
	if err != nil {
		t.Fatalf("HandleGetAgentReports() error = %v", err)
	}
	got := result.(agentReportsResult)
	if got.Status != "partial" || got.Completed != 0 || got.Pending != 0 || got.TimedOut {
		t.Fatalf("HandleGetAgentReports() = %#v, want non-timeout partial with no completed item", got)
	}
	item := got.Results[0]
	if item.OK || item.Report != "" || !strings.Contains(item.Error, "without producing a turn report") {
		t.Fatalf("stopped old-seq result = %#v, want explicit terminal error", item)
	}
}

func TestGetAgentReportsHandlerTimeoutReturnsPartial(t *testing.T) {
	handler := HandleGetAgentReports(&golden.OrchestrationStub{
		GetReportFunc: func(_ context.Context, agentID string) (contract.AgentReportResult, error) {
			if agentID == "agent-a" {
				return contract.AgentReportResult{AgentID: agentID, State: "idle", Report: "done", ReportSeq: 1}, nil
			}
			return contract.AgentReportResult{AgentID: agentID, State: "turn_running"}, nil
		},
	})

	result, err := handler(context.Background(), mustReportWaitJSON(t, map[string]any{
		"agent_ids":                 []string{"agent-a", "agent-b"},
		"wait":                      true,
		"after_report_seq_by_agent": map[string]int64{"agent-a": 0, "agent-b": 0},
		"timeout_ms":                80,
	}))
	if err != nil {
		t.Fatalf("HandleGetAgentReports() error = %v", err)
	}
	got := result.(agentReportsResult)
	if got.Status != "partial" || got.Completed != 1 || got.Pending != 1 || !got.TimedOut {
		t.Fatalf("HandleGetAgentReports() = %#v, want partial timeout", got)
	}
	pending := got.Results[1]
	if pending.AgentID != "agent-b" || pending.OK || !strings.Contains(pending.Error, "timed out") {
		t.Fatalf("pending result = %#v, want timed-out item", pending)
	}
}

func TestGetAgentReportsHandlerWaitsForReportAfterSeqByAgent(t *testing.T) {
	reports := newBatchReportStore(map[string]contract.AgentReportResult{
		"agent-a": {AgentID: "agent-a", State: "idle", Report: "old", ReportSeq: 3},
		"agent-b": {AgentID: "agent-b", State: "idle", Report: "ready", ReportSeq: 2},
	})
	handler := HandleGetAgentReports(&golden.OrchestrationStub{GetReportFunc: reports.getReport})
	time.AfterFunc(75*time.Millisecond, func() {
		reports.setReport("agent-a", contract.AgentReportResult{
			AgentID:   "agent-a",
			State:     "idle",
			Report:    "new",
			ReportSeq: 4,
		})
	})

	started := time.Now()
	result, err := handler(context.Background(), mustReportWaitJSON(t, map[string]any{
		"agent_ids": []string{"agent-a", "agent-b"},
		"wait":      true,
		"after_report_seq_by_agent": map[string]int64{
			"agent-a": 3,
			"agent-b": 1,
		},
		"timeout_ms": 500,
	}))
	if err != nil {
		t.Fatalf("HandleGetAgentReports() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed < 50*time.Millisecond {
		t.Fatalf("HandleGetAgentReports() returned after %s, want it to wait for newer report", elapsed)
	}
	got := result.(agentReportsResult)
	if got.Status != "completed" || got.Results[0].Report != "new" || got.Results[0].ReportSeq != 4 {
		t.Fatalf("HandleGetAgentReports() = %#v, want new report after seq", got)
	}
}

type batchReportStore struct {
	mu      sync.Mutex
	reports map[string]contract.AgentReportResult
}

func newBatchReportStore(reports map[string]contract.AgentReportResult) *batchReportStore {
	return &batchReportStore{reports: reports}
}

func (s *batchReportStore) getReport(_ context.Context, agentID string) (contract.AgentReportResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result, ok := s.reports[agentID]
	if !ok {
		return contract.AgentReportResult{AgentID: agentID, State: "turn_running"}, nil
	}
	return result, nil
}

func (s *batchReportStore) setReport(agentID string, report contract.AgentReportResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reports[agentID] = report
}

func TestGetAgentReportsHandlerWaitRequiresEveryCursor(t *testing.T) {
	for _, cursors := range []map[string]int64{nil, {"agent-a": 0}} {
		calls := 0
		handler := HandleGetAgentReports(&golden.OrchestrationStub{GetReportFunc: func(_ context.Context, id string) (contract.AgentReportResult, error) {
			calls++
			return contract.AgentReportResult{AgentID: id, Report: "old", ReportSeq: 3}, nil
		}})
		_, err := handler(context.Background(), mustReportWaitJSON(t, map[string]any{
			"agent_ids": []string{"agent-a", "agent-b"}, "wait": true, "after_report_seq_by_agent": cursors,
		}))
		if err == nil || !strings.Contains(err.Error(), "after_report_seq_by_agent is required for agent") || calls != 0 {
			t.Fatalf("cursors=%v error=%v calls=%d, want missing cursor error before reads", cursors, err, calls)
		}
	}
}

func TestGetAgentReportsRejectsDuplicateNormalizedTargets(t *testing.T) {
	calls := 0
	ports := ToolPorts{AgentReports: &golden.OrchestrationStub{GetReportFunc: func(context.Context, string) (contract.AgentReportResult, error) {
		calls++
		return contract.AgentReportResult{Report: "old", ReportSeq: 3}, nil
	}}}
	registry := newRegistryFromToolDefinitions(newToolRuntimeState(), orchestrationToolDefinitions(ports))
	tool, ok := registry.Lookup("get_agent_reports")
	if !ok {
		t.Fatal("missing get_agent_reports")
	}
	_, err := tool.Handler(context.Background(), json.RawMessage(`{"agent_ids":["a"," a "]}`))
	if err == nil || !strings.Contains(err.Error(), "duplicate normalized agent") || calls != 0 {
		t.Fatalf("error=%v calls=%d, want duplicate rejection before reading", err, calls)
	}
}

func TestGetAgentReportsHandlerRejectsInvalidCursorTargets(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cursors map[string]int64
		want    string
	}{
		{"unexpected", map[string]int64{"agent-a": 0, "extra": 0}, "unrequested agent"},
		{"normalized duplicate", map[string]int64{"agent-a": 0, " agent-a ": 1}, "duplicate normalized agent"},
		{"negative", map[string]int64{"agent-a": -1}, "non-negative"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			handler := HandleGetAgentReports(&golden.OrchestrationStub{GetReportFunc: func(context.Context, string) (contract.AgentReportResult, error) {
				calls++
				return contract.AgentReportResult{Report: "old", ReportSeq: 3}, nil
			}})
			_, err := handler(context.Background(), mustReportWaitJSON(t, map[string]any{"agent_ids": []string{"agent-a"}, "wait": true, "after_report_seq_by_agent": tc.cursors}))
			if err == nil || !strings.Contains(err.Error(), tc.want) || calls != 0 {
				t.Fatalf("error=%v calls=%d, want invalid cursor error before reading", err, calls)
			}
		})
	}
}
