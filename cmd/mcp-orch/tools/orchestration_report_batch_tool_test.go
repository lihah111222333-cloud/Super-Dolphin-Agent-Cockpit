package tools

import (
	"context"
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
		"agent_ids":  []string{"agent-a", "agent-b"},
		"wait":       true,
		"timeout_ms": 500,
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
		"agent_ids": []string{"agent-a", "agent-missing"},
		"wait":      true,
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

func TestGetAgentReportsHandlerReturnsTerminalFallback(t *testing.T) {
	handler := HandleGetAgentReports(&golden.OrchestrationStub{
		GetReportFunc: func(_ context.Context, agentID string) (contract.AgentReportResult, error) {
			return contract.AgentReportResult{AgentID: agentID, State: "stopped"}, nil
		},
	})

	result, err := handler(context.Background(), mustReportWaitJSON(t, map[string]any{
		"agent_ids": []string{"agent-stopped"},
		"wait":      true,
	}))
	if err != nil {
		t.Fatalf("HandleGetAgentReports() error = %v", err)
	}
	got := result.(agentReportsResult)
	if got.Status != "completed" || got.Completed != 1 {
		t.Fatalf("HandleGetAgentReports() = %#v, want completed fallback", got)
	}
	item := got.Results[0]
	if !item.OK || item.State != "stopped" || !strings.Contains(item.Report, "without producing a turn report") {
		t.Fatalf("stopped result = %#v, want fallback report", item)
	}
}

func TestGetAgentReportsHandlerAfterSeqDoesNotCompleteOldTerminalFallback(t *testing.T) {
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
	if item.OK || item.Report != "" || !strings.Contains(item.Error, "without a report after report_seq 3") {
		t.Fatalf("stopped old-seq result = %#v, want error instead of completed fallback", item)
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
		"agent_ids":  []string{"agent-a", "agent-b"},
		"wait":       true,
		"timeout_ms": 80,
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
