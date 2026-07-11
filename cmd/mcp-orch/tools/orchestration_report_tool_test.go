package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/testutil/golden"
)

func TestGetAgentReportHandlerWaitsWhenRequested(t *testing.T) {
	ready := make(chan struct{})
	remembered := false
	handler := HandleGetAgentReport(&golden.OrchestrationStub{
		GetReportFunc: func(_ context.Context, agentID string) (contract.AgentReportResult, error) {
			if agentID != "agent-child" {
				t.Fatalf("GetReport agentID = %q, want agent-child", agentID)
			}
			select {
			case <-ready:
				return contract.AgentReportResult{AgentID: agentID, Report: "done", State: "idle"}, nil
			default:
				return contract.AgentReportResult{AgentID: agentID, State: "turn_running"}, nil
			}
		},
		RememberReportRequestFunc: func(_ context.Context, req contract.RememberReportRequest) (contract.RememberReportRequestResult, error) {
			remembered = true
			if req.AgentID != "agent-child" || req.RequesterID != "agent-parent" {
				t.Fatalf("RememberReportRequest = %#v, want child/parent", req)
			}
			return contract.RememberReportRequestResult{Success: true, AgentID: req.AgentID, RequesterID: req.RequesterID}, nil
		},
	})
	time.AfterFunc(75*time.Millisecond, func() { close(ready) })

	started := time.Now()
	result, err := handler(context.Background(), json.RawMessage(`{"agent_id":"agent-child","wait":true,"requester_id":"agent-parent","timeout_ms":500}`))
	if err != nil {
		t.Fatalf("HandleGetAgentReport() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed < 50*time.Millisecond {
		t.Fatalf("HandleGetAgentReport() returned after %s, want it to block for report", elapsed)
	}
	got, ok := result.(contract.AgentReportResult)
	if !ok {
		t.Fatalf("HandleGetAgentReport() result type = %T, want AgentReportResult", result)
	}
	if got.Report != "done" {
		t.Fatalf("HandleGetAgentReport().Report = %q, want done", got.Report)
	}
	if !remembered {
		t.Fatalf("RememberReportRequest was not called")
	}
}

func TestGetAgentReportHandlerWaitsForReportAfterSeq(t *testing.T) {
	ready := make(chan struct{})
	handler := HandleGetAgentReport(&golden.OrchestrationStub{
		GetReportFunc: func(_ context.Context, agentID string) (contract.AgentReportResult, error) {
			select {
			case <-ready:
				return contract.AgentReportResult{AgentID: agentID, Report: "new", State: "idle", ReportSeq: 4}, nil
			default:
				return contract.AgentReportResult{AgentID: agentID, Report: "old", State: "idle", ReportSeq: 3}, nil
			}
		},
	})
	time.AfterFunc(75*time.Millisecond, func() { close(ready) })

	started := time.Now()
	result, err := handler(context.Background(), json.RawMessage(`{"agent_id":"agent-child","wait":true,"after_report_seq":3,"timeout_ms":500}`))
	if err != nil {
		t.Fatalf("HandleGetAgentReport() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed < 50*time.Millisecond {
		t.Fatalf("HandleGetAgentReport() returned after %s, want it to wait for a newer report", elapsed)
	}
	got := result.(contract.AgentReportResult)
	if got.Report != "new" || got.ReportSeq != 4 {
		t.Fatalf("HandleGetAgentReport() = %#v, want new report seq 4", got)
	}
}

func TestGetAgentReportHandlerWaitWithoutAfterSeqKeepsOldBehavior(t *testing.T) {
	handler := HandleGetAgentReport(&golden.OrchestrationStub{
		GetReportFunc: func(_ context.Context, agentID string) (contract.AgentReportResult, error) {
			return contract.AgentReportResult{AgentID: agentID, Report: "old", State: "idle", ReportSeq: 3}, nil
		},
	})

	started := time.Now()
	result, err := handler(context.Background(), json.RawMessage(`{"agent_id":"agent-child","wait":true,"timeout_ms":500}`))
	if err != nil {
		t.Fatalf("HandleGetAgentReport() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("HandleGetAgentReport() elapsed = %s, want immediate old behavior", elapsed)
	}
	got := result.(contract.AgentReportResult)
	if got.Report != "old" || got.ReportSeq != 3 {
		t.Fatalf("HandleGetAgentReport() = %#v, want current report", got)
	}
}

func TestGetAgentReportHandlerTimeoutMentionsAfterSeq(t *testing.T) {
	handler := HandleGetAgentReport(&golden.OrchestrationStub{
		GetReportFunc: func(_ context.Context, agentID string) (contract.AgentReportResult, error) {
			return contract.AgentReportResult{AgentID: agentID, Report: "old", State: "idle", ReportSeq: 3}, nil
		},
	})

	_, err := handler(context.Background(), json.RawMessage(`{"agent_id":"agent-child","wait":true,"after_report_seq":3,"timeout_ms":50}`))
	if err == nil ||
		!strings.Contains(err.Error(), `timed out waiting 50ms for report from agent "agent-child"`) ||
		!strings.Contains(err.Error(), "after report_seq 3") {
		t.Fatalf("HandleGetAgentReport() error = %v, want timeout with agent and seq", err)
	}
}

func TestGetAgentReportHandlerDefaultsToImmediateSnapshot(t *testing.T) {
	calls := 0
	handler := HandleGetAgentReport(&golden.OrchestrationStub{
		GetReportFunc: func(_ context.Context, agentID string) (contract.AgentReportResult, error) {
			calls++
			return contract.AgentReportResult{AgentID: agentID, State: "turn_running"}, nil
		},
	})

	result, err := handler(context.Background(), json.RawMessage(`{"agent_id":"agent-child","timeout_ms":20}`))
	if err != nil {
		t.Fatalf("HandleGetAgentReport() error = %v", err)
	}
	got, ok := result.(contract.AgentReportResult)
	if !ok {
		t.Fatalf("HandleGetAgentReport() result type = %T, want AgentReportResult", result)
	}
	if calls != 1 || got.Report != "" || got.State != "turn_running" {
		t.Fatalf("HandleGetAgentReport() calls=%d result=%#v, want one immediate empty snapshot", calls, got)
	}
}

func TestGetAgentReportHandlerWaitFalseReturnsImmediateSnapshot(t *testing.T) {
	calls := 0
	handler := HandleGetAgentReport(&golden.OrchestrationStub{
		GetReportFunc: func(_ context.Context, agentID string) (contract.AgentReportResult, error) {
			calls++
			return contract.AgentReportResult{AgentID: agentID, State: "turn_running"}, nil
		},
	})

	result, err := handler(context.Background(), json.RawMessage(`{"agent_id":"agent-child","wait":false}`))
	if err != nil {
		t.Fatalf("HandleGetAgentReport() error = %v", err)
	}
	got, ok := result.(contract.AgentReportResult)
	if !ok {
		t.Fatalf("HandleGetAgentReport() result type = %T, want AgentReportResult", result)
	}
	if calls != 1 || got.Report != "" || got.State != "turn_running" {
		t.Fatalf("HandleGetAgentReport() calls=%d result=%#v, want one immediate empty snapshot", calls, got)
	}
}

func TestGetAgentReportHandlerWaitsThroughTransientAgentNotFound(t *testing.T) {
	calls := 0
	handler := HandleGetAgentReport(&golden.OrchestrationStub{
		GetReportFunc: func(_ context.Context, agentID string) (contract.AgentReportResult, error) {
			calls++
			if calls < 3 {
				return contract.AgentReportResult{}, fmt.Errorf("transient lookup: %w", contract.ErrAgentNotFound)
			}
			return contract.AgentReportResult{AgentID: agentID, Report: "eventual report", State: "idle"}, nil
		},
	})

	result, err := handler(context.Background(), json.RawMessage(`{"agent_id":"agent-child","wait":true,"timeout_ms":500}`))
	if err != nil {
		t.Fatalf("HandleGetAgentReport() error = %v", err)
	}
	got := result.(contract.AgentReportResult)
	if calls != 3 || got.Report != "eventual report" {
		t.Fatalf("HandleGetAgentReport() calls=%d result=%#v, want wait through not-found then report", calls, got)
	}
}

func TestGetAgentReportHandlerWaitsThroughTransientRememberNotFound(t *testing.T) {
	calls := 0
	handler := HandleGetAgentReport(&golden.OrchestrationStub{
		RememberReportRequestFunc: func(context.Context, contract.RememberReportRequest) (contract.RememberReportRequestResult, error) {
			return contract.RememberReportRequestResult{}, fmt.Errorf("remember before launch: %w", contract.ErrAgentNotFound)
		},
		GetReportFunc: func(_ context.Context, agentID string) (contract.AgentReportResult, error) {
			calls++
			if calls < 2 {
				return contract.AgentReportResult{}, fmt.Errorf("lookup before launch: %w", contract.ErrAgentNotFound)
			}
			return contract.AgentReportResult{AgentID: agentID, Report: "eventual report", State: "idle"}, nil
		},
	})

	result, err := handler(context.Background(), json.RawMessage(`{"agent_id":"agent-child","wait":true,"requester_id":"agent-parent","timeout_ms":500}`))
	if err != nil {
		t.Fatalf("HandleGetAgentReport() error = %v", err)
	}
	got := result.(contract.AgentReportResult)
	if calls != 2 || got.Report != "eventual report" {
		t.Fatalf("HandleGetAgentReport() calls=%d result=%#v, want wait through remember not-found", calls, got)
	}
}

func TestGetAgentReportHandlerReturnsTerminalEmptyReportFallback(t *testing.T) {
	calls := 0
	handler := HandleGetAgentReport(&golden.OrchestrationStub{
		GetReportFunc: func(_ context.Context, agentID string) (contract.AgentReportResult, error) {
			calls++
			return contract.AgentReportResult{AgentID: agentID, State: "failed"}, nil
		},
	})

	result, err := handler(context.Background(), json.RawMessage(`{"agent_id":"agent-child","wait":true,"timeout_ms":20}`))
	if err != nil {
		t.Fatalf("HandleGetAgentReport() error = %v", err)
	}
	got := result.(contract.AgentReportResult)
	if calls != 1 {
		t.Fatalf("HandleGetAgentReport() calls=%d, want one terminal poll", calls)
	}
	if got.Report == "" || !strings.Contains(got.Report, "without producing a turn report") {
		t.Fatalf("HandleGetAgentReport().Report = %q, want no-report fallback", got.Report)
	}
	if got.State != "failed" {
		t.Fatalf("HandleGetAgentReport().State = %q, want failed", got.State)
	}
}

func TestGetAgentReportHandlerAppliesWaitTimeoutToGetReportContext(t *testing.T) {
	called := make(chan struct{})
	handler := HandleGetAgentReport(&golden.OrchestrationStub{
		GetReportFunc: func(ctx context.Context, agentID string) (contract.AgentReportResult, error) {
			select {
			case <-called:
			default:
				close(called)
			}
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatalf("GetReport context has no deadline")
			}
			if until := time.Until(deadline); until > 250*time.Millisecond {
				t.Fatalf("GetReport deadline in %s, want handler wait timeout", until)
			}
			<-ctx.Done()
			return contract.AgentReportResult{AgentID: agentID}, ctx.Err()
		},
	})

	started := time.Now()
	_, err := handler(context.Background(), json.RawMessage(`{"agent_id":"agent-child","wait":true,"timeout_ms":50}`))
	if err == nil || !strings.Contains(err.Error(), `timed out waiting 50ms for report from agent "agent-child"`) {
		t.Fatalf("HandleGetAgentReport() error = %v, want report wait timeout", err)
	}
	if elapsed := time.Since(started); elapsed > 300*time.Millisecond {
		t.Fatalf("HandleGetAgentReport() elapsed = %s, want timeout applied to GetReport", elapsed)
	}
	select {
	case <-called:
	default:
		t.Fatalf("GetReport was not called")
	}
}

func TestGetAgentReportHandlerExplicitWaitDefaultUsesRPCRequestTimeout(t *testing.T) {
	var until time.Duration
	handler := HandleGetAgentReport(&golden.OrchestrationStub{
		GetReportFunc: func(ctx context.Context, agentID string) (contract.AgentReportResult, error) {
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatalf("GetReport context has no deadline")
			}
			until = time.Until(deadline)
			return contract.AgentReportResult{AgentID: agentID, State: "failed"}, nil
		},
	})

	result, err := handler(context.Background(), json.RawMessage(`{"agent_id":"agent-child","wait":true}`))
	if err != nil {
		t.Fatalf("HandleGetAgentReport() error = %v", err)
	}
	got := result.(contract.AgentReportResult)
	if !strings.Contains(got.Report, "without producing a turn report") {
		t.Fatalf("HandleGetAgentReport().Report = %q, want fallback", got.Report)
	}
	if until > platformconfig.RPCRequestTimeout+time.Second {
		t.Fatalf("GetReport deadline in %s, want RPC request timeout %s", until, platformconfig.RPCRequestTimeout)
	}
}

func TestGetAgentReportHandlerReturnsTerminalEmptyReportWithoutWaiting(t *testing.T) {
	handler := HandleGetAgentReport(&golden.OrchestrationStub{
		GetReportFunc: func(_ context.Context, agentID string) (contract.AgentReportResult, error) {
			return contract.AgentReportResult{AgentID: agentID, State: "failed"}, nil
		},
	})

	started := time.Now()
	result, err := handler(context.Background(), json.RawMessage(`{"agent_id":"agent-child","wait":true,"timeout_ms":500}`))
	if err != nil {
		t.Fatalf("HandleGetAgentReport() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("HandleGetAgentReport() elapsed = %s, want immediate terminal fallback", elapsed)
	}
	got, ok := result.(contract.AgentReportResult)
	if !ok {
		t.Fatalf("HandleGetAgentReport() result type = %T, want AgentReportResult", result)
	}
	if got.State != "failed" || !strings.Contains(got.Report, "without producing a turn report") {
		t.Fatalf("HandleGetAgentReport() = %#v, want failed fallback report", got)
	}
}
