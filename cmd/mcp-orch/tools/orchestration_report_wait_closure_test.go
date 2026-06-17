package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/testutil/golden"
)

func TestReportWaitClosureUsesLaunchReturnedAgentID(t *testing.T) {
	reports := newReportWaitClosureStore()
	svc := &golden.OrchestrationStub{
		LaunchAgentSnapshotFunc: func(_ context.Context, req contract.LaunchRequest) (contract.AgentSnapshot, error) {
			if req.AgentID != "agent-requested-report-wait" {
				t.Fatalf("LaunchAgentSnapshot AgentID = %q, want agent-requested-report-wait", req.AgentID)
			}
			return contract.AgentSnapshot{
				ID:       req.AgentID,
				AgentID:  "agent-final-report-wait",
				ThreadID: "thread-report-wait",
				State:    "turn_running",
			}, nil
		},
		GetReportFunc:             reports.getReport,
		RememberReportRequestFunc: reports.remember,
	}

	launchResult, err := handleLaunchAgentWithExeFn(svc, mockExe())(context.Background(), mustReportWaitJSON(t, LaunchAgentInput{
		AgentID:  "agent-requested-report-wait",
		Name:     "report closure",
		Provider: "codex",
		Prompt:   "请完成闭环验证并提交 report",
		CWD:      t.TempDir(),
	}))
	if err != nil {
		t.Fatalf("HandleLaunchAgent() error = %v", err)
	}
	launchMap, ok := launchResult.(map[string]any)
	if !ok {
		t.Fatalf("HandleLaunchAgent() result type = %T, want map[string]any", launchResult)
	}
	agentID, _ := launchMap["agent_id"].(string)
	if agentID != "agent-final-report-wait" {
		t.Fatalf("returned agent_id = %q, want agent-final-report-wait", agentID)
	}

	done := waitForAgentReportAsync(t, svc, agentID, "agent-parent-report-wait")
	waitForRememberedRequester(t, reports, agentID, "agent-parent-report-wait")
	reports.setReport(agentID, "状态: success | blocked | failed\n\n结论:\n- 闭环完成", "idle")

	got := receiveWaitedReport(t, done)
	if got.AgentID != agentID {
		t.Fatalf("waited report AgentID = %q, want %q", got.AgentID, agentID)
	}
	if !strings.Contains(got.Report, "闭环完成") {
		t.Fatalf("waited report = %q, want closure content", got.Report)
	}
}

func TestReportWaitClosureReturnsFallbackForStoppedAgentWithoutReport(t *testing.T) {
	handler := HandleGetAgentReport(&golden.OrchestrationStub{
		GetReportFunc: func(_ context.Context, agentID string) (contract.AgentReportResult, error) {
			return contract.AgentReportResult{AgentID: agentID, State: "stopped"}, nil
		},
	})

	result, err := handler(context.Background(), mustReportWaitJSON(t, map[string]any{
		"agent_id":   "agent-stopped-report-wait",
		"wait":       true,
		"timeout_ms": 500,
	}))
	if err != nil {
		t.Fatalf("HandleGetAgentReport() error = %v", err)
	}
	got, ok := result.(contract.AgentReportResult)
	if !ok {
		t.Fatalf("HandleGetAgentReport() result type = %T, want AgentReportResult", result)
	}
	if got.State != "stopped" {
		t.Fatalf("HandleGetAgentReport().State = %q, want stopped", got.State)
	}
	if !strings.Contains(got.Report, "without producing a turn report") {
		t.Fatalf("HandleGetAgentReport().Report = %q, want stopped fallback", got.Report)
	}
}

type reportWaitClosureStore struct {
	mu         sync.Mutex
	reports    map[string]contract.AgentReportResult
	requesters map[string][]string
	remembered chan contract.RememberReportRequest
}

func newReportWaitClosureStore() *reportWaitClosureStore {
	return &reportWaitClosureStore{
		reports:    map[string]contract.AgentReportResult{},
		requesters: map[string][]string{},
		remembered: make(chan contract.RememberReportRequest, 4),
	}
}

func (s *reportWaitClosureStore) getReport(_ context.Context, agentID string) (contract.AgentReportResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result, ok := s.reports[agentID]
	if !ok {
		return contract.AgentReportResult{AgentID: agentID, State: "turn_running"}, nil
	}
	if result.AgentID == "" {
		result.AgentID = agentID
	}
	if requesters := s.requesters[agentID]; len(requesters) > 0 {
		result.Metadata = &contract.AgentReportMetadata{RequesterIDs: append([]string(nil), requesters...)}
	}
	return result, nil
}

func (s *reportWaitClosureStore) remember(_ context.Context, req contract.RememberReportRequest) (contract.RememberReportRequestResult, error) {
	s.mu.Lock()
	s.requesters[req.AgentID] = append(s.requesters[req.AgentID], req.RequesterID)
	s.mu.Unlock()
	select {
	case s.remembered <- req:
	default:
	}
	return contract.RememberReportRequestResult{Success: true, AgentID: req.AgentID, RequesterID: req.RequesterID}, nil
}

func (s *reportWaitClosureStore) setReport(agentID, report, state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reports[agentID] = contract.AgentReportResult{AgentID: agentID, Report: report, State: state}
}

type reportWaitResult struct {
	report contract.AgentReportResult
	err    error
}

func waitForAgentReportAsync(t *testing.T, svc contract.OrchestrationService, agentID, requesterID string) <-chan reportWaitResult {
	t.Helper()
	done := make(chan reportWaitResult, 1)
	go func() {
		result, err := HandleGetAgentReport(svc)(context.Background(), mustReportWaitJSON(t, map[string]any{
			"agent_id":     agentID,
			"requester_id": requesterID,
			"wait":         true,
			"timeout_ms":   1000,
		}))
		if err != nil {
			done <- reportWaitResult{err: err}
			return
		}
		report, ok := result.(contract.AgentReportResult)
		if !ok {
			done <- reportWaitResult{err: fmt.Errorf("HandleGetAgentReport() result type = %T, want AgentReportResult", result)}
			return
		}
		done <- reportWaitResult{report: report}
	}()
	return done
}

func waitForRememberedRequester(t *testing.T, reports *reportWaitClosureStore, agentID, requesterID string) {
	t.Helper()
	select {
	case got := <-reports.remembered:
		if got.AgentID != agentID || got.RequesterID != requesterID {
			t.Fatalf("RememberReportRequest = %#v, want agent/requester %q/%q", got, agentID, requesterID)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("RememberReportRequest was not called")
	}
}

func receiveWaitedReport(t *testing.T, done <-chan reportWaitResult) contract.AgentReportResult {
	t.Helper()
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("HandleGetAgentReport() error = %v", got.err)
		}
		return got.report
	case <-time.After(2 * time.Second):
		t.Fatalf("HandleGetAgentReport() did not return")
		return contract.AgentReportResult{}
	}
}

func mustReportWaitJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return raw
}
