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

func TestSendMessageWaitReportReturnsNewReportAfterSubmit(t *testing.T) {
	reports := newSendMessageReportStore(contract.AgentReportResult{
		AgentID:   "agent-b",
		State:     "idle",
		Report:    "old report",
		ReportSeq: 3,
	})
	handler := handleSendMessageWithStub(&golden.OrchestrationStub{
		SnapshotFunc: func(_ context.Context, agentID string) (contract.AgentSnapshot, error) {
			return contract.AgentSnapshot{AgentID: agentID, ThreadID: "thread-b", State: "idle"}, nil
		},
		GetReportFunc: reports.getReport,
		SubmitTurnFunc: func(_ context.Context, req contract.TurnSubmission) error {
			if req.AgentID != "agent-b" || req.ThreadID != "thread-b" {
				t.Fatalf("SubmitTurn() req = %#v, want agent-b/thread-b", req)
			}
			reports.setReport(contract.AgentReportResult{
				AgentID:   "agent-b",
				State:     "idle",
				Report:    "new report",
				ReportSeq: 4,
			})
			return nil
		},
	})

	result, err := handler(context.Background(), json.RawMessage(`{
		"agent_id": "agent-b",
		"message": "请补充证据",
		"wait_report": true,
		"timeout_ms": 1000
	}`))
	if err != nil {
		t.Fatalf("HandleSendMessage() error = %v", err)
	}
	got := result.(map[string]any)
	report, ok := got["report"].(contract.AgentReportResult)
	if !ok {
		t.Fatalf("HandleSendMessage() report type = %T, want AgentReportResult; result=%#v", got["report"], got)
	}
	if got["agent_id"] != "agent-b" || got["submitted"] != true || got["previous_report_seq"] != int64(3) {
		t.Fatalf("HandleSendMessage() = %#v, want submitted result with previous seq", got)
	}
	if report.ReportSeq != 4 || report.Report != "new report" {
		t.Fatalf("HandleSendMessage() report = %#v, want new report seq 4", report)
	}
}

func TestSendMessageWaitReportRequiresIdleAgent(t *testing.T) {
	states := []string{
		"provisioning",
		"turn_queued",
		"turn_starting",
		"turn_running",
		"awaiting_user_input",
		"recovering",
		"stopping",
		"stopped",
		"failed",
	}
	for _, state := range states {
		t.Run(state, func(t *testing.T) {
			handler := handleSendMessageWithStub(&golden.OrchestrationStub{
				SnapshotFunc: func(_ context.Context, agentID string) (contract.AgentSnapshot, error) {
					return contract.AgentSnapshot{AgentID: agentID, State: state}, nil
				},
				GetReportFunc: func(_ context.Context, agentID string) (contract.AgentReportResult, error) {
					return contract.AgentReportResult{AgentID: agentID, State: state, Report: "old", ReportSeq: 3}, nil
				},
				SubmitTurnFunc: func(context.Context, contract.TurnSubmission) error {
					t.Fatal("SubmitTurn must not be called for non-idle wait_report follow-up")
					return nil
				},
			})

			_, err := handler(context.Background(), json.RawMessage(`{
				"agent_id": "agent-b",
				"message": "请补充证据",
				"wait_report": true
			}`))
			if err == nil || !strings.Contains(err.Error(), state) || !strings.Contains(err.Error(), "idle") {
				t.Fatalf("HandleSendMessage() error = %v, want fail-fast idle-state error", err)
			}
		})
	}
}

func TestSendMessageWaitFalseKeepsOriginalSubmissionBehavior(t *testing.T) {
	var got contract.TurnSubmission
	handler := handleSendMessageWithStub(&golden.OrchestrationStub{
		SnapshotFunc: func(_ context.Context, agentID string) (contract.AgentSnapshot, error) {
			return contract.AgentSnapshot{AgentID: agentID, ThreadID: "thread-running", State: "turn_running"}, nil
		},
		GetReportFunc: func(context.Context, string) (contract.AgentReportResult, error) {
			t.Fatal("GetReport must not be called when wait_report=false")
			return contract.AgentReportResult{}, nil
		},
		SubmitTurnFunc: func(_ context.Context, req contract.TurnSubmission) error {
			got = req
			return nil
		},
	})

	result, err := handler(context.Background(), json.RawMessage(`{
		"agent_id": "agent-running",
		"message": "hello",
		"wait_report": false,
		"timeout_ms": 1
	}`))
	if err != nil {
		t.Fatalf("HandleSendMessage() error = %v", err)
	}
	out := result.(map[string]any)
	if out["success"] != true || out["agent_id"] != "agent-running" || out["submitted"] != nil {
		t.Fatalf("HandleSendMessage() = %#v, want original success envelope", out)
	}
	if got.AgentID != "agent-running" || got.ThreadID != "thread-running" || len(got.Inputs) != 1 {
		t.Fatalf("SubmitTurn() req = %#v, want original submission behavior", got)
	}
}

func TestSendMessageWaitReportSubmitFailureDoesNotWait(t *testing.T) {
	getReportCalls := 0
	handler := handleSendMessageWithStub(&golden.OrchestrationStub{
		SnapshotFunc: func(_ context.Context, agentID string) (contract.AgentSnapshot, error) {
			return contract.AgentSnapshot{AgentID: agentID, State: "idle"}, nil
		},
		GetReportFunc: func(_ context.Context, agentID string) (contract.AgentReportResult, error) {
			getReportCalls++
			return contract.AgentReportResult{AgentID: agentID, State: "idle", Report: "old", ReportSeq: 3}, nil
		},
		SubmitTurnFunc: func(context.Context, contract.TurnSubmission) error {
			return fmt.Errorf("submit down")
		},
	})

	_, err := handler(context.Background(), json.RawMessage(`{
		"agent_id": "agent-b",
		"message": "请补充证据",
		"wait_report": true,
		"timeout_ms": 1000
	}`))
	if err == nil || !strings.Contains(err.Error(), "submit down") {
		t.Fatalf("HandleSendMessage() error = %v, want submit error", err)
	}
	if getReportCalls != 1 {
		t.Fatalf("GetReport calls = %d, want one pre-submit read only", getReportCalls)
	}
}

func TestSendMessageWaitReportTimeoutCoversSubmitTurn(t *testing.T) {
	handler := handleSendMessageWithStub(&golden.OrchestrationStub{
		SnapshotFunc: func(_ context.Context, agentID string) (contract.AgentSnapshot, error) {
			return contract.AgentSnapshot{AgentID: agentID, State: "idle"}, nil
		},
		GetReportFunc: func(_ context.Context, agentID string) (contract.AgentReportResult, error) {
			return contract.AgentReportResult{AgentID: agentID, State: "idle", Report: "old", ReportSeq: 3}, nil
		},
		SubmitTurnFunc: func(ctx context.Context, _ contract.TurnSubmission) error {
			<-ctx.Done()
			return ctx.Err()
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		_, err := handler(ctx, json.RawMessage(`{
			"agent_id": "agent-b",
			"message": "follow-up",
			"wait_report": true,
			"timeout_ms": 25
		}`))
		errCh <- err
	})

	select {
	case err := <-errCh:
		if err == nil ||
			!strings.Contains(err.Error(), `agent "agent-b"`) ||
			!strings.Contains(err.Error(), "submit follow-up turn") ||
			!strings.Contains(err.Error(), "timed out") {
			t.Fatalf("HandleSendMessage() error = %v, want submit timeout with agent context", err)
		}
	case <-time.After(150 * time.Millisecond):
		cancel()
		t.Fatal("HandleSendMessage() did not respect timeout_ms while SubmitTurn was blocked")
	}
}

func TestSendMessageWaitReportTimeoutMentionsAgentAndSeq(t *testing.T) {
	handler := handleSendMessageWithStub(&golden.OrchestrationStub{
		SnapshotFunc: func(_ context.Context, agentID string) (contract.AgentSnapshot, error) {
			return contract.AgentSnapshot{AgentID: agentID, State: "idle"}, nil
		},
		GetReportFunc: func(_ context.Context, agentID string) (contract.AgentReportResult, error) {
			return contract.AgentReportResult{AgentID: agentID, State: "idle", Report: "old", ReportSeq: 3}, nil
		},
		SubmitTurnFunc: func(context.Context, contract.TurnSubmission) error {
			return nil
		},
	})

	_, err := handler(context.Background(), json.RawMessage(`{
		"agent_id": "agent-b",
		"message": "请补充证据",
		"wait_report": true,
		"timeout_ms": 50
	}`))
	if err == nil ||
		!strings.Contains(err.Error(), `agent "agent-b"`) ||
		!strings.Contains(err.Error(), "after report_seq 3") {
		t.Fatalf("HandleSendMessage() error = %v, want timeout with agent and seq", err)
	}
}

func TestSendMessageSchemaDocumentsWaitReport(t *testing.T) {
	def := mustFindToolDefinition(t, orchestrationToolDefinitions(ToolPorts{}), "send_message")
	props := def.InputSchema["properties"].(map[string]any)
	waitReport, ok := props["wait_report"].(map[string]any)
	if !ok {
		t.Fatalf("send_message schema properties = %#v, want wait_report", props)
	}
	description, _ := waitReport["description"].(string)
	for _, want := range []string{"idle", "does not interrupt", "queue"} {
		if !strings.Contains(description, want) {
			t.Fatalf("wait_report description = %q, want %q", description, want)
		}
	}
	if _, ok := props["timeout_ms"].(map[string]any); !ok {
		t.Fatalf("send_message schema properties = %#v, want timeout_ms", props)
	}
}

type sendMessageReportStore struct {
	mu     sync.Mutex
	report contract.AgentReportResult
}

func newSendMessageReportStore(report contract.AgentReportResult) *sendMessageReportStore {
	return &sendMessageReportStore{report: report}
}

func (s *sendMessageReportStore) getReport(_ context.Context, agentID string) (contract.AgentReportResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	report := s.report
	report.AgentID = agentID
	return report, nil
}

func (s *sendMessageReportStore) setReport(report contract.AgentReportResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.report = report
}
